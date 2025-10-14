package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// PollService handles polling the control plane for tunnel status
type PollService struct {
	config     *PollConfig
	client     *Client
	httpClient *http.Client
	logger     *logrus.Logger

	// Activity tracking
	lastActivity time.Time
	activityChan chan struct{}

	// Control channels
	stopChan chan struct{}
}

// PollConfig represents poll service configuration
type PollConfig struct {
	// Control plane API URL
	APIURL string

	// Agent ID
	AgentID string

	// Authentication token
	Token string

	// Poll frequency
	PollInterval time.Duration

	// Inactivity timeout for closing tunnel
	InactivityTimeout time.Duration

	// Configured port forwards
	Forwards []PortForward
}

// PortForward represents a configured port forward
type PortForward struct {
	Name      string
	LocalAddr string
}

// PollStatusResponse represents the response from control plane
type PollStatusResponse struct {
	Status            string              `json:"status"`                // IDLE, REQUIRED, ACTIVE
	Forwards          []ForwardAllocation `json:"forwards,omitempty"`    // Allocated port forwards
	Credentials       string              `json:"credentials,omitempty"` // Encrypted SSH credentials
	PollFrequency     int                 `json:"poll_frequency"`        // Seconds between polls
	TunnelServerAddr  string              `json:"tunnel_server_addr"`    // Tunnel server address
	ServerFingerprint string              `json:"server_fingerprint"`    // SSH fingerprint
}

// ForwardAllocation represents an allocated port forward from control plane
type ForwardAllocation struct {
	Name       string `json:"name"`        // Name of the forward (e.g., "kubernetes-api")
	RemotePort int    `json:"remote_port"` // Allocated remote port
}

// TunnelStatus represents tunnel state constants
const (
	TunnelStatusIdle     = "IDLE"
	TunnelStatusRequired = "REQUIRED"
	TunnelStatusActive   = "ACTIVE"
)

// NewPollService creates a new poll service
func NewPollService(config *PollConfig, logger *logrus.Logger) *PollService {
	return &PollService{
		config: config,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:       logger,
		activityChan: make(chan struct{}, 10),
		stopChan:     make(chan struct{}),
	}
}

// Start begins the polling loop
func (ps *PollService) Start(ctx context.Context) error {
	ps.logger.WithFields(logrus.Fields{
		"api_url":            ps.config.APIURL,
		"agent_id":           ps.config.AgentID,
		"poll_interval":      ps.config.PollInterval,
		"inactivity_timeout": ps.config.InactivityTimeout,
	}).Info("Starting tunnel poll service")

	// Start polling loop
	go ps.pollLoop(ctx)

	// Start activity monitoring loop
	go ps.activityMonitorLoop(ctx)

	return nil
}

// Stop stops the poll service
func (ps *PollService) Stop() {
	ps.logger.Info("Stopping tunnel poll service")
	close(ps.stopChan)

	// Close tunnel if open
	if ps.client != nil && ps.client.IsTunnelOpen() {
		ps.client.CloseTunnel()
	}
}

// pollLoop continuously polls the control plane for tunnel status
func (ps *PollService) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(ps.config.PollInterval)
	defer ticker.Stop()

	// Poll immediately on start
	ps.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ps.stopChan:
			return
		case <-ticker.C:
			ps.poll(ctx)
		}
	}
}

// poll performs a single poll to the control plane
func (ps *PollService) poll(ctx context.Context) {
	ps.logger.Debug("Polling control plane for tunnel status")

	// Build poll URL
	url := fmt.Sprintf("%s/api/agents/%s/tunnel/status", ps.config.APIURL, ps.config.AgentID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		ps.logger.WithError(err).Error("Failed to create poll request")
		return
	}

	// Add authentication
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ps.config.Token))
	req.Header.Set("User-Agent", "PipeOps-Agent/1.0")

	resp, err := ps.httpClient.Do(req)
	if err != nil {
		ps.logger.WithError(err).Error("Failed to poll control plane")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Don't spam logs for 404 - endpoint might not be implemented yet
		if resp.StatusCode == http.StatusNotFound {
			ps.logger.Debug("Tunnel poll endpoint not found - control plane may not support tunnel polling yet")
		} else {
			ps.logger.WithFields(logrus.Fields{
				"status_code": resp.StatusCode,
				"body":        string(body),
			}).Error("Poll request failed")
		}
		return
	}

	// Parse response
	var status PollStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		// Don't spam logs for EOF - likely empty response from unsupported endpoint
		if err == io.EOF {
			ps.logger.Debug("Empty poll response - control plane may not support tunnel polling yet")
		} else {
			ps.logger.WithError(err).Error("Failed to decode poll response")
		}
		return
	}

	ps.logger.WithFields(logrus.Fields{
		"status":         status.Status,
		"forwards":       len(status.Forwards),
		"poll_frequency": status.PollFrequency,
	}).Debug("Received tunnel status from control plane")

	// Handle tunnel status
	ps.handleTunnelStatus(ctx, &status)

	// Update poll frequency if changed
	if status.PollFrequency > 0 {
		newInterval := time.Duration(status.PollFrequency) * time.Second
		if newInterval != ps.config.PollInterval {
			ps.config.PollInterval = newInterval
			ps.logger.WithField("new_interval", newInterval).Info("Updated poll interval")
		}
	}
}

// handleTunnelStatus processes the tunnel status and manages tunnel lifecycle
func (ps *PollService) handleTunnelStatus(ctx context.Context, status *PollStatusResponse) {
	switch status.Status {
	case TunnelStatusIdle:
		// Tunnel not needed, close if open
		if ps.client != nil && ps.client.IsTunnelOpen() {
			ps.logger.Info("Tunnel status is IDLE, closing tunnel")
			if err := ps.client.CloseTunnel(); err != nil {
				ps.logger.WithError(err).Error("Failed to close tunnel")
			}
		}

	case TunnelStatusRequired:
		// Tunnel requested, create if not already open
		if ps.client == nil || !ps.client.IsTunnelOpen() {
			ps.logger.Info("Tunnel status is REQUIRED, creating tunnel")
			if err := ps.createTunnel(ctx, status); err != nil {
				ps.logger.WithError(err).Error("Failed to create tunnel")
			} else {
				// Reset activity timer
				ps.resetActivityTimer()
			}
		} else {
			ps.logger.Debug("Tunnel already open, status remains REQUIRED")
		}

	case TunnelStatusActive:
		// Tunnel should be active
		if ps.client != nil && ps.client.IsTunnelOpen() {
			ps.logger.Debug("Tunnel status is ACTIVE, tunnel operational")
		} else {
			ps.logger.Warn("Tunnel status is ACTIVE but tunnel not open, creating tunnel")
			if err := ps.createTunnel(ctx, status); err != nil {
				ps.logger.WithError(err).Error("Failed to create tunnel")
			}
		}
	}
}

// createTunnel creates a new tunnel with the provided configuration
func (ps *PollService) createTunnel(ctx context.Context, status *PollStatusResponse) error {
	// Decrypt credentials
	credentials, err := ps.decryptCredentials(status.Credentials)
	if err != nil {
		return fmt.Errorf("failed to decrypt credentials: %w", err)
	}

	// Build forwards list from control plane allocations
	var forwards []Forward
	for _, allocation := range status.Forwards {
		// Find matching local config
		for _, configFwd := range ps.config.Forwards {
			if configFwd.Name == allocation.Name {
				forwards = append(forwards, Forward{
					Name:       allocation.Name,
					LocalAddr:  configFwd.LocalAddr,
					RemotePort: fmt.Sprintf("%d", allocation.RemotePort),
				})
				ps.logger.WithFields(logrus.Fields{
					"name":        allocation.Name,
					"local_addr":  configFwd.LocalAddr,
					"remote_port": allocation.RemotePort,
				}).Info("Port forward allocated by control plane")
				break
			}
		}
	}

	if len(forwards) == 0 {
		return fmt.Errorf("no valid port forwards received from control plane")
	}

	// Build tunnel config with multiple forwards
	tunnelConfig := &Config{
		ServerAddr:        status.TunnelServerAddr,
		ServerFingerprint: status.ServerFingerprint,
		Forwards:          forwards,
		Credentials:       credentials,
	}

	// Create tunnel client
	ps.client = NewClient(tunnelConfig, ps.logger)

	// Create tunnel
	if err := ps.client.CreateTunnel(ctx); err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}

	return nil
}

// decryptCredentials decrypts the tunnel credentials
// TODO: Implement proper decryption based on your security model
func (ps *PollService) decryptCredentials(encrypted string) (string, error) {
	// For now, assume credentials are base64 encoded
	// In production, implement proper encryption/decryption
	decoded, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// activityMonitorLoop monitors tunnel activity and closes on inactivity
func (ps *PollService) activityMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ps.stopChan:
			return
		case <-ticker.C:
			ps.checkInactivity()
		case <-ps.activityChan:
			ps.lastActivity = time.Now()
		}
	}
}

// checkInactivity checks if tunnel should be closed due to inactivity
func (ps *PollService) checkInactivity() {
	if ps.client == nil || !ps.client.IsTunnelOpen() {
		return
	}

	if ps.lastActivity.IsZero() {
		return
	}

	elapsed := time.Since(ps.lastActivity)

	ps.logger.WithField("last_activity", elapsed).Debug("Checking tunnel inactivity")

	if elapsed > ps.config.InactivityTimeout {
		ps.logger.WithFields(logrus.Fields{
			"elapsed":            elapsed,
			"inactivity_timeout": ps.config.InactivityTimeout,
		}).Info("Closing tunnel due to inactivity")

		if err := ps.client.CloseTunnel(); err != nil {
			ps.logger.WithError(err).Error("Failed to close inactive tunnel")
		}
	}
}

// resetActivityTimer resets the activity timer
func (ps *PollService) resetActivityTimer() {
	select {
	case ps.activityChan <- struct{}{}:
	default:
		// Channel full, skip
	}
}

// RecordActivity records tunnel activity (called by HTTP handlers)
func (ps *PollService) RecordActivity() {
	ps.resetActivityTimer()
}
