# PipeOps Kubernetes Agent

A lightweight Kubernetes agent that enables secure, on-demand access to your Kubernetes clusters through encrypted Chisel tunnels with multi-port forwarding.

## What It Does

The PipeOps agent is deployed **as a pod inside your Kubernetes cluster** and establishes an outbound connection to the PipeOps control plane. It creates secure TCP tunnels that allow the control plane to access your cluster's Kubernetes API, kubelet metrics, monitoring services, and agent status endpoints without requiring inbound firewall rules or exposing your cluster to the internet.

**Key Features:**
- **Cloud-Native Design**: Runs as a pod inside your Kubernetes cluster
- **In-Cluster Access**: Uses Kubernetes REST API directly (no kubectl dependencies)
- **ServiceAccount Authentication**: Native Kubernetes authentication via mounted tokens
- **Outbound-Only Connections**: No inbound ports required on your infrastructure
- **Multi-Port TCP Tunneling**: Single Chisel tunnel forwards multiple services simultaneously
- **Dynamic Port Allocation**: Control plane dynamically assigns remote ports
- **Protocol-Agnostic**: Pure TCP forwarding works with any protocol
- **Secure by Default**: All connections encrypted with TLS
- **Mandatory Registration**: Strict validation prevents operation with invalid credentials
- **Real-time Heartbeat**: 5-second interval with cluster metrics (node/pod counts)
- **Integrated Monitoring**: Prometheus, Loki, Grafana, and OpenCost

## In-Cluster Architecture

The agent is designed to run **as a pod inside your Kubernetes cluster**:

- ✅ **No kubectl**: Uses Kubernetes REST API directly
- ✅ **No external binaries**: Pure Go implementation
- ✅ **ServiceAccount authentication**: Automatic token mounting
- ✅ **Works everywhere**: K3s, Minikube, Kind, EKS, GKE, AKS
- ✅ **Cloud-native**: Native Kubernetes deployment patterns

See [In-Cluster Architecture Documentation](docs/IN_CLUSTER_ARCHITECTURE.md) for details.

## Architecture Overview

```text
┌──────────────────────────────────────────────────────────────────┐
│                    PipeOps Control Plane                         │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐     │
│  │   Dashboard  │  │  API Gateway │  │  Chisel Server    │     │
│  │              │  │              │  │  (Port Allocator) │     │
│  └──────────────┘  └──────────────┘  └───────────────────┘     │
└──────────────────────────────────────────────────────────────────┘
                           │                      │
                           │ HTTPS API            │ Chisel Tunnel
                           │ (Registration,       │ (TCP Forwarding)
                           │  Heartbeat,          │
Prefer to stick with pure `kubectl` commands? Option B in `docs/install-from-github.md` shows how to recreate the secret and ConfigMap without relying on `sed`.

                           │  Tunnel Status)      │
                           ▼                      ▼
┌──────────────────────────────────────────────────────────────────┐
│                      Your Infrastructure                         │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                    Kubernetes Cluster                     │  │
│  │  ┌────────────────────────────────────────────────────┐   │  │
│  │  │           PipeOps Agent (Pod)                      │   │  │
│  │  │                                                     │   │  │
│  │  │  ┌──────────────┐      ┌──────────────────────┐   │   │  │
│  │  │  │ HTTP Server  │      │  Tunnel Manager      │   │   │  │
│  │  │  │ (Port 8080)  │      │                      │   │   │  │
│  │  │  │              │      │  • Poll Service      │   │   │  │
│  │  │  │ • /health    │      │  • Chisel Client     │   │   │  │
│  │  │  │ • /ready     │      │  • Multi-Port Forward│   │   │  │
│  │  │  │ • /metrics   │      │                      │   │   │  │
│  │  │  │ • /dashboard │      │  Forwards:           │   │   │  │
│  │  │  └──────────────┘      │  • K8s API :6443     │   │   │  │
│  │  │                        │  • Kubelet :10250    │   │   │  │
│  │  │                        │  • Agent   :8080     │   │   │  │
│  │  │                        └──────────────────────┘   │   │  │
│  │  └────────────────────────────────────────────────────┘   │  │
│  │                                                            │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌───────────────┐   │  │
│  │  │  K8s API     │  │   Kubelet    │  │   Workloads   │   │  │
│  │  │  (Port 6443) │  │ (Port 10250) │  │ (Deployments) │   │  │
│  │  └──────────────┘  └──────────────┘  └───────────────┘   │  │
│  └───────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

## How It Works

### 1. Agent Registration (Mandatory)

When the agent starts, it **must** successfully register with the PipeOps control plane before any other services start:

```text
Agent                              Control Plane
  |                                      |
  |--- POST /api/v1/clusters/agent/register
  |    {                                 |
  |      agent_id: "...",                |
  |      name: "prod-cluster",           |
  |      k8s_service_token: "...",       |
  |      ...                             |
  |    }                                 |
  |                                      |
  |                                      |--- Validates & creates cluster
  |                                      |
  |<-- 200 OK ---------------------------|
  |    {                                 |
  |      cluster_id: "uuid-...",         |
  |      name: "prod-cluster",           |
  |      status: "active"                |
  |    }                                 |
  |                                      |
  |--- Registration Success             |
  |    Stores cluster_id for all         |
  |    subsequent operations             |
```

**Registration Requirements:**
- Agent ID (generated from hostname if not configured)
- Cluster name
- Kubernetes ServiceAccount token (read from `/var/run/secrets/kubernetes.io/serviceaccount/token`)
- Tunnel port configuration
- Agent version

**Registration Failure Behavior:**
- If registration fails (network error, invalid credentials, JSON parse error), the agent **exits immediately**
- HTTP server is stopped before exit
- No fallback to agent_id—cluster_id from control plane is mandatory
- Pod enters CrashLoopBackoff until registration succeeds

### 2. Heartbeat (Every 5 Seconds)

After successful registration, the agent sends heartbeat requests every 5 seconds:

```text
Agent                              Control Plane
  |                                      |
  |--- POST /api/v1/clusters/agent/{cluster_uuid}/heartbeat
  |    {                                 |
  |      cluster_id: "uuid-...",         |
  |      agent_id: "...",                |
  |      status: "active",               |
  |      tunnel_status: "connected",     |
  |      timestamp: "2025-10-15...",     |
  |      metadata: {...}                 |
  |    }                                 |
  |                                      |
  |<-- 200 OK ---------------------------|
  |                                      |
  |--- Wait 5 seconds ------------------>|
  |                                      |
  |--- Next heartbeat ------------------>|
```

**Heartbeat Details:**
- Heartbeat and status updates sent via **WebSocket connection**
- Real-time bidirectional communication with control plane
- Includes cluster_id (UUID from registration)
- Includes tunnel status (connected/disconnected/connecting)
- No token field in payload (control plane doesn't expect it)

### 3. Tunnel Establishment (Optional)

If tunneling is enabled, the agent polls for tunnel status and establishes Chisel connections:

```text
Agent                              Control Plane
  |                                      |
  |--- Poll: GET /api/v1/tunnel/status?agent_id=...
  |                                      |
  |                                      |--- Allocates ports
  |                                      |    (e.g., 8000, 8001, 8002)
  |                                      |
  |<-- 200 OK ---------------------------|
  |    {                                 |
  |      status: "ready",                |
  |      tunnel_url: "wss://...",        |
  |      forwards: [                     |
  |        {name: "k8s-api",             |
  |         remote_port: 8000},          |
  |        {name: "kubelet",             |
  |         remote_port: 8001},          |
  |        {name: "agent-http",          |
  |         remote_port: 8002}           |
  |      ]                               |
  |    }                                 |
  |                                      |
  |--- Chisel Connect ------------------>|
  |    R:8000:localhost:6443             |
  |    R:8001:localhost:10250            |
  |    R:8002:localhost:8080             |
  |                                      |
  |<=== Tunnel Established =============>|
```

**Tunnel Details:**
- Poll interval: 5 seconds (same as heartbeat)
- Single Chisel connection with multiple remote specifications
- Remote ports dynamically allocated by control plane
- Inactivity timeout: 5 minutes (tunnel closes if no API requests)
- Automatic reconnection on disconnect

### 4. Control Plane Access

Once the tunnel is established, the control plane can access your cluster through the forwarded ports:

```text
Control Plane                      Tunnel                    Agent
      |                              |                         |
      |--- K8s API Request---------->|                         |
      |    (Port 8000)               |                         |
      |                              |=== Forward to :6443 ===>|
      |                              |                         |--- K8s API
      |                              |<========================|
      |<-- K8s Response -------------|                         |
```

## Components

### 1. Agent Core (`internal/agent/agent.go`)

The main agent orchestrator that coordinates all operations:

- **Registration**: Mandatory registration with control plane before starting services
- **Heartbeat**: Maintains connection health every 5 seconds
- **Tunnel Management**: Orchestrates tunnel lifecycle (if enabled)
- **HTTP Server**: Exposes health checks and metrics
- **Graceful Shutdown**: Cleans up connections on SIGTERM/SIGINT

### 2. Control Plane Client (`internal/controlplane/`)

Handles all HTTP communication with the PipeOps control plane:

- **Registration API**: `POST /api/v1/clusters/agent/register`
- **Heartbeat API**: `POST /api/v1/clusters/agent/{cluster_uuid}/heartbeat`
- **Tunnel Status API**: `GET /api/v1/tunnel/status`
- **TLS Security**: TLS 1.2+ with certificate validation
- **Error Handling**: Comprehensive error logging with response bodies

### 3. Tunnel Manager (`internal/tunnel/`)

Manages Chisel tunnel lifecycle for multi-port forwarding:

**Poll Service (`poll.go`):**
- Requests tunnel status from control plane at 5-second intervals
- Receives dynamic port allocations
- Triggers tunnel creation when status is "ready"
- Handles inactivity timeout (closes tunnel after 5 minutes of no activity)

**Chisel Client (`client.go`):**
- Wraps [jpillora/chisel](https://github.com/jpillora/chisel) for TCP tunneling
- Establishes single WebSocket connection with multiple remote specifications
- Format: `R:remote_port:local_addr` (e.g., `R:8000:localhost:6443`)
- Automatic reconnection on disconnect

**Manager (`manager.go`):**
- Coordinates poll service and Chisel client
- Converts YAML configuration to internal format
- Tracks tunnel activity for inactivity timeout
- Graceful shutdown on SIGTERM

### 4. HTTP Server (`internal/server/`)

Lightweight HTTP server providing health checks and metrics:

**Health Endpoints:**
- `GET /health` - Liveness probe (simple health check)
- `GET /ready` - Readiness probe (operational status)
- `GET /version` - Agent version and build info

**Monitoring Endpoints:**
- `GET /api/health/detailed` - Comprehensive health information
- `GET /api/status/features` - Available capabilities
- `GET /api/metrics/runtime` - Runtime performance metrics
- `GET /api/status/connectivity` - Network connectivity test

**Real-Time Endpoints:**
- `GET /ws` - WebSocket for bidirectional communication
- `GET /api/realtime/events` - Server-Sent Events stream
- `GET /api/realtime/logs` - Live log streaming
- `GET /dashboard` - Static HTML dashboard

### Kubernetes Token (`pkg/k8s/token.go`)

Extracts the Kubernetes ServiceAccount token for control plane to access the cluster:

- Reads from `/var/run/secrets/kubernetes.io/serviceaccount/token` (in-cluster)
- Fallback to state file (standalone mode)
- Token sent during registration for control plane authentication

### State Manager (`pkg/state/state.go`)

Manages persistent agent state in a consolidated YAML file:

- **Single State File**: Stores agent_id, cluster_id, and cluster_token in one file
- **Multiple Locations**: Tries `/var/lib/pipeops`, `/etc/pipeops`, `tmp/`, or local directory
- **Automatic Migration**: Converts from legacy separate files on first run
- **Secure Permissions**: State file has 0600 permissions (owner read/write only)
- **Development Friendly**: Uses `tmp/agent-state.yaml` for local development (gitignored)

## Configuration

### Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `PIPEOPS_API_URL` | Control plane API URL | `https://api.pipeops.sh` | Yes |
| `PIPEOPS_TOKEN` | Authentication token | - | Yes |
| `PIPEOPS_CLUSTER_NAME` | Cluster identifier | `default-cluster` | Yes |
| `PIPEOPS_AGENT_ID` | Unique agent identifier | Auto-generated | No |
| `PIPEOPS_NODE_NAME` | Kubernetes node name | From pod fieldRef | No |
| `PIPEOPS_POD_NAME` | Pod name | From pod fieldRef | No |
| `PIPEOPS_POD_NAMESPACE` | Pod namespace | From pod fieldRef | No |
| `PIPEOPS_LOG_LEVEL` | Logging level | `info` | No |
| `PIPEOPS_PORT` | HTTP server port | `8080` | No |

### Configuration File

The agent can be configured via YAML file (`config.yaml` or `~/.pipeops-agent.yaml`):

```yaml
# Agent configuration
agent:
  id: ""  # Auto-generated from hostname if not specified
  name: "pipeops-agent"
  cluster_name: "production-cluster"
  grafana_sub_path: true  # Set to false to skip PipeOps proxy subpath configuration
  labels:
    environment: "production"
    region: "us-east-1"

# PipeOps control plane configuration
pipeops:
  api_url: "https://api.pipeops.sh"
  token: "your-cluster-token-here"
  timeout: "30s"
  reconnect:
    enabled: true
    max_attempts: 10
    interval: "5s"
    backoff: "5s"
  tls:
    enabled: true
    insecure_skip_verify: false

# Tunnel configuration (optional)
tunnel:
  enabled: true
  inactivity_timeout: "5m"
  forwards:
    - name: "kubernetes-api"
      local_addr: "localhost:6443"
      remote_port: 0  # 0 = dynamically assigned by control plane
    - name: "kubelet-metrics"
      local_addr: "localhost:10250"
      remote_port: 0
    - name: "agent-http"
      local_addr: "localhost:8080"
      remote_port: 0

# Kubernetes configuration
kubernetes:
  in_cluster: true
  namespace: "pipeops-system"
  kubeconfig: ""

# Logging configuration
logging:
  level: "info"
  format: "json"
  output: "stdout"
```

### Tunnel Configuration Explained

The `tunnel.forwards` array defines which local services are forwarded through the tunnel:

- **`name`**: Human-readable identifier for the forward
- **`local_addr`**: Local address to forward (format: `host:port`)
- **`remote_port`**: Remote port on control plane (0 = dynamic allocation)

**Common Forwards:**
1. **Kubernetes API** (`localhost:6443`) - Direct access to K8s API server
2. **Kubelet Metrics** (`localhost:10250`) - Node-level metrics and logs
3. **Agent HTTP** (`localhost:8080`) - Agent health checks and dashboard

## Quick Start

**🚀 1-Command Installation with Intelligent Detection:**

```bash
# 1) Provide your PipeOps control plane token (and optionally a friendly cluster name)
export PIPEOPS_TOKEN="your-token-here"
export CLUSTER_NAME="my-pipeops-cluster"

# 2) Run the installer straight from GitHub (auto-detects the best Kubernetes distro)
bash <(curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh)
```

The installer will:
- Analyze your system (CPU, memory, disk, OS)
- Automatically select the optimal Kubernetes distribution (k3s, minikube, k3d, or kind)
- Install the cluster
- Deploy the PipeOps agent
- Install monitoring stack (Prometheus, Loki, Grafana, OpenCost)

To pin a specific distribution just set `CLUSTER_TYPE` (e.g., `export CLUSTER_TYPE=k3s`).

**✅ Verify:**

```bash
kubectl get pods -n pipeops-system
kubectl get pods -n pipeops-monitoring
```

**💡 Agent-Only Deployment (Existing Cluster):**

```bash
export PIPEOPS_TOKEN="your-token-here"
export PIPEOPS_CLUSTER_NAME="my-existing-cluster"

curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml \
  | sed "s/PIPEOPS_TOKEN: \"your-token-here\"/PIPEOPS_TOKEN: \"${PIPEOPS_TOKEN}\"/" \
  | sed "s/token: \"your-token-here\"/token: \"${PIPEOPS_TOKEN}\"/" \
  | sed "s/cluster_name: \"default-cluster\"/cluster_name: \"${PIPEOPS_CLUSTER_NAME}\"/" \
  | sed "s/PIPEOPS_CLUSTER_NAME: \"default-cluster\"/PIPEOPS_CLUSTER_NAME: \"${PIPEOPS_CLUSTER_NAME}\"/" \
  | kubectl apply -f -

kubectl rollout status deployment/pipeops-agent -n pipeops-system
```

**🎯 Supported Cluster Types:**
- **k3s**: Lightweight Kubernetes for production (VMs, bare metal, cloud)
- **minikube**: Local development (macOS, workstations)
- **k3d**: k3s in Docker (fast, lightweight)
- **kind**: Kubernetes in Docker (CI/CD, testing)

**📖 Guides:**
- **[Intelligent Cluster Setup](docs/INTELLIGENT_CLUSTER_SETUP.md)** - Complete guide on auto-detection and cluster selection
- **[Deployment Quick Start](docs/DEPLOYMENT_QUICK_START.md)** - Manual deployment instructions
- **[Scripts README](scripts/README.md)** - Installation script details
- **[Install from GitHub Script](docs/install-from-github.md)** - Step-by-step walkthrough of the command above

**Manual Installation:**

```bash
# 1. Build and load image (Minikube example)
docker build -t pipeops-agent:latest .
minikube image load pipeops-agent:latest

# 2. Create secret
kubectl create namespace pipeops
kubectl create secret generic pipeops-agent-token \
  --from-literal=token=YOUR_AGENT_TOKEN \
  -n pipeops

# 3. Deploy
kubectl apply -f deployments/agent.yaml
```

## Deployment

### Kubernetes Deployment

Deploy the agent using the provided manifest:

```bash
# Create namespace
kubectl create namespace pipeops

# Create secret with your PipeOps token
kubectl create secret generic pipeops-agent-token \
  --from-literal=token=your-pipeops-token-here \
  -n pipeops

# Deploy the agent
kubectl apply -f deployments/agent.yaml -n pipeops

# Check status
kubectl get pods -n pipeops
kubectl logs deployment/pipeops-agent -n pipeops
```

### Helm Deployment

Deploy using the Helm chart:

```bash
# Add repository (if published)
helm repo add pipeops https://charts.pipeops.io
helm repo update

# Install with custom values
helm install pipeops-agent pipeops/pipeops-agent \
  --namespace pipeops-system \
  --create-namespace \
  --set config.pipeops.token=your-pipeops-token-here \
  --set config.agent.cluster_name=my-cluster

# Or use local chart
cd helm/pipeops-agent
helm install pipeops-agent . \
  --namespace pipeops-system \
  --create-namespace \
  --values values.yaml
```

### Required RBAC Permissions

The agent requires minimal RBAC permissions:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pipeops-agent
  namespace: pipeops-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pipeops-agent
rules:
  - apiGroups: [""]
    resources: ["pods", "nodes", "services", "namespaces"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments", "replicasets", "statefulsets"]
    verbs: ["get", "list", "watch"]
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

### Health Probes

The deployment includes health probes to ensure agent reliability:

**Liveness Probe:**
```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 60  # Allows time for registration
  periodSeconds: 30
  failureThreshold: 3
```

**Readiness Probe:**
```yaml
readinessProbe:
  httpGet:
    path: /ready
    port: 8080
  initialDelaySeconds: 15  # Allows time for startup
  periodSeconds: 10
  failureThreshold: 3
```

**Probe Timing Rationale:**
- **Liveness 60s delay**: Allows time for registration with control plane
- **Readiness 15s delay**: Allows time for HTTP server startup
- **Registration failure**: Agent exits before probes start checking, causing CrashLoopBackoff

## Security

### Network Security

- **Outbound-Only**: Agent initiates all connections; no inbound ports required
- **TLS Encryption**: All HTTP/WebSocket communication uses TLS 1.2+
- **Certificate Validation**: Strict certificate validation for control plane
- **Firewall-Friendly**: Only requires outbound HTTPS (443) and WSS connections

### Authentication & Authorization

- **Token-Based Auth**: Agent authenticates using secure API tokens
- **ServiceAccount Token**: K8s token extracted from pod ServiceAccount mount
- **RBAC Integration**: Honors Kubernetes RBAC for all cluster operations
- **Minimal Permissions**: Agent requests least-privilege ClusterRole

### Container Security

- **Non-Root User**: Runs as UID 1000 (non-privileged)
- **Read-Only Filesystem**: Root filesystem is read-only
- **No Privileged Access**: No privileged container capabilities required
- **Security Context**: Comprehensive security context configuration
- **Minimal Image**: Alpine-based image with minimal attack surface

### Registration Security

- **Mandatory Validation**: Agent exits if registration fails
- **No Fallback**: No operation without valid cluster_id from control plane
- **Error Logging**: Full error details logged for troubleshooting
- **Response Validation**: JSON response must be valid and contain required fields

## Monitoring & Observability

### Structured Logging

JSON-formatted logs with contextual fields:

```json
{
  "time": "2025-10-15T10:30:00Z",
  "level": "info",
  "msg": "Agent registered successfully with control plane",
  "agent_id": "agent-hostname-abc123",
  "cluster_id": "550e8400-e29b-41d4-a716-446655440000",
  "cluster_uuid": "550e8400-e29b-41d4-a716-446655440000",
  "name": "production-cluster",
  "status": "active",
  "workspace_id": 123,
  "has_token": true
}
```

### Metrics

The agent exposes Prometheus-compatible metrics:

- `pipeops_agent_status` - Agent operational status (0=disconnected, 1=connected)
- `pipeops_tunnel_status` - Tunnel connection status
- `pipeops_heartbeat_total` - Total heartbeats sent
- `pipeops_heartbeat_errors_total` - Failed heartbeat attempts
- `pipeops_registration_attempts_total` - Registration attempts
- `pipeops_http_requests_total` - HTTP requests by endpoint
- `pipeops_tunnel_forwards_active` - Number of active port forwards

### Dashboard

Access the built-in dashboard at `http://host.docker.internal:8080/dashboard`:

- Real-time agent status
- Tunnel connection status
- Recent heartbeat activity
- Port forward configuration
- Runtime metrics (CPU, memory, goroutines)

## Troubleshooting

### Registration Failures

**Symptom**: Pod in CrashLoopBackoff with log message "failed to register agent"

**Common Causes:**
1. Invalid `PIPEOPS_TOKEN` - Check secret configuration
2. Network connectivity - Verify outbound HTTPS access to control plane
3. Invalid cluster name - Ensure cluster doesn't already exist with different config

**Debug Steps:**
```bash
# Check agent logs
kubectl logs deployment/pipeops-agent -n pipeops-system

# Verify secret
kubectl get secret pipeops-agent-secret -n pipeops-system -o yaml

# Test network connectivity
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl -v https://api.pipeops.sh/health

# Check for JSON parse errors in logs
kubectl logs deployment/pipeops-agent -n pipeops-system | grep "JSON"
```

### Heartbeat Failures

**Symptom**: Agent connected but heartbeats failing (500 errors)

**Common Causes:**
1. cluster_id not set - Registration may have failed silently
2. Token field in heartbeat - Control plane doesn't expect it
3. Network interruption - Temporary connectivity issues

**Debug Steps:**
```bash
# Check if cluster_id is set
kubectl logs deployment/pipeops-agent -n pipeops-system | grep "cluster_id"

# Check for 500 errors
kubectl logs deployment/pipeops-agent -n pipeops-system | grep "500"

# Restart agent to re-register
kubectl rollout restart deployment/pipeops-agent -n pipeops-system
```

### Tunnel Connection Issues

**Symptom**: Tunnel status shows "disconnected" or "connecting"

**Common Causes:**
1. Tunnel disabled in configuration (`tunnel.enabled: false`)
2. Control plane not allocating ports yet
3. WebSocket connection blocked by firewall
4. Chisel server not available

**Debug Steps:**
```bash
# Check tunnel configuration
kubectl get configmap pipeops-agent-config -n pipeops-system -o yaml

# Check tunnel logs
kubectl logs deployment/pipeops-agent -n pipeops-system | grep -i tunnel

# Verify outbound WSS connectivity
kubectl run -it --rm debug --image=appropriate/curl --restart=Never -- \
  curl -v -H "Connection: Upgrade" -H "Upgrade: websocket" \
  https://tunnel.pipeops.sh/
```

### Debug Mode

Enable debug logging for more verbose output:

```bash
# Using kubectl
kubectl set env deployment/pipeops-agent PIPEOPS_LOG_LEVEL=debug -n pipeops-system

# Using Helm
helm upgrade pipeops-agent . --set config.logging.level=debug

# Check debug logs
kubectl logs -f deployment/pipeops-agent -n pipeops-system
```

## Performance Considerations

### Resource Requirements

**Recommended Resources:**
```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

**Actual Usage (typical):**
- CPU: ~20-50m (idle), ~100-200m (active tunneling)
- Memory: ~50-100Mi (steady state)
- Network: ~500B/s (heartbeat), ~1-5KB/s (active tunnel)

### Scaling Considerations

- **Single Instance**: Agent is designed to run as single instance per cluster
- **Deployment Strategy**: `Recreate` ensures only one instance runs at a time
- **No Horizontal Scaling**: Do not scale replicas >1 (multiple agents will conflict)
- **Cluster Size**: Tested with clusters up to 100 nodes
- **Connection Pooling**: WebSocket and HTTP connections reused efficiently

### Network Bandwidth

**Baseline (idle):**
- Heartbeat: ~50 bytes every 5 seconds = ~10 B/s
- Tunnel poll: ~100 bytes every 5 seconds = ~20 B/s
- Total idle: ~500 B/s

**Active (tunneling):**
- K8s API access: ~1-10 KB/s (depends on control plane activity)
- Kubelet metrics: ~500 B/s (periodic scraping)
- Agent metrics: ~100 B/s (occasional health checks)
- Total active: ~1-5 KB/s

## Development

### Quick Start

The Makefile has been refactored to support **any Kubernetes cluster** (Minikube, Kind, GKE, EKS, AKS, etc.) while maintaining Minikube-specific convenience commands.

**📚 Documentation:**
- **[Makefile Refactoring Guide](docs/MAKEFILE-REFACTOR.md)** - Complete documentation with examples
- **[Makefile Quick Reference](docs/MAKEFILE-QUICKREF.md)** - Fast lookup for common commands

#### Minikube Testing (Fastest)

```bash
# One command starts Minikube and deploys everything
make minikube-deploy-all

# View logs
make minikube-logs

# Check status
make minikube-status

# Stop when done
make minikube-stop
```

#### Any Kubernetes Cluster

```bash
# Configure kubectl for your cluster (GKE, EKS, Kind, etc.)
kubectl config use-context your-cluster

# Deploy to current context
make k8s-deploy-all

# View logs
make k8s-logs

# Check status
make k8s-status
```

#### Local Development (No Cluster)

```bash
# Generate mock token and run locally
make generate-token
make run
```

### Building from Source

```bash
# Clone repository
git clone https://github.com/PipeOpsHQ/pipeops-k8-agent.git
cd pipeops-k8-agent

# Build binary
make build

# Build Docker image
make docker

# Run tests
make test

# Run locally (requires kubeconfig)
./bin/pipeops-vm-agent --config config.example.yaml
```

### Local Development Details

```bash
# Quick start - Generate mock token and run
make generate-token
make run

# Or step by step:

# Install dependencies
go mod download

# Generate mock ServiceAccount token for local dev
make generate-token

# Build and run with local config
make build
make run

# Run with debug logging
go run cmd/agent/main.go \
  --config config.example.yaml \
  --log-level debug

# Run tests with coverage
go test -v -cover ./...

# Lint code
golangci-lint run

# Clean state to force fresh registration
make clean-state
```

**Local State Storage:**
- State files are stored in `tmp/agent-state.yaml` (gitignored)
- Contains agent_id, cluster_id, and cluster_token
- Use `make generate-token` to create mock token for testing
- Automatically created on first run
- Run `make clean-state` to reset and force fresh registration

### Available Make Commands

Run `make help` to see all available commands. Key commands:

**Local Development:**
- `make build` - Build binary
- `make run` - Run locally
- `make test` - Run tests
- `make generate-token` - Generate mock token

**Kubernetes (Any Cluster):**
- `make k8s-deploy-all` - Complete deployment
- `make k8s-logs` - View logs
- `make k8s-status` - Check status
- `make k8s-clean` - Clean resources

**Minikube (Quick Testing):**
- `make minikube-deploy-all` - Start + deploy everything
- `make minikube-logs` - View logs
- `make minikube-status` - Check status
- `make minikube-stop` - Stop cluster

See the [Quick Reference](docs/MAKEFILE-QUICKREF.md) for complete command list.

### Testing Registration

To test registration without starting a full cluster:

1. Disable tunnel in config:
   ```yaml
   tunnel:
     enabled: false
   ```

2. Run agent with valid token:
   ```bash
   export PIPEOPS_TOKEN="your-token"
   export PIPEOPS_CLUSTER_NAME="test-cluster"
   go run cmd/agent/main.go --config config.example.yaml
   ```

3. Check logs for successful registration:
   ```
   INFO Agent registered successfully with control plane
   cluster_id=550e8400-... name=test-cluster status=active
   ```

## API Endpoints Reference

### Agent HTTP API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Liveness probe (always returns 200 OK) |
| `/ready` | GET | Readiness probe (checks operational status) |
| `/version` | GET | Agent version and build information |
| `/metrics` | GET | Prometheus-compatible metrics |
| `/api/health/detailed` | GET | Comprehensive health information |
| `/api/status/features` | GET | Available features and capabilities |
| `/api/metrics/runtime` | GET | Runtime performance metrics |
| `/api/status/connectivity` | GET | Network connectivity test |
| `/ws` | GET | WebSocket upgrade for bidirectional communication |
| `/api/realtime/events` | GET | Server-Sent Events stream |
| `/api/realtime/logs` | GET | Live log streaming |
| `/dashboard` | GET | Static HTML dashboard |

### Control Plane API (called by agent)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/clusters/agent/register` | POST | Register agent and receive cluster_id |
| `/api/v1/clusters/agent/{cluster_uuid}/heartbeat` | POST | Send heartbeat to maintain connection |
| `/api/v1/tunnel/status` | GET | Poll for tunnel status and port allocations |

## FAQ

**Q: Why does the agent exit when registration fails?**  
A: The agent requires a valid `cluster_id` from the control plane to operate correctly. Without it, heartbeats and tunnel connections will fail. Exiting immediately (CrashLoopBackoff) makes the failure visible and ensures Kubernetes will retry with fresh credentials.

**Q: Can I run multiple agent instances?**  
A: No, the agent is designed to run as a single instance per cluster. Multiple instances will create conflicting tunnels and heartbeats. Use `strategy: Recreate` in your deployment.

**Q: What happens if the tunnel disconnects?**  
A: The Chisel client automatically attempts to reconnect. The poll service continues monitoring tunnel status and will recreate the tunnel if needed. Heartbeats continue independently of tunnel status.

**Q: Do I need to open inbound firewall ports?**  
A: No, the agent initiates all connections outbound. You only need to allow outbound HTTPS (443) and WSS connections to the PipeOps control plane.

**Q: How does the control plane access my Kubernetes API?**  
A: Through the Chisel tunnel. The agent forwards `localhost:6443` (K8s API) to a remote port on the control plane (e.g., 8000). The control plane accesses your K8s API via `https://tunnel.pipeops.sh:8000`.

**Q: Can I disable the tunnel and only use registration/heartbeat?**  
A: Yes, set `tunnel.enabled: false` in your config. The agent will register and send heartbeats but won't establish tunnels. This is useful for testing or environments where direct access is already available.

**Q: What's the difference between `cluster_id` and `agent_id`?**  
A: `agent_id` is the unique identifier for this agent instance (based on hostname). `cluster_id` is the UUID assigned by the control plane during registration and represents the cluster in PipeOps. The `cluster_id` is used for all subsequent API calls.

**Q: Where is the Kubernetes token stored?**  
A: The agent reads the ServiceAccount token from `/var/run/secrets/kubernetes.io/serviceaccount/token` when running in-cluster. For standalone/development mode, it stores the token in the consolidated state file (`tmp/agent-state.yaml` or `.pipeops-agent-state.yaml`).

## Documentation

### Controller API (For Agent Developers)
- **⚠️ [Controller Documentation](docs/CONTROLLER.md)** - **URGENT**: HTTP endpoints deprecated, migrate to WebSocket
- **[WebSocket API](docs/agent-websocket-api.md)** - Complete WebSocket API reference for agents
- **[Migration Guide](docs/agent-migration-guide.md)** - Step-by-step HTTP to WebSocket migration

### Installation & Setup
- **[Intelligent Cluster Setup](docs/INTELLIGENT_CLUSTER_SETUP.md)** - Auto-detection and multi-cluster support
- **[Deployment Quick Start](docs/DEPLOYMENT_QUICK_START.md)** - Complete deployment guide (Minikube, K3s, Kind, production)
- **[Installation Scripts](scripts/README.md)** - Script documentation and usage

### Architecture & Development
- **[In-Cluster Architecture](docs/IN_CLUSTER_ARCHITECTURE.md)** - How the agent runs as a pod in Kubernetes
- **[Architecture Guide](docs/ARCHITECTURE.md)** - System architecture and design
- **[Setup Guide](docs/SETUP_GUIDE.md)** - Development setup

### Monitoring & Operations
- **[Monitoring Integration](docs/MONITORING_REGISTRATION_INTEGRATION.md)** - Monitoring stack setup and registration
- **[Cluster Metrics Collection](docs/CLUSTER_METRICS_COLLECTION.md)** - How metrics are collected via REST API

### Future
- **[Next Steps](docs/NEXT_STEPS.md)** - Future enhancements

## License

See [LICENSE](LICENSE) file for details.

## Support

- **Documentation**: <https://docs.pipeops.io>
- **Community**: <https://community.pipeops.io>
- **Issues**: <https://github.com/PipeOpsHQ/pipeops-k8-agent/issues>
- **Email**: support@pipeops.io
