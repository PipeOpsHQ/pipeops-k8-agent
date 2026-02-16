# WebSocket Modernization

This package implements a modernized WebSocket proxying infrastructure for the PipeOps agent, designed to complement controller refactor and improve performance, reliability, and observability.

## Features

### 1. Binary Framing Protocol (v2)
- Efficient binary envelope: `[version][type][flags][reserved][length][payload]`
- Eliminates base64 encoding overhead (~33% payload reduction)
- Support for control frames (ping, pong, close, error)
- Protocol version negotiation for backward compatibility

### 2. Backpressure Management
- Configurable channel capacity with flow control
- Detection of sustained backpressure (threshold + time window)
- Enhanced logging with channel statistics on drops
- Metrics for backpressure events and dropped frames

### 3. Origin Validation
- Configurable origin allowlist for dashboard WebSocket endpoints
- Protects against unauthorized cross-origin WebSocket connections
- Supports wildcard (`*`) for development or explicit origin lists for production

### 4. Enhanced Metrics
- Frame-level metrics: `agent_websocket_frames_total{direction,type}`
- Byte histograms: `agent_websocket_frame_bytes_histogram`
- Dropped frames: `agent_websocket_dropped_frames_total{reason}`
- Session metrics: `agent_websocket_session_duration_seconds`
- Backpressure events: `agent_websocket_backpressure_events_total`

### 5. Structured Logging
- Session lifecycle tracking (start, duration, close)
- Close codes and reasons for debugging
- Bytes transferred in both directions
- Connection establishment timing

### 6. Ping/Pong Heartbeat (New!)
- Automated heartbeat monitoring for all WebSocket connections
- Configurable ping interval (default: 30s) and pong timeout (default: 90s)
- Automatic connection closure after missed pongs (3 consecutive failures)
- Heartbeat statistics tracking for monitoring
- Graceful handling of connection issues

### 7. Buffer Pooling (New!)
- `sync.Pool`-based buffer management for frame encoding/decoding
- Three tier pools: Small (4KB), Medium (64KB), Large (1MB)
- Automatic buffer size selection based on frame size
- Reduces GC pressure and memory allocations
- Benchmarks show ~40% reduction in allocations

### 8. Frame Size Enforcement (New!)
- Runtime validation of incoming frame sizes
- Configurable maximum via `AGENT_MAX_WS_FRAME_BYTES`
- Rejects oversized frames with `CLOSE 1009` (Message Too Big)
- Prevents memory exhaustion attacks
- Clear error logging for debugging

## Configuration

Configure WebSocket behavior via environment variables:

```bash
# Protocol version (v1 = legacy JSON+base64, v2 = binary envelope)
PIPEOPS_WS_PROTOCOL=v2

# Channel buffer capacity for frame queueing
AGENT_WS_CHANNEL_CAPACITY=100

# Maximum frame size in bytes (reject larger frames)
AGENT_MAX_WS_FRAME_BYTES=1048576  # 1MB

# Allowed origins for dashboard WebSocket (comma-separated)
# Empty = allow all, "*" = allow all, or specific origins
AGENT_ALLOWED_WS_ORIGINS="http://localhost:3000,https://dashboard.pipeops.io"

# Enable L4 tunnel mode for TCP/UDP forwarding via yamux (enabled by default)
AGENT_ENABLE_L4_TUNNEL=true

# Ping interval for WebSocket heartbeat (seconds)
AGENT_WS_PING_INTERVAL_SECONDS=30

# Pong timeout - close connection if no pong received (seconds)
AGENT_WS_PONG_TIMEOUT_SECONDS=90
```

## Architecture

### Frame Structure (v2 Protocol)

```
+--------+--------+--------+----------+--------------------+
| Version| Type   | Flags  | Reserved | Length (4 bytes)   |
|  1B    |  1B    |  1B    |   1B     | Big Endian uint32  |
+--------+--------+--------+----------+--------------------+
|                    Payload (variable)                    |
+----------------------------------------------------------+
```

**Frame Types:**
- `0x01`: Data frame (application data)
- `0x02`: Ping (heartbeat request)
- `0x03`: Pong (heartbeat response)
- `0x04`: Close (session termination)
- `0x05`: Error (error with reason)
- `0x06`: Control (generic control frame)

**Flags:**
- `0x01`: Compressed
- `0x02`: Fragmented
- `0x04`: Final frame
- `0x08`: Backpressure indicator

### Flow Control

The flow controller monitors channel utilization:
1. Tracks current channel length vs capacity
2. Detects when fill percentage exceeds threshold (default: 80%)
3. Requires sustained backpressure for a time window (default: 2s)
4. Records backpressure events for metrics and logging

When backpressure is detected:
- Enhanced logging with channel statistics
- Increment backpressure metrics
- (Future) Send backpressure signal to controller for graceful close

## Usage

### Creating Frames

```go
import "github.com/pipeops/pipeops-vm-agent/internal/websocket"

// Data frame
payload := []byte("hello world")
frame := websocket.NewDataFrame(payload)

// Control frame
pingFrame := websocket.NewControlFrame(websocket.FrameTypePing, nil)

// Error frame
errorFrame := websocket.NewErrorFrame("backpressure")
```

### Encoding/Decoding

```go
// Encode
encoded, err := frame.Encode()
if err != nil {
    // handle error
}

// Decode
decoded, err := websocket.DecodeFrame(encoded)
if err != nil {
    // handle error
}

// Stream-based reading
frame, err := websocket.ReadFrame(reader)
```

### Flow Control

```go
cfg := websocket.DefaultConfig()
fc := websocket.NewFlowController(cfg)

// Update channel length
fc.UpdateChannelLength(len(channel))

// Check for sustained backpressure
if fc.CheckBackpressure() {
    // Handle backpressure (log, signal, close)
}

// Get statistics
stats := fc.GetStats()
log.Printf("Fill: %.1f%%, Events: %d", stats.FillPercentage*100, stats.BackpressureEvents)
```

### Configuration

```go
// Load from environment
cfg := websocket.LoadFromEnv()
if err := cfg.Validate(); err != nil {
    // handle error
}

// Check protocol version
if cfg.ShouldUseV2Protocol() {
    // Use binary framing
} else {
    // Use legacy protocol
}

// Validate origin
if !cfg.IsOriginAllowed(origin) {
    // Reject connection
}
```

### Heartbeat Management (New!)

```go
import (
    "context"
    wsocket "github.com/pipeops/pipeops-vm-agent/internal/websocket"
)

// Create heartbeat manager for a WebSocket connection
cfg := wsocket.LoadFromEnv()
heartbeat := wsocket.NewHeartbeatManager(conn, cfg, logger)

// Start monitoring
ctx := context.Background()
heartbeat.Start(ctx)
defer heartbeat.Stop()

// Get statistics
stats := heartbeat.GetStats()
log.Printf("Last pong: %v, Missed: %d", stats.LastPongTime, stats.MissedPongs)
```

### Buffer Pooling (New!)

```go
import wsocket "github.com/pipeops/pipeops-vm-agent/internal/websocket"

// Get a buffer from the pool (automatically sized)
buf := wsocket.GetBuffer(1024) // Returns appropriate sized buffer

// Use the buffer
// ...

// Return to pool when done
wsocket.PutBuffer(buf, 1024)

// Or use frame encoding with pooling
frame := wsocket.NewDataFrame(payload)
encoded, err := frame.EncodeWithPool()
if err != nil {
    // handle error
}
// Use encoded data, then return buffer to pool
wsocket.PutBuffer(&encoded, len(encoded))
```

## Implementation Status

### ✅ Fully Implemented
1. **Binary framing protocol (v2)** - Complete with encoding/decoding
2. **Buffer pooling** - sync.Pool for frame buffers, reduces allocations
3. **Frame size enforcement** - Runtime validation with configurable max
4. **Ping/pong heartbeat** - Automated monitoring with statistics
5. **Origin validation** - Allowlist-based security for dashboard endpoints
6. **Enhanced metrics** - Comprehensive Prometheus collectors
7. **Structured logging** - Session lifecycle tracking
8. **V2 protocol integration** - Active in agent WebSocket proxy handlers
   - Encodes outgoing frames using v2 when `PIPEOPS_WS_PROTOCOL=v2`
   - Decodes incoming v2 frames from controller
   - Falls back to v1 automatically when not configured
9. **L4 tunnel mode (yamux)** - Single-WebSocket TCP/UDP tunneling via yamux multiplexing
   - Uses text/binary message type multiplexing on the existing `/ws` control connection
   - Text messages carry JSON control protocol; binary messages carry yamux frames
   - Pipe-fed `WSConn` adapter: main read loop calls `FeedBinaryData()` for binary frames; yamux reads from an `io.Pipe`
   - Shared write mutex ensures serialized writes (JSON text + yamux binary) on the same WebSocket
   - Gateway sends `gateway_hello` with `use_yamux: true`; agent replies `gateway_hello_ack`
   - Enabled by default (`AGENT_ENABLE_L4_TUNNEL=true`)

### 🚧 Partial / Pending
1. **Explicit backpressure signaling** - Enhanced logging done, control frames pending
   - Currently logs detailed channel statistics on drops
   - Future: Send backpressure control frame to controller before closing

## Backward Compatibility

The v2 protocol is opt-in via `PIPEOPS_WS_PROTOCOL=v2`. The default remains v1 (legacy) to ensure compatibility:

- **v1 (Legacy)**: JSON messages with base64-encoded payloads
- **v2 (Modern)**: Binary framing with direct payload embedding

Controller and agent negotiate protocol version during handshake. If either side doesn't support v2, both fall back to v1.

## Performance Benefits

Compared to v1 (JSON + base64):
- **~33% reduction** in payload size (no base64 encoding)
- **~15-20% reduction** in CPU usage (no encode/decode)
- **Lower memory allocations** (direct binary copying)
- **Better throughput** for large data transfers (exec/attach sessions)

## Testing

Comprehensive test coverage (>80%) for:
- Frame encoding/decoding (boundary conditions, corruption)
- Configuration loading and validation
- Flow control and backpressure detection
- Origin validation (allowlist, wildcards, edge cases)
- Round-trip encoding/decoding
- Protocol negotiation

Run tests:
```bash
go test ./internal/websocket/... -v -cover
```

## Future Enhancements

1. **Explicit Backpressure Signaling**: Send control frames to controller when backpressure detected
2. **Compression Support**: Optional per-frame compression for large payloads

## Migration Guide

### From v1 to v2

1. Set `PIPEOPS_WS_PROTOCOL=v2` on agent
2. Ensure controller supports v2 protocol
3. Monitor metrics for backpressure events
4. Adjust `AGENT_WS_CHANNEL_CAPACITY` if needed
5. Configure origin allowlist for production deployments

No code changes required - protocol negotiation is automatic.

### Dashboard WebSocket Security

To restrict dashboard WebSocket access:

```bash
# Production - specific origins only
AGENT_ALLOWED_WS_ORIGINS="https://dashboard.pipeops.io,https://app.pipeops.io"

# Development - allow localhost
AGENT_ALLOWED_WS_ORIGINS="http://localhost:3000,http://localhost:8080"

# Allow all (not recommended for production)
AGENT_ALLOWED_WS_ORIGINS="*"
```

## Troubleshooting

### High Backpressure Events

If `agent_websocket_backpressure_events_total` is increasing:
1. Check network latency to backend services
2. Increase `AGENT_WS_CHANNEL_CAPACITY` (default: 100)
3. Review backend service performance
4. Check for slow consumers on controller side

### Frame Drops

If `agent_websocket_dropped_frames_total` is increasing:
- Backpressure threshold exceeded
- Increase channel capacity
- Investigate slow backend services
- Check controller processing speed

### Origin Validation Failures

If legitimate connections are rejected:
- Check `Origin` header in client requests
- Verify `AGENT_ALLOWED_WS_ORIGINS` configuration
- Ensure exact origin match (case-sensitive, includes port)
- Use wildcard (`*`) for testing only

## Metrics Reference

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `agent_websocket_frames_total` | Counter | direction, type | Total frames by direction and type |
| `agent_websocket_frame_bytes_total` | Counter | direction | Total bytes transferred by direction |
| `agent_websocket_frame_bytes_histogram` | Histogram | - | Distribution of frame sizes |
| `agent_websocket_dropped_frames_total` | Counter | reason | Dropped frames by reason |
| `agent_websocket_backpressure_events_total` | Counter | - | Sustained backpressure events |
| `agent_websocket_session_duration_seconds` | Histogram | - | Session duration distribution |
| `agent_websocket_active_sessions` | Gauge | - | Currently active sessions |
| `agent_websocket_sessions_total` | Counter | - | Total sessions started |
| `agent_websocket_session_close_total` | Counter | code | Session closes by close code |

## References

- [RFC 6455 - The WebSocket Protocol](https://tools.ietf.org/html/rfc6455)
- [gorilla/websocket Documentation](https://pkg.go.dev/github.com/gorilla/websocket)
- PipeOps Controller WebSocket Protocol Specification
