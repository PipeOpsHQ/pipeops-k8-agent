package types

import (
	"time"
)

// Timeouts centralizes all timeout configurations used throughout the agent.
// This provides a single source of truth for timeout values and makes them
// easy to configure and adjust.
type Timeouts struct {
	// WebSocket connection and communication timeouts
	WebSocketHandshake    time.Duration `yaml:"websocket_handshake" json:"websocket_handshake"`
	WebSocketPing         time.Duration `yaml:"websocket_ping" json:"websocket_ping"`
	WebSocketRead         time.Duration `yaml:"websocket_read" json:"websocket_read"`
	WebSocketReconnect    time.Duration `yaml:"websocket_reconnect" json:"websocket_reconnect"`
	WebSocketReconnectMax time.Duration `yaml:"websocket_reconnect_max" json:"websocket_reconnect_max"`

	// Kubernetes API operation timeouts
	K8sOperation     time.Duration `yaml:"k8s_operation" json:"k8s_operation"`
	K8sDeployment    time.Duration `yaml:"k8s_deployment" json:"k8s_deployment"`
	K8sResourceWatch time.Duration `yaml:"k8s_resource_watch" json:"k8s_resource_watch"`

	// HTTP proxy timeouts
	ProxyRequest time.Duration `yaml:"proxy_request" json:"proxy_request"`
	ProxyDial    time.Duration `yaml:"proxy_dial" json:"proxy_dial"`
	ProxyIdle    time.Duration `yaml:"proxy_idle" json:"proxy_idle"`

	// Registration and heartbeat timeouts
	Registration time.Duration `yaml:"registration" json:"registration"`
	Heartbeat    time.Duration `yaml:"heartbeat" json:"heartbeat"`

	// Helm operations
	HelmInstall time.Duration `yaml:"helm_install" json:"helm_install"`
	HelmUpgrade time.Duration `yaml:"helm_upgrade" json:"helm_upgrade"`

	// Graceful shutdown
	Shutdown time.Duration `yaml:"shutdown" json:"shutdown"`
}

// DefaultTimeouts returns the default timeout configuration.
// These values are tuned for production use.
func DefaultTimeouts() *Timeouts {
	return &Timeouts{
		// WebSocket timeouts
		WebSocketHandshake:    30 * time.Second, // Increased from 10s to handle slow connections
		WebSocketPing:         10 * time.Second, // Increased frequency (was 30s) to keep NAT/LB alive
		WebSocketRead:         60 * time.Second,       // 2x ping interval
		WebSocketReconnect:    500 * time.Millisecond, // Fast initial reconnect
		WebSocketReconnectMax: 15 * time.Second,       // Cap for sustained outages

		// Kubernetes API timeouts
		K8sOperation:     30 * time.Second,
		K8sDeployment:    3 * time.Minute,
		K8sResourceWatch: 5 * time.Minute,

		// HTTP proxy timeouts
		ProxyRequest: 30 * time.Second,
		ProxyDial:    30 * time.Second, // Increased from 10s
		ProxyIdle:    90 * time.Second,

		// Registration and heartbeat
		Registration: 30 * time.Second,
		Heartbeat:    30 * time.Second,

		// Helm operations
		HelmInstall: 5 * time.Minute,
		HelmUpgrade: 5 * time.Minute,

		// Graceful shutdown
		Shutdown: 30 * time.Second,
	}
}

// Merge merges non-zero values from other into the current Timeouts.
// This allows partial configuration overrides.
func (t *Timeouts) Merge(other *Timeouts) {
	if other == nil {
		return
	}

	if other.WebSocketHandshake > 0 {
		t.WebSocketHandshake = other.WebSocketHandshake
	}
	if other.WebSocketPing > 0 {
		t.WebSocketPing = other.WebSocketPing
	}
	if other.WebSocketRead > 0 {
		t.WebSocketRead = other.WebSocketRead
	}
	if other.WebSocketReconnect > 0 {
		t.WebSocketReconnect = other.WebSocketReconnect
	}
	if other.WebSocketReconnectMax > 0 {
		t.WebSocketReconnectMax = other.WebSocketReconnectMax
	}
	if other.K8sOperation > 0 {
		t.K8sOperation = other.K8sOperation
	}
	if other.K8sDeployment > 0 {
		t.K8sDeployment = other.K8sDeployment
	}
	if other.K8sResourceWatch > 0 {
		t.K8sResourceWatch = other.K8sResourceWatch
	}
	if other.ProxyRequest > 0 {
		t.ProxyRequest = other.ProxyRequest
	}
	if other.ProxyDial > 0 {
		t.ProxyDial = other.ProxyDial
	}
	if other.ProxyIdle > 0 {
		t.ProxyIdle = other.ProxyIdle
	}
	if other.Registration > 0 {
		t.Registration = other.Registration
	}
	if other.Heartbeat > 0 {
		t.Heartbeat = other.Heartbeat
	}
	if other.HelmInstall > 0 {
		t.HelmInstall = other.HelmInstall
	}
	if other.HelmUpgrade > 0 {
		t.HelmUpgrade = other.HelmUpgrade
	}
	if other.Shutdown > 0 {
		t.Shutdown = other.Shutdown
	}
}
