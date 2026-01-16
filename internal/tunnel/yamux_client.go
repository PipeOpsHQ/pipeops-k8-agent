package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
)

var (
	yamuxStreamsAccepted = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "pipeops_agent_yamux_streams_accepted_total",
			Help: "Total yamux streams accepted from gateway",
		},
	)

	yamuxStreamDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "pipeops_agent_yamux_stream_duration_seconds",
			Help:    "Duration of yamux tunnel streams",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 15),
		},
	)

	yamuxBytesTransferred = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pipeops_agent_yamux_bytes_total",
			Help: "Bytes transferred through yamux",
		},
		[]string{"direction"}, // "in" or "out"
	)

	yamuxActiveStreams = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "pipeops_agent_yamux_active_streams",
			Help: "Number of currently active yamux streams",
		},
	)

	yamuxStreamErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pipeops_agent_yamux_stream_errors_total",
			Help: "Total errors in yamux stream handling",
		},
		[]string{"type"}, // "dial_error", "header_error", "copy_error"
	)
)

// YamuxConfig holds configuration for the yamux tunnel client
type YamuxConfig struct {
	MaxStreamWindowSize uint32
	KeepAliveInterval   time.Duration
	ConnectionTimeout   time.Duration
	AcceptBacklog       int
}

// DefaultYamuxConfig returns default yamux configuration
func DefaultYamuxConfig() YamuxConfig {
	return YamuxConfig{
		MaxStreamWindowSize: 256 * 1024, // 256KB
		KeepAliveInterval:   30 * time.Second,
		ConnectionTimeout:   10 * time.Second,
		AcceptBacklog:       256,
	}
}

// YamuxTunnelClient manages yamux multiplexed tunnel streams
type YamuxTunnelClient struct {
	session     *yamux.Session
	wsConn      *WSConn
	clusterUUID string
	config      YamuxConfig
	logger      *logrus.Logger

	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	activeStreams int64
}

// NewYamuxTunnelClient creates a new yamux tunnel client
func NewYamuxTunnelClient(clusterUUID string, ws *websocket.Conn, config YamuxConfig, logger *logrus.Logger) (*YamuxTunnelClient, error) {
	wsConn := NewWSConn(ws)

	// Configure yamux - agent is CLIENT (accepts streams from gateway SERVER)
	yamuxCfg := yamux.DefaultConfig()
	yamuxCfg.AcceptBacklog = config.AcceptBacklog
	yamuxCfg.EnableKeepAlive = true
	yamuxCfg.KeepAliveInterval = config.KeepAliveInterval
	yamuxCfg.ConnectionWriteTimeout = config.ConnectionTimeout
	yamuxCfg.MaxStreamWindowSize = config.MaxStreamWindowSize

	// Agent is CLIENT - accepts streams opened by gateway
	session, err := yamux.Client(wsConn, yamuxCfg)
	if err != nil {
		wsConn.Close()
		return nil, fmt.Errorf("failed to create yamux client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &YamuxTunnelClient{
		session:     session,
		wsConn:      wsConn,
		clusterUUID: clusterUUID,
		config:      config,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
	}

	return client, nil
}

// Run starts accepting streams from the gateway
func (c *YamuxTunnelClient) Run() error {
	c.logger.WithField("cluster_uuid", c.clusterUUID).Info("[YAMUX] Starting tunnel client")

	for {
		select {
		case <-c.ctx.Done():
			return nil
		default:
		}

		// Accept stream opened by gateway
		stream, err := c.session.AcceptStream()
		if err != nil {
			if c.ctx.Err() != nil {
				return nil // Context cancelled, clean shutdown
			}
			if err == yamux.ErrSessionShutdown {
				c.logger.Info("[YAMUX] Session shutdown, stopping client")
				return nil
			}
			return fmt.Errorf("accept stream error: %w", err)
		}

		yamuxStreamsAccepted.Inc()
		atomic.AddInt64(&c.activeStreams, 1)
		yamuxActiveStreams.Set(float64(atomic.LoadInt64(&c.activeStreams)))

		c.wg.Add(1)
		go func(s *yamux.Stream) {
			defer c.wg.Done()
			defer func() {
				atomic.AddInt64(&c.activeStreams, -1)
				yamuxActiveStreams.Set(float64(atomic.LoadInt64(&c.activeStreams)))
			}()
			c.handleStream(s)
		}(stream)
	}
}

// handleStream processes a single tunnel stream
func (c *YamuxTunnelClient) handleStream(stream *yamux.Stream) {
	startTime := time.Now()
	defer func() {
		stream.Close()
		yamuxStreamDuration.Observe(time.Since(startTime).Seconds())
	}()

	// Read tunnel header
	var header TunnelStreamHeader
	if err := header.Decode(stream); err != nil {
		c.logger.WithError(err).Error("[YAMUX] Failed to read tunnel header")
		yamuxStreamErrors.WithLabelValues("header_error").Inc()
		return
	}

	logger := c.logger.WithFields(logrus.Fields{
		"service":   header.ServiceName,
		"namespace": header.ServiceNamespace,
		"port":      header.ServicePort,
		"tunnel_id": header.TunnelID,
		"protocol":  protocolName(header.Protocol),
	})

	logger.Debug("[YAMUX] Received tunnel stream")

	// Dial target service
	target := fmt.Sprintf("%s.%s.svc.cluster.local:%d",
		header.ServiceName, header.ServiceNamespace, header.ServicePort)

	var targetConn net.Conn
	var err error

	if header.Protocol == TunnelProtocolUDP {
		targetConn, err = net.DialTimeout("udp", target, c.config.ConnectionTimeout)
	} else {
		targetConn, err = net.DialTimeout("tcp", target, c.config.ConnectionTimeout)
	}

	if err != nil {
		logger.WithError(err).Errorf("[YAMUX] Failed to dial target %s", target)
		yamuxStreamErrors.WithLabelValues("dial_error").Inc()
		return
	}
	defer targetConn.Close()

	logger.Debugf("[YAMUX] Connected to target %s", target)

	// Bidirectional copy with metrics
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream -> Target (data from gateway client to target service)
	go func() {
		defer wg.Done()
		n, err := io.Copy(targetConn, stream)
		if err != nil && err != io.EOF {
			logger.WithError(err).Debug("[YAMUX] Error copying stream->target")
		}
		yamuxBytesTransferred.WithLabelValues("in").Add(float64(n))
		// Signal EOF to target
		if tc, ok := targetConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	// Target -> Stream (data from target service back to gateway client)
	go func() {
		defer wg.Done()
		n, err := io.Copy(stream, targetConn)
		if err != nil && err != io.EOF {
			logger.WithError(err).Debug("[YAMUX] Error copying target->stream")
		}
		yamuxBytesTransferred.WithLabelValues("out").Add(float64(n))
		stream.Close()
	}()

	wg.Wait()
	logger.Debug("[YAMUX] Tunnel stream closed")
}

// Close shuts down the yamux session
func (c *YamuxTunnelClient) Close() error {
	c.logger.Info("[YAMUX] Shutting down tunnel client")
	c.cancel()

	// Wait for active streams to finish (with timeout)
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("[YAMUX] All streams closed gracefully")
	case <-time.After(30 * time.Second):
		c.logger.Warn("[YAMUX] Timeout waiting for streams to close")
	}

	return c.session.Close()
}

// IsReady returns true if the yamux session is active
func (c *YamuxTunnelClient) IsReady() bool {
	return !c.session.IsClosed() && !c.wsConn.IsClosed()
}

// ActiveStreamCount returns the number of active streams
func (c *YamuxTunnelClient) ActiveStreamCount() int64 {
	return atomic.LoadInt64(&c.activeStreams)
}

// protocolName returns a human-readable protocol name
func protocolName(p uint8) string {
	switch p {
	case TunnelProtocolTCP:
		return "TCP"
	case TunnelProtocolUDP:
		return "UDP"
	default:
		return "unknown"
	}
}
