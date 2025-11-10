# Controller Implementation Checklist - Zero-Copy WebSocket Support

**Date:** 2025-11-10  
**Priority:** HIGH - Required for zero-copy mode to work  
**Estimated Effort:** 2-3 days

---

## Overview

The agent now supports **dual-mode WebSocket proxying**:
1. **Zero-Copy Mode** (UseZeroCopy: true) - 0-1ms latency
2. **WebSocket-Aware Mode** (UseZeroCopy: false) - 5-10ms latency, full observability

The controller must implement support for both modes.

---

## Part 1: Detect Zero-Copy Mode (1 hour)

### 1.1 Update ProxyRequest Handling

**Location:** Gateway proxy handler

**Add these fields to ProxyRequest:**
```go
type ProxyRequest struct {
    // ... existing fields ...
    
    // NEW fields from agent
    IsWebSocket  bool   `json:"is_websocket,omitempty"`
    UseZeroCopy  bool   `json:"use_zero_copy,omitempty"`
    HeadData     []byte `json:"head_data,omitempty"`
}
```

### 1.2 Add Zero-Copy Flag Logic

**Implementation:**
```go
// In gateway request handler
func (g *Gateway) HandleHTTP(w http.ResponseWriter, r *http.Request) {
    // ... existing code ...
    
    // Detect WebSocket
    if isWebSocketUpgrade(r) {
        proxyReq.IsWebSocket = true
        
        // DECISION: Use zero-copy mode?
        // Option 1: Always use zero-copy for production
        proxyReq.UseZeroCopy = true
        
        // Option 2: Use config flag
        // proxyReq.UseZeroCopy = g.config.WebSocket.UseZeroCopy
        
        // Option 3: Per-route configuration
        // proxyReq.UseZeroCopy = route.ZeroCopyEnabled
        
        logger.WithField("zero_copy", proxyReq.UseZeroCopy).Info("WebSocket upgrade detected")
        
        g.handleWebSocketProxy(w, r, route, proxyReq)
        return
    }
    
    // ... existing HTTP proxy logic ...
}
```

**Recommendation:** Start with **Option 1** (always use zero-copy), then add configuration later.

---

## Part 2: Handle Zero-Copy Mode (4 hours)

### 2.1 Implement Dual-Mode WebSocket Handler

**Location:** Gateway WebSocket proxy handler

**Implementation:**
```go
func (g *Gateway) handleWebSocketProxy(w http.ResponseWriter, r *http.Request, route *Route, proxyReq *ProxyRequest) {
    logger := g.logger.WithFields(logrus.Fields{
        "host":       r.Host,
        "path":       r.URL.Path,
        "service":    route.ServiceName,
        "zero_copy":  proxyReq.UseZeroCopy,
    })
    
    if proxyReq.UseZeroCopy {
        // Use zero-copy mode (max performance)
        logger.Info("Using zero-copy WebSocket mode")
        g.handleZeroCopyWebSocket(w, r, route, proxyReq, logger)
    } else {
        // Use WebSocket-aware mode (max observability)
        logger.Info("Using WebSocket-aware mode")
        g.handleWebSocketAwareMode(w, r, route, proxyReq, logger)
    }
}
```

### 2.2 Implement Zero-Copy Mode Handler

**This is the CRITICAL part:**

```go
func (g *Gateway) handleZeroCopyWebSocket(w http.ResponseWriter, r *http.Request, route *Route, proxyReq *ProxyRequest, logger *logrus.Entry) {
    // Step 1: Upgrade client connection
    upgrader := websocket.Upgrader{
        ReadBufferSize:  4096,
        WriteBufferSize: 4096,
        CheckOrigin: func(r *http.Request) bool {
            return true  // Configure properly for production
        },
    }
    
    clientWs, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        logger.WithError(err).Error("Failed to upgrade client connection")
        return
    }
    defer clientWs.Close()
    
    logger.Info("Client WebSocket upgraded")
    
    // Step 2: Send ProxyRequest to agent
    agentConn := g.getAgentConnection(route.ClusterUUID)
    if agentConn == nil {
        logger.Error("No agent connection for cluster")
        clientWs.WriteControl(websocket.CloseMessage, 
            websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "no agent"),
            time.Now().Add(time.Second))
        return
    }
    
    if err := agentConn.SendProxyRequest(proxyReq); err != nil {
        logger.WithError(err).Error("Failed to send proxy request to agent")
        return
    }
    
    // Step 3: Wait for agent's 101 Switching Protocols response
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    agentResp, err := agentConn.WaitForProxyResponse(ctx, proxyReq.RequestID)
    if err != nil {
        logger.WithError(err).Error("Agent upgrade timeout")
        return
    }
    
    if agentResp.Status != http.StatusSwitchingProtocols {
        logger.WithField("status", agentResp.Status).Error("Agent rejected upgrade")
        return
    }
    
    logger.Info("Agent successfully upgraded to WebSocket")
    
    // Step 4: Zero-copy bidirectional forwarding
    // THIS IS THE MAGIC - Raw byte copying without WebSocket parsing
    
    done := make(chan struct{})
    errChan := make(chan error, 2)
    
    // Get underlying TCP connection from client WebSocket
    clientConn := clientWs.UnderlyingConn()
    
    // Direction 1: Client → Agent (raw bytes)
    go func() {
        for {
            // Read raw bytes from client WebSocket
            _, data, err := clientWs.ReadMessage()
            if err != nil {
                if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
                    logger.Debug("Client closed connection")
                } else {
                    errChan <- err
                }
                return
            }
            
            // Send raw bytes to agent via stream
            if err := agentConn.SendStreamChunk(proxyReq.RequestID, data); err != nil {
                logger.WithError(err).Error("Failed to send to agent")
                errChan <- err
                return
            }
        }
    }()
    
    // Direction 2: Agent → Client (raw bytes)
    go func() {
        defer close(done)
        for {
            // Read raw bytes from agent stream
            data, err := agentConn.ReadStreamChunk(proxyReq.RequestID)
            if err != nil {
                if err == io.EOF {
                    logger.Debug("Agent stream closed")
                } else {
                    errChan <- err
                }
                return
            }
            
            // Write raw bytes to client WebSocket
            if err := clientWs.WriteMessage(websocket.BinaryMessage, data); err != nil {
                logger.WithError(err).Error("Failed to write to client")
                errChan <- err
                return
            }
        }
    }()
    
    // Wait for completion
    select {
    case <-done:
        logger.Info("Zero-copy WebSocket proxy completed")
    case err := <-errChan:
        logger.WithError(err).Warn("Zero-copy proxy error")
    }
    
    // Cleanup
    closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
    clientWs.WriteControl(websocket.CloseMessage, closeMsg, time.Now().Add(time.Second))
    agentConn.CloseProxyStream(proxyReq.RequestID)
}
```

---

## Part 3: Implement Stream Chunk Methods (3 hours)

### 3.1 Add Streaming to AgentConnection

**These methods are REQUIRED:**

```go
type AgentConnection struct {
    // ... existing fields ...
    
    // NEW: Stream management
    activeStreams  map[string]*ProxyStream
    streamsMutex   sync.RWMutex
}

type ProxyStream struct {
    RequestID      string
    IncomingChunks chan []byte  // Agent → Controller
    OutgoingChunks chan []byte  // Controller → Agent
    Done           chan struct{}
    Created        time.Time
}

// SendStreamChunk sends data from controller to agent
func (ac *AgentConnection) SendStreamChunk(requestID string, data []byte) error {
    ac.streamsMutex.RLock()
    stream, exists := ac.activeStreams[requestID]
    ac.streamsMutex.RUnlock()
    
    if !exists {
        // Create stream on first use
        stream = &ProxyStream{
            RequestID:      requestID,
            IncomingChunks: make(chan []byte, 100),
            OutgoingChunks: make(chan []byte, 100),
            Done:           make(chan struct{}),
            Created:        time.Now(),
        }
        
        ac.streamsMutex.Lock()
        ac.activeStreams[requestID] = stream
        ac.streamsMutex.Unlock()
        
        // Start goroutine to forward outgoing chunks to agent
        go ac.forwardStreamChunksToAgent(stream)
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

// ReadStreamChunk reads data from agent stream
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

// CloseProxyStream closes the stream
func (ac *AgentConnection) CloseProxyStream(requestID string) error {
    ac.streamsMutex.Lock()
    defer ac.streamsMutex.Unlock()
    
    stream, exists := ac.activeStreams[requestID]
    if !exists {
        return nil
    }
    
    // Signal closure
    close(stream.Done)
    
    // Notify agent to close stream
    ac.sendStreamCloseMessage(requestID)
    
    // Remove from active streams
    delete(ac.activeStreams, requestID)
    
    return nil
}

// forwardStreamChunksToAgent sends outgoing chunks to agent
func (ac *AgentConnection) forwardStreamChunksToAgent(stream *ProxyStream) {
    for {
        select {
        case chunk := <-stream.OutgoingChunks:
            // Send to agent via WebSocket tunnel
            msg := StreamChunkMessage{
                Type:      "proxy_stream_chunk",
                RequestID: stream.RequestID,
                Data:      chunk,
                Direction: "to_agent",
            }
            ac.websocket.WriteJSON(msg)
            
        case <-stream.Done:
            return
        }
    }
}
```

### 3.2 Handle Incoming Stream Chunks from Agent

**Add to agent message handler:**

```go
func (ac *AgentConnection) handleIncomingMessage() {
    for {
        var msg TunnelMessage
        if err := ac.websocket.ReadJSON(&msg); err != nil {
            return
        }
        
        switch msg.Type {
        case "proxy_response":
            // ... existing proxy response handling ...
            
        case "proxy_chunk":
            // NEW: Handle stream chunk from agent
            ac.streamsMutex.RLock()
            if stream, exists := ac.activeStreams[msg.RequestID]; exists {
                select {
                case stream.IncomingChunks <- msg.Data:
                    // Successfully queued
                case <-stream.Done:
                    // Stream closed, discard
                default:
                    // Channel full, log warning
                    log.Warn("Stream chunk dropped: buffer full")
                }
            }
            ac.streamsMutex.RUnlock()
            
        case "proxy_stream_close":
            // Agent is closing the stream
            ac.CloseProxyStream(msg.RequestID)
        }
    }
}
```

---

## Part 4: Add Message Types to Tunnel Protocol (1 hour)

### 4.1 Extend Tunnel Message Types

**Add these to your tunnel protocol:**

```go
const (
    MsgTypeProxyRequest      = "proxy_request"       // Existing
    MsgTypeProxyResponse     = "proxy_response"      // Existing
    MsgTypeProxyChunk        = "proxy_chunk"         // NEW: Data chunk
    MsgTypeProxyStreamClose  = "proxy_stream_close"  // NEW: Close stream
)

type StreamChunkMessage struct {
    Type      string `json:"type"`      // "proxy_chunk"
    RequestID string `json:"request_id"`
    Data      []byte `json:"data"`
    Direction string `json:"direction"` // "to_agent" or "from_agent"
}

type StreamCloseMessage struct {
    Type      string `json:"type"`      // "proxy_stream_close"
    RequestID string `json:"request_id"`
    Reason    string `json:"reason,omitempty"`
}
```

---

## Part 5: Configuration & Monitoring (2 hours)

### 5.1 Add Configuration

```yaml
# config.yaml
websocket:
  # Enable zero-copy mode globally
  zero_copy_enabled: true
  
  # Buffer sizes
  read_buffer_size: 4096
  write_buffer_size: 4096
  stream_buffer_size: 100
  
  # Timeouts
  upgrade_timeout: 10s
  stream_send_timeout: 5s
  
  # Monitoring
  log_performance_metrics: true
```

### 5.2 Add Prometheus Metrics

```go
var (
    wsConnectionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "websocket_connections_active",
        Help: "Number of active WebSocket connections",
    })
    
    wsZeroCopyMode = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "websocket_mode_total",
            Help: "WebSocket connections by mode",
        },
        []string{"mode"}, // "zero_copy" or "websocket_aware"
    )
    
    wsStreamBytesTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "websocket_stream_bytes_total",
            Help: "Total bytes transferred via WebSocket streams",
        },
        []string{"direction"}, // "to_agent" or "from_agent"
    )
    
    wsStreamLatency = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "websocket_stream_latency_seconds",
            Help:    "WebSocket stream chunk latency",
            Buckets: prometheus.DefBuckets,
        },
    )
)

// Use in code
wsConnectionsActive.Inc()
defer wsConnectionsActive.Dec()

wsZeroCopyMode.WithLabelValues("zero_copy").Inc()
wsStreamBytesTotal.WithLabelValues("to_agent").Add(float64(len(data)))
```

---

## Part 6: Testing (4 hours)

### 6.1 Unit Tests

```go
func TestZeroCopyMode(t *testing.T) {
    // Test that UseZeroCopy flag is set correctly
    req := &ProxyRequest{
        IsWebSocket:  true,
        UseZeroCopy:  true,
    }
    
    if !req.UseZeroCopy {
        t.Error("Expected UseZeroCopy to be true")
    }
}

func TestStreamChunks(t *testing.T) {
    conn := &AgentConnection{
        activeStreams: make(map[string]*ProxyStream),
    }
    
    // Test sending chunk
    err := conn.SendStreamChunk("test-123", []byte("hello"))
    if err != nil {
        t.Errorf("Failed to send chunk: %v", err)
    }
    
    // Test reading chunk
    data, err := conn.ReadStreamChunk("test-123")
    if err != nil {
        t.Errorf("Failed to read chunk: %v", err)
    }
    
    if string(data) != "hello" {
        t.Errorf("Expected 'hello', got %s", string(data))
    }
}
```

### 6.2 Integration Tests

```go
func TestEndToEndZeroCopy(t *testing.T) {
    // 1. Set up test WebSocket server (simulates service)
    server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
        // Echo server
        io.Copy(ws, ws)
    }))
    defer server.Close()
    
    // 2. Set up gateway with mock agent
    gateway := setupTestGateway(t)
    
    // 3. Connect client
    client, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/test", nil)
    if err != nil {
        t.Fatal(err)
    }
    defer client.Close()
    
    // 4. Send message
    testMsg := []byte("hello world")
    if err := client.WriteMessage(websocket.BinaryMessage, testMsg); err != nil {
        t.Fatal(err)
    }
    
    // 5. Receive echo
    _, msg, err := client.ReadMessage()
    if err != nil {
        t.Fatal(err)
    }
    
    if !bytes.Equal(msg, testMsg) {
        t.Errorf("Expected %s, got %s", testMsg, msg)
    }
}
```

---

## Implementation Checklist

### Day 1: Basic Structure (6 hours)
- [ ] Add UseZeroCopy and HeadData fields to ProxyRequest
- [ ] Implement isWebSocketUpgrade() detection
- [ ] Create handleWebSocketProxy() routing function
- [ ] Implement dual-mode decision logic
- [ ] Test with simple WebSocket echo server

### Day 2: Streaming Implementation (6 hours)
- [ ] Add ProxyStream struct
- [ ] Implement SendStreamChunk()
- [ ] Implement ReadStreamChunk()
- [ ] Implement CloseProxyStream()
- [ ] Add stream forwarding goroutines
- [ ] Add tunnel protocol message types
- [ ] Test bidirectional streaming

### Day 3: Zero-Copy Mode (4 hours)
- [ ] Implement handleZeroCopyWebSocket()
- [ ] Add client WebSocket upgrade
- [ ] Implement bidirectional forwarding
- [ ] Add performance logging
- [ ] Test with Uptime Kuma

### Day 3: Polish & Deploy (4 hours)
- [ ] Add configuration options
- [ ] Add Prometheus metrics
- [ ] Write documentation
- [ ] Deploy to staging
- [ ] Monitor and validate

---

## Success Criteria

After implementation, you should see:

### Logs
```
INFO  WebSocket upgrade detected zero_copy=true
INFO  Client WebSocket upgraded
INFO  Agent successfully upgraded to WebSocket
INFO  Zero-copy WebSocket proxy completed duration_ms=1234 bytes_total=567890
```

### Metrics
```
websocket_connections_active{} 42
websocket_mode_total{mode="zero_copy"} 1000
websocket_mode_total{mode="websocket_aware"} 50
websocket_stream_bytes_total{direction="to_agent"} 1234567
websocket_stream_bytes_total{direction="from_agent"} 7654321
```

### Application Behavior
- ✅ Uptime Kuma shows "Connected"
- ✅ Real-time updates work
- ✅ No "Cannot connect to socket server" errors
- ✅ Latency < 50ms end-to-end

---

## Troubleshooting Guide

### Problem: Agent sends 101 but no data flows
**Solution:** Check that `SendStreamChunk()` and `ReadStreamChunk()` are implemented

### Problem: Client connects but immediately disconnects
**Solution:** Check that tunnel protocol messages are being handled

### Problem: High latency despite zero-copy mode
**Solution:** Verify that raw bytes are being forwarded, not parsed

### Problem: Memory leak
**Solution:** Ensure `CloseProxyStream()` is called and channels are closed

---

## Summary

**What Controller Needs:**
1. Detect `UseZeroCopy` flag
2. Implement stream chunk methods
3. Handle bidirectional forwarding
4. Add tunnel protocol messages
5. Monitor and test

**Estimated Time:** 2-3 days  
**Complexity:** Medium  
**Priority:** HIGH

**Once this is done, you'll have KubeSail-level performance! 🚀**
