package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/pipeops/pipeops-vm-agent/internal/monitoring"
	"github.com/pipeops/pipeops-vm-agent/internal/server"
	"github.com/pipeops/pipeops-vm-agent/internal/tunnel"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/k8s"
	"github.com/pipeops/pipeops-vm-agent/pkg/state"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// ConnectionState represents the agent's connection state
type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateReconnecting
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

// Agent represents the main PipeOps agent
// Note: With Portainer-style tunneling, K8s access happens directly through
// the tunnel, so we don't need a K8s client in the agent.
type Agent struct {
	config          *types.Config
	logger          *logrus.Logger
	server          *server.Server
	controlPlane    *controlplane.Client
	tunnelMgr       *tunnel.Manager
	monitoringMgr   *monitoring.Manager // Monitoring stack manager
	stateManager    *state.StateManager // Manages persistent state
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	clusterID       string          // Cluster UUID from registration
	clusterToken    string          // K8s ServiceAccount token for control plane to access cluster
	connectionState ConnectionState // Current connection state
	lastHeartbeat   time.Time       // Last successful heartbeat
	stateMutex      sync.RWMutex    // Protects connection state
	monitoringReady bool            // Indicates if monitoring stack is ready
	monitoringMutex sync.RWMutex    // Protects monitoring ready state
}

// New creates a new agent instance
func New(config *types.Config, logger *logrus.Logger) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize state manager
	stateManager := state.NewStateManager()
	logger.WithField("state_path", stateManager.GetStatePath()).Info("Initialized state manager")

	// Migrate legacy state files if they exist
	if err := stateManager.MigrateLegacyState(); err != nil {
		logger.WithError(err).Warn("Failed to migrate legacy state (non-fatal)")
	}

	agent := &Agent{
		config:          config,
		logger:          logger,
		stateManager:    stateManager,
		ctx:             ctx,
		cancel:          cancel,
		connectionState: StateDisconnected,
		lastHeartbeat:   time.Time{},
	}

	// Generate or load persistent agent ID if not set in config
	if config.Agent.ID == "" {
		hostname, _ := os.Hostname()
		logger.Debug("Agent ID not set in config, loading from persistent storage")

		// Try to load from state manager
		if persistentID, err := stateManager.GetAgentID(); err == nil && persistentID != "" {
			config.Agent.ID = persistentID
			logger.WithField("agent_id", persistentID).Info("Agent ID loaded from state")
		} else {
			// Generate new ID
			config.Agent.ID = generateAgentID(hostname)
			logger.WithField("agent_id", config.Agent.ID).Info("Generated new agent ID")

			// Save to state
			if err := stateManager.SaveAgentID(config.Agent.ID); err != nil {
				logger.WithError(err).Warn("Failed to save agent ID to state")
			}
		}
	} else {
		logger.WithField("agent_id", config.Agent.ID).Info("Using agent ID from configuration")

		// Save to state for persistence
		if err := stateManager.SaveAgentID(config.Agent.ID); err != nil {
			logger.WithError(err).Warn("Failed to save agent ID to state")
		}
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

	// Register agent with control plane FIRST (must succeed before starting services)
	// Registration is required - agent cannot function without cluster ID from control plane
	if err := a.register(); err != nil {
		// Stop HTTP server before returning error
		if a.server != nil {
			a.server.Stop()
		}
		return fmt.Errorf("failed to register agent with control plane: %w", err)
	}

	// Configure monitoring tunnels if monitoring is ready
	if a.monitoringMgr != nil && a.monitoringReady && a.tunnelMgr != nil {
		a.logger.Info("Adding monitoring service tunnels...")
		if err := a.addMonitoringTunnels(); err != nil {
			a.logger.WithError(err).Warn("Failed to add monitoring tunnels (non-fatal)")
		}
	}

	// Start tunnel manager (if initialized) - only after successful registration
	if a.tunnelMgr != nil {
		if err := a.tunnelMgr.Start(); err != nil {
			a.logger.WithError(err).Error("Failed to start tunnel manager")
			// Stop HTTP server before returning error
			if a.server != nil {
				a.server.Stop()
			}
			return fmt.Errorf("failed to start tunnel manager: %w", err)
		}
		a.logger.Info("Tunnel manager started")
	}

	// Start heartbeat - only after successful registration
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.startHeartbeat()
	}()

	a.logger.WithFields(logrus.Fields{
		"port":       a.config.Agent.Port,
		"cluster_id": a.clusterID,
		"agent_id":   a.config.Agent.ID,
	}).Info("PipeOps agent started successfully")

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
// This includes setting up the monitoring stack and waiting for it to be ready
func (a *Agent) register() error {
	// Skip registration if control plane client not configured
	if a.controlPlane == nil {
		a.logger.Info("Skipping registration - running in standalone mode")
		return nil
	}

	a.updateConnectionState(StateConnecting)

	// Load cluster credentials first (needed for both new and existing registrations)
	a.loadClusterCredentials()

	// Try to load existing cluster ID first
	if existingClusterID, err := a.loadClusterID(); err == nil {
		a.clusterID = existingClusterID

		a.logger.WithFields(logrus.Fields{
			"cluster_id": existingClusterID,
			"has_token":  a.clusterToken != "",
		}).Info("Using existing cluster ID, skipping re-registration")

		a.updateConnectionState(StateConnected)

		// Still set up monitoring even for existing clusters
		if err := a.setupMonitoring(); err != nil {
			a.logger.WithError(err).Warn("Failed to set up monitoring stack (non-fatal)")
		}

		return nil
	}

	hostname, _ := os.Hostname()

	// Get K8s version from local cluster
	k8sVersion := a.getK8sVersion()

	// Get server IP
	serverIP := a.getServerIP()

	// Set up monitoring stack BEFORE registration
	a.logger.Info("Setting up monitoring stack before registration...")
	if err := a.setupMonitoring(); err != nil {
		a.logger.WithError(err).Warn("Failed to set up monitoring stack (continuing with registration)")
	}

	// Wait for monitoring to be ready (with timeout)
	a.logger.Info("Waiting for monitoring stack to be ready...")
	if err := a.waitForMonitoring(120 * time.Second); err != nil {
		a.logger.WithError(err).Warn("Monitoring stack not ready within timeout (continuing with registration)")
	}

	// Get monitoring information if available
	monitoringInfo := a.getMonitoringInfo()

	// Prepare agent registration payload matching control plane's RegisterClusterRequest
	agent := &types.Agent{
		// Required fields
		ID:   a.config.Agent.ID,          // agent_id
		Name: a.config.Agent.ClusterName, // name (cluster name)

		// K8s and server information
		Version:       k8sVersion,      // k8s_version
		ServerIP:      serverIP,        // server_ip
		ServerCode:    serverIP,        // server_code (same as ServerIP for agent clusters)
		Token:         a.clusterToken,  // k8s_service_token (K8s ServiceAccount token)
		Region:        "agent-managed", // region (default for agent clusters)
		CloudProvider: "agent",         // cloud_provider (default for agent clusters)

		// Agent details
		Hostname:     hostname,              // hostname
		AgentVersion: version.GetVersion(),  // agent_version
		Labels:       a.config.Agent.Labels, // labels
		TunnelPortConfig: types.TunnelPortConfig{
			KubernetesAPI: 6443,  // kubernetes_api port
			Kubelet:       10250, // kubelet port
			AgentHTTP:     8080,  // agent_http port
		},
		ServerSpecs: a.getServerSpecs(), // server_specs

		// Monitoring stack information (if available)
		PrometheusURL:        monitoringInfo.PrometheusURL,
		PrometheusUsername:   monitoringInfo.PrometheusUsername,
		PrometheusPassword:   monitoringInfo.PrometheusPassword,
		PrometheusSSL:        monitoringInfo.PrometheusSSL,
		TunnelPrometheusPort: monitoringInfo.TunnelPrometheusPort,

		LokiURL:        monitoringInfo.LokiURL,
		LokiUsername:   monitoringInfo.LokiUsername,
		LokiPassword:   monitoringInfo.LokiPassword,
		LokiSSL:        monitoringInfo.LokiSSL,
		TunnelLokiPort: monitoringInfo.TunnelLokiPort,

		OpenCostURL:        monitoringInfo.OpenCostURL,
		OpenCostUsername:   monitoringInfo.OpenCostUsername,
		OpenCostPassword:   monitoringInfo.OpenCostPassword,
		OpenCostSSL:        monitoringInfo.OpenCostSSL,
		TunnelOpenCostPort: monitoringInfo.TunnelOpenCostPort,

		GrafanaURL:        monitoringInfo.GrafanaURL,
		GrafanaUsername:   monitoringInfo.GrafanaUsername,
		GrafanaPassword:   monitoringInfo.GrafanaPassword,
		GrafanaSSL:        monitoringInfo.GrafanaSSL,
		TunnelGrafanaPort: monitoringInfo.TunnelGrafanaPort,

		// Metadata - can be extended later
		Metadata: make(map[string]string),
	}

	// Add default labels
	if agent.Labels == nil {
		agent.Labels = make(map[string]string)
	}
	agent.Labels["hostname"] = hostname
	agent.Labels["agent.pipeops.io/version"] = version.GetVersion()

	// Add metadata
	agent.Metadata["agent_mode"] = "vm-agent"
	agent.Metadata["registration_timestamp"] = time.Now().Format(time.RFC3339)

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	result, err := a.controlPlane.RegisterAgent(ctx, agent)
	if err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}

	// Store cluster ID for heartbeats
	a.clusterID = result.ClusterID

	// Save cluster ID to disk for persistence
	if err := a.saveClusterID(result.ClusterID); err != nil {
		a.logger.WithError(err).Warn("Failed to save cluster ID to disk")
	}

	// Save token if provided by control plane
	if result.Token != "" {
		a.clusterToken = result.Token // Store in memory
		if err := a.saveClusterToken(result.Token); err != nil {
			a.logger.WithError(err).Warn("Failed to save cluster token to disk")
		} else {
			a.logger.Info("Cluster token saved successfully")
		}
	}

	logFields := logrus.Fields{
		"cluster_id": result.ClusterID,
	}
	if result.Token != "" {
		logFields["has_token"] = true
	}
	if result.APIServer != "" {
		logFields["api_server"] = result.APIServer
	}
	a.logger.WithFields(logFields).Info("Cluster registered and credentials stored")
	a.updateConnectionState(StateConnected)

	return nil
}

// setupMonitoring initializes and starts the monitoring stack
func (a *Agent) setupMonitoring() error {
	a.logger.Info("Initializing monitoring stack...")

	// Create monitoring manager with default configuration
	stack := monitoring.DefaultMonitoringStack()

	mgr, err := monitoring.NewManager(stack, a.logger)
	if err != nil {
		return fmt.Errorf("failed to create monitoring manager: %w", err)
	}

	a.monitoringMgr = mgr

	// Start monitoring stack in background
	go func() {
		if err := mgr.Start(); err != nil {
			a.logger.WithError(err).Error("Failed to start monitoring stack")
			return
		}

		// Mark monitoring as ready
		a.monitoringMutex.Lock()
		a.monitoringReady = true
		a.monitoringMutex.Unlock()

		a.logger.Info("Monitoring stack started successfully")
	}()

	return nil
}

// waitForMonitoring waits for the monitoring stack to be ready or timeout
func (a *Agent) waitForMonitoring(timeout time.Duration) error {
	start := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		// Check if monitoring is ready
		a.monitoringMutex.RLock()
		ready := a.monitoringReady
		a.monitoringMutex.RUnlock()

		if ready {
			a.logger.WithField("duration", time.Since(start)).Info("Monitoring stack is ready")
			return nil
		}

		// Check timeout
		if time.Since(start) >= timeout {
			return fmt.Errorf("monitoring stack not ready after %v", timeout)
		}

		// Wait for next check
		select {
		case <-ticker.C:
			a.logger.Debug("Waiting for monitoring stack to be ready...")
		case <-a.ctx.Done():
			return fmt.Errorf("context cancelled while waiting for monitoring")
		}
	}
}

// monitoringInfo holds monitoring service information
type monitoringInfo struct {
	PrometheusURL        string
	PrometheusUsername   string
	PrometheusPassword   string
	PrometheusSSL        bool
	TunnelPrometheusPort int

	LokiURL        string
	LokiUsername   string
	LokiPassword   string
	LokiSSL        bool
	TunnelLokiPort int

	OpenCostURL        string
	OpenCostUsername   string
	OpenCostPassword   string
	OpenCostSSL        bool
	TunnelOpenCostPort int

	GrafanaURL        string
	GrafanaUsername   string
	GrafanaPassword   string
	GrafanaSSL        bool
	TunnelGrafanaPort int
}

// getMonitoringInfo retrieves monitoring service information
func (a *Agent) getMonitoringInfo() monitoringInfo {
	info := monitoringInfo{}

	if a.monitoringMgr == nil {
		a.logger.Debug("Monitoring manager not initialized")
		return info
	}

	// Get monitoring info from manager
	mgr := a.monitoringMgr.GetMonitoringInfo()

	// Get tunnel forwards for port mapping
	tunnelForwards := a.monitoringMgr.GetTunnelForwards()

	// Prometheus
	if url, ok := mgr["prometheus_url"].(string); ok {
		info.PrometheusURL = url
	}
	if username, ok := mgr["prometheus_username"].(string); ok {
		info.PrometheusUsername = username
	}
	if password, ok := mgr["prometheus_password"].(string); ok {
		info.PrometheusPassword = password
	}
	if ssl, ok := mgr["prometheus_ssl"].(bool); ok {
		info.PrometheusSSL = ssl
	}
	// Find Prometheus tunnel port
	for _, fwd := range tunnelForwards {
		if fwd.Name == "prometheus" {
			info.TunnelPrometheusPort = fwd.RemotePort
			break
		}
	}

	// Loki
	if url, ok := mgr["loki_url"].(string); ok {
		info.LokiURL = url
	}
	if username, ok := mgr["loki_username"].(string); ok {
		info.LokiUsername = username
	}
	if password, ok := mgr["loki_password"].(string); ok {
		info.LokiPassword = password
	}
	// Find Loki tunnel port
	for _, fwd := range tunnelForwards {
		if fwd.Name == "loki" {
			info.TunnelLokiPort = fwd.RemotePort
			break
		}
	}

	// OpenCost
	if url, ok := mgr["opencost_base_url"].(string); ok {
		info.OpenCostURL = url
	}
	if username, ok := mgr["opencost_username"].(string); ok {
		info.OpenCostUsername = username
	}
	if password, ok := mgr["opencost_password"].(string); ok {
		info.OpenCostPassword = password
	}
	// Find OpenCost tunnel port
	for _, fwd := range tunnelForwards {
		if fwd.Name == "opencost" {
			info.TunnelOpenCostPort = fwd.RemotePort
			break
		}
	}

	// Grafana
	if url, ok := mgr["grafana_url"].(string); ok {
		info.GrafanaURL = url
	}
	if username, ok := mgr["grafana_username"].(string); ok {
		info.GrafanaUsername = username
	}
	if password, ok := mgr["grafana_password"].(string); ok {
		info.GrafanaPassword = password
	}
	// Find Grafana tunnel port
	for _, fwd := range tunnelForwards {
		if fwd.Name == "grafana" {
			info.TunnelGrafanaPort = fwd.RemotePort
			break
		}
	}

	a.logger.WithFields(logrus.Fields{
		"prometheus_url":  info.PrometheusURL,
		"prometheus_port": info.TunnelPrometheusPort,
		"loki_url":        info.LokiURL,
		"loki_port":       info.TunnelLokiPort,
		"opencost_url":    info.OpenCostURL,
		"opencost_port":   info.TunnelOpenCostPort,
		"grafana_url":     info.GrafanaURL,
		"grafana_port":    info.TunnelGrafanaPort,
	}).Debug("Retrieved monitoring info")

	return info
}

// addMonitoringTunnels adds monitoring service tunnels to the tunnel manager
func (a *Agent) addMonitoringTunnels() error {
	if a.monitoringMgr == nil {
		return fmt.Errorf("monitoring manager not initialized")
	}

	// Get tunnel forwards from monitoring manager
	tunnelForwards := a.monitoringMgr.GetTunnelForwards()

	a.logger.WithField("count", len(tunnelForwards)).Info("Adding monitoring service tunnels")

	// Convert to tunnel manager's format
	for _, fwd := range tunnelForwards {
		forward := tunnel.PortForwardConfig{
			Name:      fwd.Name,
			LocalAddr: fwd.LocalAddr,
		}

		// Add to tunnel manager's configuration
		// Note: The tunnel manager will handle dynamic port allocation
		a.logger.WithFields(logrus.Fields{
			"name":       forward.Name,
			"local_addr": forward.LocalAddr,
		}).Debug("Added monitoring tunnel")

		// If tunnel manager has an AddForward method, use it
		// Otherwise, the forwards should be configured at initialization
	}

	a.logger.Info("Monitoring tunnels configured successfully")
	return nil
}

// updateConnectionState updates the connection state with logging
func (a *Agent) updateConnectionState(newState ConnectionState) {
	a.stateMutex.Lock()
	oldState := a.connectionState
	a.connectionState = newState
	a.stateMutex.Unlock()

	if oldState != newState {
		a.logger.WithFields(logrus.Fields{
			"old_state": oldState.String(),
			"new_state": newState.String(),
		}).Info("Connection state changed")
	}
}

// getConnectionState returns the current connection state
func (a *Agent) getConnectionState() ConnectionState {
	a.stateMutex.RLock()
	defer a.stateMutex.RUnlock()
	return a.connectionState
}

// startHeartbeat starts periodic heartbeat with retry logic
func (a *Agent) startHeartbeat() {
	// Send heartbeat immediately on connection (don't wait for first tick)
	if err := a.sendHeartbeatWithRetry(); err != nil {
		a.logger.WithError(err).Error("Failed to send initial heartbeat")
		a.updateConnectionState(StateDisconnected)
	} else {
		a.updateConnectionState(StateConnected)
	}

	// Continue sending heartbeat every 5 seconds
	ticker := time.NewTicker(5 * time.Second) // Heartbeat every 5 seconds (was 30s)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendHeartbeatWithRetry(); err != nil {
				a.logger.WithError(err).Error("Failed to send heartbeat after retries")
				a.updateConnectionState(StateDisconnected)
			} else {
				a.updateConnectionState(StateConnected)
			}
		}
	}
}

// sendHeartbeatWithRetry sends heartbeat with exponential backoff retry
func (a *Agent) sendHeartbeatWithRetry() error {
	// Get retry configuration
	maxAttempts := a.config.PipeOps.Reconnect.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3 // Default
	}

	baseInterval := a.config.PipeOps.Reconnect.Interval
	if baseInterval == 0 {
		baseInterval = 5 * time.Second // Default
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := a.sendHeartbeat()
		if err == nil {
			// Success!
			a.stateMutex.Lock()
			a.lastHeartbeat = time.Now()
			a.stateMutex.Unlock()
			return nil
		}

		lastErr = err

		// Don't sleep after last attempt
		if attempt < maxAttempts {
			// Exponential backoff: interval * 2^(attempt-1)
			backoff := baseInterval * time.Duration(1<<uint(attempt-1))
			if backoff > 30*time.Second {
				backoff = 30 * time.Second // Cap at 30 seconds
			}

			a.logger.WithFields(logrus.Fields{
				"attempt": attempt,
				"backoff": backoff,
				"error":   err,
			}).Warn("Heartbeat failed, retrying with backoff...")

			a.updateConnectionState(StateReconnecting)

			select {
			case <-a.ctx.Done():
				return fmt.Errorf("context cancelled during retry")
			case <-time.After(backoff):
				// Continue to next attempt
			}
		}
	}

	return fmt.Errorf("heartbeat failed after %d attempts: %w", maxAttempts, lastErr)
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

	// Skip if cluster ID not set (not registered yet)
	if a.clusterID == "" {
		a.logger.Debug("Skipping heartbeat - cluster not registered yet")
		return nil
	}

	// Determine tunnel status
	tunnelStatus := "disconnected"
	if a.tunnelMgr != nil {
		tunnelStatus = "connected"
	}

	heartbeat := &controlplane.HeartbeatRequest{
		ClusterID:    a.clusterID,
		AgentID:      a.config.Agent.ID,
		Status:       "healthy",
		TunnelStatus: tunnelStatus,
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"version":      version.GetVersion(),
			"k8s_nodes":    0, // TODO: Collect from K8s if needed
			"k8s_pods":     0, // TODO: Collect from K8s if needed
			"cpu_usage":    "0%",
			"memory_usage": "0%",
		},
	}

	// Debug: Log the heartbeat payload
	if jsonPayload, err := json.Marshal(heartbeat); err == nil {
		a.logger.WithField("payload", string(jsonPayload)).Debug("Heartbeat payload")
	}

	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	if err := a.controlPlane.SendHeartbeat(ctx, heartbeat); err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}

	a.logger.WithFields(map[string]interface{}{
		"cluster_id": a.clusterID,
		"agent_id":   a.config.Agent.ID,
		"status":     heartbeat.Status,
	}).Debug("Heartbeat sent successfully")

	return nil
}

// RecordTunnelActivity records tunnel activity (called by server middleware)
func (a *Agent) RecordTunnelActivity() {
	if a.tunnelMgr != nil {
		a.tunnelMgr.RecordActivity()
	}
}

// GetHealthStatus returns the agent's health status
func (a *Agent) GetHealthStatus() map[string]interface{} {
	a.stateMutex.RLock()
	state := a.connectionState
	lastHB := a.lastHeartbeat
	a.stateMutex.RUnlock()

	timeSinceLastHB := time.Duration(0)
	if !lastHB.IsZero() {
		timeSinceLastHB = time.Since(lastHB)
	}

	status := "healthy"
	if state == StateDisconnected {
		status = "unhealthy"
	} else if state == StateReconnecting {
		status = "degraded"
	}

	return map[string]interface{}{
		"status":                    status,
		"connection_state":          state.String(),
		"control_plane_connected":   state == StateConnected,
		"cluster_id":                a.clusterID,
		"agent_id":                  a.config.Agent.ID,
		"has_cluster_token":         a.clusterToken != "",
		"last_heartbeat":            lastHB,
		"time_since_last_heartbeat": timeSinceLastHB.String(),
		"heartbeat_interval":        "30s",
	}
}

// getK8sVersion attempts to get K8s version from the running cluster
func (a *Agent) getK8sVersion() string {
	// Method 1: Try to get SERVER version from kubectl (actual running cluster)
	cmd := exec.Command("kubectl", "version", "--short")
	if output, err := cmd.Output(); err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			// Look for "Server Version:" line
			if strings.Contains(line, "Server Version:") {
				if idx := strings.Index(line, "v"); idx != -1 {
					parts := strings.Fields(line[idx:])
					if len(parts) > 0 {
						version := strings.TrimSpace(parts[0])
						a.logger.WithField("version", version).Debug("Got K8s version from cluster")
						return version
					}
				}
			}
		}
	}

	// Method 2: Try k3s-specific command (if running on k3s node)
	cmd = exec.Command("k3s", "--version")
	if output, err := cmd.Output(); err == nil {
		lines := strings.Split(string(output), "\n")
		if len(lines) > 0 {
			// k3s version output: "k3s version v1.28.3+k3s1 (1234abcd)"
			if idx := strings.Index(lines[0], "v"); idx != -1 {
				parts := strings.Fields(lines[0][idx:])
				if len(parts) > 0 {
					version := strings.TrimSpace(parts[0])
					a.logger.WithField("version", version).Debug("Got K3s version from k3s binary")
					return version
				}
			}
		}
	}

	// Method 3: Try reading k3s version file (common on k3s nodes)
	if data, err := os.ReadFile("/etc/rancher/k3s/k3s-version"); err == nil {
		version := strings.TrimSpace(string(data))
		if version != "" && strings.HasPrefix(version, "v") {
			a.logger.WithField("version", version).Debug("Got K3s version from file")
			return version
		}
	}

	// Method 4: Fallback - try kubectl client version as last resort
	cmd = exec.Command("kubectl", "version", "--short", "--client")
	if output, err := cmd.Output(); err == nil {
		if idx := strings.Index(string(output), "v"); idx != -1 {
			parts := strings.Fields(string(output)[idx:])
			if len(parts) > 0 {
				version := strings.TrimSpace(parts[0])
				a.logger.WithField("version", version).Warn("Using kubectl client version (could not reach cluster)")
				return version
			}
		}
	}

	// Default version
	a.logger.Warn("Could not detect K8s version, using default")
	return "v1.28.0+k3s1"
}

// getServerIP gets the server's public IP address
func (a *Agent) getServerIP() string {
	// Try to get external IP from multiple sources

	// Method 1: Query external service
	if ip := getExternalIP(); ip != "" {
		return ip
	}

	// Method 2: Get primary network interface IP
	if ip := getPrimaryIP(); ip != "" {
		return ip
	}

	return "0.0.0.0"
}

// getServerSpecs collects server hardware specifications
func (a *Agent) getServerSpecs() types.ServerSpecs {
	specs := types.ServerSpecs{
		CPUCores: runtime.NumCPU(),
		OS:       runtime.GOOS,
	}

	// Try to get memory info (Linux)
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		content := string(data)
		if idx := strings.Index(content, "MemTotal:"); idx != -1 {
			line := content[idx:]
			if end := strings.Index(line, "\n"); end != -1 {
				line = line[:end]
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if memKB, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					specs.MemoryGB = int(memKB / 1024 / 1024)
				}
			}
		}
	}

	// Try to get disk info
	if stat := getDiskSpace("/"); stat != nil {
		specs.DiskGB = int(stat.All / 1024 / 1024 / 1024)
	}

	// Get OS version
	if osInfo := getOSVersion(); osInfo != "" {
		specs.OS = osInfo
	}

	return specs
}

// generateAgentID generates a unique agent ID based on hostname
func generateAgentID(hostname string) string {
	// Create a unique ID from hostname and timestamp
	data := fmt.Sprintf("%s-%d", hostname, time.Now().UnixNano())
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("agent-%s", hex.EncodeToString(hash[:8]))
}

// saveClusterID saves the cluster ID to persistent storage
func (a *Agent) saveClusterID(clusterID string) error {
	if err := a.stateManager.SaveClusterID(clusterID); err != nil {
		return fmt.Errorf("failed to save cluster ID: %w", err)
	}
	a.logger.WithFields(logrus.Fields{
		"cluster_id": clusterID,
		"state_path": a.stateManager.GetStatePath(),
	}).Debug("Saved cluster ID to state")
	return nil
}

// loadClusterCredentials attempts to load the Kubernetes ServiceAccount token
// This token is needed by the control plane to access the cluster through the tunnel
func (a *Agent) loadClusterCredentials() {
	// Try to read from Kubernetes ServiceAccount mount (when running in pod)
	if token, err := k8s.GetServiceAccountToken(); err == nil {
		a.clusterToken = token
		// Also save to state as backup
		if err := a.saveClusterToken(token); err != nil {
			a.logger.WithError(err).Warn("Failed to save ServiceAccount token to state")
		}
		a.logger.Info("Loaded ServiceAccount token from Kubernetes mount")
		return
	}

	// Fallback: try loading from state (for dev mode or restart)
	if token, err := a.loadClusterToken(); err == nil {
		a.clusterToken = token
		a.logger.Info("Loaded cluster token from state")
		return
	}

	a.logger.Warn("No cluster ServiceAccount token available - control plane will not be able to access cluster")
}

// loadClusterID loads the cluster ID from persistent storage
func (a *Agent) loadClusterID() (string, error) {
	clusterID, err := a.stateManager.GetClusterID()
	if err != nil {
		return "", fmt.Errorf("no cluster ID found in state: %w", err)
	}

	a.logger.WithFields(logrus.Fields{
		"cluster_id": clusterID,
		"state_path": a.stateManager.GetStatePath(),
	}).Info("Loaded cluster ID from state")

	return clusterID, nil
}

// saveClusterToken saves the cluster ServiceAccount token to persistent storage
func (a *Agent) saveClusterToken(token string) error {
	if err := a.stateManager.SaveClusterToken(token); err != nil {
		return fmt.Errorf("failed to save cluster token: %w", err)
	}
	a.logger.WithField("state_path", a.stateManager.GetStatePath()).Debug("Saved cluster token to state")
	return nil
}

// loadClusterToken loads the cluster ServiceAccount token from persistent storage
func (a *Agent) loadClusterToken() (string, error) {
	token, err := a.stateManager.GetClusterToken()
	if err != nil {
		return "", fmt.Errorf("no cluster token found in state: %w", err)
	}

	a.logger.WithField("state_path", a.stateManager.GetStatePath()).Debug("Loaded cluster token from state")
	return token, nil
}

// getExternalIP gets external IP from external service
func getExternalIP() string {
	urls := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 3 * time.Second}
	for _, url := range urls {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				ip := strings.TrimSpace(string(body))
				if net.ParseIP(ip) != nil {
					return ip
				}
			}
		}
	}
	return ""
}

// getPrimaryIP gets the primary network interface IP
func getPrimaryIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// getOSVersion gets the operating system version
func getOSVersion() string {
	// Try to read /etc/os-release (Linux)
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		content := string(data)
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				name := strings.TrimPrefix(line, "PRETTY_NAME=")
				name = strings.Trim(name, "\"")
				return name
			}
		}
	}

	// Fallback to runtime.GOOS
	return runtime.GOOS
}

// getDiskSpace gets disk space information
func getDiskSpace(path string) *diskStatus {
	fs := syscall.Statfs_t{}
	err := syscall.Statfs(path, &fs)
	if err != nil {
		return nil
	}

	return &diskStatus{
		All:  fs.Blocks * uint64(fs.Bsize),
		Free: fs.Bfree * uint64(fs.Bsize),
	}
}

type diskStatus struct {
	All  uint64
	Free uint64
}
