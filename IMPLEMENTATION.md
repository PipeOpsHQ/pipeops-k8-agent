# PipeOps VM Agent - Complete Implementation

This document provides a comprehensive overview of the PipeOps VM Agent implementation, including the Kubernetes API proxy functionality that allows centralized business logic in the Runner.

## Architecture Overview

The VM Agent serves as a secure bridge between PipeOps Control Plane/Runner and Kubernetes clusters, implementing the "Capsule Proxy" pattern over WebSocket connections.

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         PipeOps Control Plane                          │
│  ┌─────────────────┐                           ┌─────────────────┐      │
│  │     Runner      │                           │   Controller    │      │
│  │                 │                           │                 │      │
│  │  - Deployments  │                           │  - Reads K8s    │      │
│  │  - Scaling      │                           │  - Cluster Info │      │
│  │  - Jobs         │                           │  - Monitoring   │      │
│  │  - Secrets      │                           │                 │      │
│  └─────────────────┘                           └─────────────────┘      │
│           │                                             │                │
│           │ HTTP/WebSocket                             │                │
│           │ (K8s API Proxy)                           │                │
└───────────┼─────────────────────────────────────────────┼────────────────┘
            │                                             │
            │                                             │
  ┌─────────▼─────────────────────────────────────────────▼────────────────┐
  │                         VM Agent                                       │
  │  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐     │
  │  │  HTTP Server    │    │  K8s API Proxy  │    │ Communication   │     │
  │  │                 │    │                 │    │     Client      │     │
  │  │  - Health       │    │  - WebSocket    │    │                 │     │
  │  │  - Metrics      │    │  - Streaming    │    │  - Control      │     │
  │  │  - WebSocket    │    │  - Auth         │    │    Plane Conn   │     │
  │  │    Endpoints    │    │  - Full K8s API │    │  - Heartbeat    │     │
  │  └─────────────────┘    └─────────────────┘    └─────────────────┘     │
  │           │                       │                       │            │
  │           └───────────────────────┼───────────────────────┘            │
  │                                   │                                    │
  └───────────────────────────────────┼────────────────────────────────────┘
                                      │
                                      │ Kubernetes API
                                      │
                ┌─────────────────────▼─────────────────────┐
                │              Kubernetes Cluster          │
                │                   (k3s)                  │
                │                                          │
                │  - Pods, Deployments, Services           │
                │  - ConfigMaps, Secrets                   │
                │  - Ingress, NetworkPolicies              │
                │  - Custom Resources                      │
                └──────────────────────────────────────────┘
```

## Key Components

### 1. VM Agent Server (`internal/server/server.go`)
- **HTTP Server**: Gin-based web server on port 8080
- **Health Endpoints**: `/health`, `/ready`, `/metrics`
- **WebSocket Endpoints**: 
  - `/ws/control-plane` - Control Plane connection
  - `/ws/runner` - Runner connections  
  - `/ws/k8s` - Kubernetes API proxy
- **Graceful Shutdown**: Proper cleanup of connections

### 2. Kubernetes API Proxy (`internal/proxy/k8s_proxy.go`)
- **Full K8s API Support**: GET, POST, PUT, DELETE, PATCH operations
- **Streaming Support**: WATCH operations for real-time monitoring
- **WebSocket Communication**: Efficient, persistent connections
- **Authentication**: Forwards K8s bearer tokens and TLS config
- **Stream Management**: Handles multiple concurrent WATCH streams

### 3. Communication Client (`internal/communication/client.go`)
- **Control Plane Connection**: WebSocket to PipeOps API
- **Message Handling**: Type-based message routing
- **Reconnection Logic**: Automatic reconnection with exponential backoff
- **Heartbeat**: Periodic status reporting

### 4. Kubernetes Client (`internal/k8s/client.go`)
- **Cluster Operations**: Deployments, scaling, pod management
- **Status Monitoring**: Node, pod, service status collection
- **Log Retrieval**: Pod log streaming
- **Resource Management**: Full CRUD operations

## API Proxy Implementation Details

### Request Format
```json
{
  "id": "unique-request-id",
  "method": "GET|POST|PUT|DELETE|PATCH",
  "path": "/api/v1/namespaces/default/pods",
  "headers": {
    "Accept": "application/json",
    "Content-Type": "application/json"
  },
  "body": "base64-encoded-request-body",
  "query": {
    "labelSelector": ["app=nginx"],
    "watch": ["true"]
  },
  "stream": true,
  "stream_id": "optional-for-stream-control"
}
```

### Response Format
```json
{
  "id": "matching-request-id",
  "status_code": 200,
  "headers": {
    "Content-Type": "application/json"
  },
  "body": "base64-encoded-response-body",
  "error": "optional-error-message",
  "stream": true,
  "stream_id": "stream-identifier",
  "chunk": true,
  "done": false
}
```

### Streaming Support
For WATCH operations:
1. Initial request with `stream: true`
2. Server responds with `stream_id`
3. Continuous chunks with `chunk: true`
4. End marker with `done: true`
5. Client can cancel with `CANCEL` method

## Runner Integration Examples

### Basic Pod Listing
```go
request := &types.K8sProxyRequest{
    ID:     "list-pods-123",
    Method: "GET", 
    Path:   "/api/v1/namespaces/default/pods",
    Headers: map[string]string{
        "Accept": "application/json",
    },
}

// Send via WebSocket to /ws/k8s
conn.WriteJSON(request)

// Read response
var response types.K8sProxyResponse
conn.ReadJSON(&response)

// Parse Kubernetes pod list from response.Body
```

### Deployment Creation
```go
deploymentYAML := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:latest
`

request := &types.K8sProxyRequest{
    ID:     "create-deploy-456",
    Method: "POST",
    Path:   "/apis/apps/v1/namespaces/default/deployments",
    Headers: map[string]string{
        "Content-Type": "application/yaml",
    },
    Body: []byte(deploymentYAML),
}
```

### Pod WATCH Stream
```go
request := &types.K8sProxyRequest{
    ID:     "watch-pods-789",
    Method: "GET",
    Path:   "/api/v1/namespaces/default/pods",
    Query: map[string][]string{
        "watch": {"true"},
    },
    Stream: true,
}

// Handle streaming events
for {
    var response types.K8sProxyResponse
    conn.ReadJSON(&response)
    
    if response.Done {
        break
    }
    
    if response.Chunk {
        var event map[string]interface{}
        json.Unmarshal(response.Body, &event)
        
        eventType := event["type"].(string)  // ADDED, MODIFIED, DELETED
        object := event["object"]            // Pod object
        
        // Process event
    }
}
```

## Security Implementation

### Authentication Flow
1. **Agent Registration**: Agent authenticates with Control Plane using token
2. **Runner Assignment**: Control Plane provides Runner endpoint and token
3. **K8s API Access**: Agent uses in-cluster or kubeconfig credentials
4. **Proxy Authentication**: All K8s API calls include proper bearer tokens

### Network Security
- **Outbound Only**: Agent only makes outbound connections
- **TLS Encryption**: All connections use TLS/WSS
- **Firewall Friendly**: No inbound ports required on VM
- **Token-based Auth**: No long-lived credentials stored

### RBAC Configuration
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pipeops-agent
rules:
- apiGroups: ["*"]
  resources: ["*"]
  verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pipeops-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: pipeops-agent
subjects:
- kind: ServiceAccount
  name: pipeops-agent
  namespace: pipeops-system
```

## Configuration Options

### Complete Configuration Example
```yaml
agent:
  id: "prod-k3s-agent-01"
  name: "Production K3s Agent"
  cluster_name: "production-k3s"
  poll_interval: "30s"
  port: 8080
  debug: false
  labels:
    environment: "production"
    region: "us-west-2"

pipeops:
  api_url: "https://api.pipeops.io"
  token: "your-secure-token"
  timeout: "30s"
  tls:
    enabled: true
    insecure_skip_verify: false
  reconnect:
    enabled: true
    max_attempts: 10
    interval: "5s"

kubernetes:
  kubeconfig: "/home/user/.kube/config"
  in_cluster: false
  namespace: "pipeops-system"

logging:
  level: "info"
  format: "json"
  output: "stdout"
```

## Deployment Methods

### 1. Standalone Binary
```bash
# Download and configure
wget https://releases.pipeops.io/agent/v1.0.0/pipeops-agent-linux-amd64
chmod +x pipeops-agent-linux-amd64
mv pipeops-agent-linux-amd64 /usr/local/bin/pipeops-agent

# Create config
cat > /etc/pipeops/config.yaml << EOF
agent:
  cluster_name: "my-cluster"
pipeops:
  api_url: "https://api.pipeops.io"
  token: "your-token"
EOF

# Run
pipeops-agent --config /etc/pipeops/config.yaml
```

### 2. Docker Container  
```bash
docker run -d \\
  --name pipeops-agent \\
  --restart unless-stopped \\
  -v ~/.kube/config:/etc/kubeconfig \\
  -e PIPEOPS_PIPEOPS_API_URL="https://api.pipeops.io" \\
  -e PIPEOPS_PIPEOPS_TOKEN="your-token" \\
  -e PIPEOPS_AGENT_CLUSTER_NAME="my-cluster" \\
  -e PIPEOPS_KUBERNETES_KUBECONFIG="/etc/kubeconfig" \\
  -p 8080:8080 \\
  pipeops/agent:latest
```

### 3. Kubernetes Deployment
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pipeops-agent
  namespace: pipeops-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: pipeops-agent
  template:
    metadata:
      labels:
        app: pipeops-agent
    spec:
      serviceAccountName: pipeops-agent
      containers:
      - name: agent
        image: pipeops/agent:latest
        env:
        - name: PIPEOPS_PIPEOPS_API_URL
          value: "https://api.pipeops.io"
        - name: PIPEOPS_PIPEOPS_TOKEN
          valueFrom:
            secretKeyRef:
              name: pipeops-token
              key: token
        - name: PIPEOPS_AGENT_CLUSTER_NAME
          value: "production-k3s"
        - name: PIPEOPS_KUBERNETES_IN_CLUSTER
          value: "true"
        ports:
        - containerPort: 8080
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
```

## Monitoring and Observability

### Health Checks
- **`/health`**: Basic health status
- **`/ready`**: Readiness for traffic
- **`/metrics`**: Cluster and agent metrics

### Metrics Collection
```json
{
  "cluster": {
    "nodes": 3,
    "pods": 45,
    "deployments": 12,
    "services": 8
  },
  "connections": {
    "control_plane": true,
    "runners": 2
  },
  "proxy": {
    "active_streams": 5,
    "requests_per_minute": 120
  }
}
```

### Logging
- **Structured JSON**: Machine-readable logs
- **Request Tracing**: Full K8s API request/response logging
- **Connection Events**: WebSocket connection status
- **Error Details**: Detailed error messages and stack traces

## Performance Considerations

### Scalability
- **Single Agent per Cluster**: One agent handles all Runner connections
- **Stream Multiplexing**: Multiple WATCH streams over single WebSocket
- **Connection Pooling**: Efficient K8s API client connection reuse
- **Memory Management**: Automatic cleanup of completed streams

### Optimization
- **Compression**: WebSocket message compression enabled
- **Buffering**: Efficient I/O buffering for large responses
- **Timeouts**: Configurable timeouts for all operations
- **Rate Limiting**: Built-in protection against request flooding

## Troubleshooting Guide

### Common Issues

1. **Agent Won't Connect**
   ```bash
   # Check network connectivity
   curl -v https://api.pipeops.io
   
   # Verify token
   curl -H "Authorization: Bearer $TOKEN" https://api.pipeops.io/health
   ```

2. **K8s API Errors**
   ```bash
   # Test kubectl access
   kubectl cluster-info
   
   # Check RBAC permissions
   kubectl auth can-i "*" "*" --as=system:serviceaccount:pipeops-system:pipeops-agent
   ```

3. **WebSocket Issues**
   ```bash
   # Test WebSocket endpoint
   wscat -c ws://localhost:8080/ws/k8s
   
   # Check firewall rules
   netstat -tlnp | grep 8080
   ```

### Debug Mode
```bash
# Enable debug logging
pipeops-agent --log-level debug

# Output shows:
# - WebSocket handshake details
# - K8s API request/response bodies
# - Stream lifecycle events
# - Connection retry attempts
```

## Future Enhancements

### Planned Features
1. **gRPC Support**: Alternative to WebSocket for high-performance scenarios
2. **Multi-tenancy**: Support for multiple Runner connections per agent
3. **Caching**: Intelligent caching of K8s API responses
4. **Compression**: Advanced compression for large payloads
5. **Metrics Export**: Prometheus metrics endpoint

### Integration Roadmap
1. **Helm Charts**: Easy Kubernetes deployment
2. **Operator**: Kubernetes operator for agent lifecycle management
3. **CLI Tools**: Enhanced debugging and management tools
4. **Dashboard**: Web UI for agent monitoring

This implementation provides a complete, production-ready solution for secure Kubernetes cluster management through PipeOps, maintaining centralized business logic while ensuring security and performance.
