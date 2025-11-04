package ingress

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

// RouteClient defines the interface for route management operations
type RouteClient interface {
	RegisterRoute(ctx context.Context, req RegisterRouteRequest) error
	SyncIngresses(ctx context.Context, req SyncIngressesRequest) error
	UnregisterRoute(ctx context.Context, hostname string) error
}

// ControllerClient handles HTTP communication with the PipeOps controller
type ControllerClient struct {
	baseURL    string
	httpClient *http.Client
	agentToken string
	logger     *logrus.Logger
}

// NewControllerClient creates a new controller API client
func NewControllerClient(baseURL, agentToken string, logger *logrus.Logger) *ControllerClient {
	return &ControllerClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		agentToken: agentToken,
		logger:     logger,
	}
}

// RegisterRoute registers a single route with the controller
func (c *ControllerClient) RegisterRoute(ctx context.Context, req RegisterRouteRequest) error {
	url := fmt.Sprintf("%s/api/v1/gateway/routes/register", c.baseURL)

	c.logger.WithFields(logrus.Fields{
		"hostname":        req.Hostname,
		"cluster_uuid":    req.ClusterUUID,
		"routing_mode":    req.RoutingMode,
		"public_endpoint": req.PublicEndpoint,
	}).Debug("Registering route with controller")

	return c.doRequest(ctx, "POST", url, req)
}

// SyncIngresses syncs all ingresses at once (bulk operation)
func (c *ControllerClient) SyncIngresses(ctx context.Context, req SyncIngressesRequest) error {
	url := fmt.Sprintf("%s/api/v1/gateway/routes/sync", c.baseURL)

	c.logger.WithFields(logrus.Fields{
		"cluster_uuid":    req.ClusterUUID,
		"ingress_count":   len(req.Ingresses),
		"routing_mode":    req.RoutingMode,
		"public_endpoint": req.PublicEndpoint,
	}).Info("Syncing ingresses with controller")

	return c.doRequest(ctx, "POST", url, req)
}

// UnregisterRoute unregisters a route from the controller
func (c *ControllerClient) UnregisterRoute(ctx context.Context, hostname string) error {
	url := fmt.Sprintf("%s/api/v1/gateway/routes/unregister", c.baseURL)

	req := map[string]string{"hostname": hostname}

	c.logger.WithField("hostname", hostname).Debug("Unregistering route from controller")

	return c.doRequest(ctx, "POST", url, req)
}

// doRequest performs the HTTP request with proper error handling
func (c *ControllerClient) doRequest(ctx context.Context, method, url string, body interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.agentToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	c.logger.WithFields(logrus.Fields{
		"method": method,
		"url":    url,
		"status": resp.StatusCode,
	}).Debug("Controller API request successful")

	return nil
}

// Request types matching controller API

// RegisterRouteRequest is the request to register a single route
type RegisterRouteRequest struct {
	Hostname       string            `json:"hostname"`
	ClusterUUID    string            `json:"cluster_uuid"`
	Namespace      string            `json:"namespace"`
	ServiceName    string            `json:"service_name"`
	ServicePort    int32             `json:"service_port"`
	IngressName    string            `json:"ingress_name"`
	Path           string            `json:"path"`
	PathType       string            `json:"path_type"`
	TLS            bool              `json:"tls"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	PublicEndpoint string            `json:"public_endpoint,omitempty"` // For direct routing
	RoutingMode    string            `json:"routing_mode,omitempty"`    // "direct" or "tunnel"
}

// SyncIngressesRequest is the bulk sync request for all ingresses
type SyncIngressesRequest struct {
	ClusterUUID    string        `json:"cluster_uuid"`
	PublicEndpoint string        `json:"public_endpoint,omitempty"`
	RoutingMode    string        `json:"routing_mode,omitempty"`
	Ingresses      []IngressData `json:"ingresses"`
}

// IngressData represents a single ingress with all its rules
type IngressData struct {
	Namespace   string            `json:"namespace"`
	IngressName string            `json:"ingress_name"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Rules       []IngressRule     `json:"rules"`
}

// IngressRule represents a single ingress rule
type IngressRule struct {
	Host  string        `json:"host"`
	TLS   bool          `json:"tls"`
	Paths []IngressPath `json:"paths"`
}

// IngressPath represents a single path in an ingress rule
type IngressPath struct {
	Path        string `json:"path"`
	PathType    string `json:"path_type"`
	ServiceName string `json:"service_name"`
	ServicePort int32  `json:"service_port"`
}
