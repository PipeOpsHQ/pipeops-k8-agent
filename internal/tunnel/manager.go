package tunnel

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// Manager coordinates the tunnel lifecycle
type Manager struct {
	pollService *PollService
	client      *Client
	logger      *logrus.Logger
	ctx         context.Context
	cancel      context.CancelFunc
}

// ManagerConfig represents manager configuration
type ManagerConfig struct {
	// Control Plane configuration
	ControlPlaneURL string
	AgentID         string
	Token           string

	// Tunnel configuration
	PollInterval      string              // e.g., "5s"
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

	// Parse durations
	pollInterval, err := parseDuration(config.PollInterval, "5s")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("invalid poll interval: %w", err)
	}

	inactivityTimeout, err := parseDuration(config.InactivityTimeout, "5m")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("invalid inactivity timeout: %w", err)
	}

	// Convert forwards config
	var forwards []PortForward
	for _, fwd := range config.Forwards {
		forwards = append(forwards, PortForward{
			Name:      fwd.Name,
			LocalAddr: fwd.LocalAddr,
		})
	}

	// Create poll service
	pollConfig := &PollConfig{
		APIURL:            config.ControlPlaneURL,
		AgentID:           config.AgentID,
		Token:             config.Token,
		PollInterval:      pollInterval,
		InactivityTimeout: inactivityTimeout,
		Forwards:          forwards,
	}

	pollService := NewPollService(pollConfig, logger)

	return &Manager{
		pollService: pollService,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// Start starts the tunnel manager
func (m *Manager) Start() error {
	m.logger.Info("Starting tunnel manager")

	if err := m.pollService.Start(m.ctx); err != nil {
		return fmt.Errorf("failed to start poll service: %w", err)
	}

	m.logger.Info("Tunnel manager started successfully")
	return nil
}

// Stop stops the tunnel manager
func (m *Manager) Stop() error {
	m.logger.Info("Stopping tunnel manager")

	m.cancel()
	m.pollService.Stop()

	m.logger.Info("Tunnel manager stopped")
	return nil
}

// IsTunnelOpen returns true if a tunnel is currently open
func (m *Manager) IsTunnelOpen() bool {
	if m.pollService.client == nil {
		return false
	}
	return m.pollService.client.IsTunnelOpen()
}

// RecordActivity records activity on the tunnel
func (m *Manager) RecordActivity() {
	m.pollService.RecordActivity()
}

// parseDuration parses a duration string with a default fallback
func parseDuration(value, defaultValue string) (time.Duration, error) {
	if value == "" {
		value = defaultValue
	}
	return time.ParseDuration(value)
}
