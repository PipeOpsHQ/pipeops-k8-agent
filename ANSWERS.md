# Answers to Your Questions

## 1. Why are we getting "Discovered Prometheus service" logs repeatedly?

**Answer:** This is **NORMAL and EXPECTED behavior**.

The agent periodically (every 30 seconds) checks that it can reach the Prometheus service to collect cluster metrics. This is a health check ensuring the monitoring stack is operational.

**Why only Prometheus?**
- Prometheus is actively queried by the agent for metrics (CPU, memory, pods, etc.)
- Other services (Grafana, Loki, OpenCost) don't need periodic agent polling:
  - **Grafana**: Accessed via HTTP proxy/ingress
  - **Loki**: Receives logs from Promtail (push model)
  - **OpenCost**: Scraped by Prometheus (not by agent)

**Should you be concerned?**
No. These logs indicate:
- Monitoring stack is healthy
- Prometheus is reachable
- Metrics collection is working

**Can you reduce the log noise?**
Options:
1. Set log level to WARN (hides INFO logs) - but you'll lose other useful info
2. Filter these logs in your log aggregation system (Loki/Grafana)
3. Leave as-is - they're harmless and indicate health

---

## 2. Is ingress sync enabled by default?

**YES**, ingress sync is **automatically enabled** for private clusters.

**How it works:**

1. **On agent startup:**
   - Agent detects if cluster is private (no public LoadBalancer IP on ingress-nginx)
   - If private, starts ingress watcher
   - Syncs all existing ingresses to control plane

2. **After startup:**
   - Watches for ingress create/update/delete events
   - Automatically registers/updates/removes routes in control plane
   - Routes traffic through WebSocket tunnel

**Your logs confirm it's working:**
```json
{"msg":"Initializing gateway proxy detection..."}
{"msg":"🔒 Private cluster detected - using tunnel routing"}
{"msg":"Starting ingress watcher for gateway proxy"}
{"cluster_uuid":"d875996a-b242-4e21-b5e5-c41630347080","ingress_count":4,"msg":"Syncing ingresses with controller"}
{"msg":"✅ Gateway proxy ingress watcher started successfully"}
```

**When is it NOT enabled?**
- Public clusters with LoadBalancer external IPs → Direct routing (no tunnel needed)
- Standalone mode (no control plane configured)

---

## 3. Why doesn't the agent log heartbeat/ping after restart?

**Answer:** The agent **IS** sending heartbeats, but successful heartbeats log at **DEBUG level** (not visible in INFO logs).

**Heartbeat behavior:**

| Event | Log Level | What you see |
|-------|-----------|--------------|
| **Success** | DEBUG | Silent in INFO logs |
| **Failure** | WARN | `"Heartbeat failed, retrying with backoff..."` |
| **After retries fail** | ERROR | `"Failed to send heartbeat after retries"` |

**How to confirm the agent is healthy:**

Look for these INFO-level indicators:

1. **Registration:**
   ```
   {"msg":"Agent registered successfully via WebSocket"}
   {"msg":"✓ Cluster registered successfully"}
   ```

2. **Connection state:**
   ```
   {"msg":"Connection state changed","new_state":"connected"}
   ```

3. **Ingress sync:**
   ```
   {"msg":"Finished syncing existing ingresses to controller","routes":4}
   ```

4. **Prometheus discovery:**
   ```
   {"msg":"✓ Discovered Prometheus service"}
   ```

If you see these periodically (especially the Prometheus discovery every 30s), your agent is healthy and connected.

**Your agent IS working correctly!** The logs confirm:
- WebSocket connected
- Agent registered
- Ingresses synced
- Monitoring stack operational

**Want to see heartbeat logs?**
Set log level to DEBUG:
```yaml
# config.yaml
logging:
  level: debug
```

Or environment variable:
```bash
export LOG_LEVEL=debug
```

---

## 4. What does the restart behavior mean?

**From your logs:**

```
{"error":"websocket: close 1006 (abnormal closure): unexpected EOF"}
{"msg":"Attempting to reconnect to WebSocket"}
{"msg":"WebSocket connection established with control plane"}
{"msg":"Reconnected successfully to WebSocket"}
{"msg":"Control plane connection re-established - refreshing registration"}
{"msg":"Agent registered successfully via WebSocket","status":"re-registered"}
{"msg":"Connection state changed","new_state":"connected"}
{"msg":"Re-registration after reconnect completed successfully"}
```

**This is PERFECT behavior!**

What happened:
1. WebSocket connection dropped (network hiccup, control plane restart, etc.)
2. Agent detected disconnect within 1 second
3. Agent automatically reconnected to control plane
4. Agent re-registered with existing identity (maintaining cluster_id and agent_id)
5. Agent resumed normal operation

**This is NOT an error** - it's the resilience system working as designed.

**Why does this happen?**
Common causes:
- Network hiccups
- Control plane restarts/deployments
- Load balancer health checks
- NAT timeout
- Firewall connection tracking

**Should you be concerned?**
No, unless:
- Reconnects happen constantly (every few seconds)
- Agent fails to reconnect (stays in "reconnecting" state)
- Registration fails after reconnect

Your agent reconnected in ~7 seconds and resumed operation - this is excellent.

---

## Summary: Your Agent is Healthy

All the behaviors you're seeing are **normal and expected**:

1. **Prometheus discovery logs** → Monitoring health checks (every 30s)
2. **Ingress sync enabled** → Auto-enabled for private cluster
3. **No heartbeat logs** → Success logs are at DEBUG level
4. **WebSocket reconnect** → Resilience system working correctly

**Your agent is functioning perfectly!**

The logs show:
- Monitoring stack operational
- Gateway proxy working
- Ingress routes synced
- Connection resilient
- Auto-reconnect working

---

## Recommended Actions

### For Production

**Keep current configuration** - everything is working correctly.

Optional improvements:
1. Monitor control plane WebSocket endpoint uptime
2. Set up alerts for frequent reconnects (>10 per hour)
3. Add Grafana dashboard for agent metrics

### For Development/Debugging

Enable DEBUG logging to see heartbeats:
```bash
export LOG_LEVEL=debug
```

You'll see:
```
{"level":"debug","msg":"Heartbeat sent successfully"}
{"level":"debug","msg":"Received pong from control plane"}
```

### For Log Reduction

If Prometheus discovery logs are too noisy:

**Option 1:** Filter in Loki (recommended)
```logql
{app="pipeops-agent"} |= "" != "Discovered Prometheus service"
```

**Option 2:** Reduce to WARN level (not recommended - hides useful info)
```yaml
logging:
  level: warn
```

---

## Additional Questions?

See [AGENT_FAQ.md](./AGENT_FAQ.md) for comprehensive documentation on:
- Gateway proxy architecture
- Region detection
- Troubleshooting
- Configuration options
- Advanced topics
