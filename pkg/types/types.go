package types

import (
	"time"
)

// Agent represents the main agent configuration
type Agent struct {
	ID          string            `json:"id" yaml:"id"`
	Name        string            `json:"name" yaml:"name"`
	ClusterName string            `json:"cluster_name" yaml:"cluster_name"`
	Version     string            `json:"version" yaml:"version"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Status      AgentStatus       `json:"status" yaml:"status"`
	LastSeen    time.Time         `json:"last_seen" yaml:"last_seen"`

	// Control Plane registration details
	ControlPlaneURL string `json:"control_plane_url" yaml:"control_plane_url"`
	Token           string `json:"token,omitempty" yaml:"token,omitempty"`

	// Runner communication details (populated by Control Plane)
	RunnerEndpoint string `json:"runner_endpoint,omitempty" yaml:"runner_endpoint,omitempty"`
	RunnerToken    string `json:"runner_token,omitempty" yaml:"runner_token,omitempty"`
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
	Agent      AgentConfig      `yaml:"agent"`
	PipeOps    PipeOpsConfig    `yaml:"pipeops"`
	Kubernetes KubernetesConfig `yaml:"kubernetes"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// AgentConfig represents agent-specific configuration
type AgentConfig struct {
	Name         string            `yaml:"name"`
	ID           string            `yaml:"id"`
	ClusterName  string            `yaml:"cluster_name"`
	Labels       map[string]string `yaml:"labels"`
	PollInterval time.Duration     `yaml:"poll_interval"`
	Port         int               `yaml:"port"`
	Debug        bool              `yaml:"debug"`
	Version      string            `yaml:"version"`
}

// PipeOpsConfig represents PipeOps control plane configuration
type PipeOpsConfig struct {
	APIURL    string          `yaml:"api_url"`
	Token     string          `yaml:"token"`
	TLS       TLSConfig       `yaml:"tls"`
	Timeout   time.Duration   `yaml:"timeout"`
	Reconnect ReconnectConfig `yaml:"reconnect"`
}

// TLSConfig represents TLS configuration
type TLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	CertFile           string `yaml:"cert_file"`
	KeyFile            string `yaml:"key_file"`
	CAFile             string `yaml:"ca_file"`
}

// ReconnectConfig represents reconnection configuration
type ReconnectConfig struct {
	Enabled     bool          `yaml:"enabled"`
	MaxAttempts int           `yaml:"max_attempts"`
	Interval    time.Duration `yaml:"interval"`
	Backoff     time.Duration `yaml:"backoff"`
}

// KubernetesConfig represents Kubernetes client configuration
type KubernetesConfig struct {
	Kubeconfig string `yaml:"kubeconfig"`
	InCluster  bool   `yaml:"in_cluster"`
	Namespace  string `yaml:"namespace"`
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Output string `yaml:"output"`
}

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
