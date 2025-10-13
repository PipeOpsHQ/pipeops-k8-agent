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

// RegisterAgent registers the agent with the control plane
func (c *Client) RegisterAgent(ctx context.Context, agent *types.Agent) error {
	endpoint := fmt.Sprintf("%s/api/v1/agents/register", c.apiURL)

	payload, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("failed to marshal agent data: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	req.Header.Set("User-Agent", fmt.Sprintf("PipeOps-Agent/%s", agent.Version))

	c.logger.WithFields(logrus.Fields{
		"endpoint":     endpoint,
		"agent_id":     agent.ID,
		"cluster_name": agent.ClusterName,
	}).Debug("Registering agent with control plane")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send registration request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("registration failed with status %d: %s", resp.StatusCode, string(body))
	}

	c.logger.WithFields(logrus.Fields{
		"agent_id":     agent.ID,
		"cluster_name": agent.ClusterName,
	}).Info("Agent registered successfully with control plane")

	return nil
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context, heartbeat *HeartbeatRequest) error {
	endpoint := fmt.Sprintf("%s/api/v1/agents/%s/heartbeat", c.apiURL, c.agentID)

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
		"agent_id": c.agentID,
		"status":   heartbeat.Status,
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
