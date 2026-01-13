package controlplane

import (
	"context"
	"encoding/base64"
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

// TestWebSocketClient_SendWebSocketData tests sending WebSocket frame data to the control plane
func TestWebSocketClient_SendWebSocketData(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	receivedMessages := make(chan *WebSocketMessage, 10)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read messages from client
		for {
			var msg WebSocketMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				break
			}
			receivedMessages <- &msg
		}
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)
	defer client.Close()

	// Test sending WebSocket data
	ctx := context.Background()
	requestID := "test-request-123"
	messageType := websocket.TextMessage
	testData := []byte("Hello WebSocket!")

	err = client.SendWebSocketData(ctx, requestID, messageType, testData)
	assert.NoError(t, err)

	// Verify message was received by mock server
	select {
	case msg := <-receivedMessages:
		assert.Equal(t, "proxy_websocket_data", msg.Type)
		assert.Equal(t, requestID, msg.RequestID)

		// Verify payload contains encoded data
		dataStr, ok := msg.Payload["data"].(string)
		require.True(t, ok, "data field should be a string")

		// Verify stream_id is present for gateway/controller delivery
		streamID, ok := msg.Payload["stream_id"].(string)
		require.True(t, ok, "stream_id should be a string")
		assert.Equal(t, requestID, streamID)

		// Decode and verify
		decoded, err := base64.StdEncoding.DecodeString(dataStr)
		require.NoError(t, err)

		// Verify format: [1 byte message type][data]
		require.True(t, len(decoded) >= 1, "decoded data should have at least 1 byte")
		assert.Equal(t, byte(messageType), decoded[0])
		assert.Equal(t, testData, decoded[1:])

		// Verify message_type field (JSON unmarshaling makes it float64)
		msgTypeFloat, ok := msg.Payload["message_type"].(float64)
		require.True(t, ok, "message_type should be a number")
		assert.Equal(t, messageType, int(msgTypeFloat))

	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message")
	}
}

// TestWebSocketClient_GatewayStyleMessages tests receiving gateway-style ws_data messages
// This verifies the fix for gateway mode where the gateway sends ws_start/ws_data/ws_close
// instead of proxy_websocket_start/proxy_websocket_data/proxy_websocket_close
func TestWebSocketClient_GatewayStyleMessages(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	receivedData := make(chan struct {
		requestID string
		data      []byte
	}, 10)

	// Create mock WebSocket server that sends gateway-style messages
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Wait a bit for client to set up callback
		time.Sleep(100 * time.Millisecond)

		// Send ws_data message (gateway style) instead of proxy_websocket_data
		requestID := "gateway-test-789"
		testData := []byte{byte(websocket.TextMessage), 'H', 'e', 'l', 'l', 'o'}
		encoded := base64.StdEncoding.EncodeToString(testData)

		msg := WebSocketMessage{
			Type:      "ws_data", // Gateway-style message type
			RequestID: requestID,
			Payload: map[string]interface{}{
				"request_id":   requestID,
				"stream_id":    requestID,
				"data":         encoded,
				"message_type": websocket.TextMessage,
			},
			Timestamp: time.Now(),
		}

		err = conn.WriteJSON(msg)
		if err != nil {
			t.Logf("Failed to send message: %v", err)
			return
		}

		// Keep connection open
		time.Sleep(1 * time.Second)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	// Set up callback to receive WebSocket data
	client.SetOnWebSocketData(func(requestID string, data []byte) {
		receivedData <- struct {
			requestID string
			data      []byte
		}{requestID: requestID, data: data}
	})

	err = client.Connect()
	require.NoError(t, err)
	defer client.Close()

	// Wait for data to be received
	select {
	case result := <-receivedData:
		assert.Equal(t, "gateway-test-789", result.requestID)
		// Verify data format
		require.True(t, len(result.data) >= 1)
		assert.Equal(t, byte(websocket.TextMessage), result.data[0])
		assert.Equal(t, []byte("Hello"), result.data[1:])
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for gateway-style ws_data callback")
	}
}

// TestWebSocketClient_OnWebSocketData tests receiving WebSocket data from the control plane
func TestWebSocketClient_OnWebSocketData(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	receivedData := make(chan struct {
		requestID string
		data      []byte
	}, 10)

	// Create mock WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Wait a bit for client to set up callback
		time.Sleep(100 * time.Millisecond)

		// Send proxy_websocket_data message to client
		requestID := "test-request-456"
		testData := []byte{byte(websocket.BinaryMessage), 0x01, 0x02, 0x03, 0x04}
		encoded := base64.StdEncoding.EncodeToString(testData)

		msg := WebSocketMessage{
			Type:      "proxy_websocket_data",
			RequestID: requestID,
			Payload: map[string]interface{}{
				"request_id":   requestID,
				"data":         encoded,
				"message_type": websocket.BinaryMessage,
			},
			Timestamp: time.Now(),
		}

		err = conn.WriteJSON(msg)
		if err != nil {
			t.Logf("Failed to send message: %v", err)
			return
		}

		// Keep connection open
		time.Sleep(1 * time.Second)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	// Set up callback to receive WebSocket data
	client.SetOnWebSocketData(func(requestID string, data []byte) {
		receivedData <- struct {
			requestID string
			data      []byte
		}{requestID: requestID, data: data}
	})

	err = client.Connect()
	require.NoError(t, err)
	defer client.Close()

	// Wait for data to be received
	select {
	case result := <-receivedData:
		assert.Equal(t, "test-request-456", result.requestID)
		// Verify data format
		require.True(t, len(result.data) >= 1)
		assert.Equal(t, byte(websocket.BinaryMessage), result.data[0])
		assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, result.data[1:])
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout waiting for WebSocket data callback")
	}
}

// TestWebSocketClient_BidirectionalForwarding tests bidirectional WebSocket data flow
func TestWebSocketClient_BidirectionalForwarding(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	serverReceived := make(chan []byte, 10)
	clientReceived := make(chan []byte, 10)

	// Create mock WebSocket server that echoes data back
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read messages and echo back
		for {
			var msg WebSocketMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				break
			}

			if msg.Type == "proxy_websocket_data" {
				// Decode received data
				if dataStr, ok := msg.Payload["data"].(string); ok {
					decoded, err := base64.StdEncoding.DecodeString(dataStr)
					if err == nil {
						serverReceived <- decoded

						// Echo back to client
						echoMsg := WebSocketMessage{
							Type:      "proxy_websocket_data",
							RequestID: msg.RequestID,
							Payload: map[string]interface{}{
								"request_id":   msg.RequestID,
								"data":         dataStr,
								"message_type": websocket.TextMessage,
							},
							Timestamp: time.Now(),
						}
						conn.WriteJSON(echoMsg)
					}
				}
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	// Set up callback
	client.SetOnWebSocketData(func(requestID string, data []byte) {
		clientReceived <- data
	})

	err = client.Connect()
	require.NoError(t, err)
	defer client.Close()

	// Send data to server
	ctx := context.Background()
	testData := []byte("Bidirectional test")
	err = client.SendWebSocketData(ctx, "test-123", websocket.TextMessage, testData)
	require.NoError(t, err)

	// Verify server received the data
	select {
	case data := <-serverReceived:
		assert.Equal(t, byte(websocket.TextMessage), data[0])
		assert.Equal(t, testData, data[1:])
	case <-time.After(2 * time.Second):
		t.Fatal("Server did not receive data")
	}

	// Verify client received echo back
	select {
	case data := <-clientReceived:
		assert.Equal(t, byte(websocket.TextMessage), data[0])
		assert.Equal(t, testData, data[1:])
	case <-time.After(2 * time.Second):
		t.Fatal("Client did not receive echo")
	}
}

// TestWebSocketClient_HandleMultipleFrameTypes tests handling different WebSocket frame types
func TestWebSocketClient_HandleMultipleFrameTypes(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	receivedMessages := make(chan *WebSocketMessage, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var msg WebSocketMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				break
			}
			receivedMessages <- &msg
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, err := NewWebSocketClient(wsURL, "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	err = client.Connect()
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()

	// Test different frame types
	frameTypes := []struct {
		name        string
		messageType int
		data        []byte
	}{
		{"text", websocket.TextMessage, []byte("text message")},
		{"binary", websocket.BinaryMessage, []byte{0x01, 0x02, 0x03}},
		{"empty", websocket.TextMessage, []byte{}},
	}

	for _, ft := range frameTypes {
		t.Run(ft.name, func(t *testing.T) {
			err := client.SendWebSocketData(ctx, "test-"+ft.name, ft.messageType, ft.data)
			assert.NoError(t, err)

			select {
			case msg := <-receivedMessages:
				assert.Equal(t, "proxy_websocket_data", msg.Type)
				dataStr := msg.Payload["data"].(string)
				decoded, err := base64.StdEncoding.DecodeString(dataStr)
				require.NoError(t, err)

				if len(decoded) > 0 {
					assert.Equal(t, byte(ft.messageType), decoded[0])
					assert.Equal(t, ft.data, decoded[1:])
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Timeout waiting for message")
			}
		})
	}
}
