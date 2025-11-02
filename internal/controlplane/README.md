# Control Plane Communication Package

This package handles all communication between the PipeOps agent and the control plane.

## Components

### WebSocket Client (`websocket_client.go`)

Main WebSocket client that:
- Establishes and maintains WebSocket connection to control plane
- Handles agent registration and heartbeats
- Routes incoming messages to appropriate handlers
- Manages reconnection logic with exponential backoff
- Supports proxy requests (HTTP and WebSocket)

### WebSocket Proxy Manager (`websocket_proxy.go`)

Manages WebSocket proxy streams for interactive Kubernetes commands:
- Handles `kubectl exec`, `kubectl attach`, and `kubectl port-forward`
- Establishes WebSocket connections to Kubernetes API
- Relays data bidirectionally between controller and Kubernetes
- Manages stream lifecycle and cleanup

#### Key Features

- **Stream Management**: Tracks active WebSocket streams with unique IDs
- **Bidirectional Relay**: Two goroutines per stream for concurrent data flow
- **Clean Shutdown**: Proper cleanup on close, errors, or timeout
- **Error Handling**: Reports connection and relay errors to controller
- **Resource Safety**: Context-based cancellation and mutex-protected maps

### Types (`types.go`)

Common data structures:
- `RegistrationResult`: Agent registration response
- `HeartbeatRequest`: Health check and monitoring data
- `ProxyRequest`: HTTP proxy request from controller
- `ProxyResponse`: HTTP proxy response to controller
- `ProxyError`: Proxy operation error

## Message Protocol

### Agent → Controller

#### Registration
```json
{
  "type": "register",
  "request_id": "uuid",
  "payload": {
    "agent_id": "agent-xxx",
    "name": "cluster-name",
    "k8s_version": "1.28.0",
    "region": "us-east-1"
  }
}
```

#### Heartbeat
```json
{
  "type": "heartbeat",
  "payload": {
    "cluster_id": "uuid",
    "agent_id": "agent-xxx",
    "status": "healthy",
    "tunnel_status": "connected"
  }
}
```

#### WebSocket Data (from Kubernetes)
```json
{
  "type": "proxy_websocket_data",
  "request_id": "stream-uuid",
  "payload": {
    "stream_id": "stream-uuid",
    "data": "base64-encoded-data"
  }
}
```

#### WebSocket Close
```json
{
  "type": "proxy_websocket_close",
  "request_id": "stream-uuid",
  "payload": {
    "stream_id": "stream-uuid"
  }
}
```

### Controller → Agent

#### Registration Success
```json
{
  "type": "register_success",
  "request_id": "uuid",
  "payload": {
    "cluster_id": "uuid",
    "name": "cluster-name",
    "workspace_id": 123
  }
}
```

#### HTTP Proxy Request
```json
{
  "type": "proxy_request",
  "request_id": "uuid",
  "payload": {
    "method": "GET",
    "path": "/api/v1/pods",
    "headers": {...}
  }
}
```

#### WebSocket Proxy Start
```json
{
  "type": "proxy_websocket_start",
  "request_id": "stream-uuid",
  "payload": {
    "stream_id": "stream-uuid",
    "method": "GET",
    "path": "/api/v1/namespaces/default/pods/my-pod/exec",
    "query": "command=/bin/bash&stdin=true",
    "protocol": "v4.channel.k8s.io"
  }
}
```

#### WebSocket Data (to Kubernetes)
```json
{
  "type": "proxy_websocket_data",
  "request_id": "stream-uuid",
  "payload": {
    "stream_id": "stream-uuid",
    "data": "base64-encoded-data"
  }
}
```

#### WebSocket Close Request
```json
{
  "type": "proxy_websocket_close",
  "request_id": "stream-uuid",
  "payload": {
    "stream_id": "stream-uuid"
  }
}
```

## WebSocket Proxy Flow

### kubectl exec Example

```
┌─────────┐      ┌────────────┐      ┌───────┐      ┌─────────────┐
│ kubectl │─────▶│ Controller │─────▶│ Agent │─────▶│ K8s API     │
└─────────┘      └────────────┘      └───────┘      └─────────────┘
     │                  │                  │                │
     │  WebSocket       │                  │                │
     │  Upgrade         │                  │                │
     │─────────────────▶│                  │                │
     │                  │ proxy_websocket_ │                │
     │                  │ start            │                │
     │                  │─────────────────▶│                │
     │                  │                  │  WebSocket     │
     │                  │                  │  Connect       │
     │                  │                  │───────────────▶│
     │                  │                  │                │
     │                  │                  │  ◀─Connected─▶ │
     │                  │                  │                │
     │  Type command    │ proxy_websocket_ │                │
     │─────────────────▶│ data             │                │
     │                  │─────────────────▶│  Write data    │
     │                  │                  │───────────────▶│
     │                  │                  │                │
     │                  │                  │  Read response │
     │                  │ proxy_websocket_ │◀───────────────│
     │  Show output     │ data             │                │
     │◀─────────────────│◀─────────────────│                │
     │                  │                  │                │
```

## Usage

### Initialize Client

```go
import "github.com/pipeops/pipeops-vm-agent/internal/controlplane"

client, err := controlplane.NewWebSocketClient(
    "https://api.pipeops.io",
    "agent-token",
    "agent-id",
    tlsConfig,
    logger,
)
if err != nil {
    log.Fatal(err)
}

// Set proxy handler for HTTP requests
client.SetStreamingProxyHandler(func(req *controlplane.ProxyRequest, w controlplane.ProxyResponseWriter) {
    // Handle proxy request
})

// Connect to control plane
if err := client.Connect(); err != nil {
    log.Fatal(err)
}
```

### Register Agent

```go
agent := &types.Agent{
    ID:          "agent-xxx",
    Name:        "my-cluster",
    Version:     "1.28.0",
    Hostname:    "node-1",
    CloudProvider: "aws",
    Region:      "us-east-1",
}

result, err := client.RegisterAgent(ctx, agent)
if err != nil {
    log.Fatal(err)
}

log.Printf("Registered: cluster_id=%s", result.ClusterID)
```

### Send Heartbeat

```go
heartbeat := &controlplane.HeartbeatRequest{
    ClusterID:    clusterID,
    AgentID:      agentID,
    Status:       "healthy",
    TunnelStatus: "connected",
}

if err := client.SendHeartbeat(ctx, heartbeat); err != nil {
    log.Error(err)
}
```

## Testing

Run tests:
```bash
go test -v ./internal/controlplane/...
```

Run with race detection:
```bash
go test -v -race ./internal/controlplane/...
```

## Architecture Notes

### Concurrency

- **WebSocket Connection**: Protected by `connMutex` (RWMutex)
- **WebSocket Writes**: Protected by `writeMutex` (Mutex)
- **Streams Map**: Protected by `streamsMu` (RWMutex)
- **Goroutines**: Each stream has two relay goroutines
- **Cancellation**: Context-based cancellation for clean shutdown

### Error Handling

- **Connection Errors**: Trigger reconnection with exponential backoff
- **Stream Errors**: Cancel stream and notify controller
- **Parse Errors**: Log and drop malformed messages
- **Timeout Errors**: Clean up resources and close streams

### Resource Management

- **Channels**: Buffered channels (size 100) to prevent blocking
- **Contexts**: Cancellable contexts for each stream
- **Cleanup**: Deferred cleanup in stream handlers
- **Goroutine Tracking**: WaitGroup tracks goroutine lifecycle

## Performance

### Optimizations

- Buffered channels reduce blocking
- RWMutex allows concurrent reads
- Base64 encoding done lazily
- Connection pooling for multiple streams (future)

### Limits

- Data channel buffer: 100 messages
- WebSocket write timeout: 30 seconds
- Reconnect max delay: 60 seconds
- Heartbeat interval: 30 seconds (configurable)

## Security

- TLS required for all connections
- Bearer token authentication
- ServiceAccount token for Kubernetes API
- Stream isolation via contexts
- No credential logging
