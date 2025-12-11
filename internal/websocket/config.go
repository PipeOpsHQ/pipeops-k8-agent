package websocket

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds WebSocket configuration
type Config struct {
	// Protocol version (v1 or v2)
	Protocol string

	// Channel capacity for buffering frames
	ChannelCapacity int

	// Maximum frame size in bytes
	MaxFrameBytes uint32

	// Allowed origins for dashboard WebSocket (empty = allow all)
	AllowedOrigins []string

	// Enable L4 tunnel mode
	EnableL4Tunnel bool

	// Ping interval for heartbeat
	PingInterval time.Duration

	// Pong timeout (if no pong received)
	PongTimeout time.Duration

	// Backpressure threshold (percentage of channel capacity)
	BackpressureThreshold float64

	// Backpressure window (duration to check backpressure)
	BackpressureWindow time.Duration
}

// DefaultConfig returns default WebSocket configuration
func DefaultConfig() *Config {
	return &Config{
		// Default to legacy v1 for backward compatibility.
		// v2 is opt-in via PIPEOPS_WS_PROTOCOL=v2 once the controller supports it.
		Protocol:              ProtocolV1,
		ChannelCapacity:       100,
		MaxFrameBytes:         1024 * 1024, // 1MB
		AllowedOrigins:        []string{},
		EnableL4Tunnel:        false,
		PingInterval:          30 * time.Second,
		PongTimeout:           90 * time.Second,
		BackpressureThreshold: 0.8, // 80% of channel capacity
		BackpressureWindow:    2 * time.Second,
	}
}

// LoadFromEnv loads configuration from environment variables
func LoadFromEnv() *Config {
	cfg := DefaultConfig()

	if protocol := os.Getenv("PIPEOPS_WS_PROTOCOL"); protocol != "" {
		cfg.Protocol = protocol
	}

	if capacity := os.Getenv("AGENT_WS_CHANNEL_CAPACITY"); capacity != "" {
		if val, err := strconv.Atoi(capacity); err == nil && val > 0 {
			cfg.ChannelCapacity = val
		}
	}

	if maxBytes := os.Getenv("AGENT_MAX_WS_FRAME_BYTES"); maxBytes != "" {
		if val, err := strconv.ParseUint(maxBytes, 10, 32); err == nil && val > 0 {
			cfg.MaxFrameBytes = uint32(val)
		}
	}

	if origins := os.Getenv("AGENT_ALLOWED_WS_ORIGINS"); origins != "" {
		cfg.AllowedOrigins = strings.Split(origins, ",")
		// Trim whitespace from each origin
		for i := range cfg.AllowedOrigins {
			cfg.AllowedOrigins[i] = strings.TrimSpace(cfg.AllowedOrigins[i])
		}
	}

	if enable := os.Getenv("AGENT_ENABLE_L4_TUNNEL"); enable != "" {
		cfg.EnableL4Tunnel = enable == "true" || enable == "1"
	}

	if pingInterval := os.Getenv("AGENT_WS_PING_INTERVAL_SECONDS"); pingInterval != "" {
		if val, err := strconv.Atoi(pingInterval); err == nil && val > 0 {
			cfg.PingInterval = time.Duration(val) * time.Second
		}
	}

	if pongTimeout := os.Getenv("AGENT_WS_PONG_TIMEOUT_SECONDS"); pongTimeout != "" {
		if val, err := strconv.Atoi(pongTimeout); err == nil && val > 0 {
			cfg.PongTimeout = time.Duration(val) * time.Second
		}
	}

	return cfg
}

// IsOriginAllowed checks if the given origin is allowed
// If no origins are configured, all origins are allowed
func (c *Config) IsOriginAllowed(origin string) bool {
	if len(c.AllowedOrigins) == 0 {
		return true
	}

	origin = strings.TrimSpace(origin)
	for _, allowed := range c.AllowedOrigins {
		if allowed == origin || allowed == "*" {
			return true
		}
	}

	return false
}

// ShouldUseV2Protocol returns true if the v2 protocol should be used
func (c *Config) ShouldUseV2Protocol() bool {
	return c.Protocol == ProtocolV2 || c.Protocol == "v2"
}

// ShouldUseV1Protocol returns true if the v1 protocol should be used
func (c *Config) ShouldUseV1Protocol() bool {
	return c.Protocol == ProtocolV1 || c.Protocol == "v1"
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.ChannelCapacity <= 0 {
		c.ChannelCapacity = 100
	}

	if c.MaxFrameBytes == 0 {
		c.MaxFrameBytes = 1024 * 1024
	}

	if c.PingInterval <= 0 {
		c.PingInterval = 30 * time.Second
	}

	if c.PongTimeout <= 0 {
		c.PongTimeout = 90 * time.Second
	}

	if c.BackpressureThreshold <= 0 || c.BackpressureThreshold > 1 {
		c.BackpressureThreshold = 0.8
	}

	if c.BackpressureWindow <= 0 {
		c.BackpressureWindow = 2 * time.Second
	}

	return nil
}
