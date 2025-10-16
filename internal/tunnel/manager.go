package tunnel

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

// Manager coordinates the tunnel lifecycle
// Note: Tunnel control is now handled via WebSocket push notifications from the control plane
// instead of HTTP polling. The manager maintains the tunnel client and handles commands.
type Manager struct {
	client       *Client
	logger       *logrus.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	lastActivity time.Time
}

// ManagerConfig represents manager configuration
type ManagerConfig struct {
	// Control Plane configuration
	ControlPlaneURL string
	AgentID         string
	Token           string

	// Tunnel configuration
	InactivityTimeout string              // e.g., "5m"
	Forwards          []PortForwardConfig // Port forwards to establish
}

// PortForwardConfig represents a configured port forward
type PortForwardConfig struct {
	Name      string
	LocalAddr string
}

// NewManager creates a new tunnel manager
func NewManager(config *ManagerConfig, logger *logrus.Logger) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		client:       nil, // Will be created when tunnel is opened via WebSocket command
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
		lastActivity: time.Now(),
	}, nil
}

// Start starts the tunnel manager
// Note: The tunnel is no longer started automatically. It waits for WebSocket commands.
func (m *Manager) Start() error {
	m.logger.Info("Tunnel manager started - waiting for WebSocket commands")
	return nil
}

// Stop stops the tunnel manager
func (m *Manager) Stop() error {
	m.logger.Info("Stopping tunnel manager")

	m.cancel()

	// Close tunnel if open
	if m.client != nil && m.client.IsTunnelOpen() {
		m.client.CloseTunnel()
	}

	m.logger.Info("Tunnel manager stopped")
	return nil
}

// IsTunnelOpen returns true if a tunnel is currently open
func (m *Manager) IsTunnelOpen() bool {
	if m.client == nil {
		return false
	}
	return m.client.IsTunnelOpen()
}

// RecordActivity records activity on the tunnel
func (m *Manager) RecordActivity() {
	m.lastActivity = time.Now()
}

// GetLastActivity returns the time of last tunnel activity
func (m *Manager) GetLastActivity() time.Time {
	return m.lastActivity
}

// parseDuration parses a duration string with a default fallback
func parseDuration(value, defaultValue string) (time.Duration, error) {
	if value == "" {
		value = defaultValue
	}
	return time.ParseDuration(value)
}
