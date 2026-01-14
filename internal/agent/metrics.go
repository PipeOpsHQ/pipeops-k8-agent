package agent

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the agent
type Metrics struct {
	// Heartbeat metrics
	heartbeatSuccessTotal     prometheus.Counter
	heartbeatFailuresTotal    prometheus.Counter
	heartbeatSkipReconnecting prometheus.Counter
	heartbeatDuration         prometheus.Histogram

	// Connection state metrics
	connectionState        prometheus.Gauge
	connectionStateChanges *prometheus.CounterVec
	websocketReconnections prometheus.Counter

	// WebSocket proxy metrics
	websocketFramesSent  *prometheus.CounterVec
	websocketFramesRecv  *prometheus.CounterVec
	websocketBytesSent   *prometheus.CounterVec
	websocketBytesRecv   *prometheus.CounterVec
	websocketConnections prometheus.Gauge
	websocketProxyErrors *prometheus.CounterVec

	// Unhealthy duration tracking
	unhealthyDuration  prometheus.Gauge
	lastStateChange    time.Time
	unhealthyStartTime time.Time

	// WebSocket proxy metrics
	wsProxyBytesFromService *prometheus.CounterVec
	wsProxyBytesToService   *prometheus.CounterVec
	wsProxyActiveStreams    prometheus.Gauge
	wsProxyStreamTotal      prometheus.Counter

	// TCP tunnel metrics
	tcpTunnelActiveConnections prometheus.Gauge
	tcpTunnelConnectionsTotal  prometheus.Counter
	tcpTunnelBytesTransferred  *prometheus.CounterVec
	tcpTunnelConnectionErrors  *prometheus.CounterVec
	tcpTunnelConnectionDuration prometheus.Histogram

	// UDP tunnel metrics
	udpTunnelActiveSessions    prometheus.Gauge
	udpTunnelSessionsTotal     prometheus.Counter
	udpTunnelPacketsTransferred *prometheus.CounterVec
	udpTunnelPacketErrors      *prometheus.CounterVec

	// Gateway API watcher metrics
	gatewayWatcherDiscoveredServices prometheus.Gauge
	gatewayWatcherRegistrationErrors prometheus.Counter
	gatewayWatcherSyncTotal          prometheus.Counter
}

// newMetrics creates and registers all agent metrics
func newMetrics() *Metrics {
	return &Metrics{
		heartbeatSuccessTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_heartbeat_success_total",
			Help: "Total number of successful heartbeats sent to control plane",
		}),
		heartbeatFailuresTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_heartbeat_failures_total",
			Help: "Total number of failed heartbeats to control plane",
		}),
		heartbeatSkipReconnecting: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_heartbeat_skip_reconnecting_total",
			Help: "Total number of heartbeats skipped due to reconnecting state",
		}),
		heartbeatDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "pipeops_agent_heartbeat_duration_seconds",
			Help:    "Duration of heartbeat requests in seconds",
			Buckets: prometheus.DefBuckets,
		}),
		connectionState: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_connection_state",
			Help: "Current connection state (0=disconnected, 1=connecting, 2=connected, 3=reconnecting)",
		}),
		connectionStateChanges: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_connection_state_changes_total",
				Help: "Total number of connection state changes by target state",
			},
			[]string{"state"},
		),
		websocketReconnections: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_websocket_reconnections_total",
			Help: "Total number of WebSocket reconnection attempts",
		}),
		websocketFramesSent: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_frames_sent_total",
				Help: "Total number of WebSocket frames sent to controller",
			},
			[]string{"direction"},
		),
		websocketFramesRecv: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_frames_received_total",
				Help: "Total number of WebSocket frames received from controller",
			},
			[]string{"direction"},
		),
		websocketBytesSent: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_bytes_sent_total",
				Help: "Total number of WebSocket bytes sent to controller",
			},
			[]string{"direction"},
		),
		websocketBytesRecv: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_bytes_received_total",
				Help: "Total number of WebSocket bytes received from controller",
			},
			[]string{"direction"},
		),
		websocketConnections: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_websocket_active_connections",
			Help: "Number of active WebSocket proxy connections",
		}),
		websocketProxyErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_proxy_errors_total",
				Help: "Total number of WebSocket proxy errors",
			},
			[]string{"error_type"},
		),
		unhealthyDuration: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_unhealthy_duration_seconds",
			Help: "Duration the agent has been in unhealthy state (disconnected) in seconds",
		}),
		wsProxyBytesFromService: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_proxy_bytes_from_service_total",
				Help: "Total bytes received from backend service WebSocket connections",
			},
			[]string{"namespace", "service"},
		),
		wsProxyBytesToService: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_websocket_proxy_bytes_to_service_total",
				Help: "Total bytes sent to backend service WebSocket connections",
			},
			[]string{"namespace", "service"},
		),
		wsProxyActiveStreams: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_websocket_proxy_active_streams",
			Help: "Number of active WebSocket proxy streams",
		}),
		wsProxyStreamTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_websocket_proxy_streams_total",
			Help: "Total number of WebSocket proxy streams created",
		}),

		// TCP tunnel metrics
		tcpTunnelActiveConnections: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_tcp_tunnel_active_connections",
			Help: "Number of active TCP tunnel connections",
		}),
		tcpTunnelConnectionsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_tcp_tunnel_connections_total",
			Help: "Total number of TCP tunnel connections established",
		}),
		tcpTunnelBytesTransferred: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_tcp_tunnel_bytes_total",
				Help: "Total bytes transferred through TCP tunnels",
			},
			[]string{"direction"}, // "sent" or "received"
		),
		tcpTunnelConnectionErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_tcp_tunnel_errors_total",
				Help: "Total number of TCP tunnel errors",
			},
			[]string{"error_type"}, // "connection_failed", "write_error", "read_error", etc.
		),
		tcpTunnelConnectionDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "pipeops_agent_tcp_tunnel_connection_duration_seconds",
			Help:    "Duration of TCP tunnel connections in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s to ~1h
		}),

		// UDP tunnel metrics
		udpTunnelActiveSessions: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_udp_tunnel_active_sessions",
			Help: "Number of active UDP tunnel sessions",
		}),
		udpTunnelSessionsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_udp_tunnel_sessions_total",
			Help: "Total number of UDP tunnel sessions created",
		}),
		udpTunnelPacketsTransferred: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_udp_tunnel_packets_total",
				Help: "Total packets transferred through UDP tunnels",
			},
			[]string{"direction"}, // "sent" or "received"
		),
		udpTunnelPacketErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pipeops_agent_udp_tunnel_errors_total",
				Help: "Total number of UDP tunnel errors",
			},
			[]string{"error_type"},
		),

		// Gateway API watcher metrics
		gatewayWatcherDiscoveredServices: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_gateway_watcher_discovered_services",
			Help: "Number of TCP/UDP services discovered via Gateway API",
		}),
		gatewayWatcherRegistrationErrors: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_gateway_watcher_registration_errors_total",
			Help: "Total number of tunnel registration errors",
		}),
		gatewayWatcherSyncTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "pipeops_agent_gateway_watcher_sync_total",
			Help: "Total number of gateway watcher sync operations",
		}),

		lastStateChange: time.Now(),
	}
}

// recordHeartbeatSuccess increments the success counter
func (m *Metrics) recordHeartbeatSuccess() {
	m.heartbeatSuccessTotal.Inc()
}

// recordHeartbeatFailure increments the failure counter
func (m *Metrics) recordHeartbeatFailure() {
	m.heartbeatFailuresTotal.Inc()
}

// recordHeartbeatSkip increments the skip counter
func (m *Metrics) recordHeartbeatSkip() {
	m.heartbeatSkipReconnecting.Inc()
}

// recordHeartbeatDuration records the duration of a heartbeat
func (m *Metrics) recordHeartbeatDuration(duration time.Duration) {
	m.heartbeatDuration.Observe(duration.Seconds())
}

// recordConnectionStateChange updates the connection state metrics
func (m *Metrics) recordConnectionStateChange(state ConnectionState) {
	// Update state gauge
	m.connectionState.Set(float64(state))

	// Increment state change counter
	m.connectionStateChanges.WithLabelValues(state.String()).Inc()

	// Track unhealthy duration
	now := time.Now()
	if state == StateDisconnected {
		// Entering unhealthy state
		if m.unhealthyStartTime.IsZero() {
			m.unhealthyStartTime = now
		}
		duration := now.Sub(m.unhealthyStartTime).Seconds()
		m.unhealthyDuration.Set(duration)
	} else {
		// Leaving unhealthy state (or staying healthy)
		if !m.unhealthyStartTime.IsZero() {
			// Was unhealthy, now healthy - reset
			m.unhealthyStartTime = time.Time{}
			m.unhealthyDuration.Set(0)
		}
	}

	m.lastStateChange = now
}

// recordWebSocketReconnection increments the reconnection counter
func (m *Metrics) recordWebSocketReconnection() {
	m.websocketReconnections.Inc()
}

// updateUnhealthyDuration updates the unhealthy duration gauge (called periodically)
func (m *Metrics) updateUnhealthyDuration() {
	if !m.unhealthyStartTime.IsZero() {
		duration := time.Since(m.unhealthyStartTime).Seconds()
		m.unhealthyDuration.Set(duration)
	}
}

// recordWebSocketFrameSent increments the frames sent counter
func (m *Metrics) recordWebSocketFrameSent(direction string, bytes int) {
	m.websocketFramesSent.WithLabelValues(direction).Inc()
	m.websocketBytesSent.WithLabelValues(direction).Add(float64(bytes))
}

// recordWebSocketFrameReceived increments the frames received counter
func (m *Metrics) recordWebSocketFrameReceived(direction string, bytes int) {
	m.websocketFramesRecv.WithLabelValues(direction).Inc()
	m.websocketBytesRecv.WithLabelValues(direction).Add(float64(bytes))
}

// recordWebSocketConnectionStart increments the active connections gauge
func (m *Metrics) recordWebSocketConnectionStart() {
	m.websocketConnections.Inc()
}

// recordWebSocketConnectionEnd decrements the active connections gauge
func (m *Metrics) recordWebSocketConnectionEnd() {
	m.websocketConnections.Dec()
}

// recordWebSocketProxyError increments the proxy error counter
func (m *Metrics) recordWebSocketProxyError(errorType string) {
	m.websocketProxyErrors.WithLabelValues(errorType).Inc()
}

// recordGatewayRouteRefreshError increments the gateway route refresh error counter
func (m *Metrics) recordGatewayRouteRefreshError() {
	// Using existing websocket proxy error counter with specific error type
	m.websocketProxyErrors.WithLabelValues("gateway_route_refresh").Inc()
}

// recordGatewayRouteRefreshSuccess records successful gateway route refresh
func (m *Metrics) recordGatewayRouteRefreshSuccess() {
	// No-op for now, can be extended with specific metrics if needed
}

// recordWebSocketProxyStreamStart increments active streams and total counter
func (m *Metrics) recordWebSocketProxyStreamStart() {
	m.wsProxyActiveStreams.Inc()
	m.wsProxyStreamTotal.Inc()
}

// recordWebSocketProxyStreamEnd decrements active streams
func (m *Metrics) recordWebSocketProxyStreamEnd() {
	m.wsProxyActiveStreams.Dec()
}

// recordWebSocketBytesFromService records bytes received from backend service
func (m *Metrics) recordWebSocketBytesFromService(namespace, service string, bytes int64) {
	m.wsProxyBytesFromService.WithLabelValues(namespace, service).Add(float64(bytes))
}

// recordWebSocketBytesToService records bytes sent to backend service
func (m *Metrics) recordWebSocketBytesToService(namespace, service string, bytes int64) {
	m.wsProxyBytesToService.WithLabelValues(namespace, service).Add(float64(bytes))
}

// TCP Tunnel Metrics

// recordTCPTunnelConnectionStart increments active connections and total counter
func (m *Metrics) recordTCPTunnelConnectionStart() {
	m.tcpTunnelActiveConnections.Inc()
	m.tcpTunnelConnectionsTotal.Inc()
}

// recordTCPTunnelConnectionEnd decrements active connections and records duration
func (m *Metrics) recordTCPTunnelConnectionEnd(duration time.Duration) {
	m.tcpTunnelActiveConnections.Dec()
	m.tcpTunnelConnectionDuration.Observe(duration.Seconds())
}

// recordTCPTunnelBytesSent records bytes sent through TCP tunnel
func (m *Metrics) recordTCPTunnelBytesSent(bytes int64) {
	m.tcpTunnelBytesTransferred.WithLabelValues("sent").Add(float64(bytes))
}

// recordTCPTunnelBytesReceived records bytes received through TCP tunnel
func (m *Metrics) recordTCPTunnelBytesReceived(bytes int64) {
	m.tcpTunnelBytesTransferred.WithLabelValues("received").Add(float64(bytes))
}

// recordTCPTunnelError records TCP tunnel errors
func (m *Metrics) recordTCPTunnelError(errorType string) {
	m.tcpTunnelConnectionErrors.WithLabelValues(errorType).Inc()
}

// UDP Tunnel Metrics

// recordUDPTunnelSessionStart increments active sessions and total counter
func (m *Metrics) recordUDPTunnelSessionStart() {
	m.udpTunnelActiveSessions.Inc()
	m.udpTunnelSessionsTotal.Inc()
}

// recordUDPTunnelSessionEnd decrements active sessions
func (m *Metrics) recordUDPTunnelSessionEnd() {
	m.udpTunnelActiveSessions.Dec()
}

// recordUDPTunnelPacketSent records packets sent through UDP tunnel
func (m *Metrics) recordUDPTunnelPacketSent() {
	m.udpTunnelPacketsTransferred.WithLabelValues("sent").Inc()
}

// recordUDPTunnelPacketReceived records packets received through UDP tunnel
func (m *Metrics) recordUDPTunnelPacketReceived() {
	m.udpTunnelPacketsTransferred.WithLabelValues("received").Inc()
}

// recordUDPTunnelError records UDP tunnel errors
func (m *Metrics) recordUDPTunnelError(errorType string) {
	m.udpTunnelPacketErrors.WithLabelValues(errorType).Inc()
}

// Gateway API Watcher Metrics

// recordGatewayWatcherDiscoveredServices sets the number of discovered services
func (m *Metrics) recordGatewayWatcherDiscoveredServices(count int) {
	m.gatewayWatcherDiscoveredServices.Set(float64(count))
}

// recordGatewayWatcherRegistrationError increments registration error counter
func (m *Metrics) recordGatewayWatcherRegistrationError() {
	m.gatewayWatcherRegistrationErrors.Inc()
}

// recordGatewayWatcherSync increments the sync counter
func (m *Metrics) recordGatewayWatcherSync() {
	m.gatewayWatcherSyncTotal.Inc()
}
