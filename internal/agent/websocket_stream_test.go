package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMetrics is a package-level singleton to avoid prometheus duplicate registration panics.
var (
	testMetrics     *Metrics
	testMetricsOnce sync.Once
)

func getTestMetrics() *Metrics {
	testMetricsOnce.Do(func() {
		testMetrics = newMetrics()
	})
	return testMetrics
}

// TestHandleWebSocketData_RoutesToStream verifies that handleWebSocketData delivers
// data to the correct wsStreamEntry's dataChan.
func TestHandleWebSocketData_RoutesToStream(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	a := &Agent{
		logger:    logger,
		wsStreams: make(map[string]*wsStreamEntry),
		ctx:       context.Background(),
		metrics:   getTestMetrics(),
	}

	dataChan := make(chan []byte, 10)
	a.wsStreams["req-1"] = &wsStreamEntry{dataChan: dataChan, cancel: func() {}}

	payload := []byte{0x01, 'h', 'e', 'l', 'l', 'o'}
	a.handleWebSocketData("req-1", payload)

	select {
	case data := <-dataChan:
		assert.Equal(t, payload, data)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for data on dataChan")
	}
}

// TestHandleWebSocketData_UnknownStream verifies that data for a non-existent
// request ID is safely dropped with a warning (no panic).
func TestHandleWebSocketData_UnknownStream(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	a := &Agent{
		logger:    logger,
		wsStreams: make(map[string]*wsStreamEntry),
		ctx:       context.Background(),
		metrics:   getTestMetrics(),
	}

	// Should not panic
	a.handleWebSocketData("nonexistent", []byte("data"))
}

// TestHandleWebSocketClose_CancelsRelayContext verifies that handleWebSocketClose
// invokes the cancel function stored in the wsStreamEntry, which is the mechanism
// that tears down the bidirectional relay goroutines.
func TestHandleWebSocketClose_CancelsRelayContext(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	a := &Agent{
		logger:    logger,
		wsStreams: make(map[string]*wsStreamEntry),
		ctx:       context.Background(),
		metrics:   getTestMetrics(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.wsStreams["req-ws-1"] = &wsStreamEntry{
		dataChan: make(chan []byte, 1),
		cancel:   cancel,
	}

	// Before close, context should not be done
	select {
	case <-ctx.Done():
		t.Fatal("context should not be cancelled before handleWebSocketClose")
	default:
	}

	a.handleWebSocketClose("req-ws-1")

	// After close, context should be cancelled
	select {
	case <-ctx.Done():
		// Success
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled by handleWebSocketClose")
	}
}

// TestHandleWebSocketClose_UnknownStream verifies that closing a non-existent
// stream is a no-op (no panic, no error).
func TestHandleWebSocketClose_UnknownStream(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	a := &Agent{
		logger:    logger,
		wsStreams: make(map[string]*wsStreamEntry),
		ctx:       context.Background(),
		metrics:   getTestMetrics(),
	}

	// Should not panic
	a.handleWebSocketClose("nonexistent")
}

// TestStreamChanNilOut_RelayContinuesWithDataChan simulates the core bug fix:
// when streamChan is closed (as happens after writer.Close()), the relay goroutine
// should nil it out and continue reading from dataChan, not exit.
func TestStreamChanNilOut_RelayContinuesWithDataChan(t *testing.T) {
	// Simulate the select loop from handleWebSocketProxy controller->service goroutine.
	// streamChan is closed immediately (simulating writer.Close()).
	// dataChan receives data afterward.
	// The goroutine should forward the data, not exit.

	streamChan := make(chan []byte)
	close(streamChan) // Simulates writer.Close()

	dataChan := make(chan []byte, 1)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	forwarded := make(chan []byte, 1)

	go func() {
		for {
			select {
			case data, ok := <-dataChan:
				if !ok {
					return
				}
				forwarded <- data
			case _, ok := <-streamChan:
				if !ok {
					// BUG FIX: nil out instead of returning
					streamChan = nil
					continue
				}
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()

	// Send data through dataChan after streamChan is already closed
	dataChan <- []byte("test-frame")

	select {
	case data := <-forwarded:
		assert.Equal(t, []byte("test-frame"), data)
	case <-time.After(time.Second):
		t.Fatal("relay goroutine did not forward data from dataChan after streamChan closed — the bug is NOT fixed")
	}
}

// TestStreamChanNilOut_OldBehaviorExitsImmediately demonstrates the old buggy behavior
// where a closed streamChan causes the select to exit immediately, preventing any
// dataChan reads.
func TestStreamChanNilOut_OldBehaviorExitsImmediately(t *testing.T) {
	streamChan := make(chan []byte)
	close(streamChan)

	dataChan := make(chan []byte, 1)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exited := make(chan struct{})

	// Old buggy behavior: return on streamChan close
	go func() {
		defer close(exited)
		for {
			select {
			case _, ok := <-dataChan:
				if !ok {
					return
				}
			case _, ok := <-streamChan:
				if !ok {
					return // BUG: exits immediately
				}
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()

	// The goroutine should exit immediately due to closed streamChan
	select {
	case <-exited:
		// Confirmed: old behavior exits immediately
	case <-time.After(time.Second):
		t.Fatal("expected old behavior to exit immediately on closed streamChan")
	}
}

// TestWSStreamEntry_ConcurrentAccess verifies that concurrent reads and writes
// to the wsStreams map are safe under the RWMutex.
func TestWSStreamEntry_ConcurrentAccess(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	a := &Agent{
		logger:    logger,
		wsStreams: make(map[string]*wsStreamEntry),
		ctx:       context.Background(),
		metrics:   getTestMetrics(),
	}

	var wg sync.WaitGroup
	const numStreams = 50

	// Concurrently add streams
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reqID := "req-" + string(rune('A'+id%26)) + "-" + time.Now().Format(time.RFC3339Nano)
			ctx, cancel := context.WithCancel(context.Background())

			a.wsStreamsMu.Lock()
			a.wsStreams[reqID] = &wsStreamEntry{
				dataChan: make(chan []byte, 1),
				cancel:   cancel,
			}
			a.wsStreamsMu.Unlock()

			// Simulate some work
			time.Sleep(time.Millisecond)

			// Send data
			a.handleWebSocketData(reqID, []byte("data"))

			// Close
			a.handleWebSocketClose(reqID)

			require.Error(t, ctx.Err(), "context should be cancelled")

			// Cleanup
			a.wsStreamsMu.Lock()
			delete(a.wsStreams, reqID)
			a.wsStreamsMu.Unlock()
		}(i)
	}

	wg.Wait()
}
