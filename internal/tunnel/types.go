package tunnel

import (
	"time"
)

// RoutingMode defines how traffic is routed to the service
type RoutingMode string

const (
	// RoutingModeDirect - User connects directly to cluster's public LoadBalancer IP
	// Example: User → 203.0.113.45:5432 → Istio Gateway → Service
	RoutingModeDirect RoutingMode = "direct"

	// RoutingModeTunnel - User connects via PipeOps gateway, tunneled through agent
	// Example: User → gateway.pipeops.io:15432 → WebSocket → Agent → Istio Gateway:5432 → Service
	RoutingModeTunnel RoutingMode = "tunnel"

	// RoutingModeDual - Both direct and tunnel endpoints available
	// User can choose which method to use
	RoutingModeDual RoutingMode = "dual"
)

// ClusterType represents whether a cluster is publicly accessible
type ClusterType string

const (
	// ClusterTypePublic - Cluster has LoadBalancer with external IP
	ClusterTypePublic ClusterType = "public"

	// ClusterTypePrivate - Cluster has no public LoadBalancer (or pending)
	ClusterTypePrivate ClusterType = "private"

	// ClusterTypeUnknown - Cluster type not yet determined
	ClusterTypeUnknown ClusterType = "unknown"
)

// Protocol represents the tunnel protocol type
type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

// TunnelService represents a discovered TCP/UDP service that can be tunneled
type TunnelService struct {
	// Protocol is either "tcp" or "udp"
	Protocol Protocol

	// Gateway information (from Gateway API resources)
	GatewayName      string
	GatewayNamespace string
	GatewayPort      int // Port exposed by Istio Gateway

	// Backend service information
	ServiceName      string
	ServiceNamespace string
	ServicePort      int

	// Tunnel information (for tunnel/dual modes)
	TunnelID       string // Unique identifier: "tcp-{cluster_uuid}-{gateway}-{port}"
	TunnelPort     int    // Port allocated by control plane (15000-35000 range)
	TunnelEndpoint string // Full endpoint: "gateway.pipeops.io:15432"

	// Routing configuration
	RoutingMode RoutingMode // "direct", "tunnel", or "dual"

	// Public endpoint (for direct/dual modes)
	PublicEndpoint string // e.g., "203.0.113.45:5432"

	// Metadata from Gateway resource
	Labels      map[string]string
	Annotations map[string]string

	// Registration status
	RegisteredAt time.Time
	LastSeen     time.Time
	Status       TunnelStatus
}

// TunnelStatus represents the current state of a tunnel
type TunnelStatus string

const (
	TunnelStatusPending    TunnelStatus = "pending"    // Discovered but not yet registered
	TunnelStatusActive     TunnelStatus = "active"     // Registered and available
	TunnelStatusInactive   TunnelStatus = "inactive"   // Temporarily unavailable
	TunnelStatusFailed     TunnelStatus = "failed"     // Registration failed
	TunnelStatusTerminated TunnelStatus = "terminated" // Gateway deleted
)

// TunnelRegistration represents a tunnel registration request/response
type TunnelRegistration struct {
	ClusterUUID string   `json:"cluster_uuid"`
	Protocol    Protocol `json:"protocol"`
	TunnelID    string   `json:"tunnel_id"`

	// Gateway information
	GatewayName      string `json:"gateway_name"`
	GatewayNamespace string `json:"gateway_namespace"`
	GatewayPort      int    `json:"gateway_port"`

	// Backend service
	ServiceName      string `json:"service_name"`
	ServiceNamespace string `json:"service_namespace"`
	ServicePort      int    `json:"service_port"`

	// Routing mode
	RoutingMode RoutingMode `json:"routing_mode"`

	// Public endpoint (if available)
	PublicEndpoint string `json:"public_endpoint,omitempty"`

	// Allocated tunnel port (response only)
	TunnelPort     int    `json:"tunnel_port,omitempty"`
	TunnelEndpoint string `json:"tunnel_endpoint,omitempty"`

	// Metadata
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// TunnelSyncRequest represents a bulk sync request for all tunnels
type TunnelSyncRequest struct {
	ClusterUUID string               `json:"cluster_uuid"`
	Tunnels     []TunnelRegistration `json:"tunnels"`
}

// TunnelSyncResponse represents the response from a sync request
type TunnelSyncResponse struct {
	Accepted []TunnelRegistration `json:"accepted"` // Successfully registered
	Rejected []TunnelRejection    `json:"rejected"` // Failed registrations
}

// TunnelRejection represents a rejected tunnel registration
type TunnelRejection struct {
	TunnelID string `json:"tunnel_id"`
	Reason   string `json:"reason"`
}

// GatewayListener represents a listener from a Gateway resource
type GatewayListener struct {
	Name     string
	Port     int
	Protocol Protocol
}

// TCPRouteInfo represents extracted information from a TCPRoute
type TCPRouteInfo struct {
	Name      string
	Namespace string
	Gateway   string
	Port      int
	Backends  []BackendRef
}

// UDPRouteInfo represents extracted information from a UDPRoute
type UDPRouteInfo struct {
	Name      string
	Namespace string
	Gateway   string
	Port      int
	Backends  []BackendRef
}

// BackendRef represents a backend service reference
type BackendRef struct {
	Name      string
	Namespace string
	Port      int
	Weight    int
}
