# PipeOps VM Agent Architecture

The PipeOps VM Agent is a Kubernetes-native agent featuring a **Portainer-style Chisel tunnel architecture** with multi-port forwarding capabilities for secure, direct access to cluster resources.

## Architecture Overview

The agent operates with a simplified, pure TCP tunneling approach inspired by Portainer, featuring:
- **Single Chisel tunnel** with multiple port forwards
- **Direct TCP forwarding** without HTTP translation layer
- **Dynamic port allocation** from control plane
- **Protocol-agnostic** tunneling for any TCP service

## Architecture Diagram

```text
┌─────────────────────────────────────────────────────────────────────────┐
│                      PipeOps Control Plane                              │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────────────┐   │
│  │  Dashboard   │  │ API Gateway  │  │   Chisel Tunnel Server     │   │
│  │              │  │              │  │  (Port Allocator)          │   │
│  └──────────────┘  └──────────────┘  └────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
                           │                      │
                           │ HTTP/REST API        │ Chisel Tunnel
                           │ (Tunnel Status)      │ (TCP Forwarding)
                           ▼                      ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        Customer Infrastructure                           │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                         k3s Cluster                             │   │
│  │  ┌──────────────────────────────────────────────────────────┐  │   │
│  │  │              PipeOps Agent (Pod)                         │  │   │
│  │  │  ┌────────────────┐  ┌─────────────────────────────┐    │  │   │
│  │  │  │  HTTP Server   │  │    Tunnel Manager           │    │  │   │
│  │  │  │  (Port 8080)   │  │  - Poll Service (Status)    │    │  │   │
│  │  │  │                │  │  - Chisel Client            │    │  │   │
│  │  │  │  - Health      │  │  - Multi-Port Forwarding    │    │  │   │
│  │  │  │  - Metrics     │  │                             │    │  │   │
│  │  │  │  - Dashboard   │  │  Forwards:                  │    │  │   │
│  │  │  └────────────────┘  │  • K8s API (6443)           │    │  │   │
│  │  │                      │  • Kubelet (10250)          │    │  │   │
│  │  │  ┌────────────────┐  │  • Agent HTTP (8080)        │    │  │   │
│  │  │  │  K8s Client    │  └─────────────────────────────┘    │  │   │
│  │  │  │  (In-cluster)  │                                      │  │   │
│  │  │  └────────────────┘                                      │  │   │
│  │  └──────────────────────────────────────────────────────────┘  │   │
│  │                                                                 │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐    │   │
│  │  │   K8s API    │  │   Kubelet    │  │  User Workloads  │    │   │
│  │  │  (Port 6443) │  │  (Port 10250)│  │  (Deployments)   │    │   │
│  │  └──────────────┘  └──────────────┘  └──────────────────────┘    │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                        Host System                              │   │
│  │  • Ubuntu/Debian/CentOS/RHEL                                    │   │
│  │  • Docker Engine                                                │   │
│  │  • k3s (Lightweight Kubernetes)                                 │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘

Tunnel Flow:
═══════════
Agent → Control Plane: "Request tunnel with forwards: [k8s-api, kubelet, agent-http]"
Control Plane: Allocates ports [8000, 8001, 8002]
Agent → Chisel Server: Creates tunnel with remotes:
  - R:8000:localhost:6443   (K8s API)
  - R:8001:localhost:10250  (Kubelet)
  - R:8002:localhost:8080   (Agent HTTP)
Control Plane → Agent: Access via allocated ports (protocol-agnostic)
```

## Components

### 1. PipeOps Agent Core (`internal/agent`)

The main agent orchestrator with Portainer-style tunnel management:

- **Registration**: Direct registration with PipeOps control plane via HTTP
- **Heartbeat**: Maintains connection health with tunnel status
- **Tunnel Orchestration**: Initializes and manages tunnel lifecycle
- **Multi-Forward Configuration**: Configures multiple port forwards from YAML
- **Lifecycle Management**: Clean startup/shutdown with tunnel cleanup

### 2. Tunnel Infrastructure (`internal/tunnel/`)

**Portainer-Style Multi-Port Forwarding:**

#### Tunnel Client (`client.go`)
- **Chisel Integration**: Wraps jpillora/chisel for TCP tunneling
- **Multi-Remote Support**: Single tunnel with multiple port forwards
- **Configuration**: Accepts array of forwards (name, local_addr, remote_port)
- **Connection Management**: Handles tunnel lifecycle and reconnection

#### Poll Service (`poll.go`)
- **Control Plane Polling**: Requests tunnel status at configured intervals
- **Forward Allocation**: Receives remote port assignments from control plane
- **Dynamic Configuration**: Matches allocations with local forward config
- **Tunnel Creation**: Creates Chisel tunnel when status is "ready"

#### Tunnel Manager (`manager.go`)
- **Lifecycle Coordination**: Orchestrates poll service and client
- **Configuration Management**: Converts YAML config to internal format
- **Activity Tracking**: Records tunnel usage for inactivity timeout
- **Graceful Shutdown**: Ensures clean tunnel disconnection

### 3. HTTP Server (`internal/server`)

Simplified server without proxy translation layer:

- **HTTP Endpoints**: RESTful API for health, metrics, and status
- **WebSocket Hub**: Bidirectional real-time communication
- **Server-Sent Events**: Live event streaming to control plane
- **Static Dashboard**: Real-time monitoring interface
- **Advanced Health Checks**: Comprehensive system health reporting
- **Runtime Metrics**: Performance and resource monitoring
- **No Proxy Layer**: Direct tunnel forwarding (Portainer approach)

### 4. Kubernetes Client (`internal/k8s`)

Direct Kubernetes API integration:

- **Cluster Monitoring**: Node, pod, and service status collection
- **Resource Management**: Direct Kubernetes API operations
- **Log Retrieval**: Pod log streaming and collection
- **Metrics Collection**: Real-time cluster resource metrics
- **In-Cluster Access**: Uses service account for authentication

### 5. Type System (`pkg/types`)

Configuration types for multi-port forwarding:

- **Agent Configuration**: Direct communication configuration
- **Status Types**: Health and status reporting structures
- **Command Types**: Direct command execution definitions
- **Kubernetes Types**: API proxy request/response structures

## Communication Protocols

### 1. HTTP REST API

Direct HTTP communication for standard operations:

- **Registration**: `POST /register` - Agent registration with control plane
- **Health Checks**: `GET /health`, `/ready`, `/version` - Basic health endpoints
- **Advanced Health**: `GET /api/health/detailed` - Comprehensive health information
- **Features**: `GET /api/status/features` - Dynamic capability reporting
- **Runtime Metrics**: `GET /api/metrics/runtime` - Performance monitoring
- **Connectivity**: `GET /api/status/connectivity` - Network connectivity testing

### 2. WebSocket Real-Time Communication

Bidirectional real-time communication:

```text
Client                           Agent
  |                               |
  |--- WebSocket Connect -------->|
  |<-- Connection Established ----|
  |                               |
  |--- Real-time Commands ------->|
  |<-- Status Updates -------------|
  |<-- Log Streams ---------------|
  |<-- Event Notifications ------|
```

### 3. Server-Sent Events (SSE)

One-way real-time updates from agent:

```text
Control Plane                    Agent
      |                           |
      |--- SSE Connection ------->|
      |<-- Live Events ------------|
      |<-- Status Updates ---------|
      |<-- Metrics Stream ---------|
      |<-- Log Events -------------|
```

## Data Flow

## Portainer-Style Tunneling Approach

### Why Portainer-Style?

The agent uses a **pure TCP tunneling approach** inspired by Portainer, eliminating the need for application-level protocol translation:

**Benefits:**

- 🚀 **Simpler**: No HTTP translation layer needed
- 🔒 **More Secure**: Direct TLS to services, no man-in-the-middle translation
- 📈 **Scalable**: Easy to add more port forwards
- 🎯 **Flexible**: Protocol-agnostic (works with any TCP service)
- 💪 **Robust**: Fewer moving parts = more reliable

### Multi-Port Forwarding

Single Chisel tunnel with multiple remote specifications:

```yaml
tunnel:
  enabled: true
  forwards:
    - name: "kubernetes-api"
      local_addr: "localhost:6443"
      remote_port: 0  # Allocated by control plane
    - name: "kubelet-metrics"
      local_addr: "localhost:10250"
      remote_port: 0
    - name: "agent-http"
      local_addr: "localhost:8080"
      remote_port: 0
```

**Chisel Remote Format:**

```text
R:8000:localhost:6443,R:8001:localhost:10250,R:8002:localhost:8080
```

## Data Flow

### 1. Tunnel Establishment Flow

```text
Agent                          Control Plane
  |                               |
  |--- Poll: GET /tunnel/status ->|
  |    {agent_id}                 |
  |                               |
  |                               |--- Allocate Ports
  |                               |    (8000, 8001, 8002)
  |                               |
  |<-- Tunnel Status -------------|
  |    {status: "ready",          |
  |     forwards: [               |
  |       {name: "k8s-api",       |
  |        remote_port: 8000},    |
  |       ...                     |
  |     ]}                        |
  |                               |
  |--- Create Chisel Tunnel ----->|
  |    R:8000:localhost:6443      |
  |    R:8001:localhost:10250     |
  |    R:8002:localhost:8080      |
  |                               |
  |<== Tunnel Established =======>|
```

### 2. Control Plane Access Flow

```text
Control Plane                  Tunnel Server                  Agent
      |                           |                           |
      |--- Access K8s API -------->|                           |
      |    (Port 8000)            |                           |
      |                           |=== Forward to 6443 ======>|
      |                           |                           |--- K8s API
      |                           |<== Response =============|
      |<-- K8s Response -----------|                           |
```

### 3. Agent Registration Flow (Direct HTTP)

```text
Agent                          Control Plane
  |                               |
  |--- HTTP POST /register ------>|
  |    (ID, Name, Cluster, etc.)  |
  |<-- Registration Response -----|
  |                               |
  |--- Initial Status Report ---->|
  |<-- Acknowledgment ------------|
```

### 4. Real-Time Monitoring Flow

```text
Agent                          Control Plane
  |                               |
````
  |<-- WebSocket Connection ------|
  |                               |
  |--- Live Health Data --------->|
  |--- Runtime Metrics ---------->|
  |--- Event Notifications ------>|
  |--- Log Streams -------------->|
  |                               |
  |<-- Commands/Requests ----------|
  |--- Response/Status ----------->|
```

### 3. Kubernetes Operations Flow

```text
Control Plane    Agent           k3s Cluster
     |             |                  |
     |--- Command->|                  |
     |             |--- K8s API ----->|
     |             |<-- Response -----|
     |<-- Status --|                  |
     |             |                  |
     |<-- Live Updates (WebSocket)---|
```

## Security Model

### 1. Network Security

- **Direct Communication**: Simplified network model without proxy complexity
- **TLS Encryption**: All HTTP/WebSocket communication encrypted with TLS 1.3
- **Certificate Validation**: Strict certificate validation for all connections
- **Configurable Ports**: Agent exposes single HTTP port (default: 8080)
- **WebSocket Security**: Secure WebSocket (WSS) for real-time communication

### 2. Authentication

- **Token-Based**: Agent authenticates using secure API tokens
- **Direct Auth**: No proxy authentication layers to manage
- **Token Rotation**: Supports frequent token rotation for enhanced security
- **Scope Limitation**: Tokens scoped to specific clusters and operations

### 3. Authorization

- **RBAC Integration**: Uses Kubernetes RBAC for all operations
- **Principle of Least Privilege**: Minimal required permissions
- **Namespace Isolation**: Configurable namespace restrictions
- **Resource Limitations**: Fine-grained resource access controls
- **Feature-Based Access**: Dynamic feature detection with access control

### 4. Container Security

- **Non-Root User**: Runs as non-root user (UID 1000)
- **Read-Only Filesystem**: Root filesystem is read-only
- **No Privileged Access**: No privileged container access required
- **Capability Dropping**: All unnecessary capabilities dropped
- **Security Contexts**: Comprehensive security context configuration
- **Minimal Attack Surface**: Reduced complexity without proxy dependencies

### 5. Real-Time Security

- **WebSocket Authentication**: Secure WebSocket connections with token validation
- **SSE Security**: Server-sent events with proper authentication
- **Connection Limits**: Configurable connection limits and rate limiting
- **Message Validation**: All real-time messages validated and sanitized

## Deployment Strategies

### 1. Single-Node Deployment

Ideal for development, testing, or small production workloads:

```bash
# Install k3s and agent in one command
curl -sSL https://get.pipeops.io/agent | bash
```

### 2. Multi-Node Deployment

For production clusters with multiple nodes:

```bash
# Master node
export K3S_TOKEN="your-cluster-token"
curl -sSL https://get.k3s.io | sh -s - server

# Worker nodes
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="your-cluster-token"
curl -sSL https://get.k3s.io | sh -s - agent

# Deploy PipeOps agent on master
kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml
```

### 3. High Availability Deployment

For production environments requiring high availability:

```bash
# Setup embedded etcd cluster
curl -sSL https://get.k3s.io | sh -s - server --cluster-init
# Additional master nodes join the cluster
curl -sSL https://get.k3s.io | sh -s - server --server https://first-master:6443
```

## Configuration

### 1. Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PIPEOPS_API_URL` | PipeOps control plane URL | `https://api.pipeops.io` |
| `PIPEOPS_TOKEN` | Authentication token | Required |
| `PIPEOPS_CLUSTER_NAME` | Cluster identifier | `default-cluster` |
| `PIPEOPS_AGENT_ID` | Unique agent identifier | Auto-generated |
| `PIPEOPS_LOG_LEVEL` | Logging level | `info` |
| `PIPEOPS_PORT` | HTTP server port | `8080` |
| `PIPEOPS_ENABLE_WEBSOCKET` | Enable WebSocket features | `true` |
| `PIPEOPS_ENABLE_SSE` | Enable Server-Sent Events | `true` |

### 2. Configuration File

```yaml
# ~/.pipeops-agent.yaml
agent:
  id: "agent-prod-east-001"
  name: "Production East Agent"
  cluster_name: "production-east"
  version: "v2.0.0"
  port: 8080
  labels:
    environment: "production"
    region: "us-east-1"

pipeops:
  api_url: "https://api.pipeops.io"
  token: "your-token-here"
  
server:
  enable_websocket: true
  enable_sse: true
  enable_dashboard: true
  cors_enabled: true
  
realtime:
  websocket_port: 8080
  sse_heartbeat: "30s"
  connection_timeout: "5m"
  max_connections: 100

kubernetes:
  in_cluster: true
  namespace: "pipeops-system"
  kubeconfig: ""

logging:
  level: "info"
  format: "json"
  
features:
  health_monitoring: true
  runtime_metrics: true
  feature_detection: true
  connectivity_testing: true
```

## Monitoring and Observability

### 1. Health Endpoints

Comprehensive health monitoring with multiple endpoints:

- **Basic Health**: `/health` - Simple liveness check
- **Readiness**: `/ready` - Kubernetes connectivity and operational status
- **Version**: `/version` - Agent version and build information
- **Detailed Health**: `/api/health/detailed` - Comprehensive system health
- **Features**: `/api/status/features` - Available features and capabilities
- **Runtime Metrics**: `/api/metrics/runtime` - Real-time performance metrics
- **Connectivity**: `/api/status/connectivity` - Network connectivity tests

### 2. Real-Time Monitoring

Live monitoring capabilities:

- **WebSocket Dashboard**: `/ws` - Real-time bidirectional monitoring
- **Event Stream**: `/api/realtime/events` - Server-sent event stream  
- **Log Stream**: `/api/realtime/logs` - Live log streaming
- **Static Dashboard**: `/dashboard` - Web-based monitoring interface

### 3. Metrics Collection

Advanced metrics exposed on the main port:

- `pipeops_agent_status` - Agent operational status (direct communication)
- `pipeops_websocket_connections` - Active WebSocket connections
- `pipeops_sse_connections` - Active Server-Sent Event connections
- `pipeops_http_requests_total` - HTTP request counter by endpoint
- `pipeops_cluster_nodes_total` - Number of cluster nodes
- `pipeops_cluster_pods_total` - Number of pods in cluster
- `pipeops_agent_uptime_seconds` - Agent uptime
- `pipeops_kubernetes_api_calls_total` - Kubernetes API call counter

### 4. Structured Logging

JSON logging with enhanced context:

```json
{
  "time": "2024-01-15T10:30:00Z",
  "level": "info",
  "msg": "WebSocket connection established",
  "component": "websocket-handler",
  "connection_id": "ws-conn-123",
  "client_ip": "10.0.1.15",
  "protocol": "direct"
}
```

### 5. Dashboard Features

Static dashboard provides:

- **Real-time Health Monitoring**: Live health status updates
- **WebSocket Connection Status**: Active connection monitoring
- **Cluster Overview**: Node and pod status visualization
- **Runtime Metrics**: CPU, memory, and performance charts
- **Event Timeline**: Live event stream visualization
- **Feature Detection**: Available capabilities display

## Troubleshooting

### 1. Common Issues

#### Agent Won't Connect

```bash
# Check network connectivity
curl -v https://api.pipeops.io/health

# Check token validity
kubectl logs deployment/pipeops-agent -n pipeops-system

# Verify configuration
kubectl get secret pipeops-agent-config -n pipeops-system -o yaml
```

#### Deployments Failing

```bash
# Check RBAC permissions
kubectl auth can-i create deployments --as=system:serviceaccount:pipeops-system:pipeops-agent

# Check resource quotas
kubectl describe resourcequota -A

# Check node resources
kubectl top nodes
```

### 2. Debug Mode

Enable debug logging:

```bash
kubectl set env deployment/pipeops-agent PIPEOPS_LOG_LEVEL=debug -n pipeops-system
```

### 3. Agent Logs

View real-time logs:

```bash
kubectl logs -f deployment/pipeops-agent -n pipeops-system
```

## Performance Considerations

### 1. Resource Requirements

| Component | CPU Request | Memory Request | CPU Limit | Memory Limit |
|-----------|-------------|----------------|-----------|--------------|
| Agent Core | 100m | 128Mi | 500m | 512Mi |
| WebSocket Hub | 50m | 64Mi | 200m | 256Mi |
| Real-Time Monitor | 25m | 32Mi | 100m | 128Mi |

### 2. Scaling Considerations

- **Simplified Architecture**: No proxy tunnel overhead
- **Direct Connections**: WebSocket and HTTP connections scale efficiently
- **Connection Limits**: Configurable WebSocket connection pooling (default: 100)
- **Message Throughput**: ~2000 messages/second per agent (improved without proxy)
- **Cluster Size**: Tested with clusters up to 100 nodes
- **Real-Time Performance**: Sub-100ms latency for WebSocket communications

### 3. Network Requirements

- **Bandwidth**: ~500B/s baseline, ~5KB/s during deployments (improved efficiency)
- **Real-Time Overhead**: ~1KB/s for WebSocket heartbeats and live updates
- **Latency**: <200ms recommended for responsive real-time operations
- **Reliability**: Direct connections with automatic reconnection logic

## Advanced Features

### 1. Real-Time Capabilities

- **Live Health Monitoring**: Continuous health status streaming
- **WebSocket Commands**: Bidirectional command execution
- **Event Broadcasting**: Real-time event distribution to multiple clients
- **Log Streaming**: Live pod log streaming via WebSocket
- **Dashboard Integration**: Real-time web dashboard

### 2. KubeSail-Inspired Features

- **Feature Detection**: Dynamic capability discovery and reporting
- **Runtime Metrics**: Advanced performance monitoring
- **Connectivity Testing**: Network connectivity validation
- **Health Aggregation**: Comprehensive system health reporting
- **Service Discovery**: Automatic service and endpoint detection

### 3. Extensibility

- **WebSocket Protocols**: Custom WebSocket message protocols
- **SSE Channels**: Multiple Server-Sent Event channels
- **Dashboard Plugins**: Extensible dashboard components
- **Custom Health Checks**: User-defined health validation
- **Event Handlers**: Custom real-time event processing

## Migration Benefits

### 1. Architectural Improvements

- **Simplified Deployment**: No external proxy dependencies
- **Reduced Complexity**: Direct communication patterns
- **Enhanced Control**: Full control over communication protocols
- **Better Reliability**: Fewer points of failure
- **Improved Performance**: Direct connections without proxy overhead

### 2. Real-Time Advantages

- **Instant Updates**: WebSocket-based real-time communication
- **Live Monitoring**: Server-sent events for continuous monitoring
- **Interactive Dashboard**: Real-time web interface
- **Bidirectional Communication**: Full-duplex agent communication
- **Streaming Capabilities**: Live log and event streaming

### 3. Modern Platform Features

- **KubeSail-Inspired**: Advanced monitoring and health features
- **API-First Design**: RESTful API with real-time extensions
- **Container-Native**: Optimized for container environments
- **Cloud-Ready**: Designed for modern cloud-native deployments
