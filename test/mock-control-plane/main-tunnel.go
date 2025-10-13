package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	chserver "github.com/jpillora/chisel/server"
)

// MockControlPlane simulates the PipeOps control plane with Chisel tunnel support
type MockControlPlane struct {
	tunnelManager *TunnelManager
	chiselServer  *chserver.Server
	apiServer     *http.Server
	logger        *log.Logger
}

// TunnelManager manages agent tunnels
type TunnelManager struct {
	tunnels   map[string]*TunnelInfo
	mu        sync.RWMutex
	portStart int
	portEnd   int
	usedPorts map[int]string
}

// TunnelInfo represents tunnel information for an agent
type TunnelInfo struct {
	AgentID       string              `json:"agent_id"`
	Status        string              `json:"status"` // IDLE, REQUIRED, ACTIVE
	Forwards      []ForwardAllocation `json:"forwards,omitempty"`
	Credentials   string              `json:"credentials,omitempty"` // base64 encoded
	LastRequested time.Time           `json:"last_requested"`
}

// ForwardAllocation represents an allocated port forward
type ForwardAllocation struct {
	Name       string `json:"name"`
	RemotePort int    `json:"remote_port"`
}

// NewTunnelManager creates a new tunnel manager
func NewTunnelManager(portStart, portEnd int) *TunnelManager {
	return &TunnelManager{
		tunnels:   make(map[string]*TunnelInfo),
		usedPorts: make(map[int]string),
		portStart: portStart,
		portEnd:   portEnd,
	}
}

// RequestTunnel marks a tunnel as required for an agent
func (tm *TunnelManager) RequestTunnel(agentID string) (*TunnelInfo, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Check if tunnel already exists
	if info, exists := tm.tunnels[agentID]; exists {
		info.Status = "REQUIRED"
		info.LastRequested = time.Now()
		log.Printf("[TunnelManager] Tunnel request for agent %s (existing)", agentID)
		return info, nil
	}

	// Allocate ports for multiple forwards (Portainer-style)
	forwards := []ForwardAllocation{
		{Name: "kubernetes-api", RemotePort: 0},
		{Name: "agent-http", RemotePort: 0},
	}

	// Allocate a port for each forward
	for i := range forwards {
		port, err := tm.allocatePort(agentID)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate port for %s: %w", forwards[i].Name, err)
		}
		forwards[i].RemotePort = port
		log.Printf("[TunnelManager] Allocated port %d for %s (%s)", port, forwards[i].Name, agentID)
	}

	// Generate credentials
	creds := tm.generateCredentials(agentID)

	info := &TunnelInfo{
		AgentID:       agentID,
		Status:        "REQUIRED",
		Forwards:      forwards,
		Credentials:   creds,
		LastRequested: time.Now(),
	}

	tm.tunnels[agentID] = info
	log.Printf("[TunnelManager] New tunnel allocated for agent %s with %d port forwards", agentID, len(forwards))
	return info, nil
}

// GetTunnelStatus returns the current status for an agent
func (tm *TunnelManager) GetTunnelStatus(agentID string) *TunnelInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if info, exists := tm.tunnels[agentID]; exists {
		return info
	}

	// Return IDLE status if no tunnel requested
	return &TunnelInfo{
		AgentID: agentID,
		Status:  "IDLE",
	}
}

// MarkActive marks a tunnel as active
func (tm *TunnelManager) MarkActive(agentID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if info, exists := tm.tunnels[agentID]; exists {
		info.Status = "ACTIVE"
		log.Printf("[TunnelManager] Tunnel for agent %s marked ACTIVE", agentID)
	}
}

// MarkIdle marks a tunnel as idle
func (tm *TunnelManager) MarkIdle(agentID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if info, exists := tm.tunnels[agentID]; exists {
		info.Status = "IDLE"
		log.Printf("[TunnelManager] Tunnel for agent %s marked IDLE", agentID)
	}
}

// allocatePort finds an available port
func (tm *TunnelManager) allocatePort(agentID string) (int, error) {
	for port := tm.portStart; port <= tm.portEnd; port++ {
		if _, used := tm.usedPorts[port]; !used {
			tm.usedPorts[port] = agentID
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports")
}

// generateCredentials generates SSH credentials for agent
func (tm *TunnelManager) generateCredentials(agentID string) string {
	// Simple password for testing
	password := fmt.Sprintf("test-password-%s-%d", agentID, time.Now().Unix())

	// Format: username:password
	creds := fmt.Sprintf("%s:%s", agentID, password)

	// Base64 encode
	return base64.StdEncoding.EncodeToString([]byte(creds))
}

// ValidateCredentials validates agent credentials
func (tm *TunnelManager) ValidateCredentials(username, password string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tunnels[username]
	if !exists {
		log.Printf("[Auth] No tunnel info for agent: %s", username)
		return false
	}

	// Decode stored credentials
	decoded, err := base64.StdEncoding.DecodeString(info.Credentials)
	if err != nil {
		log.Printf("[Auth] Failed to decode credentials: %v", err)
		return false
	}

	expectedCreds := string(decoded)
	providedCreds := fmt.Sprintf("%s:%s", username, password)

	if expectedCreds == providedCreds {
		log.Printf("[Auth] Authentication successful for agent: %s", username)
		// Mark tunnel as active
		go tm.MarkActive(username)
		return true
	}

	log.Printf("[Auth] Authentication failed for agent: %s", username)
	return false
}

// NewMockControlPlane creates a new mock control plane
func NewMockControlPlane() (*MockControlPlane, error) {
	logger := log.New(os.Stdout, "[MockControlPlane] ", log.LstdFlags)

	// Create tunnel manager (ports 8000-8100)
	tunnelManager := NewTunnelManager(8000, 8100)

	// Create Chisel server
	// Note: We'll use dynamic auth via AuthFile or handle it in the API
	// For now, we won't set Auth here as it needs to match the credentials we generate
	chiselConfig := &chserver.Config{
		KeySeed:   "test-seed-for-mock-server",
		Reverse:   true, // Enable reverse tunneling
		KeepAlive: 25 * time.Second,
	}

	chiselServer, err := chserver.NewServer(chiselConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Chisel server: %w", err)
	}

	return &MockControlPlane{
		tunnelManager: tunnelManager,
		chiselServer:  chiselServer,
		logger:        logger,
	}, nil
}

// Start starts the mock control plane
func (cp *MockControlPlane) Start(ctx context.Context) error {
	cp.logger.Println("Starting Mock Control Plane...")

	// Start Chisel tunnel server
	go func() {
		cp.logger.Println("Starting Chisel tunnel server on :8080")
		if err := cp.chiselServer.StartContext(ctx, "0.0.0.0", "8080"); err != nil {
			cp.logger.Printf("Chisel server error: %v", err)
		}
	}()

	// Wait briefly for Chisel server to start
	time.Sleep(1 * time.Second)

	// Setup API server
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	// API routes
	router.GET("/api/agents/:id/tunnel/status", cp.handleTunnelStatus)
	router.POST("/api/agents/:id/tunnel/request", cp.handleTunnelRequest)
	router.POST("/api/agents/:id/tunnel/close", cp.handleTunnelClose)

	// Kubernetes API proxy (for testing through tunnel)
	router.Any("/api/agents/:id/k8s/*path", cp.handleK8sProxy)

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	cp.apiServer = &http.Server{
		Addr:    ":9000",
		Handler: router,
	}

	// Start API server
	go func() {
		cp.logger.Println("Starting API server on :9000")
		if err := cp.apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			cp.logger.Printf("API server error: %v", err)
		}
	}()

	cp.logger.Println("Mock Control Plane started successfully")
	cp.logger.Println("  Chisel Server: :8080")
	cp.logger.Println("  API Server: :9000")
	cp.logger.Println("")
	cp.logger.Println("Usage:")
	cp.logger.Println("  1. Start agent with tunnel enabled")
	cp.logger.Println("  2. Request tunnel: curl -X POST http://localhost:9000/api/agents/test-agent/tunnel/request")
	cp.logger.Println("  3. Agent will poll and create tunnel")
	cp.logger.Println("  4. Make K8s API calls: curl http://localhost:9000/api/agents/test-agent/k8s/api/v1/namespaces")

	return nil
}

// Stop stops the mock control plane
func (cp *MockControlPlane) Stop(ctx context.Context) error {
	cp.logger.Println("Stopping Mock Control Plane...")

	if cp.apiServer != nil {
		if err := cp.apiServer.Shutdown(ctx); err != nil {
			cp.logger.Printf("API server shutdown error: %v", err)
		}
	}

	if cp.chiselServer != nil {
		if err := cp.chiselServer.Close(); err != nil {
			cp.logger.Printf("Chisel server close error: %v", err)
		}
	}

	cp.logger.Println("Mock Control Plane stopped")
	return nil
}

// handleTunnelStatus handles GET /api/agents/:id/tunnel/status
func (cp *MockControlPlane) handleTunnelStatus(c *gin.Context) {
	agentID := c.Param("id")

	info := cp.tunnelManager.GetTunnelStatus(agentID)

	response := map[string]interface{}{
		"status":             info.Status,
		"poll_frequency":     5, // Poll every 5 seconds
		"tunnel_server_addr": "ws://localhost:8080",
		"server_fingerprint": "", // Optional
	}

	if info.Status != "IDLE" {
		response["forwards"] = info.Forwards
		response["credentials"] = info.Credentials
	}

	c.JSON(http.StatusOK, response)
}

// handleTunnelRequest handles POST /api/agents/:id/tunnel/request
func (cp *MockControlPlane) handleTunnelRequest(c *gin.Context) {
	agentID := c.Param("id")

	info, err := cp.tunnelManager.RequestTunnel(agentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, info)
}

// handleTunnelClose handles POST /api/agents/:id/tunnel/close
func (cp *MockControlPlane) handleTunnelClose(c *gin.Context) {
	agentID := c.Param("id")

	cp.tunnelManager.MarkIdle(agentID)

	c.JSON(http.StatusOK, gin.H{"message": "tunnel marked for closure"})
}

// handleK8sProxy proxies Kubernetes API requests through tunnel
func (cp *MockControlPlane) handleK8sProxy(c *gin.Context) {
	agentID := c.Param("id")
	k8sPath := c.Param("path")

	info := cp.tunnelManager.GetTunnelStatus(agentID)

	if info.Status != "ACTIVE" {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":  "tunnel not active",
			"status": info.Status,
		})
		return
	}

	// Find the kubernetes-api forward
	var k8sPort int
	for _, fwd := range info.Forwards {
		if fwd.Name == "kubernetes-api" {
			k8sPort = fwd.RemotePort
			break
		}
	}

	if k8sPort == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "kubernetes-api forward not found",
		})
		return
	}

	// Build target URL through tunnel
	targetURL := fmt.Sprintf("http://localhost:%d%s", k8sPort, k8sPath)

	// Create HTTP client
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request
	req, err := http.NewRequest(c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Copy headers
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":      "failed to proxy request through tunnel",
			"details":    err.Error(),
			"target_url": targetURL,
		})
		return
	}
	defer resp.Body.Close()

	// Copy response
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(resp.StatusCode)
	c.Stream(func(w io.Writer) bool {
		io.Copy(w, resp.Body)
		return false
	})
}

func main() {
	// Create mock control plane
	cp, err := NewMockControlPlane()
	if err != nil {
		log.Fatalf("Failed to create mock control plane: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start control plane
	if err := cp.Start(ctx); err != nil {
		log.Fatalf("Failed to start mock control plane: %v", err)
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("\nShutdown signal received...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := cp.Stop(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
