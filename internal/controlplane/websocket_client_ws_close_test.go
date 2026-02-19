package controlplane

import (
	"testing"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWebSocketClient_handleMessage_WSClose_CallsOnWebSocketClose verifies that
// a ws_close message invokes the onWebSocketClose callback with the correct request ID.
func TestWebSocketClient_handleMessage_WSClose_CallsOnWebSocketClose(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan string, 1)
	client.SetOnWebSocketClose(func(requestID string) {
		received <- requestID
	})

	msg := &WebSocketMessage{
		Type:      "ws_close",
		RequestID: "ws-framed-123",
		Payload: map[string]interface{}{
			"stream_id": "ws-framed-123",
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "ws-framed-123", got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onWebSocketClose callback")
	}
}

// TestWebSocketClient_handleMessage_ProxyWebSocketClose_CallsOnWebSocketClose verifies
// that proxy_websocket_close also invokes the onWebSocketClose callback.
func TestWebSocketClient_handleMessage_ProxyWebSocketClose_CallsOnWebSocketClose(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan string, 1)
	client.SetOnWebSocketClose(func(requestID string) {
		received <- requestID
	})

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_close",
		RequestID: "req-456",
		Payload: map[string]interface{}{
			"stream_id": "stream-456",
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "stream-456", got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onWebSocketClose callback")
	}
}

// TestWebSocketClient_handleMessage_WSClose_UsesPayloadRequestID verifies that
// the ws_close handler prefers the payload's request_id field over the top-level
// RequestID, matching the behavior of ws_data routing.
func TestWebSocketClient_handleMessage_WSClose_UsesPayloadRequestID(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan string, 1)
	client.SetOnWebSocketClose(func(requestID string) {
		received <- requestID
	})

	msg := &WebSocketMessage{
		Type:      "ws_close",
		RequestID: "top-level-id",
		Payload: map[string]interface{}{
			"request_id": "payload-req-id",
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "payload-req-id", got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onWebSocketClose callback")
	}
}

// TestWebSocketClient_handleMessage_WSClose_FallsBackToTopLevelRequestID verifies
// that when the payload has no request_id or stream_id, the top-level RequestID is used.
func TestWebSocketClient_handleMessage_WSClose_FallsBackToTopLevelRequestID(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan string, 1)
	client.SetOnWebSocketClose(func(requestID string) {
		received <- requestID
	})

	msg := &WebSocketMessage{
		Type:      "ws_close",
		RequestID: "top-level-only",
		Payload:   map[string]interface{}{},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "top-level-only", got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onWebSocketClose callback")
	}
}

// TestWebSocketClient_handleMessage_WSClose_NoCallbackNoPanic verifies that
// ws_close does not panic when no onWebSocketClose callback is registered.
func TestWebSocketClient_handleMessage_WSClose_NoCallbackNoPanic(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	// No callback registered — should not panic
	msg := &WebSocketMessage{
		Type:      "ws_close",
		RequestID: "req-no-callback",
		Payload: map[string]interface{}{
			"stream_id": "stream-no-callback",
		},
		Timestamp: time.Now(),
	}

	require.NotPanics(t, func() {
		client.handleMessage(msg)
	})
}

// TestWebSocketClient_handleMessage_WSClose_AlsoRoutesToWSProxyManager verifies
// that ws_close is routed to BOTH the wsProxyManager (kubectl exec/attach) AND the
// onWebSocketClose callback (application WS streams). Both should fire.
func TestWebSocketClient_handleMessage_WSClose_AlsoRoutesToWSProxyManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	closeCallbackCalled := make(chan string, 1)
	client.SetOnWebSocketClose(func(requestID string) {
		closeCallbackCalled <- requestID
	})

	// The wsProxyManager should also receive the close message.
	// Since there's no matching stream in the manager, it will log an error
	// but not panic. We just verify the callback fires.
	msg := &WebSocketMessage{
		Type:      "ws_close",
		RequestID: "req-dual",
		Payload: map[string]interface{}{
			"stream_id": "stream-dual",
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-closeCallbackCalled:
		assert.Equal(t, "stream-dual", got)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onWebSocketClose callback")
	}
}
