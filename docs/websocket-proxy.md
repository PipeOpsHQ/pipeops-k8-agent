# WebSocket Proxy Support

The PipeOps agent now supports WebSocket proxying for interactive Kubernetes commands like `kubectl exec`, `kubectl attach`, and `kubectl port-forward` through the control plane tunnel.

## Architecture

```
User (kubectl) → Control Plane → Agent → Kubernetes API
                  WebSocket       WebSocket
```

The agent establishes bidirectional WebSocket connections to the Kubernetes API and relays data to/from the control plane.

## Message Types

### `proxy_websocket_start`

Initiates a WebSocket proxy session.

**Request from Controller:**
```json
{
  "type": "proxy_websocket_start",
  "request_id": "stream-uuid",
  "timestamp": "2025-11-02T22:00:00Z",
  "payload": {
    "stream_id": "stream-uuid",
    "cluster_uuid": "cluster-uuid",
    "method": "GET",
    "path": "/api/v1/namespaces/default/pods/my-pod/exec",
    "query": "command=/bin/bash&stdin=true&stdout=true&tty=true",
    "headers": {
      "Authorization": ["Bearer token"],
      "Upgrade": ["SPDY/3.1"],
      "Connection": ["Upgrade"]
    },
    "protocol": "v4.channel.k8s.io"
  }
}
```

**Agent Actions:**
1. Creates a WebSocket stream context
2. Establishes WebSocket connection to Kubernetes API
3. Starts bidirectional relay goroutines
4. Sends errors if connection fails

### `proxy_websocket_data`

Relays data from client to Kubernetes.

**Request from Controller:**
```json
{
  "type": "proxy_websocket_data",
  "request_id": "stream-uuid",
  "timestamp": "2025-11-02T22:00:01Z",
  "payload": {
    "stream_id": "stream-uuid",
    "data": "<base64 encoded: [messageType][data]>"
  }
}
```

**Data Format:**
- First byte: WebSocket message type (1=Text, 2=Binary, etc.)
- Remaining bytes: Actual message data

**Agent Actions:**
1. Decodes base64 data
2. Extracts message type and data
3. Writes to Kubernetes WebSocket connection

### `proxy_websocket_close`

Closes a WebSocket stream.

**Request from Controller:**
```json
{
  "type": "proxy_websocket_close",
  "request_id": "stream-uuid",
  "timestamp": "2025-11-02T22:00:05Z",
  "payload": {
    "stream_id": "stream-uuid"
  }
}
```

**Agent Actions:**
1. Cancels stream context
2. Closes Kubernetes WebSocket connection
3. Cleans up resources
4. Sends close acknowledgment

## Outgoing Messages

### Data from Kubernetes

```json
{
  "type": "proxy_websocket_data",
  "request_id": "stream-uuid",
  "timestamp": "2025-11-02T22:00:02Z",
  "payload": {
    "stream_id": "stream-uuid",
    "data": "<base64 encoded: [messageType][data]>"
  }
}
```

### Stream Closed

```json
{
  "type": "proxy_websocket_close",
  "request_id": "stream-uuid",
  "timestamp": "2025-11-02T22:00:06Z",
  "payload": {
    "stream_id": "stream-uuid"
  }
}
```

### Error Occurred

```json
{
  "type": "proxy_error",
  "request_id": "stream-uuid",
  "timestamp": "2025-11-02T22:00:03Z",
  "payload": {
    "request_id": "stream-uuid",
    "error": "failed to connect to K8s API: connection refused"
  }
}
```

## Implementation Details

### Stream Management

Each WebSocket stream maintains:
- **Stream ID**: Unique identifier for the session
- **WebSocket Connection**: Connection to Kubernetes API
- **Data Channel**: Buffered channel for incoming data from controller
- **Close Channel**: Signal channel for shutdown
- **Context**: Cancellable context for goroutine coordination
- **Logger**: Contextual logger with stream_id field

### Bidirectional Relay

Two goroutines handle data flow:

1. **Client → Kubernetes** (`relayClientToK8s`)
   - Reads from data channel
   - Writes to Kubernetes WebSocket
   - Cancels on write errors

2. **Kubernetes → Client** (`relayK8sToClient`)
   - Reads from Kubernetes WebSocket
   - Sends to controller via control plane WebSocket
   - Cancels on read errors

### Cleanup

Stream cleanup occurs on:
- Normal close from controller
- Kubernetes connection close
- Write/read errors
- Agent shutdown

Cleanup steps:
1. Cancel context (stops goroutines)
2. Close data and close channels
3. Close Kubernetes WebSocket
4. Remove from streams map
5. Send close notification to controller

## Usage Examples

### kubectl exec

```bash
kubectl exec -it my-pod -n default -- /bin/bash
```

**Flow:**
1. kubectl sends WebSocket upgrade request to control plane
2. Control plane sends `proxy_websocket_start` to agent
3. Agent connects to K8s API `/api/v1/namespaces/default/pods/my-pod/exec`
4. Bidirectional data relay begins
5. User types commands, sees output in real-time
6. On exit, stream is closed cleanly

### kubectl attach

```bash
kubectl attach my-pod -n default -it
```

Similar flow to exec but attaches to running container.

### kubectl port-forward

```bash
kubectl port-forward my-pod 8080:80 -n default
```

**Flow:**
1. kubectl establishes port forward via WebSocket
2. Agent relays TCP data over WebSocket to K8s API
3. User can connect to `localhost:8080`
4. Traffic proxied through control plane → agent → pod

## Monitoring

### Logs

**Stream Start:**
```
INFO WebSocket proxy start requested stream_id=uuid path=/api/v1/...
INFO Connected to Kubernetes API stream_id=uuid
```

**Data Transfer:**
```
DEBUG Relaying data to Kubernetes stream_id=uuid size=1024
DEBUG Received data from Kubernetes stream_id=uuid size=512
```

**Stream End:**
```
INFO WebSocket stream closed stream_id=uuid duration=2m15s
```

**Errors:**
```
ERROR Failed to connect to Kubernetes API stream_id=uuid error="connection refused"
WARN Kubernetes WebSocket closed unexpectedly stream_id=uuid
```

### Metrics

Track these metrics (if metrics are implemented):
- Active WebSocket streams count
- Stream duration histogram
- Bytes transferred per stream
- Connection errors rate
- Stream creation rate

## Troubleshooting

### Connection Refused

**Symptom:** `failed to connect to K8s API: connection refused`

**Causes:**
- Kubernetes API server is down
- Network policy blocking access
- Invalid service account token

**Solution:**
- Check agent logs for TLS/auth errors
- Verify agent can reach `kubernetes.default.svc`
- Check RBAC permissions

### Stream Not Found

**Symptom:** `Received data for unknown stream`

**Causes:**
- Stream was closed before data arrived
- Controller sent wrong stream_id
- Network delay causing race condition

**Solution:**
- Check for premature stream closure
- Verify controller stream management
- Review timing in logs

### Data Channel Full

**Symptom:** `Data channel full, dropping message`

**Causes:**
- Kubernetes connection slow or stuck
- Large burst of data from controller
- Relay goroutine died

**Solution:**
- Increase data channel buffer size (currently 100)
- Check Kubernetes connection health
- Review goroutine status

## Security Considerations

1. **Authentication:** Agent uses ServiceAccount token to authenticate with K8s API
2. **Authorization:** RBAC policies control what exec/attach/port-forward operations are allowed
3. **TLS:** All WebSocket connections use TLS
4. **Isolation:** Each stream has isolated context and channels
5. **Resource Limits:** Streams are cleaned up on errors/timeouts

## Future Enhancements

- [ ] Stream timeout configuration
- [ ] Rate limiting per stream
- [ ] Bandwidth throttling
- [ ] Stream metrics and monitoring
- [ ] Support for other protocols (SPDY, WebSocket subprotocols)
- [ ] Connection pooling for multiple streams to same pod
