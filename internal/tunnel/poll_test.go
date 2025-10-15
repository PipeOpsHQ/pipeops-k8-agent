package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPollService(t *testing.T) {
	logger := logrus.New()
	config := &PollConfig{
		APIURL:            "https://api.test.com",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
		Forwards: []PortForward{
			{Name: "k8s-api", LocalAddr: "localhost:6443"},
		},
	}

	ps := NewPollService(config, logger)
	assert.NotNil(t, ps)
	assert.Equal(t, config, ps.config)
	assert.NotNil(t, ps.httpClient)
	assert.NotNil(t, ps.logger)
	assert.NotNil(t, ps.activityChan)
	assert.NotNil(t, ps.stopChan)
}

func TestPollConfig_Structure(t *testing.T) {
	config := &PollConfig{
		APIURL:            "https://api.example.com",
		AgentID:           "agent-123",
		Token:             "token-456",
		PollInterval:      10 * time.Second,
		InactivityTimeout: 3 * time.Minute,
		Forwards: []PortForward{
			{Name: "test", LocalAddr: "localhost:8080"},
		},
	}

	assert.Equal(t, "https://api.example.com", config.APIURL)
	assert.Equal(t, "agent-123", config.AgentID)
	assert.Equal(t, "token-456", config.Token)
	assert.Equal(t, 10*time.Second, config.PollInterval)
	assert.Len(t, config.Forwards, 1)
}

func TestPortForward_Structure(t *testing.T) {
	fwd := PortForward{
		Name:      "kubernetes-api",
		LocalAddr: "localhost:6443",
	}

	assert.Equal(t, "kubernetes-api", fwd.Name)
	assert.Equal(t, "localhost:6443", fwd.LocalAddr)
}

func TestPollStatusResponse_Structure(t *testing.T) {
	resp := PollStatusResponse{
		Status:            TunnelStatusRequired,
		PollFrequency:     5,
		TunnelServerAddr:  "tunnel.example.com:22",
		ServerFingerprint: "SHA256:abc123",
		Forwards: []ForwardAllocation{
			{Name: "k8s-api", RemotePort: 8000},
		},
		Credentials: "base64encodedcreds",
	}

	assert.Equal(t, TunnelStatusRequired, resp.Status)
	assert.Equal(t, 5, resp.PollFrequency)
	assert.Len(t, resp.Forwards, 1)
	assert.Equal(t, 8000, resp.Forwards[0].RemotePort)
}

func TestForwardAllocation_Structure(t *testing.T) {
	alloc := ForwardAllocation{
		Name:       "kubernetes-api",
		RemotePort: 8000,
	}

	assert.Equal(t, "kubernetes-api", alloc.Name)
	assert.Equal(t, 8000, alloc.RemotePort)
}

func TestTunnelStatusConstants(t *testing.T) {
	assert.Equal(t, "IDLE", TunnelStatusIdle)
	assert.Equal(t, "REQUIRED", TunnelStatusRequired)
	assert.Equal(t, "ACTIVE", TunnelStatusActive)
}

func TestPollService_RecordActivity(t *testing.T) {
	logger := logrus.New()
	config := &PollConfig{
		APIURL:            "https://api.test.com",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)

	// RecordActivity should not panic
	assert.NotPanics(t, func() {
		ps.RecordActivity()
	})
}

func TestPollService_Stop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	config := &PollConfig{
		APIURL:            "https://api.test.com",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)

	// Stop should not panic
	assert.NotPanics(t, func() {
		ps.Stop()
	})
}

func TestPollService_DecryptCredentials(t *testing.T) {
	logger := logrus.New()
	config := &PollConfig{
		APIURL:            "https://api.test.com",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)

	// Test base64 decoding
	testCreds := "test-credentials"
	encoded := base64.StdEncoding.EncodeToString([]byte(testCreds))

	decoded, err := ps.decryptCredentials(encoded)
	assert.NoError(t, err)
	assert.Equal(t, testCreds, decoded)
}

func TestPollService_DecryptCredentials_InvalidBase64(t *testing.T) {
	logger := logrus.New()
	config := &PollConfig{
		APIURL:            "https://api.test.com",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)

	// Test invalid base64
	_, err := ps.decryptCredentials("not-valid-base64!@#")
	assert.Error(t, err)
}

func TestPollService_Poll_404(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config := &PollConfig{
		APIURL:            server.URL,
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)
	ctx := context.Background()

	// Poll should handle 404 gracefully
	assert.NotPanics(t, func() {
		ps.poll(ctx)
	})
}

func TestPollService_Poll_Success(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := PollStatusResponse{
			Status:        TunnelStatusIdle,
			PollFrequency: 10,
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := &PollConfig{
		APIURL:            server.URL,
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      5 * time.Second,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)
	ctx := context.Background()

	// Poll should succeed
	assert.NotPanics(t, func() {
		ps.poll(ctx)
	})
}

func TestPollService_StartStop(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	config := &PollConfig{
		APIURL:            "https://api.test.com",
		AgentID:           "test-agent",
		Token:             "test-token",
		PollInterval:      100 * time.Millisecond,
		InactivityTimeout: 5 * time.Minute,
	}

	ps := NewPollService(config, logger)
	ctx := context.Background()

	// Start service
	err := ps.Start(ctx)
	require.NoError(t, err)

	// Let it run briefly
	time.Sleep(200 * time.Millisecond)

	// Stop service
	ps.Stop()
}

func TestPollStatusResponse_JSON(t *testing.T) {
	resp := PollStatusResponse{
		Status:            TunnelStatusRequired,
		PollFrequency:     5,
		TunnelServerAddr:  "tunnel.example.com:22",
		ServerFingerprint: "SHA256:abc123",
		Forwards: []ForwardAllocation{
			{Name: "k8s-api", RemotePort: 8000},
			{Name: "kubelet", RemotePort: 8001},
		},
		Credentials: "base64encodedcreds",
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotEmpty(t, jsonData)

	// Test JSON unmarshaling
	var decoded PollStatusResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)
	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.PollFrequency, decoded.PollFrequency)
	assert.Len(t, decoded.Forwards, 2)
}
