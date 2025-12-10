package controlplane

import (
	"context"
	"io"
	"net/http"
	"time"
)

// RegisterResponse represents the response from cluster registration
type RegisterResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	ClusterID    string `json:"cluster_id"`    // Top-level cluster_id (UUID)
	ClusterUUID  string `json:"cluster_uuid"`  // Alternative field name
	Name         string `json:"name"`
	Status       string `json:"status"`
	TunnelURL    string `json:"tunnel_url"`
	APIServer    string `json:"api_server"`               // K8s API URL via tunnel
	GatewayWSURL string `json:"gateway_ws_url,omitempty"` // Gateway WebSocket URL for new architecture

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
	ClusterID    string `json:"cluster_id"`              // Cluster UUID (from cluster_id or cluster_uuid field)
	ClusterUUID  string `json:"cluster_uuid"`            // Alternative UUID field
	Name         string `json:"name"`                    // Cluster name
	Status       string `json:"status"`                  // Cluster status
	TunnelURL    string `json:"tunnel_url"`              // Tunnel URL for K8s API access
	APIServer    string `json:"api_server"`              // K8s API server URL (usually same as TunnelURL)
	Token        string `json:"token,omitempty"`         // ServiceAccount token (if provided by control plane)
	WorkspaceID  int    `json:"workspace_id"`            // Workspace ID
	GatewayWSURL string `json:"gateway_ws_url,omitempty"` // Gateway WebSocket URL for new architecture (e.g., wss://gateway.pipeops.io/ws)
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
	TunnelGrafanaPort    int `json:"tunnel_grafana_port,omitempty"`

	WorkerJoin *WorkerJoinInfo `json:"worker_join,omitempty"`
}

// WorkerJoinInfo describes how a user can join worker nodes to the cluster
// (e.g., provide tokens and commands for k3s agents).
type WorkerJoinInfo struct {
	Provider    string            `json:"provider"`
	JoinCommand string            `json:"join_command,omitempty"`
	Token       string            `json:"token,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	ServerIP    string            `json:"server_ip,omitempty"`
	ServerURL   string            `json:"server_url,omitempty"`
	Commands    []string          `json:"commands,omitempty"`
	GeneratedAt time.Time         `json:"generated_at,omitempty"`
}

// ProxyRequest represents a request to be proxied through the agent to a backend service
type ProxyRequest struct {
	RequestID         string              `json:"request_id"`
	Method            string              `json:"method"`
	Path              string              `json:"path"`
	Query             string              `json:"query,omitempty"`
	Headers           map[string][]string `json:"headers,omitempty"`
	Body              []byte              `json:"body,omitempty"`
	ClusterID         string              `json:"cluster_id,omitempty"`
	ClusterUUID       string              `json:"cluster_uuid,omitempty"`
	AgentID           string              `json:"agent_id,omitempty"`
	BodyEncoding      string              `json:"body_encoding,omitempty"`
	SupportsStreaming bool                `json:"supports_streaming,omitempty"` // Indicates if streaming response is supported
	Scheme            string              `json:"scheme,omitempty"`            // Original request scheme (http/https)
	Deadline          time.Time           `json:"deadline,omitempty"`
	Timeout           time.Duration       `json:"timeout,omitempty"`
	RateLimitBps      float64             `json:"rate_limit_bps,omitempty"`
	IsWebSocket       bool                `json:"is_websocket,omitempty"` // Indicates if this is a WebSocket upgrade request
	UseZeroCopy       bool                `json:"use_zero_copy,omitempty"` // Indicates if zero-copy TCP forwarding should be used
	HeadData          []byte              `json:"head_data,omitempty"` // Initial data for zero-copy proxying

	// Fields for routing to a specific service in the cluster (e.g., application services)
	ServiceName string `json:"service_name,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	ServicePort int32  `json:"service_port,omitempty"`

	// BodyStream for streaming large request bodies
	bodyStream io.ReadCloser
	cancelFunc context.CancelFunc // Used to cancel the context of the streaming body
}

func (r *ProxyRequest) SetBodyStream(stream io.ReadCloser) {
	r.bodyStream = stream
}

func (r *ProxyRequest) BodyStream() io.ReadCloser {
	return r.bodyStream
}

func (r *ProxyRequest) CloseBody() error {
	if r.bodyStream != nil {
		return r.bodyStream.Close()
	}
	return nil
}

// ProxyResponse represents a response from a proxied request
type ProxyResponse struct {
	RequestID string              `json:"request_id"`
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"json_headers,omitempty"` // Renamed to avoid conflict with `Header` method on http.Response
	Body      string              `json:"body,omitempty"`
	Encoding  string              `json:"encoding,omitempty"`
}

// ProxyError represents an error during a proxied request
type ProxyError struct {
	RequestID string `json:"request_id"`
	Error     string `json:"error"`
}

// ProxyResponseWriter is an interface for writing proxy responses back to the control plane.
// It supports streaming responses with headers and chunks.
type ProxyResponseWriter interface {
	// WriteHeader writes the HTTP status code and headers.
	WriteHeader(statusCode int, headers http.Header) error

	// WriteChunk writes a chunk of the response body.
	WriteChunk(chunk []byte) error

	// Close closes the response writer, optionally with an error.
	Close() error
	CloseWithError(err error) error

	// StreamChannel returns a channel for bidirectional data streaming (e.g. WebSocket)
	StreamChannel() <-chan []byte
}

// StreamingRequest represents a streaming request from the control plane
type StreamingRequest struct {
	RequestID string `json:"request_id"`
	Type      string `json:"type"` // e.g., "stdin", "resize"
	Data      []byte `json:"data,omitempty"`
	Rows      uint16 `json:"rows,omitempty"` // For "resize" type
	Cols      uint16 `json:"cols,omitempty"` // For "resize" type
}

// StreamingResponse represents a streaming response to the control plane
type StreamingResponse struct {
	RequestID string `json:"request_id"`
	Type      string `json:"type"` // e.g., "stdout", "stderr", "exit"
	Data      []byte `json:"data,omitempty"`
	ExitCode  uint8  `json:"exit_code,omitempty"` // For "exit" type
}

// WebSocketClientMessage represents a message exchanged with the WebSocket client.
// It is used to encapsulate different message types for sending/receiving.
type WebSocketClientMessage struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"request_id,omitempty"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

// BackpressureSignal represents a signal to the control plane that the agent is experiencing backpressure.
type BackpressureSignal struct {
	RequestID string `json:"request_id"`
	Reason    string `json:"reason,omitempty"`
}
