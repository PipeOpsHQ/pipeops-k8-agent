package agent

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/pipeops/pipeops-vm-agent/internal/server"
	"github.com/pipeops/pipeops-vm-agent/internal/tunnel"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Agent represents the main PipeOps agent
// Note: With Portainer-style tunneling, K8s access happens directly through
// the tunnel, so we don't need a K8s client in the agent.
type Agent struct {
	config       *types.Config
	logger       *logrus.Logger
	server       *server.Server
	controlPlane *controlplane.Client
	tunnelMgr    *tunnel.Manager
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// New creates a new agent instance
func New(config *types.Config, logger *logrus.Logger) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	agent := &Agent{
		config: config,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}

	// Note: With Portainer-style tunneling, we don't need a K8s client in the agent.
	// Control plane accesses Kubernetes directly through the forwarded port (6443).

	// Initialize control plane client
	if config.PipeOps.APIURL != "" && config.PipeOps.Token != "" {
		controlPlaneClient, err := controlplane.NewClient(
			config.PipeOps.APIURL,
			config.PipeOps.Token,
			config.Agent.ID,
			logger,
		)
		if err != nil {
			logger.WithError(err).Warn("Failed to create control plane client")
		} else {
			agent.controlPlane = controlPlaneClient
			logger.Info("Control plane client initialized")
		}
	} else {
		logger.Warn("Control plane not configured - agent will run in standalone mode")
	}

	// Initialize HTTP server (simplified - no K8s proxy needed)
	httpServer := server.NewServer(config, logger)
	agent.server = httpServer

	// Set activity recorder for tunnel (will be used if tunnel is enabled)
	httpServer.SetActivityRecorder(func() {
		agent.RecordTunnelActivity()
	})

	// Initialize tunnel manager (if enabled)
	if config.Tunnel != nil && config.Tunnel.Enabled {
		// Convert tunnel forwards config
		var forwards []tunnel.PortForwardConfig
		for _, fwd := range config.Tunnel.Forwards {
			forwards = append(forwards, tunnel.PortForwardConfig{
				Name:      fwd.Name,
				LocalAddr: fwd.LocalAddr,
			})
		}

		tunnelConfig := &tunnel.ManagerConfig{
			ControlPlaneURL:   config.PipeOps.APIURL,
			AgentID:           config.Agent.ID,
			Token:             config.PipeOps.Token,
			PollInterval:      config.Tunnel.PollInterval.String(),
			InactivityTimeout: config.Tunnel.InactivityTimeout.String(),
			Forwards:          forwards,
		}

		tunnelMgr, err := tunnel.NewManager(tunnelConfig, logger)
		if err != nil {
			logger.WithError(err).Warn("Failed to create tunnel manager")
		} else {
			agent.tunnelMgr = tunnelMgr
			logger.WithField("forwards", len(forwards)).Info("Tunnel manager initialized with port forwards")
		}
	} else {
		logger.Info("Tunnel disabled - agent will not establish reverse tunnels")
	}

	return agent, nil
}

// Start starts the agent
func (a *Agent) Start() error {
	a.logger.Info("Starting PipeOps agent...")

	// Start HTTP server with K8s API proxy
	if err := a.server.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// FRP client and authentication removed - agent now uses custom real-time architecture

	// Start tunnel manager (if initialized)
	if a.tunnelMgr != nil {
		if err := a.tunnelMgr.Start(); err != nil {
			a.logger.WithError(err).Warn("Failed to start tunnel manager")
		} else {
			a.logger.Info("Tunnel manager started")
		}
	}

	// Register agent with control plane via HTTP (if configured)
	if err := a.register(); err != nil {
		a.logger.WithError(err).Warn("Failed to register agent with control plane")
	}

	// Note: Status reporting removed - with Portainer-style tunneling,
	// the control plane accesses K8s directly. Only heartbeat is needed.

	// Start heartbeat
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.startHeartbeat()
	}()

	a.logger.WithField("port", a.config.Agent.Port).Info("PipeOps agent started successfully")

	// Wait for context cancellation
	<-a.ctx.Done()

	return nil
}

// Stop stops the agent
func (a *Agent) Stop() error {
	a.logger.Info("Stopping PipeOps agent...")

	a.cancel()

	// Stop tunnel manager
	if a.tunnelMgr != nil {
		if err := a.tunnelMgr.Stop(); err != nil {
			a.logger.WithError(err).Error("Failed to stop tunnel manager")
		} else {
			a.logger.Info("Tunnel manager stopped")
		}
	}

	// Stop HTTP server
	if a.server != nil {
		if err := a.server.Stop(); err != nil {
			a.logger.WithError(err).Error("Failed to stop HTTP server")
		}
	}

	// FRP client removed - agent now uses custom real-time architecture

	// Wait for all goroutines to finish
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	// Wait for graceful shutdown or timeout
	select {
	case <-done:
		a.logger.Info("PipeOps agent stopped gracefully")
	case <-time.After(30 * time.Second):
		a.logger.Warn("Graceful shutdown timeout, forcing stop")
	}

	return nil
}

// register registers the agent with the control plane via HTTP
func (a *Agent) register() error {
	// Skip registration if control plane client not configured
	if a.controlPlane == nil {
		a.logger.Info("Skipping registration - running in standalone mode")
		return nil
	}

	hostname, _ := os.Hostname()

	agent := &types.Agent{
		ID:          a.config.Agent.ID,
		Name:        a.config.Agent.Name,
		ClusterName: a.config.Agent.ClusterName,
		Version:     version.GetVersion(),
		Labels:      a.config.Agent.Labels,
		Status:      types.AgentStatusRegistering,
		LastSeen:    time.Now(),
	}

	// Add default labels
	if agent.Labels == nil {
		agent.Labels = make(map[string]string)
	}
	agent.Labels["hostname"] = hostname
	agent.Labels["agent.pipeops.io/version"] = agent.Version

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	if err := a.controlPlane.RegisterAgent(ctx, agent); err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}

	a.logger.WithFields(map[string]interface{}{
		"agent_id":     agent.ID,
		"cluster_name": agent.ClusterName,
		"version":      agent.Version,
	}).Info("Agent registered successfully with control plane")

	return nil
}

// startHeartbeat starts periodic heartbeat
func (a *Agent) startHeartbeat() {
	ticker := time.NewTicker(30 * time.Second) // Heartbeat every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(); err != nil {
				a.logger.WithError(err).Error("Failed to send heartbeat")
			}
		}
	}
}

// sendHeartbeat sends a heartbeat message directly
// Note: With Portainer-style tunneling, we only send heartbeats to indicate the agent is alive.
// The control plane accesses K8s metrics directly through the tunnel (port 6443).
func (a *Agent) sendHeartbeat() error {
	// Skip heartbeat if control plane client not configured
	if a.controlPlane == nil {
		a.logger.Debug("Skipping heartbeat - running in standalone mode")
		return nil
	}

	heartbeat := &controlplane.HeartbeatRequest{
		AgentID:     a.config.Agent.ID,
		ClusterName: a.config.Agent.ClusterName,
		Status:      string(types.AgentStatusConnected),
		ProxyStatus: "direct",
		Timestamp:   time.Now(),
		Metadata: map[string]interface{}{
			"version": version.GetVersion(),
		},
	}

	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	if err := a.controlPlane.SendHeartbeat(ctx, heartbeat); err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}

	a.logger.WithFields(map[string]interface{}{
		"agent_id":     a.config.Agent.ID,
		"cluster_name": a.config.Agent.ClusterName,
	}).Debug("Heartbeat sent successfully")

	return nil
}

// RecordTunnelActivity records tunnel activity (called by server middleware)
func (a *Agent) RecordTunnelActivity() {
	if a.tunnelMgr != nil {
		a.tunnelMgr.RecordActivity()
	}
}
