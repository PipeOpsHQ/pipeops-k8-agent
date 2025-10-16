package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
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

// Client represents the control plane API client (supports both HTTP and WebSocket)
type Client struct {
	apiURL     string
	token      string
	httpClient *http.Client
	wsClient   *WebSocketClient
	agentID    string
	logger     *logrus.Logger
	useWebSocket bool
}

// NewClient creates a new control plane client
// By default, it will try to use WebSocket, falling back to HTTP if needed
func NewClient(apiURL, token, agentID string, logger *logrus.Logger) (*Client, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("authentication token is required")
	}

	// Create HTTP client with timeout and TLS configuration
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	client := &Client{
		apiURL:     apiURL,
		token:      token,
		httpClient: httpClient,
		agentID:    agentID,
		logger:     logger,
		useWebSocket: true, // Default to WebSocket
	}

	// Check if we should disable WebSocket (for backward compatibility)
	if os.Getenv("PIPEOPS_DISABLE_WEBSOCKET") == "true" {
		client.useWebSocket = false
		logger.Info("WebSocket disabled via PIPEOPS_DISABLE_WEBSOCKET environment variable")
	} else {
		// Try to create WebSocket client
		wsClient, err := NewWebSocketClient(apiURL, token, agentID, logger)
		if err != nil {
			logger.WithError(err).Warn("Failed to create WebSocket client, falling back to HTTP")
			client.useWebSocket = false
		} else {
			client.wsClient = wsClient
			
			// Try to connect
			if err := wsClient.Connect(); err != nil {
				logger.WithError(err).Warn("Failed to connect via WebSocket, falling back to HTTP")
				client.useWebSocket = false
			} else {
				logger.Info("✓ Using WebSocket for control plane communication")
			}
		}
	}

	if !client.useWebSocket {
		logger.Info("Using HTTP for control plane communication")
	}

	return client, nil
}

// RegisterAgent registers the agent with the control plane and returns registration result
func (c *Client) RegisterAgent(ctx context.Context, agent *types.Agent) (*RegistrationResult, error) {
	// Try WebSocket first if available
	if c.useWebSocket && c.wsClient != nil {
		c.logger.Debug("Attempting registration via WebSocket")
		result, err := c.wsClient.RegisterAgent(ctx, agent)
		if err == nil {
			return result, nil
		}
		c.logger.WithError(err).Warn("WebSocket registration failed, falling back to HTTP")
		// Fall through to HTTP
	}

	// Use HTTP registration
	return c.registerAgentHTTP(ctx, agent)
}

// registerAgentHTTP registers the agent via HTTP (fallback method)
func (c *Client) registerAgentHTTP(ctx context.Context, agent *types.Agent) (*RegistrationResult, error) {
	// Endpoint format: /api/v1/clusters/agent/{agent_id}
	endpoint := fmt.Sprintf("%s/api/v1/clusters/agent/register", c.apiURL)

	payload, err := json.Marshal(agent)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal agent data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	req.Header.Set("User-Agent", fmt.Sprintf("PipeOps-Agent/%s", agent.Version))

	c.logger.WithFields(logrus.Fields{
		"endpoint":     endpoint,
		"agent_id":     agent.ID,
		"cluster_name": agent.Name,
	}).Debug("Registering agent with control plane")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send registration request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle conflict (409) - cluster might already exist
	if resp.StatusCode == http.StatusConflict {
		c.logger.Warn("Cluster already exists (409), attempting to parse existing cluster info")
		// Try to parse the response anyway, it might contain cluster info
		var registerResp RegisterResponse
		if err := json.Unmarshal(body, &registerResp); err == nil {
			// Extract cluster UUID (same logic as successful registration)
			clusterUUID := ""
			if registerResp.ClusterID != "" {
				clusterUUID = registerResp.ClusterID
			}

			if clusterUUID != "" {
				c.logger.WithFields(logrus.Fields{
					"cluster_id": clusterUUID,
					"name":       registerResp.Cluster.Name,
				}).Info("Using existing cluster")

				// Extract other details
				tunnelURL := registerResp.TunnelURL
				apiServer := registerResp.APIServer
				if apiServer == "" {
					apiServer = tunnelURL
				}
				if apiServer == "" && registerResp.Cluster.APIServer != "" {
					apiServer = registerResp.Cluster.APIServer
				}

				return &RegistrationResult{
					ClusterID:   clusterUUID,
					ClusterUUID: registerResp.ClusterUUID,
					Name:        registerResp.Cluster.Name,
					Status:      registerResp.Status,
					TunnelURL:   tunnelURL,
					APIServer:   apiServer,
					Token:       registerResp.Cluster.Token,
					WorkspaceID: registerResp.Cluster.WorkspaceID,
				}, nil
			}
		}
		// If we can't parse cluster info, return error
		return nil, fmt.Errorf("cluster already exists: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("registration failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to get cluster ID and token
	// This MUST succeed - we cannot operate without a valid cluster ID from control plane
	var registerResp RegisterResponse
	if err := json.Unmarshal(body, &registerResp); err != nil {
		c.logger.WithFields(logrus.Fields{
			"error":    err.Error(),
			"response": string(body),
		}).Error("Failed to parse registration response - control plane returned invalid JSON")
		return nil, fmt.Errorf("failed to parse registration response (invalid JSON from control plane): %w", err)
	}

	// Extract cluster UUID from multiple possible locations (control plane may vary response structure)
	// This MUST be present - we cannot operate without a valid cluster UUID from control plane
	clusterUUID := ""
	if registerResp.ClusterID != "" {
		clusterUUID = registerResp.ClusterID // Top-level cluster_id (preferred)
	}

	// Cluster UUID is mandatory - fail if not provided
	if clusterUUID == "" {
		c.logger.WithFields(logrus.Fields{
			"response": string(body),
		}).Error("No cluster UUID found in registration response - control plane must provide cluster_id")
		return nil, fmt.Errorf("no cluster UUID found in registration response - control plane did not provide cluster_id")
	}

	// Extract API server URL (prefer top-level, fallback to nested)
	apiServer := ""
	if registerResp.APIServer != "" {
		apiServer = registerResp.APIServer // Top-level api_server
	} else if registerResp.TunnelURL != "" {
		apiServer = registerResp.TunnelURL // Top-level tunnel_url
	}

	// Extract tunnel URL (prefer top-level, fallback to nested)
	tunnelURL := ""
	if registerResp.TunnelURL != "" {
		tunnelURL = registerResp.TunnelURL // Top-level tunnel_url
	}

	// Extract cluster name (prefer top-level, fallback to nested)
	clusterName := ""
	if registerResp.Name != "" {
		clusterName = registerResp.Name // Top-level name
	} else if registerResp.Cluster.Name != "" {
		clusterName = registerResp.Cluster.Name // Nested cluster.name
	}

	// Extract status (prefer top-level, fallback to nested)
	status := ""
	if registerResp.Status != "" {
		status = registerResp.Status // Top-level status
	} else if registerResp.Cluster.Status != "" {
		status = registerResp.Cluster.Status // Nested cluster.status
	}

	// Extract workspace ID (from nested cluster object)
	workspaceID := 0
	if registerResp.Cluster.WorkspaceID != 0 {
		workspaceID = registerResp.Cluster.WorkspaceID
	}

	logFields := logrus.Fields{
		"agent_id":     agent.ID,
		"cluster_id":   clusterUUID,
		"cluster_uuid": registerResp.ClusterUUID,
		"name":         clusterName,
		"status":       status,
		"workspace_id": workspaceID,
	}
	if tunnelURL != "" {
		logFields["tunnel_url"] = tunnelURL
	}
	if registerResp.Cluster.Token != "" {
		logFields["has_token"] = true
	}
	c.logger.WithFields(logFields).Info("Agent registered successfully with control plane")

	// Return complete registration result with all important details
	return &RegistrationResult{
		ClusterID:   clusterUUID,
		ClusterUUID: registerResp.ClusterUUID,
		Name:        clusterName,
		Status:      status,
		TunnelURL:   tunnelURL,
		APIServer:   apiServer,
		Token:       registerResp.Cluster.Token,
		WorkspaceID: workspaceID,
	}, nil
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error {
	// Try WebSocket first if available
	if c.useWebSocket && c.wsClient != nil {
		err := c.wsClient.SendHeartbeat(ctx, heartbeat)
		if err == nil {
			return nil
		}
		c.logger.WithError(err).Debug("WebSocket heartbeat failed, falling back to HTTP")
		// Fall through to HTTP
	}

	// Use HTTP heartbeat
	return c.sendHeartbeatHTTP(ctx, heartbeat)
}

// sendHeartbeatHTTP sends a heartbeat via HTTP (fallback method)
func (c *Client) sendHeartbeatHTTP(ctx context.Context, heartbeat *HeartbeatRequest) error {
	// Endpoint format: /api/v1/clusters/agent/{cluster_uuid}/heartbeat
	endpoint := fmt.Sprintf("%s/api/v1/clusters/agent/%s/heartbeat", c.apiURL, heartbeat.ClusterID)

	payload, err := json.Marshal(heartbeat)
	if err != nil {
		return fmt.Errorf("failed to marshal heartbeat data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	c.logger.WithFields(logrus.Fields{
		"endpoint":   endpoint,
		"cluster_id": heartbeat.ClusterID,
		"agent_id":   heartbeat.AgentID,
	}).Debug("Sending heartbeat to control plane")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat failed with status %d: %s", resp.StatusCode, string(body))
	}

	c.logger.WithFields(logrus.Fields{
		"cluster_id": heartbeat.ClusterID,
		"agent_id":   heartbeat.AgentID,
		"status":     heartbeat.Status,
	}).Debug("Heartbeat sent successfully")

	return nil
}

// Note: ReportStatus, FetchCommands, and SendCommandResult methods removed.
// With Portainer-style architecture, the control plane accesses K8s directly through
// the tunnel (port 6443), so the agent doesn't need to report cluster status or
// execute K8s commands. The agent only needs to register and send heartbeats.

// Ping checks connectivity to the control plane
func (c *Client) Ping(ctx context.Context) error {
	// Try WebSocket first if available
	if c.useWebSocket && c.wsClient != nil {
		return c.wsClient.Ping(ctx)
	}

	// Use HTTP ping
	endpoint := fmt.Sprintf("%s/api/v1/health", c.apiURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to ping control plane: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("control plane returned status %d", resp.StatusCode)
	}

	return nil
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
