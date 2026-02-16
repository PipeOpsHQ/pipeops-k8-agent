package tunnel

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upgrader for tests
var testUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func TestWSConn_ReadWrite(t *testing.T) {
	// Create a WebSocket test server
	var serverConn *websocket.Conn
	var serverWG sync.WaitGroup
	serverWG.Add(1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		serverConn, err = testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		serverWG.Done()
	}))
	defer server.Close()

	// Connect client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	serverWG.Wait()
	defer serverConn.Close()

	// Wrap both in WSConn with their own write mutexes
	var clientMu, serverMu sync.Mutex
	clientWSConn := NewWSConn(clientConn, &clientMu)
	serverWSConn := NewWSConn(serverConn, &serverMu)

	// Test write from client → binary frame arrives at server WS → feed to serverWSConn → Read()
	testData := []byte("hello yamux!")

	// Write in goroutine (sends binary frame via client WS)
	go func() {
		n, err := clientWSConn.Write(testData)
		assert.NoError(t, err)
		assert.Equal(t, len(testData), n)
	}()

	// Simulate the main read loop: read from server WS and feed to serverWSConn
	go func() {
		msgType, data, err := serverConn.ReadMessage()
		if err != nil {
			return
		}
		assert.Equal(t, websocket.BinaryMessage, msgType)
		err = serverWSConn.FeedBinaryData(data)
		assert.NoError(t, err)
	}()

	// Read on server via pipe
	buf := make([]byte, 1024)
	n, err := serverWSConn.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf[:n])
}

func TestWSConn_Close(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		// Just accept and close
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	var mu sync.Mutex
	wsConn := NewWSConn(clientConn, &mu)
	assert.False(t, wsConn.IsClosed())

	err = wsConn.Close()
	assert.NoError(t, err)
	assert.True(t, wsConn.IsClosed())

	// Double close should be safe
	err = wsConn.Close()
	assert.NoError(t, err)
}

func TestWSConn_ReadAfterClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	var mu sync.Mutex
	wsConn := NewWSConn(clientConn, &mu)
	wsConn.Close()

	buf := make([]byte, 1024)
	_, err = wsConn.Read(buf)
	// After Close(), the pipe reader is closed — Read returns io.ErrClosedPipe
	assert.Error(t, err)
}

func TestWSConn_WriteAfterClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	var mu sync.Mutex
	wsConn := NewWSConn(clientConn, &mu)
	wsConn.Close()

	_, err = wsConn.Write([]byte("test"))
	assert.Equal(t, io.ErrClosedPipe, err)
}

func TestWSConn_NetConnInterface(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	var mu sync.Mutex
	wsConn := NewWSConn(clientConn, &mu)

	// Verify it implements net.Conn
	var _ net.Conn = wsConn

	// Test address methods
	assert.NotNil(t, wsConn.LocalAddr())
	assert.NotNil(t, wsConn.RemoteAddr())

	// Test deadline methods (should not error)
	err = wsConn.SetDeadline(time.Now().Add(time.Second))
	assert.NoError(t, err)

	err = wsConn.SetReadDeadline(time.Now().Add(time.Second))
	assert.NoError(t, err)

	err = wsConn.SetWriteDeadline(time.Now().Add(time.Second))
	assert.NoError(t, err)
}

func TestWSConn_LargeMessage(t *testing.T) {
	var serverConn *websocket.Conn
	var serverWG sync.WaitGroup
	serverWG.Add(1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		serverConn, err = testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		serverWG.Done()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	serverWG.Wait()
	defer serverConn.Close()

	var clientMu, serverMu sync.Mutex
	clientWSConn := NewWSConn(clientConn, &clientMu)
	serverWSConn := NewWSConn(serverConn, &serverMu)

	// Send 64KB of data
	largeData := bytes.Repeat([]byte("X"), 64*1024)

	// Write in goroutine (sends binary frame via client WS)
	go func() {
		n, err := clientWSConn.Write(largeData)
		assert.NoError(t, err)
		assert.Equal(t, len(largeData), n)
	}()

	// Simulate the main read loop: read from server WS and feed to serverWSConn
	go func() {
		msgType, data, err := serverConn.ReadMessage()
		if err != nil {
			return
		}
		assert.Equal(t, websocket.BinaryMessage, msgType)
		err = serverWSConn.FeedBinaryData(data)
		assert.NoError(t, err)
	}()

	received := make([]byte, 0, len(largeData))
	buf := make([]byte, 4096)
	for len(received) < len(largeData) {
		n, err := serverWSConn.Read(buf)
		if err != nil {
			break
		}
		received = append(received, buf[:n]...)
	}

	assert.Equal(t, largeData, received)
}

func TestWSConn_FeedBinaryData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	var mu sync.Mutex
	wsConn := NewWSConn(clientConn, &mu)

	// Feed data and read it back through the pipe
	testData := []byte("test binary data from demuxer")
	go func() {
		err := wsConn.FeedBinaryData(testData)
		assert.NoError(t, err)
	}()

	buf := make([]byte, 1024)
	n, err := wsConn.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, testData, buf[:n])

	// After close, FeedBinaryData should fail
	wsConn.Close()
	err = wsConn.FeedBinaryData([]byte("after close"))
	assert.Error(t, err)
}
