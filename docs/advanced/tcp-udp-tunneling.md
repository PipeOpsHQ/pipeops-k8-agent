# TCP/UDP Tunneling

This guide covers TCP/UDP tunneling for accessing services like PostgreSQL, Redis, SSH, and custom TCP/UDP applications through the PipeOps gateway.

## Overview

PipeOps TCP/UDP tunneling allows you to expose TCP and UDP services from your Kubernetes cluster to external clients, even when your cluster is behind a firewall or NAT.

### How It Works

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              User Traffic                                │
│                                                                          │
│    psql ─────────────►  gateway.pipeops.io:15432  ──── WebSocket ────►  │
│    redis-cli ────────►  gateway.pipeops.io:16379  ──── Tunnel ──────►   │
│    ssh ──────────────►  gateway.pipeops.io:12222  ──────────────────►   │
│                                                                          │
│                              PipeOps Gateway                             │
│                                   │                                      │
│                                   ▼                                      │
│                         ┌─────────────────┐                             │
│                         │  PipeOps Agent  │                             │
│                         │   (WebSocket)   │                             │
│                         └────────┬────────┘                             │
│                                  │                                       │
│              ┌───────────────────┼───────────────────┐                  │
│              ▼                   ▼                   ▼                  │
│      ┌───────────┐       ┌───────────┐       ┌───────────┐             │
│      │ PostgreSQL│       │   Redis   │       │    SSH    │             │
│      │   :5432   │       │   :6379   │       │    :22    │             │
│      └───────────┘       └───────────┘       └───────────┘             │
│                                                                          │
│                          Your Kubernetes Cluster                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Routing Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| **direct** | Public IP from LoadBalancer | Public clusters with external IPs |
| **tunnel** | WebSocket through PipeOps gateway | Private clusters, NAT, firewalls |
| **dual** | Both direct + tunnel endpoints | Redundancy, client choice |

### Protocol Options

The agent supports two tunnel protocols:

| Protocol | Description | Performance |
|----------|-------------|-------------|
| **JSON** | Base64-encoded data over WebSocket | Compatible, ~33% overhead |
| **Yamux** | Binary stream multiplexing | Optimal, zero encoding overhead |

The agent automatically negotiates yamux when the gateway supports it. Yamux provides:

- **Zero base64 overhead**: Raw bytes instead of base64-encoded JSON
- **Per-stream backpressure**: Flow control prevents memory exhaustion  
- **Lower latency**: No JSON parsing per message
- **True multiplexing**: Multiple concurrent tunnels over single connection

## Prerequisites

1. **Gateway API CRDs** - Including experimental TCPRoute/UDPRoute
2. **Istio** - With alpha Gateway API enabled
3. **PipeOps Agent** - v1.5.0+ with tunneling enabled

See [Gateway API Setup](gateway-api-setup.md) for installation instructions.

## Quick Start

### 1. Enable Tunneling in Agent Config

```yaml
# config.yaml
tunnels:
  enabled: true
  routing:
    force_tunnel_mode: false
    dual_mode_enabled: true
  discovery:
    tcp:
      enabled: true
      gateway_label: "pipeops.io/managed"
    udp:
      enabled: true
      gateway_label: "pipeops.io/managed"
```

### 2. Create a Gateway

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: tcp-gateway
  namespace: pipeops-system
  labels:
    pipeops.io/managed: "true"  # Required for discovery
spec:
  gatewayClassName: istio
  listeners:
    - name: postgres
      port: 5432
      protocol: TCP
    - name: redis
      port: 6379
      protocol: TCP
    - name: ssh
      port: 2222
      protocol: TCP
```

### 3. Create TCPRoutes

```yaml
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: postgres-route
  namespace: pipeops-system
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: postgres
  rules:
    - backendRefs:
        - name: postgres-service
          namespace: databases
          port: 5432
---
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: redis-route
  namespace: pipeops-system
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: redis
  rules:
    - backendRefs:
        - name: redis-service
          namespace: databases
          port: 6379
```

### 4. Connect to Your Service

Once the agent discovers the Gateway and routes, you'll receive tunnel endpoints:

```bash
# Connect to PostgreSQL via tunnel
psql -h gateway.pipeops.io -p 15432 -U myuser mydatabase

# Connect to Redis via tunnel
redis-cli -h gateway.pipeops.io -p 16379

# SSH via tunnel
ssh -p 12222 user@gateway.pipeops.io
```

## Configuration Reference

### Full Configuration

```yaml
tunnels:
  # Enable/disable TCP/UDP tunnel discovery
  enabled: true
  
  routing:
    # Force all services to use tunnel mode
    # Useful for security or testing
    force_tunnel_mode: false
    
    # Enable dual-mode for public clusters
    # Provides both direct IP and tunnel endpoints
    dual_mode_enabled: true

  discovery:
    tcp:
      enabled: true
      # Label selector for managed gateways
      gateway_label: "pipeops.io/managed"
    
    udp:
      enabled: true
      gateway_label: "pipeops.io/managed"

  tcp:
    # Buffer size for TCP data (bytes)
    buffer_size: 32768
    
    # Enable TCP keepalive
    keepalive: true
    keepalive_period: "30s"
    
    # Connection timeout
    connection_timeout: "30s"
    
    # Idle timeout - close after inactivity
    idle_timeout: "5m"
    
    # Max concurrent connections per service
    max_connections: 1000

  udp:
    # Buffer size for UDP packets (bytes)
    buffer_size: 65535
    
    # Session timeout for UDP
    session_timeout: "5m"
    
    # Max concurrent UDP sessions
    max_sessions: 10000
```

### Gateway Annotations

Control routing behavior per-gateway:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: my-gateway
  annotations:
    # Override auto-detected routing mode
    pipeops.io/routing-mode: "tunnel"  # tunnel, direct, or dual
    
    # Custom timeout for this gateway
    pipeops.io/idle-timeout: "10m"
```

## Common Use Cases

### PostgreSQL Access

```yaml
# Gateway listener
- name: postgres
  port: 5432
  protocol: TCP

# TCPRoute
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: postgres-route
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: postgres
  rules:
    - backendRefs:
        - name: postgres
          namespace: databases
          port: 5432
```

```bash
# Connect via tunnel
psql "host=gateway.pipeops.io port=15432 user=admin dbname=mydb sslmode=require"
```

### Redis Access

```yaml
# Gateway listener
- name: redis
  port: 6379
  protocol: TCP

# TCPRoute
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: redis-route
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: redis
  rules:
    - backendRefs:
        - name: redis-master
          namespace: cache
          port: 6379
```

```bash
# Connect via tunnel
redis-cli -h gateway.pipeops.io -p 16379 -a mypassword
```

### SSH/SFTP Access

```yaml
# Gateway listener
- name: ssh
  port: 22
  protocol: TCP

# TCPRoute to jump server
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: ssh-route
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: ssh
  rules:
    - backendRefs:
        - name: jump-server
          namespace: admin
          port: 22
```

```bash
# SSH via tunnel
ssh -p 12222 admin@gateway.pipeops.io

# SFTP via tunnel
sftp -P 12222 admin@gateway.pipeops.io
```

### MySQL Access

```yaml
# Gateway listener
- name: mysql
  port: 3306
  protocol: TCP

# TCPRoute
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: mysql-route
spec:
  parentRefs:
    - name: tcp-gateway
      sectionName: mysql
  rules:
    - backendRefs:
        - name: mysql-primary
          namespace: databases
          port: 3306
```

```bash
# Connect via tunnel
mysql -h gateway.pipeops.io -P 13306 -u admin -p
```

### UDP: DNS Service

```yaml
# Gateway listener
- name: dns
  port: 53
  protocol: UDP

# UDPRoute
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: UDPRoute
metadata:
  name: dns-route
spec:
  parentRefs:
    - name: udp-gateway
      sectionName: dns
  rules:
    - backendRefs:
        - name: coredns
          namespace: kube-system
          port: 53
```

```bash
# Query via tunnel
dig @gateway.pipeops.io -p 10053 example.com
```

## Monitoring

### Prometheus Metrics

The agent exposes metrics for tunnel connections:

```promql
# Active TCP connections
pipeops_agent_tcp_tunnel_active_connections

# Total TCP connections
pipeops_agent_tcp_tunnel_connections_total

# TCP bytes transferred
pipeops_agent_tcp_tunnel_bytes_total{direction="sent"}
pipeops_agent_tcp_tunnel_bytes_total{direction="received"}

# TCP connection duration
pipeops_agent_tcp_tunnel_connection_duration_seconds

# TCP errors
pipeops_agent_tcp_tunnel_errors_total

# Active UDP sessions
pipeops_agent_udp_tunnel_active_sessions

# UDP packets
pipeops_agent_udp_tunnel_packets_total

# Gateway watcher metrics
pipeops_agent_gateway_watcher_discovered_services
pipeops_agent_gateway_watcher_sync_total
```

### Grafana Dashboard

Import the PipeOps tunnel dashboard (ID: coming soon) for visualization.

## Troubleshooting

### Tunnel Not Connecting

1. **Check agent logs:**
   ```bash
   kubectl logs -n pipeops-system deployment/pipeops-agent | grep -i tunnel
   ```

2. **Verify Gateway discovery:**
   ```bash
   kubectl get gateway -l pipeops.io/managed=true -A
   ```

3. **Check TCPRoute status:**
   ```bash
   kubectl describe tcproute -A
   ```

### Connection Timeouts

1. **Increase idle timeout:**
   ```yaml
   tunnels:
     tcp:
       idle_timeout: "10m"
   ```

2. **Enable keepalive:**
   ```yaml
   tunnels:
     tcp:
       keepalive: true
       keepalive_period: "30s"
   ```

### UDP Packets Lost

1. **Increase buffer size:**
   ```yaml
   tunnels:
     udp:
       buffer_size: 65535
   ```

2. **Check session timeout:**
   ```yaml
   tunnels:
     udp:
       session_timeout: "10m"
   ```

### Gateway Not Discovered

1. **Ensure label is set:**
   ```bash
   kubectl label gateway my-gateway pipeops.io/managed=true -n pipeops-system
   ```

2. **Check discovery config:**
   ```yaml
   tunnels:
     discovery:
       tcp:
         enabled: true
         gateway_label: "pipeops.io/managed"
   ```

## Security Considerations

### Network Policies

Restrict access to backend services:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-from-gateway
  namespace: databases
spec:
  podSelector:
    matchLabels:
      app: postgres
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: pipeops-system
      ports:
        - protocol: TCP
          port: 5432
```

### TLS Termination

For encrypted connections, configure TLS at the service level (e.g., PostgreSQL SSL, Redis TLS).

### Authentication

Always use strong authentication for exposed services:
- PostgreSQL: `md5` or `scram-sha-256` authentication
- Redis: `requirepass` or ACL
- SSH: Key-based authentication only

## Limitations

- **UDP reliability**: UDP is connectionless; packet loss is possible
- **Maximum connections**: Default 1000 concurrent TCP connections per service
- **Latency**: Tunnel adds ~5-20ms latency depending on network conditions
- **Bandwidth**: WebSocket overhead ~2-5% for TCP traffic

## Next Steps

- [Gateway API Setup](gateway-api-setup.md) - Detailed Istio + Gateway API installation
- [Monitoring](monitoring.md) - Set up Prometheus and Grafana
- [Configuration Reference](../getting-started/configuration.md) - Full agent configuration

## References

- [Kubernetes Gateway API - TCPRoute](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1alpha2.TCPRoute)
- [Kubernetes Gateway API - UDPRoute](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1alpha2.UDPRoute)
- [Istio TCP Traffic Management](https://istio.io/latest/docs/tasks/traffic-management/tcp-traffic-shifting/)
