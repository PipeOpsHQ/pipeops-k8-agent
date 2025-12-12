package controlplane

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebSocketClient_handleMessage_ProxyWebSocketData_UsesPayloadRequestIDWhenTopLevelMissing(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan struct {
		requestID string
		data      []byte
	}, 1)

	client.SetOnWebSocketData(func(requestID string, data []byte) {
		received <- struct {
			requestID string
			data      []byte
		}{requestID: requestID, data: data}
	})

	raw := []byte{byte(websocket.TextMessage), 'o', 'k'}
	encoded := base64.StdEncoding.EncodeToString(raw)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: "",
		Payload: map[string]interface{}{
			"request_id": "req-123",
			"data":       encoded,
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "req-123", got.requestID)
		assert.Equal(t, raw, got.data)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for WebSocket data callback")
	}
}

func TestWebSocketClient_handleMessage_ProxyWebSocketData_UsesPayloadStreamIDWhenNoKubectlStream(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan struct {
		requestID string
		data      []byte
	}, 1)

	client.SetOnWebSocketData(func(requestID string, data []byte) {
		received <- struct {
			requestID string
			data      []byte
		}{requestID: requestID, data: data}
	})

	raw := []byte{byte(websocket.BinaryMessage), 0x01, 0x02}
	encoded := base64.StdEncoding.EncodeToString(raw)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: "not-the-stream-id",
		Payload: map[string]interface{}{
			"stream_id": "ws-framed-abc",
			"data":      encoded,
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "ws-framed-abc", got.requestID)
		assert.Equal(t, raw, got.data)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for WebSocket data callback")
	}
}

func TestWebSocketClient_handleMessage_ProxyWebSocketData_StreamIDRoutesToKubectlManagerWhenStreamExists(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	appCallback := make(chan struct{}, 1)
	client.SetOnWebSocketData(func(_ string, _ []byte) {
		appCallback <- struct{}{}
	})

	streamID := "kubectl-stream-1"
	stream := &WebSocketStream{
		streamID: streamID,
		dataCh:   make(chan []byte, 1),
		closeCh:  make(chan struct{}),
		ctx:      context.Background(),
		cancel:   func() {},
	}
	client.wsProxyManager.streamsMu.Lock()
	client.wsProxyManager.streams[streamID] = stream
	client.wsProxyManager.streamsMu.Unlock()

	raw := []byte{byte(websocket.TextMessage), 'h', 'i'}
	encoded := base64.StdEncoding.EncodeToString(raw)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: "",
		Payload: map[string]interface{}{
			"stream_id": streamID,
			"data":      encoded,
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-stream.dataCh:
		assert.Equal(t, raw, got)
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for wsProxyManager stream data")
	}

	select {
	case <-appCallback:
		t.Fatal("unexpected onWebSocketData callback for kubectl stream")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWebSocketClient_handleMessage_ProxyWebSocketData_PrependsMessageTypeWhenUnframed(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	received := make(chan struct {
		requestID string
		data      []byte
	}, 1)

	client.SetOnWebSocketData(func(requestID string, data []byte) {
		received <- struct {
			requestID string
			data      []byte
		}{requestID: requestID, data: data}
	})

	// Raw WS payload without the leading message type byte.
	raw := []byte("hello")
	encoded := base64.StdEncoding.EncodeToString(raw)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: "",
		Payload: map[string]interface{}{
			"request_id":   "req-hello",
			"data":         encoded,
			"message_type": float64(websocket.TextMessage),
		},
		Timestamp: time.Now(),
	}

	client.handleMessage(msg)

	select {
	case got := <-received:
		assert.Equal(t, "req-hello", got.requestID)
		require.GreaterOrEqual(t, len(got.data), 1)
		assert.Equal(t, byte(websocket.TextMessage), got.data[0])
		assert.Equal(t, raw, got.data[1:])
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for WebSocket data callback")
	}
}
