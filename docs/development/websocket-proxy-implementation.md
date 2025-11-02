# WebSocket Proxy Implementation Guide

## Overview

This document describes the implementation of WebSocket proxy support in the PipeOps agent to enable `kubectl exec`, `attach`, and `port-forward` through the tunnel.

## Status

**NOT YET IMPLEMENTED** - This document serves as an implementation guide.

## Background

The controller has been updated with WebSocket proxy support. The agent now needs to handle bidirectional WebSocket connections to the Kubernetes API for interactive commands.

## Architecture

```
User (kubectl) → Control Plane → WebSocket Tunnel → Agent → K8s API
                                                              ↓
                                                          Pod/Container
```

## Message Protocol

### Message Types

The agent must handle three new message types:

1. **`proxy_websocket_start`** - Initiate WebSocket connection to K8s API
2. **`proxy_websocket_data`** - Relay data from controller to K8s
3. **`proxy_websocket_close`** - Close WebSocket connection

### Message Format

#### Incoming: Start WebSocket Session

```json
{
  "type": "proxy_websocket_start",
  "request_id": "<stream_id>",
  "timestamp": "2025-11-02T22:00:00Z",
  "payload": {
    "stream_id": "uuid",
    "cluster_uuid": "cluster-uuid",
    "method": "GET",
    "path": "/api/v1/namespaces/default/pods/my-pod/exec",
    "query": "command=/bin/bash&stdin=true&stdout=true&tty=true",
    "headers": {
      "Authorization": ["Bearer ..."],
      "Upgrade": ["SPDY/3.1"],
      "Connection": ["Upgrade"]
    },
    "protocol": "v4.channel.k8s.io"
  }
}
```

#### Incoming: Data from Client

```json
{
  "type": "proxy_websocket_data",
  "request_id": "<stream_id>",
  "timestamp": "2025-11-02T22:00:01Z",
  "payload": {
    "stream_id": "uuid",
    "data": "<base64 encoded: [messageType byte][data...]>"
  }
}
```

#### Outgoing: Data to Client

```json
{
  "type": "proxy_websocket_data",
  "request_id": "<stream_id>",
  "timestamp": "2025-11-02T22:00:02Z",
  "payload": {
    "stream_id": "uuid",
    "data": "<base64 encoded: [messageType byte][data...]>"
  }
}
```

#### Close Stream

```json
{
  "type": "proxy_websocket_close",
  "request_id": "<stream_id>",
  "timestamp": "2025-11-02T22:00:05Z",
  "payload": {
    "stream_id": "uuid"
  }
}
```

## Implementation Steps

### 1. Add Stream Management

Add to `internal/agent/agent.go`:

```go
type websocketStream struct {
    conn     *websocket.Conn  // Connection to K8s
    dataCh   chan []byte      // Data from controller
    closeCh  chan struct{}
    mu       sync.Mutex
}

// Add to Agent struct
type Agent struct {
    // ... existing fields ...
    wsStreams   map[string]*websocketStream
    wsStreamsMu sync.RWMutex
}

// Initialize in NewAgent
func NewAgent(...) *Agent {
    return &Agent{
        // ... existing fields ...
        wsStreams: make(map[string]*websocketStream),
    }
}
```

### 2. Add Message Handlers

Create `internal/agent/websocket_proxy.go`:

```go
package agent

import (
    "context"
    "encoding/base64"
    "fmt"
    "time"
    
    "github.com/gorilla/websocket"
)

func (a *Agent) handleWebSocketProxyStart(msg Message) {
    payload := msg.Payload
    streamID := payload["stream_id"].(string)
    path := payload["path"].(string)
    query := payload["query"].(string)
    headers := parseHeaders(payload["headers"])
    
    a.logger.Infof("Starting WebSocket proxy: stream_id=%s path=%s", streamID, path)
    
    // Build K8s WebSocket URL
    k8sURL := fmt.Sprintf("wss://%s%s", a.k8sClient.GetAPIHost(), path)
    if query != "" {
        k8sURL += "?" + query
    }
    
    // Prepare headers with K8s auth
    reqHeaders := http.Header{}
    for k, v := range headers {
        reqHeaders[k] = v
    }
    reqHeaders.Set("Authorization", "Bearer "+a.k8sClient.GetToken())
    
    // Connect to K8s WebSocket
    dialer := websocket.Dialer{
        TLSClientConfig: a.k8sClient.GetTLSConfig(),
        HandshakeTimeout: 10 * time.Second,
    }
    
    k8sConn, _, err := dialer.Dial(k8sURL, reqHeaders)
    if err != nil {
        a.logger.WithError(err).Error("Failed to connect to K8s WebSocket")
        a.sendWebSocketError(streamID, err)
        return
    }
    
    a.logger.Infof("Connected to K8s WebSocket: stream_id=%s", streamID)
    
    // Register stream
    stream := &websocketStream{
        conn:    k8sConn,
        dataCh:  make(chan []byte, 100),
        closeCh: make(chan struct{}),
    }
    
    a.wsStreamsMu.Lock()
    a.wsStreams[streamID] = stream
    a.wsStreamsMu.Unlock()
    
    // Start bidirectional relay
    go a.relayControllerToK8s(streamID, stream)
    go a.relayK8sToController(streamID, stream)
}

func (a *Agent) handleWebSocketProxyData(msg Message) {
    payload := msg.Payload
    streamID := payload["stream_id"].(string)
    dataStr := payload["data"].(string)
    
    // Decode base64 data
    data, err := base64.StdEncoding.DecodeString(dataStr)
    if err != nil {
        a.logger.WithError(err).Error("Failed to decode WebSocket data")
        return
    }
    
    // Get stream
    a.wsStreamsMu.RLock()
    stream, ok := a.wsStreams[streamID]
    a.wsStreamsMu.RUnlock()
    
    if !ok {
        a.logger.Warnf("WebSocket stream not found: %s", streamID)
        return
    }
    
    // Send to stream channel (non-blocking)
    select {
    case stream.dataCh <- data:
    default:
        a.logger.Warnf("WebSocket stream channel full: %s", streamID)
    }
}

func (a *Agent) handleWebSocketProxyClose(msg Message) {
    payload := msg.Payload
    streamID := payload["stream_id"].(string)
    
    a.logger.Infof("Closing WebSocket stream: %s", streamID)
    a.unregisterWebSocketStream(streamID)
}

func (a *Agent) relayControllerToK8s(streamID string, stream *websocketStream) {
    defer a.unregisterWebSocketStream(streamID)
    
    for {
        select {
        case <-stream.closeCh:
            return
            
        case data, ok := <-stream.dataCh:
            if !ok {
                return
            }
            
            // Data format: [messageType byte][actual data]
            if len(data) < 1 {
                continue
            }
            
            messageType := int(data[0])
            messageData := data[1:]
            
            // Forward to K8s
            stream.mu.Lock()
            err := stream.conn.WriteMessage(messageType, messageData)
            stream.mu.Unlock()
            
            if err != nil {
                a.logger.WithError(err).Error("Failed to write to K8s WebSocket")
                return
            }
        }
    }
}

func (a *Agent) relayK8sToController(streamID string, stream *websocketStream) {
    defer stream.conn.Close()
    
    for {
        select {
        case <-stream.closeCh:
            return
            
        default:
            messageType, data, err := stream.conn.ReadMessage()
            if err != nil {
                if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
                    a.logger.Warnf("K8s WebSocket closed unexpectedly: %v", err)
                }
                a.sendWebSocketClose(streamID)
                return
            }
            
            // Encode: [messageType byte][data]
            encoded := append([]byte{byte(messageType)}, data...)
            
            // Send to controller
            if err := a.sendWebSocketDataToController(streamID, encoded); err != nil {
                a.logger.WithError(err).Error("Failed to send data to controller")
                return
            }
        }
    }
}

func (a *Agent) unregisterWebSocketStream(streamID string) {
    a.wsStreamsMu.Lock()
    defer a.wsStreamsMu.Unlock()
    
    stream, ok := a.wsStreams[streamID]
    if !ok {
        return
    }
    
    // Close channels
    close(stream.closeCh)
    close(stream.dataCh)
    
    // Close K8s connection
    stream.conn.Close()
    
    // Remove from map
    delete(a.wsStreams, streamID)
    
    a.logger.Infof("WebSocket stream unregistered: %s", streamID)
}

func (a *Agent) sendWebSocketDataToController(streamID string, data []byte) error {
    msg := map[string]interface{}{
        "type": "proxy_websocket_data",
        "request_id": streamID,
        "timestamp": time.Now(),
        "payload": map[string]interface{}{
            "stream_id": streamID,
            "data": base64.StdEncoding.EncodeToString(data),
        },
    }
    
    return a.controlPlane.SendMessage(msg)
}

func (a *Agent) sendWebSocketClose(streamID string) error {
    msg := map[string]interface{}{
        "type": "proxy_websocket_close",
        "request_id": streamID,
        "timestamp": time.Now(),
        "payload": map[string]interface{}{
            "stream_id": streamID,
        },
    }
    
    return a.controlPlane.SendMessage(msg)
}

func (a *Agent) sendWebSocketError(streamID string, err error) error {
    msg := map[string]interface{}{
        "type": "proxy_error",
        "request_id": streamID,
        "timestamp": time.Now(),
        "payload": map[string]interface{}{
            "request_id": streamID,
            "error": err.Error(),
        },
    }
    
    return a.controlPlane.SendMessage(msg)
}
```

### 3. Update Message Router

In `internal/agent/agent.go`, update the message handler:

```go
func (a *Agent) handleMessage(messageType int, data []byte) {
    var msg Message
    if err := json.Unmarshal(data, &msg); err != nil {
        a.logger.WithError(err).Error("Failed to unmarshal message")
        return
    }
    
    switch msg.Type {
    case "proxy_request":
        go a.handleProxyRequest(msg)
        
    case "proxy_websocket_start":
        go a.handleWebSocketProxyStart(msg)
        
    case "proxy_websocket_data":
        a.handleWebSocketProxyData(msg)
        
    case "proxy_websocket_close":
        a.handleWebSocketProxyClose(msg)
        
    // ... other message types
    
    default:
        a.logger.Warnf("Unknown message type: %s", msg.Type)
    }
}
```

## Testing

### Test kubectl exec

```bash
kubectl exec -it pod-name -n namespace -- /bin/bash
```

Expected agent logs:
```
Starting WebSocket proxy: stream_id=... path=/api/v1/.../exec
Connected to K8s WebSocket: stream_id=...
WebSocket stream closed: stream_id=...
```

### Test kubectl attach

```bash
kubectl attach pod-name -n namespace -it
```

### Test kubectl port-forward

```bash
kubectl port-forward pod-name 8080:80 -n namespace
curl http://localhost:8080
```

### Test Concurrent Sessions

```bash
for i in {1..5}; do
  kubectl exec pod-name -- echo "Session $i" &
done
```

## Success Criteria

- [ ] kubectl exec opens shell successfully
- [ ] kubectl attach shows container output
- [ ] kubectl port-forward forwards ports
- [ ] Multiple concurrent WebSocket sessions work
- [ ] Proper cleanup on connection close
- [ ] No goroutine leaks
- [ ] Error handling for network issues
- [ ] Graceful degradation on failures

## Performance Considerations

- Use buffered channels (100 items) to prevent blocking
- Implement proper connection pooling
- Add timeouts for WebSocket operations
- Monitor goroutine count
- Add metrics for stream lifecycle

## Security Considerations

- Validate all stream IDs
- Enforce authentication on K8s connections
- Implement rate limiting per stream
- Add connection limits per agent
- Log all WebSocket proxy operations for audit

## Future Enhancements

- [ ] Add compression for WebSocket frames
- [ ] Implement binary protocol (no base64 overhead)
- [ ] Add stream multiplexing
- [ ] Implement backpressure handling
- [ ] Add stream health checks

## References

- Controller WebSocket Proxy: PR #5040
- Kubernetes Exec API: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#pod-v1-core
- WebSocket Protocol: RFC 6455
