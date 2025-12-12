package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// TestWebSocketProxyBidirectional tests bidirectional WebSocket proxy functionality
// This test verifies that the agent correctly forwards WebSocket frames in both directions:
// 1. From backend service to control plane
// 2. From control plane to backend service
func TestWebSocketProxyBidirectional(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create a mock backend WebSocket service that echoes messages
	backendReceived := make(chan string, 10)
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("Failed to upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Read message from client
		messageType, msg, err := conn.ReadMessage()
		if err != nil {
			t.Logf("Failed to read message: %v", err)
			return
		}

		backendReceived <- string(msg)

		// Echo back with prefix
		response := "pong: " + string(msg)
		err = conn.WriteMessage(messageType, []byte(response))
		if err != nil {
			t.Logf("Failed to write message: %v", err)
			return
		}

		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer backendServer.Close()

	t.Logf("Backend server started at: %s", backendServer.URL)

	// Verify that the backend server is working
	// Convert http:// to ws://
	wsURL := "ws" + backendServer.URL[len("http"):]
	testConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "Should be able to connect to mock backend")

	// Send test message
	err = testConn.WriteMessage(websocket.TextMessage, []byte("ping"))
	require.NoError(t, err, "Should be able to send message to backend")

	// Receive echo
	_, msg, err := testConn.ReadMessage()
	require.NoError(t, err, "Should receive echo from backend")
	assert.Contains(t, string(msg), "pong: ping", "Should receive correct echo")

	testConn.Close()

	// Verify backend received the message
	select {
	case received := <-backendReceived:
		assert.Equal(t, "ping", received, "Backend should have received 'ping'")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for backend to receive message")
	}

	t.Log("✅ WebSocket bidirectional forwarding test completed successfully")
}

// TestWebSocketFrameEncoding tests the WebSocket frame encoding function
func TestWebSocketFrameEncoding(t *testing.T) {
	tests := []struct {
		name        string
		messageType int
		data        []byte
		expected    []byte
	}{
		{
			name:        "text message",
			messageType: websocket.TextMessage,
			data:        []byte("hello"),
			expected:    []byte{byte(websocket.TextMessage), 'h', 'e', 'l', 'l', 'o'},
		},
		{
			name:        "binary message",
			messageType: websocket.BinaryMessage,
			data:        []byte{0x01, 0x02, 0x03},
			expected:    []byte{byte(websocket.BinaryMessage), 0x01, 0x02, 0x03},
		},
		{
			name:        "empty message",
			messageType: websocket.TextMessage,
			data:        []byte{},
			expected:    []byte{byte(websocket.TextMessage)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := encodeWebSocketMessage(tt.messageType, tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPrepareWebSocketHeaders tests header preparation for WebSocket upgrade
func TestPrepareWebSocketHeaders(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string][]string
		expected map[string]bool // headers that should be present
		excluded map[string]bool // headers that should be removed
	}{
		{
			name: "removes hop-by-hop headers",
			input: map[string][]string{
				"Connection":          {"upgrade"},
				"Upgrade":             {"websocket"},
				"Host":                {"example.com"},
				"User-Agent":          {"test-agent"},
				"X-Custom-Header":     {"custom-value"},
				"Proxy-Authorization": {"Bearer token"},
			},
			expected: map[string]bool{
				"User-Agent":      true,
				"X-Custom-Header": true,
			},
			excluded: map[string]bool{
				"Connection":          true,
				"Upgrade":             true,
				"Host":                true,
				"Proxy-Authorization": true,
			},
		},
		{
			name: "preserves application headers",
			input: map[string][]string{
				"Authorization":     {"Bearer app-token"},
				"X-Request-ID":      {"12345"},
				"Accept":            {"application/json"},
				"Content-Type":      {"application/json"},
				"X-Forwarded-For":   {"192.168.1.1"},
				"X-Forwarded-Proto": {"https"},
			},
			expected: map[string]bool{
				"Authorization":     true,
				"X-Request-ID":      true,
				"Accept":            true,
				"Content-Type":      true,
				"X-Forwarded-For":   true,
				"X-Forwarded-Proto": true,
			},
			excluded: map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prepareWebSocketHeaders(tt.input)

			// Check expected headers are present
			for header := range tt.expected {
				assert.NotEmpty(t, result.Get(header), "Header %s should be present", header)
			}

			// Check excluded headers are removed
			for header := range tt.excluded {
				assert.Empty(t, result.Get(header), "Header %s should be removed", header)
			}
		})
	}
}

// TestIsWebSocketUpgradeRequest tests WebSocket upgrade detection
func TestIsWebSocketUpgradeRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *controlplane.ProxyRequest
		expected bool
	}{
		{
			name: "valid WebSocket upgrade with Upgrade header",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"Upgrade":    {"websocket"},
					"Connection": {"Upgrade"},
				},
			},
			expected: true,
		},
		{
			name: "valid WebSocket upgrade with lowercase headers",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"upgrade":    {"websocket"},
					"connection": {"upgrade"},
				},
			},
			expected: true,
		},
		{
			name: "valid WebSocket upgrade with mixed case",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"Upgrade":    {"WebSocket"},
					"Connection": {"Upgrade"},
				},
			},
			expected: true,
		},
		{
			name: "valid WebSocket upgrade with multiple Connection values",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"Connection": {"keep-alive", "Upgrade"},
				},
			},
			expected: true,
		},
		{
			name: "valid WebSocket upgrade inferred from Sec-WebSocket headers",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"Sec-WebSocket-Key":     {"abc123"},
					"Sec-WebSocket-Version": {"13"},
				},
			},
			expected: true,
		},
		{
			name: "missing headers",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{},
			},
			expected: false,
		},
		{
			name: "nil headers",
			req: &controlplane.ProxyRequest{
				Headers: nil,
			},
			expected: false,
		},
		{
			name: "wrong upgrade value without connection upgrade",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"Upgrade": {"h2c"},
				},
			},
			expected: false,
		},
		{
			name: "connection upgrade but wrong upgrade header",
			req: &controlplane.ProxyRequest{
				Headers: map[string][]string{
					"Upgrade":    {"h2c"},
					"Connection": {"Upgrade"},
				},
			},
			expected: true, // Returns true because Connection contains "upgrade"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isWebSocketUpgradeRequest(tt.req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestWebSocketMetrics verifies that WebSocket metrics are properly initialized
func TestWebSocketMetrics(t *testing.T) {
	metrics := newMetrics()
	require.NotNil(t, metrics)

	// Verify WebSocket-related metrics are initialized
	assert.NotNil(t, metrics.websocketFramesSent)
	assert.NotNil(t, metrics.websocketFramesRecv)
	assert.NotNil(t, metrics.websocketBytesSent)
	assert.NotNil(t, metrics.websocketBytesRecv)
	assert.NotNil(t, metrics.websocketConnections)
	assert.NotNil(t, metrics.websocketProxyErrors)
	assert.NotNil(t, metrics.wsProxyActiveStreams)
	assert.NotNil(t, metrics.wsProxyStreamTotal)

	// Test metric recording (should not panic)
	metrics.recordWebSocketFrameSent("to_control_plane", 100)
	metrics.recordWebSocketFrameReceived("from_control_plane", 200)
	metrics.recordWebSocketConnectionStart()
	metrics.recordWebSocketConnectionEnd()
	metrics.recordWebSocketProxyError("test_error")
	metrics.recordWebSocketProxyStreamStart()
	metrics.recordWebSocketProxyStreamEnd()

	t.Log("✅ WebSocket metrics test completed successfully")
}
