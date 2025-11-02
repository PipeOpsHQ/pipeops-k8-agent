# PipeOps Kubernetes Agent

A lightweight Kubernetes agent that enables secure management and gateway proxy access for your Kubernetes clusters. Supports both private and public clusters with automatic detection and optimized routing.

## What It Does

The PipeOps agent is deployed **as a pod inside your Kubernetes cluster** and establishes an outbound connection to the PipeOps control plane. It provides:

1. **Secure Cluster Access**: WebSocket tunnel for secure API access without inbound firewall rules
2. **Gateway Proxy**: Automatic ingress route management for private clusters without public LoadBalancer IPs
3. **Real-time Monitoring**: Integrated Prometheus, Loki, Grafana, and OpenCost stack
4. **Cluster Management**: Heartbeat, health checks, and real-time status reporting

**Key Features:**
- **Cloud-Native Design**: Runs as a pod inside your Kubernetes cluster
- **In-Cluster Access**: Uses Kubernetes REST API directly (no kubectl dependencies)
- **ServiceAccount Authentication**: Native Kubernetes authentication via mounted tokens
- **Outbound-Only Connections**: No inbound ports required on your infrastructure
- **WebSocket Tunneling**: Encrypted bidirectional communication for cluster API access
- **Gateway Proxy**: Automatic ingress route discovery and registration (private clusters)
- **Dual Routing Modes**: Direct routing for public clusters, tunnel for private clusters
- **Secure by Default**: All connections encrypted with TLS
- **Mandatory Registration**: Strict validation prevents operation with invalid credentials
- **Real-time Heartbeat**: Regular interval with cluster metrics (node/pod counts)
- **Integrated Monitoring**: Prometheus, Loki, Grafana, and OpenCost

## PipeOps Gateway Proxy (Optional - Disabled by Default)

**IMPORTANT**: The PipeOps Gateway Proxy is **DISABLED by default** for security. The agent provides secure admin access only unless you explicitly enable external exposure.

When enabled, the PipeOps Gateway Proxy provides:

- **Opt-in Feature**: Must be explicitly enabled via `agent.enable_ingress_sync: true`
- **Automatic Detection**: Identifies cluster type (private vs public) on startup  
- **Ingress Watching**: Monitors all ingress resources across namespaces when enabled
- **Route Registration**: Registers routes with controller via REST API
- **Custom Domains**: Support for custom domain mapping
- **TLS Termination**: Secure HTTPS access at gateway level

**Routing Modes:**
- **Direct Mode**: Public clusters with LoadBalancer (3-5x faster, no tunnel overhead)
- **Tunnel Mode**: Private clusters without public IPs (secure WebSocket tunnel)

**To enable PipeOps Gateway Proxy:**
```yaml
agent:
  enable_ingress_sync: true  # Default: false
```

See [PipeOps Gateway Proxy Documentation](docs/advanced/pipeops-gateway-proxy.md) for details.

## In-Cluster Architecture

The agent is designed to run **as a pod inside your Kubernetes cluster**:

- **No kubectl**: Uses Kubernetes REST API directly
- **No external binaries**: Pure Go implementation
- **ServiceAccount authentication**: Automatic token mounting
- **Works everywhere**: K3s, Minikube, Kind, EKS, GKE, AKS
- **Cloud-native**: Native Kubernetes deployment patterns

See [In-Cluster Architecture Documentation](docs/IN_CLUSTER_ARCHITECTURE.md) for details.

## Architecture Overview

```text
┌──────────────────────────────────────────────────────────────────┐
│                    PipeOps Control Plane                         │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐     │
│  │   Dashboard  │  │  API Gateway │  │  Gateway Proxy    │     │
│  │              │  │              │  │  (Route Registry) │     │
│  └──────────────┘  └──────────────┘  └───────────────────┘     │
└──────────────────────────────────────────────────────────────────┘
                           │                      │
                           │ HTTPS API            │ WebSocket Tunnel
                           │ (Registration,       │ (Proxy Requests)
                           │  Heartbeat,          │
                           │  Route Updates)      │
                           ▼                      ▼
┌──────────────────────────────────────────────────────────────────┐
│                      Your Infrastructure                         │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │                    Kubernetes Cluster                     │  │
│  │  ┌────────────────────────────────────────────────────┐   │  │
│  │  │           PipeOps Agent (Pod)                      │   │  │
│  │  │                                                     │   │  │
│  │  │  ┌──────────────┐      ┌──────────────────────┐   │   │  │
│  │  │  │ HTTP Server  │      │  Gateway Watcher     │   │   │  │
│  │  │  │ (Port 8080)  │      │                      │   │   │  │
│  │  │  │              │      │  • Ingress Monitor   │   │   │  │
│  │  │  │ • /health    │      │  • Route Extraction  │   │   │  │
│  │  │  │ • /ready     │      │  • Controller API    │   │   │  │
│  │  │  │ • /metrics   │      │                      │   │   │  │
│  │  │  │ • /dashboard │      │  Routes:             │   │   │  │
│  │  │  └──────────────┘      │  • Host/Path Rules   │   │   │  │
│  │  │                        │  • Service Mapping   │   │   │  │
│  │  │                        │  • TLS Configuration │   │   │  │
│  │  │                        └──────────────────────┘   │   │  │
│  │  └────────────────────────────────────────────────────┘   │  │
│  │                                                            │  │
│  │  ┌──────────────┐  ┌──────────────┐  ┌───────────────┐   │  │
│  │  │  K8s API     │  │   Ingress    │  │   Workloads   │   │  │
│  │  │  (Port 6443) │  │ (Port 80/443)│  │ (Deployments) │   │  │
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

### 3. WebSocket Tunnel (Optional)

If tunneling is enabled, the agent establishes a WebSocket connection for bidirectional communication:

```text
Agent                              Control Plane
  |                                      |
  |--- WebSocket Connect --------------->|
  |    wss://api.pipeops.io/tunnel       |
  |                                      |
  |<=== Tunnel Established =============>|
  |                                      |
  |<-- Proxy Request --------------------|
  |    {                                 |
  |      request_id: "...",              |
  |      method: "GET",                  |
  |      path: "/api/v1/pods",           |
  |      headers: {...}                  |
  |    }                                 |
  |                                      |
  |--- Forward to K8s API --------------->
  |                                      |
  |<-- K8s Response ---------------------|
  |                                      |
  |--- Proxy Response ------------------>|
  |    {                                 |
  |      request_id: "...",              |
  |      status_code: 200,               |
  |      body: {...}                     |
  |    }                                 |
```

**Tunnel Details:**
- Persistent WebSocket connection for real-time bidirectional communication
- Handles proxy requests from control plane to cluster
- Automatic reconnection on disconnect
- Heartbeat keepalive every 30 seconds

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

Manages WebSocket tunnel lifecycle for bidirectional communication:

**Hub (`hub.go`):**
- Maintains WebSocket connection to control plane
- Routes proxy requests to appropriate handlers
- Manages connection health and reconnection
- Handles concurrent proxy requests

**Manager (`manager.go`):**
- Coordinates WebSocket connection lifecycle
- Tracks tunnel activity and status
- Graceful shutdown on SIGTERM
- Automatic reconnection on disconnect

### 4. Gateway Proxy (`internal/gateway/`)

Manages ingress route discovery and registration for private clusters:

**IngressWatcher (`watcher.go`):**
- Monitors all ingress resources using Kubernetes informers
- Extracts route information (host, path, service, port, TLS)
- Detects cluster type (private vs public)
- Handles ingress add/update/delete events

**ControllerClient (`client.go`):**
- HTTP client for controller gateway API
- Registers routes via REST API endpoints
- Bulk sync on startup for efficiency
- Individual updates on ingress changes

**Cluster Detection:**
- Automatically detects LoadBalancer external IP
- Determines routing mode (direct vs tunnel)
- Waits up to 2 minutes for IP assignment

### 5. HTTP Server (`internal/server/`)

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

**Agent Gateway Configuration (Optional TCP/UDP Networking):**

The agent can optionally create Kubernetes networking resources for TCP/UDP port exposure.

| Variable | Description | Default |
|----------|-------------|---------|
| `GATEWAY_ENABLED` | Enable gateway networking resources | `false` |
| `GATEWAY_ENVIRONMENT_MODE` | `managed` or `single-vm` | `managed` |
| `GATEWAY_ENVIRONMENT_VM_IP` | Node/VM IP when `single-vm` | `` |
| `GATEWAY_ISTIO_ENABLED` | Create Istio Gateway/VirtualService | `false` |
| `GATEWAY_ISTIO_SERVICE_CREATE` | Create LB Service for Istio ingress | `false` |
| `GATEWAY_ISTIO_SERVICE_NAMESPACE` | Namespace of Istio ingress | `istio-system` |
| `GATEWAY_GATEWAY_API_ENABLED` | Create K8s Gateway API resources | `false` |
| `GATEWAY_GATEWAY_API_GATEWAY_CLASS` | GatewayClass name | `istio` |

Advanced (JSON) env/flags for inline definitions:

- `GATEWAY_GWAPI_LISTENERS_JSON` / `--gateway-gwapi-listeners-json`
  - Example: `[{"name":"tcp-runner","port":5000,"protocol":"TCP"},{"name":"udp-runner","port":6000,"protocol":"UDP"}]`
- `GATEWAY_GWAPI_TCP_ROUTES_JSON` / `--gateway-gwapi-tcp-routes-json`
  - Example: `[{"name":"runner-tcp","sectionName":"tcp-runner","backendRefs":[{"name":"runner","namespace":"pipeops-system","port":5000}]}]`
- `GATEWAY_GWAPI_UDP_ROUTES_JSON` / `--gateway-gwapi-udp-routes-json`
  - Example: `[{"name":"runner-udp","sectionName":"udp-runner","backendRefs":[{"name":"runner","namespace":"pipeops-system","port":6000}]}]`
- `GATEWAY_ISTIO_SERVERS_JSON` / `--gateway-istio-servers-json`
  - Example: `[{"port":{"number":5000,"name":"tcp-runner","protocol":"TCP"}}]`
- `GATEWAY_ISTIO_TCP_ROUTES_JSON` / `--gateway-istio-tcp-routes-json`
  - Example: `[{"name":"runner","port":5000,"destination":{"host":"runner.pipeops-system.svc.cluster.local","port":5000}}]`
- `GATEWAY_ISTIO_TLS_ROUTES_JSON` / `--gateway-istio-tls-routes-json`
  - Example: `[{"name":"app","port":443,"sniHosts":["app.example.com"],"destination":{"host":"app.default.svc.cluster.local","port":443}}]`

Validation and defaults
- At least one Gateway API listener is required when `gateway_api.enabled=true`.
- Listener protocol defaults to `TCP` when omitted; listener name auto-generates as `<proto>-<port>` when omitted.
- TCPRoute must reference a TCP listener via `sectionName`; UDPRoute must reference a UDP listener.
- Istio servers default protocol to `TCP` when omitted. TLS routes require `sniHosts`.

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

# Gateway configuration (optional networking infrastructure)
  # Enables TCP/UDP port exposure via Istio or Kubernetes Gateway API
  gateway:
    enabled: false
      environment:
      mode: managed  # managed | single-vm
      vmIP: ""       # required when single-vm
    istio:
    enabled: false
    service:
      create: false
      namespace: istio-system
    gateway:
      selector:
        istio: ingressgateway
      servers: []
      # - port:
      #     number: 6379
      #     name: tcp-redis
      #     protocol: TCP
    virtual_service:
      tcp_routes: []
      # - name: redis
      #   port: 6379
      #   destination:
      #     host: redis.default.svc.cluster.local
      #     port: 6379
      tls_routes: []
  gateway_api:
    enabled: false
    gateway_class: istio
    listeners: []
    # - name: tcp-redis
    #   port: 6379
    #   protocol: TCP
    tcp_routes: []
    # - name: redis
    #   section_name: tcp-redis
    #   backend_refs:
    #     - name: redis
    #       namespace: default
    #       port: 6379
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
# This automatically installs: Kubernetes + Gateway API + Istio + PipeOps Agent
curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

**What Gets Installed:**
- Kubernetes cluster (k3s/minikube/k3d/kind based on your environment)
- **Gateway API experimental CRDs** (for TCP/UDP routing)
- **Istio with alpha Gateway API support** (gateway controller)
- PipeOps Agent
- Monitoring stack (optional: set `INSTALL_MONITORING=true`)

**Skip Gateway API (if not needed):**
```bash
export INSTALL_GATEWAY_API=false  # Skip Gateway API/Istio installation
export PIPEOPS_TOKEN="your-token"
curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

**Gateway Networking Configuration:**

Gateway API is installed by default and enabled in the agent configuration. To use it, enable the gateway feature:

```bash
# Enable via CLI flags
./bin/pipeops-vm-agent \
  --config config.example.yaml \
  --gateway-enabled=true \
  --gateway-gwapi-enabled=true \
  --gateway-gwapi-gateway-class=istio \
  --gateway-env-mode=single-vm \
  --gateway-env-vm-ip=192.0.2.10

# Or with inline JSON
./bin/pipeops-vm-agent \
  --gateway-enabled=true \
  --gateway-gwapi-enabled=true \
  --gateway-gwapi-gateway-class=istio \
  --gateway-gwapi-listeners-json='[{"name":"tcp-runner","port":5000,"protocol":"TCP"}]' \
  --gateway-gwapi-tcp-routes-json='[{"name":"runner","sectionName":"tcp-runner","backendRefs":[{"name":"runner","namespace":"pipeops-system","port":5000}]}]'

# Or enable in Helm values
helm upgrade --install pipeops-agent ./helm/pipeops-agent \
  --set agent.gateway.enabled=true \
  --set agent.gateway.gatewayApi.enabled=true \
  --set agent.gateway.environment.mode=single-vm \
  --set agent.gateway.environment.vmIP=192.0.2.10
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

curl -fsSL https://get.pipeops.dev/k8-agent.yaml \
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
helm install pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent
helm repo update

# Install with custom values
--namespace pipeops-system \
  --create-namespace \
  --set agent.pipeops.token=your-pipeops-token-here \
  --set agent.cluster.name=my-cluster

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
2. WebSocket connection blocked by firewall
3. Control plane WebSocket server unavailable
4. Authentication failure

**Debug Steps:**
```bash
# Check tunnel configuration
kubectl get configmap pipeops-agent-config -n pipeops-system -o yaml

# Check tunnel logs
kubectl logs deployment/pipeops-agent -n pipeops-system | grep -i tunnel

# Verify outbound WSS connectivity
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl -v -H "Connection: Upgrade" -H "Upgrade: websocket" \
  https://api.pipeops.io/tunnel
```

### Gateway Proxy Issues

**Symptom**: Ingresses not accessible externally

**Common Causes:**
1. Cluster detected as public but should be private
2. Routes not registered with controller
3. DNS not configured correctly

**Debug Steps:**
```bash
# Check if gateway proxy is running
kubectl logs deployment/pipeops-agent -n pipeops-system | grep -i gateway

# Verify cluster detection
kubectl logs deployment/pipeops-agent -n pipeops-system | grep "cluster detected"

# Check registered routes
kubectl logs deployment/pipeops-agent -n pipeops-system | grep "Registering route"

# Verify ingress exists
kubectl get ingress -A
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

See the [detailed FAQ](docs/FAQ.md) for comprehensive answers to common questions.

**Quick answers:**

- **Ingress sync disabled by default?** Yes, for security. Enable with `agent.enable_ingress_sync: true`
- **Multiple agent instances?** No, use single instance per cluster with `Recreate` strategy
- **Inbound firewall ports needed?** No, agent uses outbound connections only
- **Tunnel disconnects?** Automatic reconnection with exponential backoff

## License

See [LICENSE](LICENSE) file for details.

## Support

- **Documentation**: <https://docs.pipeops.io>
- **Community**: <https://community.pipeops.io>
- **Issues**: <https://github.com/PipeOpsHQ/pipeops-k8-agent/issues>
- **Email**: support@pipeops.io
