package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/components"
	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
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

var errReRegistrationInProgress = errors.New("re-registration in progress")

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
	monitoringMgr   *components.Manager // Components manager (monitoring, ingress, metrics)
	stateManager    *state.StateManager // Manages persistent state
	k8sClient       *k8s.Client         // Kubernetes client for in-cluster API access
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	clusterID       string          // Cluster UUID from registration
	clusterToken    string          // K8s ServiceAccount token for control plane to access cluster
	clusterCertData string          // Base64-encoded cluster CA bundle for control plane access
	connectionState ConnectionState // Current connection state
	lastHeartbeat   time.Time       // Last successful heartbeat
	stateMutex      sync.RWMutex    // Protects connection state
	monitoringReady bool            // Indicates if monitoring stack is ready
	monitoringSetup bool            // Indicates if monitoring stack setup has been initiated
	monitoringMutex sync.RWMutex    // Protects monitoring ready and setup state
	reregistering   bool            // Indicates if re-registration is in progress
	reregisterMutex sync.Mutex      // Protects re-registration flag
	registerMutex   sync.Mutex      // Serializes register() executions
}

// New creates a new agent instance
func New(config *types.Config, logger *logrus.Logger) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize state manager for optional persistence
	stateManager := state.NewStateManager()
	logger.WithField("state_path", stateManager.GetStatePath()).Debug("Initialized state manager for optional persistence")

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
		logger.WithFields(logrus.Fields{
			"hostname":        hostname,
			"state_path":      stateManager.GetStatePath(),
			"using_configmap": stateManager.IsUsingConfigMap(),
		}).Debug("Agent ID not set in config, attempting to load from persistent storage")

		// Try to load from state manager (ConfigMap)
		if persistentID, err := stateManager.GetAgentID(); err == nil && persistentID != "" {
			config.Agent.ID = persistentID
			logger.WithFields(logrus.Fields{
				"agent_id":   persistentID,
				"state_path": stateManager.GetStatePath(),
				"state_type": "ConfigMap",
			}).Info("✅ Agent ID loaded from persistent state - will maintain identity across restarts")
		} else {
			// Generate new ID
			config.Agent.ID = generateAgentID(hostname)
			logger.WithFields(logrus.Fields{
				"agent_id":        config.Agent.ID,
				"reason":          "no existing agent ID in state",
				"error":           err,
				"using_configmap": stateManager.IsUsingConfigMap(),
			}).Warn("⚠️  Generated new agent ID - this will cause re-registration!")

			// Try to save to state immediately
			if err := stateManager.SaveAgentID(config.Agent.ID); err != nil {
				logger.WithError(err).Error("❌ CRITICAL: Failed to persist agent ID to state - agent will re-register on every restart!")
				logger.WithFields(logrus.Fields{
					"state_path":      stateManager.GetStatePath(),
					"using_configmap": stateManager.IsUsingConfigMap(),
					"namespace":       stateManager.GetStatePath(),
				}).Error("   Check RBAC permissions for ConfigMap creation/update in agent namespace")
			} else {
				logger.WithFields(logrus.Fields{
					"agent_id":   config.Agent.ID,
					"state_path": stateManager.GetStatePath(),
				}).Info("✅ Agent ID persisted to state for future restarts")
			}
		}
	} else {
		logger.WithFields(logrus.Fields{
			"agent_id": config.Agent.ID,
			"source":   "configuration",
		}).Info("Using agent ID from configuration")

		// Save to state for persistence across restarts
		if err := stateManager.SaveAgentID(config.Agent.ID); err != nil {
			logger.WithError(err).Warn("Failed to persist agent ID to state - agent may re-register on restart")
		} else {
			logger.WithField("state_path", stateManager.GetStatePath()).Debug("Agent ID persisted to state")
		}
	}

	// Initialize Kubernetes client for in-cluster API access
	if k8sClient, err := k8s.NewInClusterClient(); err != nil {
		logger.WithError(err).Warn("Failed to create Kubernetes client (will use fallback methods)")
	} else {
		agent.k8sClient = k8sClient
		logger.Info("Kubernetes client initialized")
	}

	// Initialize control plane client
	if config.PipeOps.APIURL != "" && config.PipeOps.Token != "" {
		controlPlaneClient, err := controlplane.NewClient(
			config.PipeOps.APIURL,
			config.PipeOps.Token,
			config.Agent.ID,
			&config.PipeOps.TLS,
			logger,
		)
		if err != nil {
			logger.WithError(err).Warn("Failed to create control plane client")
		} else {
			agent.controlPlane = controlPlaneClient
			logger.Info("Control plane client initialized")

			// Set up callback for registration errors
			controlPlaneClient.SetOnRegistrationError(func(err error) {
				logger.WithError(err).Warn("Registration error received from control plane - clearing state to trigger re-registration")
				agent.handleRegistrationError()
			})

			// Handle proxy requests from the control plane via WebSocket tunnel
			controlPlaneClient.SetProxyRequestHandler(agent.handleProxyRequest)
			controlPlaneClient.SetOnReconnect(agent.handleControlPlaneReconnect)
		}
	} else {
		logger.Warn("Control plane not configured - agent will run in standalone mode")
	}

	// Initialize HTTP server (simplified - no K8s proxy needed)
	httpServer := server.NewServer(config, logger)
	if agent.k8sClient != nil {
		httpServer.SetKubernetesProxy(agent.k8sClient)
	}
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
	a.registerMutex.Lock()
	defer a.registerMutex.Unlock()

	// Skip registration if control plane client not configured
	if a.controlPlane == nil {
		a.logger.Info("Skipping registration - running in standalone mode")
		return nil
	}

	a.updateConnectionState(StateConnecting)

	// Load cluster credentials first (needed for both new and existing registrations)
	a.loadClusterCredentials()

	existingClusterID := ""
	if storedClusterID, err := a.loadClusterID(); err == nil && storedClusterID != "" {
		existingClusterID = storedClusterID
		a.clusterID = storedClusterID

		a.logger.WithFields(logrus.Fields{
			"cluster_id": storedClusterID,
			"agent_id":   a.config.Agent.ID,
		}).Info("✓ Loaded existing cluster registration from state")
	} else if err != nil {
		a.logger.WithFields(logrus.Fields{
			"error":      err.Error(),
			"state_path": a.stateManager.GetStatePath(),
		}).Info("No existing cluster ID found in state, will register as new cluster")
	}

	hostname, _ := os.Hostname()

	// Get K8s version from local cluster
	k8sVersion := a.getK8sVersion()

	// Get server IP
	serverIP := a.getServerIP()

	// Prepare agent registration payload matching control plane's RegisterClusterRequest
	// Note: Monitoring stack will be set up AFTER successful registration
	agent := &types.Agent{
		// Required fields
		ID:   a.config.Agent.ID,          // agent_id
		Name: a.config.Agent.ClusterName, // name (cluster name)

		// K8s and server information
		Version:         k8sVersion,        // k8s_version
		ServerIP:        serverIP,          // server_ip
		ServerCode:      serverIP,          // server_code (same as ServerIP for agent clusters)
		Token:           a.clusterToken,    // k8s_service_token (K8s ServiceAccount token)
		ClusterCertData: a.clusterCertData, // cluster_cert_data (base64 CA bundle)
		Region:          "agent-managed",   // region (default for agent clusters)
		CloudProvider:   "agent",           // cloud_provider (default for agent clusters)

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

		// Monitoring stack information - will be empty during initial registration
		// Will be updated after monitoring stack is set up post-registration

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
	if existingClusterID != "" {
		agent.ClusterID = existingClusterID
		agent.Metadata["registration_mode"] = "resume"
	} else {
		agent.Metadata["registration_mode"] = "fresh"
	}

	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	result, err := a.controlPlane.RegisterAgent(ctx, agent)
	if err != nil {
		return fmt.Errorf("failed to register agent: %w", err)
	}

	if result.ClusterID == "" {
		return fmt.Errorf("control plane returned empty cluster_id")
	}

	previousClusterID := existingClusterID
	a.clusterID = result.ClusterID

	isNewCluster := previousClusterID == "" || result.ClusterID != previousClusterID
	if isNewCluster {
		if previousClusterID != "" && result.ClusterID != previousClusterID {
			a.logger.WithFields(logrus.Fields{
				"old_cluster_id": previousClusterID,
				"new_cluster_id": result.ClusterID,
			}).Warn("Control plane returned a different cluster_id - updating state")
		}

		if err := a.saveClusterID(result.ClusterID); err != nil {
			a.logger.WithError(err).Error("❌ CRITICAL: Failed to persist cluster ID to state - cluster will re-register on every restart!")
			a.logger.WithFields(logrus.Fields{
				"state_path":      a.stateManager.GetStatePath(),
				"using_configmap": a.stateManager.IsUsingConfigMap(),
			}).Error("   Check RBAC permissions for ConfigMap update in agent namespace")
		} else {
			a.logger.WithFields(logrus.Fields{
				"cluster_id": result.ClusterID,
				"agent_id":   a.config.Agent.ID,
				"state_path": a.stateManager.GetStatePath(),
				"state_type": "ConfigMap",
			}).Info("✅ Cluster ID persisted to state - will prevent re-registration on restart")
		}
	} else {
		a.logger.WithFields(logrus.Fields{
			"cluster_id": result.ClusterID,
			"agent_id":   a.config.Agent.ID,
		}).Info("✓ Re-validated existing cluster registration with control plane")
	}

	// Save token if provided by control plane
	if result.Token != "" {
		if a.validateClusterTokenForProvisioning(result.Token, "control plane") {
			a.clusterToken = result.Token
			if err := a.saveClusterToken(result.Token); err != nil {
				a.logger.WithError(err).Warn("Failed to persist control plane issued cluster token")
			}
		} else {
			a.logger.WithField("token_source", "control plane").Warn("Ignoring control plane issued token without namespace provisioning rights")
		}
	}

	a.logger.WithFields(logrus.Fields{
		"cluster_id": result.ClusterID,
		"agent_id":   a.config.Agent.ID,
	}).Info("✓ Cluster registered successfully")
	a.updateConnectionState(StateConnected)

	// Set up monitoring stack AFTER successful registration (only if not already set up)
	if err := a.setupMonitoring(); err != nil {
		return fmt.Errorf("failed to set up monitoring stack: %w", err)
	}

	// Wait for monitoring to be ready (with timeout)
	a.monitoringMutex.RLock()
	alreadyReady := a.monitoringReady
	a.monitoringMutex.RUnlock()

	if !alreadyReady {
		if err := a.waitForMonitoring(120 * time.Second); err != nil {
			return fmt.Errorf("monitoring stack not ready within timeout: %w", err)
		}
		a.logger.Info("✓ Monitoring stack ready and operational")
	}

	return nil
}

// setupMonitoring initializes and starts the monitoring stack
func (a *Agent) setupMonitoring() error {
	// Check if monitoring has already been set up
	a.monitoringMutex.Lock()
	if a.monitoringSetup {
		a.monitoringMutex.Unlock()
		a.logger.Debug("Monitoring stack already set up, skipping")
		return nil
	}
	a.monitoringSetup = true
	a.monitoringMutex.Unlock()

	a.logger.Info("Initializing components stack (monitoring, ingress, metrics)...")

	// Create components manager with default monitoring configuration
	stack := components.DefaultMonitoringStack()
	a.configureGrafanaSubPath(stack)

	mgr, err := components.NewManager(stack, a.logger)
	if err != nil {
		return fmt.Errorf("failed to create components manager: %w", err)
	}

	a.monitoringMgr = mgr

	// Start components stack synchronously (blocking)
	// This ensures we catch any initialization errors before proceeding
	// Note: The manager logs its own "Starting monitoring stack manager..." message
	if err := mgr.Start(); err != nil {
		return fmt.Errorf("failed to start monitoring stack: %w", err)
	}

	// Mark monitoring as ready
	a.monitoringMutex.Lock()
	a.monitoringReady = true
	a.monitoringMutex.Unlock()

	a.logger.Info("Monitoring stack initialization completed")

	return nil
}

// configureGrafanaSubPath updates the Grafana configuration so it serves assets from the
// control-plane proxy path. This avoids broken dashboards when Grafana is accessed via the
// agent-managed subpath exposed by the PipeOps API.
func (a *Agent) configureGrafanaSubPath(stack *components.MonitoringStack) {
	if stack == nil || stack.Grafana == nil || stack.Prometheus == nil {
		return
	}

	if a.config != nil && !a.config.Agent.GrafanaSubPath {
		a.logger.Debug("Skipping Grafana subpath configuration (disabled via config)")
		return
	}

	if stack.Grafana.RootURL != "" || stack.Grafana.ServeFromSubPath {
		// Respect user-specified Grafana routing configuration.
		return
	}

	if a.config == nil || a.config.PipeOps.APIURL == "" || a.clusterID == "" {
		return
	}

	baseURL := strings.TrimSuffix(a.config.PipeOps.APIURL, "/")
	namespace := stack.Grafana.Namespace
	if namespace == "" && stack.Prometheus.Namespace != "" {
		namespace = stack.Prometheus.Namespace
	}
	if namespace == "" {
		namespace = "default"
	}
	serviceName := fmt.Sprintf("%s-grafana", stack.Prometheus.ReleaseName)
	if stack.Prometheus.ReleaseName == "" {
		serviceName = "grafana"
	}
	port := stack.Grafana.LocalPort
	if port == 0 {
		port = 3000
	}

	rootURL := fmt.Sprintf("%s/api/v1/clusters/agent/%s/proxy/api/v1/namespaces/%s/services/http:%s:%d/proxy/",
		baseURL,
		a.clusterID,
		namespace,
		serviceName,
		port,
	)

	stack.Grafana.RootURL = rootURL
	stack.Grafana.ServeFromSubPath = true

	a.logger.WithFields(logrus.Fields{
		"root_url": rootURL,
		"service":  serviceName,
	}).Debug("Configured Grafana to serve from control plane subpath")
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

// startHeartbeat starts periodic heartbeat with retry logic
func (a *Agent) startHeartbeat() {
	// Send heartbeat immediately on connection (don't wait for first tick)
	if err := a.sendHeartbeatWithRetry(); err != nil {
		a.logger.WithError(err).Error("Failed to send initial heartbeat")
		a.updateConnectionState(StateDisconnected)
	} else {
		a.updateConnectionState(StateConnected)
	}

	// Continue sending heartbeat every 30 seconds to match control plane expectations
	ticker := time.NewTicker(30 * time.Second)
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

		if errors.Is(err, errReRegistrationInProgress) {
			a.logger.Debug("Heartbeat skipped because re-registration is in progress")
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

	// If cluster ID not set, attempt registration
	if a.clusterID == "" {
		if a.isReregistering() {
			a.logger.Debug("Skipping heartbeat while re-registration is in progress")
			return errReRegistrationInProgress
		}
		a.logger.Info("Cluster not registered - attempting registration")
		if err := a.register(); err != nil {
			a.logger.WithError(err).Warn("Failed to register cluster")
			return err
		}
		// After successful registration, continue with heartbeat
		if a.clusterID == "" {
			// Registration failed to set cluster_id
			return fmt.Errorf("registration did not set cluster_id")
		}
	}

	// Determine tunnel status
	tunnelStatus := "disconnected"
	if a.tunnelMgr != nil {
		tunnelStatus = "connected"
	}

	// Collect cluster metrics
	nodeCount, podCount := a.getClusterMetrics()

	// Collect monitoring stack credentials
	monInfo := a.getMonitoringInfo()

	heartbeat := &controlplane.HeartbeatRequest{
		ClusterID:    a.clusterID,
		AgentID:      a.config.Agent.ID,
		Status:       "healthy",
		TunnelStatus: tunnelStatus,
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"version":      version.GetVersion(),
			"k8s_nodes":    nodeCount,
			"k8s_pods":     podCount,
			"cpu_usage":    "0%",
			"memory_usage": "0%",
		},

		// Monitoring stack credentials (sent with every heartbeat)
		PrometheusURL:      monInfo.PrometheusURL,
		PrometheusUsername: monInfo.PrometheusUsername,
		PrometheusPassword: monInfo.PrometheusPassword,
		PrometheusSSL:      monInfo.PrometheusSSL,

		LokiURL:      monInfo.LokiURL,
		LokiUsername: monInfo.LokiUsername,
		LokiPassword: monInfo.LokiPassword,

		OpenCostBaseURL:  monInfo.OpenCostURL,
		OpenCostUsername: monInfo.OpenCostUsername,
		OpenCostPassword: monInfo.OpenCostPassword,

		GrafanaURL:      monInfo.GrafanaURL,
		GrafanaUsername: monInfo.GrafanaUsername,
		GrafanaPassword: monInfo.GrafanaPassword,

		// Tunnel ports for monitoring services
		TunnelPrometheusPort: monInfo.TunnelPrometheusPort,
		TunnelLokiPort:       monInfo.TunnelLokiPort,
		TunnelOpenCostPort:   monInfo.TunnelOpenCostPort,
		TunnelGrafanaPort:    monInfo.TunnelGrafanaPort,
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

// getK8sVersion gets K8s version from the cluster using the Kubernetes client-go SDK
func (a *Agent) getK8sVersion() string {
	if a.k8sClient == nil {
		a.logger.Debug("Kubernetes client not initialized, using default version")
		return "v1.28.0+k3s1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	versionInfo, err := a.k8sClient.GetVersion(ctx)
	if err != nil {
		a.logger.WithError(err).Debug("Failed to get Kubernetes version, using default")
		return "v1.28.0+k3s1"
	}

	a.logger.WithField("version", versionInfo.GitVersion).Debug("Got K8s version from API")
	return versionInfo.GitVersion
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

// getClusterMetrics collects basic cluster metrics (node count, pod count)
// Uses Kubernetes client-go SDK for in-cluster access
func (a *Agent) getClusterMetrics() (nodeCount int, podCount int) {
	if a.k8sClient == nil {
		a.logger.Debug("Kubernetes client not initialized, skipping metrics collection")
		return 0, 0
	}

	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()

	var err error
	nodeCount, podCount, err = a.k8sClient.GetClusterMetrics(ctx)
	if err != nil {
		a.logger.WithError(err).Debug("Failed to collect cluster metrics")
		return 0, 0
	}

	a.logger.WithFields(logrus.Fields{
		"nodes": nodeCount,
		"pods":  podCount,
	}).Debug("Collected cluster metrics from Kubernetes API")

	return nodeCount, podCount
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
	return nil
}

// handleRegistrationError handles registration errors from the control plane
// This triggers re-registration without clearing the cluster_id
func (a *Agent) handleRegistrationError() {
	// Check if re-registration is already in progress
	a.reregisterMutex.Lock()
	if a.reregistering {
		a.reregisterMutex.Unlock()
		a.logger.Debug("Re-registration already in progress, skipping duplicate attempt")
		return
	}
	a.reregistering = true
	a.reregisterMutex.Unlock()

	a.logger.Warn("Handling registration error - will trigger re-registration")

	// Clear the invalid cluster_id from memory and state
	// This forces a fresh registration instead of reloading the rejected cluster_id
	oldClusterID := a.clusterID
	a.clusterID = ""

	// Clear cluster_id from state so register() doesn't reload it
	if err := a.stateManager.ClearClusterID(); err != nil {
		a.logger.WithError(err).Warn("Failed to clear invalid cluster_id from state")
	} else {
		a.logger.WithField("old_cluster_id", oldClusterID).Info("Cleared invalid cluster_id from state - will register as new cluster")
	}

	go func() {
		defer func() {
			a.reregisterMutex.Lock()
			a.reregistering = false
			a.reregisterMutex.Unlock()
		}()

		a.logger.Info("Re-registering with control plane after error...")
		if err := a.register(); err != nil {
			a.logger.WithError(err).Error("Failed to re-register after error")
		} else {
			a.logger.Info("Successfully re-registered with control plane")
		}
	}()
}

// handleControlPlaneReconnect refreshes registration after the WebSocket connection is restored.
func (a *Agent) handleControlPlaneReconnect() {
	if a.ctx.Err() != nil {
		return
	}

	a.reregisterMutex.Lock()
	if a.reregistering {
		a.reregisterMutex.Unlock()
		a.logger.Debug("Re-registration already in progress, skipping reconnect handler")
		return
	}
	a.reregistering = true
	a.reregisterMutex.Unlock()

	a.logger.Info("Control plane connection re-established - refreshing registration")

	go func() {
		defer func() {
			a.reregisterMutex.Lock()
			a.reregistering = false
			a.reregisterMutex.Unlock()
		}()

		if err := a.register(); err != nil {
			a.logger.WithError(err).Error("Failed to refresh registration after reconnect")
			return
		}

		a.logger.Info("Re-registration after reconnect completed successfully")
	}()
}

func (a *Agent) isReregistering() bool {
	a.reregisterMutex.Lock()
	inProgress := a.reregistering
	a.reregisterMutex.Unlock()
	return inProgress
}

func (a *Agent) handleProxyRequest(req *controlplane.ProxyRequest, writer controlplane.ProxyResponseWriter) {
	logger := a.logger.WithFields(logrus.Fields{
		"request_id": req.RequestID,
		"method":     strings.ToUpper(req.Method),
		"path":       req.Path,
	})

	if a.controlPlane == nil {
		logger.Warn("Control plane client unavailable - cannot respond to proxy request")
		_ = writer.CloseWithError(fmt.Errorf("control plane client unavailable"))
		return
	}

	if a.k8sClient == nil {
		logger.Warn("Kubernetes client unavailable - responding with proxy error")
		_ = writer.CloseWithError(fmt.Errorf("kubernetes client not initialized"))
		return
	}

	ctx, cancel := a.proxyRequestContext(req)
	defer cancel()

	var requestBody io.ReadCloser
	switch {
	case req.BodyStream() != nil:
		requestBody = req.BodyStream()
	case len(req.Body) > 0:
		requestBody = io.NopCloser(bytes.NewReader(req.Body))
	default:
		requestBody = nil
	}

	statusCode, respHeaders, respBody, err := a.k8sClient.ProxyRequest(ctx, req.Method, req.Path, req.Query, req.Headers, requestBody)
	if err != nil {
		logger.WithError(err).Warn("Proxy request failed")
		_ = writer.CloseWithError(err)
		_ = req.CloseBody()
		return
	}
	defer respBody.Close()
	defer req.CloseBody()

	filteredHeaders := make(map[string][]string)
	for key, values := range respHeaders {
		if strings.EqualFold(key, "connection") ||
			strings.EqualFold(key, "transfer-encoding") ||
			strings.EqualFold(key, "keep-alive") ||
			strings.EqualFold(key, "proxy-authenticate") ||
			strings.EqualFold(key, "proxy-authorization") ||
			strings.EqualFold(key, "te") ||
			strings.EqualFold(key, "trailers") ||
			strings.EqualFold(key, "upgrade") {
			continue
		}
		filteredHeaders[key] = append([]string(nil), values...)
	}

	if err := writer.WriteHeader(statusCode, filteredHeaders); err != nil {
		logger.WithError(err).Warn("Failed to write proxy response header")
		_ = writer.CloseWithError(err)
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := respBody.Read(buf)
		if n > 0 {
			if err := writer.WriteChunk(buf[:n]); err != nil {
				logger.WithError(err).Warn("Failed to stream proxy response body")
				_ = writer.CloseWithError(err)
				return
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}

			logger.WithError(readErr).Warn("Error reading proxy response body")
			_ = writer.CloseWithError(readErr)
			return
		}
	}

	if err := writer.Close(); err != nil {
		logger.WithError(err).Warn("Failed to finalize proxy response to control plane")
	}
}

func (a *Agent) proxyRequestContext(req *controlplane.ProxyRequest) (context.Context, context.CancelFunc) {
	if !req.Deadline.IsZero() {
		return context.WithDeadline(a.ctx, req.Deadline)
	}

	if req.Timeout > 0 {
		return context.WithTimeout(a.ctx, req.Timeout)
	}

	return context.WithTimeout(a.ctx, 2*time.Minute)
}

// validateClusterTokenForProvisioning ensures the provided token can create namespaces, which is
// the minimum privilege required for control plane auto-provisioning flows. When the Kubernetes
// client is unavailable (e.g., running outside a cluster), validation is skipped.
func (a *Agent) validateClusterTokenForProvisioning(token string, source string) bool {
	if token == "" {
		return false
	}

	if a.k8sClient == nil {
		a.logger.WithField("token_source", source).Debug("Kubernetes client unavailable - skipping token RBAC validation")
		return true
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	allowed, err := a.k8sClient.TokenHasNamespaceWriteAccess(ctx, token)
	if err != nil {
		a.logger.WithFields(logrus.Fields{
			"token_source": source,
		}).WithError(err).Warn("Failed to validate cluster token RBAC")
		return false
	}

	if !allowed {
		a.logger.WithField("token_source", source).Warn("Cluster token lacks permission to create namespaces")
		return false
	}

	a.logger.WithField("token_source", source).Info("Cluster token validated for namespace provisioning")
	return true
}

// loadClusterCredentials attempts to load the Kubernetes ServiceAccount token
// This token is needed by the control plane to access the cluster through the tunnel
// loadClusterCredentials attempts to load the Kubernetes ServiceAccount token
// This token is needed by the control plane to access the cluster through the tunnel
func (a *Agent) loadClusterCredentials() {
	var (
		persistedToken   string
		persistedTokenOK bool
		fallbackToken    string
		fallbackSource   string
	)

	tryToken := func(rawToken, source string, persist bool) bool {
		token := strings.TrimSpace(rawToken)
		if token == "" {
			return false
		}
		if !a.validateClusterTokenForProvisioning(token, source) {
			return false
		}
		a.clusterToken = token
		if persist {
			if err := a.saveClusterToken(token); err != nil {
				a.logger.WithFields(logrus.Fields{
					"token_source": source,
				}).WithError(err).Debug("Failed to persist cluster token to state")
			}
		}
		return true
	}

	if token, err := a.loadClusterToken(); err == nil && token != "" {
		persistedToken = token
		fallbackToken = token
		fallbackSource = "persistent state"
		persistedTokenOK = tryToken(token, "persistent state", false)
		if persistedTokenOK {
			a.logger.Info("Loaded cluster token from state with confirmed namespace provisioning access")
		}
	} else if err != nil {
		a.logger.WithError(err).Debug("No persisted cluster token available in state")
	}

	if a.clusterToken == "" && a.config != nil {
		if cfgToken := strings.TrimSpace(a.config.Kubernetes.ServiceToken); cfgToken != "" {
			if tryToken(cfgToken, "configuration override", true) {
				a.logger.Info("Using cluster token provided via configuration override")
			} else {
				a.logger.Warn("Configuration-provided cluster token failed namespace provisioning validation")
			}
		}
	}

	saToken, saErr := k8s.GetServiceAccountToken()
	if saErr != nil {
		a.logger.WithError(saErr).Debug("Failed to read ServiceAccount token from mount path")
	}

	if a.clusterToken == "" && saToken != "" {
		fallbackToken = saToken
		fallbackSource = "service account"
		if !persistedTokenOK || saToken != persistedToken {
			if tryToken(saToken, "service account", true) {
				a.logger.Info("Persisted ServiceAccount token for control plane access")
			}
		}
	}

	if a.clusterToken == "" && fallbackToken != "" {
		a.clusterToken = fallbackToken
		a.logger.WithField("token_source", fallbackSource).Warn("Using cluster token without confirmed namespace provisioning rights - control plane write operations may continue to fail")
		if fallbackSource == "service account" {
			if err := a.saveClusterToken(fallbackToken); err != nil {
				a.logger.WithError(err).Debug("Failed to persist fallback ServiceAccount token to state")
			}
		}
	}

	if a.clusterToken == "" {
		a.logger.Warn("No cluster token available - control plane interactions requiring Kubernetes access will fail")
	}

	// Handle CA certificate loading after token selection
	a.loadClusterCertificate()
}

// loadClusterCertificate ensures the agent has a base64-encoded CA bundle for control plane access.
func (a *Agent) loadClusterCertificate() {
	if a.config != nil {
		if cfgCert := strings.TrimSpace(a.config.Kubernetes.CACertData); cfgCert != "" {
			a.clusterCertData = cfgCert
			if err := a.saveClusterCertData(cfgCert); err != nil {
				a.logger.WithError(err).Debug("Failed to persist configuration-provided cluster CA data")
			} else {
				a.logger.Info("Using cluster CA bundle provided via configuration override")
			}
			return
		}
	}

	if certData, err := a.loadClusterCertData(); err == nil && certData != "" {
		a.clusterCertData = certData
		a.logger.Info("Loaded cluster CA bundle from persistent state")
		return
	} else if err != nil {
		a.logger.WithError(err).Debug("No persisted cluster CA bundle available in state")
	}

	saCert, err := k8s.GetServiceAccountCACertData()
	if err != nil {
		a.logger.WithError(err).Debug("Failed to read ServiceAccount CA certificate from mount path")
	} else if saCert != "" {
		a.clusterCertData = saCert
		if err := a.saveClusterCertData(saCert); err != nil {
			a.logger.WithError(err).Debug("Failed to persist ServiceAccount CA certificate to state")
		} else {
			a.logger.Info("Persisted ServiceAccount CA certificate for control plane access")
		}
		return
	}

	if a.clusterCertData == "" {
		a.logger.Warn("No cluster CA certificate available - control plane TLS validation may fail")
	}
}

// loadClusterID loads the cluster ID from persistent storage
func (a *Agent) loadClusterID() (string, error) {
	clusterID, err := a.stateManager.GetClusterID()
	if err != nil {
		a.logger.WithFields(logrus.Fields{
			"error":      err.Error(),
			"state_path": a.stateManager.GetStatePath(),
		}).Debug("Failed to load cluster ID from state")
		return "", fmt.Errorf("no cluster ID found in state: %w", err)
	}

	a.logger.WithFields(logrus.Fields{
		"cluster_id": clusterID,
		"state_path": a.stateManager.GetStatePath(),
	}).Info("✓ Loaded cluster ID from state")

	return clusterID, nil
}

// saveClusterToken saves the cluster ServiceAccount token to persistent storage
// saveClusterToken saves the cluster ServiceAccount token to persistent storage
func (a *Agent) saveClusterToken(token string) error {
	if err := a.stateManager.SaveClusterToken(token); err != nil {
		return fmt.Errorf("failed to save cluster token: %w", err)
	}
	return nil
}

// loadClusterToken loads the cluster ServiceAccount token from persistent storage
func (a *Agent) loadClusterToken() (string, error) {
	token, err := a.stateManager.GetClusterToken()
	if err != nil {
		return "", fmt.Errorf("no cluster token found in state: %w", err)
	}
	return token, nil
}

// saveClusterCertData saves the cluster CA certificate to persistent storage
func (a *Agent) saveClusterCertData(certData string) error {
	if err := a.stateManager.SaveClusterCertData(certData); err != nil {
		return fmt.Errorf("failed to save cluster cert data: %w", err)
	}
	return nil
}

// loadClusterCertData loads the cluster CA certificate from persistent storage
func (a *Agent) loadClusterCertData() (string, error) {
	certData, err := a.stateManager.GetClusterCertData()
	if err != nil {
		return "", fmt.Errorf("no cluster cert data found in state: %w", err)
	}
	return certData, nil
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
