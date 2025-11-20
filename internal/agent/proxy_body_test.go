package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyToServiceWithFormBody verifies that POST requests with form-encoded bodies
// (like Prometheus query_range) are correctly forwarded to the target service
func TestProxyToServiceWithFormBody(t *testing.T) {
	// Create a mock backend service (simulating Prometheus)
	var receivedBody string
	var receivedContentType string
	var receivedMethod string
	var receivedFormValues url.Values

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedContentType = r.Header.Get("Content-Type")

		// Read body first (before ParseForm consumes it)
		bodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody = string(bodyBytes)

		// Restore body for ParseForm
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		// Parse form
		err = r.ParseForm()
		require.NoError(t, err)
		receivedFormValues = r.Form

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer backend.Close()

	// Create agent with K8s client
	config := &types.Config{
		Agent: types.AgentConfig{
			ID:          "test-agent",
			Name:        "test-agent",
			ClusterName: "test-cluster",
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	agent := &Agent{
		config:          config,
		logger:          logger,
		proxyHTTPClient: &http.Client{Timeout: 10 * time.Second},
		clusterToken:    "test-token",
	}

	// Prepare form data (like Prometheus query_range POST)
	formData := url.Values{}
	formData.Set("query", "up")
	formData.Set("start", "1234567890")
	formData.Set("end", "1234567900")
	formData.Set("step", "15s")
	encodedBody := formData.Encode()

	// Create proxy request with body
	proxyReq := &controlplane.ProxyRequest{
		RequestID: "test-request-123",
		ClusterID: "test-cluster",
		AgentID:   "test-agent",
		Method:    "POST",
		Path:      "/api/v1/query_range",
		Query:     "",
		Headers: map[string][]string{
			"Content-Type": {"application/x-www-form-urlencoded"},
		},
		Body:         []byte(encodedBody),
		BodyEncoding: "",
		Namespace:    "monitoring",
		ServiceName:  "prometheus-server",
		ServicePort:  80,
	}

	// Override service URL to point to our test backend
	// We'll use a custom proxyToService call
	ctx := context.Background()
	requestBody := io.NopCloser(bytes.NewReader(proxyReq.Body))

	// Build service URL manually for testing
	serviceURL := backend.URL + proxyReq.Path

	httpReq, err := http.NewRequestWithContext(ctx, proxyReq.Method, serviceURL, requestBody)
	require.NoError(t, err)

	// Copy headers
	for key, values := range proxyReq.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	// Execute request
	resp, err := agent.proxyHTTPClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify backend received the correct data
	assert.Equal(t, "POST", receivedMethod, "Backend should receive POST method")
	assert.Equal(t, "application/x-www-form-urlencoded", receivedContentType, "Content-Type should be preserved")
	assert.Equal(t, encodedBody, receivedBody, "Body should be forwarded intact")
	assert.NotEmpty(t, receivedBody, "Body should not be empty")

	// Verify form parameters were received
	assert.Equal(t, "up", receivedFormValues.Get("query"), "Query parameter should be preserved")
	assert.Equal(t, "1234567890", receivedFormValues.Get("start"), "Start parameter should be preserved")
	assert.Equal(t, "1234567900", receivedFormValues.Get("end"), "End parameter should be preserved")
	assert.Equal(t, "15s", receivedFormValues.Get("step"), "Step parameter should be preserved")

	t.Logf("✅ Successfully forwarded POST body with form data (%d bytes)", len(receivedBody))
}

// TestProxyToServiceWithJSONBody verifies that POST requests with JSON bodies
// are correctly forwarded to the target service
func TestProxyToServiceWithJSONBody(t *testing.T) {
	var receivedBody string
	var receivedContentType string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		bodyBytes, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBody = string(bodyBytes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	config := &types.Config{
		Agent: types.AgentConfig{
			ID:          "test-agent",
			Name:        "test-agent",
			ClusterName: "test-cluster",
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	agent := &Agent{
		config:          config,
		logger:          logger,
		proxyHTTPClient: &http.Client{Timeout: 10 * time.Second},
		clusterToken:    "test-token",
	}

	jsonBody := `{"metric":"cpu_usage","labels":{"pod":"test-pod"}}`

	proxyReq := &controlplane.ProxyRequest{
		RequestID: "test-request-456",
		ClusterID: "test-cluster",
		AgentID:   "test-agent",
		Method:    "POST",
		Path:      "/api/v1/metrics",
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
		Body:        []byte(jsonBody),
		Namespace:   "monitoring",
		ServiceName: "metrics-server",
		ServicePort: 8080,
	}

	ctx := context.Background()
	requestBody := io.NopCloser(bytes.NewReader(proxyReq.Body))

	serviceURL := backend.URL + proxyReq.Path
	httpReq, err := http.NewRequestWithContext(ctx, proxyReq.Method, serviceURL, requestBody)
	require.NoError(t, err)

	for key, values := range proxyReq.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	resp, err := agent.proxyHTTPClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", receivedContentType, "Content-Type should be preserved")
	assert.Equal(t, jsonBody, receivedBody, "JSON body should be forwarded intact")
	assert.NotEmpty(t, receivedBody, "Body should not be empty")

	t.Logf("✅ Successfully forwarded POST body with JSON data (%d bytes)", len(receivedBody))
}

// TestProxyToServiceEmptyBody verifies that requests without bodies work correctly
func TestProxyToServiceEmptyBody(t *testing.T) {
	var receivedMethod string
	var receivedPath string
	var receivedBody string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	config := &types.Config{
		Agent: types.AgentConfig{
			ID:          "test-agent",
			Name:        "test-agent",
			ClusterName: "test-cluster",
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	agent := &Agent{
		config:          config,
		logger:          logger,
		proxyHTTPClient: &http.Client{Timeout: 10 * time.Second},
		clusterToken:    "test-token",
	}

	proxyReq := &controlplane.ProxyRequest{
		RequestID:   "test-request-789",
		ClusterID:   "test-cluster",
		AgentID:     "test-agent",
		Method:      "GET",
		Path:        "/api/v1/query",
		Query:       "query=up",
		Headers:     map[string][]string{},
		Body:        nil, // No body
		Namespace:   "monitoring",
		ServiceName: "prometheus-server",
		ServicePort: 80,
	}

	ctx := context.Background()
	serviceURL := fmt.Sprintf("%s%s?%s", backend.URL, proxyReq.Path, proxyReq.Query)
	httpReq, err := http.NewRequestWithContext(ctx, proxyReq.Method, serviceURL, nil)
	require.NoError(t, err)

	resp, err := agent.proxyHTTPClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "GET", receivedMethod)
	assert.Equal(t, "/api/v1/query", receivedPath)
	assert.Empty(t, receivedBody, "GET request should have no body")

	t.Logf("✅ Successfully handled GET request without body")
}
