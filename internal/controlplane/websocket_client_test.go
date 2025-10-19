package controlplane

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func TestWebSocketClient_Connect(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify token in Authorization header (server-to-server authentication)
		authHeader := r.Header.Get("Authorization")
		assert.Equal(t, "Bearer test-token", authHeader)

		// Upgrade to WebSocket
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	assert.NoError(t, err)
	assert.True(t, client.isConnected())

	// Cleanup
	client.Close()
}

func TestWebSocketClient_RegisterAgent(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read registration message
		var msg WebSocketMessage
		err = conn.ReadJSON(&msg)
		if err != nil {
			return
		}

		assert.Equal(t, "register", msg.Type)
		assert.NotEmpty(t, msg.RequestID)

		// Send registration response
		response := WebSocketMessage{
			Type:      "register_success",
			RequestID: msg.RequestID,
			Payload: map[string]interface{}{
				"cluster_id":   "550e8400-e29b-41d4-a716-446655440000",
				"cluster_uuid": "550e8400-e29b-41d4-a716-446655440000",
				"name":         "test-cluster",
				"status":       "registered",
				"workspace_id": float64(123),
			},
			Timestamp: time.Now(),
		}
		conn.WriteJSON(response)

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Register agent
	agent := &types.Agent{
		ID:       "agent-123",
		Name:     "test-cluster",
		Version:  "v1.28.3+k3s1",
		Hostname: "test-host",
		ServerIP: "192.168.1.1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.RegisterAgent(ctx, agent)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", result.ClusterID)
	assert.Equal(t, "test-cluster", result.Name)
	assert.Equal(t, "registered", result.Status)
	assert.Equal(t, 123, result.WorkspaceID)

	// Cleanup
	client.Close()
}

func TestProxyResponseWriterCloseSendsResponse(t *testing.T) {
	sender := &stubProxySender{}
	logger := logrus.New()

	writer := newBufferedProxyResponseWriter(sender, "req-1", logger)

	headers := map[string][]string{
		"Content-Type": {"application/json"},
		"Connection":   {"keep-alive"},
	}

	require.NoError(t, writer.WriteHeader(201, headers))
	require.NoError(t, writer.WriteChunk([]byte("chunk1")))
	require.NoError(t, writer.WriteChunk([]byte("chunk2")))

	err := writer.Close()
	require.NoError(t, err)

	require.NotNil(t, sender.response)
	assert.Equal(t, "req-1", sender.response.RequestID)
	assert.Equal(t, 201, sender.response.Status)
	assert.Equal(t, []string{"application/json"}, sender.response.Headers["Content-Type"])
	_, hasConnection := sender.response.Headers["Connection"]
	assert.False(t, hasConnection)

	expectedBody := base64.StdEncoding.EncodeToString([]byte("chunk1chunk2"))
	assert.Equal(t, expectedBody, sender.response.Body)
	assert.Equal(t, "base64", sender.response.Encoding)

	assert.Nil(t, sender.errPayload)
}

func TestProxyResponseWriterCloseWithError(t *testing.T) {
	sender := &stubProxySender{}
	writer := newBufferedProxyResponseWriter(sender, "req-2", nil)

	testErr := errors.New("boom")
	err := writer.CloseWithError(testErr)
	require.Error(t, err)

	require.NotNil(t, sender.errPayload)
	assert.Equal(t, "req-2", sender.errPayload.RequestID)
	assert.Equal(t, "boom", sender.errPayload.Error)
	assert.Nil(t, sender.response)
}

type stubProxySender struct {
	response   *ProxyResponse
	errPayload *ProxyError
	respErr    error
	errErr     error
}

func (s *stubProxySender) SendProxyResponse(_ context.Context, response *ProxyResponse) error {
	s.response = response
	return s.respErr
}

func (s *stubProxySender) SendProxyError(_ context.Context, proxyErr *ProxyError) error {
	s.errPayload = proxyErr
	return s.errErr
}

func TestWebSocketClient_SendHeartbeat(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	var receivedHeartbeat atomic.Bool

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read heartbeat message
		go func() {
			var msg WebSocketMessage
			err = conn.ReadJSON(&msg)
			if err != nil {
				return
			}

			if msg.Type == "heartbeat" {
				receivedHeartbeat.Store(true)
				// Send acknowledgment
				response := WebSocketMessage{
					Type:      "heartbeat_ack",
					Payload:   map[string]interface{}{"received_at": time.Now()},
					Timestamp: time.Now(),
				}
				conn.WriteJSON(response)
			}
		}()

		// Keep connection open
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Send heartbeat
	heartbeat := &HeartbeatRequest{
		ClusterID:    "550e8400-e29b-41d4-a716-446655440000",
		AgentID:      "agent-123",
		Status:       "healthy",
		TunnelStatus: "connected",
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"version": "1.0.0",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.SendHeartbeat(ctx, heartbeat)
	assert.NoError(t, err)

	// Wait for server to process
	time.Sleep(200 * time.Millisecond)

	assert.True(t, receivedHeartbeat.Load())

	// Cleanup
	client.Close()
}

func TestWebSocketClient_PingPong(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	var receivedPong atomic.Bool

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send ping
		pingMsg := WebSocketMessage{
			Type:      "ping",
			RequestID: "ping-123",
			Payload:   map[string]interface{}{},
			Timestamp: time.Now(),
		}
		conn.WriteJSON(pingMsg)

		// Wait for pong
		var pongMsg WebSocketMessage
		err = conn.ReadJSON(&pongMsg)
		if err == nil && pongMsg.Type == "pong" {
			receivedPong.Store(true)
		}

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Wait for ping/pong exchange
	time.Sleep(200 * time.Millisecond)

	assert.True(t, receivedPong.Load())

	// Cleanup
	client.Close()
}

func TestWebSocketClient_Reconnection(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	connectionCount := 0
	var connMutex sync.Mutex

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connMutex.Lock()
		connectionCount++
		currentCount := connectionCount
		connMutex.Unlock()

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Close connection immediately on first connection to trigger reconnect
		if currentCount == 1 {
			time.Sleep(50 * time.Millisecond)
			conn.Close()
			return
		}

		// Keep subsequent connections open
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", nil, logger)
	require.NoError(t, err)

	// Set shorter reconnect delay for testing
	client.reconnectDelay = 200 * time.Millisecond
	client.maxReconnectDelay = 1 * time.Second

	err = client.Connect()
	require.NoError(t, err)

	// Wait for reconnection to happen
	time.Sleep(2 * time.Second)

	// Should have at least 2 connections (initial + reconnect)
	connMutex.Lock()
	count := connectionCount
	connMutex.Unlock()

	assert.GreaterOrEqual(t, count, 2, "Expected at least 2 connections (initial + reconnect)")

	// Cleanup
	client.Close()
}

func TestWebSocketClient_ErrorHandling(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send error message
		errorMsg := WebSocketMessage{
			Type:      "error",
			RequestID: "error-123",
			Payload: map[string]interface{}{
				"error": "Test error message",
			},
			Timestamp: time.Now(),
		}
		conn.WriteJSON(errorMsg)

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Wait for error message
	time.Sleep(200 * time.Millisecond)

	// Error should be logged (not returned since it's unsolicited)

	// Cleanup
	client.Close()
}
