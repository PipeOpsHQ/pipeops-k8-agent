package controlplane

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// ControlPlaneClient defines the interface for control plane communication
type ControlPlaneClient interface {
	RegisterAgent(ctx context.Context, agent *types.Agent) (*RegistrationResult, error)
	SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error
	SendMessage(messageType string, payload map[string]interface{}) error
	Ping(ctx context.Context) error
	Close() error
}

// Client represents the control plane API client (WebSocket only)
type Client struct {
	apiURL     string
	token      string
	httpClient *http.Client // Kept for legacy HTTP methods (deprecated)
	wsClient   *WebSocketClient
	agentID    string
	logger     *logrus.Logger
	tlsConfig  *tls.Config // TLS config for reconnecting to gateway
	timeouts   *types.Timeouts

	// Stored handlers to re-apply on client recreation
	proxyHandler        func(*ProxyRequest, ProxyResponseWriter)
	onRegistrationError func(error)
	onReconnect         func()
	onWebSocketData     func(string, []byte)
}

func buildTLSConfig(cfg *types.TLSConfig, logger *logrus.Logger) (*tls.Config, error) {
	base := &tls.Config{MinVersion: tls.VersionTLS12}

	if cfg == nil || !cfg.Enabled {
		return base, nil
	}

	if cfg.InsecureSkipVerify {
		base.InsecureSkipVerify = true
		if logger != nil {
			logger.Warn("TLS certificate verification disabled for control plane connections")
		}
	}

	if cfg.CAFile != "" {
		caData, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read control plane CA file %q: %w", cfg.CAFile, err)
		}

		systemPool, err := x509.SystemCertPool()
		if err != nil {
			systemPool = x509.NewCertPool()
		}

		if ok := systemPool.AppendCertsFromPEM(caData); !ok {
			return nil, fmt.Errorf("failed to append CA certificate from %q", cfg.CAFile)
		}

		base.RootCAs = systemPool
	}

	if cfg.CertFile != "" || cfg.KeyFile != "" {
		if cfg.CertFile == "" || cfg.KeyFile == "" {
			return nil, fmt.Errorf("both cert_file and key_file must be provided for mutual TLS")
		}

		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate or key: %w", err)
		}

		base.Certificates = []tls.Certificate{cert}
	}

	return base, nil
}

// NewClient creates a new control plane client using WebSocket
func NewClient(apiURL, token, agentID string, timeouts *types.Timeouts, tlsSettings *types.TLSConfig, logger *logrus.Logger) (*Client, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
	}
	if timeouts == nil {
		timeouts = types.DefaultTimeouts()
	}

	tlsConfig, err := buildTLSConfig(tlsSettings, logger)
	if err != nil {
		return nil, err
	}

	transportTLS := tlsConfig.Clone()
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     transportTLS,
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Create WebSocket client
	wsClient, err := NewWebSocketClient(apiURL, token, agentID, timeouts, tlsConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create WebSocket client: %w", err)
	}

	// Connect to WebSocket
	if err := wsClient.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect via WebSocket: %w", err)
	}

	logger.Info("✓ Connected to control plane via WebSocket")

	return &Client{
		apiURL:     apiURL,
		token:      token,
		httpClient: httpClient,
		wsClient:   wsClient,
		agentID:    agentID,
		logger:     logger,
		tlsConfig:  tlsConfig,
		timeouts:   timeouts,
	}, nil
}

// RegisterAgent registers the agent with the control plane and returns registration result.
// If the control plane returns a gateway_ws_url, the client will reconnect to the gateway
// for heartbeat and proxy operations (new architecture with controller/gateway separation).
func (c *Client) RegisterAgent(ctx context.Context, agent *types.Agent) (*RegistrationResult, error) {
	// Ensure we are connected to the Controller (not Gateway) for registration
	if c.wsClient != nil && c.wsClient.IsGatewayMode() {
		c.logger.Info("Switching from Gateway back to Controller for re-registration")
		if err := c.wsClient.Close(); err != nil {
			c.logger.WithError(err).Warn("Error closing gateway connection")
		}
		c.wsClient = nil
	}

	// Re-initialize WebSocket client to Controller if needed
	if c.wsClient == nil {
		wsClient, err := NewWebSocketClient(c.apiURL, c.token, c.agentID, c.timeouts, c.tlsConfig, c.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create controller WebSocket client: %w", err)
		}

		// Re-apply handlers
		if c.onRegistrationError != nil {
			wsClient.SetOnRegistrationError(c.onRegistrationError)
		}
		if c.proxyHandler != nil {
			wsClient.SetStreamingProxyHandler(c.proxyHandler)
		}
		if c.onReconnect != nil {
			wsClient.SetOnReconnect(c.onReconnect)
		}
		if c.onWebSocketData != nil {
			wsClient.SetOnWebSocketData(c.onWebSocketData)
		}

		if err := wsClient.Connect(); err != nil {
			return nil, fmt.Errorf("failed to connect to controller: %w", err)
		}
		c.wsClient = wsClient
	}

	c.logger.Debug("Registering agent via WebSocket")
	result, err := c.wsClient.RegisterAgent(ctx, agent)
	if err != nil {
		return nil, err
	}

	// Check if control plane returned a gateway URL (new architecture)
	if result.GatewayWSURL != "" {
		c.logger.WithField("gateway_ws_url", result.GatewayWSURL).Info("Control plane returned gateway URL - reconnecting to gateway")

		// Close existing controller WebSocket connection
		if err := c.wsClient.Close(); err != nil {
			c.logger.WithError(err).Warn("Error closing controller WebSocket connection")
		}

		// Create new WebSocket client connected to gateway
		clusterUUID := result.ClusterID
		if result.ClusterUUID != "" {
			clusterUUID = result.ClusterUUID
		}

		gatewayClient, err := NewWebSocketClientWithGateway(result.GatewayWSURL, c.token, c.agentID, clusterUUID, c.wsClient.timeouts, c.tlsConfig, c.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create gateway WebSocket client: %w", err)
		}

		// Re-apply handlers to gateway client
		if c.onRegistrationError != nil {
			gatewayClient.SetOnRegistrationError(c.onRegistrationError)
		}
		if c.proxyHandler != nil {
			gatewayClient.SetStreamingProxyHandler(c.proxyHandler)
		}
		if c.onReconnect != nil {
			gatewayClient.SetOnReconnect(c.onReconnect)
		}
		if c.onWebSocketData != nil {
			gatewayClient.SetOnWebSocketData(c.onWebSocketData)
		}

		// Connect to gateway
		if err := gatewayClient.ConnectToGateway(); err != nil {
			return nil, fmt.Errorf("failed to connect to gateway: %w", err)
		}

		c.wsClient = gatewayClient
		c.logger.Info("✓ Reconnected to gateway for heartbeat and proxy operations")
	} else {
		c.logger.Warn("Control plane did NOT return gateway_ws_url - remaining connected to controller (this may cause disconnection if controller enforces gateway usage)")
	}

	return result, nil
}

// ResumeSession attempts to resume a session directly with the gateway using a cached URL.
// This skips the initial registration with the controller, reducing startup time.
func (c *Client) ResumeSession(ctx context.Context, gatewayURL string, clusterUUID string) error {
	if gatewayURL == "" {
		return fmt.Errorf("gateway URL is required")
	}
	if clusterUUID == "" {
		return fmt.Errorf("cluster UUID is required")
	}

	c.logger.WithFields(logrus.Fields{
		"gateway_url":  gatewayURL,
		"cluster_uuid": clusterUUID,
	}).Info("Resuming session with cached gateway URL")

	// Close existing client if any
	if c.wsClient != nil {
		c.wsClient.Close()
	}

	// Create new gateway client
	gatewayClient, err := NewWebSocketClientWithGateway(gatewayURL, c.token, c.agentID, clusterUUID, c.timeouts, c.tlsConfig, c.logger)
	if err != nil {
		return fmt.Errorf("failed to create gateway client: %w", err)
	}

	// Re-apply handlers
	if c.onRegistrationError != nil {
		gatewayClient.SetOnRegistrationError(c.onRegistrationError)
	}
	if c.proxyHandler != nil {
		gatewayClient.SetStreamingProxyHandler(c.proxyHandler)
	}
	if c.onReconnect != nil {
		gatewayClient.SetOnReconnect(c.onReconnect)
	}
	if c.onWebSocketData != nil {
		gatewayClient.SetOnWebSocketData(c.onWebSocketData)
	}

	// Connect to gateway
	if err := gatewayClient.ConnectToGateway(); err != nil {
		return fmt.Errorf("failed to connect to gateway: %w", err)
	}

	c.wsClient = gatewayClient
	c.logger.Info("✓ Successfully resumed session with gateway")
	return nil
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error {
	// Use WebSocket only
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}

	return c.wsClient.SendHeartbeat(ctx, heartbeat)
}

// SendMessage sends a generic message to the control plane
func (c *Client) SendMessage(messageType string, payload map[string]interface{}) error {
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}

	return c.wsClient.SendMessage(messageType, payload)
}

// SendProxyResponse sends the result of a proxy request back to the control plane
func (c *Client) SendProxyResponse(ctx context.Context, response *ProxyResponse) error {
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}
	return c.wsClient.SendProxyResponse(ctx, response)
}

// SendProxyError reports a proxy error to the control plane
func (c *Client) SendProxyError(ctx context.Context, proxyErr *ProxyError) error {
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}
	return c.wsClient.SendProxyError(ctx, proxyErr)
}

// SendWebSocketData sends WebSocket frame data to the control plane for zero-copy proxying
func (c *Client) SendWebSocketData(ctx context.Context, requestID string, messageType int, data []byte) error {
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}
	return c.wsClient.SendWebSocketData(ctx, requestID, messageType, data)
}

// Note: ReportStatus, FetchCommands, and SendCommandResult methods removed.
// With Portainer-style architecture, the control plane accesses K8s directly through
// the tunnel (port 6443), so the agent doesn't need to report cluster status or
// execute K8s commands. The agent only needs to register and send heartbeats.

// SendBackpressureSignal sends a backpressure signal to the control plane
func (c *Client) SendBackpressureSignal(ctx context.Context, requestID string, reason string) error {
	if c.wsClient == nil {
		return fmt.Errorf("websocket client not initialized")
	}
	return c.wsClient.SendBackpressureSignal(ctx, requestID, reason)
}

// Ping sends a ping to the control plane
func (c *Client) Ping(ctx context.Context) error {
	// Use WebSocket only
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}

	return c.wsClient.Ping(ctx)
}

// SetOnRegistrationError sets a callback for registration errors from the control plane
func (c *Client) SetOnRegistrationError(callback func(error)) {
	c.onRegistrationError = callback
	if c.wsClient != nil {
		c.wsClient.SetOnRegistrationError(callback)
	}
}

// SetProxyRequestHandler registers a handler for proxy requests
func (c *Client) SetProxyRequestHandler(handler func(*ProxyRequest, ProxyResponseWriter)) {
	c.proxyHandler = handler
	if c.wsClient != nil {
		c.wsClient.SetStreamingProxyHandler(handler)
	}
}

// SetOnReconnect registers a callback that is invoked after the WebSocket reconnects successfully.
func (c *Client) SetOnReconnect(callback func()) {
	c.onReconnect = callback
	if c.wsClient != nil {
		c.wsClient.SetOnReconnect(callback)
	}
}

// SetOnWebSocketData registers a callback for receiving WebSocket data from the control plane
// This is used for zero-copy proxying of application service WebSocket connections
func (c *Client) SetOnWebSocketData(callback func(requestID string, data []byte)) {
	c.onWebSocketData = callback
	if c.wsClient != nil {
		c.wsClient.SetOnWebSocketData(callback)
	}
}

// Close closes the client and cleans up resources
func (c *Client) Close() error {
	// Close WebSocket connection if active
	if c.wsClient != nil {
		if err := c.wsClient.Close(); err != nil {
			c.logger.WithError(err).Warn("Error closing WebSocket connection")
		}
	}

	// Close HTTP client
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}

	return nil
}

// IsConnected returns whether the WebSocket connection is currently established
func (c *Client) IsConnected() bool {
	if c.wsClient == nil {
		return false
	}
	return c.wsClient.IsConnected()
}

// IsGatewayMode returns whether the client is connected to a gateway
func (c *Client) IsGatewayMode() bool {
	if c.wsClient == nil {
		return false
	}
	return c.wsClient.IsGatewayMode()
}
