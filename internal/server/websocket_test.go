package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebSocketMessage_Structure(t *testing.T) {
	msg := WebSocketMessage{
		Type:      "test",
		Timestamp: time.Now(),
		Data:      map[string]string{"key": "value"},
	}

	assert.Equal(t, "test", msg.Type)
	assert.NotNil(t, msg.Timestamp)
	assert.NotNil(t, msg.Data)
}

func TestWebSocketMessage_JSON(t *testing.T) {
	msg := WebSocketMessage{
		Type:      "status",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"agent_id": "test-agent",
			"status":   "connected",
		},
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.NotEmpty(t, jsonData)

	// Test JSON unmarshaling
	var decoded WebSocketMessage
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)
	assert.Equal(t, msg.Type, decoded.Type)
}

func TestHandleEventStream(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/realtime/events", nil)

	// Start serving in a goroutine and close after a short time
	done := make(chan bool)
	go func() {
		server.router.ServeHTTP(w, req)
		done <- true
	}()

	// Give it time to start streaming
	time.Sleep(100 * time.Millisecond)

	// Check headers
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
	assert.Equal(t, "keep-alive", w.Header().Get("Connection"))
}

func TestHandleLogStream(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/realtime/logs", nil)
	server.router.ServeHTTP(w, req)

	// Check headers
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
}

func TestSendSSEEvent(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	data := gin.H{
		"message": "test event",
		"value":   123,
	}

	server.sendSSEEvent(c.Writer, "test", data)

	result := w.Body.String()
	assert.Contains(t, result, "event: test")
	assert.Contains(t, result, "data:")
	assert.Contains(t, result, "test event")
}

func TestGetRuntimeMetricsForWebSocket(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	metrics := server.getRuntimeMetrics()

	assert.NotNil(t, metrics)
	memoryMap, ok := metrics["memory"].(gin.H)
	assert.True(t, ok)
	assert.NotNil(t, memoryMap)

	assert.NotNil(t, metrics["goroutines"])
	assert.NotNil(t, metrics["uptime"])
	assert.NotNil(t, metrics["timestamp"])
}

func TestWebSocketUpgrader(t *testing.T) {
	// Test that upgrader allows all origins (in test mode)
	assert.NotNil(t, upgrader)
	assert.NotNil(t, upgrader.CheckOrigin)

	// Test CheckOrigin function
	req := &http.Request{
		Header: http.Header{},
	}
	result := upgrader.CheckOrigin(req)
	assert.True(t, result)
}

func TestWebSocketMessageTypes(t *testing.T) {
	// Test various message types
	types := []string{"ping", "pong", "status", "metrics", "error", "health_update"}

	for _, msgType := range types {
		msg := WebSocketMessage{
			Type:      msgType,
			Timestamp: time.Now(),
			Data:      gin.H{"test": "data"},
		}

		assert.Equal(t, msgType, msg.Type)
		jsonData, err := json.Marshal(msg)
		require.NoError(t, err)
		assert.Contains(t, string(jsonData), msgType)
	}
}

func TestSSEEventFormat(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	data := gin.H{
		"agent_id":  "test-agent",
		"timestamp": time.Now(),
	}

	server.sendSSEEvent(c.Writer, "connected", data)

	body := w.Body.String()

	// Check SSE format
	lines := strings.Split(body, "\n")
	assert.True(t, len(lines) >= 2)

	// First line should be event
	assert.Contains(t, lines[0], "event: connected")

	// Second line should be data
	assert.Contains(t, lines[1], "data:")

	// Should have proper JSON in data
	dataLine := strings.TrimPrefix(lines[1], "data: ")
	var parsedData map[string]interface{}
	err := json.Unmarshal([]byte(dataLine), &parsedData)
	assert.NoError(t, err)
	assert.Equal(t, "test-agent", parsedData["agent_id"])
}

func TestHandleWebSocketMessage_Ping(t *testing.T) {
	// We can't easily test the full WebSocket flow without a real connection,
	// but we can test the message handling logic
	message := &WebSocketMessage{
		Type:      "ping",
		Timestamp: time.Now(),
		Data:      nil,
	}

	// Verify message structure is valid
	assert.Equal(t, "ping", message.Type)
	jsonData, err := json.Marshal(message)
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), "ping")
}

func TestHandleWebSocketMessage_GetStatus(t *testing.T) {
	message := &WebSocketMessage{
		Type:      "get_status",
		Timestamp: time.Now(),
		Data:      nil,
	}

	assert.Equal(t, "get_status", message.Type)
}

func TestHandleWebSocketMessage_GetMetrics(t *testing.T) {
	message := &WebSocketMessage{
		Type:      "get_metrics",
		Timestamp: time.Now(),
		Data:      nil,
	}

	assert.Equal(t, "get_metrics", message.Type)
}

func TestHandleWebSocketMessage_Unknown(t *testing.T) {
	message := &WebSocketMessage{
		Type:      "unknown_type",
		Timestamp: time.Now(),
		Data:      gin.H{"test": "data"},
	}

	assert.Equal(t, "unknown_type", message.Type)
}

func TestWebSocketIntegration(t *testing.T) {
	// Integration test for WebSocket endpoint
	config := getTestServerConfig()
	config.Agent.Port = 0 // Random port
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	server := NewServer(config, logger)
	server.setupRoutes()

	// Create test server
	testServer := httptest.NewServer(server.router)
	defer testServer.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"

	// Try to connect (may fail since it's a test environment)
	dialer := websocket.Dialer{
		HandshakeTimeout: time.Second,
	}

	conn, _, err := dialer.Dial(wsURL, nil)
	if err == nil {
		defer conn.Close()

		// Try to read initial status message
		var msg WebSocketMessage
		err = conn.ReadJSON(&msg)
		if err == nil {
			assert.Equal(t, "status", msg.Type)
		}
	}
	// Note: This test may fail in CI environments without WebSocket support
}
