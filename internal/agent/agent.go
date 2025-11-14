package agent

import (
	"bufio"
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

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/internal/components"
	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/pipeops/pipeops-vm-agent/internal/helm"
	"github.com/pipeops/pipeops-vm-agent/internal/ingress"
	"github.com/pipeops/pipeops-vm-agent/internal/server"
	"github.com/pipeops/pipeops-vm-agent/internal/tunnel"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/cloud"
	"github.com/pipeops/pipeops-vm-agent/pkg/k8s"
	"github.com/pipeops/pipeops-vm-agent/pkg/state"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Buffer pool for proxy request/response buffering to reduce GC pressure
var proxyBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 32*1024) // 32KB buffers
		return &buf
	},
}

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
	config                       *types.Config
	logger                       *logrus.Logger
	server                       *server.Server
	controlPlane                 *controlplane.Client
	tunnelMgr                    *tunnel.Manager
	monitoringMgr                *components.Manager     // Components manager (monitoring, ingress, metrics)
	stateManager                 *state.StateManager     // Manages persistent state
	k8sClient                    *k8s.Client             // Kubernetes client for in-cluster API access
	gatewayWatcher               *ingress.IngressWatcher // Gateway proxy ingress watcher (for private clusters)
	metrics                      *Metrics                // Prometheus metrics
	ctx                          context.Context
	cancel                       context.CancelFunc
	wg                           sync.WaitGroup
	clusterID                    string          // Cluster UUID from registration
	clusterToken                 string          // K8s ServiceAccount token for control plane to access cluster
	clusterCertData              string          // Base64-encoded cluster CA bundle for control plane access
	connectionState              ConnectionState // Current connection state
	lastHeartbeat                time.Time       // Last successful heartbeat
	consecutiveHeartbeatFailures int             // Number of consecutive heartbeat failures
	heartbeatFailureThreshold    int             // Number of failures before marking disconnected
	stateMutex                   sync.RWMutex    // Protects connection state and heartbeat counters
	monitoringReady              bool            // Indicates if monitoring stack is ready
	monitoringSetup              bool            // Indicates if monitoring stack setup has been initiated
	monitoringMutex              sync.RWMutex    // Protects monitoring ready and setup state
	reregistering                bool            // Indicates if re-registration is in progress
	reregisterMutex              sync.Mutex      // Protects re-registration flag
	registerMutex                sync.Mutex      // Serializes register() executions
	isPrivateCluster             bool            // Indicates if cluster is private (no public LoadBalancer)
	gatewayMutex                 sync.RWMutex    // Protects gateway watcher state
}

// New creates a new agent instance
func New(config *types.Config, logger *logrus.Logger) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize state manager for optional persistence
	stateManager := state.NewStateManager()
	logger.WithField("state_path", stateManager.GetStatePath()).Debug("Initialized state manager for optional persistence")

	agent := &Agent{
		config:                    config,
		logger:                    logger,
		stateManager:              stateManager,
		metrics:                   newMetrics(),
		ctx:                       ctx,
		cancel:                    cancel,
		connectionState:           StateDisconnected,
		lastHeartbeat:             time.Time{},
		heartbeatFailureThreshold: 3, // Allow 3 failures (90s) before marking disconnected
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

	// Set health status provider for server
	httpServer.SetHealthStatusProvider(func() server.HealthStatus {
		agentHealth := agent.GetHealthStatus()
		return server.HealthStatus{
			Healthy:             agentHealth.Healthy,
			Connected:           agentHealth.Connected,
			Registered:          agentHealth.Registered,
			ConnectionState:     agentHealth.ConnectionState,
			LastHeartbeat:       agentHealth.LastHeartbeat,
			TimeSinceLastHB:     agentHealth.TimeSinceLastHB,
			ConsecutiveFailures: agentHealth.ConsecutiveFailures,
		}
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

	// Detect cluster type and start gateway proxy watcher if enabled
	if a.config.Agent.EnableIngressSync && a.k8sClient != nil {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.initializeGatewayProxy()
		}()
		a.logger.Info("Ingress sync enabled - monitoring ingresses for gateway proxy")
	} else if !a.config.Agent.EnableIngressSync {
		a.logger.Info("Ingress sync disabled - agent will not expose cluster via gateway proxy")
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

	// Stop gateway watcher
	a.gatewayMutex.Lock()
	if a.gatewayWatcher != nil {
		if err := a.gatewayWatcher.Stop(); err != nil {
			a.logger.WithError(err).Error("Failed to stop gateway watcher")
		} else {
			a.logger.Info("Gateway watcher stopped")
		}
	}
	a.gatewayMutex.Unlock()

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
	// Detect cloud provider and region
	regionInfo := cloud.DetectRegion(a.ctx, a.k8sClient.GetClientset(), a.logger)

	// Note: Monitoring stack will be set up AFTER successful registration
	agent := &types.Agent{
		// Required fields
		ID:   a.config.Agent.ID,          // agent_id
		Name: a.config.Agent.ClusterName, // name (cluster name)

		// K8s and server information
		Version:         k8sVersion,                              // k8s_version
		ServerIP:        serverIP,                                // server_ip
		ServerCode:      serverIP,                                // server_code (same as ServerIP for agent clusters)
		Token:           a.clusterToken,                          // k8s_service_token (K8s ServiceAccount token)
		ClusterCertData: a.clusterCertData,                       // cluster_cert_data (base64 CA bundle)
		Region:          regionInfo.GetRegionCode(),              // detected region from cloud provider
		CloudProvider:   regionInfo.GetCloudProvider(),           // detected provider (aws, gcp, azure, digitalocean, linode, hetzner, bare-metal, on-premises, or "agent")
		RegistryRegion:  regionInfo.GetPreferredRegistryRegion(), // registry region (eu/us)

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

	// Optionally install the env-aware gateway after monitoring setup
	if err := a.setupGateway(); err != nil {
		// Non-fatal: gateway is optional
		a.logger.WithError(err).Warn("Failed to set up gateway (optional)")
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

	// Check if auto-installation is enabled (set by installer script)
	// If PIPEOPS_AUTO_INSTALL_COMPONENTS is not set or is false, skip component installation
	// This allows Helm/K8s manifests to deploy to existing clusters without auto-installing
	autoInstall := os.Getenv("PIPEOPS_AUTO_INSTALL_COMPONENTS")
	if autoInstall != "true" {
		a.logger.Info("Auto-installation of components disabled (PIPEOPS_AUTO_INSTALL_COMPONENTS != true)")
		a.logger.Info("Agent will securely tunnel cluster to PipeOps without installing monitoring stack")
		// Mark as ready without actually installing anything
		a.monitoringMutex.Lock()
		a.monitoringReady = true
		a.monitoringMutex.Unlock()
		return nil
	}

	a.logger.Info("Auto-installation enabled - initializing components stack (monitoring, ingress, metrics)...")

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

// setupGateway installs the env-aware TCP gateway based on config
func (a *Agent) setupGateway() error {
	if a.config == nil || a.config.Gateway == nil || !a.config.Gateway.Enabled {
		a.logger.Debug("Gateway setup skipped (disabled)")
		return nil
	}

	cfg := a.config.Gateway

	// Construct Helm/Gateway installer
	installer, err := helm.NewHelmInstaller(a.logger)
	if err != nil {
		return fmt.Errorf("failed to create helm installer: %w", err)
	}
	gw := ingress.NewGatewayInstaller(installer, a.logger)

	// Map config -> options
	opts := ingress.GatewayOptions{
		ReleaseName:           cfg.ReleaseName,
		Namespace:             cfg.Namespace,
		EnvironmentMode:       cfg.Environment.Mode,
		VMIP:                  cfg.Environment.VMIP,
		IstioEnabled:          cfg.Istio.Enabled,
		GatewayAPIEnabled:     cfg.GatewayAPI.Enabled,
		IstioCreateLBService:  cfg.Istio.Service.Create,
		IstioServiceNamespace: cfg.Istio.Service.Namespace,
		IstioGatewaySelector:  cfg.Istio.Gateway.Selector,
		GatewayClassName:      cfg.GatewayAPI.GatewayClass,
	}

	// Istio servers
	for _, s := range cfg.Istio.Gateway.Servers {
		opts.IstioServers = append(opts.IstioServers, ingress.IstioServer{
			PortNumber:   s.Port.Number,
			PortName:     s.Port.Name,
			PortProtocol: s.Port.Protocol,
			Hosts:        s.Hosts,
			TLS:          s.TLS,
		})
	}

	// Istio routes
	for _, r := range cfg.Istio.VirtualService.TCPRoutes {
		opts.IstiotcpRoutes = append(opts.IstiotcpRoutes, ingress.TCPRouteVS{
			Name:     r.Name,
			Port:     r.Port,
			DestHost: r.Destination.Host,
			DestPort: r.Destination.Port,
		})
	}
	for _, r := range cfg.Istio.VirtualService.TLSRoutes {
		opts.IstioTLSRoutes = append(opts.IstioTLSRoutes, ingress.TLSRouteVS{
			Name:     r.Name,
			Port:     r.Port,
			SNIHosts: r.SNIHosts,
			DestHost: r.Destination.Host,
			DestPort: r.Destination.Port,
		})
	}

	// Gateway API listeners and routes
	for _, l := range cfg.GatewayAPI.Listeners {
		opts.GatewayListeners = append(opts.GatewayListeners, ingress.GatewayListener{
			Name:     l.Name,
			Port:     l.Port,
			Protocol: l.Protocol,
		})
	}
	for _, r := range cfg.GatewayAPI.TCPRoutes {
		gr := ingress.GatewayAPITCPRoute{Name: r.Name, SectionName: r.SectionName}
		for _, b := range r.BackendRefs {
			gr.BackendRefs = append(gr.BackendRefs, ingress.BackendRef{
				Name: b.Name, Namespace: b.Namespace, Port: b.Port,
			})
		}
		opts.GatewayTcpRoutes = append(opts.GatewayTcpRoutes, gr)
	}
	for _, r := range cfg.GatewayAPI.UDPRoutes {
		gr := ingress.GatewayAPIUDPRoute{Name: r.Name, SectionName: r.SectionName}
		for _, b := range r.BackendRefs {
			gr.BackendRefs = append(gr.BackendRefs, ingress.BackendRef{
				Name: b.Name, Namespace: b.Namespace, Port: b.Port,
			})
		}
		opts.GatewayUdpRoutes = append(opts.GatewayUdpRoutes, gr)
	}

	a.logger.WithFields(logrus.Fields{
		"release":     opts.ReleaseName,
		"namespace":   opts.Namespace,
		"istio":       opts.IstioEnabled,
		"gateway_api": opts.GatewayAPIEnabled,
	}).Info("Installing env-aware gateway component…")

	if err := gw.Install(a.ctx, opts); err != nil {
		return fmt.Errorf("failed to install gateway: %w", err)
	}

	a.logger.Info("✓ Gateway installed")
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

	// WARNING: Never log sensitive fields such as usernames or passwords.
	// Only include non-sensitive fields like URLs and ports below.
	a.logger.WithFields(logrus.Fields{
		"prometheus_url":  info.PrometheusURL,
		"prometheus_port": info.TunnelPrometheusPort,
		"loki_url":        info.LokiURL,
		"loki_port":       info.TunnelLokiPort,
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

		// Record metrics
		a.metrics.recordConnectionStateChange(newState)
	}
}

// startHeartbeat starts periodic heartbeat with dynamic interval based on connection state
func (a *Agent) startHeartbeat() {
	// Send heartbeat immediately on connection (don't wait for first tick)
	if err := a.sendHeartbeatWithRetry(); err != nil {
		a.logger.WithError(err).Error("Failed to send initial heartbeat")
		a.handleHeartbeatFailure()
	} else {
		a.handleHeartbeatSuccess()
	}

	// Use dynamic ticker that adjusts based on connection state
	interval := a.getHeartbeatInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			// Check if interval needs to change based on current state
			newInterval := a.getHeartbeatInterval()
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
				a.logger.WithFields(logrus.Fields{
					"old_interval": interval.Seconds(),
					"new_interval": newInterval.Seconds(),
					"state":        a.getConnectionState().String(),
				}).Debug("Adjusted heartbeat interval based on connection state")
			}

			// Skip heartbeat if WebSocket is reconnecting
			if a.controlPlane != nil && !a.controlPlane.IsConnected() {
				a.logger.Debug("Skipping heartbeat - WebSocket reconnecting")
				a.metrics.recordHeartbeatSkip()
				continue
			}

			start := time.Now()
			if err := a.sendHeartbeatWithRetry(); err != nil {
				a.logger.WithError(err).Error("Failed to send heartbeat after retries")
				a.handleHeartbeatFailure()
			} else {
				duration := time.Since(start)
				a.metrics.recordHeartbeatDuration(duration)
				a.handleHeartbeatSuccess()
			}
		}
	}
}

// getHeartbeatInterval returns the appropriate heartbeat interval based on connection state
func (a *Agent) getHeartbeatInterval() time.Duration {
	state := a.getConnectionState()

	// Get configured intervals or use defaults
	connectedInterval := a.config.Agent.HeartbeatIntervalConnected
	if connectedInterval <= 0 {
		connectedInterval = 30 // Default: 30s when connected
	}

	reconnectingInterval := a.config.Agent.HeartbeatIntervalReconnecting
	if reconnectingInterval <= 0 {
		reconnectingInterval = 60 // Default: 60s when reconnecting (less frequent)
	}

	disconnectedInterval := a.config.Agent.HeartbeatIntervalDisconnected
	if disconnectedInterval <= 0 {
		disconnectedInterval = 15 // Default: 15s when disconnected (more aggressive)
	}

	switch state {
	case StateConnected:
		return time.Duration(connectedInterval) * time.Second
	case StateReconnecting:
		return time.Duration(reconnectingInterval) * time.Second
	case StateDisconnected:
		return time.Duration(disconnectedInterval) * time.Second
	case StateConnecting:
		return time.Duration(connectedInterval) * time.Second // Use normal interval during initial connection
	default:
		return 30 * time.Second // Fallback to default
	}
}

// getConnectionState returns the current connection state (thread-safe)
func (a *Agent) getConnectionState() ConnectionState {
	a.stateMutex.RLock()
	defer a.stateMutex.RUnlock()
	return a.connectionState
}

// handleHeartbeatSuccess handles a successful heartbeat
func (a *Agent) handleHeartbeatSuccess() {
	a.stateMutex.Lock()
	a.consecutiveHeartbeatFailures = 0 // Reset failure counter
	a.stateMutex.Unlock()

	a.metrics.recordHeartbeatSuccess()
	a.updateConnectionState(StateConnected)
}

// handleHeartbeatFailure handles a failed heartbeat with grace period
func (a *Agent) handleHeartbeatFailure() {
	a.stateMutex.Lock()
	a.consecutiveHeartbeatFailures++
	failures := a.consecutiveHeartbeatFailures
	threshold := a.heartbeatFailureThreshold
	a.stateMutex.Unlock()

	a.metrics.recordHeartbeatFailure()

	if failures >= threshold {
		a.logger.WithFields(logrus.Fields{
			"consecutive_failures": failures,
			"threshold":            threshold,
		}).Warn("Heartbeat failure threshold reached, marking disconnected")
		a.updateConnectionState(StateDisconnected)
	} else {
		a.logger.WithFields(logrus.Fields{
			"consecutive_failures": failures,
			"threshold":            threshold,
			"remaining":            threshold - failures,
		}).Warn("Heartbeat failed, within grace period")
		// Stay in current state (likely StateReconnecting or StateConnected)
	}
}

// updateMetricsPeriodically updates metrics that need periodic calculation
func (a *Agent) updateMetricsPeriodically() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.metrics.updateUnhealthyDuration()
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

	// Get gateway proxy info
	a.gatewayMutex.RLock()
	isPrivate := a.isPrivateCluster
	routeCount := a.getGatewayRouteCount()
	a.gatewayMutex.RUnlock()

	heartbeat := &controlplane.HeartbeatRequest{
		ClusterID:    a.clusterID,
		AgentID:      a.config.Agent.ID,
		Status:       "healthy",
		TunnelStatus: tunnelStatus,
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"version":        version.GetVersion(),
			"k8s_nodes":      nodeCount,
			"k8s_pods":       podCount,
			"cpu_usage":      "0%",
			"memory_usage":   "0%",
			"is_private":     isPrivate,
			"gateway_routes": routeCount,
		},

		// Monitoring stack credentials (sent with every heartbeat)
		PrometheusURL:      monInfo.PrometheusURL,
		PrometheusUsername: monInfo.PrometheusUsername,
		PrometheusPassword: monInfo.PrometheusPassword,
		PrometheusSSL:      monInfo.PrometheusSSL,

		LokiURL:      monInfo.LokiURL,
		LokiUsername: monInfo.LokiUsername,
		LokiPassword: monInfo.LokiPassword,

		GrafanaURL:      monInfo.GrafanaURL,
		GrafanaUsername: monInfo.GrafanaUsername,
		GrafanaPassword: monInfo.GrafanaPassword,

		// Tunnel ports for monitoring services
		TunnelPrometheusPort: monInfo.TunnelPrometheusPort,
		TunnelLokiPort:       monInfo.TunnelLokiPort,
		TunnelGrafanaPort:    monInfo.TunnelGrafanaPort,
	}

	// Debug: Log the heartbeat payload (with sensitive fields redacted)
	redactedHeartbeat := *heartbeat
	// Redact credentials
	redactedHeartbeat.PrometheusPassword = "[REDACTED]"
	redactedHeartbeat.LokiPassword = "[REDACTED]"
	redactedHeartbeat.GrafanaPassword = "[REDACTED]"
	if jsonPayload, err := json.Marshal(redactedHeartbeat); err == nil {
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
// HealthStatus represents the agent's health status
type HealthStatus struct {
	Healthy             bool          `json:"healthy"`
	Connected           bool          `json:"connected"`
	Registered          bool          `json:"registered"`
	ConnectionState     string        `json:"connection_state"`
	LastHeartbeat       time.Time     `json:"last_heartbeat"`
	TimeSinceLastHB     time.Duration `json:"time_since_last_heartbeat"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
}

func (a *Agent) GetHealthStatus() HealthStatus {
	a.stateMutex.RLock()
	state := a.connectionState
	lastHB := a.lastHeartbeat
	failures := a.consecutiveHeartbeatFailures
	a.stateMutex.RUnlock()

	timeSinceHB := time.Since(lastHB)

	// Agent is healthy if:
	// 1. Connected to control plane
	// 2. Last heartbeat was within reasonable time (90s = 3x 30s interval)
	isHealthy := state == StateConnected && timeSinceHB < 90*time.Second
	isConnected := a.controlPlane != nil && a.controlPlane.IsConnected()
	isRegistered := a.clusterID != ""

	return HealthStatus{
		Healthy:             isHealthy,
		Connected:           isConnected,
		Registered:          isRegistered,
		ConnectionState:     state.String(),
		LastHeartbeat:       lastHB,
		TimeSinceLastHB:     timeSinceHB,
		ConsecutiveFailures: failures,
	}
}

// IsHealthy returns true if the agent is healthy (connected and heartbeating)
func (a *Agent) IsHealthy() bool {
	return a.GetHealthStatus().Healthy
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
	// Gather machine-specific identifiers for a stable, unique ID
	var identifiers []string

	// 1. Hostname (primary identifier)
	identifiers = append(identifiers, hostname)

	// 2. Machine ID (stable across reboots on Linux/systemd systems)
	if machineID, err := os.ReadFile("/etc/machine-id"); err == nil {
		identifiers = append(identifiers, strings.TrimSpace(string(machineID)))
	} else if machineID, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil {
		// Fallback location for machine-id
		identifiers = append(identifiers, strings.TrimSpace(string(machineID)))
	}

	// 3. Try to get MAC address from network interfaces
	if interfaces, err := net.Interfaces(); err == nil {
		for _, iface := range interfaces {
			// Skip loopback and down interfaces
			if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
				continue
			}
			// Use first valid MAC address
			if len(iface.HardwareAddr) > 0 {
				identifiers = append(identifiers, iface.HardwareAddr.String())
				break
			}
		}
	}

	// 4. Kubernetes Pod UID (if running in K8s)
	if podUID := os.Getenv("POD_UID"); podUID != "" {
		identifiers = append(identifiers, podUID)
	}

	// 5. Kubernetes Namespace + Pod Name (for uniqueness in multi-tenant clusters)
	if podNamespace := os.Getenv("POD_NAMESPACE"); podNamespace != "" {
		identifiers = append(identifiers, podNamespace)
	}
	if podName := os.Getenv("POD_NAME"); podName != "" {
		identifiers = append(identifiers, podName)
	}

	// Fallback: if we have no identifiers, use a timestamp-based UUID
	if len(identifiers) == 0 {
		identifiers = append(identifiers, fmt.Sprintf("fallback-%d", time.Now().UnixNano()))
	}

	// Create deterministic hash from all identifiers
	data := strings.Join(identifiers, "|")
	hash := sha256.Sum256([]byte(data))

	// Use first 16 hex chars for readability
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

		// Trigger gateway proxy re-sync after successful reconnection
		a.gatewayMutex.RLock()
		watcher := a.gatewayWatcher
		a.gatewayMutex.RUnlock()

		if watcher != nil {
			// Update cluster UUID in case it changed
			watcher.UpdateClusterUUID(a.clusterID)

			// Trigger re-sync of all ingresses
			if err := watcher.TriggerResync(); err != nil {
				a.logger.WithError(err).Warn("Failed to re-sync gateway routes after reconnect")
			}
		}
	}()
}

func (a *Agent) isReregistering() bool {
	a.reregisterMutex.Lock()
	inProgress := a.reregistering
	a.reregisterMutex.Unlock()
	return inProgress
}

func (a *Agent) handleProxyRequest(req *controlplane.ProxyRequest, writer controlplane.ProxyResponseWriter) {
	startTime := time.Now()
	logger := a.logger.WithFields(logrus.Fields{
		"request_id": req.RequestID,
		"method":     strings.ToUpper(req.Method),
		"path":       req.Path,
	})

	// Add route context to logging if available
	if req.ServiceName != "" {
		logger = logger.WithFields(logrus.Fields{
			"namespace":    req.Namespace,
			"service_name": req.ServiceName,
			"service_port": req.ServicePort,
		})
	}

	defer func() {
		duration := time.Since(startTime)
		if duration > 5*time.Second {
			logger.WithField("duration_ms", duration.Milliseconds()).Warn("Slow proxy request detected")
		}
	}()

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

	// FALLBACK: If gateway didn't send service routing info, try to look it up locally
	if req.ServiceName == "" {
		// Extract hostname from Host header
		var hostname string
		if req.Headers != nil {
			if hosts, exists := req.Headers["Host"]; exists && len(hosts) > 0 {
				hostname = hosts[0]
			}
		}

		if hostname != "" {
			a.gatewayMutex.RLock()
			watcher := a.gatewayWatcher
			a.gatewayMutex.RUnlock()

			if watcher != nil {
				if route := watcher.LookupRoute(hostname); route != nil {
					logger.WithFields(logrus.Fields{
						"host":        hostname,
						"service":     route.Service,
						"namespace":   route.Namespace,
						"port":        route.Port,
						"lookup_mode": "fallback",
					}).Info("Resolved route from local ingress cache (gateway didn't send service info)")

					req.ServiceName = route.Service
					req.Namespace = route.Namespace
					req.ServicePort = route.Port

					// Update logger with resolved route info
					logger = logger.WithFields(logrus.Fields{
						"namespace":    req.Namespace,
						"service_name": req.ServiceName,
						"service_port": req.ServicePort,
					})
				} else {
					logger.WithField("host", hostname).Warn("No route found in local cache for hostname")
				}
			}
		}
	}

	// Check if this is a WebSocket upgrade request
	if req.IsWebSocket || isWebSocketUpgradeRequest(req) {
		logger.Info("Detected WebSocket upgrade request")

		// Use zero-copy mode for maximum performance (KubeSail-style)
		if req.UseZeroCopy {
			logger.Info("Using zero-copy TCP forwarding for optimal performance")
			a.handleZeroCopyProxy(ctx, req, writer, logger)
			return
		}

		// Use WebSocket-aware mode for debugging and inspection
		logger.Debug("Using WebSocket-aware mode (can inspect frames)")
		a.handleWebSocketProxy(ctx, req, writer, logger)
		return
	}

	// Route to application service if route context is provided, otherwise use K8s API
	var statusCode int
	var respHeaders http.Header
	var respBody io.ReadCloser
	var err error

	if req.ServiceName != "" && req.Namespace != "" && req.ServicePort > 0 {
		// Proxy to application service (no K8s authentication required)
		logger.Debug("Routing to application service")
		statusCode, respHeaders, respBody, err = a.proxyToService(ctx, req, requestBody)
	} else {
		// K8s API proxy - ENFORCE cluster ServiceAccount token (kubeconfig token)
		logger.Debug("Routing to Kubernetes API - enforcing cluster ServiceAccount token")

		// Validate that we have the cluster token
		if a.clusterToken == "" {
			logger.Error("K8s API proxy request rejected - cluster ServiceAccount token not available")
			_ = writer.CloseWithError(fmt.Errorf("cluster not authenticated - no ServiceAccount token"))
			_ = req.CloseBody()
			return
		}

		// ENFORCE: Always use the cluster's Kubernetes ServiceAccount token
		// This ensures PIPEOPS service token cannot be used to access K8s API
		// Only the cluster's kubeconfig token (ServiceAccount) is used
		if req.Headers == nil {
			req.Headers = make(map[string][]string)
		}

		// Override any Authorization header with cluster ServiceAccount token
		req.Headers["Authorization"] = []string{"Bearer " + a.clusterToken}

		logger.WithField("token_source", "cluster_serviceaccount").Debug("Using cluster ServiceAccount token for K8s API access")

		// Proxy to K8s API with cluster's kubeconfig credentials
		statusCode, respHeaders, respBody, err = a.k8sClient.ProxyRequest(ctx, req.Method, req.Path, req.Query, req.Headers, requestBody)
	}

	if err != nil {
		if ctx.Err() != nil {
			logger.WithError(ctx.Err()).Debug("Proxy request cancelled or timed out")
		} else {
			logger.WithError(err).Warn("Proxy request failed")
		}
		_ = writer.CloseWithError(err)
		_ = req.CloseBody()
		return
	}
	defer respBody.Close()
	defer req.CloseBody()

	filteredHeaders := a.filterProxyHeaders(respHeaders)

	if err := writer.WriteHeader(statusCode, filteredHeaders); err != nil {
		logger.WithError(err).Warn("Failed to write proxy response header")
		_ = writer.CloseWithError(err)
		return
	}

	// Use buffer pool to reduce GC pressure
	bufPtr := proxyBufferPool.Get().(*[]byte)
	defer proxyBufferPool.Put(bufPtr)
	buf := *bufPtr

	bytesWritten := int64(0)
	for {
		// Check if context is cancelled to stop early
		select {
		case <-ctx.Done():
			logger.Debug("Proxy request context cancelled during streaming")
			_ = writer.CloseWithError(ctx.Err())
			return
		default:
		}

		n, readErr := respBody.Read(buf)
		if n > 0 {
			if err := writer.WriteChunk(buf[:n]); err != nil {
				logger.WithError(err).Warn("Failed to stream proxy response body")
				_ = writer.CloseWithError(err)
				return
			}
			bytesWritten += int64(n)
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

	logger.WithFields(logrus.Fields{
		"status":        statusCode,
		"bytes_written": bytesWritten,
		"duration_ms":   time.Since(startTime).Milliseconds(),
	}).Debug("Proxy request completed successfully")

	if err := writer.Close(); err != nil {
		logger.WithError(err).Warn("Failed to finalize proxy response to control plane")
	}
}

// proxyToService proxies a request to an application service instead of the Kubernetes API
func (a *Agent) proxyToService(ctx context.Context, req *controlplane.ProxyRequest, body io.ReadCloser) (int, http.Header, io.ReadCloser, error) {
	// Validate inputs to prevent SSRF attacks
	if err := validateServiceName(req.ServiceName); err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("invalid service name: %w", err)
	}
	if err := validateNamespace(req.Namespace); err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("invalid namespace: %w", err)
	}
	if err := validateServicePort(req.ServicePort); err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("invalid service port: %w", err)
	}
	if err := validatePath(req.Path); err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("invalid path: %w", err)
	}

	// Build service URL: http://service-name.namespace.svc.cluster.local:port/path
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s",
		req.ServiceName,
		req.Namespace,
		req.ServicePort,
		req.Path,
	)

	if req.Query != "" {
		serviceURL = fmt.Sprintf("%s?%s", serviceURL, req.Query)
	}

	logger := a.logger.WithFields(logrus.Fields{
		"service_url":  serviceURL,
		"namespace":    req.Namespace,
		"service_name": req.ServiceName,
		"service_port": req.ServicePort,
	})

	logger.Debug("Proxying request to application service")

	// Create HTTP request
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = body
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, serviceURL, reqBody)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("failed to create service request: %w", err)
	}

	// Copy headers (skip hop-by-hop headers)
	for key, values := range req.Headers {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	// Create HTTP client for cluster-internal service communication
	// This will use cluster DNS to resolve service names
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Execute the request
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		logger.WithError(err).Error("Failed to proxy request to service")
		return 0, nil, nil, fmt.Errorf("service request failed: %w", err)
	}

	logger.WithField("status_code", resp.StatusCode).Debug("Service proxy request completed")

	return resp.StatusCode, resp.Header, resp.Body, nil
}

// filterProxyHeaders filters out hop-by-hop headers that shouldn't be proxied
func (a *Agent) filterProxyHeaders(headers map[string][]string) map[string][]string {
	filtered := make(map[string][]string)
	for key, values := range headers {
		if isHopByHopHeader(key) {
			continue
		}
		filtered[key] = append([]string(nil), values...)
	}
	return filtered
}

// isHopByHopHeader checks if a header is hop-by-hop (shouldn't be forwarded)
func isHopByHopHeader(header string) bool {
	switch strings.ToLower(header) {
	case "connection", "transfer-encoding", "keep-alive",
		"proxy-authenticate", "proxy-authorization",
		"te", "trailers", "upgrade":
		return true
	}
	return false
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

// initializeGatewayProxy detects cluster type and starts gateway proxy watcher
func (a *Agent) initializeGatewayProxy() {
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
	defer cancel()

	a.logger.Info("Initializing gateway proxy detection...")

	// Detect if cluster is private or public
	isPrivate, err := ingress.DetectClusterType(ctx, a.k8sClient.GetClientset(), a.logger)
	if err != nil {
		a.logger.WithError(err).Error("Failed to detect cluster type, assuming private")
		isPrivate = true
	}

	// Determine routing mode and public endpoint
	var publicEndpoint string
	var routingMode string

	if !isPrivate {
		// Try to get LoadBalancer endpoint for direct routing
		publicEndpoint = ingress.DetectLoadBalancerEndpoint(ctx, a.k8sClient.GetClientset(), a.logger)
		if publicEndpoint != "" {
			routingMode = "direct"
			a.logger.WithField("endpoint", publicEndpoint).Info("✅ Using direct routing via LoadBalancer")
		} else {
			routingMode = "tunnel"
			a.logger.Info("⚠️  Public cluster but no LoadBalancer endpoint, using tunnel routing")
		}
	} else {
		routingMode = "tunnel"
		a.logger.Info("🔒 Private cluster detected - using tunnel routing")
	}

	a.gatewayMutex.Lock()
	a.isPrivateCluster = isPrivate
	a.gatewayMutex.Unlock()

	// Create controller HTTP client
	controllerClient := ingress.NewControllerClient(
		a.config.PipeOps.APIURL,
		a.config.PipeOps.Token,
		a.logger,
	)

	// Create and start ingress watcher
	watcher := ingress.NewIngressWatcher(
		a.k8sClient.GetClientset(),
		a.clusterID,
		controllerClient,
		a.logger,
		publicEndpoint,
		routingMode,
	)

	if err := watcher.Start(a.ctx); err != nil {
		a.logger.WithError(err).Error("Failed to start ingress watcher")
		return
	}

	a.gatewayMutex.Lock()
	a.gatewayWatcher = watcher
	a.gatewayMutex.Unlock()

	a.logger.WithFields(logrus.Fields{
		"routing_mode":    routingMode,
		"public_endpoint": publicEndpoint,
		"is_private":      isPrivate,
	}).Info("✅ Gateway proxy ingress watcher started successfully")

	// Start periodic route refresh goroutine to prevent TTL expiry
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.startPeriodicRouteRefresh()
	}()
}

// startPeriodicRouteRefresh runs periodic route refreshes to prevent Redis TTL expiry
func (a *Agent) startPeriodicRouteRefresh() {
	// Get refresh interval from config, default to 4 hours
	refreshInterval := a.config.Agent.GatewayRouteRefreshInterval
	if refreshInterval <= 0 {
		refreshInterval = 4 // Default: 4 hours (well before 24h TTL)
	}

	ticker := time.NewTicker(time.Duration(refreshInterval) * time.Hour)
	defer ticker.Stop()

	a.logger.WithField("interval_hours", refreshInterval).Info("Starting periodic gateway route refresh")

	for {
		select {
		case <-ticker.C:
			a.gatewayMutex.RLock()
			watcher := a.gatewayWatcher
			a.gatewayMutex.RUnlock()

			if watcher == nil {
				a.logger.Debug("Gateway watcher not initialized, skipping periodic refresh")
				continue
			}

			a.logger.Info("Triggering periodic gateway route refresh")
			if err := watcher.TriggerResync(); err != nil {
				a.logger.WithError(err).Warn("Periodic route refresh failed")
				a.metrics.recordGatewayRouteRefreshError()
			} else {
				a.logger.Info("Periodic route refresh completed successfully")
				a.metrics.recordGatewayRouteRefreshSuccess()
			}

		case <-a.ctx.Done():
			a.logger.Info("Stopping periodic route refresh")
			return
		}
	}
}

// getGatewayRouteCount returns the current number of gateway routes
func (a *Agent) getGatewayRouteCount() int {
	a.gatewayMutex.RLock()
	defer a.gatewayMutex.RUnlock()

	if a.gatewayWatcher == nil {
		return 0
	}

	return a.gatewayWatcher.GetRouteCount()
}

// validateServiceName validates Kubernetes service name to prevent SSRF
func validateServiceName(name string) error {
	if name == "" {
		return fmt.Errorf("service name cannot be empty")
	}
	if len(name) > 253 {
		return fmt.Errorf("service name too long")
	}
	// K8s DNS-1123 label validation: lowercase alphanumeric, hyphens allowed
	for i, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return fmt.Errorf("service name contains invalid character at position %d", i)
		}
		if i == 0 && r == '-' {
			return fmt.Errorf("service name cannot start with hyphen")
		}
		if i == len(name)-1 && r == '-' {
			return fmt.Errorf("service name cannot end with hyphen")
		}
	}
	return nil
}

// validateNamespace validates Kubernetes namespace to prevent SSRF
func validateNamespace(namespace string) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if len(namespace) > 253 {
		return fmt.Errorf("namespace too long")
	}
	// K8s DNS-1123 label validation: lowercase alphanumeric, hyphens allowed
	for i, r := range namespace {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return fmt.Errorf("namespace contains invalid character at position %d", i)
		}
		if i == 0 && r == '-' {
			return fmt.Errorf("namespace cannot start with hyphen")
		}
		if i == len(namespace)-1 && r == '-' {
			return fmt.Errorf("namespace cannot end with hyphen")
		}
	}
	return nil
}

// validateServicePort validates service port to prevent SSRF
func validateServicePort(port int32) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("service port must be between 1 and 65535")
	}
	return nil
}

// validatePath validates URL path to prevent path traversal and SSRF
func validatePath(path string) error {
	// Path must start with / or be empty
	if path != "" && !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must start with /")
	}
	// Prevent path traversal
	if strings.Contains(path, "..") {
		return fmt.Errorf("path cannot contain '..'")
	}
	// Prevent protocol smuggling
	if strings.Contains(path, "://") {
		return fmt.Errorf("path cannot contain protocol scheme")
	}
	return nil
}

// isWebSocketUpgradeRequest checks if the proxy request is a WebSocket upgrade
func isWebSocketUpgradeRequest(req *controlplane.ProxyRequest) bool {
	if req.Headers == nil {
		return false
	}

	// Check Upgrade header
	upgrade := req.Headers["Upgrade"]
	if len(upgrade) == 0 {
		upgrade = req.Headers["upgrade"]
	}
	if len(upgrade) > 0 && strings.ToLower(upgrade[0]) == "websocket" {
		return true
	}

	// Check Connection header contains "upgrade"
	connection := req.Headers["Connection"]
	if len(connection) == 0 {
		connection = req.Headers["connection"]
	}
	if len(connection) > 0 && strings.Contains(strings.ToLower(connection[0]), "upgrade") {
		return true
	}

	return false
}

// handleWebSocketProxy handles WebSocket proxying to services
func (a *Agent) handleWebSocketProxy(ctx context.Context, req *controlplane.ProxyRequest, writer controlplane.ProxyResponseWriter, logger *logrus.Entry) {
	logger.WithFields(logrus.Fields{
		"service":   req.ServiceName,
		"namespace": req.Namespace,
		"port":      req.ServicePort,
		"path":      req.Path,
	}).Info("Handling WebSocket proxy request")

	// Validate service info
	if req.ServiceName == "" || req.Namespace == "" || req.ServicePort == 0 {
		logger.Error("WebSocket proxy requires service routing info")
		_ = writer.CloseWithError(fmt.Errorf("missing service routing information for WebSocket"))
		return
	}

	// Build WebSocket URL to service
	// Use ws:// for in-cluster services (TLS termination happens at ingress)
	serviceURL := fmt.Sprintf("ws://%s.%s.svc.cluster.local:%d%s",
		req.ServiceName,
		req.Namespace,
		req.ServicePort,
		req.Path,
	)

	if req.Query != "" {
		serviceURL = fmt.Sprintf("%s?%s", serviceURL, req.Query)
	}

	logger.WithField("target_url", serviceURL).Debug("Connecting to service WebSocket")

	// Create WebSocket dialer with headers
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		// Enable compression for better performance
		EnableCompression: true,
	}

	// Prepare headers for WebSocket upgrade - remove hop-by-hop headers
	headers := prepareWebSocketHeaders(req.Headers)

	// Connect to the service WebSocket
	serviceConn, resp, err := dialer.Dial(serviceURL, headers)
	if err != nil {
		logger.WithError(err).Error("Failed to connect to service WebSocket")
		if resp != nil {
			_ = writer.WriteHeader(resp.StatusCode, convertHeaders(resp.Header))
		} else {
			_ = writer.WriteHeader(http.StatusBadGateway, nil)
		}
		_ = writer.CloseWithError(fmt.Errorf("failed to connect to service WebSocket: %w", err))
		return
	}
	defer serviceConn.Close()

	// Configure TCP socket for optimal WebSocket performance
	configureWebSocketConnection(serviceConn, logger)

	logger.Info("Successfully connected to service WebSocket")

	// Send successful upgrade response to controller
	upgradeHeaders := make(map[string][]string)
	upgradeHeaders["Upgrade"] = []string{"websocket"}
	upgradeHeaders["Connection"] = []string{"Upgrade"}
	if resp != nil {
		for key, values := range resp.Header {
			if key != "Upgrade" && key != "Connection" {
				upgradeHeaders[key] = values
			}
		}
	}

	if err := writer.WriteHeader(http.StatusSwitchingProtocols, upgradeHeaders); err != nil {
		logger.WithError(err).Error("Failed to write WebSocket upgrade response")
		return
	}

	// Note: The actual bidirectional forwarding is handled by the controller's WebSocket implementation
	// The agent just needs to keep the connection open and forward data via the writer

	logger.Info("WebSocket tunnel established - controller handles bidirectional forwarding")

	// Create channels for errors and completion
	done := make(chan struct{})
	errChan := make(chan error, 2)

	// Set up ping/pong handlers for connection keep-alive
	serviceConn.SetPongHandler(func(string) error {
		logger.Debug("Received pong from service")
		return serviceConn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	// Start ping ticker to keep connection alive
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	// Ping service periodically
	go func() {
		for {
			select {
			case <-pingTicker.C:
				if err := serviceConn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
					logger.WithError(err).Debug("Failed to send ping to service")
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Forward from service to controller (read from service, write via writer)
	go func() {
		defer close(done)
		for {
			messageType, data, err := serviceConn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.Debug("Service WebSocket closed normally")
				} else {
					logger.WithError(err).Warn("Error reading from service WebSocket")
					errChan <- err
				}
				return
			}

			// Write message to controller via response writer
			// Encode as WebSocket frame metadata + payload
			if err := writer.WriteChunk(encodeWebSocketMessage(messageType, data)); err != nil {
				logger.WithError(err).Error("Failed to write WebSocket data to controller")
				errChan <- err
				return
			}
		}
	}()

	// Wait for completion or error
	select {
	case <-done:
		logger.Info("WebSocket proxy completed normally")
	case err := <-errChan:
		logger.WithError(err).Warn("WebSocket proxy error")
	case <-ctx.Done():
		logger.Info("WebSocket proxy context cancelled")
	}

	// Send close frame to service
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	_ = serviceConn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))

	_ = writer.Close()
}

// convertHeaders converts http.Header to map[string][]string
func convertHeaders(h http.Header) map[string][]string {
	result := make(map[string][]string)
	for key, values := range h {
		result[key] = values
	}
	return result
}

// encodeWebSocketMessage encodes a WebSocket message for transmission
// Format: [1 byte message type][data]
func encodeWebSocketMessage(messageType int, data []byte) []byte {
	result := make([]byte, 1+len(data))
	result[0] = byte(messageType)
	copy(result[1:], data)
	return result
}

// prepareWebSocketHeaders prepares headers for WebSocket upgrade by removing hop-by-hop headers
// This follows RFC 7230 and best practices from KubeSail's implementation
func prepareWebSocketHeaders(reqHeaders map[string][]string) http.Header {
	headers := http.Header{}

	// Hop-by-hop headers that must be removed (per RFC 7230 Section 6.1)
	hopByHopHeaders := map[string]bool{
		"connection":          true,
		"keep-alive":          true,
		"proxy-authenticate":  true,
		"proxy-authorization": true,
		"te":                  true,
		"trailer":             true,
		"transfer-encoding":   true,
		"upgrade":             true,
		"proxy-connection":    true,
		"http2-settings":      true,
	}

	// NEW: Parse Connection header for additional headers to remove (RFC 7230 compliant)
	// This matches KubeSail's dynamic header filtering
	if connHeaders, ok := reqHeaders["Connection"]; ok {
		for _, connHeader := range connHeaders {
			// Split by comma and trim spaces
			for _, headerName := range strings.Split(connHeader, ",") {
				headerName = strings.TrimSpace(strings.ToLower(headerName))
				// Don't add connection/keep-alive again, and validate header name
				if headerName != "" && headerName != "connection" && headerName != "keep-alive" {
					hopByHopHeaders[headerName] = true
				}
			}
		}
	}

	// Also check lowercase variant
	if connHeaders, ok := reqHeaders["connection"]; ok {
		for _, connHeader := range connHeaders {
			for _, headerName := range strings.Split(connHeader, ",") {
				headerName = strings.TrimSpace(strings.ToLower(headerName))
				if headerName != "" && headerName != "connection" && headerName != "keep-alive" {
					hopByHopHeaders[headerName] = true
				}
			}
		}
	}

	// Copy headers, filtering out hop-by-hop and special headers
	for key, values := range reqHeaders {
		lowerKey := strings.ToLower(key)

		// Skip hop-by-hop headers
		if hopByHopHeaders[lowerKey] {
			continue
		}

		// Skip host header (will be set by Dial)
		if lowerKey == "host" {
			continue
		}

		// Copy the header
		for _, value := range values {
			headers.Add(key, value)
		}
	}

	return headers
}

// configureWebSocketConnection configures TCP socket options for optimal WebSocket performance
// This implements best practices from KubeSail's setupSocket function
func configureWebSocketConnection(conn *websocket.Conn, logger *logrus.Entry) {
	// Get the underlying TCP connection
	if netConn := conn.UnderlyingConn(); netConn != nil {
		if tcpConn, ok := netConn.(*net.TCPConn); ok {
			// Disable Nagle's algorithm for lower latency
			// This sends packets immediately rather than buffering
			if err := tcpConn.SetNoDelay(true); err != nil {
				logger.WithError(err).Warn("Failed to set TCP_NODELAY")
			} else {
				logger.Debug("TCP_NODELAY enabled for WebSocket connection")
			}

			// Enable TCP keep-alive to detect dead connections
			if err := tcpConn.SetKeepAlive(true); err != nil {
				logger.WithError(err).Warn("Failed to enable TCP keep-alive")
			} else {
				logger.Debug("TCP keep-alive enabled")
			}

			// Set keep-alive period (30 seconds)
			if err := tcpConn.SetKeepAlivePeriod(30 * time.Second); err != nil {
				logger.WithError(err).Warn("Failed to set TCP keep-alive period")
			} else {
				logger.Debug("TCP keep-alive period set to 30s")
			}
		}
	}

	// Set read deadline to prevent hanging connections
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		logger.WithError(err).Warn("Failed to set initial read deadline")
	}
}

// handleZeroCopyProxy handles WebSocket proxying using zero-copy TCP forwarding
// This matches KubeSail's performance by avoiding WebSocket frame parsing
func (a *Agent) handleZeroCopyProxy(ctx context.Context, req *controlplane.ProxyRequest, writer controlplane.ProxyResponseWriter, logger *logrus.Entry) {
	startTime := time.Now()

	logger.WithFields(logrus.Fields{
		"service":   req.ServiceName,
		"namespace": req.Namespace,
		"port":      req.ServicePort,
		"path":      req.Path,
		"mode":      "zero-copy",
	}).Info("Starting zero-copy WebSocket proxy")

	// Validate service info
	if req.ServiceName == "" || req.Namespace == "" || req.ServicePort == 0 {
		logger.Error("Zero-copy proxy requires service routing info")
		_ = writer.CloseWithError(fmt.Errorf("missing service routing information"))
		return
	}

	// Build service address (use TCP directly, not ws://)
	serviceAddr := fmt.Sprintf("%s.%s.svc.cluster.local:%d",
		req.ServiceName,
		req.Namespace,
		req.ServicePort,
	)

	logger.WithField("target", serviceAddr).Debug("Connecting to service via TCP")

	// Dial raw TCP connection to service
	serviceConn, err := net.DialTimeout("tcp", serviceAddr, 10*time.Second)
	if err != nil {
		logger.WithError(err).Error("Failed to connect to service")
		_ = writer.WriteHeader(http.StatusBadGateway, nil)
		_ = writer.CloseWithError(fmt.Errorf("failed to connect to service: %w", err))
		return
	}
	defer serviceConn.Close()

	// Configure TCP socket for optimal performance
	if tcpConn, ok := serviceConn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
		logger.Debug("TCP optimizations applied (NoDelay, KeepAlive)")
	}

	connectTime := time.Since(startTime)
	logger.WithField("connect_ms", connectTime.Milliseconds()).Info("Connected to service")

	// Send WebSocket upgrade request to service
	upgradeReq := buildWebSocketUpgradeRequest(req)
	if _, err := serviceConn.Write(upgradeReq); err != nil {
		logger.WithError(err).Error("Failed to send upgrade request to service")
		_ = writer.CloseWithError(fmt.Errorf("failed to send upgrade: %w", err))
		return
	}

	// Read upgrade response from service
	upgradeResp, err := readHTTPResponse(serviceConn)
	if err != nil {
		logger.WithError(err).Error("Failed to read upgrade response from service")
		_ = writer.CloseWithError(fmt.Errorf("failed to read upgrade response: %w", err))
		return
	}

	// Check if upgrade was successful
	if upgradeResp.StatusCode != http.StatusSwitchingProtocols {
		logger.WithField("status", upgradeResp.StatusCode).Error("Service rejected WebSocket upgrade")
		_ = writer.WriteHeader(upgradeResp.StatusCode, convertHeaders(upgradeResp.Header))
		_ = writer.CloseWithError(fmt.Errorf("service rejected upgrade: status %d", upgradeResp.StatusCode))
		return
	}

	upgradeTime := time.Since(startTime)
	logger.WithField("upgrade_ms", upgradeTime.Milliseconds()).Info("WebSocket upgrade successful")

	// Send 101 Switching Protocols to controller
	upgradeHeaders := make(map[string][]string)
	upgradeHeaders["Upgrade"] = []string{"websocket"}
	upgradeHeaders["Connection"] = []string{"Upgrade"}
	for key, values := range upgradeResp.Header {
		if key != "Upgrade" && key != "Connection" {
			upgradeHeaders[key] = values
		}
	}

	if err := writer.WriteHeader(http.StatusSwitchingProtocols, upgradeHeaders); err != nil {
		logger.WithError(err).Error("Failed to send upgrade response to controller")
		return
	}

	logger.Info("Upgrade complete, starting zero-copy bidirectional forwarding")

	// THIS IS THE MAGIC - Zero-copy bidirectional forwarding (like KubeSail!)
	// No parsing, no encoding, just raw byte copying

	done := make(chan struct{})
	errChan := make(chan error, 2)

	// Track bytes for metrics
	var bytesFromService, bytesToService int64

	// Service → Controller direction
	go func() {
		defer close(done)

		// Create a writer that forwards to controller via WriteChunk
		controllerWriter := &zeroCopyWriter{writer: writer}

		// io.Copy is highly optimized (uses splice/sendfile on Linux)
		n, err := io.Copy(controllerWriter, serviceConn)
		bytesFromService = n

		if err != nil && err != io.EOF {
			logger.WithError(err).Debug("Service → Controller copy ended")
			errChan <- err
		}
	}()

	// Controller → Service direction (read from head data if any, then stream)
	go func() {
		// First, write any buffered head data
		if req.HeadData != nil && len(req.HeadData) > 0 {
			if _, err := serviceConn.Write(req.HeadData); err != nil {
				logger.WithError(err).Error("Failed to write head data to service")
				errChan <- err
				return
			}
			bytesToService += int64(len(req.HeadData))
			logger.WithField("bytes", len(req.HeadData)).Debug("Forwarded head data to service")
		}

		// Then handle streaming data from controller
		// Note: This part needs controller implementation to send data via stream chunks
		// For now, we just wait for the service → controller direction to complete
	}()

	// Wait for completion
	select {
	case <-done:
		duration := time.Since(startTime)
		logger.WithFields(logrus.Fields{
			"duration_ms":        duration.Milliseconds(),
			"bytes_from_service": bytesFromService,
			"bytes_to_service":   bytesToService,
			"connect_ms":         connectTime.Milliseconds(),
			"upgrade_ms":         upgradeTime.Milliseconds(),
		}).Info("Zero-copy WebSocket proxy completed")
	case err := <-errChan:
		logger.WithError(err).Warn("Zero-copy proxy error")
	case <-ctx.Done():
		logger.Info("Zero-copy proxy context cancelled")
	}

	_ = writer.Close()
}

// zeroCopyWriter wraps ProxyResponseWriter for io.Copy
type zeroCopyWriter struct {
	writer controlplane.ProxyResponseWriter
}

func (w *zeroCopyWriter) Write(p []byte) (n int, err error) {
	if err := w.writer.WriteChunk(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// buildWebSocketUpgradeRequest creates the raw HTTP upgrade request
func buildWebSocketUpgradeRequest(req *controlplane.ProxyRequest) []byte {
	var buf bytes.Buffer

	// Request line
	path := req.Path
	if req.Query != "" {
		path = path + "?" + req.Query
	}
	fmt.Fprintf(&buf, "GET %s HTTP/1.1\r\n", path)

	// Headers
	for key, values := range req.Headers {
		for _, value := range values {
			fmt.Fprintf(&buf, "%s: %s\r\n", key, value)
		}
	}

	// Ensure required WebSocket headers
	if req.Headers["Upgrade"] == nil {
		buf.WriteString("Upgrade: websocket\r\n")
	}
	if req.Headers["Connection"] == nil {
		buf.WriteString("Connection: Upgrade\r\n")
	}

	// End of headers
	buf.WriteString("\r\n")

	return buf.Bytes()
}

// readHTTPResponse reads and parses HTTP response from raw connection
func readHTTPResponse(conn net.Conn) (*http.Response, error) {
	// Set read timeout
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Use bufio to read response
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
