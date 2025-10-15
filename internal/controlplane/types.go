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
