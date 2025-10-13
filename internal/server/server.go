package server

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pipeops/pipeops-vm-agent/internal/k8s"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Server represents the VM agent HTTP server
type Server struct {
	config    *types.Config
	k8sClient *k8s.Client
	logger    *logrus.Logger

	// HTTP server
	httpServer *http.Server
	router     *gin.Engine

	// KubeSail-inspired monitoring
	startTime time.Time
	features  map[string]interface{}
	status    *AgentStatus

	// Tunnel activity recorder
	activityRecorder func()

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
func NewServer(config *types.Config, k8sClient *k8s.Client, logger *logrus.Logger) *Server {
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

	// Initialize features (KubeSail-inspired)
	features := make(map[string]interface{})
	features["real_time_proxy"] = true
	features["k8s_api"] = true
	features["metrics"] = true

	// Initialize status
	status := &AgentStatus{
		Connected:     false,
		Registered:    false,
		LastHeartbeat: time.Now(),
	}

	return &Server{
		config:    config,
		k8sClient: k8sClient,
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

	// Serve static dashboard (for demonstration)
	s.router.Static("/static", "./web")
	s.router.GET("/dashboard", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/static/dashboard.html")
	})

	// FRP endpoints removed - agent now uses custom real-time architecture
}

// handleHealth handles health check requests
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now(),
		"version":   s.config.Agent.Version,
	})
}

// handleReady handles readiness check requests
func (s *Server) handleReady(c *gin.Context) {
	// Check if K8s client is working
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.k8sClient.GetClusterStatus(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "ready",
		"timestamp": time.Now(),
	})
}

// handleMetrics handles metrics requests
func (s *Server) handleMetrics(c *gin.Context) {
	// Get cluster status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := s.k8sClient.GetClusterStatus(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	metrics := gin.H{
		"cluster":   status,
		"proxy":     gin.H{"method": "direct"},
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, metrics)
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

	// Get cluster status
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clusterStatus, err := s.k8sClient.GetClusterStatus(ctx)

	uptime := time.Since(s.startTime)

	health := gin.H{
		"status": "healthy",
		"agent": gin.H{
			"version":    version.GetFullVersion(),
			"uptime":     uptime.String(),
			"start_time": s.startTime,
			"agent_id":   s.config.Agent.ID,
		},
		"cluster": gin.H{
			"connected": err == nil,
			"status":    clusterStatus,
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

	if err != nil {
		health["cluster"].(gin.H)["error"] = err.Error()
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

	// Test Kubernetes API connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.k8sClient.GetClusterStatus(ctx)
	tests["kubernetes_api"] = gin.H{
		"status": err == nil,
		"error":  nil,
	}
	if err != nil {
		tests["kubernetes_api"].(gin.H)["error"] = err.Error()
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

// detectFeatures dynamically detects available features (KubeSail-inspired)
func (s *Server) detectFeatures() {
	// Test Kubernetes API connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := s.k8sClient.GetClusterStatus(ctx)
	s.features["k8s_api"] = err == nil

	// Always available features
	s.features["real_time_proxy"] = true
	s.features["metrics"] = true
	s.features["health_monitoring"] = true
	s.features["runtime_metrics"] = true

	// Feature versioning
	s.features["feature_version"] = "1.0.0"
}

// SetActivityRecorder sets the tunnel activity recorder function
func (s *Server) SetActivityRecorder(recorder func()) {
	s.activityRecorder = recorder
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
