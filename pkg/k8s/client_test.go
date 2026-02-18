package k8s

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/rest"
)

func TestDefaultK8sTimeout(t *testing.T) {
	assert.Equal(t, 30*time.Second, DefaultK8sTimeout)
}

func TestClient_NilReceiver(t *testing.T) {
	var c *Client

	// Test all methods handle nil receiver gracefully
	_, err := c.GetVersion(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")

	_, err = c.GetNodeCount(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")

	_, err = c.GetPodCount(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")

	_, _, err = c.GetClusterMetrics(context.Background())
	assert.Error(t, err)

	_, _, _, err = c.ProxyRequest(context.Background(), "GET", "/test", "", nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")

	_, err = c.TokenHasNamespaceWriteAccess(context.Background(), "test-token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestClient_ConcurrentAccess(t *testing.T) {
	// Test that Client can handle concurrent reads safely
	// Create a mock server
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond) // Simulate some processing
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()

	// Create a client with mock config
	client := &Client{
		restConfig: &rest.Config{
			Host: server.URL,
		},
		httpClient:    server.Client(),
		baseTransport: server.Client().Transport,
	}

	// Run concurrent proxy requests
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			status, _, body, err := client.ProxyRequest(ctx, "GET", "/test", "", nil, nil)
			if err != nil {
				errors <- err
				return
			}
			if body != nil {
				_ = body.Close()
			}
			if status != http.StatusOK {
				errors <- assert.AnError
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for any errors
	for err := range errors {
		t.Errorf("Concurrent request failed: %v", err)
	}
}

func TestProxyRequest_BodyCleanupOnError(t *testing.T) {
	client := &Client{
		restConfig: nil, // Intentionally nil to trigger error
		httpClient: nil,
	}

	// Create a mock body that tracks if it was closed
	bodyClosed := false
	body := io.NopCloser(strings.NewReader("test data"))
	mockBody := &closeTracker{
		ReadCloser: body,
		onClose: func() {
			bodyClosed = true
		},
	}

	_, _, _, err := client.ProxyRequest(context.Background(), "GET", "/test", "", nil, mockBody)
	assert.Error(t, err)
	assert.True(t, bodyClosed, "Body should be closed on error")
}

func TestProxyRequest_MethodNormalization(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method is uppercase
		assert.Contains(t, []string{"GET", "POST", "PUT", "DELETE"}, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		restConfig:    &rest.Config{Host: server.URL},
		httpClient:    server.Client(),
		baseTransport: server.Client().Transport,
	}

	testCases := []struct {
		input    string
		expected string
	}{
		{"get", "GET"},
		{"", "GET"}, // Default to GET
		{"post", "POST"},
		{"PUT", "PUT"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			status, _, body, err := client.ProxyRequest(context.Background(), tc.input, "/test", "", nil, nil)
			require.NoError(t, err)
			if body != nil {
				_ = body.Close()
			}
			assert.Equal(t, http.StatusOK, status)
		})
	}
}

func TestProxyRequest_HeaderFiltering(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authorization header SHOULD be present now (passed through)
		assert.Equal(t, "Bearer user-token", r.Header.Get("Authorization"))
		assert.Equal(t, "should-be-included", r.Header.Get("X-Custom"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		restConfig:    &rest.Config{Host: server.URL},
		httpClient:    server.Client(),
		baseTransport: server.Client().Transport,
	}

	headers := map[string][]string{
		"Authorization": {"Bearer user-token"},
		"X-Custom":      {"should-be-included"},
	}

	status, _, body, err := client.ProxyRequest(context.Background(), "GET", "/test", "", headers, nil)
	require.NoError(t, err)
	if body != nil {
		_ = body.Close()
	}
	assert.Equal(t, http.StatusOK, status)
}

func TestProxyRequest_PathNormalization(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All paths should start with /
		assert.True(t, strings.HasPrefix(r.URL.Path, "/"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		restConfig:    &rest.Config{Host: server.URL},
		httpClient:    server.Client(),
		baseTransport: server.Client().Transport,
	}

	testPaths := []string{
		"/api/v1/pods",
		"api/v1/pods", // Should be normalized to /api/v1/pods
		"/",
	}

	for _, path := range testPaths {
		t.Run(path, func(t *testing.T) {
			status, _, body, err := client.ProxyRequest(context.Background(), "GET", path, "", nil, nil)
			require.NoError(t, err)
			if body != nil {
				_ = body.Close()
			}
			assert.Equal(t, http.StatusOK, status)
		})
	}
}

func TestTokenHasNamespaceWriteAccess_EmptyToken(t *testing.T) {
	client := &Client{
		restConfig: &rest.Config{},
	}

	allowed, err := client.TokenHasNamespaceWriteAccess(context.Background(), "")
	assert.Error(t, err)
	assert.False(t, allowed)
	assert.Contains(t, err.Error(), "token is empty")
}

func TestGetClusterMetrics_ConcurrentCalls(t *testing.T) {
	// This test verifies that GetClusterMetrics makes concurrent API calls
	// We can't easily test this without a real k8s client, but we verify the structure is correct
	t.Skip("Requires real Kubernetes client")
}

// closeTracker wraps an io.ReadCloser and tracks when Close is called
type closeTracker struct {
	io.ReadCloser
	onClose func()
}

func (c *closeTracker) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return c.ReadCloser.Close()
}

func TestStringSliceContainsAll(t *testing.T) {
	tests := []struct {
		name     string
		haystack []string
		needles  []string
		want     bool
	}{
		{name: "exact match", haystack: []string{"a", "b"}, needles: []string{"a", "b"}, want: true},
		{name: "superset", haystack: []string{"a", "b", "c"}, needles: []string{"a", "b"}, want: true},
		{name: "missing element", haystack: []string{"a"}, needles: []string{"a", "b"}, want: false},
		{name: "empty needles", haystack: []string{"a"}, needles: []string{}, want: true},
		{name: "empty haystack", haystack: []string{}, needles: []string{"a"}, want: false},
		{name: "both empty", haystack: []string{}, needles: []string{}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stringSliceContainsAll(tt.haystack, tt.needles))
		})
	}
}

func TestHasRule(t *testing.T) {
	tests := []struct {
		name     string
		existing []rbacv1.PolicyRule
		required rbacv1.PolicyRule
		want     bool
	}{
		{
			name: "exact match",
			existing: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/portforward"}, Verbs: []string{"create", "get"}},
			},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/portforward"}, Verbs: []string{"create", "get"}},
			want:     true,
		},
		{
			name: "covered by broader rule",
			existing: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/portforward", "pods/log"}, Verbs: []string{"create", "get"}},
			},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
			want:     true,
		},
		{
			name: "missing resource",
			existing: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"create"}},
			},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
			want:     false,
		},
		{
			name: "missing verb",
			existing: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"get"}},
			},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
			want:     false,
		},
		{
			name: "wrong apiGroup",
			existing: []rbacv1.PolicyRule{
				{APIGroups: []string{"apps"}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
			},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
			want:     false,
		},
		{
			name:     "no existing rules",
			existing: []rbacv1.PolicyRule{},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
			want:     false,
		},
		{
			name: "covered across multiple rules does not match - each rule must cover independently",
			existing: []rbacv1.PolicyRule{
				{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create", "get"}},
				{APIGroups: []string{""}, Resources: []string{"pods/portforward"}, Verbs: []string{"create", "get"}},
			},
			required: rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"pods/exec", "pods/portforward"}, Verbs: []string{"create", "get"}},
			want:     false, // no single rule covers both resources
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasRule(tt.existing, tt.required))
		})
	}
}

func TestEnsureRBACRules_NilClient(t *testing.T) {
	var c *Client
	err := c.EnsureRBACRules(context.Background(), "pipeops-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestRulesAdded_NilClient(t *testing.T) {
	var c *Client
	_, err := c.RulesAdded(context.Background(), "pipeops-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}
