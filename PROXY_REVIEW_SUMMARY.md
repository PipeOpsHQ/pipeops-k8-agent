# WebSocket Proxy Architecture Review & Improvement Recommendations

## Executive Summary

The current WebSocket-based proxy architecture is **well-designed** with good separation of concerns, but has several opportunities for improving **stability, performance, and reliability** under high load.

---

## Architecture Overview

### Agent Side (pipeops-vm-agent)
- **WebSocket Client**: Maintains persistent connection to control plane
- **Proxy Handler**: Receives proxy requests via WebSocket, executes against local K8s API, streams response back
- **K8s Client**: Uses in-cluster credentials to proxy requests to Kubernetes API

### Controller Side (pipeops-controller)
- **WebSocket Hub**: Manages agent connections and routes proxy requests
- **Tunnel Manager**: Dispatches requests with timeout/circuit breaker
- **Agent Controller**: Handles HTTP→WebSocket proxy translation with rate limiting

---

## Current Strengths ✅

1. **Streaming Support**: Handles large responses efficiently with chunked streaming
2. **Circuit Breaker**: Prevents overload with `MaxPendingRequestsPerConnection`
3. **Rate Limiting**: Token-based rate limiting on controller side
4. **Timeout Handling**: Multiple timeout layers (context, request, WebSocket)
5. **Reconnection Logic**: Exponential backoff with max delay
6. **Proper Resource Cleanup**: Deferred cleanup and proper body closing
7. **Metrics**: Prometheus metrics for proxy performance tracking

---

## Critical Issues & Improvements 🔴

### 1. **Memory Leak Risk - Unbounded Buffering**

**Problem**: Agent buffers entire response in memory before sending
```go
// agent/agent.go:1319 - Buffering up to EOF
buf := make([]byte, 32*1024)
for {
    n, readErr := respBody.Read(buf)
    if n > 0 {
        if err := writer.WriteChunk(buf[:n]); err != nil {
            // What if control plane is slow? Buffer fills memory
        }
    }
}
```

**Impact**: Large K8s API responses (pod logs, large ConfigMaps) can cause OOM

**Recommendation**:
```go
// Add backpressure mechanism
type bufferedWriter struct {
    semaphore chan struct{} // Limit in-flight chunks
    maxChunks int
}

func (w *bufferedWriter) WriteChunk(data []byte) error {
    // Block if too many chunks in flight
    select {
    case w.semaphore <- struct{}{}:
        defer func() { <-w.semaphore }()
        return w.actualWrite(data)
    case <-time.After(10 * time.Second):
        return errors.New("backpressure timeout")
    }
}
```

### 2. **WebSocket Write Contention**

**Problem**: Single writeMutex serializes ALL writes
```go
// websocket_client.go:556
func (c *WebSocketClient) sendMessage(msg *WebSocketMessage) error {
    c.writeMutex.Lock()  // Global lock for all messages
    defer c.writeMutex.Unlock()
    return conn.WriteJSON(msg)
}
```

**Impact**: Heartbeats/pings blocked by slow proxy responses → connection drop

**Recommendation**:
```go
// Separate channels for different message priorities
type priorityWebSocket struct {
    controlChan chan *WebSocketMessage  // Heartbeats, pings (priority)
    proxyChan   chan *WebSocketMessage  // Proxy responses (can be slower)
}

// Dedicated goroutine drains both with priority
func (c *WebSocketClient) writeLoop() {
    for {
        select {
        case msg := <-c.controlChan:
            c.writeWithTimeout(msg, 5*time.Second)
        case msg := <-c.proxyChan:
            c.writeWithTimeout(msg, 30*time.Second)
        default:
            // Handle either
        }
    }
}
```

### 3. **No Request Cancellation Propagation**

**Problem**: Controller timeout doesn't cancel agent's K8s request
```go
// agent/agent.go:1275
ctx, cancel := a.proxyRequestContext(req)  // Has timeout
defer cancel()

// But K8s request continues even after context cancelled
statusCode, _, respBody, err := a.k8sClient.ProxyRequest(ctx, ...)
```

**Impact**: Agent wastes resources on cancelled requests

**Recommendation**:
```go
// Controller: Send cancellation signal
func (m *Manager) cancelRequest(reqID string) {
    envelope := map[string]interface{}{
        "type":       "proxy_cancel",
        "request_id": reqID,
    }
    conn.socket.SendJSON(envelope)
}

// Agent: Handle cancellation
case "proxy_cancel":
    if cancel, ok := c.activeRequests[msg.RequestID]; ok {
        cancel()  // Cancel context
        delete(c.activeRequests, msg.RequestID)
    }
```

### 4. **Inefficient Header Filtering**

**Problem**: Header filtering done twice (agent + controller)
```go
// agent/agent.go:1298 - Manual filtering
for key, values := range respHeaders {
    if strings.EqualFold(key, "connection") ||
       strings.EqualFold(key, "transfer-encoding") || ...
}

// websocket_client.go:774 - Same filtering again
func sanitizeHeaders(...) {
    // Duplicate logic
}
```

**Recommendation**: Extract to shared package or do once

### 5. **Missing Health Checks for Proxy Path**

**Problem**: No way to detect if agent is healthy for proxying
```go
// Agent could be connected but K8s client broken
if a.k8sClient == nil {
    // Only discovered when request fails
}
```

**Recommendation**:
```go
// Periodic health probe
func (c *WebSocketClient) sendHealthProbe() {
    probe := &WebSocketMessage{
        Type: "health_probe",
        Payload: map[string]interface{}{
            "k8s_healthy": c.checkK8sHealth(),
            "disk_space":  c.checkDiskSpace(),
        },
    }
    c.sendMessage(probe)
}
```

---

## Performance Optimizations ⚡

### 6. **Controller: Pending Request Cleanup Inefficiency**

**Problem**: O(n) scan to cleanup pending requests
```go
// hub.go: No efficient way to find timed-out requests
m.pendingMu.Lock()
delete(m.pending, reqID)
delete(m.pendingInfo, reqID)
m.pendingMu.Unlock()
```

**Recommendation**: Use time-ordered heap for O(log n) cleanup

### 7. **Agent: Buffer Pool Optimization**

**Problem**: Allocates new 32KB buffer for each proxy request
```go
buf := make([]byte, 32*1024)  // Per request
```

**Recommendation**:
```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        b := make([]byte, 32*1024)
        return &b
    },
}

func (a *Agent) handleProxyRequest(...) {
    bufPtr := bufferPool.Get().(*[]byte)
    defer bufferPool.Put(bufPtr)
    buf := *bufPtr
    // Use buf
}
```

### 8. **WebSocket Compression**

**Problem**: Compression disabled
```go
// websocket_client.go:149
EnableCompression: false,  // Why disabled?
```

**Recommendation**: Enable compression for large JSON payloads
```go
EnableCompression: true,
```

### 9. **Agent: Concurrent Proxy Handling**

**Problem**: No limit on concurrent proxy requests
```go
case "proxy_request":
    go handler(req)  // Unbounded goroutines
```

**Recommendation**:
```go
// Add worker pool
type proxyWorkerPool struct {
    semaphore chan struct{}
}

func (p *proxyWorkerPool) Handle(req *ProxyRequest) {
    p.semaphore <- struct{}{}  // Acquire slot
    go func() {
        defer func() { <-p.semaphore }()  // Release
        handler(req)
    }()
}
```

---

## Stability Improvements 🛡️

### 10. **Connection State Management**

**Problem**: Race conditions in reconnection logic
```go
// Multiple goroutines can trigger reconnect
func (c *WebSocketClient) reconnect() {
    time.Sleep(c.reconnectDelay)
    if err := c.Connect(); err != nil {
        go c.reconnect()  // Can have multiple reconnect goroutines
    }
}
```

**Recommendation**: Add reconnection mutex

### 11. **Graceful Degradation**

**Problem**: Controller returns 503 if agent disconnected
```go
if !awc.tunnelManager.IsConnected(clusterUUID) {
    return 503  // Hard failure
}
```

**Recommendation**: Add fallback or queue
```go
// Option 1: Queue requests during brief disconnects
if !awc.tunnelManager.IsConnected(clusterUUID) {
    if time.Since(lastDisconnect) < 10*time.Second {
        return queueRequest(request)  // Retry when reconnects
    }
    return 503
}

// Option 2: Direct K8s API fallback (if credentials available)
```

### 12. **Better Error Context**

**Problem**: Generic errors don't help debugging
```go
return fmt.Errorf("proxy request failed")
```

**Recommendation**: Structured errors with context
```go
type ProxyError struct {
    Code       string
    Message    string
    ClusterID  string
    Path       string
    Cause      error
    Retryable  bool
}
```

---

## Monitoring Improvements 📊

### 13. **Missing Key Metrics**

**Add metrics for**:
- WebSocket connection duration
- Proxy request queue depth per cluster
- P95/P99 latency by path pattern
- Agent-side K8s API error rates
- Memory usage during large responses

### 14. **Request Tracing**

**Problem**: No way to trace request through system

**Recommendation**: Add trace IDs
```go
// Controller generates trace ID
headers["X-Trace-ID"] = []string{uuid.NewString()}

// Agent logs with trace ID
logger.WithField("trace_id", traceID).Info("...")

// Shows full request path: user→controller→agent→k8s→agent→controller→user
```

---

## Configuration Recommendations ⚙️

### Current Timeouts (Review Needed)

**Agent**:
```go
DefaultK8sTimeout = 30 * time.Second     // K8s API timeout
ProxyRequestTimeout = 2 * time.Minute    // Default proxy timeout
WriteDeadline = 30 * time.Second         // WebSocket write
```

**Controller**:
```go
defaultProxyRequestTimeout = 30 * time.Second  // ⚠️ Agent has 2min, controller 30s
```

**Recommendation**: Align timeouts
```go
// Controller should be >= agent timeout + network overhead
controllerTimeout = agentTimeout + 10*time.Second
```

---

## Priority Implementation Plan 🎯

### Phase 1: Critical Fixes (Week 1)
1. ✅ Fix timeout alignment (controller vs agent)
2. ✅ Add backpressure to prevent OOM on large responses
3. ✅ Implement request cancellation propagation
4. ✅ Add reconnection mutex to prevent races

### Phase 2: Performance (Week 2)
1. Implement buffer pooling
2. Add priority queues for WebSocket writes
3. Add worker pool for concurrent proxy handling
4. Enable WebSocket compression

### Phase 3: Observability (Week 3)
1. Add missing metrics
2. Implement distributed tracing
3. Add health probes for proxy path
4. Structured error types

### Phase 4: Reliability (Week 4)
1. Implement request queueing during brief disconnects
2. Add circuit breakers per cluster
3. Implement gradual backoff for failing clusters
4. Add proxy response caching for read-only endpoints

---

## Testing Recommendations 🧪

### Load Testing Scenarios

1. **High Volume Test**: 1000 concurrent proxy requests
2. **Large Response Test**: Stream 100MB pod logs
3. **Connection Flap Test**: Rapid connect/disconnect cycles
4. **Timeout Test**: Verify cancellation propagates correctly
5. **Memory Test**: Monitor for leaks under sustained load

### Integration Tests Needed

```go
func TestProxyRequestCancellation(t *testing.T) {
    // Start proxy request
    // Cancel context after 1s
    // Verify K8s request also cancelled
    // Verify no goroutine leak
}

func TestBackpressure(t *testing.T) {
    // Slow controller (simulate network lag)
    // Fast agent (large response)
    // Verify agent doesn't OOM
}

func TestReconnectionRace(t *testing.T) {
    // Kill connection
    // Verify only one reconnect attempt active
}
```

---

## Summary of Key Changes for Controller

### High-Impact Changes (Controller Side)

1. **Align Timeouts**
```go
// services/tunnel/hub.go
const defaultProxyRequestTimeout = 2*time.Minute + 10*time.Second
```

2. **Add Request Cancellation**
```go
func (m *Manager) CancelRequest(reqID string, clusterUUID string) error {
    conn, err := m.getConnection(clusterUUID)
    if err != nil {
        return err
    }
    
    envelope := map[string]interface{}{
        "type":       "proxy_cancel",
        "request_id": reqID,
    }
    
    return conn.socket.SendJSON(envelope)
}

// Call on context cancellation
```

3. **Add Graceful Degradation**
```go
func (awc *AgentWebSocketController) ProxyClusterRequest(c *gin.Context) {
    if !awc.tunnelManager.IsConnected(clusterUUID) {
        disconnectTime := awc.tunnelManager.GetDisconnectTime(clusterUUID)
        
        if time.Since(disconnectTime) < 10*time.Second {
            // Agent just disconnected, might reconnect
            c.JSON(http.StatusServiceUnavailable, gin.H{
                "success": false,
                "message": "Cluster temporarily unavailable, please retry",
                "error":   "cluster_reconnecting",
                "retry_after": 5,
            })
            return
        }
        
        // Long disconnect - truly offline
        c.JSON(http.StatusServiceUnavailable, gin.H{
            "success": false,
            "message": "Cluster is not connected",
            "error":   "cluster_offline",
        })
        return
    }
    // ... rest of proxy logic
}
```

4. **Add Better Metrics**
```go
var (
    ProxyLatencyByPathPattern = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "agent_proxy_latency_by_path",
            Help: "Proxy request latency by path pattern",
            Buckets: []float64{.01, .05, .1, .5, 1, 2.5, 5, 10},
        },
        []string{"path_pattern", "cluster_uuid"},
    )
    
    ProxyQueueDepth = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "agent_proxy_queue_depth",
            Help: "Number of queued proxy requests per cluster",
        },
        []string{"cluster_uuid"},
    )
)
```

---

## Conclusion

The current architecture is solid but needs **production hardening**. The most critical issues are:

1. **Memory safety** - Unbounded buffering can cause OOM
2. **Timeout alignment** - Controller/agent timeout mismatch
3. **Cancellation** - Wasted resources on cancelled requests
4. **Write contention** - Single mutex blocks critical messages

Implementing these improvements will make the proxy **production-grade** for handling high-volume traffic reliably.
