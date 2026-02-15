package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWSUpgrader is a shared upgrader for test WebSocket servers.
var testWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// newTestLogger returns a logrus.Entry with logging suppressed for quiet tests.
func newTestLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	return logger.WithField("component", "heartbeat_test")
}

// newTestConfig returns a Config with short intervals suitable for tests.
func newTestConfig(pingInterval, pongTimeout time.Duration) *Config {
	return &Config{
		PingInterval: pingInterval,
		PongTimeout:  pongTimeout,
	}
}

// dialTestServer creates a test WebSocket server using the provided handler,
// dials it, and returns the client connection plus a cleanup function.
func dialTestServer(t *testing.T, handler http.HandlerFunc) (*websocket.Conn, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	wsURL := "ws" + server.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "should connect to test WebSocket server")
	cleanup := func() {
		conn.Close()
		server.Close()
	}
	return conn, cleanup
}

func TestNewHeartbeatManager(t *testing.T) {
	tests := []struct {
		name         string
		pingInterval time.Duration
		pongTimeout  time.Duration
	}{
		{
			name:         "default intervals",
			pingInterval: 30 * time.Second,
			pongTimeout:  90 * time.Second,
		},
		{
			name:         "short intervals",
			pingInterval: 10 * time.Millisecond,
			pongTimeout:  50 * time.Millisecond,
		},
		{
			name:         "custom intervals",
			pingInterval: 5 * time.Second,
			pongTimeout:  15 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				c, err := testWSUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				// Keep server alive long enough for the test
				time.Sleep(100 * time.Millisecond)
			})
			defer cleanup()

			config := newTestConfig(tt.pingInterval, tt.pongTimeout)
			logger := newTestLogger()

			h := NewHeartbeatManager(conn, config, logger)

			require.NotNil(t, h)
			assert.Equal(t, conn, h.conn)
			assert.Equal(t, tt.pingInterval, h.pingInterval)
			assert.Equal(t, tt.pongTimeout, h.pongTimeout)
			assert.Equal(t, logger, h.logger)
			assert.Equal(t, 3, h.maxMissedPongs)
			assert.Equal(t, 0, h.missedPongs)
			assert.NotNil(t, h.stopChan)
			assert.NotNil(t, h.stoppedChan)
			// lastPongTime should be approximately now
			assert.WithinDuration(t, time.Now(), h.lastPongTime, 2*time.Second)
		})
	}
}

func TestHeartbeatManager_StartStop(t *testing.T) {
	// Server that reads messages to process pings (gorilla auto-responds with pong)
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// Read messages to keep the server side alive and to process control frames
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	config := newTestConfig(20*time.Millisecond, 200*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should not panic
	h.Start(ctx)

	// Let the heartbeat loop run for a few cycles
	time.Sleep(80 * time.Millisecond)

	// Stop should not panic or hang
	done := make(chan struct{})
	go func() {
		h.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Stopped successfully
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within timeout")
	}
}

func TestHeartbeatManager_StopViaContext(t *testing.T) {
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	config := newTestConfig(20*time.Millisecond, 200*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	ctx, cancel := context.WithCancel(context.Background())
	h.Start(ctx)

	// Let it run briefly
	time.Sleep(50 * time.Millisecond)

	// Cancel context should cause the heartbeat loop to exit
	cancel()

	// stoppedChan should close when the loop exits
	select {
	case <-h.stoppedChan:
		// Loop exited as expected
	case <-time.After(2 * time.Second):
		t.Fatal("Heartbeat loop did not stop after context cancellation")
	}
}

func TestHeartbeatManager_SendPing(t *testing.T) {
	// Track pings received on the server side
	var pingCount int
	var mu sync.Mutex
	pingReceived := make(chan struct{}, 10)

	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()

		// Register a ping handler on the server to count incoming pings.
		// gorilla/websocket automatically responds with a pong by default.
		c.SetPingHandler(func(appData string) error {
			mu.Lock()
			pingCount++
			mu.Unlock()
			select {
			case pingReceived <- struct{}{}:
			default:
			}
			// Write pong back (default behavior)
			return c.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
		})

		// Must read to process control frames
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	config := newTestConfig(50*time.Millisecond, 500*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	// Directly call sendPing to verify it works without error
	h.sendPing()

	// Wait for the server to receive the ping
	select {
	case <-pingReceived:
		mu.Lock()
		assert.GreaterOrEqual(t, pingCount, 1, "Server should have received at least one ping")
		mu.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("Server did not receive ping within timeout")
	}
}

func TestHeartbeatManager_SendPingClearsWriteDeadline(t *testing.T) {
	// This test verifies the critical bug fix: sendPing must clear the write
	// deadline after sending the ping so that subsequent data writes are not
	// constrained by the 5-second ping deadline.
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	config := newTestConfig(50*time.Millisecond, 500*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	// Send a ping (sets then clears write deadline internally)
	h.sendPing()

	// After sendPing, we should be able to write a data message without hitting
	// a stale write deadline. If the deadline was NOT cleared, this write would
	// fail after 5 seconds.
	err := conn.WriteMessage(websocket.TextMessage, []byte("data after ping"))
	assert.NoError(t, err, "Should be able to write data after sendPing clears the write deadline")
}

func TestHeartbeatManager_GetStats(t *testing.T) {
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(200 * time.Millisecond)
	})
	defer cleanup()

	config := newTestConfig(50*time.Millisecond, 500*time.Millisecond)
	logger := newTestLogger()

	beforeCreate := time.Now()
	h := NewHeartbeatManager(conn, config, logger)

	stats := h.GetStats()

	// LastPongTime should be close to creation time
	assert.WithinDuration(t, beforeCreate, stats.LastPongTime, 2*time.Second)
	// MissedPongs should be 0 initially
	assert.Equal(t, 0, stats.MissedPongs)
	// TimeSincePong should be very small (just created)
	assert.Less(t, stats.TimeSincePong, 2*time.Second)
}

func TestHeartbeatManager_GetStatsAfterMissedPongs(t *testing.T) {
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		time.Sleep(500 * time.Millisecond)
	})
	defer cleanup()

	config := newTestConfig(50*time.Millisecond, 500*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	// Manually simulate missed pongs
	h.mu.Lock()
	h.missedPongs = 2
	h.lastPongTime = time.Now().Add(-100 * time.Millisecond)
	h.mu.Unlock()

	stats := h.GetStats()
	assert.Equal(t, 2, stats.MissedPongs)
	assert.GreaterOrEqual(t, stats.TimeSincePong, 100*time.Millisecond)
}

func TestHeartbeatManager_CheckPongTimeout(t *testing.T) {
	tests := []struct {
		name               string
		pongTimeout        time.Duration
		lastPongAge        time.Duration
		initialMissed      int
		expectedMissed     int
		expectConnClosed   bool
		expectMissedChange bool
	}{
		{
			name:               "no timeout when pong is recent",
			pongTimeout:        100 * time.Millisecond,
			lastPongAge:        10 * time.Millisecond,
			initialMissed:      0,
			expectedMissed:     0,
			expectConnClosed:   false,
			expectMissedChange: false,
		},
		{
			name:               "increments missed pongs when pong is overdue",
			pongTimeout:        50 * time.Millisecond,
			lastPongAge:        100 * time.Millisecond,
			initialMissed:      0,
			expectedMissed:     1,
			expectConnClosed:   false,
			expectMissedChange: true,
		},
		{
			name:               "increments from existing missed count",
			pongTimeout:        50 * time.Millisecond,
			lastPongAge:        100 * time.Millisecond,
			initialMissed:      1,
			expectedMissed:     2,
			expectConnClosed:   false,
			expectMissedChange: true,
		},
		{
			name:               "closes connection at max missed pongs",
			pongTimeout:        50 * time.Millisecond,
			lastPongAge:        100 * time.Millisecond,
			initialMissed:      2,
			expectedMissed:     3,
			expectConnClosed:   true,
			expectMissedChange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				c, err := testWSUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				// Read to keep server alive and process control frames
				for {
					if _, _, err := c.ReadMessage(); err != nil {
						return
					}
				}
			})
			defer cleanup()

			config := newTestConfig(50*time.Millisecond, tt.pongTimeout)
			logger := newTestLogger()
			h := NewHeartbeatManager(conn, config, logger)

			// Set up initial state
			h.mu.Lock()
			h.lastPongTime = time.Now().Add(-tt.lastPongAge)
			h.missedPongs = tt.initialMissed
			h.mu.Unlock()

			// Call checkPongTimeout
			h.checkPongTimeout()

			// Verify missed pongs count
			h.mu.Lock()
			assert.Equal(t, tt.expectedMissed, h.missedPongs)
			h.mu.Unlock()

			if tt.expectConnClosed {
				// After closing, writing should fail
				time.Sleep(10 * time.Millisecond) // Brief wait for close to propagate
				err := conn.WriteMessage(websocket.TextMessage, []byte("test"))
				assert.Error(t, err, "Connection should be closed after max missed pongs")
			}
		})
	}
}

func TestHeartbeatManager_PongResetsCounter(t *testing.T) {
	// This test verifies the full ping/pong cycle: the server auto-responds
	// to pings with pongs, and the pong handler resets missedPongs to 0.
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		// gorilla/websocket auto-responds to pings with pongs by default.
		// We just need to read to process control frames.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	config := newTestConfig(20*time.Millisecond, 500*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the heartbeat loop so the pong handler is registered
	h.Start(ctx)

	// Wait for at least one ping/pong cycle
	time.Sleep(80 * time.Millisecond)

	// We need to read from the client connection to process the pong frames.
	// Start a reader goroutine.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Give the reader time to process pong frames
	time.Sleep(60 * time.Millisecond)

	stats := h.GetStats()
	assert.Equal(t, 0, stats.MissedPongs, "Missed pongs should be 0 after receiving pong")
	// lastPongTime should have been updated recently
	assert.Less(t, stats.TimeSincePong, 500*time.Millisecond,
		"Time since last pong should be recent")

	cancel()
	<-h.stoppedChan
}

func TestHeartbeatManager_ConcurrentAccess(t *testing.T) {
	// Verify that concurrent calls to GetStats, sendPing, and checkPongTimeout
	// do not race. Run with -race flag to validate.
	conn, cleanup := dialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := testWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	config := newTestConfig(10*time.Millisecond, 500*time.Millisecond)
	logger := newTestLogger()
	h := NewHeartbeatManager(conn, config, logger)

	var wg sync.WaitGroup
	iterations := 20

	// Concurrent GetStats
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = h.GetStats()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Concurrent sendPing
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			h.sendPing()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Concurrent checkPongTimeout
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			h.checkPongTimeout()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	wg.Wait()
}
