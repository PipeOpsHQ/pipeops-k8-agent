package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// WebSocketClient represents a WebSocket client for control plane communication
type WebSocketClient struct {
	apiURL            string
	token             string
	agentID           string
	logger            *logrus.Logger
	tlsConfig         *tls.Config
	conn              *websocket.Conn
	connMutex         sync.RWMutex
	writeMutex        sync.Mutex
	reconnectDelay    time.Duration
	maxReconnectDelay time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	requestHandlers   map[string]chan *WebSocketMessage
	handlerMutex      sync.RWMutex
	connected         bool
	connectedMutex    sync.RWMutex
	reconnecting      atomic.Bool
	// Callback for registration errors (e.g., "Cluster not registered")
	onRegistrationError func(error)
	onReconnect         func()

	proxyHandler          func(*ProxyRequest)
	streamingProxyHandler func(*ProxyRequest, ProxyResponseWriter)
	proxyHandlerMutex     sync.RWMutex

	// Track active proxy requests for cancellation
	activeProxyRequests map[string]context.CancelFunc
	activeProxyMutex    sync.RWMutex

	// WebSocket proxy manager for kubectl exec/attach/port-forward
	wsProxyManager *WebSocketProxyManager

	// K8s API host for WebSocket proxy
	k8sAPIHost      string
	k8sAPIHostMutex sync.RWMutex
}

type proxyResponseSender interface {
	SendProxyResponse(ctx context.Context, response *ProxyResponse) error
	SendProxyError(ctx context.Context, proxyErr *ProxyError) error
}

// WebSocketMessage represents a message sent/received over WebSocket
type WebSocketMessage struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"request_id,omitempty"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewWebSocketClient creates a new WebSocket client for control plane communication
func NewWebSocketClient(apiURL, token, agentID string, tlsConfig *tls.Config, logger *logrus.Logger) (*WebSocketClient, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &WebSocketClient{
		apiURL:              apiURL,
		token:               token,
		agentID:             agentID,
		logger:              logger,
		tlsConfig:           tlsConfig,
		reconnectDelay:      1 * time.Second,
		maxReconnectDelay:   15 * time.Second, // Reduced from 60s for faster reconnection
		ctx:                 ctx,
		cancel:              cancel,
		requestHandlers:     make(map[string]chan *WebSocketMessage),
		activeProxyRequests: make(map[string]context.CancelFunc),
		connected:           false,
	}

	client.wsProxyManager = NewWebSocketProxyManager(client, logger)

	return client, nil
}

// SetOnRegistrationError sets a callback function to be called when a registration error is received
func (c *WebSocketClient) SetOnRegistrationError(callback func(error)) {
	c.onRegistrationError = callback
}

// SetProxyRequestHandler sets a callback for proxy requests from the control plane
func (c *WebSocketClient) SetProxyRequestHandler(handler func(*ProxyRequest)) {
	c.proxyHandlerMutex.Lock()
	c.proxyHandler = handler
	if handler != nil {
		c.streamingProxyHandler = nil
	}
	c.proxyHandlerMutex.Unlock()
}

// SetStreamingProxyHandler sets a streaming-capable proxy request handler.
func (c *WebSocketClient) SetStreamingProxyHandler(handler func(*ProxyRequest, ProxyResponseWriter)) {
	c.proxyHandlerMutex.Lock()
	if handler == nil {
		c.streamingProxyHandler = nil
	} else {
		c.streamingProxyHandler = handler
		c.proxyHandler = nil
	}
	c.proxyHandlerMutex.Unlock()
}

// SetOnReconnect registers a callback that fires after the client successfully reconnects.
func (c *WebSocketClient) SetOnReconnect(callback func()) {
	c.onReconnect = callback
}

// Connect establishes a WebSocket connection to the control plane
func (c *WebSocketClient) Connect() error {
	// Parse API URL and convert to WebSocket URL
	u, err := url.Parse(c.apiURL)
	if err != nil {
		return fmt.Errorf("invalid API URL: %w", err)
	}

	// Convert http/https to ws/wss
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}

	// Set WebSocket endpoint path
	u.Path = "/api/v1/clusters/agent/ws"

	// Add token as query parameter
	q := u.Query()
	q.Set("token", c.token)
	u.RawQuery = q.Encode()

	c.logger.WithField("url", u.String()).Debug("Connecting to WebSocket endpoint")

	// Create WebSocket connection with Authorization header
	dialer := websocket.Dialer{
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: false,
	}

	if c.tlsConfig != nil {
		dialer.TLSClientConfig = c.tlsConfig.Clone()
		if dialer.TLSClientConfig == nil {
			dialer.TLSClientConfig = c.tlsConfig
		}
	}

	// Set Authorization header for server-to-server authentication
	headers := make(map[string][]string)
	headers["Authorization"] = []string{"Bearer " + c.token}

	conn, resp, err := dialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("failed to connect to WebSocket (status %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	c.connMutex.Lock()
	c.conn = conn
	c.connMutex.Unlock()

	c.setConnected(true)
	c.reconnectDelay = 1 * time.Second // Reset reconnect delay on successful connection

	c.logger.Info("WebSocket connection established with control plane")

	// Start message reader
	c.wg.Add(1)
	go c.readMessages()

	// Start ping/pong handler
	c.wg.Add(1)
	go c.pingHandler()

	return nil
}

// RegisterAgent registers the agent via WebSocket
func (c *WebSocketClient) RegisterAgent(ctx context.Context, agent *types.Agent) (*RegistrationResult, error) {
	if !c.isConnected() {
		return nil, fmt.Errorf("WebSocket not connected")
	}

	// Generate unique request ID
	requestID := c.generateRequestID()

	// Prepare payload
	payload := map[string]interface{}{
		"agent_id":           agent.ID,
		"name":               agent.Name,
		"k8s_version":        agent.Version,
		"hostname":           agent.Hostname,
		"agent_version":      agent.AgentVersion,
		"server_ip":          agent.ServerIP,
		"region":             agent.Region,
		"cloud_provider":     agent.CloudProvider,
		"tunnel_port_config": agent.TunnelPortConfig,
		"labels":             agent.Labels,
		"server_specs":       agent.ServerSpecs,
		"features": map[string]interface{}{
			"supports_streaming":       true,
			"supports_binary_protocol": true,
			"supports_compression":     true,
			"supports_websocket_proxy": true,
		},
	}

	if agent.ClusterID != "" {
		payload["cluster_id"] = agent.ClusterID
	}

	// Add optional fields
	if agent.Token != "" {
		payload["k8s_service_token"] = agent.Token
	}

	// Create registration message
	msg := &WebSocketMessage{
		Type:      "register",
		RequestID: requestID,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	// Create response channel
	responseChan := make(chan *WebSocketMessage, 1)
	c.registerRequestHandler(requestID, responseChan)
	defer c.unregisterRequestHandler(requestID)

	// Send registration message
	if err := c.sendMessage(msg); err != nil {
		return nil, fmt.Errorf("failed to send registration message: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"agent_id":     agent.ID,
		"cluster_name": agent.Name,
		"request_id":   requestID,
	}).Debug("Registration message sent via WebSocket")

	// Wait for response with timeout
	select {
	case response := <-responseChan:
		return c.parseRegistrationResponse(response)
	case <-ctx.Done():
		return nil, fmt.Errorf("registration timed out")
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("registration timed out after 30 seconds")
	}
}

// SendHeartbeat sends a heartbeat via WebSocket
func (c *WebSocketClient) SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error {
	if !c.isConnected() {
		return fmt.Errorf("WebSocket not connected")
	}

	// Create heartbeat message
	msg := &WebSocketMessage{
		Type: "heartbeat",
		Payload: map[string]interface{}{
			"cluster_id":    heartbeat.ClusterID,
			"agent_id":      heartbeat.AgentID,
			"status":        heartbeat.Status,
			"tunnel_status": heartbeat.TunnelStatus,
			"metadata":      heartbeat.Metadata,
		},
		Timestamp: time.Now(),
	}

	// Add monitoring fields if present
	if heartbeat.PrometheusURL != "" {
		msg.Payload["prometheus_url"] = heartbeat.PrometheusURL
		msg.Payload["prometheus_username"] = heartbeat.PrometheusUsername
		msg.Payload["prometheus_password"] = heartbeat.PrometheusPassword
		msg.Payload["prometheus_ssl"] = heartbeat.PrometheusSSL
	}

	if heartbeat.LokiURL != "" {
		msg.Payload["loki_url"] = heartbeat.LokiURL
		msg.Payload["loki_username"] = heartbeat.LokiUsername
		msg.Payload["loki_password"] = heartbeat.LokiPassword
	}

	if heartbeat.GrafanaURL != "" {
		msg.Payload["grafana_url"] = heartbeat.GrafanaURL
		msg.Payload["grafana_username"] = heartbeat.GrafanaUsername
		msg.Payload["grafana_password"] = heartbeat.GrafanaPassword
	}

	if heartbeat.OpenCostBaseURL != "" {
		msg.Payload["opencost_base_url"] = heartbeat.OpenCostBaseURL
		msg.Payload["opencost_username"] = heartbeat.OpenCostUsername
		msg.Payload["opencost_password"] = heartbeat.OpenCostPassword
	}

	// Send heartbeat message
	if err := c.sendMessage(msg); err != nil {
		return fmt.Errorf("failed to send heartbeat message: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"cluster_id": heartbeat.ClusterID,
		"agent_id":   heartbeat.AgentID,
		"status":     heartbeat.Status,
	}).Debug("Heartbeat sent via WebSocket")

	return nil
}

// SendMessage sends a generic message to the control plane
func (c *WebSocketClient) SendMessage(messageType string, payload map[string]interface{}) error {
	if !c.isConnected() {
		return fmt.Errorf("WebSocket not connected")
	}

	msg := &WebSocketMessage{
		Type:      messageType,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	if err := c.sendMessage(msg); err != nil {
		return fmt.Errorf("failed to send %s message: %w", messageType, err)
	}

	c.logger.WithField("type", messageType).Debug("Message sent via WebSocket")
	return nil
}

// Ping checks connectivity via WebSocket ping
func (c *WebSocketClient) Ping(ctx context.Context) error {
	if !c.isConnected() {
		return fmt.Errorf("WebSocket not connected")
	}

	// WebSocket ping/pong is handled automatically by the connection
	// This is a no-op for WebSocket connections
	return nil
}

// Close closes the WebSocket connection
func (c *WebSocketClient) Close() error {
	c.logger.Info("Closing WebSocket connection to control plane")

	// Cancel context to stop goroutines
	c.cancel()

	// Close all active WebSocket proxy streams
	if c.wsProxyManager != nil {
		c.logger.Debug("Closing all active WebSocket proxy streams")
		c.wsProxyManager.CloseAll()
	}

	// Close WebSocket connection
	c.connMutex.Lock()
	if c.conn != nil {
		c.writeMutex.Lock()
		if err := c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(5*time.Second),
		); err != nil {
			c.logger.WithError(err).Debug("Failed to send close control frame")
		}
		if err := c.conn.Close(); err != nil {
			c.logger.WithError(err).Debug("Failed to close WebSocket connection")
		}
		c.conn = nil
		c.writeMutex.Unlock()
	}
	c.connMutex.Unlock()

	c.setConnected(false)

	// Wait for goroutines to finish
	c.wg.Wait()

	return nil
}

// readMessages reads messages from the WebSocket connection
func (c *WebSocketClient) readMessages() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.connMutex.RLock()
			conn := c.conn
			c.connMutex.RUnlock()

			if conn == nil {
				return
			}

			var msg WebSocketMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					c.logger.WithError(err).Warn("WebSocket connection closed unexpectedly")
				}
				c.setConnected(false)
				c.reconnect()
				return
			}

			c.handleMessage(&msg)
		}
	}
}

// handleMessage handles incoming WebSocket messages
func (c *WebSocketClient) handleMessage(msg *WebSocketMessage) {
	c.logger.WithFields(logrus.Fields{
		"type":       msg.Type,
		"request_id": msg.RequestID,
	}).Debug("Received WebSocket message")

	switch msg.Type {
	case "register_success":
		// Handle registration response
		if msg.RequestID != "" {
			c.handlerMutex.RLock()
			if ch, ok := c.requestHandlers[msg.RequestID]; ok {
				select {
				case ch <- msg:
				default:
				}
			}
			c.handlerMutex.RUnlock()
		}

	case "heartbeat_ack":
		c.logger.Debug("Heartbeat acknowledged by control plane")

	case "pong":
		c.logger.Debug("Pong received from control plane")

	case "ping":
		// Respond to ping with pong
		pongMsg := &WebSocketMessage{
			Type:      "pong",
			RequestID: msg.RequestID,
			Payload:   map[string]interface{}{"timestamp": time.Now()},
			Timestamp: time.Now(),
		}
		c.sendMessage(pongMsg)

	case "proxy_request":
		req, err := c.parseProxyRequest(msg)
		if err != nil {
			c.logger.WithError(err).Error("Failed to parse proxy request message")
			return
		}

		c.proxyHandlerMutex.RLock()
		streamHandler := c.streamingProxyHandler
		handler := c.proxyHandler
		c.proxyHandlerMutex.RUnlock()

		if streamHandler != nil {
			go c.dispatchStreamingProxyRequest(req, streamHandler)
			return
		}

		if handler == nil {
			c.logger.Warn("No proxy handler registered - dropping proxy request")
			return
		}

		go handler(req)

	case "proxy_cancel":
		// Handle request cancellation from controller
		if msg.RequestID == "" {
			c.logger.Warn("Received proxy_cancel without request_id")
			return
		}

		c.activeProxyMutex.Lock()
		if cancelFunc, ok := c.activeProxyRequests[msg.RequestID]; ok {
			c.logger.WithField("request_id", msg.RequestID).Debug("Cancelling proxy request")
			cancelFunc()
			delete(c.activeProxyRequests, msg.RequestID)
		}
		c.activeProxyMutex.Unlock()

	case "proxy_websocket_start":
		if c.wsProxyManager != nil {
			go c.wsProxyManager.HandleWebSocketProxyStart(msg)
		} else {
			c.logger.Warn("WebSocket proxy manager not initialized")
		}

	case "proxy_websocket_data":
		if c.wsProxyManager != nil {
			c.wsProxyManager.HandleWebSocketProxyData(msg)
		} else {
			c.logger.Warn("WebSocket proxy manager not initialized")
		}

	case "proxy_websocket_close":
		if c.wsProxyManager != nil {
			c.wsProxyManager.HandleWebSocketProxyClose(msg)
		} else {
			c.logger.Warn("WebSocket proxy manager not initialized")
		}

	case "error":
		errorMsg := ""
		if err, ok := msg.Payload["error"].(string); ok {
			errorMsg = err
		}
		c.logger.WithFields(logrus.Fields{
			"error":      errorMsg,
			"request_id": msg.RequestID,
		}).Error("Error message from control plane")

		// Check if this is a "not registered" error - trigger re-registration
		if strings.Contains(strings.ToLower(errorMsg), "not registered") ||
			strings.Contains(strings.ToLower(errorMsg), "cluster not found") {
			c.logger.Warn("Cluster not registered with control plane - triggering re-registration")

			// Call the error callback if set
			if c.onRegistrationError != nil {
				go c.onRegistrationError(fmt.Errorf("cluster not registered: %s", errorMsg))
			}
		}

	default:
		c.logger.WithField("type", msg.Type).Warn("Unknown message type received")
	}
}

func (c *WebSocketClient) dispatchStreamingProxyRequest(req *ProxyRequest, handler func(*ProxyRequest, ProxyResponseWriter)) {
	writer := newBufferedProxyResponseWriter(c, req.RequestID, c.logger)
	defer writer.ensureClosed()
	handler(req, writer)
}

// SendProxyResponse sends a proxy response back to the control plane
func (c *WebSocketClient) SendProxyResponse(ctx context.Context, response *ProxyResponse) error {
	if response == nil {
		return fmt.Errorf("proxy response is nil")
	}

	msg := &WebSocketMessage{
		Type:      "proxy_response",
		RequestID: response.RequestID,
		Payload: map[string]interface{}{
			"status":  response.Status,
			"headers": response.Headers,
		},
		Timestamp: time.Now(),
	}

	if response.Body != "" {
		msg.Payload["body"] = response.Body
	}

	if response.Encoding != "" {
		msg.Payload["encoding"] = response.Encoding
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return c.sendMessage(msg)
}

// SendProxyError notifies the control plane that a proxy request failed
func (c *WebSocketClient) SendProxyError(ctx context.Context, proxyErr *ProxyError) error {
	if proxyErr == nil {
		return fmt.Errorf("proxy error payload is nil")
	}

	msg := &WebSocketMessage{
		Type:      "proxy_error",
		RequestID: proxyErr.RequestID,
		Payload: map[string]interface{}{
			"error": proxyErr.Error,
		},
		Timestamp: time.Now(),
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return c.sendMessage(msg)
}

// sendMessage sends a message via WebSocket
func (c *WebSocketClient) sendMessage(msg *WebSocketMessage) error {
	c.connMutex.RLock()
	conn := c.conn
	c.connMutex.RUnlock()

	if conn == nil {
		return fmt.Errorf("WebSocket connection is nil")
	}

	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		c.logger.WithError(err).Debug("Failed to set write deadline for WebSocket message")
	}

	return conn.WriteJSON(msg)
}

// pingHandler sends periodic pings and handles pongs
func (c *WebSocketClient) pingHandler() {
	defer c.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.connMutex.RLock()
			conn := c.conn
			c.connMutex.RUnlock()

			if conn == nil {
				continue
			}

			// Send WebSocket ping frame
			c.writeMutex.Lock()
			err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second))
			c.writeMutex.Unlock()

			if err != nil {
				c.logger.WithError(err).Debug("Failed to send ping")
				c.setConnected(false)
				c.reconnect()
				return
			}

			c.logger.Debug("Ping sent to control plane")
		}
	}
}

// reconnect attempts to reconnect to the WebSocket
func (c *WebSocketClient) reconnect() {
	// Prevent multiple concurrent reconnection attempts
	if !c.reconnecting.CompareAndSwap(false, true) {
		c.logger.Debug("Reconnection already in progress, skipping duplicate attempt")
		return
	}
	defer c.reconnecting.Store(false)

	// Add jitter to prevent thundering herd (±25%)
	jitter := time.Duration(rand.Float64() * 0.25 * float64(c.reconnectDelay))
	delay := c.reconnectDelay + jitter

	c.logger.WithFields(logrus.Fields{
		"base_delay":  c.reconnectDelay,
		"jitter":      jitter,
		"total_delay": delay,
		"next_delay":  c.reconnectDelay * 2,
	}).Info("Attempting to reconnect to WebSocket")

	time.Sleep(delay)

	if err := c.Connect(); err != nil {
		c.logger.WithError(err).Warn("Reconnection failed, will retry")

		// Increase reconnect delay with exponential backoff
		c.reconnectDelay *= 2
		if c.reconnectDelay > c.maxReconnectDelay {
			c.reconnectDelay = c.maxReconnectDelay
		}

		// Try again
		go c.reconnect()
	} else {
		c.logger.Info("✓ Reconnected successfully to WebSocket")
		if c.onReconnect != nil {
			go c.onReconnect()
		}
	}
}

type proxyResponseWriter struct {
	sender    proxyResponseSender
	logger    *logrus.Logger
	requestID string

	mu      sync.Mutex
	status  int
	headers map[string][]string
	buffer  bytes.Buffer

	closed atomic.Bool
}

func newBufferedProxyResponseWriter(sender proxyResponseSender, requestID string, logger *logrus.Logger) *proxyResponseWriter {
	return &proxyResponseWriter{
		sender:    sender,
		logger:    logger,
		requestID: requestID,
		headers:   make(map[string][]string),
	}
}

func (w *proxyResponseWriter) WriteHeader(status int, headers map[string][]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return io.ErrClosedPipe
	}

	if status > 0 {
		w.status = status
	}

	if headers != nil {
		w.headers = cloneHeaderMap(headers)
	}

	return nil
}

func (w *proxyResponseWriter) WriteChunk(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return io.ErrClosedPipe
	}

	_, err := w.buffer.Write(data)
	return err
}

func (w *proxyResponseWriter) Close() error {
	return w.CloseWithError(nil)
}

func (w *proxyResponseWriter) CloseWithError(closeErr error) error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}

	if closeErr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		sendErr := w.sender.SendProxyError(ctx, &ProxyError{
			RequestID: w.requestID,
			Error:     closeErr.Error(),
		})
		if sendErr != nil {
			if w.logger != nil {
				w.logger.WithError(sendErr).Warn("Failed to send proxy error to control plane")
			}
		}
		return closeErr
	}

	w.mu.Lock()
	status := w.status
	headers := cloneHeaderMap(w.headers)
	bodyBytes := w.buffer.Bytes()
	w.mu.Unlock()

	if status == 0 {
		status = http.StatusOK
	}

	encodedBody := ""
	encoding := ""
	if len(bodyBytes) > 0 {
		// Determine if we should compress
		contentType := ""
		if ct, ok := headers["Content-Type"]; ok && len(ct) > 0 {
			contentType = ct[0]
		}

		if shouldCompress(contentType, len(bodyBytes)) {
			// Compress the body
			compressed, err := compressData(bodyBytes)
			if err != nil {
				if w.logger != nil {
					w.logger.WithError(err).Warn("Failed to compress response, sending uncompressed")
				}
				encodedBody = base64.StdEncoding.EncodeToString(bodyBytes)
				encoding = "base64"
			} else {
				encodedBody = base64.StdEncoding.EncodeToString(compressed)
				encoding = "gzip"

				originalSize := len(bodyBytes)
				compressedSize := len(compressed)
				ratio := float64(originalSize) / float64(compressedSize)
				savings := originalSize - compressedSize

				if w.logger != nil {
					w.logger.WithFields(logrus.Fields{
						"request_id":      w.requestID,
						"original_size":   originalSize,
						"compressed_size": compressedSize,
						"ratio":           fmt.Sprintf("%.2fx", ratio),
						"savings_bytes":   savings,
					}).Info("Compressed response")
				}
			}
		} else {
			encodedBody = base64.StdEncoding.EncodeToString(bodyBytes)
			encoding = "base64"
		}
	}

	response := &ProxyResponse{
		RequestID: w.requestID,
		Status:    status,
		Headers:   sanitizeHeaders(headers),
		Body:      encodedBody,
		Encoding:  encoding,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := w.sender.SendProxyResponse(ctx, response); err != nil {
		if w.logger != nil {
			w.logger.WithError(err).Warn("Failed to send proxy response to control plane")
		}
		return err
	}

	return nil
}

func (w *proxyResponseWriter) ensureClosed() {
	if w.closed.Load() {
		return
	}
	if err := w.Close(); err != nil {
		if w.logger != nil {
			w.logger.WithError(err).Warn("Proxy response writer closed with error")
		}
	}
}

func cloneHeaderMap(headers map[string][]string) map[string][]string {
	if headers == nil {
		return nil
	}

	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func sanitizeHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}

	filtered := make(map[string][]string, len(headers))
	for key, values := range headers {
		lower := strings.ToLower(key)
		switch lower {
		case "connection", "transfer-encoding", "keep-alive", "proxy-authenticate",
			"proxy-authorization", "te", "trailers", "upgrade":
			continue
		}
		filtered[key] = append([]string(nil), values...)
	}
	return filtered
}

func (c *WebSocketClient) parseProxyRequest(msg *WebSocketMessage) (*ProxyRequest, error) {
	if msg.RequestID == "" {
		return nil, fmt.Errorf("proxy request missing request_id")
	}

	if msg.Payload == nil {
		return nil, fmt.Errorf("proxy request missing payload")
	}

	payload := msg.Payload

	getString := func(key string) string {
		if val, ok := payload[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
		return ""
	}

	headers := make(map[string][]string)
	if rawHeaders, ok := payload["headers"]; ok {
		switch h := rawHeaders.(type) {
		case map[string]interface{}:
			for key, value := range h {
				headers[key] = toStringSlice(value)
			}
		case map[string][]string:
			for key, value := range h {
				headers[key] = append([]string(nil), value...)
			}
		}
	}

	bodyEncoding := strings.ToLower(getString("body_encoding"))
	bodyBytes := []byte{}

	// Check if using binary protocol
	if useBinary, ok := payload["body_binary"].(bool); ok && useBinary {
		// Binary protocol: body comes in separate binary frame
		bodySize, _ := payload["body_size"].(float64)

		c.logger.WithFields(logrus.Fields{
			"request_id": msg.RequestID,
			"body_size":  int(bodySize),
		}).Debug("Waiting for binary frame")

		// Read next binary message
		c.connMutex.RLock()
		conn := c.conn
		c.connMutex.RUnlock()

		if conn != nil {
			messageType, binaryFrame, err := conn.ReadMessage()
			if err != nil {
				return nil, fmt.Errorf("failed to read binary frame: %w", err)
			}

			if messageType != websocket.BinaryMessage {
				return nil, fmt.Errorf("expected binary message, got type %d", messageType)
			}

			// Parse binary frame: [2 bytes reqID length][reqID][body]
			if len(binaryFrame) < 2 {
				return nil, fmt.Errorf("binary frame too short")
			}

			reqIDLen := int(binaryFrame[0])<<8 | int(binaryFrame[1])
			if len(binaryFrame) < 2+reqIDLen {
				return nil, fmt.Errorf("invalid binary frame format")
			}

			frameReqID := string(binaryFrame[2 : 2+reqIDLen])
			if frameReqID != msg.RequestID {
				return nil, fmt.Errorf("request ID mismatch: expected %s, got %s", msg.RequestID, frameReqID)
			}

			bodyBytes = binaryFrame[2+reqIDLen:]
			bodyEncoding = "binary"

			c.logger.WithFields(logrus.Fields{
				"request_id": msg.RequestID,
				"body_size":  len(bodyBytes),
			}).Info("Received binary protocol request (no base64 overhead)")
		}
	} else if rawBody, ok := payload["body"].(string); ok && rawBody != "" {
		// Legacy: base64 encoded body
		if bodyEncoding == "base64" {
			decoded, err := base64.StdEncoding.DecodeString(rawBody)
			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 proxy body: %w", err)
			}
			bodyBytes = decoded
		} else {
			bodyBytes = []byte(rawBody)
		}
	}

	req := &ProxyRequest{
		RequestID:    msg.RequestID,
		ClusterID:    getString("cluster_id"),
		ClusterUUID:  getString("cluster_uuid"),
		AgentID:      getString("agent_id"),
		Method:       getString("method"),
		Path:         getString("path"),
		Query:        getString("query"),
		Headers:      headers,
		Body:         bodyBytes,
		BodyEncoding: bodyEncoding,
	}

	if supports, ok := payload["supports_streaming"].(bool); ok {
		req.SupportsStreaming = supports
	}

	if rawDeadline, ok := payload["deadline"].(string); ok && rawDeadline != "" {
		if deadline, err := time.Parse(time.RFC3339Nano, rawDeadline); err == nil {
			req.Deadline = deadline
		} else if c.logger != nil {
			c.logger.WithError(err).Debug("Failed to parse proxy request deadline")
		}
	}

	switch timeoutVal := payload["timeout"].(type) {
	case float64:
		if timeoutVal > 0 {
			req.Timeout = time.Duration(timeoutVal * float64(time.Millisecond))
		}
	case string:
		if parsed, err := time.ParseDuration(timeoutVal); err == nil {
			req.Timeout = parsed
		} else if c.logger != nil {
			c.logger.WithError(err).Debug("Failed to parse proxy request timeout")
		}
	}

	if rateLimit, ok := payload["rate_limit_bps"].(float64); ok {
		req.RateLimitBps = rateLimit
	}

	// Parse route context for proxying to application services
	req.Namespace = getString("namespace")
	req.ServiceName = getString("service_name")
	if servicePort, ok := payload["service_port"].(float64); ok {
		req.ServicePort = int32(servicePort)
	}

	return req, nil
}

func toStringSlice(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{v}
	default:
		return nil
	}
}

// parseRegistrationResponse parses a registration response message
func (c *WebSocketClient) parseRegistrationResponse(msg *WebSocketMessage) (*RegistrationResult, error) {
	payload := msg.Payload

	// Extract cluster_id (required)
	clusterID, ok := payload["cluster_id"].(string)
	if !ok || clusterID == "" {
		return nil, fmt.Errorf("missing cluster_id in registration response")
	}

	// Extract other fields
	result := &RegistrationResult{
		ClusterID: clusterID,
	}

	if clusterUUID, ok := payload["cluster_uuid"].(string); ok {
		result.ClusterUUID = clusterUUID
	}

	if name, ok := payload["name"].(string); ok {
		result.Name = name
	}

	if status, ok := payload["status"].(string); ok {
		result.Status = status
	}

	if tunnelURL, ok := payload["tunnel_url"].(string); ok {
		result.TunnelURL = tunnelURL
	}

	if apiServer, ok := payload["api_server"].(string); ok {
		result.APIServer = apiServer
	}

	if token, ok := payload["token"].(string); ok {
		result.Token = token
	}

	if workspaceID, ok := payload["workspace_id"].(float64); ok {
		result.WorkspaceID = int(workspaceID)
	}

	c.logger.WithFields(logrus.Fields{
		"cluster_id":   result.ClusterID,
		"cluster_uuid": result.ClusterUUID,
		"name":         result.Name,
		"status":       result.Status,
		"workspace_id": result.WorkspaceID,
	}).Info("Agent registered successfully via WebSocket")

	return result, nil
}

// Helper methods

func (c *WebSocketClient) registerRequestHandler(requestID string, ch chan *WebSocketMessage) {
	c.handlerMutex.Lock()
	defer c.handlerMutex.Unlock()
	c.requestHandlers[requestID] = ch
}

func (c *WebSocketClient) unregisterRequestHandler(requestID string) {
	c.handlerMutex.Lock()
	defer c.handlerMutex.Unlock()
	delete(c.requestHandlers, requestID)
}

func (c *WebSocketClient) generateRequestID() string {
	return fmt.Sprintf("req_%d_%s", time.Now().UnixNano(), c.agentID)
}

func (c *WebSocketClient) isConnected() bool {
	c.connectedMutex.RLock()
	defer c.connectedMutex.RUnlock()
	return c.connected
}

// IsConnected returns whether the WebSocket is currently connected (public method)
func (c *WebSocketClient) IsConnected() bool {
	return c.isConnected()
}

func (c *WebSocketClient) setConnected(connected bool) {
	c.connectedMutex.Lock()
	defer c.connectedMutex.Unlock()
	c.connected = connected
}

func (c *WebSocketClient) SetK8sAPIHost(host string) {
	c.k8sAPIHostMutex.Lock()
	defer c.k8sAPIHostMutex.Unlock()
	c.k8sAPIHost = host
}

func (c *WebSocketClient) getK8sAPIHost() string {
	c.k8sAPIHostMutex.RLock()
	defer c.k8sAPIHostMutex.RUnlock()
	if c.k8sAPIHost != "" {
		return c.k8sAPIHost
	}
	return "kubernetes.default.svc"
}
