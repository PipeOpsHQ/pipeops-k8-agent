package tunnel

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	logger := logrus.New()

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "5m",
		Forwards: []PortForwardConfig{
			{Name: "k8s-api", LocalAddr: "localhost:6443"},
			{Name: "kubelet", LocalAddr: "localhost:10250"},
		},
	}

	manager, err := NewManager(config, logger)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	assert.NotNil(t, manager.pollService)
	assert.NotNil(t, manager.logger)
}

func TestNewManager_InvalidPollInterval(t *testing.T) {
	logger := logrus.New()

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "invalid",
		InactivityTimeout: "5m",
	}

	manager, err := NewManager(config, logger)
	assert.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "invalid poll interval")
}

func TestNewManager_InvalidInactivityTimeout(t *testing.T) {
	logger := logrus.New()

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "invalid",
	}

	manager, err := NewManager(config, logger)
	assert.Error(t, err)
	assert.Nil(t, manager)
	assert.Contains(t, err.Error(), "invalid inactivity timeout")
}

func TestNewManager_DefaultValues(t *testing.T) {
	logger := logrus.New()

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "", // Empty, should use default
		InactivityTimeout: "", // Empty, should use default
	}

	manager, err := NewManager(config, logger)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	// Defaults should be applied by parseDuration
}

func TestManager_IsTunnelOpen_NilClient(t *testing.T) {
	logger := logrus.New()

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "5m",
	}

	manager, err := NewManager(config, logger)
	require.NoError(t, err)

	// Client should be nil initially
	isOpen := manager.IsTunnelOpen()
	assert.False(t, isOpen)
}

func TestManager_RecordActivity(t *testing.T) {
	logger := logrus.New()

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "5m",
	}

	manager, err := NewManager(config, logger)
	require.NoError(t, err)

	// Should not panic when called
	assert.NotPanics(t, func() {
		manager.RecordActivity()
	})
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		defaultValue string
		expected     time.Duration
		wantErr      bool
	}{
		{
			name:         "valid duration",
			value:        "10s",
			defaultValue: "5s",
			expected:     10 * time.Second,
			wantErr:      false,
		},
		{
			name:         "empty value uses default",
			value:        "",
			defaultValue: "5s",
			expected:     5 * time.Second,
			wantErr:      false,
		},
		{
			name:         "invalid duration",
			value:        "invalid",
			defaultValue: "5s",
			expected:     0,
			wantErr:      true,
		},
		{
			name:         "minutes",
			value:        "5m",
			defaultValue: "1m",
			expected:     5 * time.Minute,
			wantErr:      false,
		},
		{
			name:         "hours",
			value:        "2h",
			defaultValue: "1h",
			expected:     2 * time.Hour,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseDuration(tt.value, tt.defaultValue)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestManagerConfig_Structure(t *testing.T) {
	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "5m",
		Forwards: []PortForwardConfig{
			{Name: "test", LocalAddr: "localhost:8080"},
		},
	}

	assert.Equal(t, "https://api.test.pipeops.io", config.ControlPlaneURL)
	assert.Equal(t, "test-agent", config.AgentID)
	assert.Equal(t, "test-token", config.Token)
	assert.Len(t, config.Forwards, 1)
	assert.Equal(t, "test", config.Forwards[0].Name)
}

func TestPortForwardConfig_Structure(t *testing.T) {
	config := PortForwardConfig{
		Name:      "kubernetes-api",
		LocalAddr: "localhost:6443",
	}

	assert.Equal(t, "kubernetes-api", config.Name)
	assert.Equal(t, "localhost:6443", config.LocalAddr)
}

func TestManager_Stop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "5m",
	}

	manager, err := NewManager(config, logger)
	require.NoError(t, err)

	// Stop should not panic even if not started
	err = manager.Stop()
	assert.NoError(t, err)
}

func TestManager_StartStop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise

	config := &ManagerConfig{
		ControlPlaneURL:   "https://api.test.pipeops.io",
		AgentID:           "test-agent-123",
		Token:             "test-token",
		PollInterval:      "5s",
		InactivityTimeout: "5m",
	}

	manager, err := NewManager(config, logger)
	require.NoError(t, err)

	// Start manager (will fail to connect but shouldn't error)
	err = manager.Start()
	assert.NoError(t, err)

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Stop manager
	err = manager.Stop()
	assert.NoError(t, err)
}
