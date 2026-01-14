package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// ControlPlaneClient is an interface for communicating with the control plane tunnel API
// Used for TCP/UDP tunnel registration via Gateway API
type ControlPlaneClient interface {
	// RegisterTunnel registers a single tunnel with the control plane
	RegisterTunnel(ctx context.Context, req *TunnelRegistration) (*TunnelRegistration, error)

	// SyncTunnels performs bulk sync of all tunnels
	SyncTunnels(ctx context.Context, req *TunnelSyncRequest) (*TunnelSyncResponse, error)

	// DeregisterTunnel removes a tunnel registration
	DeregisterTunnel(ctx context.Context, clusterUUID string, tunnelID string) error

	// ListTunnels retrieves all registered tunnels for a cluster
	ListTunnels(ctx context.Context, clusterUUID string) ([]TunnelRegistration, error)
}

// HTTPControlPlaneClient implements ControlPlaneClient using HTTP REST API
type HTTPControlPlaneClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewHTTPControlPlaneClient creates a new HTTP-based tunnel client
func NewHTTPControlPlaneClient(baseURL string, token string, logger *logrus.Logger) *HTTPControlPlaneClient {
	return &HTTPControlPlaneClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// RegisterTunnel registers a single tunnel with the control plane
func (c *HTTPControlPlaneClient) RegisterTunnel(ctx context.Context, req *TunnelRegistration) (*TunnelRegistration, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/agent/%s/tunnels", c.baseURL, req.ClusterUUID)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result TunnelRegistration
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// SyncTunnels performs bulk sync of all tunnels
func (c *HTTPControlPlaneClient) SyncTunnels(ctx context.Context, req *TunnelSyncRequest) (*TunnelSyncResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/agent/%s/tunnels/sync", c.baseURL, req.ClusterUUID)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result TunnelSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// DeregisterTunnel removes a tunnel registration
func (c *HTTPControlPlaneClient) DeregisterTunnel(ctx context.Context, clusterUUID string, tunnelID string) error {
	url := fmt.Sprintf("%s/api/v1/clusters/agent/%s/tunnels/%s", c.baseURL, clusterUUID, tunnelID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// ListTunnels retrieves all registered tunnels for a cluster
func (c *HTTPControlPlaneClient) ListTunnels(ctx context.Context, clusterUUID string) ([]TunnelRegistration, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/agent/%s/tunnels", c.baseURL, clusterUUID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result []TunnelRegistration
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// MockControlPlaneClient is a mock implementation for testing without a real control plane
type MockControlPlaneClient struct {
	logger      *logrus.Logger
	tunnels     map[string]*TunnelRegistration // tunnelID -> registration
	portCounter int                            // Simulates port allocation
}

// NewMockControlPlaneClient creates a new mock tunnel client for testing
func NewMockControlPlaneClient(logger *logrus.Logger) *MockControlPlaneClient {
	return &MockControlPlaneClient{
		logger:      logger,
		tunnels:     make(map[string]*TunnelRegistration),
		portCounter: 15000, // Start from TCP range
	}
}

// RegisterTunnel simulates tunnel registration with deterministic port allocation
func (m *MockControlPlaneClient) RegisterTunnel(ctx context.Context, req *TunnelRegistration) (*TunnelRegistration, error) {
	// Simulate port allocation
	m.portCounter++
	tunnelPort := m.portCounter

	// Build tunnel endpoint
	tunnelEndpoint := fmt.Sprintf("gateway.pipeops.local:%d", tunnelPort)

	// Create response
	result := &TunnelRegistration{
		ClusterUUID:      req.ClusterUUID,
		Protocol:         req.Protocol,
		TunnelID:         req.TunnelID,
		GatewayName:      req.GatewayName,
		GatewayNamespace: req.GatewayNamespace,
		GatewayPort:      req.GatewayPort,
		ServiceName:      req.ServiceName,
		ServiceNamespace: req.ServiceNamespace,
		ServicePort:      req.ServicePort,
		RoutingMode:      req.RoutingMode,
		PublicEndpoint:   req.PublicEndpoint,
		TunnelPort:       tunnelPort,
		TunnelEndpoint:   tunnelEndpoint,
		Labels:           req.Labels,
		Annotations:      req.Annotations,
	}

	// Store in cache
	m.tunnels[req.TunnelID] = result

	m.logger.WithFields(logrus.Fields{
		"tunnel_id":       result.TunnelID,
		"protocol":        result.Protocol,
		"tunnel_port":     result.TunnelPort,
		"tunnel_endpoint": result.TunnelEndpoint,
		"routing_mode":    result.RoutingMode,
	}).Info("Mock: Tunnel registered")

	return result, nil
}

// SyncTunnels simulates bulk sync
func (m *MockControlPlaneClient) SyncTunnels(ctx context.Context, req *TunnelSyncRequest) (*TunnelSyncResponse, error) {
	response := &TunnelSyncResponse{
		Accepted: []TunnelRegistration{},
		Rejected: []TunnelRejection{},
	}

	for _, tunnel := range req.Tunnels {
		registered, err := m.RegisterTunnel(ctx, &tunnel)
		if err != nil {
			response.Rejected = append(response.Rejected, TunnelRejection{
				TunnelID: tunnel.TunnelID,
				Reason:   err.Error(),
			})
		} else {
			response.Accepted = append(response.Accepted, *registered)
		}
	}

	m.logger.WithFields(logrus.Fields{
		"accepted": len(response.Accepted),
		"rejected": len(response.Rejected),
	}).Info("Mock: Tunnel sync completed")

	return response, nil
}

// DeregisterTunnel simulates tunnel deregistration
func (m *MockControlPlaneClient) DeregisterTunnel(ctx context.Context, clusterUUID string, tunnelID string) error {
	if _, exists := m.tunnels[tunnelID]; !exists {
		return fmt.Errorf("tunnel not found: %s", tunnelID)
	}

	delete(m.tunnels, tunnelID)

	m.logger.WithField("tunnel_id", tunnelID).Info("Mock: Tunnel deregistered")

	return nil
}

// ListTunnels simulates retrieving all tunnels
func (m *MockControlPlaneClient) ListTunnels(ctx context.Context, clusterUUID string) ([]TunnelRegistration, error) {
	result := make([]TunnelRegistration, 0, len(m.tunnels))

	for _, tunnel := range m.tunnels {
		if tunnel.ClusterUUID == clusterUUID {
			result = append(result, *tunnel)
		}
	}

	m.logger.WithField("count", len(result)).Info("Mock: Listed tunnels")

	return result, nil
}
