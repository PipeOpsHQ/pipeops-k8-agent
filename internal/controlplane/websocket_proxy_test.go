package controlplane

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestNewWebSocketProxyManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, err := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	assert.NoError(t, err)
	assert.NotNil(t, client)

	manager := NewWebSocketProxyManager(client, logger)
	assert.NotNil(t, manager)
	assert.NotNil(t, manager.streams)
	assert.Equal(t, 0, len(manager.streams))
}

func TestHandleWebSocketProxyStart_MissingStreamID(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, _ := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	manager := NewWebSocketProxyManager(client, logger)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_start",
		RequestID: "req-123",
		Payload:   map[string]interface{}{},
		Timestamp: time.Now(),
	}

	manager.HandleWebSocketProxyStart(msg)

	assert.Equal(t, 0, len(manager.streams))
}

func TestHandleWebSocketProxyData_UnknownStream(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, _ := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	manager := NewWebSocketProxyManager(client, logger)

	data := []byte{byte(websocket.TextMessage), 'h', 'e', 'l', 'l', 'o'}
	encoded := base64.StdEncoding.EncodeToString(data)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: "stream-123",
		Payload: map[string]interface{}{
			"stream_id": "unknown-stream",
			"data":      encoded,
		},
		Timestamp: time.Now(),
	}

	manager.HandleWebSocketProxyData(msg)

	assert.Equal(t, 0, len(manager.streams))
}

func TestHandleWebSocketProxyData_InvalidBase64(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, _ := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	manager := NewWebSocketProxyManager(client, logger)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: "stream-123",
		Payload: map[string]interface{}{
			"stream_id": "stream-123",
			"data":      "not-valid-base64!!!",
		},
		Timestamp: time.Now(),
	}

	manager.HandleWebSocketProxyData(msg)

	assert.Equal(t, 0, len(manager.streams))
}

func TestHandleWebSocketProxyClose(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, _ := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	manager := NewWebSocketProxyManager(client, logger)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_close",
		RequestID: "stream-123",
		Payload: map[string]interface{}{
			"stream_id": "stream-123",
		},
		Timestamp: time.Now(),
	}

	manager.HandleWebSocketProxyClose(msg)

	assert.Equal(t, 0, len(manager.streams))
}

func TestCloseStream(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, _ := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	manager := NewWebSocketProxyManager(client, logger)

	manager.closeStream("non-existent-stream")

	assert.Equal(t, 0, len(manager.streams))
}

func TestCloseAll(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, _ := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	manager := NewWebSocketProxyManager(client, logger)

	manager.CloseAll()

	assert.Equal(t, 0, len(manager.streams))
}

func TestWebSocketProxyManager_Integration(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, err := NewWebSocketClient("https://api.example.com", "test-token", "agent-1", nil, logger)
	assert.NoError(t, err)

	assert.NotNil(t, client.wsProxyManager)

	client.Close()
}
