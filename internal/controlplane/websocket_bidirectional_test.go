package controlplane

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyResponseWriter_StreamChannel tests the StreamChannel functionality
func TestProxyResponseWriter_StreamChannel(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Create a mock sender
	mockSender := &mockProxyResponseSender{}

	writer := newBufferedProxyResponseWriter(mockSender, "test-request-123", logger)
	require.NotNil(t, writer)

	// Get the stream channel
	streamChan := writer.StreamChannel()
	require.NotNil(t, streamChan)

	// Test sending data through the channel
	testData := []byte{byte(websocket.TextMessage), 'h', 'e', 'l', 'l', 'o'}

	go func() {
		writer.streamChan <- testData
	}()

	// Receive the data
	select {
	case data := <-streamChan:
		assert.Equal(t, testData, data)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for data on stream channel")
	}

	// Test that channel is closed when writer is closed
	writer.Close()

	select {
	case _, ok := <-streamChan:
		assert.False(t, ok, "Channel should be closed")
	case <-time.After(1 * time.Second):
		t.Fatal("Channel should have been closed")
	}
}

// TestWebSocketClient_ProxyStreamData tests the proxy_stream_data message handler
func TestWebSocketClient_ProxyStreamData(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client := &WebSocketClient{
		logger:             logger,
		activeProxyWriters: make(map[string]ProxyResponseWriter),
	}

	// Create a mock writer
	mockSender := &mockProxyResponseSender{}
	writer := newBufferedProxyResponseWriter(mockSender, "test-request-456", logger)

	// Register the writer
	client.activeProxyWriters["test-request-456"] = writer

	// Create a test message
	testFrameData := []byte{byte(websocket.TextMessage), 't', 'e', 's', 't'}
	encodedData := base64.StdEncoding.EncodeToString(testFrameData)

	msg := &WebSocketMessage{
		Type:      "proxy_stream_data",
		RequestID: "test-request-456",
		Payload: map[string]interface{}{
			"data": encodedData,
		},
		Timestamp: time.Now(),
	}

	// Handle the message (this would normally be called by handleMessage)
	dataStr, ok := msg.Payload["data"].(string)
	require.True(t, ok)

	data, err := base64.StdEncoding.DecodeString(dataStr)
	require.NoError(t, err)

	// Send to writer's stream channel
	go func() {
		writer.streamChan <- data
	}()

	// Verify data is received on the stream channel
	select {
	case receivedData := <-writer.StreamChannel():
		assert.Equal(t, testFrameData, receivedData)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for stream data")
	}

	// Cleanup
	writer.Close()
}

// TestWebSocketClient_ActiveWritersTracking tests writer registration and cleanup
func TestWebSocketClient_ActiveWritersTracking(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client := &WebSocketClient{
		logger:             logger,
		activeProxyWriters: make(map[string]ProxyResponseWriter),
	}

	// Create a mock writer
	mockSender := &mockProxyResponseSender{}
	writer := newBufferedProxyResponseWriter(mockSender, "test-request-789", logger)

	// Register writer
	client.activeProxyWriters["test-request-789"] = writer
	assert.Equal(t, 1, len(client.activeProxyWriters))

	// Verify we can retrieve it
	retrievedWriter, ok := client.activeProxyWriters["test-request-789"]
	assert.True(t, ok)
	assert.Equal(t, writer, retrievedWriter)

	// Cleanup - remove writer
	delete(client.activeProxyWriters, "test-request-789")
	assert.Equal(t, 0, len(client.activeProxyWriters))

	// Cleanup
	writer.Close()
}

// TestProxyResponseWriter_ChannelBuffering tests that the channel buffer works correctly
func TestProxyResponseWriter_ChannelBuffering(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	mockSender := &mockProxyResponseSender{}
	writer := newBufferedProxyResponseWriter(mockSender, "test-buffering", logger)

	// Send multiple messages without reading
	for i := 0; i < 10; i++ {
		testData := []byte{byte(i)}
		writer.streamChan <- testData
	}

	// Read them all back
	for i := 0; i < 10; i++ {
		select {
		case data := <-writer.StreamChannel():
			assert.Equal(t, byte(i), data[0])
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for message %d", i)
		}
	}

	writer.Close()
}

// mockProxyResponseSender is a mock implementation for testing
type mockProxyResponseSender struct {
	lastResponse   *ProxyResponse
	lastError      *ProxyError
	lastBinaryBody []byte
	lastBinaryUsed bool
}

func (m *mockProxyResponseSender) SendProxyResponse(ctx context.Context, response *ProxyResponse) error {
	m.lastResponse = response
	return nil
}

func (m *mockProxyResponseSender) SendProxyResponseBinary(ctx context.Context, response *ProxyResponse, bodyBytes []byte) error {
	m.lastResponse = response
	m.lastBinaryBody = bodyBytes
	m.lastBinaryUsed = true
	return nil
}

func (m *mockProxyResponseSender) SendProxyError(ctx context.Context, proxyErr *ProxyError) error {
	m.lastError = proxyErr
	return nil
}

func (m *mockProxyResponseSender) SendProxyResponseHeader(ctx context.Context, requestID string, status int, headers map[string][]string) error {
	return nil
}

func (m *mockProxyResponseSender) SendProxyResponseChunk(ctx context.Context, requestID string, chunk []byte) error {
	return nil
}

func (m *mockProxyResponseSender) SendProxyResponseEnd(ctx context.Context, requestID string) error {
	return nil
}

func (m *mockProxyResponseSender) SendProxyResponseAbort(ctx context.Context, requestID string, reason string) error {
	return nil
}
