package types

import (
	"time"
)

// Agent represents the main agent configuration and registration request
// Maps to control plane's RegisterClusterRequest
type Agent struct {
	// Required fields
	ID        string `json:"agent_id" yaml:"id"`                     // Agent ID (required by control plane)
	Name      string `json:"name" yaml:"name"`                       // Cluster name (required by control plane)
	ClusterID string `json:"cluster_id,omitempty" yaml:"cluster_id"` // Existing cluster ID when re-registering

	// K8s and server information
	Version       string            `json:"k8s_version,omitempty" yaml:"version"`           // K8s version
	ServerIP      string            `json:"server_ip,omitempty" yaml:"server_ip"`           // Server public IP
	ServerCode    string            `json:"server_code,omitempty" yaml:"server_code"`       // Server code if available
	Token         string            `json:"k8s_service_token,omitempty" yaml:"token"`       // K8s ServiceAccount token for control plane access
	Region        string            `json:"region,omitempty" yaml:"region"`                 // Region (defaults to "agent-managed")
	CloudProvider string            `json:"cloud_provider,omitempty" yaml:"cloud_provider"` // Cloud provider (defaults to "agent")
	Metadata      map[string]string `json:"metadata,omitempty" yaml:"metadata"`             // Simple key-value metadata

	// Agent details
	Hostname         string            `json:"hostname,omitempty" yaml:"hostname"`
	AgentVersion     string            `json:"agent_version,omitempty" yaml:"agent_version"`
	Labels           map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	TunnelPortConfig TunnelPortConfig  `json:"tunnel_port_config,omitempty" yaml:"tunnel_port_config"`
	ServerSpecs      ServerSpecs       `json:"server_specs,omitempty" yaml:"server_specs"`

	// Monitoring stack configuration (sent during registration)
	PrometheusURL        string `json:"prometheus_url,omitempty" yaml:"prometheus_url"`
	PrometheusUsername   string `json:"prometheus_username,omitempty" yaml:"prometheus_username"`
	PrometheusPassword   string `json:"prometheus_password,omitempty" yaml:"prometheus_password"`
	PrometheusSSL        bool   `json:"prometheus_ssl,omitempty" yaml:"prometheus_ssl"`
	TunnelPrometheusPort int    `json:"tunnel_prometheus_port,omitempty" yaml:"tunnel_prometheus_port"` // Chisel tunnel port (e.g., 19090)

	LokiURL        string `json:"loki_url,omitempty" yaml:"loki_url"`
	LokiUsername   string `json:"loki_username,omitempty" yaml:"loki_username"`
	LokiPassword   string `json:"loki_password,omitempty" yaml:"loki_password"`
	LokiSSL        bool   `json:"loki_ssl,omitempty" yaml:"loki_ssl"`
	TunnelLokiPort int    `json:"tunnel_loki_port,omitempty" yaml:"tunnel_loki_port"` // Chisel tunnel port (e.g., 13100)

	OpenCostURL        string `json:"opencost_url,omitempty" yaml:"opencost_url"`
	OpenCostUsername   string `json:"opencost_username,omitempty" yaml:"opencost_username"`
	OpenCostPassword   string `json:"opencost_password,omitempty" yaml:"opencost_password"`
	OpenCostSSL        bool   `json:"opencost_ssl,omitempty" yaml:"opencost_ssl"`
	TunnelOpenCostPort int    `json:"tunnel_opencost_port,omitempty" yaml:"tunnel_opencost_port"` // Chisel tunnel port (e.g., 19003)

	GrafanaURL        string `json:"grafana_url,omitempty" yaml:"grafana_url"`
	GrafanaUsername   string `json:"grafana_username,omitempty" yaml:"grafana_username"`
	GrafanaPassword   string `json:"grafana_password,omitempty" yaml:"grafana_password"`
	GrafanaSSL        bool   `json:"grafana_ssl,omitempty" yaml:"grafana_ssl"`
	TunnelGrafanaPort int    `json:"tunnel_grafana_port,omitempty" yaml:"tunnel_grafana_port"` // Chisel tunnel port (e.g., 13000)

	// BYOC (Bring Your Own Cluster) fields - encrypted by control plane
	ClusterURL      string `json:"cluster_url,omitempty" yaml:"cluster_url"`             // K8s API URL (optional, will be encrypted)
	ClusterCertData string `json:"cluster_cert_data,omitempty" yaml:"cluster_cert_data"` // K8s CA certificate (optional, will be encrypted)

	// Deprecated/Internal fields
	ClusterName string      `json:"cluster_name,omitempty" yaml:"cluster_name"` // Deprecated, use Name
	Status      AgentStatus `json:"status,omitempty" yaml:"status"`
	LastSeen    time.Time   `json:"last_seen,omitempty" yaml:"last_seen"`

	// Control Plane configuration (not sent in registration)
	ControlPlaneURL string `json:"-" yaml:"control_plane_url"`
}

// TunnelPortConfig represents tunnel port configuration for registration
type TunnelPortConfig struct {
	KubernetesAPI int `json:"kubernetes_api,omitempty" yaml:"kubernetes_api"`
	Kubelet       int `json:"kubelet,omitempty" yaml:"kubelet"`
	AgentHTTP     int `json:"agent_http,omitempty" yaml:"agent_http"`
}

// ServerSpecs represents server hardware specifications
type ServerSpecs struct {
	CPUCores int    `json:"cpu_cores,omitempty" yaml:"cpu_cores"`
	MemoryGB int    `json:"memory_gb,omitempty" yaml:"memory_gb"`
	DiskGB   int    `json:"disk_gb,omitempty" yaml:"disk_gb"`
	OS       string `json:"os,omitempty" yaml:"os"`
}

// AgentStatus represents the current status of the agent
type AgentStatus string

const (
	AgentStatusConnected    AgentStatus = "connected"
	AgentStatusDisconnected AgentStatus = "disconnected"
	AgentStatusError        AgentStatus = "error"
	AgentStatusRegistering  AgentStatus = "registering"
)

// Message represents a message between agent and control plane
type Message struct {
	ID        string      `json:"id"`
	Type      MessageType `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// MessageType defines the type of message
type MessageType string

// MessageEndpoint defines the target endpoint for a message
type MessageEndpoint string

const (
	// Message Endpoints
	EndpointControlPlane MessageEndpoint = "control_plane"
	EndpointRunner       MessageEndpoint = "runner"
)

const (
	// Agent to Control Plane
	MessageTypeRegister  MessageType = "register"
	MessageTypeHeartbeat MessageType = "heartbeat"
	MessageTypeStatus    MessageType = "status"
	MessageTypeMetrics   MessageType = "metrics"
	MessageTypeResponse  MessageType = "response"

	// Control Plane to Agent
	MessageTypeAgentDiscovered MessageType = "agent_discovered" // Control Plane notifies agent of Runner details
	MessageTypeRunnerAssigned  MessageType = "runner_assigned"  // Control Plane assigns Runner to agent

	// Runner to Agent (after Control Plane notification)
	MessageTypeDeploy       MessageType = "deploy"
	MessageTypeDelete       MessageType = "delete"
	MessageTypeScale        MessageType = "scale"
	MessageTypeGetResources MessageType = "get_resources"
	MessageTypeExec         MessageType = "exec"
	MessageTypeCommand      MessageType = "command"
)

// ControlPlaneRegistration represents agent registration with Control Plane
type ControlPlaneRegistration struct {
	Agent       Agent               `json:"agent"`
	Cluster     ClusterInfo         `json:"cluster"`
	Credentials ClusterCredentials  `json:"credentials"`
	Monitoring  MonitoringEndpoints `json:"monitoring,omitempty"`
}

// ClusterInfo represents cluster information for registration
type ClusterInfo struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"` // "k3s", "eks", "gke", etc.
	Region      string            `json:"region"`
	Version     string            `json:"version"`
	NodeCount   int               `json:"node_count"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ClusterCredentials represents cluster access credentials
type ClusterCredentials struct {
	APIServer string `json:"api_server"`          // Kubernetes API server URL
	CACert    string `json:"ca_cert"`             // Base64 encoded CA certificate
	Token     string `json:"token"`               // Service account token
	ProxyURL  string `json:"proxy_url,omitempty"` // Optional proxy URL
}

// MonitoringEndpoints represents monitoring stack endpoints
type MonitoringEndpoints struct {
	Prometheus PrometheusConfig `json:"prometheus,omitempty"`
	Loki       LokiConfig       `json:"loki,omitempty"`
	OpenCost   OpenCostConfig   `json:"opencost,omitempty"`
}

// PrometheusConfig represents Prometheus configuration
type PrometheusConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// LokiConfig represents Loki configuration
type LokiConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// OpenCostConfig represents OpenCost configuration
type OpenCostConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// RunnerAssignment represents Runner assignment from Control Plane
type RunnerAssignment struct {
	RunnerID       string    `json:"runner_id"`
	RunnerEndpoint string    `json:"runner_endpoint"`
	RunnerToken    string    `json:"runner_token"`
	AssignedAt     time.Time `json:"assigned_at"`
}

// AgentDiscoveryNotification represents notification sent to Runner via RabbitMQ
type AgentDiscoveryNotification struct {
	AgentID      string              `json:"agent_id"`
	ClusterName  string              `json:"cluster_name"`
	ClusterInfo  ClusterInfo         `json:"cluster_info"`
	Credentials  ClusterCredentials  `json:"credentials"`
	Monitoring   MonitoringEndpoints `json:"monitoring,omitempty"`
	Capabilities []string            `json:"capabilities"` // List of supported operations
	RegisteredAt time.Time           `json:"registered_at"`
}

// DeploymentRequest represents a deployment request from control plane
type DeploymentRequest struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Image       string            `json:"image"`
	Replicas    int32             `json:"replicas"`
	Ports       []Port            `json:"ports,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Resources   Resources         `json:"resources,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Port represents a container port
type Port struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int32  `json:"container_port"`
	Protocol      string `json:"protocol,omitempty"`
}

// Resources represents resource requirements
type Resources struct {
	Limits   ResourceList `json:"limits,omitempty"`
	Requests ResourceList `json:"requests,omitempty"`
}

// ResourceList represents resource quantities
type ResourceList struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// ClusterStatus represents the overall cluster status
type ClusterStatus struct {
	Nodes       []NodeStatus   `json:"nodes"`
	Namespaces  []string       `json:"namespaces"`
	Deployments []ResourceInfo `json:"deployments"`
	Services    []ResourceInfo `json:"services"`
	Pods        []PodInfo      `json:"pods"`
	Metrics     ClusterMetrics `json:"metrics"`
	Timestamp   time.Time      `json:"timestamp"`
}

// NodeStatus represents a node's status
type NodeStatus struct {
	Name      string            `json:"name"`
	Ready     bool              `json:"ready"`
	Version   string            `json:"version"`
	OS        string            `json:"os"`
	Arch      string            `json:"arch"`
	Labels    map[string]string `json:"labels,omitempty"`
	Resources NodeResources     `json:"resources"`
}

// NodeResources represents node resource information
type NodeResources struct {
	Allocatable ResourceList `json:"allocatable"`
	Capacity    ResourceList `json:"capacity"`
}

// ResourceInfo represents basic resource information
type ResourceInfo struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	Ready     bool              `json:"ready"`
	CreatedAt time.Time         `json:"created_at"`
}

// PodInfo represents pod information
type PodInfo struct {
	ResourceInfo
	Phase      string `json:"phase"`
	NodeName   string `json:"node_name"`
	Containers int    `json:"containers"`
	Restarts   int32  `json:"restarts"`
}

// ClusterMetrics represents cluster-wide metrics
type ClusterMetrics struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage float64 `json:"memory_usage"`
	PodCount    int     `json:"pod_count"`
	NodeCount   int     `json:"node_count"`
}

// Config represents agent configuration
type Config struct {
	Agent   AgentConfig   `yaml:"agent" mapstructure:"agent"`
	PipeOps PipeOpsConfig `yaml:"pipeops" mapstructure:"pipeops"`
	// FRP config removed - agent now uses custom real-time architecture
	Kubernetes KubernetesConfig `yaml:"kubernetes" mapstructure:"kubernetes"`
	Logging    LoggingConfig    `yaml:"logging" mapstructure:"logging"`
	Tunnel     *TunnelConfig    `yaml:"tunnel,omitempty" mapstructure:"tunnel"`
}

// AgentConfig represents agent-specific configuration
type AgentConfig struct {
	Name           string            `yaml:"name" mapstructure:"name"`
	ID             string            `yaml:"id" mapstructure:"id"`
	ClusterName    string            `yaml:"cluster_name" mapstructure:"cluster_name"`
	Labels         map[string]string `yaml:"labels" mapstructure:"labels"`
	Port           int               `yaml:"port" mapstructure:"port"`
	Debug          bool              `yaml:"debug" mapstructure:"debug"`
	GrafanaSubPath bool              `yaml:"grafana_sub_path" mapstructure:"grafana_sub_path"`
	Version        string            `yaml:"version" mapstructure:"version"`
}

// PipeOpsConfig represents PipeOps control plane configuration
type PipeOpsConfig struct {
	APIURL    string          `yaml:"api_url" mapstructure:"api_url"`
	Token     string          `yaml:"token" mapstructure:"token"`
	TLS       TLSConfig       `yaml:"tls" mapstructure:"tls"`
	Timeout   time.Duration   `yaml:"timeout" mapstructure:"timeout"`
	Reconnect ReconnectConfig `yaml:"reconnect" mapstructure:"reconnect"`
}

// TLSConfig represents TLS configuration
type TLSConfig struct {
	Enabled            bool   `yaml:"enabled" mapstructure:"enabled"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" mapstructure:"insecure_skip_verify"`
	CertFile           string `yaml:"cert_file" mapstructure:"cert_file"`
	KeyFile            string `yaml:"key_file" mapstructure:"key_file"`
	CAFile             string `yaml:"ca_file" mapstructure:"ca_file"`
}

// ReconnectConfig represents reconnection configuration
type ReconnectConfig struct {
	Enabled     bool          `yaml:"enabled" mapstructure:"enabled"`
	MaxAttempts int           `yaml:"max_attempts" mapstructure:"max_attempts"`
	Interval    time.Duration `yaml:"interval" mapstructure:"interval"`
	Backoff     time.Duration `yaml:"backoff" mapstructure:"backoff"`
}

// KubernetesConfig represents Kubernetes client configuration
type KubernetesConfig struct {
	Kubeconfig string `yaml:"kubeconfig" mapstructure:"kubeconfig"`
	InCluster  bool   `yaml:"in_cluster" mapstructure:"in_cluster"`
	Namespace  string `yaml:"namespace" mapstructure:"namespace"`
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" mapstructure:"level"`
	Format string `yaml:"format" mapstructure:"format"`
	Output string `yaml:"output" mapstructure:"output"`
}

// TunnelConfig represents tunnel configuration for reverse proxy access
// Note: Tunnel control is now handled via WebSocket push notifications instead of polling
type TunnelConfig struct {
	Enabled           bool            `yaml:"enabled" mapstructure:"enabled"`
	InactivityTimeout time.Duration   `yaml:"inactivity_timeout" mapstructure:"inactivity_timeout"`
	Forwards          []TunnelForward `yaml:"forwards" mapstructure:"forwards"`
}

// TunnelForward represents a single port forward through the tunnel
type TunnelForward struct {
	Name       string `yaml:"name" mapstructure:"name"`               // Friendly name (e.g., "kubernetes-api")
	LocalAddr  string `yaml:"local_addr" mapstructure:"local_addr"`   // Local address to forward (e.g., "localhost:6443")
	RemotePort int    `yaml:"remote_port" mapstructure:"remote_port"` // Remote port on tunnel server (dynamically assigned by control plane)
}

// FRP-related types removed - agent now uses custom real-time architecture

// CommandRequest represents a command execution request
type CommandRequest struct {
	ID      string            `json:"id"`
	Type    CommandType       `json:"type"`
	Target  CommandTarget     `json:"target"`
	Command []string          `json:"command"`
	Args    map[string]string `json:"args,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

// CommandType defines the type of command
type CommandType string

const (
	CommandTypeExec   CommandType = "exec"
	CommandTypeLogs   CommandType = "logs"
	CommandTypeShell  CommandType = "shell"
	CommandTypeApply  CommandType = "apply"
	CommandTypeDelete CommandType = "delete"
)

// CommandTarget represents the target for command execution
type CommandTarget struct {
	Type      string `json:"type"` // pod, deployment, service, etc.
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Container string `json:"container,omitempty"`
}

// CommandResponse represents the response to a command
type CommandResponse struct {
	ID       string        `json:"id"`
	Success  bool          `json:"success"`
	Output   string        `json:"output,omitempty"`
	Error    string        `json:"error,omitempty"`
	ExitCode int           `json:"exit_code,omitempty"`
	Duration time.Duration `json:"duration"`
}

// MonitoringConfig represents the monitoring stack configuration
type MonitoringConfig struct {
	Enabled   bool                  `yaml:"enabled" mapstructure:"enabled"`
	Namespace string                `yaml:"namespace" mapstructure:"namespace"` // Default: "pipeops-monitoring"
	Stack     MonitoringStackConfig `yaml:"stack" mapstructure:"stack"`
}

// MonitoringStackConfig represents individual monitoring service configurations
type MonitoringStackConfig struct {
	Prometheus MonitoringServiceConfig `yaml:"prometheus" mapstructure:"prometheus"`
	Loki       MonitoringServiceConfig `yaml:"loki" mapstructure:"loki"`
	OpenCost   MonitoringServiceConfig `yaml:"opencost" mapstructure:"opencost"`
	Grafana    MonitoringServiceConfig `yaml:"grafana" mapstructure:"grafana"`
}

// MonitoringServiceConfig represents configuration for a single monitoring service
type MonitoringServiceConfig struct {
	Enabled        bool                   `yaml:"enabled" mapstructure:"enabled"`
	ChartRepo      string                 `yaml:"chart_repo" mapstructure:"chart_repo"`
	ChartName      string                 `yaml:"chart_name" mapstructure:"chart_name"`
	ChartVersion   string                 `yaml:"chart_version" mapstructure:"chart_version"`
	ReleaseName    string                 `yaml:"release_name" mapstructure:"release_name"`
	ServiceName    string                 `yaml:"service_name" mapstructure:"service_name"`       // K8s service name
	ServicePort    int                    `yaml:"service_port" mapstructure:"service_port"`       // K8s service port
	TunnelPort     int                    `yaml:"tunnel_port" mapstructure:"tunnel_port"`         // Chisel tunnel port
	Username       string                 `yaml:"username" mapstructure:"username"`               // Basic auth username
	Password       string                 `yaml:"password" mapstructure:"password"`               // Basic auth password
	SSL            bool                   `yaml:"ssl" mapstructure:"ssl"`                         // Enable SSL
	Values         map[string]interface{} `yaml:"values" mapstructure:"values"`                   // Helm chart values
	HealthEndpoint string                 `yaml:"health_endpoint" mapstructure:"health_endpoint"` // Health check path
}

// K8sProxyRequest represents a Kubernetes API request from the Runner
type K8sProxyRequest struct {
	ID       string              `json:"id"`
	Method   string              `json:"method"`
	Path     string              `json:"path"`
	Headers  map[string]string   `json:"headers,omitempty"`
	Body     []byte              `json:"body,omitempty"`
	Query    map[string][]string `json:"query,omitempty"`
	Stream   bool                `json:"stream,omitempty"`    // For WATCH operations
	StreamID string              `json:"stream_id,omitempty"` // For stream control
}

// K8sProxyResponse represents a response to a Kubernetes API request
type K8sProxyResponse struct {
	ID         string            `json:"id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	Error      string            `json:"error,omitempty"`
	Stream     bool              `json:"stream,omitempty"`
	StreamID   string            `json:"stream_id,omitempty"`
	Chunk      bool              `json:"chunk,omitempty"` // Indicates streaming chunk
	Done       bool              `json:"done,omitempty"`  // Indicates stream end
}
