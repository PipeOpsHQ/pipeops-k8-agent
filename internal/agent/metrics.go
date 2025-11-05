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

	// Unhealthy duration tracking
	unhealthyDuration  prometheus.Gauge
	lastStateChange    time.Time
	unhealthyStartTime time.Time
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
		unhealthyDuration: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "pipeops_agent_unhealthy_duration_seconds",
			Help: "Duration the agent has been in unhealthy state (disconnected) in seconds",
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
