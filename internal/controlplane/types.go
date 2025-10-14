package controlplane

import "time"

// RegisterResponse represents the response from cluster registration
type RegisterResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Cluster struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
		Token     string `json:"token,omitempty"`      // Cluster admin ServiceAccount token
		APIServer string `json:"api_server,omitempty"` // Optional: K8s API server URL (via tunnel)
	} `json:"cluster"`
}

// RegistrationResult contains the result of agent registration
type RegistrationResult struct {
	ClusterID string // Cluster UUID
	Token     string // ServiceAccount token (if provided by control plane)
	APIServer string // K8s API server URL (if provided, usually via tunnel)
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
