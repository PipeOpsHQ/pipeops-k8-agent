# Multi-Port Forward Testing Guide

## Overview

This guide explains how to test the new Portainer-style multi-port forwarding implementation where the agent creates a single Chisel tunnel with multiple port forwards.

## Architecture

**Portainer-Style Approach:**
- Single Chisel tunnel with multiple remote port forwards
- No application-level HTTP translation layer
- Direct TCP port forwarding (like Portainer)
- Control plane allocates unique remote ports for each forward
- Client connects: `R:8000:localhost:6443,R:8001:localhost:10250,R:8002:localhost:8080`

**Configured Forwards (config-test.yaml):**
1. `kubernetes-api`: localhost:6443 → Remote port (allocated by control plane)
2. `agent-http`: localhost:8080 → Remote port (allocated by control plane)

## Quick Test (Automated)

Run the automated test script:

```bash
./test-multi-forward.sh
```

This script will:
1. Start the mock control plane on port 8081
2. Start the agent with config-test.yaml
3. Monitor tunnel creation
4. Check logs for multi-forward indicators
5. Query tunnel status to verify forwards array
6. Display summary and wait for user input

## Manual Testing

### Terminal 1: Start Mock Control Plane

```bash
./bin/mock-control-plane
```

Expected output:
```
🚀 Mock Control Plane (Tunnel) starting on :8081
HTTP endpoints:
  - Health: http://localhost:8081/health
  - Request Tunnel: POST http://localhost:8081/api/tunnel/request
  - Tunnel Status: GET http://localhost:8081/api/tunnel/status/:agentId
```

### Terminal 2: Start Agent

```bash
./bin/pipeops-vm-agent --config config-test.yaml
```

Expected log output:
```
INFO Tunnel manager initialized with port forwards    forwards=2
INFO Starting tunnel poll service
INFO Polling control plane for tunnel status
INFO Tunnel status received                           status=ready
INFO Creating tunnel with multiple forwards           forwards=2
INFO Tunnel created successfully
```

### Terminal 3: Monitor Tunnel Status

Query the control plane for tunnel status:

```bash
curl -s http://localhost:8081/api/tunnel/status/test-agent-001 | jq
```

Expected response:
```json
{
  "status": "ready",
  "forwards": [
    {
      "name": "kubernetes-api",
      "remote_port": 8000
    },
    {
      "name": "agent-http",
      "remote_port": 8001
    }
  ],
  "credentials": "agent-test-agent-001",
  "poll_frequency": 30,
  "tunnel_server_addr": "localhost:5000",
  "server_fingerprint": ""
}
```

### Verify Each Forward

Check that each forward is allocated a unique remote port:

```bash
# Get tunnel status
STATUS=$(curl -s http://localhost:8081/api/tunnel/status/test-agent-001)

# Extract ports
K8S_PORT=$(echo $STATUS | jq -r '.forwards[] | select(.name=="kubernetes-api") | .remote_port')
AGENT_PORT=$(echo $STATUS | jq -r '.forwards[] | select(.name=="agent-http") | .remote_port')

echo "Kubernetes API forwarded to: $K8S_PORT"
echo "Agent HTTP forwarded to: $AGENT_PORT"
```

## What to Look For

### ✅ Success Indicators

1. **Control Plane Logs:**
   - "Allocated port X for agent test-agent-001" (appears multiple times)
   - "Returning 2 forwards for agent test-agent-001"
   - Each forward has a unique remote port

2. **Agent Logs:**
   - "Tunnel manager initialized with port forwards    forwards=2"
   - "Creating tunnel with multiple forwards           forwards=2"
   - "Tunnel created successfully"
   - No errors about missing LocalAddr or RemotePort fields

3. **Tunnel Status Response:**
   - `forwards` array with 2 elements
   - Each forward has `name` and `remote_port` fields
   - Remote ports are different (8000, 8001, etc.)

### ❌ Failure Indicators

1. **Compilation Errors:**
   - "LocalAddr undefined" or "RemotePort undefined"
   - "cannot use []TunnelForward" type errors
   - Missing imports

2. **Runtime Errors:**
   - "No forwards configured" or "forwards=0"
   - "Failed to create tunnel"
   - Single port allocation instead of multiple

3. **Configuration Errors:**
   - "tunnel.local_addr" still in config (old format)
   - Missing forwards array
   - Empty forwards array

## Verification Checklist

- [ ] Mock control plane starts on port 8081
- [ ] Agent starts without errors
- [ ] Agent logs show "forwards=2"
- [ ] Control plane allocates 2 separate ports
- [ ] Tunnel status returns forwards array
- [ ] Each forward has unique remote port
- [ ] No proxy-related errors (proxy layer removed)
- [ ] Agent health endpoint responds on port 8080

## Troubleshooting

### Issue: "main redeclared" error
**Solution:** Old main.go renamed to main-websocket.go.bak

### Issue: "proxy undefined" error
**Solution:** internal/proxy directory removed, imports cleaned up

### Issue: "LocalAddr undefined"
**Solution:** Code updated to use Forwards array instead of single LocalAddr

### Issue: Tunnel not creating
**Potential causes:**
- Check agent logs for error messages
- Verify config-test.yaml has forwards array
- Ensure mock control plane is running
- Check network connectivity to localhost:8081

### Issue: Only one forward allocated
**Potential causes:**
- Check RequestTunnel logic in mock control plane
- Verify loop over forwards array
- Check agent config parsing

## Next Steps After Successful Test

1. **Remove old proxy code references:**
   - Update documentation to remove K8s proxy mentions
   - Remove any remaining proxy imports
   - Clean up unused HTTP handlers

2. **Production integration:**
   - Update production control plane API to return forwards array
   - Add TLS support for tunnel server
   - Implement kubectl config generation for direct K8s access
   - Add per-forward metrics and monitoring

3. **Documentation updates:**
   - Update ARCHITECTURE.md with Portainer-style approach
   - Document direct K8s API access via tunnel
   - Add examples of using forwarded ports
   - Create guide for kubectl through tunnel

## Log Files

When running automated test:
- Mock Control Plane: `/tmp/mock-cp.log`
- Agent: `/tmp/agent.log`

View full logs:
```bash
# Control plane
cat /tmp/mock-cp.log

# Agent
cat /tmp/agent.log

# Follow in real-time
tail -f /tmp/mock-cp.log
tail -f /tmp/agent.log
```

## Configuration Reference

### config-test.yaml Structure

```yaml
tunnel:
  enabled: true
  poll_interval: "5s"
  inactivity_timeout: "2m"
  forwards:
    - name: "kubernetes-api"
      local_addr: "localhost:6443"
      remote_port: 0  # Allocated by control plane
    - name: "agent-http"
      local_addr: "localhost:8080"
      remote_port: 0  # Allocated by control plane
```

### Adding More Forwards

To add additional forwards (e.g., kubelet metrics):

```yaml
forwards:
  - name: "kubernetes-api"
    local_addr: "localhost:6443"
    remote_port: 0
  - name: "kubelet-metrics"
    local_addr: "localhost:10250"
    remote_port: 0
  - name: "agent-http"
    local_addr: "localhost:8080"
    remote_port: 0
```

Then update mock control plane RequestTunnel to allocate 3 ports.

## Architecture Changes Summary

**Before (Phase 1):**
- Single port forward: `tunnel.local_addr: "localhost:8080"`
- K8s proxy layer translated HTTP requests to K8s API
- Complex routing logic in internal/proxy/k8s_proxy.go

**After (Phase 2 - Portainer Style):**
- Multiple port forwards: `tunnel.forwards: [{name, local_addr}]`
- Direct TCP tunneling without translation
- Simpler architecture, more flexible port forwarding
- Control plane allocates remote ports dynamically
- Single Chisel tunnel handles all forwards

**Key Benefits:**
- 🚀 Simpler: No HTTP translation layer needed
- 🔒 Secure: Direct TLS to K8s API
- 📈 Scalable: Easy to add more port forwards
- 🎯 Flexible: Forward any TCP port, not just HTTP
- 💪 Robust: Less code = fewer bugs
