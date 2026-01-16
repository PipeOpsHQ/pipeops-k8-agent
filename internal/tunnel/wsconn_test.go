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

	// Wrap both in WSConn
	clientWSConn := NewWSConn(clientConn)
	serverWSConn := NewWSConn(serverConn)

	// Test write from client, read from server
	testData := []byte("hello yamux!")

	// Write in goroutine
	go func() {
		n, err := clientWSConn.Write(testData)
		assert.NoError(t, err)
		assert.Equal(t, len(testData), n)
	}()

	// Read on server
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

	wsConn := NewWSConn(clientConn)
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

	wsConn := NewWSConn(clientConn)
	wsConn.Close()

	buf := make([]byte, 1024)
	_, err = wsConn.Read(buf)
	assert.Equal(t, io.EOF, err)
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

	wsConn := NewWSConn(clientConn)
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

	wsConn := NewWSConn(clientConn)

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

	clientWSConn := NewWSConn(clientConn)
	serverWSConn := NewWSConn(serverConn)

	// Send 64KB of data
	largeData := bytes.Repeat([]byte("X"), 64*1024)

	go func() {
		n, err := clientWSConn.Write(largeData)
		assert.NoError(t, err)
		assert.Equal(t, len(largeData), n)
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
