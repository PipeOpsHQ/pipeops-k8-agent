package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

	// WebSocket connections
	controlPlaneConn *websocket.Conn
	runnerConns      map[string]*websocket.Conn
	connsMu          sync.RWMutex

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
		config:      config,
		k8sClient:   k8sClient,
		k8sProxy:    k8sProxy,
		logger:      logger,
		runnerConns: make(map[string]*websocket.Conn),
		router:      router,
		ctx:         ctx,
		cancel:      cancel,
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

	// Close WebSocket connections
	s.connsMu.Lock()
	if s.controlPlaneConn != nil {
		s.controlPlaneConn.Close()
	}
	for _, conn := range s.runnerConns {
		conn.Close()
	}
	s.connsMu.Unlock()

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

// authenticateWebSocket validates WebSocket authentication
func (s *Server) authenticateWebSocket(r *http.Request) bool {
	// Check for Bearer token in Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.logger.Warn("WebSocket authentication failed: missing Authorization header")
		return false
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.logger.Warn("WebSocket authentication failed: invalid Authorization header format")
		return false
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	// For Control Plane connections, validate against control plane token
	if r.URL.Path == "/ws/control-plane" {
		if token != s.config.PipeOps.Token {
			s.logger.Warn("WebSocket authentication failed: invalid control plane token")
			return false
		}
		return true
	}

	// For Runner connections, validate against runner token
	if r.URL.Path == "/ws/runner" || r.URL.Path == "/ws/k8s" {
		// In production, this should validate against the runner token
		// For now, we accept any non-empty token
		if token == "" {
			s.logger.Warn("WebSocket authentication failed: empty runner token")
			return false
		}
		return true
	}

	s.logger.Warn("WebSocket authentication failed: unknown endpoint")
	return false
}

// setupRoutes configures the HTTP routes
func (s *Server) setupRoutes() {
	// Health check
	s.router.GET("/health", s.handleHealth)
	s.router.GET("/ready", s.handleReady)
	s.router.GET("/version", s.handleVersion)

	// Metrics
	s.router.GET("/metrics", s.handleMetrics)

	// WebSocket endpoints
	s.router.GET("/ws/control-plane", s.handleControlPlaneWebSocket)
	s.router.GET("/ws/runner", s.handleRunnerWebSocket)

	// K8s API Proxy
	s.router.GET("/api/k8s/*path", s.handleK8sProxy)
	s.router.POST("/api/k8s/*path", s.handleK8sProxy)
	s.router.PUT("/api/k8s/*path", s.handleK8sProxy)
	s.router.DELETE("/api/k8s/*path", s.handleK8sProxy)
	s.router.PATCH("/api/k8s/*path", s.handleK8sProxy)

	// WebSocket endpoint for K8s API streaming
	s.router.GET("/ws/k8s", func(c *gin.Context) {
		s.k8sProxy.HandleWebSocketConnection(c.Writer, c.Request)
	})
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

	// Add connection metrics
	s.connsMu.RLock()
	runnerCount := len(s.runnerConns)
	controlPlaneConnected := s.controlPlaneConn != nil
	s.connsMu.RUnlock()

	metrics := gin.H{
		"cluster": status,
		"connections": gin.H{
			"control_plane": controlPlaneConnected,
			"runners":       runnerCount,
		},
		"proxy": gin.H{
			"active_streams": s.k8sProxy.GetActiveStreamsCount(),
		},
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

// handleControlPlaneWebSocket handles WebSocket connections from Control Plane
func (s *Server) handleControlPlaneWebSocket(c *gin.Context) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return s.authenticateWebSocket(r)
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.logger.WithError(err).Error("Failed to upgrade Control Plane WebSocket")
		return
	}
	defer conn.Close()

	s.connsMu.Lock()
	s.controlPlaneConn = conn
	s.connsMu.Unlock()

	s.logger.Info("Control Plane WebSocket connected")

	// Handle Control Plane messages
	s.handleControlPlaneMessages(conn)

	s.connsMu.Lock()
	s.controlPlaneConn = nil
	s.connsMu.Unlock()
}

// handleRunnerWebSocket handles WebSocket connections from Runners
func (s *Server) handleRunnerWebSocket(c *gin.Context) {
	runnerID := c.Query("runner_id")
	if runnerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runner_id is required"})
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return s.authenticateWebSocket(r)
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.logger.WithError(err).Error("Failed to upgrade Runner WebSocket")
		return
	}
	defer conn.Close()

	s.connsMu.Lock()
	s.runnerConns[runnerID] = conn
	s.connsMu.Unlock()

	s.logger.WithField("runner_id", runnerID).Info("Runner WebSocket connected")

	// Handle Runner messages
	s.handleRunnerMessages(conn, runnerID)

	s.connsMu.Lock()
	delete(s.runnerConns, runnerID)
	s.connsMu.Unlock()
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

// handleControlPlaneMessages handles messages from Control Plane
func (s *Server) handleControlPlaneMessages(conn *websocket.Conn) {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var msg types.Message
			err := conn.ReadJSON(&msg)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					s.logger.WithError(err).Error("Control Plane WebSocket read error")
				}
				return
			}

			s.logger.WithFields(logrus.Fields{
				"type": msg.Type,
				"id":   msg.ID,
			}).Debug("Received Control Plane message")

			// Handle message based on type
			response := s.processControlPlaneMessage(&msg)
			if response != nil {
				if err := conn.WriteJSON(response); err != nil {
					s.logger.WithError(err).Error("Failed to send response to Control Plane")
				}
			}
		}
	}
}

// handleRunnerMessages handles messages from Runners
func (s *Server) handleRunnerMessages(conn *websocket.Conn, runnerID string) {
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var msg types.Message
			err := conn.ReadJSON(&msg)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					s.logger.WithError(err).WithField("runner_id", runnerID).Error("Runner WebSocket read error")
				}
				return
			}

			s.logger.WithFields(logrus.Fields{
				"type":      msg.Type,
				"id":        msg.ID,
				"runner_id": runnerID,
			}).Debug("Received Runner message")

			// Handle message based on type
			response := s.processRunnerMessage(&msg, runnerID)
			if response != nil {
				if err := conn.WriteJSON(response); err != nil {
					s.logger.WithError(err).WithField("runner_id", runnerID).Error("Failed to send response to Runner")
				}
			}
		}
	}
}

// processControlPlaneMessage processes messages from Control Plane
func (s *Server) processControlPlaneMessage(msg *types.Message) *types.Message {
	switch msg.Type {
	case types.MessageTypeRunnerAssigned:
		// Handle runner assignment
		s.logger.Info("Runner assigned by Control Plane")
		return &types.Message{
			ID:        msg.ID,
			Type:      types.MessageTypeResponse,
			Timestamp: time.Now(),
			Data:      gin.H{"status": "acknowledged"},
		}

	default:
		s.logger.WithField("type", msg.Type).Warn("Unknown Control Plane message type")
		return &types.Message{
			ID:        msg.ID,
			Type:      types.MessageTypeResponse,
			Timestamp: time.Now(),
			Error:     "Unknown message type",
		}
	}
}

// processRunnerMessage processes messages from Runners
func (s *Server) processRunnerMessage(msg *types.Message, runnerID string) *types.Message {
	switch msg.Type {
	case types.MessageTypeHeartbeat:
		return &types.Message{
			ID:        msg.ID,
			Type:      types.MessageTypeResponse,
			Timestamp: time.Now(),
			Data:      gin.H{"status": "alive"},
		}

	case types.MessageTypeStatus:
		// Get cluster status
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		status, err := s.k8sClient.GetClusterStatus(ctx)
		if err != nil {
			return &types.Message{
				ID:        msg.ID,
				Type:      types.MessageTypeResponse,
				Timestamp: time.Now(),
				Error:     err.Error(),
			}
		}

		return &types.Message{
			ID:        msg.ID,
			Type:      types.MessageTypeResponse,
			Timestamp: time.Now(),
			Data:      status,
		}

	default:
		s.logger.WithFields(logrus.Fields{
			"type":      msg.Type,
			"runner_id": runnerID,
		}).Warn("Unknown Runner message type")

		return &types.Message{
			ID:        msg.ID,
			Type:      types.MessageTypeResponse,
			Timestamp: time.Now(),
			Error:     "Unknown message type",
		}
	}
}
