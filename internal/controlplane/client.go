package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Client represents the control plane API client
type Client struct {
	apiURL     string
	token      string
	httpClient *http.Client
	agentID    string
	logger     *logrus.Logger
}

// NewClient creates a new control plane client
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

	return &Client{
		apiURL:     apiURL,
		token:      token,
		httpClient: httpClient,
		agentID:    agentID,
		logger:     logger,
	}, nil
}

// RegisterAgent registers the agent with the control plane and returns registration result
func (c *Client) RegisterAgent(ctx context.Context, agent *types.Agent) (*RegistrationResult, error) {
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

	// Log response for debugging
	c.logger.WithFields(logrus.Fields{
		"status_code": resp.StatusCode,
		"response":    string(body),
	}).Debug("Registration response received")

	// Handle conflict (409) - cluster might already exist
	if resp.StatusCode == http.StatusConflict {
		c.logger.Warn("Cluster already exists (409), attempting to parse existing cluster info")
		// Try to parse the response anyway, it might contain cluster info
		var registerResp RegisterResponse
		if err := json.Unmarshal(body, &registerResp); err == nil && registerResp.Cluster.ID != "" {
			c.logger.WithFields(logrus.Fields{
				"cluster_id": registerResp.Cluster.ID,
				"name":       registerResp.Cluster.Name,
			}).Info("Using existing cluster")
			return &RegistrationResult{
				ClusterID: registerResp.Cluster.ID,
				Token:     registerResp.Cluster.Token,
				APIServer: registerResp.Cluster.APIServer,
			}, nil
		}
		// If we can't parse cluster info, return error
		return nil, fmt.Errorf("cluster already exists: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("registration failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response to get cluster ID and token
	var registerResp RegisterResponse
	if err := json.Unmarshal(body, &registerResp); err != nil {
		c.logger.WithError(err).Warn("Failed to parse registration response, but registration succeeded")
		// Fallback result with agent ID as cluster ID
		return &RegistrationResult{
			ClusterID: agent.ID,
		}, nil
	}

	logFields := logrus.Fields{
		"agent_id":   agent.ID,
		"cluster_id": registerResp.Cluster.ID,
		"name":       registerResp.Cluster.Name,
	}
	if registerResp.Cluster.Token != "" {
		logFields["has_token"] = true
	}
	c.logger.WithFields(logFields).Info("Agent registered successfully with control plane")

	return &RegistrationResult{
		ClusterID: registerResp.Cluster.ID,
		Token:     registerResp.Cluster.Token,
		APIServer: registerResp.Cluster.APIServer,
	}, nil
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error {
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
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	return nil
}
