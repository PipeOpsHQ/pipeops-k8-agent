package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTunnelManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	t.Run("with default config", func(t *testing.T) {
		tm := NewTunnelManager(logger, nil)
		require.NotNil(t, tm)
		assert.NotNil(t, tm.tcpConnections)
		assert.NotNil(t, tm.udpSessions)
		assert.NotNil(t, tm.config)
		assert.Equal(t, 32*1024, tm.config.TCPBufferSize)
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &TunnelManagerConfig{
			TCPBufferSize:     64 * 1024,
			TCPMaxConnections: 500,
			UDPMaxSessions:    500,
		}
		tm := NewTunnelManager(logger, config)
		require.NotNil(t, tm)
		assert.Equal(t, 64*1024, tm.config.TCPBufferSize)
		assert.Equal(t, 500, tm.config.TCPMaxConnections)
	})
}

func TestTunnelManager_TCPOperations(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tm := NewTunnelManager(logger, DefaultTunnelManagerConfig())

	t.Run("register TCP connection", func(t *testing.T) {
		// Create a mock connection
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("req-1", "tunnel-1", 5432, client)
		err := tm.RegisterTCPConnection("req-1", tunnel)
		require.NoError(t, err)

		// Verify it's registered
		retrieved, exists := tm.GetTCPConnection("req-1")
		assert.True(t, exists)
		assert.Equal(t, tunnel, retrieved)
	})

	t.Run("get non-existent connection", func(t *testing.T) {
		_, exists := tm.GetTCPConnection("non-existent")
		assert.False(t, exists)
	})

	t.Run("remove TCP connection", func(t *testing.T) {
		// Create and register
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("req-2", "tunnel-2", 5433, client)
		err := tm.RegisterTCPConnection("req-2", tunnel)
		require.NoError(t, err)

		// Remove it
		tm.RemoveTCPConnection("req-2")

		// Verify it's gone
		_, exists := tm.GetTCPConnection("req-2")
		assert.False(t, exists)
	})

	t.Run("duplicate registration fails", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("req-dup", "tunnel-dup", 5434, client)
		err := tm.RegisterTCPConnection("req-dup", tunnel)
		require.NoError(t, err)

		// Try to register again
		err = tm.RegisterTCPConnection("req-dup", tunnel)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("max connections limit", func(t *testing.T) {
		// Use a tiny limit for testing
		config := &TunnelManagerConfig{
			TCPMaxConnections: 2,
			UDPMaxSessions:    100,
			CleanupInterval:   time.Hour, // Don't cleanup during test
		}
		smallTM := NewTunnelManager(logger, config)

		// Register 2 connections
		for i := 0; i < 2; i++ {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()

			tunnel := NewTCPTunnel("max-req-"+string(rune('a'+i)), "tunnel", 5440+int32(i), client)
			err := smallTM.RegisterTCPConnection("max-req-"+string(rune('a'+i)), tunnel)
			require.NoError(t, err)
		}

		// Third should fail
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("max-req-c", "tunnel", 5442, client)
		err := smallTM.RegisterTCPConnection("max-req-c", tunnel)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "max TCP connections")
	})
}

func TestTunnelManager_UDPOperations(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tm := NewTunnelManager(logger, DefaultTunnelManagerConfig())

	t.Run("register UDP session", func(t *testing.T) {
		addr, _ := net.ResolveUDPAddr("udp", "localhost:5353")
		tunnel := NewUDPTunnel("udp-1", 5353, addr)
		err := tm.RegisterUDPSession("udp-1", tunnel)
		require.NoError(t, err)

		// Verify it's registered
		retrieved, exists := tm.GetUDPSession("udp-1")
		assert.True(t, exists)
		assert.Equal(t, tunnel, retrieved)
	})

	t.Run("get non-existent session", func(t *testing.T) {
		_, exists := tm.GetUDPSession("non-existent")
		assert.False(t, exists)
	})

	t.Run("remove UDP session", func(t *testing.T) {
		addr, _ := net.ResolveUDPAddr("udp", "localhost:5354")
		tunnel := NewUDPTunnel("udp-2", 5354, addr)
		err := tm.RegisterUDPSession("udp-2", tunnel)
		require.NoError(t, err)

		// Remove it
		tm.RemoveUDPSession("udp-2")

		// Verify it's gone
		_, exists := tm.GetUDPSession("udp-2")
		assert.False(t, exists)
	})
}

func TestTunnelManager_Metrics(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tm := NewTunnelManager(logger, DefaultTunnelManagerConfig())

	// Register some connections
	for i := 0; i < 3; i++ {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("metrics-req-"+string(rune('a'+i)), "tunnel", 5450+int32(i), client)
		_ = tm.RegisterTCPConnection("metrics-req-"+string(rune('a'+i)), tunnel)
	}

	addr, _ := net.ResolveUDPAddr("udp", "localhost:5355")
	udpTunnel := NewUDPTunnel("udp-metrics", 5355, addr)
	_ = tm.RegisterUDPSession("udp-metrics", udpTunnel)

	metrics := tm.GetMetrics()
	assert.Equal(t, 3, metrics.TCPConnectionsActive)
	assert.Equal(t, uint64(3), metrics.TCPConnectionsTotal)
	assert.Equal(t, 1, metrics.UDPSessionsActive)
	assert.Equal(t, uint64(1), metrics.UDPSessionsTotal)
}

func TestTunnelManager_StartStop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tm := NewTunnelManager(logger, DefaultTunnelManagerConfig())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should not panic
	assert.NotPanics(t, func() {
		tm.Start(ctx)
	})

	// Register a connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	tunnel := NewTCPTunnel("stop-test", "tunnel", 5460, client)
	_ = tm.RegisterTCPConnection("stop-test", tunnel)

	// Stop should close all connections and not panic
	assert.NotPanics(t, func() {
		tm.Stop()
	})

	// After stop, metrics should show 0 active connections
	metrics := tm.GetMetrics()
	assert.Equal(t, 0, metrics.TCPConnectionsActive)
}

func TestTCPTunnel(t *testing.T) {
	t.Run("new tunnel", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("req-1", "tunnel-1", 5432, client)
		assert.Equal(t, "req-1", tunnel.RequestID)
		assert.Equal(t, "tunnel-1", tunnel.TunnelID)
		assert.Equal(t, int32(5432), tunnel.GatewayPort)
		assert.NotNil(t, tunnel.ServiceConn)
	})

	t.Run("update activity", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("req-1", "tunnel-1", 5432, client)
		oldActivity := tunnel.LastActivity

		time.Sleep(10 * time.Millisecond)
		tunnel.UpdateActivity()

		assert.True(t, tunnel.LastActivity.After(oldActivity))
	})

	t.Run("is idle", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()

		tunnel := NewTCPTunnel("req-1", "tunnel-1", 5432, client)

		// Should not be idle immediately
		assert.False(t, tunnel.IsIdle(1*time.Second))

		// Wait and check
		time.Sleep(100 * time.Millisecond)
		assert.True(t, tunnel.IsIdle(50*time.Millisecond))
		assert.False(t, tunnel.IsIdle(1*time.Second))
	})

	t.Run("close", func(t *testing.T) {
		server, client := net.Pipe()
		defer server.Close()

		tunnel := NewTCPTunnel("req-1", "tunnel-1", 5432, client)
		err := tunnel.Close()
		assert.NoError(t, err)
	})
}

func TestUDPTunnel(t *testing.T) {
	t.Run("new tunnel", func(t *testing.T) {
		addr, _ := net.ResolveUDPAddr("udp", "localhost:5353")
		tunnel := NewUDPTunnel("udp-1", 5353, addr)

		assert.Equal(t, "udp-1", tunnel.TunnelID)
		assert.Equal(t, int32(5353), tunnel.GatewayPort)
		assert.NotNil(t, tunnel.Sessions)
		assert.Equal(t, 0, len(tunnel.Sessions))
	})

	t.Run("update session", func(t *testing.T) {
		addr, _ := net.ResolveUDPAddr("udp", "localhost:5353")
		tunnel := NewUDPTunnel("udp-1", 5353, addr)

		tunnel.UpdateSession("192.168.1.1", 12345)
		assert.Equal(t, 1, len(tunnel.Sessions))

		// Update same session
		tunnel.UpdateSession("192.168.1.1", 12345)
		assert.Equal(t, 1, len(tunnel.Sessions))

		// Add new session
		tunnel.UpdateSession("192.168.1.2", 12346)
		assert.Equal(t, 2, len(tunnel.Sessions))
	})

	t.Run("cleanup stale sessions", func(t *testing.T) {
		addr, _ := net.ResolveUDPAddr("udp", "localhost:5353")
		tunnel := NewUDPTunnel("udp-1", 5353, addr)

		// Add sessions
		tunnel.UpdateSession("192.168.1.1", 12345)
		tunnel.UpdateSession("192.168.1.2", 12346)
		assert.Equal(t, 2, len(tunnel.Sessions))

		// Wait a bit
		time.Sleep(100 * time.Millisecond)

		// Cleanup with short timeout
		tunnel.CleanupStaleSessions(50 * time.Millisecond)
		assert.Equal(t, 0, len(tunnel.Sessions))
	})
}

func TestDefaultTunnelManagerConfig(t *testing.T) {
	config := DefaultTunnelManagerConfig()

	assert.Equal(t, 32*1024, config.TCPBufferSize)
	assert.True(t, config.TCPKeepalive)
	assert.Equal(t, 30*time.Second, config.TCPKeepalivePeriod)
	assert.Equal(t, 30*time.Second, config.TCPConnectionTimeout)
	assert.Equal(t, 5*time.Minute, config.TCPIdleTimeout)
	assert.Equal(t, 1000, config.TCPMaxConnections)
	assert.Equal(t, 65507, config.UDPBufferSize)
	assert.Equal(t, 5*time.Minute, config.UDPSessionTimeout)
	assert.Equal(t, 1000, config.UDPMaxSessions)
	assert.Equal(t, 1*time.Minute, config.CleanupInterval)
}
