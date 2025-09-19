package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pipeops/pipeops-vm-agent/internal/k8s"
	"github.com/pipeops/pipeops-vm-agent/internal/proxy"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Server represents the VM agent HTTP server
type Server struct {
	config    *types.Config
	k8sClient *k8s.Client
	k8sProxy  *proxy.K8sProxy
	logger    *logrus.Logger

	// HTTP server
	httpServer *http.Server
	router     *gin.Engine

	// Shutdown
	ctx    context.Context
	cancel context.CancelFunc
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

	// Create K8s proxy
	k8sProxy := proxy.NewK8sProxy(k8sClient, logger)

	return &Server{
		config:    config,
		k8sClient: k8sClient,
		k8sProxy:  k8sProxy,
		logger:    logger,
		router:    router,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Start starts the agent server
func (s *Server) Start() error {
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

// authenticateHTTP validates HTTP authentication for FRP communication
func (s *Server) authenticateHTTP(c *gin.Context) bool {
	// Check for Bearer token in Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		s.logger.Warn("HTTP authentication failed: missing Authorization header")
		return false
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.logger.Warn("HTTP authentication failed: invalid Authorization header format")
		return false
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	// For API endpoints, validate against API token
	if token != s.config.PipeOps.Token {
		s.logger.Warn("HTTP authentication failed: invalid token")
		return false
	}

	return true
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	// Health and monitoring endpoints
	s.router.GET("/health", s.handleHealth)
	s.router.GET("/ready", s.handleReady)
	s.router.GET("/version", s.handleVersion)
	s.router.GET("/metrics", s.handleMetrics)

	// Agent API endpoints for FRP communication with Control Plane
	s.router.GET("/api/agent/status", s.handleAgentStatus)
	s.router.POST("/api/agent/heartbeat", s.handleAgentHeartbeat)
	s.router.POST("/api/control-plane/message", s.handleControlPlaneMessage)

	// Runner communication endpoints via FRP
	s.router.POST("/api/runners/:runner_id/command", s.handleRunnerCommand)

	// Kubernetes API Proxy - all HTTP methods for FRP tunnel communication
	s.router.GET("/api/k8s/*path", s.handleK8sProxy)
	s.router.POST("/api/k8s/*path", s.handleK8sProxy)
	s.router.PUT("/api/k8s/*path", s.handleK8sProxy)
	s.router.DELETE("/api/k8s/*path", s.handleK8sProxy)
	s.router.PATCH("/api/k8s/*path", s.handleK8sProxy)
	s.router.OPTIONS("/api/k8s/*path", s.handleK8sProxy)
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
		"proxy":     gin.H{"method": "frp"},
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

// handleAgentStatus handles agent status requests from Control Plane via FRP
func (s *Server) handleAgentStatus(c *gin.Context) {
	if !s.authenticateHTTP(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get cluster status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := s.k8sClient.GetClusterStatus(ctx)
	if err != nil {
		s.logger.WithError(err).Error("Failed to get cluster status")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := gin.H{
		"agent": gin.H{
			"id":      s.config.Agent.ID,
			"name":    s.config.Agent.Name,
			"cluster": s.config.Agent.ClusterName,
			"version": s.config.Agent.Version,
		},
		"cluster":   status,
		"timestamp": time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

// handleAgentHeartbeat handles heartbeat requests from Control Plane via FRP
func (s *Server) handleAgentHeartbeat(c *gin.Context) {
	if !s.authenticateHTTP(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    "alive",
		"timestamp": time.Now(),
	})
}

// handleK8sProxy handles RESTful K8s API proxy requests
func (s *Server) handleK8sProxy(c *gin.Context) {
	// Extract the K8s API path
	k8sPath := c.Param("path")
	if k8sPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API path is required"})
		return
	}

	// Generate a unique request ID
	requestID := fmt.Sprintf("http_%d", time.Now().UnixNano())

	// Convert query parameters
	queryMap := make(map[string][]string)
	for key, values := range c.Request.URL.Query() {
		queryMap[key] = values
	}

	// Create proxy request
	proxyReq := &types.K8sProxyRequest{
		ID:      requestID,
		Method:  c.Request.Method,
		Path:    k8sPath,
		Headers: make(map[string]string),
		Query:   queryMap,
	}

	// Copy headers
	for key, values := range c.Request.Header {
		if len(values) > 0 {
			proxyReq.Headers[key] = values[0]
		}
	}

	// Read body if present
	if c.Request.Body != nil {
		body, err := c.GetRawData()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
			return
		}
		proxyReq.Body = body
	}

	// Perform the proxy request
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	response, err := s.k8sProxy.ProxyRequest(ctx, proxyReq)
	if err != nil {
		s.logger.WithError(err).Error("K8s proxy request failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Set response headers
	for key, value := range response.Headers {
		c.Header(key, value)
	}

	// Return response
	c.Data(response.StatusCode, response.Headers["Content-Type"], response.Body)
}

// handleRunnerCommand handles commands sent to specific runners via FRP
func (s *Server) handleRunnerCommand(c *gin.Context) {
	if !s.authenticateHTTP(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	runnerID := c.Param("runner_id")
	if runnerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runner_id is required"})
		return
	}

	var command map[string]interface{}
	if err := c.ShouldBindJSON(&command); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.logger.WithFields(logrus.Fields{
		"runner_id": runnerID,
		"command":   command,
	}).Info("Received runner command")

	// For now, acknowledge the command
	// In a full implementation, this would forward to the runner
	response := gin.H{
		"status":     "acknowledged",
		"runner_id":  runnerID,
		"timestamp":  time.Now(),
		"command_id": command["id"],
	}

	c.JSON(http.StatusOK, response)
}

// handleControlPlaneMessage handles messages from Control Plane via FRP
func (s *Server) handleControlPlaneMessage(c *gin.Context) {
	if !s.authenticateHTTP(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var message map[string]interface{}
	if err := c.ShouldBindJSON(&message); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	messageType, ok := message["type"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message type is required"})
		return
	}

	s.logger.WithFields(logrus.Fields{
		"type":    messageType,
		"message": message,
	}).Info("Received Control Plane message")

	var response gin.H

	switch messageType {
	case "runner_assigned":
		s.logger.Info("Runner assigned by Control Plane")
		response = gin.H{
			"status":    "acknowledged",
			"timestamp": time.Now(),
		}

	default:
		s.logger.WithField("type", messageType).Warn("Unknown Control Plane message type")
		response = gin.H{
			"error":     "Unknown message type",
			"timestamp": time.Now(),
		}
	}

	// Add message ID if present
	if id, exists := message["id"]; exists {
		response["message_id"] = id
	}

	c.JSON(http.StatusOK, response)
}
