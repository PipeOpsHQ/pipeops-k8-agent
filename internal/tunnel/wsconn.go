package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSConn wraps a WebSocket connection to implement net.Conn for yamux.
//
// Single-WS architecture: The main read loop owns ws.ReadMessage() and demuxes
// text (JSON control) vs binary (yamux) frames. Binary frame data is fed into
// this adapter via FeedBinaryData(). Read() pulls from an internal pipe.
// Write() sends binary frames directly to the WebSocket (mutex-protected).
//
// This design allows yamux and the JSON control protocol to share a single
// WebSocket connection without fighting over NextReader().
//
// IMPORTANT: The writeMu must be the same mutex used by the caller for text
// writes (e.g., WebSocketClient.writeMutex) to prevent concurrent writes to
// the underlying WebSocket connection, which gorilla/websocket does not support.
type WSConn struct {
	ws *websocket.Conn

	// Pipe for binary data: the main read loop writes binary frame data into
	// pipeWriter, and Read() consumes it from pipeReader.
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter

	// Write mutex — shared with the caller so text (JSON) and binary (yamux)
	// writes to the same WebSocket are properly serialized.
	writeMu *sync.Mutex

	closed  bool
	closeMu sync.RWMutex
}

// NewWSConn creates a new WSConn wrapping a WebSocket connection.
// Binary data must be fed via FeedBinaryData() from the main read loop.
// writeMu MUST be the same mutex the caller uses for text/JSON writes to ws,
// since gorilla/websocket does not support concurrent writers.
func NewWSConn(ws *websocket.Conn, writeMu *sync.Mutex) *WSConn {
	pr, pw := io.Pipe()
	return &WSConn{
		ws:         ws,
		pipeReader: pr,
		pipeWriter: pw,
		writeMu:    writeMu,
	}
}

// FeedBinaryData pushes binary WebSocket frame data into the pipe for yamux to consume.
// Called by the main read loop when it receives a BinaryMessage.
// Returns an error if the pipe is closed (WSConn was closed).
func (c *WSConn) FeedBinaryData(data []byte) error {
	_, err := c.pipeWriter.Write(data)
	return err
}

// Read implements io.Reader / net.Conn.Read.
// It reads from the internal pipe that is fed by the main read loop's demuxer.
func (c *WSConn) Read(b []byte) (int, error) {
	return c.pipeReader.Read(b)
}

// Write implements io.Writer / net.Conn.Write.
// It sends data as a binary WebSocket frame directly.
func (c *WSConn) Write(b []byte) (int, error) {
	c.closeMu.RLock()
	if c.closed {
		c.closeMu.RUnlock()
		return 0, io.ErrClosedPipe
	}
	c.closeMu.RUnlock()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	err := c.ws.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close closes the pipe and marks the connection as closed.
// It does NOT close the underlying WebSocket — that is owned by the main
// connection lifecycle (handleConnection / readMessages).
func (c *WSConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()

	// Close the pipe — unblocks any pending Read() and FeedBinaryData()
	c.pipeWriter.Close()
	c.pipeReader.Close()

	return nil
}

// LocalAddr returns the local network address
func (c *WSConn) LocalAddr() net.Addr {
	return c.ws.LocalAddr()
}

// RemoteAddr returns the remote network address
func (c *WSConn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

// SetDeadline sets both read and write deadlines
func (c *WSConn) SetDeadline(t time.Time) error {
	// Deadlines on the pipe are not directly supported.
	// yamux handles its own timeouts internally.
	return nil
}

// SetReadDeadline sets the read deadline
func (c *WSConn) SetReadDeadline(t time.Time) error {
	// Not applicable for pipe-based reads — yamux manages timeouts.
	return nil
}

// SetWriteDeadline is a no-op. yamux calls this to manage its own write
// timeouts, but on a shared WebSocket connection these deadlines persist and
// conflict with WS-level pings and JSON control writes. Yamux's internal
// ConnectionWriteTimeout is sufficient.
func (c *WSConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// IsClosed returns whether the connection is closed
func (c *WSConn) IsClosed() bool {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()
	return c.closed
}

// Ensure WSConn implements net.Conn at compile time
var _ net.Conn = (*WSConn)(nil)
