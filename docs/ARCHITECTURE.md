# PipeOps VM Agent Architecture

The PipeOps VM agent maintains a secure, persistent bridge between the PipeOps control plane and a Kubernetes cluster. It replaces legacy tunneling utilities with a purpose-built WebSocket proxy, optional reverse tunnels, and automated observability tooling so operators retain auditability and RBAC enforcement.

## High-Level Workflow

```text
                                 PipeOps Control Plane
┌────────────────────────────────────────────────────────────────────────┐
│ ┌──────────────┐   ┌────────────────────┐   ┌───────────────────────┐ │
│ │ API /        │   │ Command Dispatcher │   │ Tunnel Broker         │ │
│ │ Gateway      │   │ (proxy + telemetry)│   │ (reverse tunnels)     │ │
│ └──────┬───────┘   └──────────┬─────────┘   └─────────────┬────────┘ │
│        │                      │                          │          │
└────────┼──────────────────────┼───────────────────────────┼──────────┘
         │ wss:// (register, heartbeats, proxy requests)     │
         │                                                   │
         ▼                                                   │
            PipeOps VM Agent (cluster)
┌────────────────────────────────────────────────────────────────────────┐
│ ┌────────────────────────────────────────────────────────────────────┐ │
│ │ Agent Runtime (internal/agent)                                     │ │
│ │ ┌────────────┐   ┌──────────────┐   ┌──────────────┐              │ │
│ │ │ Heartbeat  │   │ Proxy Exec   │   │ Monitoring   │              │ │
│ │ │ + Telemetry│   │ (pkg/k8s)    │   │ Manager      │              │ │
│ │ └─────┬──────┘   └──────┬───────┘   └──────┬───────┘              │ │
│ │       │                 │                  │                      │ │
│ │ ┌─────┴──────┐          │                  │   ┌────────────────┐ │ │
│ │ │ State      │◄─────────┴──────────────────┼──►│ Tunnel Manager │ │ │
│ │ │ Manager    │   persisted IDs/tokens      │   │ (internal/tunnel)│ │ │
│ │ └────────────┘                             │   └────────────────┘ │ │
│ └────────────────────────────────────────────┴──────────────────────┘ │
│                      |                                               │
│                      | REST (client-go, ServiceAccount)              │
│                      ▼                                               │
│            Kubernetes Cluster Components                             │
│ ┌───────────────┐  ┌───────────┐  ┌─────────┐  ┌────────┐  ┌───────┐ │
│ │ API Server    │  │ Prometheus│  │ Grafana │  │ Loki   │  │ OpenCost│ │
│ └───────┬───────┘  └────┬──────┘  └────┬────┘  └──┬─────┘  └──┬────┘ │
│         │               │              │          │           │      │
│         └───────────────┴──────────────┴──────────┴───────────┴──────│
│                      Cluster Nodes / Workloads                        │
└────────────────────────────────────────────────────────────────────────┘

Legend
  • Solid arrows represent active data flow.
  • Reverse tunnels are outbound-only and terminate at the control-plane tunnel broker.
```

1. The agent loads persisted identity from the state store and connects to the control plane via `wss://` using a scoped bearer token.
2. The control plane validates or assigns the cluster ID, sets the heartbeat cadence, and pushes initial configuration (proxy policies, monitoring preferences, tunnel forwards).
3. The agent streams heartbeats, telemetry, and executes proxy requests issued by the control plane against the local Kubernetes API.
4. Optional monitoring components and reverse tunnels are provisioned dynamically; their endpoints become reachable through PipeOps gateway routes without exposing inbound network ports.

### Message Sequence Overview

```text
Control Plane                  Agent                           Kubernetes API
      │                         │                                       │
      │ --- RegisterCluster --->│                                       │
      │<--- ClusterConfig ------│                                       │
      │                         │--- REST call via client-go ---------->│
      │<-- ProxyResponse -------│<-- JSON/protobuf payload -------------│
      │                         │                                       │
```

### Proxy Message Shape (Simplified)

```json
{
  "request_id": "ac531f8a-13ad-4cb9-9bf5-cec8c7d3164f",
  "method": "GET",
  "path": "/api/v1/namespaces/default/pods",
  "query": "labelSelector=app%3Dweb",
  "headers": {
    "Accept": ["application/json"],
    "User-Agent": ["pipeops-control-plane"]
  },
  "body": "",
  "encoding": "base64"
}
```

## Component Responsibilities

| Subsystem | Location | Purpose |
|-----------|----------|---------|
| Agent runtime | `internal/agent` | Bootstraps services, manages registration, orchestrates monitoring, handles graceful shutdown. |
| Control plane client | `internal/controlplane` | Owns the WebSocket session, heartbeat cadence, and proxy serialization. |
| Gateway proxy | `internal/gateway` | Monitors ingress resources, registers routes with controller, detects cluster type (private/public). |
| Reverse tunnel manager | `internal/tunnel` | Maintains optional outbound tunnels, multiplexes forwards, enforces idle timeouts. |
| Monitoring stack manager | `internal/monitoring` | Applies Helm releases, performs health checks, registers forwards for Grafana, Prometheus, Loki, OpenCost. |
| HTTP/SSE/WebSocket server | `internal/server` | Exposes local diagnostics (`/health`, `/ready`, `/version`), metrics, and dashboards. |
| Kubernetes helpers | `pkg/k8s` | Wraps client-go interactions, request execution, token helpers. |
| State manager | `pkg/state` | Persists agent ID, cluster ID, and ServiceAccount material across restarts. |

## Control Plane Integration

### Registration Lifecycle

```text
Agent                              Control Plane
  │                                      │
  │  Connect wss:// --------------------▶│
  │  RegisterCluster{metadata…} --------▶│
  │                                      │
  │◀------------- ClusterConfig ---------│ (cluster_id, heartbeat interval, feature flags)
  │◀------------- Secrets ---------------│ (optional ServiceAccount refresh)
  │  Persist state
```

### Heartbeats and Telemetry

Every 30 seconds (configurable) the agent:

1. Collects runtime metrics (node count, pod count, tunnel status, version info).
2. Sends a `Heartbeat` payload over the WebSocket.
3. Processes control messages queued by the control plane (proxy requests, tunnel updates, monitoring commands).

### Proxy Execution

1. Control plane emits a `ProxyRequest` JSON message.
2. The agent executes the HTTP call through client-go using its ServiceAccount and RBAC privileges.
3. Responses are normalized (hop-by-hop headers pruned, bodies optionally base64 encoded) and returned via `ProxyResponse`.
4. Failures produce a `ProxyError` with context so the UI can guide remediation.

### Reverse Tunnel Activation (Optional)

```text
Control Plane                       Agent
      │                                │
      │  TunnelPlan{forwards[]} ------▶│
      │                                │
      │◀-------- TunnelStatus ---------│ (ready, remote ports)
      │                                │
      │  Uses remote ports via gateway │
```

Reverse tunnels run outbound-only; the control plane never opens inbound sockets. Idle sessions close automatically based on `tunnel.inactivity_timeout`.

## Monitoring Stack Automation

When enabled, the agent deploys a curated observability bundle via Helm:

- Prometheus and Alertmanager for metrics scraping and alerting.
- Loki for log aggregation.
- Grafana with sub-path rewrites tailored to the PipeOps proxy routes.
- OpenCost for cost analytics.

The agent blocks until each release reports Ready, then registers the relevant forwards so the UI can reach them under `/api/v1/clusters/agent/<cluster>/proxy/...`.

## Communication Channels

| Channel | Purpose | Authentication | Encryption |
|---------|---------|----------------|------------|
| WebSocket (`wss`) | Registration, heartbeats, proxy traffic, control messages | Scoped bearer token | TLS 1.2+ |
| HTTPS (local) | Diagnostics endpoints, metrics, dashboards | Optional (cluster-local) | TLS when fronted by ingress or service mesh |
| Reverse tunnel | Optional TCP access to observability tooling | Control plane handshake | TLS between agent and tunnel endpoint |

## Configuration Essentials

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PIPEOPS_API_URL` | Control plane API base URL | `https://api.pipeops.sh` |
| `PIPEOPS_TOKEN` | Scoped bearer token for WebSocket authentication | Required |
| `PIPEOPS_CLUSTER_NAME` | Friendly name reported during registration | `default-cluster` |
| `PIPEOPS_AGENT_ID` | Override for the auto-generated agent ID | Auto-generated |
| `PIPEOPS_LOG_LEVEL` | Logging verbosity (`debug`, `info`, `warn`, `error`) | `info` |
| `PIPEOPS_PORT` | Local HTTP server port | `8080` |
| `PIPEOPS_ENABLE_SSE` | Enable server-sent events dashboard endpoints | `true` |
| `PIPEOPS_ENABLE_WEBSOCKET` | Enable the local WebSocket dashboard | `true` |

### Example Agent Configuration

```yaml
agent:
  id: ""
  name: "production-cluster"
  port: 8080
  grafana_sub_path: true
pipeops:
  api_url: "https://api.pipeops.sh"
  token: "${PIPEOPS_TOKEN}"
  timeout: 30s
  reconnect:
    enabled: true
    max_attempts: 5
    interval: 5s
  tls:
    enabled: true
    insecure_skip_verify: false
kubernetes:
  in_cluster: true
  namespace: pipeops-system
logging:
  level: info
  format: json
tunnel:
  enabled: true
  poll_interval: 5s
  inactivity_timeout: 5m
  forwards:
    - name: kubernetes-api
      local_addr: localhost:6443
    - name: agent-http
      local_addr: localhost:8080
    - name: grafana
      local_addr: localhost:3000
```

## Gateway Proxy Architecture

The agent includes gateway proxy functionality for private clusters without public LoadBalancer IPs.

### Cluster Type Detection

On startup, the agent automatically detects the cluster type:

```text
Agent Startup
      │
      ├──> Check ingress-nginx LoadBalancer service
      │
      ├──> Has external IP? ───> Public cluster (direct routing)
      │
      └──> No external IP? ───> Private cluster (tunnel routing)
```

### Route Discovery and Registration

For all clusters (private or public), the agent:

1. **Watches ingress resources** using Kubernetes informers
2. **Extracts route information** (host, path, service, port, TLS)
3. **Registers routes** with controller via REST API

```text
Ingress Created
      │
      ▼
Agent detects change
      │
      ▼
Extract: {host, path, service, port, TLS}
      │
      ▼
POST /api/v1/gateway/routes/register
      │
      ▼
Controller stores in route registry
```

### Routing Modes

**Direct Routing (Public Clusters):**
- Controller routes directly to LoadBalancer IP
- 3-5x faster than tunnel mode
- No WebSocket tunnel overhead on data plane

**Tunnel Routing (Private Clusters):**
- Controller proxies via WebSocket tunnel
- Works in private networks
- No inbound firewall rules needed

### Gateway Components

| Component | File | Purpose |
|-----------|------|---------|
| IngressWatcher | `internal/gateway/watcher.go` | Monitors ingress resources, extracts routes |
| ControllerClient | `internal/gateway/client.go` | HTTP client for controller gateway API |
| Cluster Detection | `internal/gateway/watcher.go` | Detects LoadBalancer IP and routing mode |

### API Integration

The agent calls these controller endpoints:

```text
POST /api/v1/gateway/routes/register    # Single route
POST /api/v1/gateway/routes/sync        # Bulk sync on startup
POST /api/v1/gateway/routes/unregister  # Route deletion
```

All authenticated with:
```text
Authorization: Bearer <agent-token>
```

For detailed gateway proxy documentation, see [Gateway Proxy Guide](advanced/gateway-proxy.md).

## Security Posture

- **TLS enforced:** Control plane traffic uses TLS 1.2+ with optional certificate pinning via the TLS configuration block.
- **Scoped tokens:** The bearer token is short-lived and never written to disk; refresh flows run through the WebSocket channel.
- **RBAC compliance:** Kubernetes operations execute under the agent ServiceAccount and respect cluster RBAC policies.
- **Hardened runtime:** Containers run as non-root, use read-only filesystems, and drop unnecessary Linux capabilities.
- **State protection:** Persistent identity is stored in the ConfigMap-backed state manager with filesystem fallback encryption when available.

## Performance Profile

- Heartbeat traffic averages ~500 bytes per second; proxy throughput scales with Kubernetes payload sizes.
- WebSocket handling is validated beyond 2,000 proxy messages per second with sub-150 ms latency on typical cloud links.
- Reverse tunnels consume ~1.5 MB per minute when streaming Grafana dashboards continuously and near zero when idle.
- Default resource requests/limits: 100m CPU / 128 MiB memory requests, 500m CPU / 512 MiB limits.

## Troubleshooting Checklist

- **Cannot register:** Verify `PIPEOPS_TOKEN`, ensure outbound TLS to `PIPEOPS_API_URL`, and check logs for certificate pinning warnings when using custom CAs.
- **Proxy failures:** Inspect `ProxyError` entries; they commonly indicate missing RBAC permissions or malformed API paths.
- **Monitoring stalls:** The agent waits for Helm releases to reach Ready. Inspect pods with `kubectl get pods -n pipeops-observability` for failures.
- **Idle tunnel closure:** Bump `tunnel.inactivity_timeout` if dashboards disconnect during quiet periods.
