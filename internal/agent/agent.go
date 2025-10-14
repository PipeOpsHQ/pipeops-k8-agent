package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	clusterID    string // Cluster UUID from registration
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

	// Generate or load persistent agent ID if not set in config
	if config.Agent.ID == "" {
		hostname, _ := os.Hostname()
		logger.Debug("Agent ID not set in config, generating persistent ID")
		persistentID, err := agent.getOrCreatePersistentAgentID(hostname)
		if err != nil {
			logger.WithError(err).Warn("Failed to get persistent agent ID, using generated ID")
			config.Agent.ID = generateAgentID(hostname)
		} else {
			config.Agent.ID = persistentID
			logger.WithField("agent_id", persistentID).Info("Agent ID set from persistent storage")
		}
	} else {
		logger.WithField("agent_id", config.Agent.ID).Info("Using agent ID from configuration")
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

	// Get K8s version from local cluster
	k8sVersion := a.getK8sVersion()

	// Get server IP
	serverIP := a.getServerIP()

	agent := &types.Agent{
		ID:       a.config.Agent.ID,          // Agent ID already set in New()
		Name:     a.config.Agent.ClusterName, // Cluster name is the primary name
		Version:  k8sVersion,
		Hostname: hostname,
		ServerIP: serverIP,
		Labels:   a.config.Agent.Labels,
		TunnelPortConfig: types.TunnelPortConfig{
			KubernetesAPI: 6443,
			Kubelet:       10250,
			AgentHTTP:     8080,
		},
		ServerSpecs: a.getServerSpecs(),
	}

	// Add default labels
	if agent.Labels == nil {
		agent.Labels = make(map[string]string)
	}
	agent.Labels["hostname"] = hostname
	agent.Labels["agent.pipeops.io/version"] = version.GetVersion()

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	clusterID, err := a.controlPlane.RegisterAgent(ctx, agent)
	if err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}

	// Store cluster ID for heartbeats
	a.clusterID = clusterID
	a.logger.WithField("cluster_id", clusterID).Debug("Cluster ID stored for heartbeat usage")

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

// getK8sVersion attempts to get K8s version from kubectl or returns default
func (a *Agent) getK8sVersion() string {
	// Try to get version from kubectl
	cmd := exec.Command("kubectl", "version", "--short", "--client")
	if output, err := cmd.Output(); err == nil {
		version := string(output)
		// Parse version like "Client Version: v1.28.3"
		if idx := strings.Index(version, "v"); idx != -1 {
			parts := strings.Fields(version[idx:])
			if len(parts) > 0 {
				return strings.TrimSpace(parts[0])
			}
		}
	}

	// Default version
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

// getOrCreatePersistentAgentID gets or creates a persistent agent ID
func (a *Agent) getOrCreatePersistentAgentID(hostname string) (string, error) {
	// Agent ID file path - stored in /var/lib/pipeops or fallback to local
	agentIDPaths := []string{
		"/var/lib/pipeops/agent-id",
		"/etc/pipeops/agent-id",
		".pipeops-agent-id", // Fallback to local directory
	}

	var agentIDFile string
	var existingID string

	// Try to read existing agent ID from any of the paths
	for _, path := range agentIDPaths {
		if data, err := os.ReadFile(path); err == nil {
			existingID = strings.TrimSpace(string(data))
			if existingID != "" {
				a.logger.WithField("agent_id", existingID).Info("Using existing agent ID from file")
				return existingID, nil
			}
		}
	}

	// No existing ID found, generate a new one
	// Use hostname-based deterministic ID (without timestamp for persistence)
	data := fmt.Sprintf("pipeops-agent-%s", hostname)
	hash := sha256.Sum256([]byte(data))
	newID := fmt.Sprintf("agent-%s-%s", hostname, hex.EncodeToString(hash[:8]))

	// Try to save the agent ID to persistent storage
	for _, path := range agentIDPaths {
		// Try to create directory if it doesn't exist
		dir := strings.TrimSuffix(path, "/agent-id")
		if dir != path { // Only if it's not the local fallback
			os.MkdirAll(dir, 0755)
		}

		// Try to write the agent ID
		if err := os.WriteFile(path, []byte(newID), 0644); err == nil {
			agentIDFile = path
			a.logger.WithFields(logrus.Fields{
				"agent_id": newID,
				"file":     agentIDFile,
			}).Info("Created new persistent agent ID")
			break
		}
	}

	if agentIDFile == "" {
		a.logger.Warn("Could not save agent ID to persistent storage, ID may change on restart")
	}

	return newID, nil
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
