package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSConn wraps a WebSocket connection to implement net.Conn
// This adapter is required for yamux which expects a net.Conn interface
type WSConn struct {
	ws       *websocket.Conn
	reader   io.Reader
	readerMu sync.Mutex
	writeMu  sync.Mutex
	closed   bool
	closedMu sync.RWMutex
}

// NewWSConn creates a new WSConn wrapping a WebSocket connection
func NewWSConn(ws *websocket.Conn) *WSConn {
	return &WSConn{ws: ws}
}

// Read implements io.Reader for the WebSocket connection
// It reads binary messages from the WebSocket and returns them as a byte stream
func (c *WSConn) Read(b []byte) (int, error) {
	c.closedMu.RLock()
	if c.closed {
		c.closedMu.RUnlock()
		return 0, io.EOF
	}
	c.closedMu.RUnlock()

	c.readerMu.Lock()
	defer c.readerMu.Unlock()

	for {
		if c.reader != nil {
			n, err := c.reader.Read(b)
			if err == io.EOF {
				c.reader = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}

		messageType, reader, err := c.ws.NextReader()
		if err != nil {
			return 0, err
		}

		// Only handle binary messages for yamux
		if messageType != websocket.BinaryMessage {
			io.Copy(io.Discard, reader)
			continue
		}

		c.reader = reader
	}
}

// Write implements io.Writer for the WebSocket connection
// It sends data as binary WebSocket messages
func (c *WSConn) Write(b []byte) (int, error) {
	c.closedMu.RLock()
	if c.closed {
		c.closedMu.RUnlock()
		return 0, io.ErrClosedPipe
	}
	c.closedMu.RUnlock()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	err := c.ws.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close closes the WebSocket connection
func (c *WSConn) Close() error {
	c.closedMu.Lock()
	if c.closed {
		c.closedMu.Unlock()
		return nil
	}
	c.closed = true
	c.closedMu.Unlock()

	c.writeMu.Lock()
	c.ws.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.writeMu.Unlock()

	return c.ws.Close()
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
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

// SetReadDeadline sets the read deadline
func (c *WSConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline
func (c *WSConn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}

// IsClosed returns whether the connection is closed
func (c *WSConn) IsClosed() bool {
	c.closedMu.RLock()
	defer c.closedMu.RUnlock()
	return c.closed
}
