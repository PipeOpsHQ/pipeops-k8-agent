package communication

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Client represents the WebSocket client for communicating with PipeOps Control Plane and Runner
type Client struct {
	config   *types.PipeOpsConfig
	logger   *logrus.Logger
	mu       sync.RWMutex
	closed   bool
	handlers map[types.MessageType]MessageHandler
	ctx      context.Context
	cancel   context.CancelFunc

	// Control Plane connection
	controlPlaneConn      *websocket.Conn
	controlPlaneConnected bool

	// Runner connection details (populated after Control Plane assigns Runner)
	runnerEndpoint  string
	runnerToken     string
	runnerConn      *websocket.Conn
	runnerConnected bool

	// Connection management
	reconnect     chan struct{}
	dualMode      bool // Enable dual connections after registration
	runnerEnabled bool // Flag to enable runner connection
}

// MessageHandler defines the interface for message handlers
type MessageHandler interface {
	Handle(ctx context.Context, msg *types.Message) (*types.Message, error)
}

// MessageHandlerFunc is a function adapter for MessageHandler
type MessageHandlerFunc func(ctx context.Context, msg *types.Message) (*types.Message, error)

func (f MessageHandlerFunc) Handle(ctx context.Context, msg *types.Message) (*types.Message, error) {
	return f(ctx, msg)
}

// NewClient creates a new communication client
func NewClient(config *types.PipeOpsConfig, logger *logrus.Logger) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		config:    config,
		logger:    logger,
		handlers:  make(map[types.MessageType]MessageHandler),
		ctx:       ctx,
		cancel:    cancel,
		reconnect: make(chan struct{}, 1),
	}
}

// RegisterHandler registers a message handler
func (c *Client) RegisterHandler(msgType types.MessageType, handler MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[msgType] = handler
}

// RegisterControlPlaneHandler registers a message handler for Control Plane messages (alias for compatibility)
func (c *Client) RegisterControlPlaneHandler(msgType types.MessageType, handler MessageHandler) {
	c.RegisterHandler(msgType, handler)
}

// RegisterRunnerHandler registers a message handler for Runner messages (alias for compatibility)
func (c *Client) RegisterRunnerHandler(msgType types.MessageType, handler MessageHandler) {
	c.RegisterHandler(msgType, handler)
}

// Connect establishes a WebSocket connection to the PipeOps control plane
func (c *Client) Connect() error {
	if c.config.APIURL == "" {
		return fmt.Errorf("PipeOps API URL is required")
	}

	if c.config.Token == "" {
		return fmt.Errorf("PipeOps token is required")
	}

	// Parse URL and convert to WebSocket URL
	u, err := url.Parse(c.config.APIURL)
	if err != nil {
		return fmt.Errorf("invalid API URL: %w", err)
	}

	// Convert HTTP(S) to WS(S)
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}

	// Add WebSocket endpoint
	u.Path = "/api/v1/agent/ws"

	// Set up headers
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.config.Token)
	headers.Set("User-Agent", "PipeOps-Agent/1.0")

	// Set up dialer with timeout
	dialer := websocket.Dialer{
		HandshakeTimeout: c.config.Timeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
	}

	// Configure TLS if needed
	if c.config.TLS.Enabled {
		dialer.TLSClientConfig = c.getTLSConfig()
	}

	c.logger.WithField("url", u.String()).Info("Connecting to PipeOps control plane")

	// Establish connection
	conn, _, err := dialer.Dial(u.String(), headers)
	if err != nil {
		return fmt.Errorf("failed to connect to PipeOps: %w", err)
	}

	c.mu.Lock()
	c.controlPlaneConn = conn
	c.closed = false
	c.controlPlaneConnected = true
	c.mu.Unlock()

	c.logger.Info("Successfully connected to PipeOps control plane")

	// Start message handling goroutines
	go c.readMessages()
	go c.handleReconnect()

	return nil
}

// Disconnect closes the WebSocket connections
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	c.cancel()

	// Disconnect Control Plane
	if c.controlPlaneConn != nil {
		// Send close frame
		err := c.controlPlaneConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			c.logger.WithError(err).Warn("Failed to send close frame to Control Plane")
		}

		// Close connection
		err = c.controlPlaneConn.Close()
		if err != nil {
			c.logger.WithError(err).Warn("Failed to close Control Plane connection")
		}

		c.controlPlaneConn = nil
		c.controlPlaneConnected = false
	}

	// Disconnect Runner
	if c.runnerConn != nil {
		// Send close frame
		err := c.runnerConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			c.logger.WithError(err).Warn("Failed to send close frame to Runner")
		}

		// Close connection
		err = c.runnerConn.Close()
		if err != nil {
			c.logger.WithError(err).Warn("Failed to close Runner connection")
		}

		c.runnerConn = nil
		c.runnerConnected = false
	}

	c.logger.Info("Disconnected from PipeOps Control Plane and Runner")
	return nil
}

// SendMessage sends a message to the appropriate endpoint (Control Plane or Runner)
func (c *Client) SendMessage(msg *types.Message) error {
	return c.SendMessageTo(msg, types.EndpointControlPlane)
}

// SendMessageTo sends a message to a specific endpoint
func (c *Client) SendMessageTo(msg *types.Message, endpoint types.MessageEndpoint) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return fmt.Errorf("client is closed")
	}

	var conn *websocket.Conn
	var endpointName string

	switch endpoint {
	case types.EndpointControlPlane:
		if c.controlPlaneConn == nil {
			return fmt.Errorf("control plane connection is not established")
		}
		conn = c.controlPlaneConn
		endpointName = "Control Plane"
	case types.EndpointRunner:
		if c.runnerConn == nil {
			return fmt.Errorf("runner connection is not established")
		}
		conn = c.runnerConn
		endpointName = "Runner"
	default:
		return fmt.Errorf("unknown endpoint: %v", endpoint)
	}

	// Set timestamp if not set
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	// Marshal message to JSON
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Send message
	err = conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		c.logger.WithError(err).WithField("endpoint", endpointName).Error("Failed to send message")
		// Trigger reconnection
		select {
		case c.reconnect <- struct{}{}:
		default:
		}
		return fmt.Errorf("failed to send message to %s: %w", endpointName, err)
	}

	c.logger.WithFields(logrus.Fields{
		"type":     msg.Type,
		"id":       msg.ID,
		"endpoint": endpointName,
	}).Debug("Message sent successfully")

	return nil
}

// readMessages continuously reads messages from both WebSocket connections
func (c *Client) readMessages() {
	defer func() {
		c.logger.Debug("Message reader stopped")
	}()

	// Start separate goroutines for each connection
	go c.readControlPlaneMessages()

	// Wait for context cancellation
	<-c.ctx.Done()
}

// readControlPlaneMessages reads messages from the Control Plane connection
func (c *Client) readControlPlaneMessages() {
	defer func() {
		c.logger.Debug("Control Plane message reader stopped")
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.controlPlaneConn
		closed := c.closed
		connected := c.controlPlaneConnected
		c.mu.RUnlock()

		if closed || conn == nil || !connected {
			time.Sleep(1 * time.Second)
			continue
		}

		// Set read deadline
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			c.logger.WithError(err).Error("Failed to set read deadline")
			continue
		}

		// Read message
		_, data, err := conn.ReadMessage()
		if err != nil {
			c.logger.WithError(err).Error("Failed to read message from Control Plane")

			// Check if it's a close error
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.logger.Info("Control Plane connection closed by server")
				c.mu.Lock()
				c.controlPlaneConnected = false
				c.mu.Unlock()
				return
			}

			// Trigger reconnection
			select {
			case c.reconnect <- struct{}{}:
			default:
			}
			return
		}

		// Parse message
		var msg types.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.WithError(err).Error("Failed to unmarshal Control Plane message")
			continue
		}

		c.logger.WithFields(logrus.Fields{
			"type":     msg.Type,
			"id":       msg.ID,
			"endpoint": "Control Plane",
		}).Debug("Message received")

		// Handle message
		go c.handleMessage(&msg, types.EndpointControlPlane)
	}
}

// readRunnerMessages reads messages from the Runner connection
func (c *Client) readRunnerMessages() {
	defer func() {
		c.logger.Debug("Runner message reader stopped")
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.runnerConn
		closed := c.closed
		connected := c.runnerConnected
		c.mu.RUnlock()

		if closed || conn == nil || !connected {
			time.Sleep(1 * time.Second)
			continue
		}

		// Set read deadline
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			c.logger.WithError(err).Error("Failed to set read deadline")
			continue
		}

		// Read message
		_, data, err := conn.ReadMessage()
		if err != nil {
			c.logger.WithError(err).Error("Failed to read message from Runner")

			// Check if it's a close error
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				c.logger.Info("Runner connection closed by server")
				c.mu.Lock()
				c.runnerConnected = false
				c.mu.Unlock()
				return
			}

			// Don't trigger reconnection for Runner - let Control Plane manage it
			return
		}

		// Parse message
		var msg types.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.WithError(err).Error("Failed to unmarshal Runner message")
			continue
		}

		c.logger.WithFields(logrus.Fields{
			"type":     msg.Type,
			"id":       msg.ID,
			"endpoint": "Runner",
		}).Debug("Message received")

		// Handle message
		go c.handleMessage(&msg, types.EndpointRunner)
	}
}

// handleMessage processes an incoming message from a specific endpoint
func (c *Client) handleMessage(msg *types.Message, endpoint types.MessageEndpoint) {
	c.mu.RLock()
	handler, exists := c.handlers[msg.Type]
	c.mu.RUnlock()

	if !exists {
		c.logger.WithFields(logrus.Fields{
			"type":     msg.Type,
			"endpoint": string(endpoint),
		}).Warn("No handler registered for message type")
		return
	}

	// Handle message with timeout
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()

	response, err := handler.Handle(ctx, msg)
	if err != nil {
		c.logger.WithError(err).WithField("endpoint", string(endpoint)).Error("Handler failed to process message")

		// Send error response if message expects a response
		if c.shouldSendResponse(msg.Type) {
			errorResponse := &types.Message{
				ID:        generateMessageID(),
				Type:      types.MessageTypeResponse,
				Timestamp: time.Now(),
				Error:     err.Error(),
			}
			// Send response back to the same endpoint
			c.SendMessageTo(errorResponse, endpoint)
		}
		return
	}

	// Send response if handler returned one
	if response != nil {
		// Send response back to the same endpoint that sent the original message
		if err := c.SendMessageTo(response, endpoint); err != nil {
			c.logger.WithError(err).WithField("endpoint", string(endpoint)).Error("Failed to send response message")
		}
	}
}

// handleReconnect handles reconnection logic
func (c *Client) handleReconnect() {
	if !c.config.Reconnect.Enabled {
		return
	}

	attempts := 0
	maxAttempts := c.config.Reconnect.MaxAttempts
	interval := c.config.Reconnect.Interval
	backoff := c.config.Reconnect.Backoff

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.reconnect:
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}

			// Close existing Control Plane connection
			if c.controlPlaneConn != nil {
				c.controlPlaneConn.Close()
				c.controlPlaneConn = nil
				c.controlPlaneConnected = false
			}

			// Close existing Runner connection
			if c.runnerConn != nil {
				c.runnerConn.Close()
				c.runnerConn = nil
				c.runnerConnected = false
			}
			c.mu.Unlock()

			attempts++
			if maxAttempts > 0 && attempts > maxAttempts {
				c.logger.Error("Maximum reconnection attempts exceeded")
				return
			}

			c.logger.WithField("attempt", attempts).Info("Attempting to reconnect")

			// Wait before reconnecting
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(interval):
			}

			// Attempt reconnection
			if err := c.Connect(); err != nil {
				c.logger.WithError(err).Error("Reconnection failed")

				// Increase interval with backoff
				interval += backoff

				// Trigger another reconnection attempt
				select {
				case c.reconnect <- struct{}{}:
				default:
				}
			} else {
				// Reset attempts and interval on successful reconnection
				attempts = 0
				interval = c.config.Reconnect.Interval
			}
		}
	}
}

// shouldSendResponse determines if a message type expects a response
func (c *Client) shouldSendResponse(msgType types.MessageType) bool {
	switch msgType {
	case types.MessageTypeDeploy, types.MessageTypeDelete, types.MessageTypeScale,
		types.MessageTypeGetResources, types.MessageTypeExec, types.MessageTypeCommand:
		return true
	default:
		return false
	}
}

// getTLSConfig creates a TLS configuration based on client config
func (c *Client) getTLSConfig() *tls.Config {
	config := &tls.Config{
		InsecureSkipVerify: c.config.TLS.InsecureSkipVerify,
	}

	// Load client certificates if specified
	if c.config.TLS.CertFile != "" && c.config.TLS.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.config.TLS.CertFile, c.config.TLS.KeyFile)
		if err != nil {
			c.logger.WithError(err).Error("Failed to load client certificate")
		} else {
			config.Certificates = []tls.Certificate{cert}
		}
	}

	// Load CA certificate if specified
	if c.config.TLS.CAFile != "" {
		caCert, err := os.ReadFile(c.config.TLS.CAFile)
		if err != nil {
			c.logger.WithError(err).Error("Failed to read CA certificate file")
		} else {
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				c.logger.Error("Failed to parse CA certificate")
			} else {
				config.RootCAs = caCertPool
				c.logger.Info("Successfully loaded CA certificate")
			}
		}
	}

	return config
}

// generateMessageID generates a unique message ID
func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// IsConnected returns true if the client has at least one active connection
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && (c.controlPlaneConnected || c.runnerConnected)
}

// IsControlPlaneConnected returns true if the Control Plane connection is active
func (c *Client) IsControlPlaneConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.controlPlaneConnected && c.controlPlaneConn != nil
}

// IsRunnerConnected returns true if the Runner connection is active
func (c *Client) IsRunnerConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.runnerConnected && c.runnerConn != nil
}

// EnableDualConnections enables dual connection mode after Control Plane assigns Runner
func (c *Client) EnableDualConnections(runnerEndpoint, runnerToken string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("client is closed")
	}

	c.runnerEndpoint = runnerEndpoint
	c.runnerToken = runnerToken
	c.dualMode = true
	c.runnerEnabled = true

	c.logger.WithField("runner_endpoint", runnerEndpoint).Info("Dual connections enabled")

	// Connect to Runner in background
	go c.connectToRunner()

	return nil
}

// connectToRunner establishes connection to the Runner
func (c *Client) connectToRunner() {
	if c.runnerEndpoint == "" || c.runnerToken == "" {
		c.logger.Error("Runner endpoint or token not set")
		return
	}

	c.logger.WithField("endpoint", c.runnerEndpoint).Info("Connecting to Runner")

	// Parse URL
	u, err := url.Parse(c.runnerEndpoint)
	if err != nil {
		c.logger.WithError(err).Error("Invalid Runner endpoint URL")
		return
	}

	// Convert HTTP(S) to WebSocket
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		c.logger.WithField("scheme", u.Scheme).Error("Unsupported Runner URL scheme")
		return
	}

	// Add WebSocket endpoint for Runner
	u.Path = "/api/v1/runner/ws"

	// Set up headers with Runner token
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.runnerToken)
	headers.Set("User-Agent", "PipeOps-Agent/1.0")

	// Set up dialer with timeout
	dialer := websocket.Dialer{
		HandshakeTimeout: c.config.Timeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
	}

	// Configure TLS if needed
	if c.config.TLS.Enabled {
		dialer.TLSClientConfig = c.getTLSConfig()
	}

	// Establish connection
	conn, _, err := dialer.Dial(u.String(), headers)
	if err != nil {
		c.logger.WithError(err).Error("Failed to connect to Runner")
		return
	}

	c.mu.Lock()
	c.runnerConn = conn
	c.runnerConnected = true
	c.mu.Unlock()

	c.logger.Info("Successfully connected to Runner")

	// Start Runner message reading goroutine
	go c.readRunnerMessages()
}
