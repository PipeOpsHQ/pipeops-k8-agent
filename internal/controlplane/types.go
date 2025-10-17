package controlplane

import "time"

// RegisterResponse represents the response from cluster registration
type RegisterResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	ClusterID   string `json:"cluster_id"`   // Top-level cluster_id (UUID)
	ClusterUUID string `json:"cluster_uuid"` // Alternative field name
	Name        string `json:"name"`
	Status      string `json:"status"`
	TunnelURL   string `json:"tunnel_url"`
	APIServer   string `json:"api_server"` // K8s API URL via tunnel

	// Nested cluster object for additional details
	Cluster struct {
		ID             string    `json:"id"`   // Cluster UUID
		UUID           string    `json:"uuid"` // Cluster UUID
		Name           string    `json:"name"`
		Status         string    `json:"status"`
		TunnelStatus   string    `json:"tunnel_status"`
		AgentID        string    `json:"agent_id"`
		AgentHostname  string    `json:"agent_hostname"`
		AgentVersion   string    `json:"agent_version"`
		CloudProvider  string    `json:"cloud_provider"`
		Region         string    `json:"region"`
		ClusterVersion string    `json:"cluster_version"`
		WorkspaceID    int       `json:"workspace_id"`
		RegisteredAt   time.Time `json:"registered_at"`
		Token          string    `json:"token,omitempty"`      // Cluster admin ServiceAccount token (optional)
		APIServer      string    `json:"api_server,omitempty"` // K8s API server URL (optional)
	} `json:"cluster"`

	// Backward compatibility data object
	Data struct {
		ClusterID    string    `json:"cluster_id"`
		Status       string    `json:"status"`
		TunnelURL    string    `json:"tunnel_url"`
		RegisteredAt time.Time `json:"registered_at"`
	} `json:"data"`
}

// RegistrationResult contains the result of agent registration
// Simplified structure matching control plane's flat response
type RegistrationResult struct {
	ClusterID   string `json:"cluster_id"`      // Cluster UUID (from cluster_id or cluster_uuid field)
	ClusterUUID string `json:"cluster_uuid"`    // Alternative UUID field
	Name        string `json:"name"`            // Cluster name
	Status      string `json:"status"`          // Cluster status
	TunnelURL   string `json:"tunnel_url"`      // Tunnel URL for K8s API access
	APIServer   string `json:"api_server"`      // K8s API server URL (usually same as TunnelURL)
	Token       string `json:"token,omitempty"` // ServiceAccount token (if provided by control plane)
	WorkspaceID int    `json:"workspace_id"`    // Workspace ID
}

// HeartbeatRequest represents a heartbeat request to the control plane
type HeartbeatRequest struct {
	ClusterID    string                 `json:"cluster_id"`
	AgentID      string                 `json:"agent_id"`
	Status       string                 `json:"status"`
	TunnelStatus string                 `json:"tunnel_status"`
	Timestamp    time.Time              `json:"timestamp"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`

	// Monitoring stack information
	PrometheusURL      string `json:"prometheus_url,omitempty"`
	PrometheusUsername string `json:"prometheus_username,omitempty"`
	PrometheusPassword string `json:"prometheus_password,omitempty"`
	PrometheusSSL      bool   `json:"prometheus_ssl,omitempty"`

	LokiURL      string `json:"loki_url,omitempty"`
	LokiUsername string `json:"loki_username,omitempty"`
	LokiPassword string `json:"loki_password,omitempty"`

	OpenCostBaseURL  string `json:"opencost_base_url,omitempty"`
	OpenCostUsername string `json:"opencost_username,omitempty"`
	OpenCostPassword string `json:"opencost_password,omitempty"`

	GrafanaURL      string `json:"grafana_url,omitempty"`
	GrafanaUsername string `json:"grafana_username,omitempty"`
	GrafanaPassword string `json:"grafana_password,omitempty"`

	// Tunnel ports for monitoring services
	TunnelPrometheusPort int `json:"tunnel_prometheus_port,omitempty"`
	TunnelLokiPort       int `json:"tunnel_loki_port,omitempty"`
	TunnelOpenCostPort   int `json:"tunnel_opencost_port,omitempty"`
	TunnelGrafanaPort    int `json:"tunnel_grafana_port,omitempty"`
}

// Command represents a command from the control plane
type Command struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Payload   map[string]interface{} `json:"payload"`
	CreatedAt time.Time              `json:"created_at"`
}

// CommandsResponse represents the response from fetching commands
type CommandsResponse struct {
	Commands []Command `json:"commands"`
}

// CommandResult represents the result of a command execution
type CommandResult struct {
	Success   bool                   `json:"success"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// ProxyRequest represents a proxy command sent from the control plane to the agent
type ProxyRequest struct {
	RequestID    string              `json:"request_id"`
	ClusterID    string              `json:"cluster_id"`
	ClusterUUID  string              `json:"cluster_uuid"`
	AgentID      string              `json:"agent_id"`
	Method       string              `json:"method"`
	Path         string              `json:"path"`
	Query        string              `json:"query"`
	Headers      map[string][]string `json:"headers"`
	Body         []byte              `json:"-"`
	BodyEncoding string              `json:"body_encoding"`
}

// ProxyResponse represents the response returned to the control plane after fulfilling a proxy request
type ProxyResponse struct {
	RequestID string              `json:"request_id"`
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"headers,omitempty"`
	Body      string              `json:"body,omitempty"`
	Encoding  string              `json:"encoding,omitempty"`
}

// ProxyError represents an error that occurred while processing a proxy request
type ProxyError struct {
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}
