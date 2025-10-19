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
func NewClient(apiURL, token, agentID string, tlsSettings *types.TLSConfig, logger *logrus.Logger) (*Client, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
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
	wsClient, err := NewWebSocketClient(apiURL, token, agentID, tlsConfig, logger)
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
	}, nil
}

// RegisterAgent registers the agent with the control plane and returns registration result
func (c *Client) RegisterAgent(ctx context.Context, agent *types.Agent) (*RegistrationResult, error) {
	// Use WebSocket only
	if c.wsClient == nil {
		return nil, fmt.Errorf("WebSocket client not initialized")
	}

	c.logger.Debug("Registering agent via WebSocket")
	return c.wsClient.RegisterAgent(ctx, agent)
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error {
	// Use WebSocket only
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}

	return c.wsClient.SendHeartbeat(ctx, heartbeat)
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

// Note: ReportStatus, FetchCommands, and SendCommandResult methods removed.
// With Portainer-style architecture, the control plane accesses K8s directly through
// the tunnel (port 6443), so the agent doesn't need to report cluster status or
// execute K8s commands. The agent only needs to register and send heartbeats.

// Ping checks connectivity to the control plane
func (c *Client) Ping(ctx context.Context) error {
	// Use WebSocket only
	if c.wsClient == nil {
		return fmt.Errorf("WebSocket client not initialized")
	}

	return c.wsClient.Ping(ctx)
}

// SetOnRegistrationError sets a callback for registration errors from the control plane
func (c *Client) SetOnRegistrationError(callback func(error)) {
	if c.wsClient != nil {
		c.wsClient.SetOnRegistrationError(callback)
	}
}

// SetProxyRequestHandler registers a handler for proxy requests
func (c *Client) SetProxyRequestHandler(handler func(*ProxyRequest, ProxyResponseWriter)) {
	if c.wsClient != nil {
		c.wsClient.SetStreamingProxyHandler(handler)
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
