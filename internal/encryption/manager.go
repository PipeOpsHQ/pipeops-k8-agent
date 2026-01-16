package encryption

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Provider represents the encryption provider type
type Provider string

const (
	// ProviderAESCBC is the default AES-CBC encryption provider
	ProviderAESCBC Provider = "aescbc"

	// ProviderSecretBox is the newer SecretBox encryption provider
	ProviderSecretBox Provider = "secretbox"
)

// RotationStage represents the current stage of key rotation
type RotationStage string

const (
	StageStart             RotationStage = "start"
	StagePrepare           RotationStage = "prepare"
	StageRotate            RotationStage = "rotate"
	StageReencryptRequest  RotationStage = "reencrypt_request"
	StageReencryptActive   RotationStage = "reencrypt_active"
	StageReencryptFinished RotationStage = "reencrypt_finished"
)

// Config holds configuration for secrets encryption
type Config struct {
	// Enabled enables secrets encryption management
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Provider is the encryption provider (aescbc or secretbox)
	Provider Provider `yaml:"provider" json:"provider"`

	// AutoRotate enables automatic key rotation
	AutoRotate bool `yaml:"auto_rotate" json:"auto_rotate"`

	// RotationInterval is the interval between automatic key rotations
	RotationInterval time.Duration `yaml:"rotation_interval" json:"rotation_interval"`

	// K3sDataDir is the K3s data directory (default: /var/lib/rancher/k3s)
	K3sDataDir string `yaml:"k3s_data_dir" json:"k3s_data_dir"`
}

// Status represents the current secrets encryption status
type Status struct {
	// Enabled indicates if encryption is enabled
	Enabled bool `json:"enabled"`

	// CurrentKeyType is the type of encryption key (e.g., "AES-CBC")
	CurrentKeyType string `json:"current_key_type"`

	// ActiveKeyName is the name of the currently active key
	ActiveKeyName string `json:"active_key_name"`

	// RotationStage is the current stage of key rotation
	RotationStage RotationStage `json:"rotation_stage"`

	// ServerHashes contains encryption hashes for each server
	ServerHashes map[string]string `json:"server_hashes,omitempty"`

	// LastChecked is when the status was last checked
	LastChecked time.Time `json:"last_checked"`

	// Error contains any error message from the last status check
	Error string `json:"error,omitempty"`
}

// DefaultConfig returns the default secrets encryption configuration
func DefaultConfig() Config {
	return Config{
		Enabled:          false,
		Provider:         ProviderAESCBC,
		AutoRotate:       false,
		RotationInterval: 30 * 24 * time.Hour, // 30 days
		K3sDataDir:       "/var/lib/rancher/k3s",
	}
}

// Manager handles K3s secrets encryption operations
type Manager struct {
	config Config
	logger *logrus.Logger
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	// Last known status
	lastStatus *Status
	statusMu   sync.RWMutex
}

// NewManager creates a new secrets encryption manager
func NewManager(config Config, logger *logrus.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		config: config,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start starts the secrets encryption manager
func (m *Manager) Start() error {
	if !m.config.Enabled {
		m.logger.Info("[ENCRYPTION] Secrets encryption management disabled")
		return nil
	}

	m.logger.WithFields(logrus.Fields{
		"provider":    m.config.Provider,
		"auto_rotate": m.config.AutoRotate,
	}).Info("[ENCRYPTION] Starting secrets encryption manager")

	// Get initial status
	status, err := m.GetStatus(m.ctx)
	if err != nil {
		m.logger.WithError(err).Warn("[ENCRYPTION] Failed to get initial encryption status")
	} else {
		m.logger.WithFields(logrus.Fields{
			"enabled":        status.Enabled,
			"key_type":       status.CurrentKeyType,
			"active_key":     status.ActiveKeyName,
			"rotation_stage": status.RotationStage,
		}).Info("[ENCRYPTION] Current encryption status")
	}

	// Start auto-rotation if enabled
	if m.config.AutoRotate && m.config.RotationInterval > 0 {
		go m.autoRotationLoop()
	}

	return nil
}

// Stop stops the secrets encryption manager
func (m *Manager) Stop() {
	m.cancel()
	m.logger.Info("[ENCRYPTION] Secrets encryption manager stopped")
}

// GetStatus returns the current secrets encryption status
func (m *Manager) GetStatus(ctx context.Context) (*Status, error) {
	m.logger.Debug("[ENCRYPTION] Getting encryption status")

	status := &Status{
		LastChecked: time.Now(),
	}

	// Run k3s secrets-encrypt status
	args := []string{"secrets-encrypt", "status"}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if k3s is not installed or not running as root
		if strings.Contains(string(output), "not found") {
			status.Error = "k3s command not found"
			return status, fmt.Errorf("k3s command not found: %w", err)
		}
		if strings.Contains(string(output), "permission denied") {
			status.Error = "permission denied - must run as root"
			return status, fmt.Errorf("permission denied: %w", err)
		}
		status.Error = string(output)
		return status, fmt.Errorf("failed to get encryption status: %w", err)
	}

	// Parse the output
	if err := m.parseStatus(string(output), status); err != nil {
		status.Error = err.Error()
		return status, err
	}

	// Cache the status
	m.statusMu.Lock()
	m.lastStatus = status
	m.statusMu.Unlock()

	return status, nil
}

// parseStatus parses the output of k3s secrets-encrypt status
func (m *Manager) parseStatus(output string, status *Status) error {
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "Encryption Status:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Encryption Status:"))
			status.Enabled = value == "Enabled"
		} else if strings.HasPrefix(line, "Current Rotation Stage:") {
			value := strings.TrimSpace(strings.TrimPrefix(line, "Current Rotation Stage:"))
			status.RotationStage = RotationStage(value)
		} else if strings.HasPrefix(line, "Server Encryption Hashes:") {
			// Next lines will contain server hashes
			status.ServerHashes = make(map[string]string)
		} else if strings.Contains(line, ":") && status.ServerHashes != nil {
			// Parse server hash entries like "server-1: abc123"
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				status.ServerHashes[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		} else if strings.HasPrefix(line, "Active Key Type:") {
			status.CurrentKeyType = strings.TrimSpace(strings.TrimPrefix(line, "Active Key Type:"))
		} else if strings.HasPrefix(line, "Active Key:") {
			status.ActiveKeyName = strings.TrimSpace(strings.TrimPrefix(line, "Active Key:"))
		}
	}

	return scanner.Err()
}

// Enable enables secrets encryption
func (m *Manager) Enable(ctx context.Context) error {
	m.logger.Info("[ENCRYPTION] Enabling secrets encryption")

	args := []string{"secrets-encrypt", "enable"}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to enable encryption: %s: %w", string(output), err)
	}

	m.logger.Info("[ENCRYPTION] Secrets encryption enabled - K3s server restart required")
	return nil
}

// Disable disables secrets encryption
func (m *Manager) Disable(ctx context.Context) error {
	m.logger.Info("[ENCRYPTION] Disabling secrets encryption")

	args := []string{"secrets-encrypt", "disable"}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to disable encryption: %s: %w", string(output), err)
	}

	m.logger.Info("[ENCRYPTION] Secrets encryption disabled - K3s server restart required")
	return nil
}

// Prepare prepares for key rotation by generating a new encryption key
func (m *Manager) Prepare(ctx context.Context, force bool) error {
	m.logger.Info("[ENCRYPTION] Preparing for key rotation")

	args := []string{"secrets-encrypt", "prepare"}
	if force {
		args = append(args, "--force")
	}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to prepare key rotation: %s: %w", string(output), err)
	}

	m.logger.Info("[ENCRYPTION] Key rotation prepared - K3s server restart required")
	return nil
}

// Rotate rotates the encryption key, making the prepared key the primary key
func (m *Manager) Rotate(ctx context.Context, force bool) error {
	m.logger.Info("[ENCRYPTION] Rotating encryption key")

	args := []string{"secrets-encrypt", "rotate"}
	if force {
		args = append(args, "--force")
	}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to rotate key: %s: %w", string(output), err)
	}

	m.logger.Info("[ENCRYPTION] Encryption key rotated - K3s server restart required")
	return nil
}

// Reencrypt re-encrypts all secrets with the new key
func (m *Manager) Reencrypt(ctx context.Context, force bool, skip bool) error {
	m.logger.Info("[ENCRYPTION] Re-encrypting secrets")

	args := []string{"secrets-encrypt", "reencrypt"}
	if force {
		args = append(args, "--force")
	}
	if skip {
		args = append(args, "--skip")
	}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to re-encrypt secrets: %s: %w", string(output), err)
	}

	m.logger.Info("[ENCRYPTION] Secrets re-encrypted - K3s server restart required")
	return nil
}

// RotateKeys performs the complete key rotation in one command (K3s v1.28+)
func (m *Manager) RotateKeys(ctx context.Context) error {
	m.logger.Info("[ENCRYPTION] Performing complete key rotation")

	args := []string{"secrets-encrypt", "rotate-keys"}
	if m.config.K3sDataDir != "" && m.config.K3sDataDir != "/var/lib/rancher/k3s" {
		args = append(args, "--data-dir", m.config.K3sDataDir)
	}

	cmd := exec.CommandContext(ctx, "k3s", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to rotate keys: %s: %w", string(output), err)
	}

	m.logger.Info("[ENCRYPTION] Key rotation completed successfully")
	return nil
}

// GetLastStatus returns the last cached status
func (m *Manager) GetLastStatus() *Status {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.lastStatus
}

// autoRotationLoop handles automatic key rotation
func (m *Manager) autoRotationLoop() {
	ticker := time.NewTicker(m.config.RotationInterval)
	defer ticker.Stop()

	m.logger.WithField("interval", m.config.RotationInterval).Info("[ENCRYPTION] Auto-rotation enabled")

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.performAutoRotation()
		}
	}
}

// performAutoRotation performs automatic key rotation
func (m *Manager) performAutoRotation() {
	m.logger.Info("[ENCRYPTION] Starting automatic key rotation")

	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
	defer cancel()

	// Check current status
	status, err := m.GetStatus(ctx)
	if err != nil {
		m.logger.WithError(err).Error("[ENCRYPTION] Failed to get status for auto-rotation")
		return
	}

	if !status.Enabled {
		m.logger.Debug("[ENCRYPTION] Encryption not enabled, skipping auto-rotation")
		return
	}

	// Check if already in rotation
	if status.RotationStage != StageStart && status.RotationStage != StageReencryptFinished {
		m.logger.WithField("stage", status.RotationStage).Warn("[ENCRYPTION] Rotation already in progress, skipping")
		return
	}

	// Use rotate-keys for single-command rotation
	if err := m.RotateKeys(ctx); err != nil {
		m.logger.WithError(err).Error("[ENCRYPTION] Auto-rotation failed")
		return
	}

	m.logger.Info("[ENCRYPTION] Auto-rotation completed successfully")
}

// UpdateConfig updates the encryption configuration
func (m *Manager) UpdateConfig(config Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
}

// StatusJSON returns the status as JSON
func (m *Manager) StatusJSON(ctx context.Context) ([]byte, error) {
	status, err := m.GetStatus(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(status)
}

// IsEncryptionEnabled returns whether encryption is currently enabled
func (m *Manager) IsEncryptionEnabled() bool {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	if m.lastStatus != nil {
		return m.lastStatus.Enabled
	}
	return false
}

// MigrateProvider migrates from one encryption provider to another
func (m *Manager) MigrateProvider(ctx context.Context, newProvider Provider) error {
	m.logger.WithFields(logrus.Fields{
		"from": m.config.Provider,
		"to":   newProvider,
	}).Info("[ENCRYPTION] Migrating encryption provider")

	// This requires modifying the encryption config and restarting
	// The actual migration is done via k3s server restart with new flags
	// This method is a placeholder for the migration workflow

	return fmt.Errorf("provider migration requires K3s server restart with --secrets-encryption-provider=%s", newProvider)
}

// EncryptionConfigPath returns the path to the encryption config file
func (m *Manager) EncryptionConfigPath() string {
	dataDir := m.config.K3sDataDir
	if dataDir == "" {
		dataDir = "/var/lib/rancher/k3s"
	}
	return fmt.Sprintf("%s/server/cred/encryption-config.json", dataDir)
}

// ValidateStatus validates that encryption is properly configured
func (m *Manager) ValidateStatus(ctx context.Context) error {
	status, err := m.GetStatus(ctx)
	if err != nil {
		return err
	}

	var issues []string

	// Check for mismatched server hashes (indicates HA sync issues)
	if len(status.ServerHashes) > 1 {
		hashes := make(map[string]bool)
		for _, hash := range status.ServerHashes {
			hashes[hash] = true
		}
		if len(hashes) > 1 {
			issues = append(issues, "server encryption hashes do not match - HA sync issue")
		}
	}

	// Check for stuck rotation
	if status.RotationStage != StageStart && status.RotationStage != StageReencryptFinished {
		issues = append(issues, fmt.Sprintf("key rotation appears to be in progress (stage: %s)", status.RotationStage))
	}

	if len(issues) > 0 {
		return fmt.Errorf("encryption validation issues: %s", strings.Join(issues, "; "))
	}

	return nil
}

// GetConfig returns the current configuration
func (m *Manager) GetConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// FormatStatus returns a human-readable status string
func (s *Status) FormatStatus() string {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("Encryption Status: %s\n", boolToEnabled(s.Enabled)))
	if s.CurrentKeyType != "" {
		buf.WriteString(fmt.Sprintf("Active Key Type: %s\n", s.CurrentKeyType))
	}
	if s.ActiveKeyName != "" {
		buf.WriteString(fmt.Sprintf("Active Key: %s\n", s.ActiveKeyName))
	}
	if s.RotationStage != "" {
		buf.WriteString(fmt.Sprintf("Current Rotation Stage: %s\n", s.RotationStage))
	}
	if len(s.ServerHashes) > 0 {
		buf.WriteString("Server Encryption Hashes:\n")
		for server, hash := range s.ServerHashes {
			buf.WriteString(fmt.Sprintf("  %s: %s\n", server, hash))
		}
	}
	if s.Error != "" {
		buf.WriteString(fmt.Sprintf("Error: %s\n", s.Error))
	}

	return buf.String()
}

func boolToEnabled(b bool) string {
	if b {
		return "Enabled"
	}
	return "Disabled"
}
