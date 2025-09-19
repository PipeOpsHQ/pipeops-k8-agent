# PipeOps VM Agent

A secure Kubernetes agent that enables PipeOps Runner to manage k3s clusters in bring-your-own-server environments without exposing the Kubernetes API directly.

## Architecture

The PipeOps VM Agent acts as a secure bridge in a three-tier architecture:

```
┌─────────────────┐    Registration     ┌─────────────────┐    Agent Details    ┌─────────────────┐
│  VM Agent       │ ────────────────► │  Control Plane  │ ────────────────► │  Runner System  │
│                 │    HTTP API/TLS    │                 │    RabbitMQ       │                 │
│  • Registration │         ▲           │  • Auth/Auth    │                     │  • Deployments  │
│  • Heartbeat    │         │           │  • Agent Registry│                     │  • Secrets      │
│  • Status       │         │           │  • Job Routing  │                     │  • Jobs         │
│  • Monitoring   │         │           │  • Load Balance │                     │  • Scaling      │
└─────────────────┘         │           └─────────────────┘                     └─────────────────┘
         │                  │                                                            │
         │ FRP Tunnel       │                    Operational Commands                    │
         │ (Outbound)       │               (Deployments, Scaling, etc.)               │
         ▼                  │                    HTTP via FRP Tunnels                   │
┌─────────────────┐         │                                                            │
│   FRP Server    │ ────────┘────────────────────────────────────────────────────────────┘
│                 │
│  • Tunnel Mgmt  │
│  • Load Balance │
│  • Auth Service │
└─────────────────┘
         │
         │ Kubernetes API
         ▼
┌─────────────────┐
│  k3s Cluster    │
│                 │
│  • Applications │
│  • Services     │
│  • Resources    │
│  • Storage      │
└─────────────────┘
```

### Communication Flow

1. **Registration Phase**: VM Agent registers with Control Plane via HTTPS API
2. **Tunnel Establishment**: VM Agent creates FRP tunnels to enable inbound connections
3. **Agent Discovery**: Control Plane sends agent details to Runner via RabbitMQ  
4. **Operational Phase**: Runner communicates with VM Agent via HTTP through FRP tunnels
5. **Execution**: VM Agent executes commands on the k3s cluster via Kubernetes API

## System Integration

The VM Agent integrates with the **PipeOps Runner**, a sophisticated infrastructure orchestration platform that handles:

- **Deployment Management**: Application deployments, scaling, and lifecycle management
- **Secret Creation**: Kubernetes secrets, ConfigMaps, and sensitive data management
- **Job Processing**: Build jobs, deployment pipelines, and automated tasks
- **Multi-Cloud Operations**: Cross-cloud resource management and provisioning
- **Monitoring Stack**: Prometheus, Grafana, Loki, and OpenCost integration

## Features

- 🔐 **Secure Communication**: Agent-only outbound connections, no API exposure
- 🚀 **Easy Multi-Node Setup**: One-command installation for server and worker nodes
- 📊 **Real-time Monitoring**: Cluster status and metrics reporting to Runner
- 🔄 **Command Execution**: Secure execution of Runner-initiated operations
- 🛡️ **RBAC Integration**: Fine-grained permissions for Runner operations
- 🌐 **BYOK Support**: Bring-your-own-Kubernetes cluster integration
- 📦 **Worker Node Scaling**: Simple worker node addition and management

## Quick Start

### Server Node Installation

```bash
# Set your PipeOps token
export AGENT_TOKEN="your-pipeops-token"
export CLUSTER_NAME="production-cluster"

# Install k3s and deploy agent
curl -sSL https://get.pipeops.io/agent | bash
```

### Adding Worker Nodes

After setting up the server node, scale your cluster by adding worker nodes:

```bash
# Get cluster connection info from server node
./scripts/install.sh cluster-info

# On each worker node, use the simple join script
curl -sSL https://get.pipeops.io/join-worker | bash -s <MASTER_IP> <CLUSTER_TOKEN>

# Or use environment variables
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

## Configuration

### Environment Variables

- `PIPEOPS_CONTROL_PLANE_URL`: Control Plane API endpoint for registration
- `AGENT_TOKEN`: Authentication token for Control Plane communication
- `CLUSTER_NAME`: Cluster identifier for Runner registration
- `NODE_TYPE`: Installation type (`server` for master, `agent` for worker)
- `K3S_URL`: Master server URL (required for worker nodes)
- `K3S_TOKEN`: Cluster token (required for worker nodes)
- `LOG_LEVEL`: Logging level (debug, info, warn, error)

### Control Plane Integration

The VM Agent first registers with the PipeOps Control Plane:

- **Agent Registration**: Registers cluster details with Control Plane
- **Authentication**: Secure token-based authentication
- **Heartbeat**: Continuous connection and health monitoring
- **Service Discovery**: Control Plane discovers available agents

### Runner Communication

After registration, the Control Plane notifies the Runner via RabbitMQ:

- **Agent Availability**: Control Plane sends agent details to Runner
- **Direct Communication**: Runner establishes direct connection with VM Agent
- **Command Execution**: Runner sends deployment/management commands to Agent
- **Status Reporting**: Agent reports back to Runner with operation results

### Required Kubernetes Resources

The agent ensures these components are available for Runner operations:

#### Namespaces
- `pipeops`: PipeOps system components
- `production`: Production application deployments  
- `beta`: Staging/development deployments
- `monitoring`: Observability stack (Prometheus, Grafana, Loki)
- `istio-system`: Service mesh components (optional)

#### Controllers
- **NGINX Ingress**: HTTP/HTTPS traffic routing
- **Cert Manager**: Automatic SSL certificate management
- **Metrics Server**: Resource utilization metrics
- **Storage CSI**: Persistent volume provisioning

## Development

```bash
# Clone the repository
git clone https://github.com/pipeops/pipeops-vm-agent.git
cd pipeops-vm-agent

# Install dependencies
go mod tidy

# Build the agent
go build -o bin/agent cmd/agent/main.go

# Run locally (requires kubeconfig)
./bin/agent --config config.yaml
```

## License

MIT License - see [LICENSE](LICENSE) for details.
# pipeops-k8-agent
