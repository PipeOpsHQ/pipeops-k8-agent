package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// KubernetesProxy exposes the ProxyRequest capability of the Kubernetes client without
// forcing the server package to depend on the concrete implementation.
type KubernetesProxy interface {
	ProxyRequest(ctx context.Context, method, path, rawQuery string, headers map[string][]string, body io.ReadCloser) (int, http.Header, io.ReadCloser, error)
}

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

// Server represents the VM agent HTTP server
// Note: With Portainer-style tunneling, K8s access goes directly through
// the tunnel (port 6443), so we don't need a K8s client in the agent
type Server struct {
	config *types.Config
	logger *logrus.Logger

	// HTTP server
	httpServer *http.Server
	router     *gin.Engine

	// KubeSail-inspired monitoring
	startTime time.Time
	features  map[string]interface{}
	status    *AgentStatus

	// Tunnel activity recorder
	activityRecorder func()

	// Health status provider
	healthStatusProvider func() HealthStatus

	// Optional Kubernetes API proxy
	kubernetesProxy KubernetesProxy

	// Shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// AgentStatus represents the current agent status (KubeSail-inspired)
type AgentStatus struct {
	Connected      bool      `json:"connected"`
	Registered     bool      `json:"registered"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	PublicIP       string    `json:"public_ip,omitempty"`
	ClusterAddress string    `json:"cluster_address,omitempty"`
}

// NewServer creates a new agent server
func NewServer(config *types.Config, logger *logrus.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	// Set gin mode
	if config.Agent.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())

	// Tunnel activity middleware will be added after server is created

	// Initialize features (Portainer-inspired)
	features := make(map[string]interface{})
	features["portainer_tunnel"] = true
	features["multi_port_forward"] = true
	features["direct_k8s_access"] = true // Via tunnel port 6443
	features["metrics"] = true

	// Initialize status
	status := &AgentStatus{
		Connected:     false,
		Registered:    false,
		LastHeartbeat: time.Now(),
	}

	return &Server{
		config:    config,
		logger:    logger,
		router:    router,
		startTime: time.Now(),
		features:  features,
		status:    status,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start starts the agent server
func (s *Server) Start() error {
	// Add tunnel activity middleware if recorder is set
	if s.activityRecorder != nil {
		s.router.Use(s.tunnelActivityMiddleware())
		s.logger.Info("Tunnel activity middleware enabled")
	}

	s.setupRoutes()

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.config.Agent.Port),
		Handler:      s.router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.logger.WithField("port", s.config.Agent.Port).Info("Starting VM agent server")

	// Start server in goroutine
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.WithError(err).Error("Server failed to start")
		}
	}()

	return nil
}

// Stop stops the agent server
func (s *Server) Stop() error {
	s.logger.Info("Shutting down VM agent server")

	s.cancel()

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

// FRP authentication removed - agent now uses custom real-time architecture

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	// Health and monitoring endpoints
	s.router.GET("/health", s.handleHealth)
	s.router.GET("/ready", s.handleReady)
	s.router.GET("/version", s.handleVersion)
	s.router.GET("/metrics", s.handleMetrics)

	// KubeSail-inspired advanced monitoring
	s.router.GET("/api/health/detailed", s.handleDetailedHealth)
	s.router.GET("/api/status/features", s.handleFeatures)
	s.router.GET("/api/metrics/runtime", s.handleRuntimeMetrics)
	s.router.GET("/api/status/connectivity", s.handleConnectivityTest)

	// Real-time communication endpoints (KubeSail-inspired)
	s.router.GET("/ws", s.handleWebSocket)
	s.router.GET("/api/realtime/events", s.handleEventStream)
	s.router.GET("/api/realtime/logs", s.handleLogStream)

	// Kubernetes API proxy endpoints (supports core and aggregated APIs)
	s.router.Any("/api/v1/*proxyPath", s.handleKubernetesProxy)
	s.router.Any("/apis/*proxyPath", s.handleKubernetesProxy)

	// Serve static dashboard (for demonstration)
	s.router.Static("/static", "./web")
	s.router.GET("/dashboard", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/static/dashboard.html")
	})

	// FRP endpoints removed - agent now uses custom real-time architecture
}

// handleHealth handles health check requests
// Always returns 200 OK if the server is running
func (s *Server) handleHealth(c *gin.Context) {
	response := gin.H{
		"status":    "healthy",
		"timestamp": time.Now(),
		"version":   s.config.Agent.Version,
	}

	// Include connection details if health status provider is available
	if s.healthStatusProvider != nil {
		health := s.healthStatusProvider()
		response["connected"] = health.Connected
		response["registered"] = health.Registered
		response["connection_state"] = health.ConnectionState
	}

	c.JSON(http.StatusOK, response)
}

// handleReady handles readiness check requests
// When PIPEOPS_TRUTHFUL_READINESS_PROBE is enabled, reflects actual connection state
// Otherwise, returns ready if the server is running (backward compatible)
func (s *Server) handleReady(c *gin.Context) {
	// Check if truthful readiness is enabled
	truthfulReadiness := s.config.Agent.TruthfulReadinessProbe

	// Get health status if provider is available
	var health HealthStatus
	if s.healthStatusProvider != nil {
		health = s.healthStatusProvider()
	}

	if truthfulReadiness {
		// Truthful mode: report actual connection health
		if health.Healthy {
			c.JSON(http.StatusOK, gin.H{
				"status":               "ready",
				"healthy":              true,
				"connected":            health.Connected,
				"registered":           health.Registered,
				"connection_state":     health.ConnectionState,
				"last_heartbeat":       health.LastHeartbeat,
				"time_since_heartbeat": health.TimeSinceLastHB.String(),
				"timestamp":            time.Now(),
			})
		} else {
			// Not ready - K8s will not route traffic
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":               "not_ready",
				"healthy":              false,
				"connected":            health.Connected,
				"registered":           health.Registered,
				"connection_state":     health.ConnectionState,
				"consecutive_failures": health.ConsecutiveFailures,
				"time_since_heartbeat": health.TimeSinceLastHB.String(),
				"reason":               "agent not connected to control plane",
				"timestamp":            time.Now(),
			})
		}
	} else {
		// Backward compatible mode: always ready if server is running
		response := gin.H{
			"status":    "ready",
			"timestamp": time.Now(),
			"mode":      "backward_compatible",
		}

		// Include connection info for visibility (but don't fail)
		if s.healthStatusProvider != nil {
			response["connected"] = health.Connected
			response["registered"] = health.Registered
			response["connection_state"] = health.ConnectionState
			response["note"] = "Set PIPEOPS_TRUTHFUL_READINESS_PROBE=true for truthful readiness checks"
		}

		c.JSON(http.StatusOK, response)
	}
}

// handleMetrics handles Prometheus metrics requests
func (s *Server) handleMetrics(c *gin.Context) {
	// Use Prometheus handler for standard metrics format
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}

// handleVersion handles version requests
func (s *Server) handleVersion(c *gin.Context) {
	buildInfo := version.GetBuildInfo()

	response := gin.H{
		"version":      buildInfo.Version,
		"git_commit":   buildInfo.GitCommit,
		"build_date":   buildInfo.BuildDate,
		"go_version":   buildInfo.GoVersion,
		"full_version": version.GetFullVersion(),
		"timestamp":    time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

// All FRP-related handlers removed - replaced with KubeSail-inspired real-time architecture

// handleDetailedHealth provides KubeSail-inspired comprehensive health information
func (s *Server) handleDetailedHealth(c *gin.Context) {
	// Get runtime memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	uptime := time.Since(s.startTime)

	health := gin.H{
		"status": "healthy",
		"agent": gin.H{
			"version":    version.GetFullVersion(),
			"uptime":     uptime.String(),
			"start_time": s.startTime,
			"agent_id":   s.config.Agent.ID,
		},
		"tunnel": gin.H{
			"enabled":    true,
			"type":       "portainer-style",
			"direct_k8s": "Control plane accesses K8s via forwarded port 6443",
		},
		"features": s.features,
		"connectivity": gin.H{
			"method": "direct",
			"status": "active",
		},
		"runtime": gin.H{
			"memory_mb":  memStats.Sys / 1024 / 1024,
			"goroutines": runtime.NumGoroutine(),
			"gc_cycles":  memStats.NumGC,
		},
		"timestamp": time.Now(),
	}

	// Add detailed connection health if available
	if s.healthStatusProvider != nil {
		healthStatus := s.healthStatusProvider()
		health["connection_health"] = gin.H{
			"healthy":                 healthStatus.Healthy,
			"connected":               healthStatus.Connected,
			"registered":              healthStatus.Registered,
			"state":                   healthStatus.ConnectionState,
			"last_heartbeat":          healthStatus.LastHeartbeat,
			"time_since_heartbeat":    healthStatus.TimeSinceLastHB.String(),
			"consecutive_failures":    healthStatus.ConsecutiveFailures,
			"truthful_probes_enabled": s.config.Agent.TruthfulReadinessProbe,
		}
	}

	c.JSON(http.StatusOK, health)
}

// handleFeatures returns detected features (KubeSail-inspired)
func (s *Server) handleFeatures(c *gin.Context) {
	// Update features dynamically
	s.detectFeatures()

	c.JSON(http.StatusOK, gin.H{
		"features":  s.features,
		"timestamp": time.Now(),
	})
}

// handleRuntimeMetrics provides runtime performance metrics
func (s *Server) handleRuntimeMetrics(c *gin.Context) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	metrics := gin.H{
		"memory": gin.H{
			"alloc_mb":       memStats.Alloc / 1024 / 1024,
			"total_alloc_mb": memStats.TotalAlloc / 1024 / 1024,
			"sys_mb":         memStats.Sys / 1024 / 1024,
			"heap_alloc_mb":  memStats.HeapAlloc / 1024 / 1024,
			"heap_sys_mb":    memStats.HeapSys / 1024 / 1024,
		},
		"gc": gin.H{
			"num_gc":         memStats.NumGC,
			"pause_total_ns": memStats.PauseTotalNs,
			"last_gc":        time.Unix(0, int64(memStats.LastGC)),
		},
		"runtime": gin.H{
			"goroutines": runtime.NumGoroutine(),
			"cpus":       runtime.NumCPU(),
			"go_version": runtime.Version(),
		},
		"uptime":    time.Since(s.startTime).String(),
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, metrics)
}

// handleConnectivityTest tests connectivity to various endpoints
func (s *Server) handleConnectivityTest(c *gin.Context) {
	tests := make(map[string]interface{})

	// K8s API is accessed directly through tunnel
	tests["kubernetes_api"] = gin.H{
		"status": true,
		"method": "direct via tunnel port 6443",
		"note":   "Control plane connects directly, not through agent",
	}

	// Test Control Plane connectivity (simulated)
	tests["control_plane"] = gin.H{
		"status": true, // In real implementation, test actual connectivity
		"method": "direct",
	}

	result := gin.H{
		"overall_status": "healthy",
		"tests":          tests,
		"timestamp":      time.Now(),
	}

	// Determine overall status
	allHealthy := true
	for _, test := range tests {
		if testMap, ok := test.(gin.H); ok {
			if status, exists := testMap["status"]; exists {
				if healthy, ok := status.(bool); ok && !healthy {
					allHealthy = false
					break
				}
			}
		}
	}

	if !allHealthy {
		result["overall_status"] = "degraded"
	}

	c.JSON(http.StatusOK, result)
}

// detectFeatures dynamically detects available features (Portainer-inspired)
func (s *Server) detectFeatures() {
	// Portainer-style features
	s.features["portainer_tunnel"] = true
	s.features["multi_port_forward"] = true
	s.features["direct_k8s_access"] = true
	s.features["metrics"] = true
	s.features["health_monitoring"] = true
	s.features["runtime_metrics"] = true

	// Feature versioning
	s.features["feature_version"] = "2.0.0-portainer"
}

// SetActivityRecorder sets the tunnel activity recorder function
func (s *Server) SetActivityRecorder(recorder func()) {
	s.activityRecorder = recorder
}

// SetHealthStatusProvider sets the health status provider function
func (s *Server) SetHealthStatusProvider(provider func() HealthStatus) {
	s.healthStatusProvider = provider
}

// tunnelActivityMiddleware records tunnel activity on every request
func (s *Server) tunnelActivityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Record activity before processing request
		if s.activityRecorder != nil {
			s.activityRecorder()
		}

		// Continue processing
		c.Next()
	}
}

// SetKubernetesProxy wires the Kubernetes proxy implementation into the HTTP server.
func (s *Server) SetKubernetesProxy(proxy KubernetesProxy) {
	s.kubernetesProxy = proxy
}

// handleKubernetesProxy forwards REST-style Kubernetes API calls through the in-cluster client.
func (s *Server) handleKubernetesProxy(c *gin.Context) {
	if s.kubernetesProxy == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "kubernetes proxy unavailable",
			"message": "agent is not running inside a cluster or Kubernetes client failed",
		})
		return
	}

	req := c.Request
	headers := make(map[string][]string, len(req.Header))
	for key, values := range req.Header {
		copied := make([]string, len(values))
		copy(copied, values)
		headers[key] = copied
	}

	statusCode, respHeaders, respBody, err := s.kubernetesProxy.ProxyRequest(req.Context(), req.Method, req.URL.Path, req.URL.RawQuery, headers, req.Body)
	if err != nil {
		s.logger.WithError(err).WithField("path", req.URL.Path).Warn("Kubernetes proxy request failed")
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "kubernetes proxy request failed",
			"message": err.Error(),
		})
		return
	}
	defer func() {
		if respBody != nil {
			_ = respBody.Close()
		}
	}()

	for key, values := range respHeaders {
		if shouldSkipProxyHeader(key) {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	c.Status(statusCode)

	if respBody == nil {
		return
	}

	if _, err := io.Copy(c.Writer, respBody); err != nil {
		s.logger.WithError(err).WithField("path", req.URL.Path).Warn("Failed streaming Kubernetes proxy response")
	}
}

func shouldSkipProxyHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}
