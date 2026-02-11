package controlplane

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
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

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
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

func TestProxyResponseWriterClosePreservesUpgradeHeaders(t *testing.T) {
	sender := &stubProxySender{}
	logger := logrus.New()

	writer := newBufferedProxyResponseWriter(sender, "ws-req-1", logger)

	headers := map[string][]string{
		"Upgrade":              {"websocket"},
		"Connection":           {"Upgrade"},
		"Sec-Websocket-Accept": {"s3pPLMBiTxaQ9kYGzzhZRbK+xOo="},
	}

	require.NoError(t, writer.WriteHeader(http.StatusSwitchingProtocols, headers))
	err := writer.Close()
	require.NoError(t, err)

	require.NotNil(t, sender.response)
	assert.Equal(t, http.StatusSwitchingProtocols, sender.response.Status)
	assert.Equal(t, []string{"websocket"}, sender.response.Headers["Upgrade"],
		"Upgrade header must be preserved for 101 Switching Protocols")
	assert.Equal(t, []string{"Upgrade"}, sender.response.Headers["Connection"],
		"Connection header must be preserved for 101 Switching Protocols")
	assert.Equal(t, []string{"s3pPLMBiTxaQ9kYGzzhZRbK+xOo="}, sender.response.Headers["Sec-Websocket-Accept"])
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

func TestWebSocketClient_parseProxyRequest_BodyAsJSONObject(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	msg := &WebSocketMessage{
		Type:      "proxy_request",
		RequestID: "req-obj-1",
		Payload: map[string]interface{}{
			"method": "POST",
			"path":   "/api/v1/buckets",
			"headers": map[string]interface{}{
				"Content-Type": []interface{}{"application/json"},
			},
			"body": map[string]interface{}{
				"name": "example",
			},
		},
		Timestamp: time.Now(),
	}

	req, err := client.parseProxyRequest(msg)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "req-obj-1", req.RequestID)
	assert.Equal(t, "POST", req.Method)
	assert.Equal(t, "/api/v1/buckets", req.Path)
	assert.Equal(t, "json", req.BodyEncoding)
	assert.Equal(t, []byte(`{"name":"example"}`), req.Body)
}

type stubProxySender struct {
	response   *ProxyResponse
	errPayload *ProxyError
	respErr    error
	errErr     error
	binaryBody []byte
	binaryUsed bool
}

func (s *stubProxySender) SendProxyResponse(_ context.Context, response *ProxyResponse) error {
	s.response = response
	return s.respErr
}

func (s *stubProxySender) SendProxyResponseBinary(_ context.Context, response *ProxyResponse, bodyBytes []byte) error {
	s.response = response
	s.binaryBody = bodyBytes
	s.binaryUsed = true
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

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
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

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
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

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
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

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Wait for error message
	time.Sleep(200 * time.Millisecond)

	// Error should be logged (not returned since it's unsolicited)

	// Cleanup
	client.Close()
}

func TestWebSocketClient_UnknownMessageType(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	var receivedFallbackMsg atomic.Bool
	var fallbackPayload map[string]interface{}
	var fallbackMutex sync.Mutex

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send unknown message type
		unknownMsg := WebSocketMessage{
			Type:      "future_message_v2",
			RequestID: "req-456",
			Payload: map[string]interface{}{
				"data": "some future data",
			},
			Timestamp: time.Now(),
		}
		conn.WriteJSON(unknownMsg)

		// Wait for protocol_fallback response
		var fallbackMsg WebSocketMessage
		err = conn.ReadJSON(&fallbackMsg)
		if err == nil && fallbackMsg.Type == "protocol_fallback" {
			receivedFallbackMsg.Store(true)
			fallbackMutex.Lock()
			fallbackPayload = fallbackMsg.Payload
			fallbackMutex.Unlock()
		}

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Wait for unknown message and fallback response
	time.Sleep(300 * time.Millisecond)

	// Verify fallback message was sent
	assert.True(t, receivedFallbackMsg.Load(), "Expected protocol_fallback message to be sent")

	// Verify fallback payload
	fallbackMutex.Lock()
	assert.NotNil(t, fallbackPayload)
	assert.Equal(t, "future_message_v2", fallbackPayload["unknown_type"])
	assert.Equal(t, "legacy_proxy", fallbackPayload["fallback_mode"])
	supportedTypes, ok := fallbackPayload["supported_types"].([]interface{})
	assert.True(t, ok)
	assert.Contains(t, supportedTypes, "proxy_request")
	assert.Contains(t, supportedTypes, "proxy_cancel")
	fallbackMutex.Unlock()

	// Verify unknown message type was tracked
	client.unknownMessageMutex.RLock()
	count := client.unknownMessageTypes["future_message_v2"]
	client.unknownMessageMutex.RUnlock()
	assert.Equal(t, 1, count)

	// Cleanup
	client.Close()
}

func TestWebSocketClient_PersistentUnknownMessageType(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	var fallbackCount atomic.Int32

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send same unknown message type multiple times
		for i := 0; i < 3; i++ {
			unknownMsg := WebSocketMessage{
				Type:      "experimental_feature",
				RequestID: fmt.Sprintf("req-%d", i),
				Payload:   map[string]interface{}{},
				Timestamp: time.Now(),
			}
			conn.WriteJSON(unknownMsg)
			time.Sleep(50 * time.Millisecond)
		}

		// Count protocol_fallback messages (should only get 1)
		for {
			var msg WebSocketMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				break
			}
			if msg.Type == "protocol_fallback" {
				fallbackCount.Add(1)
			}
		}
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Wait for all messages to be processed
	time.Sleep(500 * time.Millisecond)

	// Verify fallback message was sent only once (on first occurrence)
	assert.Equal(t, int32(1), fallbackCount.Load(), "Expected only 1 protocol_fallback message")

	// Verify unknown message type count
	client.unknownMessageMutex.RLock()
	count := client.unknownMessageTypes["experimental_feature"]
	client.unknownMessageMutex.RUnlock()
	assert.Equal(t, 3, count)

	// Cleanup
	client.Close()
}

func TestWebSocketClient_UnknownMessageTypeWithoutRequestID(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	var receivedFallbackMsg atomic.Bool

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send unknown message type without request_id
		unknownMsg := WebSocketMessage{
			Type:      "unknown_control_message",
			RequestID: "", // No request ID
			Payload:   map[string]interface{}{},
			Timestamp: time.Now(),
		}
		conn.WriteJSON(unknownMsg)

		// Wait for protocol_fallback response
		var fallbackMsg WebSocketMessage
		err = conn.ReadJSON(&fallbackMsg)
		if err == nil && fallbackMsg.Type == "protocol_fallback" {
			receivedFallbackMsg.Store(true)
		}

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)

	// Wait for unknown message and fallback response
	time.Sleep(300 * time.Millisecond)

	// Verify fallback message was sent even without request_id
	assert.True(t, receivedFallbackMsg.Load())

	// Cleanup
	client.Close()
}

func TestNewWebSocketClientWithGateway(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name        string
		gatewayURL  string
		token       string
		clusterUUID string
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid gateway configuration",
			gatewayURL:  "wss://gateway.example.com/ws",
			token:       "test-token",
			clusterUUID: "cluster-uuid-123",
			wantErr:     false,
		},
		{
			name:        "missing gateway URL",
			gatewayURL:  "",
			token:       "test-token",
			clusterUUID: "cluster-uuid-123",
			wantErr:     true,
			errContains: "gateway URL is required",
		},
		{
			name:        "missing token",
			gatewayURL:  "wss://gateway.example.com/ws",
			token:       "",
			clusterUUID: "cluster-uuid-123",
			wantErr:     true,
			errContains: "authentication token is required",
		},
		{
			name:        "missing cluster UUID",
			gatewayURL:  "wss://gateway.example.com/ws",
			token:       "test-token",
			clusterUUID: "",
			wantErr:     true,
			errContains: "cluster UUID is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewWebSocketClientWithGateway(tt.gatewayURL, tt.token, "agent-123", tt.clusterUUID, "test-version", types.DefaultTimeouts(), nil, logger)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, client)
			} else {
				require.NoError(t, err)
				require.NotNil(t, client)
				assert.True(t, client.gatewayMode)
				assert.Equal(t, tt.gatewayURL, client.gatewayURL)
				assert.Equal(t, tt.clusterUUID, client.clusterUUID)
			}
		})
	}
}

func TestWebSocketClient_ConnectToGateway(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock gateway server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Token should be in Authorization header, NOT in query params (security improvement)
		token := r.URL.Query().Get("token")
		assert.Empty(t, token, "token should NOT be in query params for security")

		// cluster_uuid should still be in query params for routing
		clusterUUID := r.URL.Query().Get("cluster_uuid")
		assert.Equal(t, "cluster-uuid-123", clusterUUID, "cluster_uuid query parameter")

		// Verify Authorization header has the token
		authHeader := r.Header.Get("Authorization")
		assert.Equal(t, "Bearer test-token", authHeader, "token should be in Authorization header")

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

	client, err := NewWebSocketClientWithGateway(wsURL, "test-token", "agent-123", "cluster-uuid-123", "test-version", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.ConnectToGateway()
	require.NoError(t, err)

	assert.True(t, client.IsConnected())

	// Cleanup
	client.Close()
}

func TestWebSocketClient_ConnectToGateway_NotInGatewayMode(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create regular client (not gateway mode)
	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	// Should fail because not in gateway mode
	err = client.ConnectToGateway()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in gateway mode")
}

func TestClient_RegisterAgent_WithGatewayRedirect(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock gateway server
	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify gateway-specific parameters
		// Token should be in Authorization header, not query params
		authHeader := r.Header.Get("Authorization")
		assert.NotEmpty(t, authHeader, "Authorization header should be set")

		clusterUUID := r.URL.Query().Get("cluster_uuid")
		assert.NotEmpty(t, clusterUUID, "cluster_uuid should be set")

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open for heartbeats
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}))
	defer gatewayServer.Close()

	gatewayWsURL := "ws" + strings.TrimPrefix(gatewayServer.URL, "http")

	// Create mock controller server that returns gateway URL
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read register message
		var msg WebSocketMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		if msg.Type == "register" {
			// Send register_success with gateway_ws_url
			response := WebSocketMessage{
				Type:      "register_success",
				RequestID: msg.RequestID,
				Payload: map[string]interface{}{
					"cluster_id":     "test-cluster-id",
					"cluster_uuid":   "test-cluster-uuid",
					"name":           "test-cluster",
					"status":         "active",
					"gateway_ws_url": gatewayWsURL,
				},
				Timestamp: time.Now(),
			}
			conn.WriteJSON(response)
		}

		// Wait a bit for the client to process and close
		time.Sleep(100 * time.Millisecond)
	}))
	defer controllerServer.Close()

	// Convert http:// to https:// for NewClient
	controllerURL := strings.Replace(controllerServer.URL, "http://", "http://", 1)

	client, err := NewClient(controllerURL, "test-token", "agent-123", "test-version", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)
	defer client.Close()

	// Register should detect gateway URL and reconnect
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agent := &types.Agent{
		ID:   "agent-123",
		Name: "test-cluster",
	}

	result, err := client.RegisterAgent(ctx, agent)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "test-cluster-id", result.ClusterID)
	assert.Equal(t, "test-cluster-uuid", result.ClusterUUID)
	assert.Equal(t, gatewayWsURL, result.GatewayWSURL)

	// The client should now be connected to the gateway
	assert.True(t, client.IsConnected())
}

func TestParseRegistrationResponse_WithGatewayWSURL(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client := &WebSocketClient{
		logger: logger,
	}

	msg := &WebSocketMessage{
		Type:      "register_success",
		RequestID: "test-req-id",
		Payload: map[string]interface{}{
			"cluster_id":     "cluster-123",
			"cluster_uuid":   "cluster-uuid-456",
			"name":           "my-cluster",
			"status":         "active",
			"gateway_ws_url": "wss://gateway.pipeops.io/ws",
			"workspace_id":   float64(789),
		},
	}

	result, err := client.parseRegistrationResponse(msg)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "cluster-123", result.ClusterID)
	assert.Equal(t, "cluster-uuid-456", result.ClusterUUID)
	assert.Equal(t, "my-cluster", result.Name)
	assert.Equal(t, "active", result.Status)
	assert.Equal(t, "wss://gateway.pipeops.io/ws", result.GatewayWSURL)
	assert.Equal(t, 789, result.WorkspaceID)
}
