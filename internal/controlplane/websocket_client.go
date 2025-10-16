package controlplane

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// WebSocketClient represents a WebSocket client for control plane communication
type WebSocketClient struct {
	apiURL           string
	token            string
	agentID          string
	logger           *logrus.Logger
	conn             *websocket.Conn
	connMutex        sync.RWMutex
	reconnectDelay   time.Duration
	maxReconnectDelay time.Duration
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	requestHandlers  map[string]chan *WebSocketMessage
	handlerMutex     sync.RWMutex
	connected        bool
	connectedMutex   sync.RWMutex
}

// WebSocketMessage represents a message sent/received over WebSocket
type WebSocketMessage struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"request_id,omitempty"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

// NewWebSocketClient creates a new WebSocket client for control plane communication
func NewWebSocketClient(apiURL, token, agentID string, logger *logrus.Logger) (*WebSocketClient, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WebSocketClient{
		apiURL:            apiURL,
		token:             token,
		agentID:           agentID,
		logger:            logger,
		reconnectDelay:    1 * time.Second,
		maxReconnectDelay: 60 * time.Second,
		ctx:               ctx,
		cancel:            cancel,
		requestHandlers:   make(map[string]chan *WebSocketMessage),
		connected:         false,
	}, nil
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

	// Create WebSocket connection
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.Dial(u.String(), nil)
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

	// Close WebSocket connection
	c.connMutex.Lock()
	if c.conn != nil {
		c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(5*time.Second),
		)
		c.conn.Close()
		c.conn = nil
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

	case "error":
		errorMsg := ""
		if err, ok := msg.Payload["error"].(string); ok {
			errorMsg = err
		}
		c.logger.WithFields(logrus.Fields{
			"error":      errorMsg,
			"request_id": msg.RequestID,
		}).Error("Error message from control plane")

	default:
		c.logger.WithField("type", msg.Type).Warn("Unknown message type received")
	}
}

// sendMessage sends a message via WebSocket
func (c *WebSocketClient) sendMessage(msg *WebSocketMessage) error {
	c.connMutex.RLock()
	conn := c.conn
	c.connMutex.RUnlock()

	if conn == nil {
		return fmt.Errorf("WebSocket connection is nil")
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
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
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
	c.logger.WithField("delay", c.reconnectDelay).Info("Attempting to reconnect to WebSocket")

	time.Sleep(c.reconnectDelay)

	if err := c.Connect(); err != nil {
		c.logger.WithError(err).Warn("Reconnection failed")

		// Increase reconnect delay with exponential backoff
		c.reconnectDelay *= 2
		if c.reconnectDelay > c.maxReconnectDelay {
			c.reconnectDelay = c.maxReconnectDelay
		}

		// Try again
		go c.reconnect()
	} else {
		c.logger.Info("Reconnected successfully to WebSocket")
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

func (c *WebSocketClient) setConnected(connected bool) {
	c.connectedMutex.Lock()
	defer c.connectedMutex.Unlock()
	c.connected = connected
}
