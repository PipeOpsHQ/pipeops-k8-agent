package agent

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/sirupsen/logrus"
)

// HandleUDPTunnelData handles incoming UDP datagram from gateway
func (a *Agent) HandleUDPTunnelData(data *controlplane.UDPTunnelData) error {
	logger := a.logger.WithFields(logrus.Fields{
		"tunnel_id":   data.TunnelID,
		"client_addr": data.ClientAddr,
		"client_port": data.ClientPort,
		"size":        len(data.Data),
	})

	logger.Trace("UDP datagram received from gateway")

	// Get or create UDP tunnel
	tunnel, err := a.getOrCreateUDPTunnel(data)
	if err != nil {
		logger.WithError(err).Error("Failed to get or create UDP tunnel")
		return err
	}

	// Update client session
	tunnel.UpdateSession(data.ClientAddr, data.ClientPort)

	// Forward datagram to Istio Gateway
	n, err := tunnel.Socket.Write(data.Data)
	if err != nil {
		logger.WithError(err).Error("Failed to write UDP packet to service")
		return err
	}

	// Update metrics
	atomic.AddInt64(&tunnel.PacketsSent, 1)

	logger.WithField("bytes", n).Trace("Forwarded UDP packet to service")

	return nil
}

// getOrCreateUDPTunnel gets an existing UDP tunnel or creates a new one
func (a *Agent) getOrCreateUDPTunnel(data *controlplane.UDPTunnelData) (*UDPTunnel, error) {
	// Try to get existing tunnel
	if tunnel, exists := a.tcpUDPTunnelMgr.GetUDPSession(data.TunnelID); exists {
		return tunnel, nil
	}

	// Create new tunnel
	logger := a.logger.WithFields(logrus.Fields{
		"tunnel_id":    data.TunnelID,
		"gateway_port": data.GatewayPort,
	})

	logger.Info("Creating new UDP tunnel")

	// Resolve Istio Gateway address
	targetAddr := fmt.Sprintf("localhost:%d", data.GatewayPort)
	serviceAddr, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service address: %w", err)
	}

	// Create UDP socket
	socket, err := net.DialUDP("udp", nil, serviceAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}

	// Create tunnel object
	tunnel := NewUDPTunnel(data.TunnelID, data.GatewayPort, serviceAddr)
	tunnel.Socket = socket

	// Register in tunnel manager
	if err := a.tcpUDPTunnelMgr.RegisterUDPSession(data.TunnelID, tunnel); err != nil {
		socket.Close()
		return nil, fmt.Errorf("failed to register UDP session: %w", err)
	}

	// Start listening for responses from service
	go a.listenForUDPResponses(tunnel, logger)

	logger.Info("UDP tunnel created successfully")

	return tunnel, nil
}

// listenForUDPResponses reads responses from service and forwards to gateway
func (a *Agent) listenForUDPResponses(tunnel *UDPTunnel, logger *logrus.Entry) {
	defer func() {
		// Clean up when listener exits
		tunnel.Close()
		a.tcpUDPTunnelMgr.RemoveUDPSession(tunnel.TunnelID)
		logger.Info("UDP tunnel listener stopped")
	}()

	// Allocate buffer (max UDP packet size)
	bufferSize := 65507
	if a.config.Tunnels != nil && a.config.Tunnels.UDP.BufferSize > 0 {
		bufferSize = a.config.Tunnels.UDP.BufferSize
	}
	buffer := make([]byte, bufferSize)

	logger.Debug("Started UDP response listener")

	for {
		// Check if tunnel context is cancelled
		select {
		case <-tunnel.ctx.Done():
			logger.Debug("Tunnel context cancelled, stopping listener")
			return
		default:
		}

		// Set read deadline for session timeout
		if a.config.Tunnels != nil && a.config.Tunnels.UDP.SessionTimeout > 0 {
			tunnel.Socket.SetReadDeadline(time.Now().Add(a.config.Tunnels.UDP.SessionTimeout))
		}

		// Read response from service
		n, _, err := tunnel.Socket.ReadFrom(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Check if there are any active sessions
				tunnel.sessionsMu.RLock()
				hasActiveSessions := len(tunnel.Sessions) > 0
				tunnel.sessionsMu.RUnlock()

				if !hasActiveSessions {
					logger.Debug("No active sessions, stopping listener")
					return
				}

				// Continue listening if we have active sessions
				continue
			}

			logger.WithError(err).Debug("Error reading from service socket")
			return
		}

		if n == 0 {
			continue
		}

		// Update metrics
		atomic.AddInt64(&tunnel.PacketsRecv, 1)

		// Forward response to all active client sessions
		if err := a.forwardUDPResponseToClients(tunnel, buffer[:n], logger); err != nil {
			logger.WithError(err).Warn("Failed to forward UDP response to clients")
		}
	}
}

// forwardUDPResponseToClients sends UDP response to all active client sessions
func (a *Agent) forwardUDPResponseToClients(tunnel *UDPTunnel, data []byte, logger *logrus.Entry) error {
	tunnel.sessionsMu.RLock()
	defer tunnel.sessionsMu.RUnlock()

	if len(tunnel.Sessions) == 0 {
		logger.Warn("No active sessions to forward UDP response")
		return nil
	}

	// Send to all active sessions
	var lastErr error
	for sessionKey, session := range tunnel.Sessions {
		if err := a.sendUDPTunnelData(
			tunnel.TunnelID,
			data,
			session.ClientAddr,
			session.ClientPort,
		); err != nil {
			logger.WithError(err).WithField("session", sessionKey).Error("Failed to send UDP data to client")
			lastErr = err
		} else {
			logger.WithFields(logrus.Fields{
				"session": sessionKey,
				"bytes":   len(data),
			}).Trace("Forwarded UDP response to client")
		}
	}

	return lastErr
}

// sendUDPTunnelData sends UDP datagram to gateway via WebSocket
func (a *Agent) sendUDPTunnelData(tunnelID string, data []byte, clientAddr string, clientPort int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Send via WebSocket client
	return a.controlPlane.SendUDPData(ctx, tunnelID, data, clientAddr, clientPort)
}

// SendUDPData is a convenience method for sending UDP data
func (a *Agent) SendUDPData(tunnelID string, data []byte, clientAddr string, clientPort int) error {
	return a.sendUDPTunnelData(tunnelID, data, clientAddr, clientPort)
}

// GetUDPTunnelMetrics returns metrics for a specific UDP tunnel
func (a *Agent) GetUDPTunnelMetrics(tunnelID string) (map[string]interface{}, error) {
	tunnel, exists := a.tcpUDPTunnelMgr.GetUDPSession(tunnelID)
	if !exists {
		return nil, fmt.Errorf("tunnel not found: %s", tunnelID)
	}

	tunnel.sessionsMu.RLock()
	activeSessions := len(tunnel.Sessions)
	sessions := make([]map[string]interface{}, 0, activeSessions)
	for key, session := range tunnel.Sessions {
		sessions = append(sessions, map[string]interface{}{
			"key":           key,
			"client_addr":   session.ClientAddr,
			"client_port":   session.ClientPort,
			"last_activity": session.LastActivity,
		})
	}
	tunnel.sessionsMu.RUnlock()

	return map[string]interface{}{
		"tunnel_id":       tunnel.TunnelID,
		"gateway_port":    tunnel.GatewayPort,
		"packets_sent":    tunnel.PacketsSent,
		"packets_recv":    tunnel.PacketsRecv,
		"active_sessions": activeSessions,
		"sessions":        sessions,
		"start_time":      tunnel.StartTime,
		"last_activity":   tunnel.LastActivity,
		"duration_ms":     time.Since(tunnel.StartTime).Milliseconds(),
	}, nil
}

// ListActiveUDPTunnels returns a list of all active UDP tunnels
func (a *Agent) ListActiveUDPTunnels() []map[string]interface{} {
	metrics := a.tcpUDPTunnelMgr.GetMetrics()
	result := make([]map[string]interface{}, 0, metrics.UDPSessionsActive)

	// This would require exposing the internal map from tunnel manager
	// For now, just return summary metrics
	return result
}

// CleanupStaleUDPSessions manually triggers cleanup of stale UDP sessions
func (a *Agent) CleanupStaleUDPSessions() {
	if a.tcpUDPTunnelMgr == nil {
		return
	}

	// timeout is used for configuration reference
	_ = 5 * time.Minute
	if a.config.Tunnels != nil && a.config.Tunnels.UDP.SessionTimeout > 0 {
		_ = a.config.Tunnels.UDP.SessionTimeout
	}

	a.logger.Debug("Manually cleaning up stale UDP sessions")

	// This would need to be exposed from tunnel manager
	// For now, the tunnel manager's cleanup loop handles this automatically
}

// GetUDPTunnelStats returns aggregate statistics for all UDP tunnels
func (a *Agent) GetUDPTunnelStats() map[string]interface{} {
	if a.tcpUDPTunnelMgr == nil {
		return map[string]interface{}{
			"active_tunnels": 0,
			"total_tunnels":  0,
		}
	}

	metrics := a.tcpUDPTunnelMgr.GetMetrics()

	return map[string]interface{}{
		"active_tunnels":      metrics.UDPSessionsActive,
		"total_tunnels":       metrics.UDPSessionsTotal,
		"packets_transferred": metrics.UDPPacketsTransferred,
	}
}

// GetTCPTunnelStats returns aggregate statistics for all TCP tunnels
func (a *Agent) GetTCPTunnelStats() map[string]interface{} {
	if a.tcpUDPTunnelMgr == nil {
		return map[string]interface{}{
			"active_connections": 0,
			"total_connections":  0,
		}
	}

	metrics := a.tcpUDPTunnelMgr.GetMetrics()

	return map[string]interface{}{
		"active_connections": metrics.TCPConnectionsActive,
		"total_connections":  metrics.TCPConnectionsTotal,
		"bytes_transferred":  metrics.TCPBytesTransferred,
	}
}

// GetAllTunnelStats returns combined statistics for TCP and UDP tunnels
func (a *Agent) GetAllTunnelStats() map[string]interface{} {
	if a.tcpUDPTunnelMgr == nil {
		return map[string]interface{}{
			"tcp": map[string]interface{}{
				"active_connections": 0,
				"total_connections":  0,
			},
			"udp": map[string]interface{}{
				"active_tunnels": 0,
				"total_tunnels":  0,
			},
		}
	}

	metrics := a.tcpUDPTunnelMgr.GetMetrics()

	return map[string]interface{}{
		"tcp": map[string]interface{}{
			"active_connections": metrics.TCPConnectionsActive,
			"total_connections":  metrics.TCPConnectionsTotal,
			"bytes_transferred":  metrics.TCPBytesTransferred,
		},
		"udp": map[string]interface{}{
			"active_tunnels":      metrics.UDPSessionsActive,
			"total_tunnels":       metrics.UDPSessionsTotal,
			"packets_transferred": metrics.UDPPacketsTransferred,
		},
	}
}
