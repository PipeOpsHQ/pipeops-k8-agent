# Controller/Control Plane WebSocket Implementation Requirements

**Date:** 2025-11-10  
**Status:** Required for WebSocket functionality  
**Priority:** HIGH - Blocking production WebSocket support

---

## Executive Summary

The PipeOps agent now has **production-ready WebSocket support** with KubeSail-inspired optimizations. However, WebSocket applications (like Uptime Kuma) will not work until the **controller/control plane** implements bidirectional WebSocket forwarding.

**Current State:**
- ✅ Agent can connect to in-cluster WebSocket services
- ✅ Agent forwards service messages to controller
- ❌ Controller cannot send client messages back to agent (missing reverse direction)

**Required:** Implement bidirectional WebSocket tunnel protocol in the controller.

---

## Architecture Overview

### Current Flow (HTTP - Working)
```
Client → Controller → Agent → Service
                              ↓
Client ← Controller ← Agent ← Service
```

### Required Flow (WebSocket - Needs Implementation)
```
Client (WebSocket) ↔ Controller ↔ Agent ↔ Service (WebSocket)
                     ↕           ↕
               Bidirectional   Bidirectional
               WebSocket       Message
               Connection      Forwarding
```

---

## Part 1: WebSocket Detection and Upgrade

### 1.1 Detect WebSocket Upgrade Requests

**Location:** HTTP request handler (before creating ProxyRequest)

**Implementation:**
```go
// Add helper function
func isWebSocketUpgrade(r *http.Request) bool {
    upgrade := strings.ToLower(r.Header.Get("Upgrade"))
    connection := strings.ToLower(r.Header.Get("Connection"))
    
    return upgrade == "websocket" && 
           strings.Contains(connection, "upgrade")
}

// In request handler
func (g *Gateway) HandleHTTP(w http.ResponseWriter, r *http.Request) {
    // ... existing route lookup ...
    
    // Check if this is a WebSocket upgrade
    if isWebSocketUpgrade(r) {
        g.handleWebSocketProxy(w, r, route)
        return
    }
    
    // ... existing HTTP proxy logic ...
}
```

**Why:** WebSocket connections start as HTTP upgrade requests. Must be detected before normal HTTP processing.

---

### 1.2 Set IsWebSocket Flag in ProxyRequest

**Location:** When creating ProxyRequest to send to agent

**Implementation:**
```go
proxyReq := &ProxyRequest{
    RequestID:   generateRequestID(),
    Method:      r.Method,
    Path:        r.URL.Path,
    Query:       r.URL.RawQuery,
    Headers:     r.Header,
    
    // Service routing (from route lookup)
    ServiceName: route.ServiceName,
    Namespace:   route.Namespace,
    ServicePort: route.ServicePort,
    
    // ← ADD THIS
    IsWebSocket: true,  // Flag for agent to use WebSocket proxy
}
```

**Why:** Tells the agent to use `handleWebSocketProxy()` instead of regular HTTP proxy.

---

## Part 2: Client WebSocket Upgrade

### 2.1 Upgrade Client Connection

**Location:** After detecting WebSocket upgrade

**Implementation:**
```go
func (g *Gateway) handleWebSocketProxy(w http.ResponseWriter, r *http.Request, route *Route) {
    logger := g.logger.WithFields(logrus.Fields{
        "host":      r.Host,
        "path":      r.URL.Path,
        "service":   route.ServiceName,
        "namespace": route.Namespace,
    })
    
    logger.Info("Handling WebSocket proxy request")
    
    // Upgrade client connection to WebSocket
    upgrader := websocket.Upgrader{
        ReadBufferSize:  4096,
        WriteBufferSize: 4096,
        CheckOrigin: func(r *http.Request) bool {
            // Allow all origins for now - tighten in production
            return true
        },
    }
    
    clientWs, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        logger.WithError(err).Error("Failed to upgrade client connection")
        http.Error(w, "WebSocket upgrade failed", http.StatusBadRequest)
        return
    }
    defer clientWs.Close()
    
    logger.Info("Client WebSocket connection upgraded")
    
    // Continue to Part 3...
}
```

**Why:** Upgrades the client's HTTP connection to a WebSocket connection.

---

## Part 3: Bidirectional Tunnel Implementation

### 3.1 Create ProxyRequest and Send to Agent

**Location:** Inside `handleWebSocketProxy()`

**Implementation:**
```go
    // Create ProxyRequest for agent
    proxyReq := &ProxyRequest{
        RequestID:    generateRequestID(),
        ClusterUUID:  route.ClusterUUID,
        Method:       r.Method,
        Path:         r.URL.Path,
        Query:        r.URL.RawQuery,
        Headers:      convertHeaders(r.Header),
        
        // Service routing
        ServiceName:  route.ServiceName,
        Namespace:    route.Namespace,
        ServicePort:  route.ServicePort,
        
        // WebSocket flag
        IsWebSocket:  true,
        
        // Streaming support
        SupportsStreaming: true,
        Timeout:          60 * time.Second,
    }
    
    // Get agent connection for the cluster
    agentConn := g.getAgentConnection(route.ClusterUUID)
    if agentConn == nil {
        logger.Error("No agent connection for cluster")
        clientWs.Close()
        return
    }
    
    // Send ProxyRequest to agent
    if err := agentConn.SendProxyRequest(proxyReq); err != nil {
        logger.WithError(err).Error("Failed to send proxy request to agent")
        clientWs.Close()
        return
    }
    
    logger.Info("Sent WebSocket proxy request to agent")
```

**Why:** Tells the agent to connect to the service and prepare for bidirectional forwarding.

---

### 3.2 Wait for Agent's Upgrade Response

**Location:** After sending ProxyRequest

**Implementation:**
```go
    // Wait for agent's 101 Switching Protocols response
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    agentResp, err := agentConn.WaitForProxyResponse(ctx, proxyReq.RequestID)
    if err != nil {
        logger.WithError(err).Error("Failed to get upgrade response from agent")
        clientWs.WriteControl(websocket.CloseMessage, 
            websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "agent timeout"),
            time.Now().Add(time.Second))
        return
    }
    
    if agentResp.Status != http.StatusSwitchingProtocols {
        logger.WithField("status", agentResp.Status).Error("Agent failed to upgrade connection")
        clientWs.WriteControl(websocket.CloseMessage,
            websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "upgrade failed"),
            time.Now().Add(time.Second))
        return
    }
    
    logger.Info("Agent successfully connected to service WebSocket")
```

**Why:** Ensures the agent successfully connected to the service before starting data forwarding.

---

### 3.3 Implement Bidirectional Forwarding

**THIS IS THE CRITICAL PART - Currently Missing**

**Location:** After successful upgrade

**Implementation:**
```go
    // Set up channels for coordination
    done := make(chan struct{})
    errChan := make(chan error, 2)
    
    // Configure client WebSocket
    clientWs.SetReadDeadline(time.Now().Add(60 * time.Second))
    clientWs.SetPongHandler(func(string) error {
        logger.Debug("Received pong from client")
        return clientWs.SetReadDeadline(time.Now().Add(60 * time.Second))
    })
    
    // Ping client periodically (keep connection alive)
    pingTicker := time.NewTicker(30 * time.Second)
    defer pingTicker.Stop()
    
    go func() {
        for {
            select {
            case <-pingTicker.C:
                if err := clientWs.WriteControl(websocket.PingMessage, []byte{}, 
                    time.Now().Add(5*time.Second)); err != nil {
                    logger.WithError(err).Debug("Failed to ping client")
                    return
                }
            case <-done:
                return
            }
        }
    }()
    
    // Direction 1: Client → Agent → Service
    // Read from client WebSocket, send to agent
    go func() {
        for {
            messageType, data, err := clientWs.ReadMessage()
            if err != nil {
                if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
                    logger.Debug("Client WebSocket closed normally")
                } else {
                    logger.WithError(err).Warn("Error reading from client WebSocket")
                    errChan <- err
                }
                return
            }
            
            // Encode WebSocket message (same format as agent)
            encodedMsg := encodeWebSocketMessage(messageType, data)
            
            // Send to agent via streaming
            if err := agentConn.SendStreamChunk(proxyReq.RequestID, encodedMsg); err != nil {
                logger.WithError(err).Error("Failed to send data to agent")
                errChan <- err
                return
            }
        }
    }()
    
    // Direction 2: Service → Agent → Client
    // Read from agent stream, write to client WebSocket
    go func() {
        defer close(done)
        for {
            // Read chunk from agent's WriteChunk calls
            chunk, err := agentConn.ReadStreamChunk(proxyReq.RequestID)
            if err != nil {
                if err == io.EOF {
                    logger.Debug("Agent stream closed")
                } else {
                    logger.WithError(err).Warn("Error reading from agent stream")
                    errChan <- err
                }
                return
            }
            
            // Decode WebSocket message
            messageType, payload := decodeWebSocketMessage(chunk)
            
            // Write to client
            if err := clientWs.WriteMessage(messageType, payload); err != nil {
                logger.WithError(err).Error("Failed to write to client WebSocket")
                errChan <- err
                return
            }
        }
    }()
    
    // Wait for completion or error
    select {
    case <-done:
        logger.Info("WebSocket proxy completed normally")
    case err := <-errChan:
        logger.WithError(err).Warn("WebSocket proxy error")
    }
    
    // Clean shutdown
    closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
    clientWs.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
    agentConn.CloseProxyStream(proxyReq.RequestID)
    
    logger.Info("WebSocket proxy connection closed")
}
```

**Why:** This implements the bidirectional tunnel that forwards WebSocket messages between client and service.

---

## Part 4: WebSocket Message Encoding/Decoding

### 4.1 Message Format

**Format:** `[1 byte message type][data]`

**Message Types:**
- `1` = Text message
- `2` = Binary message
- `8` = Close message
- `9` = Ping message
- `10` = Pong message

### 4.2 Encoding Function

**Implementation:**
```go
// encodeWebSocketMessage encodes a WebSocket message for transmission
// Must match agent's encoding format
func encodeWebSocketMessage(messageType int, data []byte) []byte {
    result := make([]byte, 1+len(data))
    result[0] = byte(messageType)
    copy(result[1:], data)
    return result
}
```

### 4.3 Decoding Function

**Implementation:**
```go
// decodeWebSocketMessage decodes a WebSocket message from agent
// Must match agent's encoding format
func decodeWebSocketMessage(encoded []byte) (messageType int, payload []byte) {
    if len(encoded) == 0 {
        return 0, nil
    }
    
    messageType = int(encoded[0])
    if len(encoded) > 1 {
        payload = encoded[1:]
    }
    
    return messageType, payload
}
```

**Why:** Agent encodes WebSocket messages before sending. Controller must decode them before writing to client.

---

## Part 5: Agent Connection Streaming Interface

### 5.1 Required Methods on AgentConnection

**These methods must be added to your agent connection type:**

```go
type AgentConnection interface {
    // Existing methods
    SendProxyRequest(req *ProxyRequest) error
    WaitForProxyResponse(ctx context.Context, requestID string) (*ProxyResponse, error)
    
    // NEW: Streaming methods for WebSocket
    SendStreamChunk(requestID string, data []byte) error     // Client → Agent
    ReadStreamChunk(requestID string) ([]byte, error)        // Agent → Client
    CloseProxyStream(requestID string) error                  // Clean up
}
```

### 5.2 Implementation Details

**Storage for active streams:**
```go
type AgentConnection struct {
    // ... existing fields ...
    
    activeStreams   map[string]*ProxyStream
    streamsMutex    sync.RWMutex
}

type ProxyStream struct {
    RequestID       string
    IncomingChunks  chan []byte  // From agent
    OutgoingChunks  chan []byte  // To agent
    Done            chan struct{}
}
```

**SendStreamChunk implementation:**
```go
func (ac *AgentConnection) SendStreamChunk(requestID string, data []byte) error {
    ac.streamsMutex.RLock()
    stream, exists := ac.activeStreams[requestID]
    ac.streamsMutex.RUnlock()
    
    if !exists {
        return fmt.Errorf("stream not found: %s", requestID)
    }
    
    select {
    case stream.OutgoingChunks <- data:
        return nil
    case <-stream.Done:
        return fmt.Errorf("stream closed")
    case <-time.After(5 * time.Second):
        return fmt.Errorf("stream send timeout")
    }
}
```

**ReadStreamChunk implementation:**
```go
func (ac *AgentConnection) ReadStreamChunk(requestID string) ([]byte, error) {
    ac.streamsMutex.RLock()
    stream, exists := ac.activeStreams[requestID]
    ac.streamsMutex.RUnlock()
    
    if !exists {
        return nil, fmt.Errorf("stream not found: %s", requestID)
    }
    
    select {
    case chunk := <-stream.IncomingChunks:
        return chunk, nil
    case <-stream.Done:
        return nil, io.EOF
    }
}
```

**Why:** Provides the streaming interface needed for bidirectional forwarding.

---

## Part 6: WebSocket Tunnel Protocol Extension

### 6.1 New Message Types

**Add to your WebSocket tunnel protocol:**

```go
// Message types for WebSocket streaming
const (
    MsgTypeProxyRequest      = "proxy_request"       // Existing
    MsgTypeProxyResponse     = "proxy_response"      // Existing
    MsgTypeProxyStreamChunk  = "proxy_stream_chunk"  // NEW: Data chunk
    MsgTypeProxyStreamClose  = "proxy_stream_close"  // NEW: Close stream
)

// ProxyStreamChunk - data chunk for streaming
type ProxyStreamChunk struct {
    RequestID string `json:"request_id"`
    Data      []byte `json:"data"`      // Encoded WebSocket message
    Direction string `json:"direction"` // "to_agent" or "from_agent"
}

// ProxyStreamClose - close streaming connection
type ProxyStreamClose struct {
    RequestID string `json:"request_id"`
    Reason    string `json:"reason,omitempty"`
}
```

### 6.2 Message Handlers

**Controller → Agent message handler:**
```go
func (ac *AgentConnection) handleOutgoingMessages() {
    for {
        select {
        case msg := <-ac.outgoingMessages:
            // Send to agent via WebSocket tunnel
            ac.websocket.WriteJSON(msg)
            
        case streamMsg := <-ac.outgoingStreamChunks:
            // Send stream chunk to agent
            ac.websocket.WriteJSON(TunnelMessage{
                Type:      MsgTypeProxyStreamChunk,
                RequestID: streamMsg.RequestID,
                Data:      streamMsg.Data,
                Direction: "to_agent",
            })
        }
    }
}
```

**Agent → Controller message handler:**
```go
func (ac *AgentConnection) handleIncomingMessages() {
    for {
        var msg TunnelMessage
        if err := ac.websocket.ReadJSON(&msg); err != nil {
            return
        }
        
        switch msg.Type {
        case MsgTypeProxyResponse:
            // Handle proxy response (existing)
            
        case MsgTypeProxyStreamChunk:
            // Handle stream chunk from agent
            if msg.Direction == "from_agent" {
                ac.streamsMutex.RLock()
                if stream, exists := ac.activeStreams[msg.RequestID]; exists {
                    select {
                    case stream.IncomingChunks <- msg.Data:
                    case <-stream.Done:
                    }
                }
                ac.streamsMutex.RUnlock()
            }
            
        case MsgTypeProxyStreamClose:
            // Handle stream close
            ac.CloseProxyStream(msg.RequestID)
        }
    }
}
```

**Why:** Extends the tunnel protocol to support bidirectional streaming.

---

## Part 7: Error Handling & Cleanup

### 7.1 Connection Timeouts

**Implementation:**
```go
// Set reasonable timeouts
const (
    WebSocketHandshakeTimeout = 10 * time.Second
    WebSocketReadTimeout      = 60 * time.Second
    WebSocketWriteTimeout     = 10 * time.Second
    StreamChunkTimeout        = 5 * time.Second
)

// Apply timeouts
clientWs.SetReadDeadline(time.Now().Add(WebSocketReadTimeout))
clientWs.SetWriteDeadline(time.Now().Add(WebSocketWriteTimeout))
```

### 7.2 Resource Cleanup

**Implementation:**
```go
func (ac *AgentConnection) CloseProxyStream(requestID string) error {
    ac.streamsMutex.Lock()
    defer ac.streamsMutex.Unlock()
    
    stream, exists := ac.activeStreams[requestID]
    if !exists {
        return nil
    }
    
    // Signal closure
    close(stream.Done)
    
    // Clean up channels
    close(stream.IncomingChunks)
    close(stream.OutgoingChunks)
    
    // Remove from active streams
    delete(ac.activeStreams, requestID)
    
    // Notify agent
    ac.websocket.WriteJSON(TunnelMessage{
        Type:      MsgTypeProxyStreamClose,
        RequestID: requestID,
    })
    
    return nil
}
```

### 7.3 Graceful Shutdown

**Implementation:**
```go
// On controller shutdown
func (g *Gateway) Shutdown() {
    // Close all active WebSocket streams
    for requestID := range g.activeProxyStreams {
        g.CloseProxyStream(requestID)
    }
    
    // Wait for streams to close
    time.Sleep(1 * time.Second)
    
    // Shutdown gateway
    g.server.Shutdown(context.Background())
}
```

**Why:** Prevents resource leaks and ensures clean disconnection.

---

## Part 8: Logging & Observability

### 8.1 Structured Logging

**Implementation:**
```go
logger.WithFields(logrus.Fields{
    "request_id":  proxyReq.RequestID,
    "host":        r.Host,
    "path":        r.URL.Path,
    "service":     route.ServiceName,
    "namespace":   route.Namespace,
    "cluster_id":  route.ClusterUUID,
    "direction":   "client_to_service", // or "service_to_client"
}).Info("WebSocket message forwarded")
```

### 8.2 Metrics

**Track these metrics:**
```go
// Prometheus metrics
websocketConnectionsActive.Inc()
defer websocketConnectionsActive.Dec()

websocketMessagesTotal.WithLabelValues("client_to_service").Inc()
websocketMessagesTotal.WithLabelValues("service_to_client").Inc()

websocketBytesTotal.WithLabelValues("client_to_service").Add(float64(len(data)))

// Histogram for message latency
start := time.Now()
// ... forward message ...
websocketMessageDuration.Observe(time.Since(start).Seconds())
```

**Why:** Essential for monitoring and troubleshooting WebSocket connections in production.

---

## Part 9: Security Considerations

### 9.1 Origin Validation

**Implementation:**
```go
upgrader := websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        
        // Allow same-origin requests
        if origin == "" {
            return true
        }
        
        // Parse origin
        originURL, err := url.Parse(origin)
        if err != nil {
            return false
        }
        
        // Check against allowed origins
        allowedOrigins := []string{
            "pipeops.io",
            "pipeops.dev",
            // Add customer domains from route
        }
        
        for _, allowed := range allowedOrigins {
            if strings.HasSuffix(originURL.Host, allowed) {
                return true
            }
        }
        
        return false
    },
}
```

### 9.2 Rate Limiting

**Implementation:**
```go
// Per-connection rate limiting
type WebSocketRateLimiter struct {
    messagesPerSecond int
    bytesPerSecond    int64
    lastCheck         time.Time
    messageCount      int
    byteCount         int64
}

func (rl *WebSocketRateLimiter) Allow(messageSize int) bool {
    now := time.Now()
    if now.Sub(rl.lastCheck) >= time.Second {
        rl.messageCount = 0
        rl.byteCount = 0
        rl.lastCheck = now
    }
    
    rl.messageCount++
    rl.byteCount += int64(messageSize)
    
    return rl.messageCount <= rl.messagesPerSecond &&
           rl.byteCount <= rl.bytesPerSecond
}
```

### 9.3 Message Size Limits

**Implementation:**
```go
const (
    MaxWebSocketMessageSize = 10 * 1024 * 1024  // 10MB
)

clientWs.SetReadLimit(MaxWebSocketMessageSize)

// Check before forwarding
if len(data) > MaxWebSocketMessageSize {
    logger.Warn("WebSocket message exceeds size limit")
    return errors.New("message too large")
}
```

**Why:** Prevents abuse and protects against DoS attacks.

---

## Part 10: Testing

### 10.1 Unit Tests

**Test WebSocket upgrade detection:**
```go
func TestIsWebSocketUpgrade(t *testing.T) {
    tests := []struct {
        name     string
        headers  http.Header
        expected bool
    }{
        {
            name: "valid WebSocket upgrade",
            headers: http.Header{
                "Upgrade":    []string{"websocket"},
                "Connection": []string{"Upgrade"},
            },
            expected: true,
        },
        {
            name: "missing upgrade header",
            headers: http.Header{
                "Connection": []string{"Upgrade"},
            },
            expected: false,
        },
        {
            name: "case insensitive",
            headers: http.Header{
                "Upgrade":    []string{"WebSocket"},
                "Connection": []string{"upgrade"},
            },
            expected: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req := &http.Request{Header: tt.headers}
            result := isWebSocketUpgrade(req)
            if result != tt.expected {
                t.Errorf("expected %v, got %v", tt.expected, result)
            }
        })
    }
}
```

### 10.2 Integration Tests

**Test end-to-end WebSocket proxy:**
```go
func TestWebSocketProxy(t *testing.T) {
    // Set up test WebSocket server (simulates in-cluster service)
    testServer := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
        // Echo server
        for {
            var msg string
            if err := websocket.Message.Receive(ws, &msg); err != nil {
                break
            }
            websocket.Message.Send(ws, "echo: "+msg)
        }
    }))
    defer testServer.Close()
    
    // Set up gateway with mock agent
    gateway := setupTestGateway(t)
    
    // Connect client WebSocket to gateway
    clientWs, _, err := websocket.DefaultDialer.Dial(
        "ws://localhost:8080/test",
        http.Header{"Host": []string{"test.example.com"}},
    )
    if err != nil {
        t.Fatal(err)
    }
    defer clientWs.Close()
    
    // Send test message
    if err := clientWs.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
        t.Fatal(err)
    }
    
    // Receive response
    _, msg, err := clientWs.ReadMessage()
    if err != nil {
        t.Fatal(err)
    }
    
    expected := "echo: hello"
    if string(msg) != expected {
        t.Errorf("expected %q, got %q", expected, string(msg))
    }
}
```

### 10.3 Load Tests

**Test concurrent WebSocket connections:**
```go
func TestWebSocketConcurrency(t *testing.T) {
    const numConnections = 100
    
    var wg sync.WaitGroup
    errors := make(chan error, numConnections)
    
    for i := 0; i < numConnections; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            
            // Connect WebSocket
            ws, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/test", nil)
            if err != nil {
                errors <- err
                return
            }
            defer ws.Close()
            
            // Send/receive messages
            for j := 0; j < 10; j++ {
                msg := fmt.Sprintf("conn-%d-msg-%d", id, j)
                if err := ws.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
                    errors <- err
                    return
                }
                
                _, _, err := ws.ReadMessage()
                if err != nil {
                    errors <- err
                    return
                }
            }
        }(i)
    }
    
    wg.Wait()
    close(errors)
    
    // Check for errors
    for err := range errors {
        t.Error(err)
    }
}
```

**Why:** Ensures WebSocket implementation works correctly under load.

---

## Implementation Checklist

### Phase 1: Basic WebSocket Support (Days 1-2)
- [ ] 1. Add `isWebSocketUpgrade()` detection function
- [ ] 2. Set `IsWebSocket: true` in ProxyRequest
- [ ] 3. Implement client WebSocket upgrade in `handleWebSocketProxy()`
- [ ] 4. Add `encodeWebSocketMessage()` and `decodeWebSocketMessage()` helpers
- [ ] 5. Test with simple echo WebSocket service

### Phase 2: Bidirectional Streaming (Days 3-4)
- [ ] 6. Add streaming methods to AgentConnection interface
- [ ] 7. Implement `SendStreamChunk()` and `ReadStreamChunk()`
- [ ] 8. Create `ProxyStream` type with channels
- [ ] 9. Add stream management (create, track, cleanup)
- [ ] 10. Test bidirectional message flow

### Phase 3: Protocol Extension (Day 5)
- [ ] 11. Add `MsgTypeProxyStreamChunk` message type
- [ ] 12. Add `MsgTypeProxyStreamClose` message type
- [ ] 13. Implement message handlers for stream chunks
- [ ] 14. Update WebSocket tunnel protocol documentation
- [ ] 15. Test with agent integration

### Phase 4: Production Hardening (Days 6-7)
- [ ] 16. Add connection timeouts
- [ ] 17. Implement graceful cleanup
- [ ] 18. Add structured logging
- [ ] 19. Implement Prometheus metrics
- [ ] 20. Add origin validation
- [ ] 21. Implement rate limiting
- [ ] 22. Add message size limits
- [ ] 23. Write comprehensive tests

### Phase 5: Testing & Deployment (Days 8-10)
- [ ] 24. Unit test all WebSocket functions
- [ ] 25. Integration test with real agent
- [ ] 26. Load test with 100+ concurrent connections
- [ ] 27. Test with Uptime Kuma
- [ ] 28. Test with other WebSocket apps (Grafana Live, etc.)
- [ ] 29. Deploy to staging
- [ ] 30. Monitor and validate in production

---

## Expected Behavior After Implementation

### Uptime Kuma
```
✅ WebSocket connection: Connected
✅ Real-time status updates
✅ Live monitoring dashboard
✅ No "Cannot connect to socket server" errors
```

### Grafana Live
```
✅ Real-time dashboard updates
✅ Live metric streaming
✅ Alert notifications
```

### Any Socket.IO Application
```
✅ Instant bidirectional communication
✅ Event-driven updates
✅ Real-time collaboration features
```

---

## Performance Targets

| Metric | Target | Acceptable |
|--------|--------|------------|
| **Connection Latency** | <100ms | <200ms |
| **Message Latency** | <50ms | <100ms |
| **Concurrent Connections** | 1000+ | 500+ |
| **Throughput (per connection)** | 10MB/s | 5MB/s |
| **Memory (per connection)** | <100KB | <500KB |
| **CPU (100 connections)** | <10% | <20% |

---

## Troubleshooting Guide

### Problem: WebSocket upgrade fails
**Check:**
- Is `isWebSocketUpgrade()` detecting the request correctly?
- Are upgrade headers being forwarded to the agent?
- Is the agent returning 101 Switching Protocols?

### Problem: Messages only flow one direction
**Check:**
- Is `SendStreamChunk()` implemented?
- Is `ReadStreamChunk()` implemented?
- Are both goroutines (client→agent and agent→client) running?
- Check for deadlocks in channel operations

### Problem: Connection drops after 60 seconds
**Check:**
- Are ping/pong messages being sent?
- Are read/write deadlines being extended?
- Check network firewall/load balancer timeouts

### Problem: High memory usage
**Check:**
- Are streams being cleaned up properly?
- Are channels being closed?
- Check for goroutine leaks with `pprof`

---

## Reference Implementation

**See the agent's implementation for reference:**
- `internal/agent/agent.go` - Lines 2436-2590 (handleWebSocketProxy)
- `internal/agent/agent.go` - Lines 2611-2690 (helper functions)
- `internal/controlplane/types.go` - Line 140 (IsWebSocket field)

**The controller implementation should mirror this but in reverse:**
- Agent handles Service → Controller direction
- Controller must handle Client → Agent direction

---

## Contact & Support

**Questions?**
- Review agent implementation: `/internal/agent/agent.go`
- Check message encoding format: `encodeWebSocketMessage()`
- See test examples: `/internal/agent/agent_test.go`

**Issues?**
- Enable debug logging: `logger.SetLevel(logrus.DebugLevel)`
- Check WebSocket frame types and sizes
- Verify TCP socket options are applied
- Monitor goroutine count for leaks

---

## Summary

**What's Working:**
✅ Agent WebSocket proxy (service ↔ agent)  
✅ TCP optimization (NoDelay, KeepAlive)  
✅ Ping/pong keep-alive  
✅ Header filtering (RFC 7230)  
✅ Graceful connection close  

**What's Needed in Controller:**
❌ WebSocket upgrade detection  
❌ Client WebSocket upgrade  
❌ Bidirectional streaming (client ↔ agent)  
❌ Stream chunk encoding/decoding  
❌ Tunnel protocol extension  

**Estimated Effort:** 8-10 developer days

**Priority:** HIGH - Blocking WebSocket functionality for all applications

---

**Document Version:** 1.0  
**Last Updated:** 2025-11-10  
**Status:** Ready for Implementation
