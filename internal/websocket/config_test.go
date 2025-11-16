package websocket

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, ProtocolV2, cfg.Protocol)
	assert.Equal(t, 100, cfg.ChannelCapacity)
	assert.Equal(t, uint32(1024*1024), cfg.MaxFrameBytes)
	assert.Empty(t, cfg.AllowedOrigins)
	assert.False(t, cfg.EnableL4Tunnel)
	assert.Equal(t, 30*time.Second, cfg.PingInterval)
	assert.Equal(t, 90*time.Second, cfg.PongTimeout)
	assert.Equal(t, 0.8, cfg.BackpressureThreshold)
	assert.Equal(t, 2*time.Second, cfg.BackpressureWindow)
}

func TestLoadFromEnv(t *testing.T) {
	// Save and restore env vars
	defer func() {
		os.Unsetenv("PIPEOPS_WS_PROTOCOL")
		os.Unsetenv("AGENT_WS_CHANNEL_CAPACITY")
		os.Unsetenv("AGENT_MAX_WS_FRAME_BYTES")
		os.Unsetenv("AGENT_ALLOWED_WS_ORIGINS")
		os.Unsetenv("AGENT_ENABLE_L4_TUNNEL")
		os.Unsetenv("AGENT_WS_PING_INTERVAL_SECONDS")
		os.Unsetenv("AGENT_WS_PONG_TIMEOUT_SECONDS")
	}()

	tests := []struct {
		name  string
		setup func()
		check func(*testing.T, *Config)
	}{
		{
			name: "protocol v1",
			setup: func() {
				os.Setenv("PIPEOPS_WS_PROTOCOL", "v1")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "v1", cfg.Protocol)
			},
		},
		{
			name: "custom channel capacity",
			setup: func() {
				os.Setenv("AGENT_WS_CHANNEL_CAPACITY", "200")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 200, cfg.ChannelCapacity)
			},
		},
		{
			name: "custom max frame bytes",
			setup: func() {
				os.Setenv("AGENT_MAX_WS_FRAME_BYTES", "2097152")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, uint32(2097152), cfg.MaxFrameBytes)
			},
		},
		{
			name: "allowed origins",
			setup: func() {
				os.Setenv("AGENT_ALLOWED_WS_ORIGINS", "http://localhost:3000,https://example.com")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, []string{"http://localhost:3000", "https://example.com"}, cfg.AllowedOrigins)
			},
		},
		{
			name: "enable L4 tunnel",
			setup: func() {
				os.Setenv("AGENT_ENABLE_L4_TUNNEL", "true")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.True(t, cfg.EnableL4Tunnel)
			},
		},
		{
			name: "custom ping interval",
			setup: func() {
				os.Setenv("AGENT_WS_PING_INTERVAL_SECONDS", "60")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 60*time.Second, cfg.PingInterval)
			},
		},
		{
			name: "custom pong timeout",
			setup: func() {
				os.Setenv("AGENT_WS_PONG_TIMEOUT_SECONDS", "120")
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 120*time.Second, cfg.PongTimeout)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean env
			os.Unsetenv("PIPEOPS_WS_PROTOCOL")
			os.Unsetenv("AGENT_WS_CHANNEL_CAPACITY")
			os.Unsetenv("AGENT_MAX_WS_FRAME_BYTES")
			os.Unsetenv("AGENT_ALLOWED_WS_ORIGINS")
			os.Unsetenv("AGENT_ENABLE_L4_TUNNEL")
			os.Unsetenv("AGENT_WS_PING_INTERVAL_SECONDS")
			os.Unsetenv("AGENT_WS_PONG_TIMEOUT_SECONDS")

			// Setup test
			tt.setup()

			// Load config
			cfg := LoadFromEnv()

			// Check
			tt.check(t, cfg)
		})
	}
}

func TestConfigIsOriginAllowed(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		origin         string
		expected       bool
	}{
		{
			name:           "no origins configured - allow all",
			allowedOrigins: []string{},
			origin:         "http://example.com",
			expected:       true,
		},
		{
			name:           "exact match",
			allowedOrigins: []string{"http://localhost:3000"},
			origin:         "http://localhost:3000",
			expected:       true,
		},
		{
			name:           "wildcard",
			allowedOrigins: []string{"*"},
			origin:         "http://example.com",
			expected:       true,
		},
		{
			name:           "no match",
			allowedOrigins: []string{"http://localhost:3000"},
			origin:         "http://example.com",
			expected:       false,
		},
		{
			name:           "multiple origins - match second",
			allowedOrigins: []string{"http://localhost:3000", "http://example.com"},
			origin:         "http://example.com",
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{AllowedOrigins: tt.allowedOrigins}
			assert.Equal(t, tt.expected, cfg.IsOriginAllowed(tt.origin))
		})
	}
}

func TestConfigProtocolChecks(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		expectV1 bool
		expectV2 bool
	}{
		{"v1", "v1", true, false},
		{"v2", "v2", false, true},
		{"V1", ProtocolV1, true, false},
		{"V2", ProtocolV2, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Protocol: tt.protocol}
			assert.Equal(t, tt.expectV1, cfg.ShouldUseV1Protocol())
			assert.Equal(t, tt.expectV2, cfg.ShouldUseV2Protocol())
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
		check  func(*testing.T, *Config)
	}{
		{
			name: "fix invalid channel capacity",
			config: &Config{
				ChannelCapacity: 0,
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 100, cfg.ChannelCapacity)
			},
		},
		{
			name: "fix invalid max frame bytes",
			config: &Config{
				MaxFrameBytes: 0,
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, uint32(1024*1024), cfg.MaxFrameBytes)
			},
		},
		{
			name: "fix invalid ping interval",
			config: &Config{
				PingInterval: 0,
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 30*time.Second, cfg.PingInterval)
			},
		},
		{
			name: "fix invalid pong timeout",
			config: &Config{
				PongTimeout: 0,
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 90*time.Second, cfg.PongTimeout)
			},
		},
		{
			name: "fix invalid backpressure threshold - zero",
			config: &Config{
				BackpressureThreshold: 0,
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 0.8, cfg.BackpressureThreshold)
			},
		},
		{
			name: "fix invalid backpressure threshold - over 1",
			config: &Config{
				BackpressureThreshold: 1.5,
			},
			check: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 0.8, cfg.BackpressureThreshold)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			assert.NoError(t, err)
			tt.check(t, tt.config)
		})
	}
}
