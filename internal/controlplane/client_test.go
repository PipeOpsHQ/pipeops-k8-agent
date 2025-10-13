package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	logger := logrus.New()

	tests := []struct {
		name    string
		apiURL  string
		token   string
		agentID string
		wantErr bool
	}{
		{
			name:    "valid client",
			apiURL:  "https://api.example.com",
			token:   "test-token",
			agentID: "agent-123",
			wantErr: false,
		},
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
				assert.Equal(t, tt.apiURL, client.apiURL)
				assert.Equal(t, tt.token, client.token)
				assert.Equal(t, tt.agentID, client.agentID)
			}
		})
	}
}

func TestRegisterAgent(t *testing.T) {
	logger := logrus.New()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agents/register", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", "agent-123", logger)
	require.NoError(t, err)

	agent := &types.Agent{
		ID:          "agent-123",
		Name:        "test-agent",
		ClusterName: "test-cluster",
		Version:     "1.0.0",
		Status:      types.AgentStatusRegistering,
	}

	ctx := context.Background()
	err = client.RegisterAgent(ctx, agent)
	assert.NoError(t, err)
}

func TestSendHeartbeat(t *testing.T) {
	logger := logrus.New()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agents/agent-123/heartbeat", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", "agent-123", logger)
	require.NoError(t, err)

	heartbeat := &HeartbeatRequest{
		AgentID:     "agent-123",
		ClusterName: "test-cluster",
		Status:      "connected",
		ProxyStatus: "direct",
		Timestamp:   time.Now(),
	}

	ctx := context.Background()
	err = client.SendHeartbeat(ctx, heartbeat)
	assert.NoError(t, err)
}

// Note: TestReportStatus, TestFetchCommands, and TestSendCommandResult removed.
// These methods are no longer needed with Portainer-style architecture where
// the control plane accesses K8s directly through the tunnel.

func TestPing(t *testing.T) {
	logger := logrus.New()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/health", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "healthy"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", "agent-123", logger)
	require.NoError(t, err)

	ctx := context.Background()
	err = client.Ping(ctx)
	assert.NoError(t, err)
}

func TestErrorHandling(t *testing.T) {
	logger := logrus.New()

	// Create mock server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "Internal server error"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", "agent-123", logger)
	require.NoError(t, err)

	ctx := context.Background()

	// Test registration error
	agent := &types.Agent{ID: "agent-123"}
	err = client.RegisterAgent(ctx, agent)
	assert.Error(t, err)

	// Test heartbeat error
	heartbeat := &HeartbeatRequest{AgentID: "agent-123"}
	err = client.SendHeartbeat(ctx, heartbeat)
	assert.Error(t, err)

	// Note: Status report error test removed - method no longer exists
}
