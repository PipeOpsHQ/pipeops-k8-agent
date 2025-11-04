# Gateway Proxy for Private Clusters

## Overview

The gateway proxy enables PipeOps to provide external access to applications running in private or local Kubernetes clusters that don't have public LoadBalancer IPs. This is similar to how KubeSail and other platforms handle private cluster deployments.

## Architecture

```
User Request (custom-domain.com)
    ↓
Gateway HTTP Handler (PipeOps Control Plane)
    ↓ [lookup route table]
    ↓
Tunnel Manager (WebSocket)
    ↓
Agent in Private Cluster
    ↓
Ingress Controller → Service → Pod
```

## How It Works

### 1. Cluster Type Detection

On agent startup, the system detects whether the cluster is private or public:

```go
// Check if ingress-nginx LoadBalancer has external IP
isPrivate, err := gateway.DetectClusterType(ctx, k8sClient, logger)
```

**Detection Logic:**
- ✅ **Private Cluster**: No ingress-nginx service, NodePort type, or LoadBalancer without external IP after 2 minutes
- ✅ **Public Cluster**: LoadBalancer service with external IP (cloud provider assigned)

### 2. Ingress Watching (Private Clusters Only)

If the cluster is private, the agent starts an ingress watcher:

```go
watcher := gateway.NewIngressWatcher(
    k8sClient,
    clusterUUID,
    routeSync, // Agent implements this interface
    logger,
)
watcher.Start(ctx)
```

The watcher:
- Monitors all ingress resources in all namespaces
- Extracts route information (host, path, service, port, TLS)
- Sends route updates to control plane via WebSocket

### 3. Route Synchronization

When ingress resources are created/updated/deleted:

```json
{
  "type": "ingress_routes",
  "payload": {
    "action": "add",
    "cluster_uuid": "abc-123",
    "routes": [
      {
        "host": "example.com",
        "path": "/api",
        "path_type": "Prefix",
        "service": "api-service",
        "namespace": "default",
        "port": 8080,
        "tls": true,
        "annotations": {
          "nginx.ingress.kubernetes.io/rewrite-target": "/"
        }
      }
    ]
  }
}
```

### 4. Heartbeat Integration

The agent includes gateway proxy status in heartbeats:

```json
{
  "metadata": {
    "is_private": true,
    "gateway_routes": 5
  }
}
```

## Control Plane Requirements

The control plane needs to implement:

### 1. Gateway HTTP Handler

```go
func (g *GatewayProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. Lookup route by hostname
    route := g.routeRegistry.Lookup(r.Host, r.URL.Path)
    
    // 2. Check tunnel health
    tunnel := g.tunnelManager.GetAgentConnection(route.ClusterUUID)
    
    // 3. Proxy request through WebSocket tunnel
    resp := g.tunnelManager.SendProxyRequest(tunnel, proxyReq)
    
    // 4. Stream response back to client
    io.Copy(w, resp.Body)
}
```

### 2. Route Registry Service

```go
type RouteEntry struct {
    Host        string
    Path        string
    ClusterUUID string
    ServiceName string
    Namespace   string
    Port        int32
    TLS         bool
    UpdatedAt   time.Time
}

// Redis key: route:{hostname}:{path} -> RouteEntry JSON
// TTL: 5 minutes (agent sends updates via heartbeat)
```

### 3. Message Handler

Handle `ingress_routes` messages from agents:

```go
func (h *MessageHandler) HandleIngressRoutes(msg *Message) {
    action := msg.Payload["action"].(string)
    routes := msg.Payload["routes"].([]Route)
    clusterUUID := msg.Payload["cluster_uuid"].(string)
    
    switch action {
    case "add", "update":
        h.routeRegistry.Register(clusterUUID, routes)
    case "delete":
        h.routeRegistry.Delete(clusterUUID, routes)
    }
}
```

### 4. DNS Configuration

```
*.gateway.pipeops.io -> Gateway HTTP Handler
```

Users can CNAME their custom domains to the gateway:

```
custom-domain.com CNAME abc-123.gateway.pipeops.io
```

## Benefits

### ✅ Zero Agent Cluster Changes
- No modifications to existing infrastructure
- Works with standard Kubernetes ingress resources
- Automatic route discovery

### ✅ Secure
- All traffic goes through authenticated WebSocket tunnel
- No inbound ports needed on private clusters
- Agent initiates all connections

### ✅ Cost Effective
- No need for cloud provider LoadBalancers
- Single gateway endpoint serves all private clusters
- Shared infrastructure cost

### ✅ Transparent
- Applications don't know they're behind a gateway
- Standard HTTP/HTTPS protocol support
- Preserves headers and cookies

## Example: Private Cluster Workflow

1. **Developer creates deployment:**
   ```yaml
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: my-app
   spec:
     replicas: 1
     template:
       spec:
         containers:
         - name: app
           image: my-app:v1
           ports:
           - containerPort: 8080
   ```

2. **Developer creates ingress:**
   ```yaml
   apiVersion: networking.k8s.io/v1
   kind: Ingress
   metadata:
     name: my-app
     namespace: default
   spec:
     rules:
     - host: my-app.example.com
       http:
         paths:
         - path: /
           pathType: Prefix
           backend:
             service:
               name: my-app
               port:
                 number: 8080
   ```

3. **Agent detects ingress and sends route:**
   - Agent watches ingress creation
   - Extracts route: `my-app.example.com -> my-app:8080`
   - Sends route to control plane via WebSocket

4. **Control plane registers route:**
   - Stores route in Redis: `route:my-app.example.com:/ -> {clusterUUID, my-app, 8080}`
   - Returns success to agent

5. **User accesses application:**
   - Browser: `https://my-app.example.com`
   - DNS resolves to gateway IP
   - Gateway looks up route → finds clusterUUID
   - Gateway proxies request through WebSocket tunnel
   - Agent forwards to ingress controller
   - Ingress routes to `my-app` service
   - Response streams back through tunnel to user

## Performance Considerations

### Route Caching
- Redis for persistent storage
- In-memory LRU cache for hot routes
- 5-minute TTL with agent heartbeat refresh

### Connection Pooling
- Reuse WebSocket connections (already implemented)
- Connection per cluster (not per request)

### Timeouts
- Proxy request: 30 seconds max
- Route lookup: <5ms (in-memory cache)
- WebSocket write: 10 seconds

### Load Balancing
- Multiple gateway instances behind load balancer
- Round-robin or least-connections
- Sticky sessions via cookies (optional)

## Monitoring

### Metrics
```
gateway_requests_total{cluster_uuid, status_code}
gateway_request_duration_seconds{cluster_uuid}
gateway_active_routes{cluster_uuid}
gateway_tunnel_health{cluster_uuid}
```

### Logs
```json
{
  "level": "info",
  "msg": "Gateway proxy request",
  "cluster_uuid": "abc-123",
  "host": "my-app.example.com",
  "path": "/api/users",
  "method": "GET",
  "status": 200,
  "duration_ms": 45
}
```

## Testing

Run tests:
```bash
go test ./internal/gateway/... -v
```

Test cases:
- ✅ Route extraction from ingress resources
- ✅ Cluster type detection (private vs public)
- ✅ LoadBalancer with external IP
- ✅ NodePort service (private cluster)
- ✅ No ingress service (private cluster)

## Rollout Strategy

### Phase 1: Beta Testing (Week 1)
- Enable for select private clusters
- Monitor metrics and logs
- Collect feedback

### Phase 2: Gradual Rollout (Week 2-3)
- Roll out to 25% → 50% → 100% of private clusters
- Monitor error rates and latency
- Fix issues as they arise

### Phase 3: Production (Week 4+)
- Full production deployment
- Documentation for users
- Support custom domains

## Future Enhancements

### WebSocket Support
Support WebSocket upgrades for real-time applications:
```go
if websocket.IsWebSocketUpgrade(req) {
    g.upgradeToWebSocket(w, req, tunnel)
}
```

### Rate Limiting
Per-cluster rate limits to prevent abuse:
```go
limiter := rate.NewLimiter(100, 1000) // 100 req/s, burst 1000
if !limiter.Allow() {
    http.Error(w, "Rate limit exceeded", 429)
}
```

### Custom Domains
Allow users to configure custom domains:
```go
// User dashboard:
// my-app.example.com -> CNAME -> abc-123.gateway.pipeops.io
```

### Multi-Path Routing
Support complex routing rules:
```yaml
rules:
  - host: api.example.com
    paths:
    - /v1 -> backend-v1:8080
    - /v2 -> backend-v2:8080
```

## Security Considerations

### Header Injection
```go
req.Header.Set("X-Forwarded-For", clientIP)
req.Header.Set("X-Forwarded-Proto", "https")
req.Header.Set("X-Forwarded-Host", originalHost)
```

### Strip Sensitive Headers
```go
sensitiveHeaders := []string{
    "Authorization",
    "Cookie",
    "X-Auth-Token",
}
for _, h := range sensitiveHeaders {
    req.Header.Del(h)
}
```

### TLS Termination
- Let's Encrypt certificates at gateway level
- Automatic renewal via cert-manager
- Support custom certificates

## Troubleshooting

### Route not found (404)
- Check ingress exists: `kubectl get ingress -A`
- Check agent logs: `kubectl logs -n pipeops-system pipeops-agent`
- Verify route in control plane: `redis-cli GET route:example.com:/`

### Tunnel unreachable (503)
- Check agent connection: Control plane admin panel
- Check WebSocket health: Agent heartbeat logs
- Restart agent: `kubectl rollout restart deployment/pipeops-agent`

### Slow response times
- Check tunnel latency: `ping gateway.pipeops.io`
- Check agent resource usage: `kubectl top pod -n pipeops-system`
- Check backend service: `kubectl logs <pod-name>`
