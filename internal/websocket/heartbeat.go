package websocket

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// HeartbeatManager manages ping/pong heartbeat for WebSocket connections
type HeartbeatManager struct {
	conn          *websocket.Conn
	pingInterval  time.Duration
	pongTimeout   time.Duration
	logger        *logrus.Entry
	stopChan      chan struct{}
	stoppedChan   chan struct{}
	mu            sync.Mutex
	lastPongTime  time.Time
	missedPongs   int
	maxMissedPongs int
}

// NewHeartbeatManager creates a new heartbeat manager for a WebSocket connection
func NewHeartbeatManager(conn *websocket.Conn, config *Config, logger *logrus.Entry) *HeartbeatManager {
	return &HeartbeatManager{
		conn:           conn,
		pingInterval:   config.PingInterval,
		pongTimeout:    config.PongTimeout,
		logger:         logger,
		stopChan:       make(chan struct{}),
		stoppedChan:    make(chan struct{}),
		lastPongTime:   time.Now(),
		maxMissedPongs: 3, // Allow up to 3 missed pongs before closing
	}
}

// Start begins the heartbeat monitoring
func (h *HeartbeatManager) Start(ctx context.Context) {
	go h.heartbeatLoop(ctx)
}

// Stop stops the heartbeat monitoring
func (h *HeartbeatManager) Stop() {
	close(h.stopChan)
	<-h.stoppedChan
}

// heartbeatLoop sends periodic pings and monitors for pongs
func (h *HeartbeatManager) heartbeatLoop(ctx context.Context) {
	defer close(h.stoppedChan)

	ticker := time.NewTicker(h.pingInterval)
	defer ticker.Stop()

	// Set up pong handler
	h.conn.SetPongHandler(func(appData string) error {
		h.mu.Lock()
		h.lastPongTime = time.Now()
		h.missedPongs = 0
		h.mu.Unlock()
		h.logger.Debug("Received pong from backend service")
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			h.logger.Debug("Heartbeat loop stopped due to context cancellation")
			return
		case <-h.stopChan:
			h.logger.Debug("Heartbeat loop stopped")
			return
		case <-ticker.C:
			h.sendPing()
			h.checkPongTimeout()
		}
	}
}

// sendPing sends a ping frame to the connection
func (h *HeartbeatManager) sendPing() {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Set write deadline for ping
	deadline := time.Now().Add(5 * time.Second)
	if err := h.conn.SetWriteDeadline(deadline); err != nil {
		h.logger.WithError(err).Warn("Failed to set write deadline for ping")
		return
	}

	if err := h.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
		h.logger.WithError(err).Warn("Failed to send ping")
		return
	}

	h.logger.Debug("Sent ping to backend service")
}

// checkPongTimeout checks if we've received a pong recently
func (h *HeartbeatManager) checkPongTimeout() {
	h.mu.Lock()
	defer h.mu.Unlock()

	timeSinceLastPong := time.Since(h.lastPongTime)
	if timeSinceLastPong > h.pongTimeout {
		h.missedPongs++
		h.logger.WithFields(logrus.Fields{
			"time_since_last_pong": timeSinceLastPong.String(),
			"missed_pongs":         h.missedPongs,
		}).Warn("Pong timeout detected")

		if h.missedPongs >= h.maxMissedPongs {
			h.logger.WithField("missed_pongs", h.missedPongs).Error("Too many missed pongs, closing connection")
			// Close the connection with GoingAway status
			closeMsg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "ping timeout")
			_ = h.conn.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
			_ = h.conn.Close()
		}
	}
}

// GetStats returns heartbeat statistics
func (h *HeartbeatManager) GetStats() HeartbeatStats {
	h.mu.Lock()
	defer h.mu.Unlock()

	return HeartbeatStats{
		LastPongTime:   h.lastPongTime,
		MissedPongs:    h.missedPongs,
		TimeSincePong:  time.Since(h.lastPongTime),
	}
}

// HeartbeatStats contains heartbeat statistics
type HeartbeatStats struct {
	LastPongTime  time.Time
	MissedPongs   int
	TimeSincePong time.Duration
}
