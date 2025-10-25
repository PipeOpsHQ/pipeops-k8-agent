package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTestServerConfig() *types.Config {
	return &types.Config{
		Agent: types.AgentConfig{
			ID:          "test-agent",
			Name:        "test-agent",
			Version:     "1.0.0-test",
			Port:        8080,
			ClusterName: "test-cluster",
			Debug:       false,
		},
		PipeOps: types.PipeOpsConfig{
			APIURL: "https://api.test.pipeops.io",
			Token:  "test-token",
		},
	}
}

func TestNewServer(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()

	server := NewServer(config, logger)

	assert.NotNil(t, server)
	assert.Equal(t, config, server.config)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.features)
	assert.NotNil(t, server.status)
	assert.False(t, server.status.Connected)
	assert.False(t, server.status.Registered)
}

func TestServerSetActivityRecorder(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	called := false
	recorder := func() {
		called = true
	}

	server.SetActivityRecorder(recorder)
	assert.NotNil(t, server.activityRecorder)

	// Call the recorder
	server.activityRecorder()
	assert.True(t, called)
}

func TestHandleHealth(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/health", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "healthy", response["status"])
	assert.Equal(t, "1.0.0-test", response["version"])
	assert.NotNil(t, response["timestamp"])
}

func TestHandleReady(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/ready", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ready", response["status"])
	assert.NotNil(t, response["timestamp"])
	assert.NotNil(t, response["tunnel"])
}

func TestHandleVersion(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/version", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotNil(t, response["version"])
	assert.NotNil(t, response["timestamp"])
	assert.NotNil(t, response["go_version"])
}

func TestHandleMetrics(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotNil(t, response["tunnel"])
	assert.NotNil(t, response["timestamp"])
}

func TestHandleDetailedHealth(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/health/detailed", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "healthy", response["status"])
	assert.NotNil(t, response["agent"])
	assert.NotNil(t, response["tunnel"])
	assert.NotNil(t, response["features"])
	assert.NotNil(t, response["runtime"])
}

func TestHandleFeatures(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/status/features", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	features, ok := response["features"].(map[string]interface{})
	assert.True(t, ok)
	assert.NotNil(t, features)
	assert.NotNil(t, response["timestamp"])
}

func TestHandleRuntimeMetrics(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/metrics/runtime", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotNil(t, response["memory"])
	assert.NotNil(t, response["gc"])
	assert.NotNil(t, response["runtime"])
	assert.NotNil(t, response["uptime"])
}

func TestHandleConnectivityTest(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/status/connectivity", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.NotNil(t, response["overall_status"])
	assert.NotNil(t, response["tests"])
	assert.NotNil(t, response["timestamp"])
}

func TestDetectFeatures(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	server.detectFeatures()

	assert.NotNil(t, server.features["portainer_tunnel"])
	assert.NotNil(t, server.features["multi_port_forward"])
	assert.NotNil(t, server.features["direct_k8s_access"])
	assert.NotNil(t, server.features["metrics"])
	assert.NotNil(t, server.features["health_monitoring"])
}

func TestTunnelActivityMiddleware(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	called := false
	server.SetActivityRecorder(func() {
		called = true
	})

	middleware := server.tunnelActivityMiddleware()

	// Create a test context
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	// Call middleware (it will call c.Next() internally)
	middleware(c)

	assert.True(t, called)
}

func TestHandleKubernetesProxyUnavailable(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/default/pods", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "kubernetes proxy unavailable", response["error"])
}

func TestHandleKubernetesProxySuccess(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	proxy := &fakeKubernetesProxy{
		responseStatus:  http.StatusOK,
		responseHeaders: http.Header{"Content-Type": []string{"application/json"}},
		responseBody:    `{"ok":true}`,
	}
	server.SetKubernetesProxy(proxy)
	server.setupRoutes()

	body := bytes.NewBufferString("payload")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/default/services/http:prometheus-server:/proxy/metrics?token=test", body)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"ok":true}`, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.MethodPost, proxy.seenMethod)
	assert.Equal(t, "/api/v1/namespaces/default/services/http:prometheus-server:/proxy/metrics", proxy.seenPath)
	assert.Equal(t, "token=test", proxy.seenRawQuery)
	assert.Equal(t, "payload", proxy.seenBody)
	assert.Equal(t, "application/json", proxy.seenHeaders["Content-Type"][0])
}

func TestHandleKubernetesProxyError(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	proxy := &fakeKubernetesProxy{err: errors.New("boom")}
	server.SetKubernetesProxy(proxy)
	server.setupRoutes()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/apis/apps/v1/deployments", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)

	var response map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "kubernetes proxy request failed", response["error"])
	assert.Equal(t, "boom", response["message"])
}

type fakeKubernetesProxy struct {
	responseStatus  int
	responseHeaders http.Header
	responseBody    string
	err             error

	seenMethod   string
	seenPath     string
	seenRawQuery string
	seenHeaders  map[string][]string
	seenBody     string
}

func (f *fakeKubernetesProxy) ProxyRequest(ctx context.Context, method, path, rawQuery string, headers map[string][]string, body io.ReadCloser) (int, http.Header, io.ReadCloser, error) {
	f.seenMethod = method
	f.seenPath = path
	f.seenRawQuery = rawQuery
	f.seenHeaders = headers

	if body != nil {
		data, _ := io.ReadAll(body)
		f.seenBody = string(data)
	}

	if f.err != nil {
		return 0, nil, nil, f.err
	}

	var respBody io.ReadCloser
	if f.responseBody != "" {
		respBody = io.NopCloser(strings.NewReader(f.responseBody))
	}

	status := f.responseStatus
	if status == 0 {
		status = http.StatusOK
	}

	headersCopy := make(http.Header, len(f.responseHeaders))
	for key, values := range f.responseHeaders {
		copied := make([]string, len(values))
		copy(copied, values)
		headersCopy[key] = copied
	}

	return status, headersCopy, respBody, nil
}

func TestServerStartStop(t *testing.T) {
	config := getTestServerConfig()
	config.Agent.Port = 0 // Use random available port
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise

	server := NewServer(config, logger)

	// Start server
	err := server.Start()
	require.NoError(t, err)

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Stop server
	err = server.Stop()
	assert.NoError(t, err)
}

func TestAgentStatus(t *testing.T) {
	status := &AgentStatus{
		Connected:      true,
		Registered:     true,
		LastHeartbeat:  time.Now(),
		PublicIP:       "1.2.3.4",
		ClusterAddress: "https://cluster.example.com",
	}

	assert.True(t, status.Connected)
	assert.True(t, status.Registered)
	assert.Equal(t, "1.2.3.4", status.PublicIP)
	assert.Equal(t, "https://cluster.example.com", status.ClusterAddress)
}

func TestServerContext(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	// Verify context is set up
	assert.NotNil(t, server.ctx)
	assert.NotNil(t, server.cancel)

	// Verify context can be cancelled
	done := make(chan bool)
	go func() {
		<-server.ctx.Done()
		done <- true
	}()

	server.cancel()

	select {
	case <-done:
		// Context cancelled successfully
	case <-time.After(time.Second):
		t.Fatal("Context not cancelled")
	}
}

func TestGetRuntimeMetrics(t *testing.T) {
	config := getTestServerConfig()
	logger := logrus.New()
	server := NewServer(config, logger)

	metrics := server.getRuntimeMetrics()

	assert.NotNil(t, metrics["memory"])
	assert.NotNil(t, metrics["goroutines"])
	assert.NotNil(t, metrics["uptime"])
	assert.NotNil(t, metrics["timestamp"])
}
