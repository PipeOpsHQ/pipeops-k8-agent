# PipeOps VM Agent

A modern Kubernetes agent featuring a custom real-time architecture that enables direct communication with the PipeOps Control Plane through HTTP, WebSocket, and Server-Sent Events.

## Architecture

The PipeOps VM Agent operates with a simplified, KubeSail-inspired architecture:

```text
┌─────────────────┐    Direct HTTP/WS    ┌─────────────────┐    Real-Time     ┌─────────────────┐
│  VM Agent       │ ◄──────────────────► │  Control Plane  │ ───────────────► │  Runner System  │
│                 │    TLS/WebSocket     │                 │    Events        │                 │
│  • HTTP Server  │                      │  • Agent Manager│                  │  • Deployments  │
│  • WebSocket    │                      │  • Dashboard    │                  │  • Monitoring   │
│  • Real-Time    │                      │  • API Gateway  │                  │  • Operations   │
│  • Dashboard    │                      │  • Event Hub    │                  │  • Scaling      │
└─────────────────┘                      └─────────────────┘                  └─────────────────┘
         │                                                                              │
         │ Direct K8s API                    Real-Time Communication                   │
         │ (In-Cluster)                  • HTTP REST API                               │
         ▼                              • WebSocket (Bidirectional)                    │
┌─────────────────┐                     • Server-Sent Events                          │
│  k3s Cluster    │                     • Live Dashboard                               │
│                 │                                                                    │
│  • Applications │◄───────────────────────────────────────────────────────────────────┘
│  • Services     │                    Direct Commands & Operations
│  • Resources    │
│  • Storage      │
└─────────────────┘
```

### Communication Protocols

1. **HTTP REST API**: Standard operations, health checks, and configuration
2. **WebSocket**: Bidirectional real-time communication for commands and status
3. **Server-Sent Events**: Live updates and monitoring data streaming
4. **Static Dashboard**: Real-time web interface for monitoring and management

## System Integration

The VM Agent integrates with the **PipeOps Runner**, a sophisticated infrastructure orchestration platform that handles:

- **Deployment Management**: Application deployments, scaling, and lifecycle management
- **Secret Creation**: Kubernetes secrets, ConfigMaps, and sensitive data management
- **Job Processing**: Build jobs, deployment pipelines, and automated tasks
- **Multi-Cloud Operations**: Cross-cloud resource management and provisioning
- **Monitoring Stack**: Prometheus, Grafana, Loki, and OpenCost integration

## Features

- ⚡ **Real-Time Communication**: WebSocket and Server-Sent Events for live updates
- 🎯 **KubeSail-Inspired**: Advanced health monitoring and feature detection
- 🚀 **Direct Architecture**: Simplified communication without proxy complexity
- 📊 **Live Dashboard**: Real-time web interface for monitoring and management
- 🔄 **Modern HTTP API**: RESTful endpoints with comprehensive health checks
- 🛡️ **RBAC Integration**: Fine-grained Kubernetes permissions
- 🌐 **Container Native**: Optimized for modern container environments
- 📦 **Easy Scaling**: Simple multi-node cluster management
- � **Secure by Design**: TLS encryption and token-based authentication
- 📈 **Runtime Metrics**: Advanced performance and resource monitoring

## Quick Start

### Server Node Installation

Set up a k3s server with the PipeOps agent:

```bash
# Set your PipeOps configuration
export PIPEOPS_TOKEN="your-pipeops-token"
export CLUSTER_NAME="production-cluster"
export PIPEOPS_API_URL="https://api.pipeops.io"

# Install k3s and deploy agent
curl -sSL https://get.pipeops.io/agent | bash
```

### Adding Worker Nodes

Scale your cluster by adding worker nodes:

```bash
# Get cluster connection info from server node
./scripts/install.sh cluster-info

# On each worker node, use the join script
curl -sSL https://get.pipeops.io/join-worker | bash -s <MASTER_IP> <CLUSTER_TOKEN>

# Or manually with environment variables
export NODE_TYPE="agent"
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="cluster-token"
curl -sSL https://get.pipeops.io/agent | bash
```

### Manual Installation

1. **Install k3s server:**

```bash
curl -sfL https://get.k3s.io | sh -
```

2. **Deploy the VM agent:**

```bash
kubectl apply -f https://raw.githubusercontent.com/pipeops/pipeops-vm-agent/main/deployments/agent.yaml
```

3. **Add worker nodes:**

```bash
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="your-cluster-token"
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="agent" sh -
```

### Access Real-Time Dashboard

Once deployed, access the agent's real-time dashboard:

```bash
# Port forward to access dashboard locally
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system

# Open dashboard in browser
open http://localhost:8080/dashboard
```

## Configuration

### Environment Variables

- `PIPEOPS_API_URL`: PipeOps Control Plane API endpoint
- `PIPEOPS_TOKEN`: Authentication token for secure communication  
- `PIPEOPS_CLUSTER_NAME`: Cluster identifier
- `PIPEOPS_AGENT_ID`: Unique agent identifier
- `PIPEOPS_PORT`: HTTP server port (default: 8080)
- `PIPEOPS_ENABLE_WEBSOCKET`: Enable WebSocket features (default: true)
- `PIPEOPS_ENABLE_SSE`: Enable Server-Sent Events (default: true)
- `LOG_LEVEL`: Logging level (debug, info, warn, error)

### Real-Time Features

The agent provides modern real-time capabilities:

- **HTTP REST API**: Standard operations and health monitoring
- **WebSocket Communication**: Bidirectional real-time messaging
- **Server-Sent Events**: Live updates and event streaming
- **Static Dashboard**: Web-based monitoring interface
- **Advanced Health Checks**: Comprehensive system monitoring
- **Feature Detection**: Dynamic capability reporting
- **Runtime Metrics**: Performance and resource monitoring

### Control Plane Integration

Direct communication with PipeOps Control Plane:

- **Direct Registration**: HTTP-based agent registration
- **Token Authentication**: Secure token-based authentication  
- **Real-Time Heartbeat**: Live connection monitoring
- **WebSocket Commands**: Real-time command execution
- **Event Streaming**: Live status and event updates

### Required Kubernetes Resources

The agent manages these components:

#### Namespaces

- `pipeops-system`: Agent and system components
- `pipeops`: Application deployments
- `monitoring`: Observability stack (optional)

#### Controllers

- **NGINX Ingress**: HTTP/HTTPS traffic routing
- **Cert Manager**: SSL certificate management  
- **Metrics Server**: Resource utilization metrics
- **Storage Provisioner**: Persistent volume management

## Development

### Building from Source

```bash
# Clone the repository
git clone https://github.com/PipeOpsHQ/pipeops-k8-agent.git
cd pipeops-k8-agent

# Install dependencies
go mod tidy

# Build the agent
go build -o bin/pipeops-vm-agent cmd/agent/main.go

# Run locally (requires kubeconfig)
./bin/pipeops-vm-agent --pipeops-url "https://api.pipeops.io" \
                       --token "your-token" \
                       --cluster-name "dev-cluster" \
                       --agent-id "dev-agent"
```

### Testing Real-Time Features

```bash
# Start the agent
./bin/pipeops-vm-agent --config config.yaml

# Test WebSocket connection
wscat -c ws://localhost:8080/ws

# Test Server-Sent Events
curl -N http://localhost:8080/api/realtime/events

# Access dashboard
open http://localhost:8080/dashboard
```

### API Endpoints

- **Health**: `GET /health` - Basic health check
- **Ready**: `GET /ready` - Kubernetes connectivity check
- **Version**: `GET /version` - Agent version information
- **Detailed Health**: `GET /api/health/detailed` - Comprehensive health
- **Features**: `GET /api/status/features` - Available capabilities
- **Runtime Metrics**: `GET /api/metrics/runtime` - Performance metrics
- **WebSocket**: `GET /ws` - Real-time communication
- **Events**: `GET /api/realtime/events` - Server-sent event stream
- **Dashboard**: `GET /dashboard` - Real-time monitoring interface

## Architecture Benefits

### Simplified Design

- **No Proxy Dependencies**: Direct communication eliminates complexity
- **Reduced Attack Surface**: Fewer components and potential failure points
- **Enhanced Performance**: Direct connections without tunneling overhead
- **Easier Debugging**: Simplified network topology and logging

### Modern Features

- **Real-Time Communication**: WebSocket and SSE for live updates
- **KubeSail-Inspired**: Advanced monitoring and health detection
- **API-First Design**: RESTful endpoints with real-time extensions
- **Container Native**: Optimized for modern container environments

## License

MIT License - see [LICENSE](LICENSE) for details.
