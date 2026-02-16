package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/internal/tunnel"
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
	timeouts          *types.Timeouts
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

	// Track active proxy response writers for bidirectional WebSocket
	activeProxyWriters map[string]ProxyResponseWriter
	activeWritersMutex sync.RWMutex

	// Track active request body pipes for streaming request bodies
	activeRequestBodyPipes map[string]*io.PipeWriter
	requestBodyPipesMutex  sync.RWMutex

	// WebSocket proxy manager for kubectl exec/attach/port-forward
	wsProxyManager *WebSocketProxyManager

	// Track unknown message types for diagnostics
	unknownMessageTypes map[string]int
	unknownMessageMutex sync.RWMutex

	// K8s API host for WebSocket proxy
	k8sAPIHost      string
	k8sAPIHostMutex sync.RWMutex

	// Callback for WebSocket data (for zero-copy application service proxying)
	onWebSocketData      func(requestID string, data []byte)
	onWebSocketDataMutex sync.RWMutex

	// TCP/UDP tunnel callbacks
	onTCPStart      func(*TCPTunnelStart)
	onTCPStartMutex sync.RWMutex
	onTCPData       func(requestID string, data []byte)
	onTCPDataMutex  sync.RWMutex
	onTCPClose      func(requestID string, reason string)
	onTCPCloseMutex sync.RWMutex
	onUDPData       func(*UDPTunnelData)
	onUDPDataMutex  sync.RWMutex

	// Gateway mode fields (for new controller/gateway architecture)
	gatewayMode  bool   // True when connected to gateway instead of controller
	gatewayURL   string // Gateway WebSocket URL
	clusterUUID  string // Cluster UUID for gateway authentication
	agentVersion string // Agent version for capability reporting

	// Yamux tunnel client for L4 tunneling
	yamuxClient      *yamuxClientWrapper
	yamuxClientMutex sync.RWMutex
	yamuxEnabled     bool // Set to true when gateway negotiates yamux
}

type proxyResponseSender interface {
	SendProxyResponse(ctx context.Context, response *ProxyResponse) error
	SendProxyResponseBinary(ctx context.Context, response *ProxyResponse, bodyBytes []byte) error
	SendProxyError(ctx context.Context, proxyErr *ProxyError) error
	// Streaming response methods
	SendProxyResponseHeader(ctx context.Context, requestID string, status int, headers map[string][]string) error
	SendProxyResponseChunk(ctx context.Context, requestID string, chunk []byte) error
	SendProxyResponseEnd(ctx context.Context, requestID string) error
	SendProxyResponseAbort(ctx context.Context, requestID string, reason string) error
}

// yamuxClientWrapper wraps the yamux tunnel client
type yamuxClientWrapper struct {
	client *tunnel.YamuxTunnelClient
	wsConn *tunnel.WSConn // Shared WSConn for yamux (pipe-fed by readMessages demuxer)
}

// GatewayHelloConfig contains yamux configuration from gateway
type GatewayHelloConfig struct {
	MaxStreamWindowSize   uint32 `json:"max_stream_window_size"`
	KeepAliveIntervalSecs int    `json:"keep_alive_interval_seconds"`
	ConnectionTimeoutSecs int    `json:"connection_timeout_seconds"`
}

// WebSocketMessage represents a message sent/received over WebSocket
type WebSocketMessage struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"request_id,omitempty"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewWebSocketClient creates a new WebSocket client for control plane communication
func NewWebSocketClient(apiURL, token, agentID string, timeouts *types.Timeouts, tlsConfig *tls.Config, logger *logrus.Logger) (*WebSocketClient, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
	}
	if timeouts == nil {
		timeouts = types.DefaultTimeouts()
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &WebSocketClient{
		apiURL:                 apiURL,
		token:                  token,
		agentID:                agentID,
		logger:                 logger,
		tlsConfig:              tlsConfig,
		reconnectDelay:         timeouts.WebSocketReconnect,
		maxReconnectDelay:      timeouts.WebSocketReconnectMax,
		timeouts:               timeouts,
		ctx:                    ctx,
		cancel:                 cancel,
		requestHandlers:        make(map[string]chan *WebSocketMessage),
		activeProxyRequests:    make(map[string]context.CancelFunc),
		activeProxyWriters:     make(map[string]ProxyResponseWriter),
		activeRequestBodyPipes: make(map[string]*io.PipeWriter),
		unknownMessageTypes:    make(map[string]int),
		connected:              false,
	}

	client.wsProxyManager = NewWebSocketProxyManager(client, logger)

	return client, nil
}

// NewWebSocketClientWithGateway creates a new WebSocket client configured for gateway mode.
// This is used after registration when the control plane returns a gateway_ws_url.
// In gateway mode, the WebSocket connects directly to the gateway for heartbeat and proxy,
// bypassing the controller for real-time operations.
func NewWebSocketClientWithGateway(gatewayURL, token, agentID, clusterUUID, agentVersion string, timeouts *types.Timeouts, tlsConfig *tls.Config, logger *logrus.Logger) (*WebSocketClient, error) {
	if gatewayURL == "" {
		return nil, fmt.Errorf("gateway URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
	}
	if clusterUUID == "" {
		return nil, fmt.Errorf("cluster UUID is required for gateway authentication")
	}
	if timeouts == nil {
		timeouts = types.DefaultTimeouts()
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &WebSocketClient{
		apiURL:                 gatewayURL, // Store gateway URL as apiURL for reconnection
		token:                  token,
		agentID:                agentID,
		logger:                 logger,
		tlsConfig:              tlsConfig,
		reconnectDelay:         timeouts.WebSocketReconnect,
		maxReconnectDelay:      timeouts.WebSocketReconnectMax,
		timeouts:               timeouts,
		ctx:                    ctx,
		cancel:                 cancel,
		requestHandlers:        make(map[string]chan *WebSocketMessage),
		activeProxyRequests:    make(map[string]context.CancelFunc),
		activeProxyWriters:     make(map[string]ProxyResponseWriter),
		activeRequestBodyPipes: make(map[string]*io.PipeWriter),
		unknownMessageTypes:    make(map[string]int),
		connected:              false,
		gatewayMode:            true,
		gatewayURL:             gatewayURL,
		clusterUUID:            clusterUUID,
		agentVersion:           agentVersion,
	}

	client.wsProxyManager = NewWebSocketProxyManager(client, logger)

	return client, nil
}

// ConnectToGateway establishes a WebSocket connection to the gateway.
// Unlike Connect(), this uses the gateway URL format with cluster_uuid as query parameter.
// Token is passed via Authorization header for security.
func (c *WebSocketClient) ConnectToGateway() error {
	if !c.gatewayMode {
		return fmt.Errorf("not in gateway mode - use Connect() instead")
	}

	// Parse gateway URL
	u, err := url.Parse(c.gatewayURL)
	if err != nil {
		return fmt.Errorf("invalid gateway URL: %w", err)
	}

	// Ensure WebSocket scheme
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	}

	// Gateway endpoint path (if not already set)
	if u.Path == "" {
		u.Path = "/ws"
	}

	// Add cluster_uuid and capabilities as query parameters (gateway routing)
	// Token is passed via Authorization header for security
	q := u.Query()
	q.Set("cluster_uuid", c.clusterUUID)
	q.Set("supports_streaming", "true")
	q.Set("supports_binary", "true")
	q.Set("supports_websocket", "true")
	q.Set("supports_yamux", "true") // Yamux stream multiplexing for L4 tunnels
	q.Set("supports_response_streaming", "true")
	q.Set("agent_version", c.agentVersion)
	u.RawQuery = q.Encode()

	c.logger.WithFields(logrus.Fields{
		"url":          u.Host + u.Path,
		"cluster_uuid": c.clusterUUID,
	}).Debug("Connecting to gateway WebSocket endpoint")

	// Create WebSocket connection
	handshake := c.timeouts.WebSocketHandshake
	if handshake <= 0 {
		handshake = 10 * time.Second
	}

	dialer := websocket.Dialer{
		HandshakeTimeout:  handshake,
		EnableCompression: false,
	}

	if c.tlsConfig != nil {
		dialer.TLSClientConfig = c.tlsConfig.Clone()
		if dialer.TLSClientConfig == nil {
			dialer.TLSClientConfig = c.tlsConfig
		}
	}

	// Use Authorization header for token (more secure than query params)
	headers := make(map[string][]string)
	headers["Authorization"] = []string{"Bearer " + c.token}

	conn, resp, err := dialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("failed to connect to gateway (status %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}

	// Stop any existing yamux client from a previous connection before replacing the control WS
	if err := c.StopYamuxClient(); err != nil {
		c.logger.WithError(err).Warn("Error stopping previous yamux client during reconnect")
	}

	c.connMutex.Lock()
	c.conn = conn
	c.connMutex.Unlock()

	c.setConnected(true)
	if c.timeouts.WebSocketReconnect > 0 {
		c.reconnectDelay = c.timeouts.WebSocketReconnect
	} else {
		c.reconnectDelay = 1 * time.Second
	}

	// Set initial read deadline from configured timeout (fallback 60s)
	readDeadline := c.timeouts.WebSocketRead
	if readDeadline <= 0 {
		readDeadline = 60 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		c.logger.WithError(err).Warn("Failed to set initial read deadline")
	}

	// Set Pong handler to refresh read deadline
	conn.SetPongHandler(func(string) error {
		// c.logger.Debug("Pong received from gateway (control frame)")
		return conn.SetReadDeadline(time.Now().Add(readDeadline))
	})

	c.logger.Info("WebSocket connection established with gateway")

	// Start message reader
	c.wg.Add(1)
	go c.readMessages()

	// Start ping/pong handler
	c.wg.Add(1)
	go c.pingHandler()

	return nil
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

// SetOnWebSocketData registers a callback for receiving WebSocket data from the control plane
// This is used for zero-copy proxying of application service WebSocket connections
func (c *WebSocketClient) SetOnWebSocketData(callback func(requestID string, data []byte)) {
	c.onWebSocketDataMutex.Lock()
	c.onWebSocketData = callback
	c.onWebSocketDataMutex.Unlock()
}

// SetOnTCPStart registers a callback for TCP tunnel start requests
func (c *WebSocketClient) SetOnTCPStart(callback func(*TCPTunnelStart)) {
	c.onTCPStartMutex.Lock()
	c.onTCPStart = callback
	c.onTCPStartMutex.Unlock()
}

// SetOnTCPData registers a callback for TCP tunnel data
func (c *WebSocketClient) SetOnTCPData(callback func(requestID string, data []byte)) {
	c.onTCPDataMutex.Lock()
	c.onTCPData = callback
	c.onTCPDataMutex.Unlock()
}

// SetOnTCPClose registers a callback for TCP tunnel close requests
func (c *WebSocketClient) SetOnTCPClose(callback func(requestID string, reason string)) {
	c.onTCPCloseMutex.Lock()
	c.onTCPClose = callback
	c.onTCPCloseMutex.Unlock()
}

// SetOnUDPData registers a callback for UDP tunnel data
func (c *WebSocketClient) SetOnUDPData(callback func(*UDPTunnelData)) {
	c.onUDPDataMutex.Lock()
	c.onUDPData = callback
	c.onUDPDataMutex.Unlock()
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

	// Log URL without token for security
	c.logger.WithField("url", u.Host+u.Path).Debug("Connecting to WebSocket endpoint")

	// Create WebSocket connection with Authorization header
	handshake := c.timeouts.WebSocketHandshake
	if handshake <= 0 {
		handshake = 10 * time.Second
	}

	dialer := websocket.Dialer{
		HandshakeTimeout:  handshake,
		EnableCompression: false,
	}

	if c.tlsConfig != nil {
		dialer.TLSClientConfig = c.tlsConfig.Clone()
		if dialer.TLSClientConfig == nil {
			dialer.TLSClientConfig = c.tlsConfig
		}
	}

	// Use Authorization header for authentication (more secure than query params)
	// Query params can be logged by proxies and appear in server access logs
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
	// Reset reconnect delay to configured baseline so user backoff policy is honored after reconnects
	if c.timeouts.WebSocketReconnect > 0 {
		c.reconnectDelay = c.timeouts.WebSocketReconnect
	} else {
		c.reconnectDelay = 1 * time.Second
	}

	// Set initial read deadline from configured timeout (fallback 60s) to detect half-open connections
	readDeadline := c.timeouts.WebSocketRead
	if readDeadline <= 0 {
		readDeadline = 60 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		c.logger.WithError(err).Warn("Failed to set initial read deadline")
	}

	// Set Pong handler to refresh read deadline
	conn.SetPongHandler(func(string) error {
		// c.logger.Debug("Pong received from control plane (control frame)")
		return conn.SetReadDeadline(time.Now().Add(readDeadline))
	})

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
			"supports_streaming":          true,
			"supports_binary_protocol":    true,
			"supports_compression":        true,
			"supports_websocket_proxy":    true,
			"supports_gateway":            true,
			"supports_response_streaming": true,
		},
	}

	if len(agent.Metadata) > 0 {
		payload["metadata"] = agent.Metadata
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
	case <-time.After(120 * time.Second):
		return nil, fmt.Errorf("registration timed out after 120 seconds")
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

	if heartbeat.WorkerJoin != nil {
		msg.Payload["worker_join"] = heartbeat.WorkerJoin
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

	// Stop yamux client and close dedicated yamux WebSocket
	if err := c.StopYamuxClient(); err != nil {
		c.logger.WithError(err).Warn("Error stopping yamux client during close")
	}

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

// readMessages reads messages from the WebSocket connection.
// Single-WS architecture: demuxes text (JSON control) vs binary (yamux) frames.
// Binary frames are fed to the yamux WSConn pipe; text frames are JSON-decoded and dispatched.
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

			messageType, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					c.logger.WithError(err).Warn("WebSocket connection closed unexpectedly")
				}
				c.setConnected(false)
				c.reconnect()
				return
			}

			// Refresh read deadline on every message received
			if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
				c.logger.WithError(err).Debug("Failed to refresh read deadline")
			}

			// Demux based on message type:
			// - TextMessage: always JSON control protocol
			// - BinaryMessage: yamux frames when yamux is active, otherwise legacy
			//   JSON-in-binary (the controller's non-gateway path sends JSON as
			//   BinaryMessage via SendJSON → WriteMessage(BinaryMessage, ...))
			if messageType == websocket.BinaryMessage {
				c.yamuxClientMutex.RLock()
				wrapper := c.yamuxClient
				c.yamuxClientMutex.RUnlock()

				if wrapper != nil && wrapper.wsConn != nil {
					// Yamux is active — feed binary data to the yamux pipe
					if err := wrapper.wsConn.FeedBinaryData(data); err != nil {
						c.logger.WithError(err).Warn("[YAMUX] Failed to feed binary data to yamux")
					}
				} else {
					// Yamux NOT active — treat binary as legacy JSON-in-binary.
					// The controller's non-gateway path sends JSON payloads as
					// BinaryMessage frames (SendJSON uses WriteMessage(BinaryMessage, ...)).
					var msg WebSocketMessage
					if err := json.Unmarshal(data, &msg); err != nil {
						c.logger.WithError(err).WithField("data_len", len(data)).Debug("Received non-JSON binary message with no yamux session, ignoring")
					} else {
						c.handleMessage(&msg)
					}
				}
				continue
			}

			// Text message — JSON decode and dispatch
			if messageType == websocket.TextMessage {
				var msg WebSocketMessage
				if err := json.Unmarshal(data, &msg); err != nil {
					c.logger.WithError(err).Warn("Failed to unmarshal WebSocket text message")
					continue
				}
				c.handleMessage(&msg)
			}
		}
	}
}

// handleMessage handles incoming WebSocket messages
// sanitizeLogValue strips \r and \n from log values to prevent log forging.
func sanitizeLogValue(val string) string {
	val = strings.ReplaceAll(val, "\r", "")
	val = strings.ReplaceAll(val, "\n", "")
	return val
}

func isValidWebSocketMessageType(messageType int) bool {
	switch messageType {
	case websocket.TextMessage, websocket.BinaryMessage, websocket.CloseMessage, websocket.PingMessage, websocket.PongMessage:
		return true
	default:
		return false
	}
}

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

	case "register_in_progress":
		// Control plane is processing the registration - just log and wait
		c.logger.WithFields(logrus.Fields{
			"request_id": msg.RequestID,
			"msg_type":   msg.Type,
		}).Debug("Registration is in progress on control plane (this extends the timeout window)")

	case "heartbeat_ack":
		c.logger.Debug("Heartbeat acknowledged by control plane")

	case "gateway_hello":
		// Gateway is sending protocol negotiation response
		c.handleGatewayHello(msg)

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

	case "proxy_request_chunk":
		// Handle streaming request body chunk from controller
		if msg.RequestID == "" {
			c.logger.Warn("Received proxy_request_chunk without request_id")
			return
		}

		c.requestBodyPipesMutex.RLock()
		pipeWriter, ok := c.activeRequestBodyPipes[msg.RequestID]
		c.requestBodyPipesMutex.RUnlock()

		if !ok {
			safeRequestID := strings.ReplaceAll(strings.ReplaceAll(msg.RequestID, "\n", ""), "\r", "")
			c.logger.WithField("request_id", safeRequestID).Warn("Received request chunk for unknown request - no pipe found")
			return
		}

		// Extract data from payload — accept both "data" (agent convention) and "chunk" (gateway convention)
		dataStr, ok := msg.Payload["data"].(string)
		if !ok {
			dataStr, ok = msg.Payload["chunk"].(string)
		}
		if !ok {
			c.logger.WithField("request_id", msg.RequestID).Warn("Invalid data in proxy_request_chunk")
			return
		}

		// Decode base64 data
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			c.logger.WithError(err).WithField("request_id", msg.RequestID).Error("Failed to decode request chunk data")
			return
		}

		// Write chunk to pipe (blocks if pipe buffer is full)
		_, err = pipeWriter.Write(data)
		if err != nil {
			c.logger.WithError(err).WithField("request_id", msg.RequestID).Error("Failed to write request chunk to pipe")
			return
		}

		c.logger.WithFields(logrus.Fields{
			"request_id": msg.RequestID,
			"chunk_size": len(data),
		}).Info("📦 Wrote request body chunk to pipe")

	case "proxy_request_stream_end":
		// Handle end of streaming request body from controller
		if msg.RequestID == "" {
			c.logger.Warn("Received proxy_request_stream_end without request_id")
			return
		}

		c.requestBodyPipesMutex.Lock()
		pipeWriter, ok := c.activeRequestBodyPipes[msg.RequestID]
		if ok {
			// Close the pipe writer to signal EOF to the reader
			pipeWriter.Close()
			delete(c.activeRequestBodyPipes, msg.RequestID)
		}
		c.requestBodyPipesMutex.Unlock()

		if !ok {
			safeRequestID := strings.ReplaceAll(strings.ReplaceAll(msg.RequestID, "\n", ""), "\r", "")
			c.logger.WithField("request_id", safeRequestID).Warn("Received stream end for unknown request - no pipe found")
			return
		}

		c.logger.WithField("request_id", msg.RequestID).Info("✅ Request body stream ended, pipe closed")

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

		// Also clean up any request body pipe if one exists
		c.requestBodyPipesMutex.Lock()
		if pipeWriter, ok := c.activeRequestBodyPipes[msg.RequestID]; ok {
			pipeWriter.CloseWithError(fmt.Errorf("request cancelled"))
			delete(c.activeRequestBodyPipes, msg.RequestID)
		}
		c.requestBodyPipesMutex.Unlock()

	case "proxy_stream_data":
		// Handle streaming data from controller for bidirectional WebSocket
		if msg.RequestID == "" {
			c.logger.Warn("Received proxy_stream_data without request_id")
			return
		}

		c.activeWritersMutex.RLock()
		writer, ok := c.activeProxyWriters[msg.RequestID]
		c.activeWritersMutex.RUnlock()

		if !ok {
			safeRequestID := strings.ReplaceAll(strings.ReplaceAll(msg.RequestID, "\n", ""), "\r", "")
			c.logger.WithField("request_id", safeRequestID).Debug("Received stream data for unknown request")
			return
		}

		// Extract data from payload
		dataStr, ok := msg.Payload["data"].(string)
		if !ok {
			c.logger.WithField("request_id", msg.RequestID).Warn("Invalid data in proxy_stream_data")
			return
		}

		// Decode base64 data
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			c.logger.WithError(err).WithField("request_id", msg.RequestID).Error("Failed to decode stream data")
			return
		}

		// Send to writer's stream channel (non-blocking)
		if writer.DeliverStreamData(data) {
			c.logger.WithFields(logrus.Fields{
				"request_id": msg.RequestID,
				"data_size":  len(data),
			}).Debug("Forwarded stream data to writer")
		} else {
			c.logger.WithField("request_id", msg.RequestID).Warn("Stream channel full, dropping data")
		}

	case "proxy_websocket_start", "ws_start":
		if c.wsProxyManager != nil {
			// Handle synchronously so the stream is registered before any subsequent
			// proxy_websocket_data messages are processed.
			c.wsProxyManager.HandleWebSocketProxyStart(msg)
		} else {
			c.logger.Warn("WebSocket proxy manager not initialized")
		}

	case "proxy_websocket_data", "ws_data":
		streamID, _ := msg.Payload["stream_id"].(string)

		// kubectl-style WebSocket proxy streams are explicitly created via proxy_websocket_start.
		// Some controllers may include "stream_id" in all proxy_websocket_data payloads; only treat
		// it as kubectl-style when we actually have an active kubectl WebSocket stream.
		if c.wsProxyManager != nil && streamID != "" && c.wsProxyManager.HasStream(streamID) {
			c.wsProxyManager.HandleWebSocketProxyData(msg)
			return
		}

		// This is application service WebSocket proxy (zero-copy mode)
		c.onWebSocketDataMutex.RLock()
		handler := c.onWebSocketData
		c.onWebSocketDataMutex.RUnlock()

		if handler == nil {
			c.logger.Warn("No WebSocket data handler registered - dropping proxy_websocket_data")
			return
		}

		// Extract data from payload
		dataStr, ok := msg.Payload["data"].(string)
		if !ok {
			c.logger.WithField("request_id", sanitizeLogValue(msg.RequestID)).Error("Missing or invalid data in proxy_websocket_data")
			return
		}

		// Decode base64 data
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			c.logger.WithError(err).WithField("request_id", sanitizeLogValue(msg.RequestID)).Error("Failed to decode WebSocket data")
			return
		}

		// Normalize payloads from controllers that send raw WS data + a separate message_type field.
		// Our agent-side forwarding expects [1 byte message type][frame data].
		if len(data) == 0 || !isValidWebSocketMessageType(int(data[0])) {
			if mt, ok := msg.Payload["message_type"]; ok {
				var messageType int
				switch v := mt.(type) {
				case float64:
					messageType = int(v)
				case int:
					messageType = v
				case int32:
					messageType = int(v)
				case int64:
					messageType = int(v)
				}
				if isValidWebSocketMessageType(messageType) {
					framed := make([]byte, 1+len(data))
					framed[0] = byte(messageType)
					copy(framed[1:], data)
					data = framed
				}
			}
		}

		// Determine the stream/request ID used by the agent's wsStreams map.
		// Prefer payload request_id/stream_id when present for compatibility with controllers
		// that don't set the top-level request_id field.
		targetRequestID := msg.RequestID
		if rid, ok := msg.Payload["request_id"].(string); ok && rid != "" {
			targetRequestID = rid
		} else if streamID != "" {
			targetRequestID = streamID
		}

		if targetRequestID == "" {
			c.logger.Error("Missing request_id for proxy_websocket_data")
			return
		}

		// Call the handler with the decoded data
		go handler(targetRequestID, data)

	case "proxy_websocket_close", "ws_close":
		if c.wsProxyManager != nil {
			c.wsProxyManager.HandleWebSocketProxyClose(msg)
		} else {
			c.logger.Warn("WebSocket proxy manager not initialized")
		}

	case "tcp_tunnel_start":
		// Parse TCP tunnel start request
		req := &TCPTunnelStart{
			RequestID: msg.RequestID,
		}

		if tunnelID, ok := msg.Payload["tunnel_id"].(string); ok {
			req.TunnelID = tunnelID
		}
		if gatewayName, ok := msg.Payload["gateway_name"].(string); ok {
			req.GatewayName = gatewayName
		}
		if gatewayNamespace, ok := msg.Payload["gateway_namespace"].(string); ok {
			req.GatewayNamespace = gatewayNamespace
		}
		if gatewayPort, ok := msg.Payload["gateway_port"].(float64); ok {
			req.GatewayPort = int32(gatewayPort)
		}
		if serviceName, ok := msg.Payload["service_name"].(string); ok {
			req.ServiceName = serviceName
		}
		if serviceNamespace, ok := msg.Payload["service_namespace"].(string); ok {
			req.ServiceNamespace = serviceNamespace
		}
		if servicePort, ok := msg.Payload["service_port"].(float64); ok {
			req.ServicePort = int32(servicePort)
		}
		if clientAddr, ok := msg.Payload["client_addr"].(string); ok {
			req.ClientAddr = clientAddr
		}

		c.onTCPStartMutex.RLock()
		handler := c.onTCPStart
		c.onTCPStartMutex.RUnlock()

		if handler == nil {
			c.logger.Warn("No TCP tunnel start handler registered - dropping request")
			return
		}

		go handler(req)

	case "tcp_tunnel_data":
		// Extract data from payload
		dataStr, ok := msg.Payload["data"].(string)
		if !ok {
			c.logger.WithField("request_id", msg.RequestID).Warn("Invalid data in tcp_tunnel_data")
			return
		}

		// Decode base64 data
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			c.logger.WithError(err).WithField("request_id", msg.RequestID).Error("Failed to decode TCP tunnel data")
			return
		}

		c.onTCPDataMutex.RLock()
		handler := c.onTCPData
		c.onTCPDataMutex.RUnlock()

		if handler == nil {
			c.logger.Warn("No TCP tunnel data handler registered - dropping data")
			return
		}

		go handler(msg.RequestID, data)

	case "tcp_tunnel_close":
		reason := ""
		if r, ok := msg.Payload["reason"].(string); ok {
			reason = r
		}

		c.onTCPCloseMutex.RLock()
		handler := c.onTCPClose
		c.onTCPCloseMutex.RUnlock()

		if handler == nil {
			c.logger.Warn("No TCP tunnel close handler registered")
			return
		}

		go handler(msg.RequestID, reason)

	case "udp_tunnel_data":
		// Parse UDP tunnel data request
		req := &UDPTunnelData{}

		if tunnelID, ok := msg.Payload["tunnel_id"].(string); ok {
			req.TunnelID = tunnelID
		}
		if dataStr, ok := msg.Payload["data"].(string); ok {
			data, err := base64.StdEncoding.DecodeString(dataStr)
			if err != nil {
				c.logger.WithError(err).WithField("tunnel_id", req.TunnelID).Error("Failed to decode UDP tunnel data")
				return
			}
			req.Data = data
		}
		if clientAddr, ok := msg.Payload["client_addr"].(string); ok {
			req.ClientAddr = clientAddr
		}
		if clientPort, ok := msg.Payload["client_port"].(float64); ok {
			req.ClientPort = int(clientPort)
		}
		if gatewayName, ok := msg.Payload["gateway_name"].(string); ok {
			req.GatewayName = gatewayName
		}
		if gatewayNamespace, ok := msg.Payload["gateway_namespace"].(string); ok {
			req.GatewayNamespace = gatewayNamespace
		}
		if gatewayPort, ok := msg.Payload["gateway_port"].(float64); ok {
			req.GatewayPort = int32(gatewayPort)
		}
		if serviceName, ok := msg.Payload["service_name"].(string); ok {
			req.ServiceName = serviceName
		}
		if serviceNamespace, ok := msg.Payload["service_namespace"].(string); ok {
			req.ServiceNamespace = serviceNamespace
		}
		if servicePort, ok := msg.Payload["service_port"].(float64); ok {
			req.ServicePort = int32(servicePort)
		}

		c.onUDPDataMutex.RLock()
		handler := c.onUDPData
		c.onUDPDataMutex.RUnlock()

		if handler == nil {
			c.logger.Warn("No UDP tunnel data handler registered - dropping datagram")
			return
		}

		go handler(req)

	case "error":
		errorMsg := ""
		if err, ok := msg.Payload["error"].(string); ok {
			errorMsg = err
		}
		c.logger.WithFields(logrus.Fields{
			"error":      errorMsg,
			"request_id": sanitizeLogValue(msg.RequestID),
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
		// Handle unknown message types with detailed logging and optional fallback notification
		c.handleUnknownMessageType(msg)
	}
}

// handleUnknownMessageType handles unknown message types with detailed logging and optional fallback notification
func (c *WebSocketClient) handleUnknownMessageType(msg *WebSocketMessage) {
	// Sanitize message type and request ID to prevent log injection
	safeType := sanitizeLogValue(msg.Type)
	safeRequestID := sanitizeLogValue(msg.RequestID)

	// Track this unknown message type
	c.unknownMessageMutex.Lock()
	c.unknownMessageTypes[msg.Type]++
	count := c.unknownMessageTypes[msg.Type]
	c.unknownMessageMutex.Unlock()

	// Log with full context
	logFields := logrus.Fields{
		"message_type": safeType,
		"count":        count,
	}
	if msg.RequestID != "" {
		logFields["request_id"] = safeRequestID
	}

	// Log at WARN level for persistent unknown types (seen multiple times)
	if count > 1 {
		c.logger.WithFields(logFields).Warn("Persistent unknown message type received from controller - may indicate protocol version mismatch")
	} else {
		c.logger.WithFields(logFields).Warn("Unknown message type received from controller")
	}

	// Send protocol_fallback message on first occurrence to help controller diagnose
	if count == 1 {
		c.sendProtocolFallback(msg.Type, safeRequestID)
	}
}

// sendProtocolFallback sends an optional control message back to controller
// listing supported types and observed fallback event
func (c *WebSocketClient) sendProtocolFallback(unknownType string, requestID string) {
	// List of message types this agent supports
	supportedTypes := []string{
		"register_success",
		"register_in_progress",
		"heartbeat_ack",
		"pong",
		"ping",
		"proxy_request",
		"proxy_request_chunk",
		"proxy_request_stream_end",
		"proxy_cancel",
		"proxy_stream_data",
		"proxy_websocket_start",
		"proxy_websocket_data",
		"proxy_websocket_close",
		"ws_start",
		"ws_data",
		"ws_close",
		"error",
	}

	payload := map[string]interface{}{
		"agent_version":    "pipeops-k8-agent",
		"unknown_type":     unknownType,
		"supported_types":  supportedTypes,
		"fallback_mode":    "legacy_proxy",
		"protocol_version": "1.0",
		"timestamp":        time.Now().Format(time.RFC3339),
	}

	if requestID != "" {
		payload["original_request_id"] = requestID
	}

	msg := &WebSocketMessage{
		Type:      "protocol_fallback",
		RequestID: "", // This is a control message, not a response to a specific request
		Payload:   payload,
		Timestamp: time.Now(),
	}

	if err := c.sendMessage(msg); err != nil {
		c.logger.WithError(err).Debug("Failed to send protocol_fallback message to controller")
	} else {
		safeUnknownType := sanitizeLogValue(unknownType)
		c.logger.WithFields(logrus.Fields{
			"unknown_type":    safeUnknownType,
			"supported_types": supportedTypes,
		}).Info("Sent protocol_fallback message to controller")
	}
}

func (c *WebSocketClient) dispatchStreamingProxyRequest(req *ProxyRequest, handler func(*ProxyRequest, ProxyResponseWriter)) {
	// Choose writer based on gateway capability: streaming sends chunks immediately,
	// buffered collects the entire response and sends on Close().
	var writer ProxyResponseWriter
	if req.SupportsResponseStreaming {
		sw := newStreamingProxyResponseWriter(c, req.RequestID, c.logger)
		writer = sw
		// Ensure cleanup on completion
		defer sw.ensureClosed()
	} else {
		bw := newBufferedProxyResponseWriter(c, req.RequestID, c.logger)
		writer = bw
		// Ensure cleanup on completion
		defer bw.ensureClosed()
	}

	// Register writer for bidirectional streaming
	c.activeWritersMutex.Lock()
	c.activeProxyWriters[req.RequestID] = writer
	c.activeWritersMutex.Unlock()

	// Ensure cleanup on completion
	defer func() {
		c.activeWritersMutex.Lock()
		delete(c.activeProxyWriters, req.RequestID)
		c.activeWritersMutex.Unlock()
	}()

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

// SendProxyResponseHeader sends the initial headers for a streaming proxy response.
// This is sent once at the beginning of a streaming response to deliver status code and headers immediately.
func (c *WebSocketClient) SendProxyResponseHeader(ctx context.Context, requestID string, status int, headers map[string][]string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	msg := &WebSocketMessage{
		Type:      "proxy_response_header",
		RequestID: requestID,
		Payload: map[string]interface{}{
			"status":  status,
			"headers": headers,
		},
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// SendProxyResponseChunk sends a chunk of body data for a streaming proxy response.
// The chunk is base64-encoded for JSON transport.
func (c *WebSocketClient) SendProxyResponseChunk(ctx context.Context, requestID string, chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	msg := &WebSocketMessage{
		Type:      "proxy_response_chunk",
		RequestID: requestID,
		Payload: map[string]interface{}{
			"data":     base64.StdEncoding.EncodeToString(chunk),
			"encoding": "base64",
			"size":     len(chunk),
		},
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// SendProxyResponseEnd signals that a streaming proxy response is complete.
func (c *WebSocketClient) SendProxyResponseEnd(ctx context.Context, requestID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	msg := &WebSocketMessage{
		Type:      "proxy_response_end",
		RequestID: requestID,
		Payload:   map[string]interface{}{},
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// SendProxyResponseAbort signals that a streaming proxy response was aborted due to an error.
func (c *WebSocketClient) SendProxyResponseAbort(ctx context.Context, requestID string, reason string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	msg := &WebSocketMessage{
		Type:      "proxy_response_abort",
		RequestID: requestID,
		Payload: map[string]interface{}{
			"error": reason,
		},
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// SendWebSocketData sends WebSocket frame data to the control plane for zero-copy proxying
func (c *WebSocketClient) SendWebSocketData(ctx context.Context, requestID string, messageType int, data []byte) error {
	if requestID == "" {
		return fmt.Errorf("request ID is required")
	}

	// Encode the full WebSocket message (type + data)
	fullData := make([]byte, 1+len(data))
	fullData[0] = byte(messageType)
	copy(fullData[1:], data)

	// Base64 encode for JSON transport
	encoded := base64.StdEncoding.EncodeToString(fullData)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: requestID,
		Payload: map[string]interface{}{
			"request_id":   requestID,
			"stream_id":    requestID,
			"data":         encoded,
			"message_type": messageType,
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

// sendBinaryMessage sends a binary message via WebSocket (for large payloads)
// This avoids the ~33% overhead of base64 encoding in JSON
func (c *WebSocketClient) sendBinaryMessage(data []byte) error {
	c.connMutex.RLock()
	conn := c.conn
	c.connMutex.RUnlock()

	if conn == nil {
		return fmt.Errorf("WebSocket connection is nil")
	}

	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		c.logger.WithError(err).Debug("Failed to set write deadline for binary WebSocket message")
	}

	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// SendProxyResponseBinary sends a proxy response using binary protocol for large payloads
// Binary frame format: [2 bytes reqID length][reqID][4 bytes status][4 bytes header length][headers JSON][body]
// This eliminates base64 overhead (~33% bandwidth savings) for large responses
func (c *WebSocketClient) SendProxyResponseBinary(ctx context.Context, response *ProxyResponse, bodyBytes []byte) error {
	if response == nil {
		return fmt.Errorf("proxy response is nil")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Encode request ID
	reqIDBytes := []byte(response.RequestID)
	reqIDLen := len(reqIDBytes)

	// Encode headers as JSON
	headersJSON, err := json.Marshal(response.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}

	// Build binary frame:
	// [2 bytes reqID len][reqID][4 bytes status][4 bytes headers len][headers JSON][body]
	const headerOverhead = 2 + 4 + 4
	if reqIDLen > math.MaxInt-headerOverhead-len(headersJSON)-len(bodyBytes) {
		return fmt.Errorf("frame size computation overflow")
	}
	frameSize := headerOverhead + reqIDLen + len(headersJSON) + len(bodyBytes)
	frame := make([]byte, frameSize)

	offset := 0

	// Request ID length (2 bytes, big endian)
	frame[offset] = byte(reqIDLen >> 8)
	frame[offset+1] = byte(reqIDLen)
	offset += 2

	// Request ID
	copy(frame[offset:], reqIDBytes)
	offset += reqIDLen

	// Status code (4 bytes, big endian)
	frame[offset] = byte(response.Status >> 24)
	frame[offset+1] = byte(response.Status >> 16)
	frame[offset+2] = byte(response.Status >> 8)
	frame[offset+3] = byte(response.Status)
	offset += 4

	// Headers length (4 bytes, big endian)
	headersLen := len(headersJSON)
	frame[offset] = byte(headersLen >> 24)
	frame[offset+1] = byte(headersLen >> 16)
	frame[offset+2] = byte(headersLen >> 8)
	frame[offset+3] = byte(headersLen)
	offset += 4

	// Headers JSON
	copy(frame[offset:], headersJSON)
	offset += len(headersJSON)

	// Body (raw bytes, no base64)
	copy(frame[offset:], bodyBytes)

	return c.sendBinaryMessage(frame)
}

// pingHandler sends periodic pings and handles pongs
func (c *WebSocketClient) pingHandler() {
	defer c.wg.Done()

	interval := c.timeouts.WebSocketPing
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
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
			deadline := time.Now().Add(c.timeouts.WebSocketRead)
			if c.timeouts.WebSocketRead <= 0 {
				deadline = time.Now().Add(10 * time.Second)
			}
			c.writeMutex.Lock()
			err := conn.WriteControl(websocket.PingMessage, []byte{}, deadline)
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
		"base_delay":   c.reconnectDelay,
		"jitter":       jitter,
		"total_delay":  delay,
		"next_delay":   c.reconnectDelay * 2,
		"gateway_mode": c.gatewayMode,
	}).Info("Attempting to reconnect to WebSocket")

	// Wait for delay or context cancellation
	select {
	case <-c.ctx.Done():
		c.logger.Debug("Context cancelled during reconnect delay, aborting")
		return
	case <-time.After(delay):
	}

	// Check context again before connecting
	if c.ctx.Err() != nil {
		c.logger.Debug("Context cancelled, aborting reconnect")
		return
	}

	// Use appropriate connect method based on mode
	var err error
	if c.gatewayMode {
		err = c.ConnectToGateway()
	} else {
		err = c.Connect()
	}

	if err != nil {
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

// SendBackpressureSignal sends a backpressure signal to the control plane
func (c *WebSocketClient) SendBackpressureSignal(ctx context.Context, requestID string, reason string) error {
	if !c.isConnected() {
		return fmt.Errorf("WebSocket not connected, cannot send backpressure signal")
	}

	payload := map[string]interface{}{
		"request_id": requestID,
		"reason":     reason,
	}

	msg := &WebSocketMessage{
		Type:      "backpressure",
		RequestID: requestID, // Include RequestID here too for direct correlation
		Payload:   payload,
		Timestamp: time.Now(),
	}

	if err := c.sendMessage(msg); err != nil {
		return fmt.Errorf("failed to send backpressure signal: %w", err)
	}

	c.logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"reason":     reason,
	}).Warn("Backpressure signal sent to control plane")

	return nil
}

type proxyResponseWriter struct {
	sender    proxyResponseSender
	logger    *logrus.Logger
	requestID string

	mu      sync.Mutex
	status  int
	headers map[string][]string
	buffer  bytes.Buffer

	closed     atomic.Bool
	streamChan chan []byte // Channel for bidirectional WebSocket data
}

func newBufferedProxyResponseWriter(sender proxyResponseSender, requestID string, logger *logrus.Logger) *proxyResponseWriter {
	return &proxyResponseWriter{
		sender:     sender,
		logger:     logger,
		requestID:  requestID,
		headers:    make(map[string][]string),
		streamChan: make(chan []byte, 100), // Buffered channel for incoming WebSocket frames
	}
}

// StreamChannel returns the channel for receiving data from the controller
func (w *proxyResponseWriter) StreamChannel() <-chan []byte {
	return w.streamChan
}

// DeliverStreamData sends data to the stream channel (non-blocking).
func (w *proxyResponseWriter) DeliverStreamData(data []byte) bool {
	select {
	case w.streamChan <- data:
		return true
	default:
		return false
	}
}

func (w *proxyResponseWriter) WriteHeader(status int, headers http.Header) error {
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

	// Close the stream channel to signal no more data
	close(w.streamChan)

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

	// Binary protocol threshold: use binary for payloads >= 10KB to avoid base64 overhead
	// Binary saves ~33% bandwidth by eliminating base64 encoding
	const binaryThreshold = 10 * 1024

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use binary protocol for large payloads (>= 10KB)
	if len(bodyBytes) >= binaryThreshold {
		response := &ProxyResponse{
			RequestID: w.requestID,
			Status:    status,
			Headers:   sanitizeHeadersForStatus(headers, status),
			Encoding:  "binary", // Signal to controller that body is in binary frame
		}

		// Try to compress before sending binary
		contentType := ""
		if ct, ok := headers["Content-Type"]; ok && len(ct) > 0 {
			contentType = ct[0]
		}

		finalBody := bodyBytes
		if shouldCompress(contentType, len(bodyBytes)) {
			compressed, err := compressData(bodyBytes)
			if err == nil && len(compressed) < len(bodyBytes) {
				finalBody = compressed
				response.Encoding = "binary+gzip"

				if w.logger != nil {
					w.logger.WithFields(logrus.Fields{
						"request_id":      w.requestID,
						"original_size":   len(bodyBytes),
						"compressed_size": len(compressed),
						"ratio":           fmt.Sprintf("%.2fx", float64(len(bodyBytes))/float64(len(compressed))),
						"protocol":        "binary",
					}).Debug("Using binary protocol with compression")
				}
			}
		}

		if w.logger != nil {
			w.logger.WithFields(logrus.Fields{
				"request_id": w.requestID,
				"body_size":  len(finalBody),
				"protocol":   "binary",
			}).Debug("Sending response via binary protocol (33% bandwidth savings)")
		}

		if err := w.sender.SendProxyResponseBinary(ctx, response, finalBody); err != nil {
			if w.logger != nil {
				w.logger.WithError(err).Warn("Failed to send binary proxy response, falling back to JSON")
			}
			// Fall through to JSON path below
		} else {
			return nil // Success with binary protocol
		}
	}

	// JSON protocol for small payloads or binary fallback
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
		Headers:   sanitizeHeadersForStatus(headers, status),
		Body:      encodedBody,
		Encoding:  encoding,
	}

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

// streamingProxyResponseWriter sends response headers and body chunks to the gateway immediately
// instead of buffering. This enables streaming responses (e.g., follow=true pod logs) that
// would otherwise time out with the buffered writer.
type streamingProxyResponseWriter struct {
	sender    proxyResponseSender
	logger    *logrus.Logger
	requestID string

	closed     atomic.Bool
	headerSent atomic.Bool
	streamChan chan []byte // Channel for bidirectional WebSocket data
}

func newStreamingProxyResponseWriter(sender proxyResponseSender, requestID string, logger *logrus.Logger) *streamingProxyResponseWriter {
	return &streamingProxyResponseWriter{
		sender:     sender,
		logger:     logger,
		requestID:  requestID,
		streamChan: make(chan []byte, 100),
	}
}

// StreamChannel returns the channel for receiving data from the controller
func (w *streamingProxyResponseWriter) StreamChannel() <-chan []byte {
	return w.streamChan
}

// DeliverStreamData sends data to the stream channel (non-blocking).
func (w *streamingProxyResponseWriter) DeliverStreamData(data []byte) bool {
	select {
	case w.streamChan <- data:
		return true
	default:
		return false
	}
}

func (w *streamingProxyResponseWriter) WriteHeader(status int, headers http.Header) error {
	if w.closed.Load() {
		return io.ErrClosedPipe
	}

	if status == 0 {
		status = http.StatusOK
	}

	sanitized := sanitizeHeadersForStatus(cloneHeaderMap(headers), status)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := w.sender.SendProxyResponseHeader(ctx, w.requestID, status, sanitized); err != nil {
		if w.logger != nil {
			w.logger.WithError(err).WithField("request_id", w.requestID).Warn("Failed to send streaming response header")
		}
		return err
	}

	w.headerSent.Store(true)

	if w.logger != nil {
		w.logger.WithFields(logrus.Fields{
			"request_id": w.requestID,
			"status":     status,
		}).Debug("Sent streaming response header")
	}

	return nil
}

func (w *streamingProxyResponseWriter) WriteChunk(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	if w.closed.Load() {
		return io.ErrClosedPipe
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := w.sender.SendProxyResponseChunk(ctx, w.requestID, data); err != nil {
		if w.logger != nil {
			w.logger.WithError(err).WithField("request_id", w.requestID).Warn("Failed to send streaming response chunk")
		}
		return err
	}

	return nil
}

func (w *streamingProxyResponseWriter) Close() error {
	return w.CloseWithError(nil)
}

func (w *streamingProxyResponseWriter) CloseWithError(closeErr error) error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Close the stream channel to signal no more data
	close(w.streamChan)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if closeErr != nil {
		sendErr := w.sender.SendProxyResponseAbort(ctx, w.requestID, closeErr.Error())
		if sendErr != nil {
			if w.logger != nil {
				w.logger.WithError(sendErr).Warn("Failed to send streaming response abort")
			}
		}
		return closeErr
	}

	if err := w.sender.SendProxyResponseEnd(ctx, w.requestID); err != nil {
		if w.logger != nil {
			w.logger.WithError(err).Warn("Failed to send streaming response end")
		}
		return err
	}

	return nil
}

func (w *streamingProxyResponseWriter) ensureClosed() {
	if w.closed.Load() {
		return
	}
	if err := w.Close(); err != nil {
		if w.logger != nil {
			w.logger.WithError(err).Warn("Streaming proxy response writer closed with error")
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
	return sanitizeHeadersForStatus(headers, 0)
}

// sanitizeHeadersForStatus removes hop-by-hop headers from proxy responses.
// For 101 Switching Protocols responses, Upgrade and Connection headers are
// preserved because they are required by the WebSocket handshake (RFC 6455 S4.2.2).
func sanitizeHeadersForStatus(headers map[string][]string, status int) map[string][]string {
	if len(headers) == 0 {
		return nil
	}

	isUpgrade := status == http.StatusSwitchingProtocols

	filtered := make(map[string][]string, len(headers))
	for key, values := range headers {
		lower := strings.ToLower(key)
		switch lower {
		case "connection", "upgrade":
			// Preserve for 101 Switching Protocols (WebSocket handshake)
			if !isUpgrade {
				continue
			}
		case "transfer-encoding", "keep-alive", "proxy-authenticate",
			"proxy-authorization", "te", "trailers":
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
		// Safety check: binary body protocol is incompatible with yamux.
		// When yamux is active, all BinaryMessage frames on the WebSocket are
		// routed to the yamux pipe by the readMessages() demuxer. Reading a
		// separate binary frame here would either consume a yamux frame
		// (corrupting the session) or block indefinitely. The gateway should
		// not send body_binary=true when yamux is negotiated, but guard
		// defensively in case it does.
		c.yamuxClientMutex.RLock()
		yamuxActive := c.yamuxClient != nil && c.yamuxClient.wsConn != nil
		c.yamuxClientMutex.RUnlock()

		if yamuxActive {
			return nil, fmt.Errorf("received body_binary=true proxy request but yamux is active; "+
				"binary body protocol is incompatible with yamux (request_id=%s)", msg.RequestID)
		}

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
	} else if rawBody, ok := payload["body"]; ok && rawBody != nil {
		// Some clients may send JSON bodies already decoded as objects/arrays.
		// Re-encode to bytes so downstream proxy handlers receive a request body.
		switch rawBody.(type) {
		case map[string]interface{}, []interface{}:
			encoded, err := json.Marshal(rawBody)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal proxy body: %w", err)
			}
			bodyBytes = encoded
			if bodyEncoding == "" {
				bodyEncoding = "json"
			}
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
		Scheme:       getString("scheme"), // Add original request scheme
	}

	// WebSocket-specific flags and initial stream bytes.
	if isWS, ok := payload["is_websocket"].(bool); ok {
		req.IsWebSocket = isWS
	}
	if isWS, ok := payload["isWebSocket"].(bool); ok && !req.IsWebSocket {
		req.IsWebSocket = isWS
	}
	if useZeroCopy, ok := payload["use_zero_copy"].(bool); ok {
		req.UseZeroCopy = useZeroCopy
	}
	if useZeroCopy, ok := payload["useZeroCopy"].(bool); ok && !req.UseZeroCopy {
		req.UseZeroCopy = useZeroCopy
	}

	if rawHeadData, ok := payload["head_data"]; ok && rawHeadData != nil {
		if decoded, decodeOK := decodeHeadData(rawHeadData); decodeOK {
			req.HeadData = decoded
		}
	} else if rawHeadData, ok := payload["headData"]; ok && rawHeadData != nil {
		if decoded, decodeOK := decodeHeadData(rawHeadData); decodeOK {
			req.HeadData = decoded
		}
	}

	// Some controllers send subprotocol separately from headers.
	// Ensure it is available to websocket dialers expecting Sec-WebSocket-Protocol.
	if protocol := strings.TrimSpace(getString("protocol")); protocol != "" {
		if !hasHeaderKeyCI(headers, "Sec-WebSocket-Protocol") {
			headers["Sec-WebSocket-Protocol"] = []string{protocol}
		}
		req.IsWebSocket = true
	}

	if supports, ok := payload["supports_streaming"].(bool); ok {
		req.SupportsStreaming = supports
	}

	// Parse response streaming capability flag from gateway
	if supports, ok := payload["supports_response_streaming"].(bool); ok {
		req.SupportsResponseStreaming = supports
	}

	// Check if this request will use streaming for the body
	// Accept both "use_streaming" (agent convention) and "streaming" (gateway convention) for compatibility
	useStreaming := false
	if v, ok := payload["use_streaming"].(bool); ok && v {
		useStreaming = true
	} else if v, ok := payload["streaming"].(bool); ok && v {
		useStreaming = true
	}
	if useStreaming {
		// Create a pipe for streaming the request body
		pipeReader, pipeWriter := io.Pipe()

		// Store the pipe writer so proxy_request_chunk can write to it
		c.requestBodyPipesMutex.Lock()
		c.activeRequestBodyPipes[msg.RequestID] = pipeWriter
		c.requestBodyPipesMutex.Unlock()

		// Attach the pipe reader to the request
		req.SetBodyStream(pipeReader)

		c.logger.WithField("request_id", msg.RequestID).Info("🔧 Created pipe for streaming request body - waiting for chunks")
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

	// Debug logging to see what we received
	if c.logger != nil {
		c.logger.WithFields(logrus.Fields{
			"request_id":   req.RequestID,
			"method":       req.Method,
			"path":         req.Path,
			"namespace":    req.Namespace,
			"service_name": req.ServiceName,
			"service_port": req.ServicePort,
		}).Debug("Parsed proxy request with route context")
	}

	return req, nil
}

func decodeHeadData(raw interface{}) ([]byte, bool) {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, false
		}
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
			return decoded, true
		}
		return []byte(v), true
	case []byte:
		return append([]byte(nil), v...), true
	case []interface{}:
		out := make([]byte, 0, len(v))
		for _, item := range v {
			num, ok := item.(float64)
			if !ok || num < 0 || num > 255 {
				return nil, false
			}
			out = append(out, byte(num))
		}
		return out, true
	default:
		return nil, false
	}
}

func hasHeaderKeyCI(headers map[string][]string, key string) bool {
	for k, values := range headers {
		if strings.EqualFold(k, key) && len(values) > 0 {
			return true
		}
	}
	return false
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

	// Extract gateway_ws_url for new architecture (controller -> gateway separation)
	if gatewayWSURL, ok := payload["gateway_ws_url"].(string); ok {
		result.GatewayWSURL = gatewayWSURL
	}

	logFields := logrus.Fields{
		"cluster_id":   result.ClusterID,
		"cluster_uuid": result.ClusterUUID,
		"name":         result.Name,
		"status":       result.Status,
		"workspace_id": result.WorkspaceID,
	}
	if result.GatewayWSURL != "" {
		logFields["gateway_ws_url"] = result.GatewayWSURL
	}
	c.logger.WithFields(logFields).Info("Agent registered successfully via WebSocket")

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

// IsGatewayMode returns true if the client is connected to the gateway
func (c *WebSocketClient) IsGatewayMode() bool {
	return c.gatewayMode
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

// SendTCPData sends TCP tunnel data to the gateway
func (c *WebSocketClient) SendTCPData(ctx context.Context, requestID string, data []byte) error {
	msg := &WebSocketMessage{
		Type:      "tcp_tunnel_data",
		RequestID: requestID,
		Payload: map[string]interface{}{
			"data":      base64.StdEncoding.EncodeToString(data),
			"direction": "agent_to_gateway",
		},
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// SendTCPClose sends a TCP tunnel close message to the gateway
func (c *WebSocketClient) SendTCPClose(ctx context.Context, requestID string, reason string, metrics map[string]interface{}) error {
	payload := map[string]interface{}{
		"reason": reason,
	}

	// Add metrics if provided
	for k, v := range metrics {
		payload[k] = v
	}

	msg := &WebSocketMessage{
		Type:      "tcp_tunnel_close",
		RequestID: requestID,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// SendUDPData sends UDP tunnel data to the gateway
func (c *WebSocketClient) SendUDPData(ctx context.Context, tunnelID string, data []byte, clientAddr string, clientPort int) error {
	msg := &WebSocketMessage{
		Type: "udp_tunnel_data",
		Payload: map[string]interface{}{
			"tunnel_id":   tunnelID,
			"data":        base64.StdEncoding.EncodeToString(data),
			"client_addr": clientAddr,
			"client_port": clientPort,
			"direction":   "agent_to_gateway",
		},
		Timestamp: time.Now(),
	}

	return c.sendMessage(msg)
}

// handleGatewayHello processes the gateway_hello message and initializes yamux if negotiated.
// In single-WS mode: sends gateway_hello_ack on the existing control WS, then creates a
// pipe-fed WSConn on the same connection and starts the yamux client. Binary frames are
// demuxed in readMessages() and fed to the WSConn via FeedBinaryData().
func (c *WebSocketClient) handleGatewayHello(msg *WebSocketMessage) {
	logger := c.logger.WithField("type", "gateway_hello")
	logger.Debug("Received gateway hello message")

	// Check if gateway wants to use yamux
	useYamux, _ := msg.Payload["use_yamux"].(bool)
	if !useYamux {
		logger.Debug("Gateway did not negotiate yamux, using JSON protocol")
		return
	}

	// Parse yamux configuration
	config := tunnel.DefaultYamuxConfig()
	if yamuxConfig, ok := msg.Payload["yamux_config"].(map[string]interface{}); ok {
		if windowSize, ok := yamuxConfig["max_stream_window_size"].(float64); ok && windowSize > 0 {
			config.MaxStreamWindowSize = uint32(windowSize)
		}
		if keepAlive, ok := yamuxConfig["keep_alive_interval_seconds"].(float64); ok && keepAlive > 0 {
			config.KeepAliveInterval = time.Duration(keepAlive) * time.Second
		}
		if connTimeout, ok := yamuxConfig["connection_timeout_seconds"].(float64); ok && connTimeout > 0 {
			config.ConnectionTimeout = time.Duration(connTimeout) * time.Second
		}
	}

	logger.WithFields(logrus.Fields{
		"max_stream_window_size": config.MaxStreamWindowSize,
		"keep_alive_interval":    config.KeepAliveInterval,
	}).Info("Gateway negotiated yamux protocol (single-WS mode)")

	// Send gateway_hello_ack on the existing control WS to confirm yamux
	ack := &WebSocketMessage{
		Type: "gateway_hello_ack",
		Payload: map[string]interface{}{
			"use_yamux": true,
		},
		Timestamp: time.Now(),
	}
	if err := c.sendMessage(ack); err != nil {
		logger.WithError(err).Error("Failed to send gateway_hello_ack, skipping yamux setup")
		return
	}

	// Create a pipe-fed WSConn on the existing connection.
	// Share c.writeMutex so JSON text writes and yamux binary writes are serialized.
	c.connMutex.RLock()
	conn := c.conn
	c.connMutex.RUnlock()
	if conn == nil {
		logger.Error("WebSocket connection is nil, cannot create WSConn for yamux")
		return
	}

	wsConn := tunnel.NewWSConn(conn, &c.writeMutex)

	// Start yamux client on the shared connection (non-blocking)
	if err := c.StartYamuxClient(wsConn, config); err != nil {
		logger.WithError(err).Error("Failed to start yamux client")
		wsConn.Close()
		return
	}

	logger.Info("Yamux tunnel client started on shared WebSocket (single-WS mode)")
}

// IsYamuxEnabled returns true if yamux was negotiated with the gateway
func (c *WebSocketClient) IsYamuxEnabled() bool {
	c.yamuxClientMutex.RLock()
	defer c.yamuxClientMutex.RUnlock()
	return c.yamuxEnabled
}

// StartYamuxClient starts the yamux tunnel client on the shared WebSocket connection.
// In single-WS mode, wsConn is a pipe-fed WSConn wrapping the existing control WS.
// Binary frames are fed by the readMessages() demuxer via wsConn.FeedBinaryData().
func (c *WebSocketClient) StartYamuxClient(wsConn *tunnel.WSConn, config tunnel.YamuxConfig) error {
	if wsConn == nil {
		return fmt.Errorf("no WSConn provided for yamux")
	}

	client, err := tunnel.NewYamuxTunnelClient(c.clusterUUID, wsConn, config, c.logger)
	if err != nil {
		return fmt.Errorf("failed to create yamux client: %w", err)
	}

	c.yamuxClientMutex.Lock()
	c.yamuxClient = &yamuxClientWrapper{client: client, wsConn: wsConn}
	c.yamuxEnabled = true
	c.yamuxClientMutex.Unlock()

	// Start accepting streams in a goroutine
	go func() {
		if err := client.Run(); err != nil {
			c.logger.WithError(err).Error("Yamux client error")
		}
		// When yamux client exits (session closed), clean up state
		c.yamuxClientMutex.Lock()
		c.yamuxEnabled = false
		c.yamuxClientMutex.Unlock()
		c.logger.Info("Yamux client stopped")
	}()

	c.logger.Info("Started yamux tunnel client on shared WebSocket")
	return nil
}

// StopYamuxClient stops the yamux tunnel client and closes the pipe-fed WSConn.
// It does NOT close the underlying WebSocket — that is owned by the main connection lifecycle.
func (c *WebSocketClient) StopYamuxClient() error {
	c.yamuxClientMutex.Lock()
	wrapper := c.yamuxClient
	c.yamuxClient = nil
	c.yamuxEnabled = false
	c.yamuxClientMutex.Unlock()

	if wrapper == nil {
		return nil
	}

	var firstErr error

	// Close the yamux client (closes all streams and yamux session)
	if wrapper.client != nil {
		if err := wrapper.client.Close(); err != nil {
			firstErr = err
		}
	}

	// Close the pipe-fed WSConn (closes the pipe, does NOT close the WebSocket)
	if wrapper.wsConn != nil {
		if err := wrapper.wsConn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	c.logger.Info("Stopped yamux tunnel client")
	return firstErr
}
