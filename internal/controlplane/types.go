package controlplane

import "time"

// HeartbeatRequest represents a heartbeat request to the control plane
type HeartbeatRequest struct {
	AgentID     string                 `json:"agent_id"`
	ClusterName string                 `json:"cluster_name"`
	Status      string                 `json:"status"`
	ProxyStatus string                 `json:"proxy_status"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
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
