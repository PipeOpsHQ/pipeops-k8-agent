package websocket

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds WebSocket-specific Prometheus metrics
type Metrics struct {
	// Frame metrics
	framesTotal     *prometheus.CounterVec
	frameBytesTotal *prometheus.CounterVec
	frameBytes      prometheus.Histogram

	// Drop and error metrics
	droppedFramesTotal *prometheus.CounterVec
	backpressureEvents prometheus.Counter

	// Session metrics
	sessionDuration   prometheus.Histogram
	activeSessions    prometheus.Gauge
	sessionsTotal     prometheus.Counter
	sessionCloseTotal *prometheus.CounterVec
}

// NewMetrics creates a new WebSocket metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		framesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_websocket_frames_total",
				Help: "Total number of WebSocket frames by direction and type",
			},
			[]string{"direction", "type"},
		),
		frameBytesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_websocket_frame_bytes_total",
				Help: "Total bytes transferred in WebSocket frames by direction",
			},
			[]string{"direction"},
		),
		frameBytes: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "agent_websocket_frame_bytes_histogram",
				Help:    "Distribution of WebSocket frame sizes in bytes",
				Buckets: prometheus.ExponentialBuckets(64, 2, 16), // 64B to 2MB
			},
		),
		droppedFramesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_websocket_dropped_frames_total",
				Help: "Total number of dropped frames by reason",
			},
			[]string{"reason"},
		),
		backpressureEvents: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "agent_websocket_backpressure_events_total",
				Help: "Total number of backpressure events",
			},
		),
		sessionDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "agent_websocket_session_duration_seconds",
				Help:    "Duration of WebSocket sessions in seconds",
				Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s to ~68 minutes
			},
		),
		activeSessions: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "agent_websocket_active_sessions",
				Help: "Number of currently active WebSocket sessions",
			},
		),
		sessionsTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "agent_websocket_sessions_total",
				Help: "Total number of WebSocket sessions started",
			},
		),
		sessionCloseTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "agent_websocket_session_close_total",
				Help: "Total number of session closures by code",
			},
			[]string{"code"},
		),
	}
}

// RecordFrameSent records a sent frame
func (m *Metrics) RecordFrameSent(frameType byte, size int) {
	typeName := frameTypeName(frameType)
	m.framesTotal.WithLabelValues("sent", typeName).Inc()
	m.frameBytesTotal.WithLabelValues("sent").Add(float64(size))
	m.frameBytes.Observe(float64(size))
}

// RecordFrameReceived records a received frame
func (m *Metrics) RecordFrameReceived(frameType byte, size int) {
	typeName := frameTypeName(frameType)
	m.framesTotal.WithLabelValues("received", typeName).Inc()
	m.frameBytesTotal.WithLabelValues("received").Add(float64(size))
	m.frameBytes.Observe(float64(size))
}

// RecordFrameDropped records a dropped frame
func (m *Metrics) RecordFrameDropped(reason string) {
	m.droppedFramesTotal.WithLabelValues(reason).Inc()
}

// RecordBackpressureEvent records a backpressure event
func (m *Metrics) RecordBackpressureEvent() {
	m.backpressureEvents.Inc()
}

// RecordSessionStart records the start of a new session
func (m *Metrics) RecordSessionStart() {
	m.sessionsTotal.Inc()
	m.activeSessions.Inc()
}

// RecordSessionEnd records the end of a session
func (m *Metrics) RecordSessionEnd(duration time.Duration, closeCode string) {
	m.activeSessions.Dec()
	m.sessionDuration.Observe(duration.Seconds())
	if closeCode != "" {
		m.sessionCloseTotal.WithLabelValues(closeCode).Inc()
	}
}

// frameTypeName converts frame type byte to string name
func frameTypeName(frameType byte) string {
	switch frameType {
	case FrameTypeData:
		return "data"
	case FrameTypePing:
		return "ping"
	case FrameTypePong:
		return "pong"
	case FrameTypeClose:
		return "close"
	case FrameTypeError:
		return "error"
	case FrameTypeControl:
		return "control"
	default:
		return "unknown"
	}
}
