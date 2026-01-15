package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/sirupsen/logrus"
)

// HandleTCPTunnelStart handles a new TCP tunnel connection request from the gateway
func (a *Agent) HandleTCPTunnelStart(req *controlplane.TCPTunnelStart) error {
	logger := a.logger.WithFields(logrus.Fields{
		"request_id":   req.RequestID,
		"tunnel_id":    req.TunnelID,
		"gateway_port": req.GatewayPort,
		"client_addr":  req.ClientAddr,
	})

	logger.Info("TCP tunnel start requested")

	// Build target address (Istio Gateway in the same cluster)
	// Format: localhost:<gateway_port>
	targetAddr := fmt.Sprintf("localhost:%d", req.GatewayPort)

	// Dial TCP connection to Istio Gateway
	logger.WithField("target", targetAddr).Debug("Dialing Istio Gateway")

	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", targetAddr)
	if err != nil {
		logger.WithError(err).Error("Failed to connect to Istio Gateway")
		return a.sendTCPTunnelClose(req.RequestID, "connection_failed", err.Error())
	}

	// Configure TCP options for optimal performance
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		// Disable Nagle's algorithm for low latency
		if err := tcpConn.SetNoDelay(true); err != nil {
			logger.WithError(err).Warn("Failed to set TCP_NODELAY")
		}

		// Enable TCP keepalive
		if err := tcpConn.SetKeepAlive(true); err != nil {
			logger.WithError(err).Warn("Failed to enable TCP keepalive")
		}

		// Set keepalive period
		if err := tcpConn.SetKeepAlivePeriod(30 * time.Second); err != nil {
			logger.WithError(err).Warn("Failed to set keepalive period")
		}
	}

	logger.Info("Successfully connected to Istio Gateway")

	// Create tunnel object
	tunnel := NewTCPTunnel(req.RequestID, req.TunnelID, req.GatewayPort, conn)

	// Register in tunnel manager
	if err := a.tcpUDPTunnelMgr.RegisterTCPConnection(req.RequestID, tunnel); err != nil {
		logger.WithError(err).Error("Failed to register TCP connection")
		conn.Close()
		return a.sendTCPTunnelClose(req.RequestID, "registration_failed", err.Error())
	}

	// Start bidirectional forwarding
	// Service -> Gateway: reads from service connection, sends to WebSocket
	go a.forwardServiceToGateway(tunnel, logger)
	// Gateway -> Service: handled by HandleTCPTunnelData (data comes via WebSocket)

	logger.Info("TCP tunnel established and forwarding started")

	return nil
}

// HandleTCPTunnelData forwards data from gateway to service
func (a *Agent) HandleTCPTunnelData(requestID string, data []byte) error {
	// Get tunnel
	tunnel, exists := a.tcpUDPTunnelMgr.GetTCPConnection(requestID)
	if !exists {
		a.logger.WithField("request_id", requestID).Warn("TCP tunnel not found for data")
		return fmt.Errorf("tunnel not found: %s", requestID)
	}

	// Update activity timestamp
	tunnel.UpdateActivity()

	// Write to service connection
	n, err := tunnel.ServiceConn.Write(data)
	if err != nil {
		a.logger.WithError(err).WithField("request_id", requestID).Error("Failed to write to service")
		tunnel.Close()
		a.tcpUDPTunnelMgr.RemoveTCPConnection(requestID)
		return a.sendTCPTunnelClose(requestID, "write_error", err.Error())
	}

	// Update metrics
	atomic.AddUint64(&tunnel.BytesSent, uint64(n))

	a.logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"bytes":      n,
	}).Trace("Forwarded data to service")

	return nil
}

// HandleTCPTunnelClose handles a TCP tunnel close request from gateway
func (a *Agent) HandleTCPTunnelClose(requestID string, reason string) error {
	logger := a.logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"reason":     reason,
	})

	logger.Info("TCP tunnel close requested")

	// Get tunnel
	tunnel, exists := a.tcpUDPTunnelMgr.GetTCPConnection(requestID)
	if !exists {
		logger.Warn("TCP tunnel not found")
		return nil
	}

	// Close connection
	tunnel.Close()

	// Remove from manager
	a.tcpUDPTunnelMgr.RemoveTCPConnection(requestID)

	logger.WithFields(logrus.Fields{
		"bytes_sent": tunnel.BytesSent,
		"bytes_recv": tunnel.BytesRecv,
		"duration":   time.Since(tunnel.StartTime).String(),
	}).Info("TCP tunnel closed")

	return nil
}

// forwardServiceToGateway reads from service and forwards to gateway via WebSocket
func (a *Agent) forwardServiceToGateway(tunnel *TCPTunnel, logger *logrus.Entry) {
	defer func() {
		// Ensure tunnel is cleaned up when this goroutine exits
		tunnel.Close()
		a.tcpUDPTunnelMgr.RemoveTCPConnection(tunnel.RequestID)

		// Notify gateway that tunnel is closed
		a.sendTCPTunnelClose(
			tunnel.RequestID,
			"service_closed",
			"",
		)
	}()

	// Allocate buffer (reuse from config)
	bufferSize := 32 * 1024
	if a.config.Tunnels != nil && a.config.Tunnels.TCP.BufferSize > 0 {
		bufferSize = a.config.Tunnels.TCP.BufferSize
	}
	buffer := make([]byte, bufferSize)

	logger.Debug("Started service-to-gateway forwarding")

	for {
		// Check if tunnel context is cancelled
		select {
		case <-tunnel.ctx.Done():
			logger.Debug("Tunnel context cancelled, stopping service-to-gateway forwarding")
			return
		default:
		}

		// Set read deadline for idle timeout
		if a.config.Tunnels != nil && a.config.Tunnels.TCP.IdleTimeout > 0 {
			tunnel.ServiceConn.SetReadDeadline(time.Now().Add(a.config.Tunnels.TCP.IdleTimeout))
		}

		// Read from service
		n, err := tunnel.ServiceConn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				logger.Debug("Service closed connection (EOF)")
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				logger.Debug("Connection idle timeout")
			} else {
				logger.WithError(err).Debug("Error reading from service")
			}
			return
		}

		if n == 0 {
			continue
		}

		// Update activity and metrics
		tunnel.UpdateActivity()
		atomic.AddUint64(&tunnel.BytesRecv, uint64(n))

		// Send to gateway via WebSocket
		if err := a.sendTCPTunnelData(tunnel.RequestID, buffer[:n]); err != nil {
			logger.WithError(err).Error("Failed to send data to gateway")
			return
		}

		logger.WithField("bytes", n).Trace("Forwarded data to gateway")
	}
}

// sendTCPTunnelData sends TCP data to the gateway via WebSocket
func (a *Agent) sendTCPTunnelData(requestID string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Send via WebSocket client
	return a.controlPlane.SendTCPData(ctx, requestID, data)
}

// sendTCPTunnelClose sends a TCP tunnel close message to the gateway
func (a *Agent) sendTCPTunnelClose(requestID string, reason string, errMsg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get tunnel metrics if available
	metrics := make(map[string]interface{})
	if tunnel, exists := a.tcpUDPTunnelMgr.GetTCPConnection(requestID); exists {
		metrics["bytes_sent"] = tunnel.BytesSent
		metrics["bytes_received"] = tunnel.BytesRecv
		metrics["duration_ms"] = time.Since(tunnel.StartTime).Milliseconds()
	}

	// Add error if provided
	if errMsg != "" {
		metrics["error"] = errMsg
	}

	// Send via WebSocket client
	return a.controlPlane.SendTCPClose(ctx, requestID, reason, metrics)
}

// SendTCPData is a convenience method for sending TCP data
func (a *Agent) SendTCPData(requestID string, data []byte) error {
	return a.sendTCPTunnelData(requestID, data)
}

// SendTCPClose is a convenience method for sending TCP close
func (a *Agent) SendTCPClose(requestID string, reason string) error {
	return a.sendTCPTunnelClose(requestID, reason, "")
}

// GetTCPTunnelMetrics returns metrics for a specific TCP tunnel
func (a *Agent) GetTCPTunnelMetrics(requestID string) (map[string]interface{}, error) {
	tunnel, exists := a.tcpUDPTunnelMgr.GetTCPConnection(requestID)
	if !exists {
		return nil, fmt.Errorf("tunnel not found: %s", requestID)
	}

	return map[string]interface{}{
		"request_id":    tunnel.RequestID,
		"tunnel_id":     tunnel.TunnelID,
		"gateway_port":  tunnel.GatewayPort,
		"bytes_sent":    tunnel.BytesSent,
		"bytes_recv":    tunnel.BytesRecv,
		"start_time":    tunnel.StartTime,
		"last_activity": tunnel.LastActivity,
		"duration_ms":   time.Since(tunnel.StartTime).Milliseconds(),
	}, nil
}

// ListActiveTCPTunnels returns a list of all active TCP tunnels
// TODO: Implement by exposing tunnel list from TunnelManager
func (a *Agent) ListActiveTCPTunnels() []map[string]interface{} {
	// Returns nil until TunnelManager exposes its internal tunnel map
	return nil
}

// decodeTCPData decodes base64-encoded TCP data from WebSocket
func decodeTCPData(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}

// encodeTCPData encodes TCP data to base64 for WebSocket transport
func encodeTCPData(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
