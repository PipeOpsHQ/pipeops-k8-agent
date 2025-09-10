package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/internal/k8s"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
)

// K8sProxy handles proxying Kubernetes API requests over WebSocket
type K8sProxy struct {
	k8sClient     *k8s.Client
	logger        *logrus.Logger
	activeStreams map[string]*StreamContext
	streamsMu     sync.RWMutex
	config        *rest.Config
	upgrader      websocket.Upgrader
}

// StreamContext represents an active streaming connection (for WATCH operations)
type StreamContext struct {
	ID       string
	Stream   io.ReadCloser
	Response chan *types.K8sProxyResponse
	Cancel   context.CancelFunc
	Done     chan struct{}
}

// K8sProxyRequest represents a Kubernetes API request from the Runner
type K8sProxyRequest struct {
	ID       string            `json:"id"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     []byte            `json:"body,omitempty"`
	Query    url.Values        `json:"query,omitempty"`
	Stream   bool              `json:"stream,omitempty"`    // For WATCH operations
	StreamID string            `json:"stream_id,omitempty"` // For stream control
}

// K8sProxyResponse represents a response to a Kubernetes API request
type K8sProxyResponse struct {
	ID         string            `json:"id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	Error      string            `json:"error,omitempty"`
	Stream     bool              `json:"stream,omitempty"`
	StreamID   string            `json:"stream_id,omitempty"`
	Chunk      bool              `json:"chunk,omitempty"` // Indicates streaming chunk
	Done       bool              `json:"done,omitempty"`  // Indicates stream end
}

// NewK8sProxy creates a new Kubernetes API proxy
func NewK8sProxy(k8sClient *k8s.Client, logger *logrus.Logger) *K8sProxy {
	proxy := &K8sProxy{
		k8sClient:     k8sClient,
		logger:        logger,
		activeStreams: make(map[string]*StreamContext),
		config:        k8sClient.GetConfig(),
	}

	proxy.upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return proxy.checkOrigin(r)
		},
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}

	return proxy
}

// checkOrigin validates the origin of WebSocket connections for security
func (p *K8sProxy) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")

	// Log the connection attempt for security monitoring
	p.logger.WithFields(logrus.Fields{
		"origin":      origin,
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.Header.Get("User-Agent"),
	}).Debug("WebSocket connection attempt")

	// Allow connections with no origin (e.g., from CLI tools, direct connections)
	if origin == "" {
		p.logger.Debug("Allowing connection with no origin header")
		return true
	}

	// Parse origin URL
	originURL, err := url.Parse(origin)
	if err != nil {
		p.logger.WithError(err).WithField("origin", origin).Warn("Invalid origin URL format")
		return false
	}

	// Allow localhost connections for development
	if originURL.Hostname() == "localhost" || originURL.Hostname() == "127.0.0.1" {
		p.logger.WithField("origin", origin).Debug("Allowing localhost connection")
		return true
	}

	// Allow connections from same host as the agent (same VM/container)
	if r.Host != "" {
		requestHost := r.Host
		// Remove port if present
		if colonIdx := strings.Index(requestHost, ":"); colonIdx != -1 {
			requestHost = requestHost[:colonIdx]
		}

		if originURL.Hostname() == requestHost {
			p.logger.WithField("origin", origin).Debug("Allowing same-host connection")
			return true
		}
	}

	// Check against allowed origins list (could be configurable)
	allowedOrigins := p.getAllowedOrigins()
	for _, allowedOrigin := range allowedOrigins {
		if origin == allowedOrigin {
			p.logger.WithField("origin", origin).Debug("Allowing whitelisted origin")
			return true
		}
	}

	// Check if it's a PipeOps domain (you can customize this logic)
	if strings.HasSuffix(originURL.Hostname(), ".pipeops.io") ||
		strings.HasSuffix(originURL.Hostname(), ".pipeops.com") ||
		originURL.Hostname() == "pipeops.io" ||
		originURL.Hostname() == "pipeops.com" {
		p.logger.WithField("origin", origin).Debug("Allowing PipeOps domain")
		return true
	}

	// Log rejected connection for security monitoring
	p.logger.WithFields(logrus.Fields{
		"origin":      origin,
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.Header.Get("User-Agent"),
	}).Warn("Rejected WebSocket connection from unauthorized origin")

	return false
}

// getAllowedOrigins returns a list of allowed origins for WebSocket connections
// This could be made configurable through environment variables or config file
func (p *K8sProxy) getAllowedOrigins() []string {
	return []string{
		"https://app.pipeops.io",
		"https://console.pipeops.io",
		"https://dashboard.pipeops.io",
		"http://localhost:3000", // For development
		"http://localhost:8080", // For development
		"http://127.0.0.1:3000", // For development
		"http://127.0.0.1:8080", // For development
	}
}

// HandleWebSocketConnection handles incoming WebSocket connections from the Runner
func (p *K8sProxy) HandleWebSocketConnection(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		p.logger.WithError(err).Error("Failed to upgrade WebSocket connection")
		return
	}
	defer conn.Close()

	p.logger.Info("New WebSocket connection established for K8s API proxy")

	// Handle messages
	for {
		var req types.K8sProxyRequest
		err := conn.ReadJSON(&req)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				p.logger.WithError(err).Error("WebSocket read error")
			}
			break
		}

		// Handle the request
		go p.handleRequest(conn, &req)
	}

	// Clean up any active streams when connection closes
	p.cleanupStreams()
}

// handleRequest processes a single K8s API request
func (p *K8sProxy) handleRequest(conn *websocket.Conn, req *types.K8sProxyRequest) {
	p.logger.WithFields(logrus.Fields{
		"id":     req.ID,
		"method": req.Method,
		"path":   req.Path,
		"stream": req.Stream,
	}).Debug("Processing K8s API request")

	// Handle stream control requests
	if req.StreamID != "" {
		p.handleStreamControl(conn, req)
		return
	}

	// Create HTTP request to Kubernetes API
	resp := p.proxyRequest(req)

	// Send response back over WebSocket
	if err := conn.WriteJSON(resp); err != nil {
		p.logger.WithError(err).Error("Failed to send response over WebSocket")
	}
}

// proxyRequest forwards the request to the Kubernetes API
func (p *K8sProxy) proxyRequest(req *types.K8sProxyRequest) *types.K8sProxyResponse {
	// Build the full URL
	apiURL := strings.TrimSuffix(p.config.Host, "/") + req.Path
	if len(req.Query) > 0 {
		queryValues := url.Values(req.Query)
		apiURL += "?" + queryValues.Encode()
	}

	// Create HTTP request
	var bodyReader io.Reader
	if req.Body != nil {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequest(req.Method, apiURL, bodyReader)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusBadRequest,
			Error:      fmt.Sprintf("Failed to create request: %v", err),
		}
	}

	// Set headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Add authentication
	if p.config.BearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.BearerToken)
	}

	// Create HTTP client with proper TLS config
	transport := &http.Transport{}
	if p.config.TLSClientConfig.CAFile != "" || p.config.TLSClientConfig.CAData != nil {
		// Configure TLS if needed
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: p.config.TLSClientConfig.Insecure,
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// Handle streaming requests (WATCH operations)
	if req.Stream {
		return p.handleStreamingRequest(httpReq, req, client)
	}

	// Execute regular request
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusServiceUnavailable,
			Error:      fmt.Sprintf("Request failed: %v", err),
		}
	}
	defer httpResp.Body.Close()

	// Read response body
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusInternalServerError,
			Error:      fmt.Sprintf("Failed to read response: %v", err),
		}
	}

	// Convert headers
	headers := make(map[string]string)
	for key, values := range httpResp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return &types.K8sProxyResponse{
		ID:         req.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    headers,
		Body:       body,
	}
}

// handleStreamingRequest handles WATCH operations and other streaming requests
func (p *K8sProxy) handleStreamingRequest(httpReq *http.Request, req *types.K8sProxyRequest, client *http.Client) *types.K8sProxyResponse {
	// Set streaming headers
	httpReq.Header.Set("Accept", "application/json")
	if strings.Contains(req.Path, "watch=true") || req.Stream {
		httpReq.Header.Set("Cache-Control", "no-cache")
	}

	// Execute request
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusServiceUnavailable,
			Error:      fmt.Sprintf("Streaming request failed: %v", err),
		}
	}

	// Create stream context
	ctx, cancel := context.WithCancel(context.Background())
	streamID := fmt.Sprintf("%s-stream-%d", req.ID, time.Now().UnixNano())

	streamCtx := &StreamContext{
		ID:       streamID,
		Stream:   httpResp.Body,
		Response: make(chan *types.K8sProxyResponse, 10),
		Cancel:   cancel,
		Done:     make(chan struct{}),
	}

	// Store stream context
	p.streamsMu.Lock()
	p.activeStreams[streamID] = streamCtx
	p.streamsMu.Unlock()

	// Start streaming goroutine
	go p.streamData(ctx, streamCtx, httpResp)

	// Return initial response with stream ID
	return &types.K8sProxyResponse{
		ID:         req.ID,
		StatusCode: httpResp.StatusCode,
		Stream:     true,
		StreamID:   streamID,
		Headers: map[string]string{
			"Content-Type": httpResp.Header.Get("Content-Type"),
		},
	}
}

// streamData continuously reads from the stream and sends chunks
func (p *K8sProxy) streamData(ctx context.Context, streamCtx *StreamContext, httpResp *http.Response) {
	defer close(streamCtx.Done)
	defer streamCtx.Stream.Close()

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := streamCtx.Stream.Read(buf)
			if n > 0 {
				// Send chunk
				chunk := &types.K8sProxyResponse{
					ID:       streamCtx.ID,
					StreamID: streamCtx.ID,
					Body:     buf[:n],
					Chunk:    true,
				}

				select {
				case streamCtx.Response <- chunk:
				case <-ctx.Done():
					return
				}
			}

			if err != nil {
				if err != io.EOF {
					p.logger.WithError(err).Error("Stream read error")
				}
				// Send end-of-stream marker
				endChunk := &types.K8sProxyResponse{
					ID:       streamCtx.ID,
					StreamID: streamCtx.ID,
					Done:     true,
				}
				select {
				case streamCtx.Response <- endChunk:
				case <-ctx.Done():
				}
				return
			}
		}
	}
}

// handleStreamControl handles stream control commands (cancel, etc.)
func (p *K8sProxy) handleStreamControl(conn *websocket.Conn, req *types.K8sProxyRequest) {
	p.streamsMu.RLock()
	streamCtx, exists := p.activeStreams[req.StreamID]
	p.streamsMu.RUnlock()

	if !exists {
		resp := &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusNotFound,
			Error:      "Stream not found",
		}
		conn.WriteJSON(resp)
		return
	}

	switch req.Method {
	case "CANCEL":
		streamCtx.Cancel()
		p.streamsMu.Lock()
		delete(p.activeStreams, req.StreamID)
		p.streamsMu.Unlock()

		resp := &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusOK,
			StreamID:   req.StreamID,
		}
		conn.WriteJSON(resp)
	}
}

// cleanupStreams cleans up all active streams
func (p *K8sProxy) cleanupStreams() {
	p.streamsMu.Lock()
	defer p.streamsMu.Unlock()

	for streamID, streamCtx := range p.activeStreams {
		streamCtx.Cancel()
		<-streamCtx.Done // Wait for stream to finish
		delete(p.activeStreams, streamID)
	}
}

// GetActiveStreamsCount returns the number of active streams
func (p *K8sProxy) GetActiveStreamsCount() int {
	p.streamsMu.RLock()
	defer p.streamsMu.RUnlock()
	return len(p.activeStreams)
}

// ProxyRequest handles a single K8s API request (for HTTP endpoint)
func (p *K8sProxy) ProxyRequest(ctx context.Context, req *types.K8sProxyRequest) (*types.K8sProxyResponse, error) {
	response := p.proxyRequest(req)
	if response.Error != "" {
		return response, fmt.Errorf("proxy request failed: %s", response.Error)
	}
	return response, nil
}
