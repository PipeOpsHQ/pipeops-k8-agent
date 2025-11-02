# PipeOps VM Agent - Frequently Asked Questions

!!! warning "Important Security Update"
    As of version 1.x, **ingress sync is DISABLED by default** for security. The agent will NOT expose your cluster externally unless you explicitly enable it with `agent.enable_ingress_sync: true`.

## Gateway Proxy & Ingress Sync

### Q: Is ingress sync enabled by default?

**NO.** For security reasons, ingress sync is **DISABLED by default** (as of v1.x).

The agent will NOT expose your cluster externally unless you explicitly enable it with:

```yaml
agent:
  enable_ingress_sync: true
```

When **disabled**, you'll see:
```
{"level":"info","msg":"Ingress sync disabled - agent will not expose cluster via gateway proxy"}
```

When **enabled**, the agent automatically:

1. Detects if the cluster is private (no public LoadBalancer on ingress-nginx)
2. Starts the ingress watcher
3. Syncs all existing ingresses to the control plane
4. Watches for new/updated/deleted ingresses and syncs them in real-time

You'll see:
```
{"level":"info","msg":"Ingress sync enabled - monitoring ingresses for gateway proxy"}
{"level":"info","msg":"Initializing gateway proxy detection..."}
{"level":"info","msg":"🔒 Private cluster detected - using tunnel routing"}
{"level":"info","msg":"Starting ingress watcher for gateway proxy"}
{"cluster_uuid":"...","ingress_count":4,"msg":"Syncing ingresses with controller"}
```

### Q: How does the agent determine if it should enable gateway proxy?

The agent checks the `ingress-nginx-controller` service:

- **LoadBalancer with External IP** → Public cluster → Gateway proxy DISABLED (direct routing)
- **NodePort or no External IP** → Private cluster → Gateway proxy ENABLED (tunnel routing)

### Q: What happens after ingress sync?

Routes are registered with the control plane:
- **Private clusters**: Traffic routes through WebSocket tunnel to agent
- **Public clusters**: Traffic routes directly to LoadBalancer IP (3-5x faster)

The agent sends:
- `routing_mode`: "tunnel" or "direct"
- `public_endpoint`: LoadBalancer IP (if available) or empty
- `cluster_uuid`: Cluster identifier
- Ingress rules (host, path, service, port, TLS, annotations)

---

## Agent Health & Heartbeat

### Q: Why don't I see heartbeat/ping logs?

**Heartbeats ARE running** - they just log at different levels:

- **Success**: DEBUG level (not visible in INFO logs)
- **Failure**: WARN/ERROR level (visible)

You'll see heartbeat failures like:
```
{"error":"failed to send heartbeat: WebSocket not connected","level":"warning","msg":"Heartbeat failed, retrying with backoff..."}
```

But successful heartbeats are silent at INFO level.

### Q: How can I confirm the agent is healthy?

Check for these INFO-level logs:

1. **Registration:**
   ```
   {"msg":"Agent registered successfully via WebSocket","status":"re-registered"}
   {"msg":"✓ Cluster registered successfully"}
   ```

2. **Connection state:**
   ```
   {"msg":"Connection state changed","new_state":"connected","old_state":"reconnecting"}
   ```

3. **Ingress sync:**
   ```
   {"msg":"Finished syncing existing ingresses to controller","routes":4}
   ```

4. **Prometheus metrics:**
   ```
   {"msg":"✓ Discovered Prometheus service"}
   ```

If you see these periodically, the agent is healthy.

### Q: How often does the agent send heartbeats?

Every **30 seconds** to match control plane expectations.

If heartbeat fails, it retries with exponential backoff (5s, 10s, 30s) up to 3 attempts.

---

## Monitoring Stack

### Q: Why do I keep seeing "✓ Discovered Prometheus service" every 30 seconds?

This is **normal and expected**. The agent:

1. Sends a heartbeat to the control plane every 30 seconds
2. Each heartbeat includes monitoring information (Prometheus URL, credentials, etc.)
3. To get this information, the agent discovers the Prometheus service dynamically
4. Logs at INFO level when successfully discovered

**Why dynamic discovery?** Different Kubernetes distributions (K3s, managed clusters, vanilla K8s) deploy Prometheus with different service names. The agent detects the actual service name and port automatically.

Other services (Grafana, Loki, OpenCost) are discovered once at startup because they don't need to be included in heartbeat messages.

### Q: Can I disable these periodic logs?

Not directly, but you can:

1. **Reduce log level** to WARN (hides INFO logs)
2. **Filter logs** in your monitoring system (Loki/Grafana)
3. The logs are harmless and indicate healthy monitoring

### Q: Why only Prometheus is logged repeatedly?

Because **only Prometheus information is sent with each heartbeat** (every 30 seconds) to the control plane. This allows the control plane to:
- Access Prometheus metrics via the tunnel or directly
- Monitor cluster health without polling
- Get real-time access credentials

Other services:
- **Grafana**: Accessed via ingress proxy (discovered once at startup)
- **Loki**: Logs forwarded by Promtail (no agent involvement)
- **OpenCost**: Scraped by Prometheus (no agent involvement)

**Technical detail:** The log appears in `internal/components/manager.go::discoverPrometheusService()` which is called by `GetMonitoringInfo()` on every heartbeat cycle.

---

## Region Detection

### Q: How does the agent detect region?

Detection order:

1. **Node labels** (most reliable): `topology.kubernetes.io/region`, provider-specific labels
2. **Provider ID**: `aws://`, `gce://`, `azure://`, etc.
3. **Metadata service**: AWS IMDSv2, GCP metadata, Azure IMDS
4. **Local environment detection**: K3s, kind, minikube, Docker Desktop
5. **GeoIP detection**: For bare-metal/on-premises clusters

### Q: What if region can't be detected?

Defaults:
- **Provider**: `"bare-metal"` or `"on-premises"`
- **Region**: `"on-premises"` or `"agent-managed"`
- **Registry Region**: `"us"` (unless GeoIP detects Europe)

### Q: How is registry region determined?

For cloud providers:
- EU regions (eu-west-1, eu-central-1, etc.) → `"eu"`
- All other regions → `"us"`

For bare-metal/on-premises:
- **GeoIP**: Europe + Africa → `"eu"`
- **GeoIP**: Other continents → `"us"`
- **No GeoIP**: `"us"` (default)

---

## Troubleshooting

### Agent not connecting to control plane

Check logs for:
```
{"error":"failed to connect via WebSocket","level":"warning"}
```

**Solutions:**
1. Verify `PIPEOPS_API_URL` is correct
2. Check `AGENT_TOKEN` is valid
3. Ensure network connectivity to control plane
4. Check firewall rules (WebSocket requires outbound HTTPS)

### Ingress sync not working

Check logs for:
```
{"msg":"✅ Gateway proxy ingress watcher started successfully"}
```

If you see:
```
{"msg":"Gateway proxy not needed for public cluster"}
```

Your cluster has a public LoadBalancer - ingress sync is disabled (direct routing instead).

### Monitoring stack not starting

Check:
1. Storage class available: `kubectl get storageclass`
2. Helm installed: `helm version`
3. CRDs installed: `kubectl get crd | grep monitoring.coreos.com`
4. Namespace exists: `kubectl get ns pipeops-monitoring`

### WebSocket disconnections

```
{"error":"websocket: close 1006 (abnormal closure): unexpected EOF"}
{"msg":"Attempting to reconnect to WebSocket"}
```

This is **normal** - network hiccups, control plane restarts, etc. The agent auto-reconnects and re-registers.

---

## Advanced Configuration

### Enable verbose heartbeat logging

Set log level to DEBUG:
```yaml
# config.yaml
logging:
  level: debug
```

Or via environment variable:
```bash
LOG_LEVEL=debug
```

### How to enable ingress sync

Add to your configuration file:

```yaml
agent:
  enable_ingress_sync: true
```

Or set via environment variable:
```bash
PIPEOPS_ENABLE_INGRESS_SYNC=true
```

Then restart the agent:
```bash
kubectl rollout restart deployment/pipeops-agent -n pipeops-system
```

### How to check if ingress sync is enabled

Check agent logs on startup:

```bash
kubectl logs deployment/pipeops-agent -n pipeops-system | grep -i "ingress sync"
```

**Output examples:**
- `"Ingress sync disabled"` = NOT exposing cluster
- `"Ingress sync enabled"` = Monitoring and exposing ingresses

### Disable gateway proxy (force direct routing)

Gateway proxy is automatically disabled when ingress sync is disabled. To use secure admin access without external exposure, keep `enable_ingress_sync: false` (default).

### Custom Prometheus discovery interval

Not currently configurable - hard-coded to 30 seconds. However, discovery results are now cached for 5 minutes to reduce log frequency.

---

## Log Levels Guide

| Level | What you see |
|-------|-------------|
| **ERROR** | Critical failures only |
| **WARN** | Failures with retry, missing configs |
| **INFO** | Startup, registration, sync, discoveries (default) |
| **DEBUG** | Heartbeats, WebSocket messages, detailed flow |
| **TRACE** | Raw HTTP/WebSocket traffic, internal state |

**Recommended**: `INFO` (default) for production, `DEBUG` for troubleshooting.
