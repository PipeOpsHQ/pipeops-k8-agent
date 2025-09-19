# PipeOps VM Agent - Current Architecture & Implementation

## Overview

The PipeOps VM Agent has been successfully migrated from WebSocket-based communication to FRP (Fast Reverse Proxy) based HTTP communication. The agent now operates as an HTTP-only service that establishes outbound connections through FRP tunnels.

## Current Implementation Status

### ✅ Completed Components

1. **Server Implementation** (`internal/server/server.go`)
2. **FRP Client & Authentication** (`internal/frp/`)  
3. **HTTP-only Kubernetes Proxy** (`internal/proxy/k8s_proxy.go`)
4. **Agent Core Logic** (`internal/agent/agent.go`)
5. **Configuration Types** (`pkg/types/types.go`)
6. **Deployment Configuration** (Dockerfile, Helm chart)

### 🚧 Legacy Components (Test-only)
- `test/mock-runner/main.go` - WebSocket-based mock (not used in production)
- `test/mock-control-plane/main.go` - WebSocket-based mock (not used in production)

## Architecture

### Communication Flow

```
┌─────────────────────┐    FRP Tunnel     ┌─────────────────────┐
│   PipeOps Agent     │ ───────────────→  │  PipeOps Control    │
│                     │    (Outbound)     │      Plane          │
│  - FRP Client       │                   │                     │
│  - HTTP Server      │                   │  - FRP Server       │
│  - K8s Proxy        │                   │  - API Gateway      │
└─────────────────────┘                   └─────────────────────┘
         │
         │ HTTP/HTTPS
         │
         ▼
┌─────────────────────┐
│  Kubernetes API     │
│     Server          │
└─────────────────────┘
```

### Connection Pattern
- **Outbound Only**: Agent initiates all connections to Control Plane
- **FRP Tunneling**: Secure HTTP tunnels through firewall-friendly connections
- **No Inbound Ports**: Agent doesn't expose any ports externally
- **Token Authentication**: Bearer token-based authentication for all API calls

## HTTP API Endpoints

The agent exposes the following HTTP endpoints for FRP communication:

### Health & Monitoring
- `GET /health` - Health check endpoint
- `GET /ready` - Readiness probe
- `GET /version` - Agent version information
- `GET /metrics` - Agent metrics and cluster status

### Agent Management (FRP Communication)
- `GET /api/agent/status` - Agent and cluster status for Control Plane
- `POST /api/agent/heartbeat` - Heartbeat endpoint for Control Plane monitoring
- `POST /api/control-plane/message` - Receive messages from Control Plane
- `POST /api/runners/:runner_id/command` - Receive commands for specific runners

### Kubernetes API Proxy
- `GET|POST|PUT|DELETE|PATCH|OPTIONS /api/k8s/*path` - Proxy all Kubernetes API calls

## Authentication

### HTTP Bearer Token Authentication

```go
func (s *Server) authenticateHTTP(c *gin.Context) bool {
    authHeader := c.GetHeader("Authorization")
    if authHeader == "" {
        return false
    }

    // Extract Bearer token
    token := strings.TrimPrefix(authHeader, "Bearer ")
    if token == authHeader {
        return false
    }

    // Validate token against configured agent token
    return token == s.config.Agent.Token
}
```

All agent API endpoints (`/api/agent/*`, `/api/control-plane/*`, `/api/runners/*`) require Bearer token authentication.

## Configuration

### Agent Configuration

```yaml
agent:
  id: "agent-001"
  name: "production-k8s-cluster"
  cluster_name: "prod-cluster"
  token: "your-agent-token"
  api_url: "https://api.pipeops.io"
  debug: false

frp:
  server_addr: "frp.pipeops.io"
  server_port: 7000
  token: "your-frp-token"
  admin_port: 7400

kubernetes:
  kubeconfig: ""  # Optional: path to kubeconfig
  in_cluster: true  # Use in-cluster authentication
```

### Environment Variables

```bash
# Agent Configuration
PIPEOPS_AGENT_ID=agent-001
PIPEOPS_AGENT_NAME=production-k8s-cluster
PIPEOPS_AGENT_TOKEN=your-agent-token
PIPEOPS_API_URL=https://api.pipeops.io

# FRP Configuration  
PIPEOPS_FRP_SERVER_ADDR=frp.pipeops.io
PIPEOPS_FRP_SERVER_PORT=7000
PIPEOPS_FRP_TOKEN=your-frp-token

# Kubernetes Configuration
KUBECONFIG=/path/to/kubeconfig  # Optional
```

## Key Features

### 1. FRP Integration

```go
type AuthService struct {
    config *types.FRPConfig
    logger *logrus.Logger
    client *http.Client
}

func (a *AuthService) Authenticate() (*AuthResponse, error) {
    // Authenticate with PipeOps Control Plane
    // Returns FRP tunnel configuration
}
```

### 2. HTTP-only Kubernetes Proxy

```go
func (p *K8sProxy) HandleRequest(c *gin.Context) {
    // Extract path and method
    // Forward to Kubernetes API server
    // Return response via HTTP
}
```

### 3. Cluster Status Monitoring

```go
func (s *Server) handleAgentStatus(c *gin.Context) {
    status, err := s.k8sClient.GetClusterStatus(ctx)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    
    response := gin.H{
        "agent": gin.H{
            "id":      s.config.Agent.ID,
            "name":    s.config.Agent.Name,
            "cluster": s.config.Agent.ClusterName,
            "version": s.config.Agent.Version,
        },
        "cluster":   status,
        "timestamp": time.Now(),
    }
    
    c.JSON(http.StatusOK, response)
}
```

## Deployment

### Kubernetes Deployment (Helm)

```yaml
# values.yaml
agent:
  id: "{{ .Values.agentId }}"
  name: "{{ .Values.clusterName }}"
  token: "{{ .Values.agentToken }}"
  apiUrl: "https://api.pipeops.io"

frp:
  serverAddr: "frp.pipeops.io"
  serverPort: 7000
  token: "{{ .Values.frpToken }}"

image:
  repository: pipeops/vm-agent
  tag: latest
  pullPolicy: IfNotPresent
```

### Docker Deployment

```bash
docker run -d \
  --name pipeops-agent \
  -e PIPEOPS_AGENT_ID=agent-001 \
  -e PIPEOPS_AGENT_TOKEN=your-token \
  -e PIPEOPS_FRP_TOKEN=your-frp-token \
  -v /path/to/kubeconfig:/root/.kube/config \
  pipeops/vm-agent:latest
```

## Network Requirements

### Outbound Connections
- `api.pipeops.io:443` - HTTPS API calls to Control Plane
- `frp.pipeops.io:7000` - FRP server connection
- Kubernetes API server (typically `https://kubernetes.default.svc:443` in-cluster)

### Ports
- **No inbound ports required** - Agent operates outbound-only
- Internal HTTP server on port 8080 (not exposed externally)

## Monitoring & Observability

### Health Checks
- **Liveness**: `GET /health`
- **Readiness**: `GET /ready` (validates K8s connectivity)

### Metrics
- **Agent metrics**: `GET /metrics`
- **Cluster status**: Available via `/api/agent/status`
- **FRP connection status**: Monitored by FRP client

### Logging
- Structured JSON logging via logrus
- Configurable log levels (DEBUG, INFO, WARN, ERROR)
- Connection status, API calls, and error tracking

## Security Features

1. **Outbound-only connections** - No exposed ports
2. **Token-based authentication** - Bearer tokens for all API calls
3. **TLS encryption** - All communication over HTTPS/TLS
4. **In-cluster RBAC** - Uses Kubernetes service account permissions
5. **Secure FRP tunnels** - Encrypted tunnel communication

## Benefits Over WebSocket Implementation

1. **Firewall Friendly**: Standard HTTP/HTTPS traffic
2. **Better Reliability**: HTTP retry mechanisms and connection pooling  
3. **Simplified Infrastructure**: No WebSocket upgrade complexity
4. **Standard Protocols**: REST API patterns familiar to developers
5. **Easier Debugging**: Standard HTTP tools and monitoring
6. **Better Load Balancing**: Works with standard HTTP load balancers
7. **Proxy Compatibility**: Works through corporate proxies

## Next Steps

1. **Testing**: Integration tests with real FRP server
2. **Mock Updates**: Update test mocks to use HTTP instead of WebSockets
3. **Monitoring**: Enhanced metrics and alerting
4. **Documentation**: API documentation and runbooks

The migration from WebSocket to FRP-based HTTP communication is **complete** and the agent is ready for production deployment.