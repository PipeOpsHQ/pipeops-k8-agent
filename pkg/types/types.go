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
	Version        string            `json:"k8s_version,omitempty" yaml:"version"`             // K8s version
	ServerIP       string            `json:"server_ip,omitempty" yaml:"server_ip"`             // Server public IP
	ServerCode     string            `json:"server_code,omitempty" yaml:"server_code"`         // Server code if available
	Token          string            `json:"k8s_service_token,omitempty" yaml:"token"`         // K8s ServiceAccount token for control plane access
	Region         string            `json:"region,omitempty" yaml:"region"`                   // Region (auto-detected from cloud provider or "unknown")
	CloudProvider  string            `json:"cloud_provider,omitempty" yaml:"cloud_provider"`   // Cloud provider (auto-detected: aws, gcp, azure, digitalocean, linode, hetzner, bare-metal, on-premises, or "agent")
	RegistryRegion string            `json:"registry_region,omitempty" yaml:"registry_region"` // Recommended registry region (eu/us)
	Metadata       map[string]string `json:"metadata,omitempty" yaml:"metadata"`               // Simple key-value metadata

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
	TunnelPrometheusPort int    `json:"tunnel_prometheus_port,omitempty" yaml:"tunnel_prometheus_port"` // Tunnel port for Prometheus (e.g., 19090)

	LokiURL        string `json:"loki_url,omitempty" yaml:"loki_url"`
	LokiUsername   string `json:"loki_username,omitempty" yaml:"loki_username"`
	LokiPassword   string `json:"loki_password,omitempty" yaml:"loki_password"`
	LokiSSL        bool   `json:"loki_ssl,omitempty" yaml:"loki_ssl"`
	TunnelLokiPort int    `json:"tunnel_loki_port,omitempty" yaml:"tunnel_loki_port"` // Tunnel port for Loki (e.g., 13100)

	GrafanaURL        string `json:"grafana_url,omitempty" yaml:"grafana_url"`
	GrafanaUsername   string `json:"grafana_username,omitempty" yaml:"grafana_username"`
	GrafanaPassword   string `json:"grafana_password,omitempty" yaml:"grafana_password"`
	GrafanaSSL        bool   `json:"grafana_ssl,omitempty" yaml:"grafana_ssl"`
	TunnelGrafanaPort int    `json:"tunnel_grafana_port,omitempty" yaml:"tunnel_grafana_port"` // Tunnel port for Grafana (e.g., 13000)

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
	Agent      AgentConfig              `yaml:"agent" mapstructure:"agent"`
	PipeOps    PipeOpsConfig            `yaml:"pipeops" mapstructure:"pipeops"`
	Kubernetes KubernetesConfig         `yaml:"kubernetes" mapstructure:"kubernetes"`
	Logging    LoggingConfig            `yaml:"logging" mapstructure:"logging"`
	Tunnel     *TunnelConfig            `yaml:"tunnel,omitempty" mapstructure:"tunnel"`   // Deprecated: Use Tunnels instead. Will be removed in v2.0.
	Tunnels    *TCPUDPTunnelConfig      `yaml:"tunnels,omitempty" mapstructure:"tunnels"` // TCP/UDP tunneling via Gateway API
	Gateway    *GatewayConfig           `yaml:"gateway,omitempty" mapstructure:"gateway"`
	Upgrade    *UpgradeConfig           `yaml:"upgrade,omitempty" mapstructure:"upgrade"`       // K3s automated upgrade configuration
	Encryption *SecretsEncryptionConfig `yaml:"encryption,omitempty" mapstructure:"encryption"` // K3s secrets encryption configuration
	Timeouts   *Timeouts                `yaml:"timeouts" mapstructure:"timeouts"`
}

// AgentConfig represents agent-specific configuration
type AgentConfig struct {
	Name                          string               `yaml:"name" mapstructure:"name"`
	ID                            string               `yaml:"id" mapstructure:"id"`
	ClusterName                   string               `yaml:"cluster_name" mapstructure:"cluster_name"`
	Labels                        map[string]string    `yaml:"labels" mapstructure:"labels"`
	Port                          int                  `yaml:"port" mapstructure:"port"`
	Debug                         bool                 `yaml:"debug" mapstructure:"debug"`
	GrafanaSubPath                bool                 `yaml:"grafana_sub_path" mapstructure:"grafana_sub_path"`
	Version                       string               `yaml:"version" mapstructure:"version"`
	EnableIngressSync             bool                 `yaml:"enable_ingress_sync" mapstructure:"enable_ingress_sync"`
	GatewayRouteRefreshInterval   int                  `yaml:"gateway_route_refresh_interval" mapstructure:"gateway_route_refresh_interval" env:"GATEWAY_ROUTE_REFRESH_INTERVAL"`            // Route refresh interval in hours (default: 4, prevents 24h TTL expiry)
	HeartbeatIntervalConnected    int                  `yaml:"heartbeat_interval_connected" mapstructure:"heartbeat_interval_connected" env:"PIPEOPS_HEARTBEAT_INTERVAL_CONNECTED"`          // Heartbeat interval when connected (seconds, default: 30)
	HeartbeatIntervalReconnecting int                  `yaml:"heartbeat_interval_reconnecting" mapstructure:"heartbeat_interval_reconnecting" env:"PIPEOPS_HEARTBEAT_INTERVAL_RECONNECTING"` // Heartbeat interval when reconnecting (seconds, default: 60)
	HeartbeatIntervalDisconnected int                  `yaml:"heartbeat_interval_disconnected" mapstructure:"heartbeat_interval_disconnected" env:"PIPEOPS_HEARTBEAT_INTERVAL_DISCONNECTED"` // Heartbeat interval when disconnected (seconds, default: 15)
	TruthfulReadinessProbe        bool                 `yaml:"truthful_readiness_probe" mapstructure:"truthful_readiness_probe" env:"PIPEOPS_TRUTHFUL_READINESS_PROBE"`                      // When enabled, /ready endpoint reflects actual connection state (default: false for backward compatibility)
	Compatibility                 *CompatibilityConfig `yaml:"compatibility,omitempty" mapstructure:"compatibility"`                                                                         // Compatibility settings for existing clusters
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
	Kubeconfig   string `yaml:"kubeconfig" mapstructure:"kubeconfig"`
	InCluster    bool   `yaml:"in_cluster" mapstructure:"in_cluster"`
	Namespace    string `yaml:"namespace" mapstructure:"namespace"`
	ServiceToken string `yaml:"service_token" mapstructure:"service_token"`
	CACertData   string `yaml:"ca_cert_data" mapstructure:"ca_cert_data"`
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

// GatewayConfig enables installing an env-aware TCP gateway (Istio or Gateway API)
type GatewayConfig struct {
	Enabled     bool   `yaml:"enabled" mapstructure:"enabled"`
	ReleaseName string `yaml:"release_name" mapstructure:"release_name"`
	Namespace   string `yaml:"namespace" mapstructure:"namespace"`

	Environment GatewayEnvironmentConfig `yaml:"environment" mapstructure:"environment"`

	Istio      IstioGatewayConfig      `yaml:"istio" mapstructure:"istio"`
	GatewayAPI GatewayAPIGatewayConfig `yaml:"gateway_api" mapstructure:"gateway_api"`
}

type GatewayEnvironmentConfig struct {
	Mode string `yaml:"mode" mapstructure:"mode"` // managed | single-vm
	VMIP string `yaml:"vm_ip" mapstructure:"vm_ip"`
}

type IstioGatewayConfig struct {
	Enabled        bool                      `yaml:"enabled" mapstructure:"enabled"`
	Service        IstioServiceConfig        `yaml:"service" mapstructure:"service"`
	Gateway        IstioGatewayServers       `yaml:"gateway" mapstructure:"gateway"`
	VirtualService IstioVirtualServiceConfig `yaml:"virtual_service" mapstructure:"virtual_service"`
}

type IstioServiceConfig struct {
	Create    bool   `yaml:"create" mapstructure:"create"`
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
}

type IstioGatewayServers struct {
	Selector map[string]string `yaml:"selector" mapstructure:"selector"`
	Servers  []IstioServerSpec `yaml:"servers" mapstructure:"servers"`
}

type IstioServerSpec struct {
	Port struct {
		Number   int    `yaml:"number" mapstructure:"number"`
		Name     string `yaml:"name" mapstructure:"name"`
		Protocol string `yaml:"protocol" mapstructure:"protocol"`
	} `yaml:"port" mapstructure:"port"`
	Hosts []string               `yaml:"hosts" mapstructure:"hosts"`
	TLS   map[string]interface{} `yaml:"tls" mapstructure:"tls"`
}

type IstioVirtualServiceConfig struct {
	TCPRoutes []TCPRouteVSConfig `yaml:"tcp_routes" mapstructure:"tcp_routes"`
	TLSRoutes []TLSRouteVSConfig `yaml:"tls_routes" mapstructure:"tls_routes"`
}

type TCPRouteVSConfig struct {
	Name        string `yaml:"name" mapstructure:"name"`
	Port        int    `yaml:"port" mapstructure:"port"`
	Destination struct {
		Host string `yaml:"host" mapstructure:"host"`
		Port int    `yaml:"port" mapstructure:"port"`
	} `yaml:"destination" mapstructure:"destination"`
}

type TLSRouteVSConfig struct {
	Name        string   `yaml:"name" mapstructure:"name"`
	Port        int      `yaml:"port" mapstructure:"port"`
	SNIHosts    []string `yaml:"sni_hosts" mapstructure:"sni_hosts"`
	Destination struct {
		Host string `yaml:"host" mapstructure:"host"`
		Port int    `yaml:"port" mapstructure:"port"`
	} `yaml:"destination" mapstructure:"destination"`
}

type GatewayAPIGatewayConfig struct {
	Enabled      bool                 `yaml:"enabled" mapstructure:"enabled"`
	GatewayClass string               `yaml:"gateway_class" mapstructure:"gateway_class"`
	Listeners    []GatewayListenerCfg `yaml:"listeners" mapstructure:"listeners"`
	TCPRoutes    []GatewayTCPRouteCfg `yaml:"tcp_routes" mapstructure:"tcp_routes"`
	UDPRoutes    []GatewayUDPRouteCfg `yaml:"udp_routes" mapstructure:"udp_routes"`
}

type GatewayListenerCfg struct {
	Name     string `yaml:"name" mapstructure:"name"`
	Port     int    `yaml:"port" mapstructure:"port"`
	Protocol string `yaml:"protocol" mapstructure:"protocol"`
}

type GatewayTCPRouteCfg struct {
	Name        string          `yaml:"name" mapstructure:"name"`
	SectionName string          `yaml:"section_name" mapstructure:"section_name"`
	BackendRefs []GatewayRefCfg `yaml:"backend_refs" mapstructure:"backend_refs"`
}

type GatewayUDPRouteCfg struct {
	Name        string          `yaml:"name" mapstructure:"name"`
	SectionName string          `yaml:"section_name" mapstructure:"section_name"`
	BackendRefs []GatewayRefCfg `yaml:"backend_refs" mapstructure:"backend_refs"`
}

type GatewayRefCfg struct {
	Name      string `yaml:"name" mapstructure:"name"`
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
	Port      int    `yaml:"port" mapstructure:"port"`
}

// UpgradeConfig configures K3s automated upgrades using system-upgrade-controller
type UpgradeConfig struct {
	// Enabled enables automated K3s upgrades
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`

	// Channel is the K3s release channel to track (stable, latest, v1.30, v1.31, v1.32, v1.33)
	Channel string `yaml:"channel" mapstructure:"channel"`

	// Version is a specific K3s version to upgrade to (overrides channel)
	Version string `yaml:"version,omitempty" mapstructure:"version"`

	// Concurrency is the number of nodes to upgrade simultaneously (default: 1)
	Concurrency int `yaml:"concurrency" mapstructure:"concurrency"`

	// DrainTimeout is the timeout for draining nodes before upgrade
	DrainTimeout string `yaml:"drain_timeout" mapstructure:"drain_timeout"`

	// AutoInstallController automatically installs system-upgrade-controller if missing
	AutoInstallController bool `yaml:"auto_install_controller" mapstructure:"auto_install_controller"`

	// Window defines when upgrades can occur (optional)
	Window *UpgradeWindowConfig `yaml:"window,omitempty" mapstructure:"window"`
}

// UpgradeWindowConfig defines when upgrades are allowed to occur
type UpgradeWindowConfig struct {
	// Days of the week when upgrades are allowed (monday, tuesday, etc.)
	Days []string `yaml:"days" mapstructure:"days"`

	// StartTime in HH:MM format (e.g., "19:00")
	StartTime string `yaml:"start_time" mapstructure:"start_time"`

	// EndTime in HH:MM format (e.g., "21:00")
	EndTime string `yaml:"end_time" mapstructure:"end_time"`

	// TimeZone (e.g., "UTC", "America/New_York")
	TimeZone string `yaml:"timezone" mapstructure:"timezone"`
}

// SecretsEncryptionConfig holds configuration for K3s secrets encryption management
type SecretsEncryptionConfig struct {
	// Enabled enables secrets encryption management
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`

	// Provider is the encryption provider (aescbc or secretbox)
	Provider string `yaml:"provider" mapstructure:"provider"`

	// AutoRotate enables automatic key rotation
	AutoRotate bool `yaml:"auto_rotate" mapstructure:"auto_rotate"`

	// RotationIntervalDays is the interval between automatic key rotations in days
	RotationIntervalDays int `yaml:"rotation_interval_days" mapstructure:"rotation_interval_days"`

	// K3sDataDir is the K3s data directory (default: /var/lib/rancher/k3s)
	K3sDataDir string `yaml:"k3s_data_dir" mapstructure:"k3s_data_dir"`
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
	TunnelPort     int                    `yaml:"tunnel_port" mapstructure:"tunnel_port"`         // Tunnel port for service access
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

// CompatibilityConfig represents compatibility settings for existing clusters
type CompatibilityConfig struct {
	// Mode: strict (fail on conflicts), relaxed (skip), force (overwrite)
	Mode string `yaml:"mode" mapstructure:"mode"`

	// Ingress compatibility settings
	Ingress *IngressCompatibilityConfig `yaml:"ingress,omitempty" mapstructure:"ingress"`

	// Monitoring compatibility settings
	Monitoring *MonitoringCompatibilityConfig `yaml:"monitoring,omitempty" mapstructure:"monitoring"`
}

// IngressCompatibilityConfig represents ingress-specific compatibility settings
type IngressCompatibilityConfig struct {
	// Auto-fix ConfigMaps for SSL redirect loops and WebSocket support
	AutoFixConfigMaps bool `yaml:"auto_fix_configmaps" mapstructure:"auto_fix_configmaps"`

	// Require this annotation on ConfigMaps to allow modifications
	RequireAnnotation string `yaml:"require_annotation,omitempty" mapstructure:"require_annotation"`

	// Backup ConfigMaps before modifying
	BackupBeforeModify bool `yaml:"backup_before_modify" mapstructure:"backup_before_modify"`

	// WebSocket-specific configuration
	WebSocket *WebSocketCompatibilityConfig `yaml:"websocket,omitempty" mapstructure:"websocket"`
}

// WebSocketCompatibilityConfig represents WebSocket-specific settings
type WebSocketCompatibilityConfig struct {
	// Ensure NGINX ConfigMap has WebSocket upgrade support
	EnsureUpgradeSupport bool `yaml:"ensure_upgrade_support" mapstructure:"ensure_upgrade_support"`

	// Disable proxy buffering for WebSocket (recommended)
	DisableBuffering bool `yaml:"disable_buffering" mapstructure:"disable_buffering"`

	// Set appropriate timeouts for long-lived WebSocket connections
	SetTimeouts bool `yaml:"set_timeouts" mapstructure:"set_timeouts"`

	// Timeout value in seconds (default: 3600)
	TimeoutSeconds int `yaml:"timeout_seconds" mapstructure:"timeout_seconds"`

	// Verify ingress annotations for WebSocket services
	VerifyIngressAnnotations bool `yaml:"verify_ingress_annotations" mapstructure:"verify_ingress_annotations"`
}

// MonitoringCompatibilityConfig represents monitoring-specific compatibility settings
type MonitoringCompatibilityConfig struct {
	// Reuse existing monitoring stack instead of installing
	ReuseExisting bool `yaml:"reuse_existing" mapstructure:"reuse_existing"`

	// Install monitoring if not found
	InstallIfMissing bool `yaml:"install_if_missing" mapstructure:"install_if_missing"`
}

// TCPUDPTunnelConfig represents TCP/UDP tunneling configuration via Gateway API
// This enables tunneling to services exposed through Istio Gateway or Gateway API
type TCPUDPTunnelConfig struct {
	Enabled   bool                    `yaml:"enabled" mapstructure:"enabled"`
	Routing   TunnelRoutingConfig     `yaml:"routing" mapstructure:"routing"`
	Discovery TunnelDiscoveryConfig   `yaml:"discovery" mapstructure:"discovery"`
	TCP       TCPTunnelProtocolConfig `yaml:"tcp" mapstructure:"tcp"`
	UDP       UDPTunnelProtocolConfig `yaml:"udp" mapstructure:"udp"`
}

// TunnelRoutingConfig controls routing mode behavior
type TunnelRoutingConfig struct {
	// Force tunnel mode even for public clusters (for security/auditing)
	// When true, all traffic goes through PipeOps gateway instead of direct access
	// Use cases: compliance, centralized logging, access control
	ForceTunnelMode bool `yaml:"force_tunnel_mode" mapstructure:"force_tunnel_mode"`

	// Enable dual-mode registration for public clusters
	// When true, registers both direct (public IP) and tunnel endpoints
	// Users can choose which to use
	DualModeEnabled bool `yaml:"dual_mode_enabled" mapstructure:"dual_mode_enabled"`
}

// TunnelDiscoveryConfig controls service discovery via Gateway API resources
type TunnelDiscoveryConfig struct {
	TCP TunnelProtocolDiscovery `yaml:"tcp" mapstructure:"tcp"`
	UDP TunnelProtocolDiscovery `yaml:"udp" mapstructure:"udp"`
}

// TunnelProtocolDiscovery configures protocol-specific discovery
type TunnelProtocolDiscovery struct {
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`
	// Watch for Gateway resources with this label
	// Example: "pipeops.io/managed"
	GatewayLabel string `yaml:"gateway_label" mapstructure:"gateway_label"`
}

// TCPTunnelProtocolConfig represents TCP-specific tunnel configuration
type TCPTunnelProtocolConfig struct {
	BufferSize        int           `yaml:"buffer_size" mapstructure:"buffer_size"`               // Read/write buffer size (default: 32768)
	Keepalive         bool          `yaml:"keepalive" mapstructure:"keepalive"`                   // Enable TCP keepalive (default: true)
	KeepalivePeriod   time.Duration `yaml:"keepalive_period" mapstructure:"keepalive_period"`     // Keepalive period (default: 30s)
	ConnectionTimeout time.Duration `yaml:"connection_timeout" mapstructure:"connection_timeout"` // Timeout for establishing connection (default: 30s)
	IdleTimeout       time.Duration `yaml:"idle_timeout" mapstructure:"idle_timeout"`             // Idle timeout for connections (default: 300s)
	MaxConnections    int           `yaml:"max_connections" mapstructure:"max_connections"`       // Max concurrent connections per service (default: 1000)
}

// UDPTunnelProtocolConfig represents UDP-specific tunnel configuration
type UDPTunnelProtocolConfig struct {
	BufferSize     int           `yaml:"buffer_size" mapstructure:"buffer_size"`         // Packet buffer size (default: 65507 - max UDP)
	SessionTimeout time.Duration `yaml:"session_timeout" mapstructure:"session_timeout"` // Session timeout for client tracking (default: 300s)
	MaxSessions    int           `yaml:"max_sessions" mapstructure:"max_sessions"`       // Max concurrent sessions per tunnel (default: 1000)
}
