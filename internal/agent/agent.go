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
	"github.com/pipeops/pipeops-vm-agent/internal/server"
	"github.com/pipeops/pipeops-vm-agent/internal/tunnel"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/k8s"
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
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	clusterID       string          // Cluster UUID from registration
	clusterToken    string          // K8s ServiceAccount token for control plane to access cluster
	connectionState ConnectionState // Current connection state
	lastHeartbeat   time.Time       // Last successful heartbeat
	stateMutex      sync.RWMutex    // Protects connection state
}

// New creates a new agent instance
func New(config *types.Config, logger *logrus.Logger) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	agent := &Agent{
		config:          config,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		connectionState: StateDisconnected,
		lastHeartbeat:   time.Time{},
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

	// Register agent with control plane FIRST (must succeed before starting services)
	// Registration is required - agent cannot function without cluster ID from control plane
	if err := a.register(); err != nil {
		// Stop HTTP server before returning error
		if a.server != nil {
			a.server.Stop()
		}
		return fmt.Errorf("failed to register agent with control plane: %w", err)
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
		return nil
	}

	hostname, _ := os.Hostname()

	// Get K8s version from local cluster
	k8sVersion := a.getK8sVersion()

	// Get server IP
	serverIP := a.getServerIP()

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
	paths := []string{
		"/var/lib/pipeops/cluster-id",
		"/etc/pipeops/cluster-id",
		".pipeops-cluster-id",
	}

	for _, path := range paths {
		// Try to create directory if needed
		dir := strings.TrimSuffix(path, "/cluster-id")
		if dir != path {
			os.MkdirAll(dir, 0755)
		}

		if err := os.WriteFile(path, []byte(clusterID), 0644); err == nil {
			a.logger.WithField("file", path).Debug("Saved cluster ID to disk")
			return nil
		}
	}

	return fmt.Errorf("could not save cluster ID to any location")
}

// loadClusterCredentials attempts to load the Kubernetes ServiceAccount token
// This token is needed by the control plane to access the cluster through the tunnel
func (a *Agent) loadClusterCredentials() {
	// Try to read from Kubernetes ServiceAccount mount (when running in pod)
	if token, err := k8s.GetServiceAccountToken(); err == nil {
		a.clusterToken = token
		// Also save to disk as backup
		if err := a.saveClusterToken(token); err != nil {
			a.logger.WithError(err).Warn("Failed to save ServiceAccount token to disk")
		}
		a.logger.Info("Loaded ServiceAccount token from Kubernetes mount")
		return
	}

	// Fallback: try loading from disk (for dev mode or restart)
	if token, err := a.loadClusterToken(); err == nil {
		a.clusterToken = token
		a.logger.Info("Loaded cluster token from disk")
		return
	}

	a.logger.Warn("No cluster ServiceAccount token available - control plane will not be able to access cluster")
}

// loadClusterID loads the cluster ID from persistent storage
func (a *Agent) loadClusterID() (string, error) {
	paths := []string{
		"/var/lib/pipeops/cluster-id",
		"/etc/pipeops/cluster-id",
		".pipeops-cluster-id",
	}

	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			clusterID := strings.TrimSpace(string(data))
			if clusterID != "" {
				a.logger.WithFields(logrus.Fields{
					"cluster_id": clusterID,
					"file":       path,
				}).Info("Loaded cluster ID from disk")
				return clusterID, nil
			}
		}
	}

	return "", fmt.Errorf("no cluster ID found in persistent storage")
}

// saveClusterToken saves the cluster ServiceAccount token to persistent storage
func (a *Agent) saveClusterToken(token string) error {
	paths := []string{
		"/var/lib/pipeops/cluster-token",
		"/etc/pipeops/cluster-token",
		".pipeops-cluster-token",
	}

	for _, path := range paths {
		// Try to create directory if needed
		dir := strings.TrimSuffix(path, "/cluster-token")
		if dir != path {
			os.MkdirAll(dir, 0755)
		}

		// Save with restricted permissions (0600) for security
		if err := os.WriteFile(path, []byte(token), 0600); err == nil {
			a.logger.WithField("file", path).Debug("Saved cluster token to disk")
			return nil
		}
	}

	return fmt.Errorf("could not save cluster token to any location")
}

// loadClusterToken loads the cluster ServiceAccount token from persistent storage
func (a *Agent) loadClusterToken() (string, error) {
	paths := []string{
		"/var/lib/pipeops/cluster-token",
		"/etc/pipeops/cluster-token",
		".pipeops-cluster-token",
	}

	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			token := strings.TrimSpace(string(data))
			if token != "" {
				a.logger.WithField("file", path).Debug("Loaded cluster token from disk")
				return token, nil
			}
		}
	}

	return "", fmt.Errorf("no cluster token found in persistent storage")
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
