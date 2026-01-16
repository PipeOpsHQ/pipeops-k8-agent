package encryption

import (
	"bytes"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.False(t, config.Enabled)
	assert.Equal(t, ProviderAESCBC, config.Provider)
	assert.False(t, config.AutoRotate)
	assert.Equal(t, 30*24*time.Hour, config.RotationInterval)
	assert.Equal(t, "/var/lib/rancher/k3s", config.K3sDataDir)
}

func TestNewManager(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	config := DefaultConfig()
	config.Enabled = true

	manager := NewManager(config, logger)
	require.NotNil(t, manager)

	assert.Equal(t, config, manager.config)
	assert.NotNil(t, manager.ctx)
	assert.NotNil(t, manager.cancel)
}

func TestParseStatus(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	manager := NewManager(DefaultConfig(), logger)

	tests := []struct {
		name     string
		output   string
		expected Status
	}{
		{
			name: "encryption enabled",
			output: `Encryption Status: Enabled
Current Rotation Stage: start
Active Key Type: AES-CBC
Active Key: aescbckey-1234567890`,
			expected: Status{
				Enabled:        true,
				RotationStage:  StageStart,
				CurrentKeyType: "AES-CBC",
				ActiveKeyName:  "aescbckey-1234567890",
			},
		},
		{
			name:   "encryption disabled",
			output: `Encryption Status: Disabled`,
			expected: Status{
				Enabled: false,
			},
		},
		{
			name: "with server hashes",
			output: `Encryption Status: Enabled
Current Rotation Stage: prepare
Server Encryption Hashes:
  server-1: abc123
  server-2: abc123`,
			expected: Status{
				Enabled:       true,
				RotationStage: StagePrepare,
				ServerHashes: map[string]string{
					"server-1": "abc123",
					"server-2": "abc123",
				},
			},
		},
		{
			name: "reencrypt in progress",
			output: `Encryption Status: Enabled
Current Rotation Stage: reencrypt_active`,
			expected: Status{
				Enabled:       true,
				RotationStage: StageReencryptActive,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := &Status{}
			err := manager.parseStatus(tc.output, status)
			require.NoError(t, err)

			assert.Equal(t, tc.expected.Enabled, status.Enabled)
			assert.Equal(t, tc.expected.RotationStage, status.RotationStage)
			assert.Equal(t, tc.expected.CurrentKeyType, status.CurrentKeyType)
			assert.Equal(t, tc.expected.ActiveKeyName, status.ActiveKeyName)

			if tc.expected.ServerHashes != nil {
				assert.Equal(t, tc.expected.ServerHashes, status.ServerHashes)
			}
		})
	}
}

func TestManagerStartDisabled(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	config := DefaultConfig()
	config.Enabled = false

	manager := NewManager(config, logger)
	err := manager.Start()
	require.NoError(t, err)

	manager.Stop()
}

func TestManagerGetConfig(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	config := DefaultConfig()
	config.Enabled = true
	config.Provider = ProviderSecretBox
	config.AutoRotate = true

	manager := NewManager(config, logger)
	got := manager.GetConfig()

	assert.Equal(t, config, got)
}

func TestManagerUpdateConfig(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	manager := NewManager(DefaultConfig(), logger)

	newConfig := Config{
		Enabled:          true,
		Provider:         ProviderSecretBox,
		AutoRotate:       true,
		RotationInterval: 60 * 24 * time.Hour,
		K3sDataDir:       "/custom/path",
	}

	manager.UpdateConfig(newConfig)
	got := manager.GetConfig()

	assert.Equal(t, newConfig, got)
}

func TestEncryptionConfigPath(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	tests := []struct {
		name     string
		dataDir  string
		expected string
	}{
		{
			name:     "default path",
			dataDir:  "",
			expected: "/var/lib/rancher/k3s/server/cred/encryption-config.json",
		},
		{
			name:     "custom path",
			dataDir:  "/custom/k3s",
			expected: "/custom/k3s/server/cred/encryption-config.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultConfig()
			config.K3sDataDir = tc.dataDir

			manager := NewManager(config, logger)
			path := manager.EncryptionConfigPath()

			assert.Equal(t, tc.expected, path)
		})
	}
}

func TestStatusFormatStatus(t *testing.T) {
	status := &Status{
		Enabled:        true,
		CurrentKeyType: "AES-CBC",
		ActiveKeyName:  "aescbckey-123",
		RotationStage:  StageStart,
		ServerHashes: map[string]string{
			"server-1": "abc123",
		},
	}

	output := status.FormatStatus()

	assert.Contains(t, output, "Encryption Status: Enabled")
	assert.Contains(t, output, "Active Key Type: AES-CBC")
	assert.Contains(t, output, "Active Key: aescbckey-123")
	assert.Contains(t, output, "Current Rotation Stage: start")
	assert.Contains(t, output, "server-1: abc123")
}

func TestStatusFormatStatusWithError(t *testing.T) {
	status := &Status{
		Enabled: false,
		Error:   "k3s command not found",
	}

	output := status.FormatStatus()

	assert.Contains(t, output, "Encryption Status: Disabled")
	assert.Contains(t, output, "Error: k3s command not found")
}

func TestIsEncryptionEnabled(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	manager := NewManager(DefaultConfig(), logger)

	// Initially should return false (no status cached)
	assert.False(t, manager.IsEncryptionEnabled())

	// Manually set cached status
	manager.statusMu.Lock()
	manager.lastStatus = &Status{Enabled: true}
	manager.statusMu.Unlock()

	assert.True(t, manager.IsEncryptionEnabled())
}

func TestGetLastStatus(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	manager := NewManager(DefaultConfig(), logger)

	// Initially should return nil
	assert.Nil(t, manager.GetLastStatus())

	// Set cached status
	expected := &Status{
		Enabled:       true,
		RotationStage: StageStart,
	}
	manager.statusMu.Lock()
	manager.lastStatus = expected
	manager.statusMu.Unlock()

	got := manager.GetLastStatus()
	assert.Equal(t, expected, got)
}

func TestProviderConstants(t *testing.T) {
	assert.Equal(t, Provider("aescbc"), ProviderAESCBC)
	assert.Equal(t, Provider("secretbox"), ProviderSecretBox)
}

func TestRotationStageConstants(t *testing.T) {
	assert.Equal(t, RotationStage("start"), StageStart)
	assert.Equal(t, RotationStage("prepare"), StagePrepare)
	assert.Equal(t, RotationStage("rotate"), StageRotate)
	assert.Equal(t, RotationStage("reencrypt_request"), StageReencryptRequest)
	assert.Equal(t, RotationStage("reencrypt_active"), StageReencryptActive)
	assert.Equal(t, RotationStage("reencrypt_finished"), StageReencryptFinished)
}

func TestBoolToEnabled(t *testing.T) {
	assert.Equal(t, "Enabled", boolToEnabled(true))
	assert.Equal(t, "Disabled", boolToEnabled(false))
}
