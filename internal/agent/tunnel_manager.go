package agent

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// TunnelManager manages active TCP and UDP tunnel connections
type TunnelManager struct {
	logger *logrus.Logger

	// TCP connection tracking
	tcpConnections map[string]*TCPTunnel // requestID -> TCPTunnel
	tcpMu          sync.RWMutex

	// UDP session tracking
	udpSessions map[string]*UDPTunnel // tunnelID -> UDPTunnel
	udpMu       sync.RWMutex

	// Metrics
	tcpConnectionsTotal   uint64
	tcpBytesTransferred   uint64
	udpSessionsTotal      uint64
	udpPacketsTransferred uint64

	// Configuration
	config *TunnelManagerConfig

	// Shutdown
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// TunnelManagerConfig contains configuration for the tunnel manager
type TunnelManagerConfig struct {
	// TCP settings
	TCPBufferSize        int
	TCPKeepalive         bool
	TCPKeepalivePeriod   time.Duration
	TCPConnectionTimeout time.Duration
	TCPIdleTimeout       time.Duration
	TCPMaxConnections    int

	// UDP settings
	UDPBufferSize     int
	UDPSessionTimeout time.Duration
	UDPMaxSessions    int

	// Cleanup intervals
	CleanupInterval time.Duration
}

// DefaultTunnelManagerConfig returns default configuration
func DefaultTunnelManagerConfig() *TunnelManagerConfig {
	return &TunnelManagerConfig{
		TCPBufferSize:        32 * 1024, // 32KB
		TCPKeepalive:         true,
		TCPKeepalivePeriod:   30 * time.Second,
		TCPConnectionTimeout: 30 * time.Second,
		TCPIdleTimeout:       5 * time.Minute,
		TCPMaxConnections:    1000,
		UDPBufferSize:        65507, // Max UDP packet size
		UDPSessionTimeout:    5 * time.Minute,
		UDPMaxSessions:       1000,
		CleanupInterval:      1 * time.Minute,
	}
}

// NewTunnelManager creates a new tunnel manager
func NewTunnelManager(logger *logrus.Logger, config *TunnelManagerConfig) *TunnelManager {
	if config == nil {
		config = DefaultTunnelManagerConfig()
	}

	return &TunnelManager{
		logger:         logger,
		tcpConnections: make(map[string]*TCPTunnel),
		udpSessions:    make(map[string]*UDPTunnel),
		config:         config,
		stopCh:         make(chan struct{}),
	}
}

// Start begins the tunnel manager background tasks
func (tm *TunnelManager) Start(ctx context.Context) {
	tm.logger.Info("Starting tunnel manager")

	// Start cleanup goroutine
	tm.wg.Add(1)
	go tm.cleanupLoop(ctx)
}

// Stop gracefully shuts down the tunnel manager
func (tm *TunnelManager) Stop() {
	tm.logger.Info("Stopping tunnel manager")
	close(tm.stopCh)

	// Close all TCP connections
	tm.tcpMu.Lock()
	for requestID, tunnel := range tm.tcpConnections {
		tm.logger.WithField("request_id", requestID).Debug("Closing TCP tunnel")
		tunnel.Close()
		delete(tm.tcpConnections, requestID)
	}
	tm.tcpMu.Unlock()

	// Close all UDP sessions
	tm.udpMu.Lock()
	for tunnelID, tunnel := range tm.udpSessions {
		tm.logger.WithField("tunnel_id", tunnelID).Debug("Closing UDP tunnel")
		tunnel.Close()
		delete(tm.udpSessions, tunnelID)
	}
	tm.udpMu.Unlock()

	tm.wg.Wait()
	tm.logger.Info("Tunnel manager stopped")
}

// RegisterTCPConnection registers a new TCP tunnel connection
func (tm *TunnelManager) RegisterTCPConnection(requestID string, tunnel *TCPTunnel) error {
	tm.tcpMu.Lock()
	defer tm.tcpMu.Unlock()

	// Check max connections
	if len(tm.tcpConnections) >= tm.config.TCPMaxConnections {
		return fmt.Errorf("max TCP connections reached: %d", tm.config.TCPMaxConnections)
	}

	// Check for duplicate
	if _, exists := tm.tcpConnections[requestID]; exists {
		return fmt.Errorf("TCP connection already exists: %s", requestID)
	}

	tm.tcpConnections[requestID] = tunnel
	atomic.AddUint64(&tm.tcpConnectionsTotal, 1)

	tm.logger.WithFields(logrus.Fields{
		"request_id":   requestID,
		"gateway_port": tunnel.GatewayPort,
		"active_count": len(tm.tcpConnections),
	}).Debug("TCP connection registered")

	return nil
}

// GetTCPConnection retrieves a TCP tunnel by request ID
func (tm *TunnelManager) GetTCPConnection(requestID string) (*TCPTunnel, bool) {
	tm.tcpMu.RLock()
	defer tm.tcpMu.RUnlock()

	tunnel, exists := tm.tcpConnections[requestID]
	return tunnel, exists
}

// RemoveTCPConnection removes a TCP tunnel connection
func (tm *TunnelManager) RemoveTCPConnection(requestID string) {
	tm.tcpMu.Lock()
	defer tm.tcpMu.Unlock()

	if tunnel, exists := tm.tcpConnections[requestID]; exists {
		delete(tm.tcpConnections, requestID)

		// Update metrics
		atomic.AddUint64(&tm.tcpBytesTransferred, tunnel.BytesSent+tunnel.BytesRecv)

		tm.logger.WithFields(logrus.Fields{
			"request_id":   requestID,
			"bytes_sent":   tunnel.BytesSent,
			"bytes_recv":   tunnel.BytesRecv,
			"duration":     time.Since(tunnel.StartTime).String(),
			"active_count": len(tm.tcpConnections),
		}).Debug("TCP connection removed")
	}
}

// RegisterUDPSession registers a new UDP tunnel session
func (tm *TunnelManager) RegisterUDPSession(tunnelID string, tunnel *UDPTunnel) error {
	tm.udpMu.Lock()
	defer tm.udpMu.Unlock()

	// Check max sessions
	if len(tm.udpSessions) >= tm.config.UDPMaxSessions {
		return fmt.Errorf("max UDP sessions reached: %d", tm.config.UDPMaxSessions)
	}

	// Reuse existing tunnel or create new
	if _, exists := tm.udpSessions[tunnelID]; !exists {
		tm.udpSessions[tunnelID] = tunnel
		atomic.AddUint64(&tm.udpSessionsTotal, 1)

		tm.logger.WithFields(logrus.Fields{
			"tunnel_id":    tunnelID,
			"gateway_port": tunnel.GatewayPort,
			"active_count": len(tm.udpSessions),
		}).Debug("UDP session registered")
	}

	return nil
}

// GetUDPSession retrieves a UDP tunnel by tunnel ID
func (tm *TunnelManager) GetUDPSession(tunnelID string) (*UDPTunnel, bool) {
	tm.udpMu.RLock()
	defer tm.udpMu.RUnlock()

	tunnel, exists := tm.udpSessions[tunnelID]
	return tunnel, exists
}

// RemoveUDPSession removes a UDP tunnel session
func (tm *TunnelManager) RemoveUDPSession(tunnelID string) {
	tm.udpMu.Lock()
	defer tm.udpMu.Unlock()

	if tunnel, exists := tm.udpSessions[tunnelID]; exists {
		delete(tm.udpSessions, tunnelID)

		// Update metrics
		atomic.AddUint64(&tm.udpPacketsTransferred, uint64(tunnel.PacketsSent+tunnel.PacketsRecv))

		tm.logger.WithFields(logrus.Fields{
			"tunnel_id":    tunnelID,
			"packets_sent": tunnel.PacketsSent,
			"packets_recv": tunnel.PacketsRecv,
			"sessions":     len(tunnel.Sessions),
			"duration":     time.Since(tunnel.StartTime).String(),
			"active_count": len(tm.udpSessions),
		}).Debug("UDP session removed")
	}
}

// GetMetrics returns current tunnel metrics
func (tm *TunnelManager) GetMetrics() TunnelMetrics {
	tm.tcpMu.RLock()
	activeTCP := len(tm.tcpConnections)
	tm.tcpMu.RUnlock()

	tm.udpMu.RLock()
	activeUDP := len(tm.udpSessions)
	tm.udpMu.RUnlock()

	return TunnelMetrics{
		TCPConnectionsActive:  activeTCP,
		TCPConnectionsTotal:   atomic.LoadUint64(&tm.tcpConnectionsTotal),
		TCPBytesTransferred:   atomic.LoadUint64(&tm.tcpBytesTransferred),
		UDPSessionsActive:     activeUDP,
		UDPSessionsTotal:      atomic.LoadUint64(&tm.udpSessionsTotal),
		UDPPacketsTransferred: atomic.LoadUint64(&tm.udpPacketsTransferred),
	}
}

// cleanupLoop periodically cleans up idle connections and sessions
func (tm *TunnelManager) cleanupLoop(ctx context.Context) {
	defer tm.wg.Done()

	ticker := time.NewTicker(tm.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.cleanupIdleTCP()
			tm.cleanupStaleUDP()

		case <-ctx.Done():
			return

		case <-tm.stopCh:
			return
		}
	}
}

// cleanupIdleTCP closes idle TCP connections
func (tm *TunnelManager) cleanupIdleTCP() {
	tm.tcpMu.Lock()
	defer tm.tcpMu.Unlock()

	now := time.Now()
	for requestID, tunnel := range tm.tcpConnections {
		if tunnel.IsIdle(tm.config.TCPIdleTimeout) {
			tm.logger.WithFields(logrus.Fields{
				"request_id": requestID,
				"idle_time":  now.Sub(tunnel.LastActivity).String(),
			}).Info("Closing idle TCP connection")

			tunnel.Close()
			delete(tm.tcpConnections, requestID)
		}
	}
}

// cleanupStaleUDP removes stale UDP sessions
func (tm *TunnelManager) cleanupStaleUDP() {
	tm.udpMu.Lock()
	defer tm.udpMu.Unlock()

	for tunnelID, tunnel := range tm.udpSessions {
		// Clean up stale client sessions within the tunnel
		tunnel.CleanupStaleSessions(tm.config.UDPSessionTimeout)

		// Remove tunnel if no active sessions
		if len(tunnel.Sessions) == 0 && time.Since(tunnel.LastActivity) > tm.config.UDPSessionTimeout {
			tm.logger.WithField("tunnel_id", tunnelID).Info("Removing UDP tunnel with no active sessions")
			tunnel.Close()
			delete(tm.udpSessions, tunnelID)
		}
	}
}

// TCPTunnel represents an active TCP tunnel connection
type TCPTunnel struct {
	RequestID   string
	TunnelID    string
	GatewayPort int32

	// Network connection
	ServiceConn net.Conn

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Timing
	StartTime    time.Time
	LastActivity time.Time
	activityMu   sync.RWMutex

	// Metrics
	BytesSent uint64
	BytesRecv uint64
}

// NewTCPTunnel creates a new TCP tunnel
func NewTCPTunnel(requestID, tunnelID string, gatewayPort int32, conn net.Conn) *TCPTunnel {
	ctx, cancel := context.WithCancel(context.Background())

	return &TCPTunnel{
		RequestID:    requestID,
		TunnelID:     tunnelID,
		GatewayPort:  gatewayPort,
		ServiceConn:  conn,
		ctx:          ctx,
		cancel:       cancel,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
	}
}

// UpdateActivity updates the last activity timestamp
func (t *TCPTunnel) UpdateActivity() {
	t.activityMu.Lock()
	t.LastActivity = time.Now()
	t.activityMu.Unlock()
}

// IsIdle checks if the connection has been idle for longer than the timeout
func (t *TCPTunnel) IsIdle(timeout time.Duration) bool {
	t.activityMu.RLock()
	defer t.activityMu.RUnlock()
	return time.Since(t.LastActivity) > timeout
}

// Close closes the TCP tunnel
func (t *TCPTunnel) Close() error {
	t.cancel()
	if t.ServiceConn != nil {
		return t.ServiceConn.Close()
	}
	return nil
}

// UDPTunnel represents an active UDP tunnel
type UDPTunnel struct {
	TunnelID    string
	GatewayPort int32

	// Network socket
	Socket      *net.UDPConn
	ServiceAddr *net.UDPAddr

	// Client sessions (clientAddr:port -> session)
	Sessions   map[string]*UDPSession
	sessionsMu sync.RWMutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Timing
	StartTime    time.Time
	LastActivity time.Time
	activityMu   sync.RWMutex

	// Metrics (use int64 for atomic operations)
	PacketsSent int64
	PacketsRecv int64
}

// NewUDPTunnel creates a new UDP tunnel
func NewUDPTunnel(tunnelID string, gatewayPort int32, serviceAddr *net.UDPAddr) *UDPTunnel {
	ctx, cancel := context.WithCancel(context.Background())

	return &UDPTunnel{
		TunnelID:     tunnelID,
		GatewayPort:  gatewayPort,
		ServiceAddr:  serviceAddr,
		Sessions:     make(map[string]*UDPSession),
		ctx:          ctx,
		cancel:       cancel,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
	}
}

// UpdateSession updates or creates a client session
func (t *UDPTunnel) UpdateSession(clientAddr string, clientPort int) {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	key := fmt.Sprintf("%s:%d", clientAddr, clientPort)
	if session, exists := t.Sessions[key]; exists {
		session.LastActivity = time.Now()
	} else {
		t.Sessions[key] = &UDPSession{
			ClientAddr:   clientAddr,
			ClientPort:   clientPort,
			LastActivity: time.Now(),
		}
	}

	t.updateActivity()
}

// CleanupStaleSessions removes sessions that haven't been active
func (t *UDPTunnel) CleanupStaleSessions(timeout time.Duration) {
	t.sessionsMu.Lock()
	defer t.sessionsMu.Unlock()

	now := time.Now()
	for key, session := range t.Sessions {
		if now.Sub(session.LastActivity) > timeout {
			delete(t.Sessions, key)
		}
	}
}

// updateActivity updates the last activity timestamp (internal, assumes lock held)
func (t *UDPTunnel) updateActivity() {
	t.activityMu.Lock()
	t.LastActivity = time.Now()
	t.activityMu.Unlock()
}

// Close closes the UDP tunnel
func (t *UDPTunnel) Close() error {
	t.cancel()
	if t.Socket != nil {
		return t.Socket.Close()
	}
	return nil
}

// UDPSession represents a client UDP session
type UDPSession struct {
	ClientAddr   string
	ClientPort   int
	LastActivity time.Time
}

// TunnelMetrics contains tunnel statistics
type TunnelMetrics struct {
	TCPConnectionsActive  int
	TCPConnectionsTotal   uint64
	TCPBytesTransferred   uint64
	UDPSessionsActive     int
	UDPSessionsTotal      uint64
	UDPPacketsTransferred uint64
}
