package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func TestNewClient(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name    string
		apiURL  string
		token   string
		agentID string
		wantErr bool
	}{
		{
			name:    "missing API URL",
			apiURL:  "",
			token:   "test-token",
			agentID: "agent-123",
			wantErr: true,
		},
		{
			name:    "missing token",
			apiURL:  "https://api.example.com",
			token:   "",
			agentID: "agent-123",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.apiURL, tt.token, tt.agentID, logger)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, client)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

func TestNewClient_WebSocketConnection(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify token in Authorization header (server-to-server authentication)
		authHeader := r.Header.Get("Authorization")
		assert.Equal(t, "Bearer test-token", authHeader)

		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewClient(wsURL, "test-token", "agent-123", logger)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.wsClient)

	// Cleanup
	client.Close()
}

func TestClient_RegisterAgent(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
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

		// Send registration response
		response := WebSocketMessage{
			Type:      "register_success",
			RequestID: msg.RequestID,
			Payload: map[string]interface{}{
				"cluster_id":   "550e8400-e29b-41d4-a716-446655440000",
				"cluster_uuid": "550e8400-e29b-41d4-a716-446655440000",
				"name":         "test-cluster",
				"status":       "registered",
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

	client, err := NewClient(wsURL, "test-token", "agent-123", logger)
	require.NoError(t, err)
	defer client.Close()

	// Test registration
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
}

func TestClient_SendHeartbeat(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	receivedHeartbeat := false

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
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
				receivedHeartbeat = true
			}
		}()

		// Keep connection open
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewClient(wsURL, "test-token", "agent-123", logger)
	require.NoError(t, err)
	defer client.Close()

	// Test heartbeat
	heartbeat := &HeartbeatRequest{
		ClusterID:    "550e8400-e29b-41d4-a716-446655440000",
		AgentID:      "agent-123",
		Status:       "healthy",
		TunnelStatus: "connected",
		Timestamp:    time.Now(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.SendHeartbeat(ctx, heartbeat)
	assert.NoError(t, err)

	// Wait for server to process
	time.Sleep(100 * time.Millisecond)
	assert.True(t, receivedHeartbeat)
}

func TestClient_Ping(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewClient(wsURL, "test-token", "agent-123", logger)
	require.NoError(t, err)
	defer client.Close()

	// Test ping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Ping(ctx)
	assert.NoError(t, err)
}

func TestClient_WebSocketNotInitialized(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create a client with nil WebSocket client (should not happen in practice)
	client := &Client{
		apiURL:   "ws://localhost",
		token:    "test-token",
		agentID:  "agent-123",
		logger:   logger,
		wsClient: nil,
	}

	ctx := context.Background()

	// Test that methods return errors when WebSocket is not initialized
	agent := &types.Agent{ID: "test"}
	_, err := client.RegisterAgent(ctx, agent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WebSocket client not initialized")

	heartbeat := &HeartbeatRequest{ClusterID: "test"}
	err = client.SendHeartbeat(ctx, heartbeat)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WebSocket client not initialized")

	err = client.Ping(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "WebSocket client not initialized")
}
