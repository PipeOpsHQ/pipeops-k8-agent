# Test Commands Summary

## Quick Start

### Run Automated Test
```bash
./test-multi-forward.sh
```

This automated script will:
- ✅ Start mock control plane
- ✅ Start agent with test config
- ✅ Monitor tunnel creation
- ✅ Verify multi-port forwards
- ✅ Display logs and results

---

## Manual Testing

### Option 1: Simple Test (2 Terminals)

**Terminal 1 - Mock Control Plane:**
```bash
./bin/mock-control-plane
```

**Terminal 2 - Agent:**
```bash
./bin/pipeops-vm-agent --config config-test.yaml
```

### Option 2: With Log Monitoring (3 Terminals)

**Terminal 1 - Control Plane:**
```bash
./bin/mock-control-plane 2>&1 | tee /tmp/mock-cp.log
```

**Terminal 2 - Agent:**
```bash
./bin/pipeops-vm-agent --config config-test.yaml 2>&1 | tee /tmp/agent.log
```

**Terminal 3 - Monitor:**
```bash
# Check tunnel status
curl -s http://localhost:8081/api/tunnel/status/test-agent-001 | jq

# Watch agent health
watch -n 2 'curl -s http://localhost:8080/health | jq'

# Follow logs
tail -f /tmp/agent.log /tmp/mock-cp.log
```

---

## Build Commands

### Build All
```bash
make build
# or
go build -o bin/pipeops-vm-agent cmd/agent/main.go
go build -o bin/mock-control-plane test/mock-control-plane/main-tunnel.go
```

### Build Agent Only
```bash
go build -o bin/pipeops-vm-agent cmd/agent/main.go
```

### Build Mock Control Plane Only
```bash
go build -o bin/mock-control-plane test/mock-control-plane/main-tunnel.go
```

---

## Verification Commands

### Check Tunnel Status
```bash
curl -s http://localhost:8081/api/tunnel/status/test-agent-001 | jq
```

### Check Agent Health
```bash
curl -s http://localhost:8080/health | jq
```

### Extract Port Allocations
```bash
# Get all allocated ports
curl -s http://localhost:8081/api/tunnel/status/test-agent-001 | \
  jq -r '.forwards[] | "\(.name): \(.remote_port)"'

# Expected output:
# kubernetes-api: 8000
# agent-http: 8001
```

### Count Forwards
```bash
curl -s http://localhost:8081/api/tunnel/status/test-agent-001 | \
  jq '.forwards | length'

# Expected: 2
```

---

## What Success Looks Like

### ✅ Control Plane Output
```
🚀 Mock Control Plane (Tunnel) starting on :8081
INFO Tunnel requested for agent: test-agent-001
INFO Allocated port 8000 for agent test-agent-001
INFO Allocated port 8001 for agent test-agent-001
INFO Returning 2 forwards for agent test-agent-001
```

### ✅ Agent Output
```
INFO Tunnel manager initialized with port forwards    forwards=2
INFO Starting tunnel poll service
INFO Polling control plane for tunnel status
INFO Tunnel status received                           status=ready forwards=2
INFO Creating tunnel with multiple forwards           forwards=2
INFO Tunnel created successfully
```

### ✅ Tunnel Status Response
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
  "tunnel_server_addr": "localhost:5000"
}
```

---

## Troubleshooting

### Binaries Not Found
```bash
# Build both binaries
go build -o bin/pipeops-vm-agent cmd/agent/main.go
go build -o bin/mock-control-plane test/mock-control-plane/main-tunnel.go
```

### Port Already in Use
```bash
# Kill existing processes
pkill -f mock-control-plane
pkill -f pipeops-vm-agent

# Or kill specific ports
lsof -ti:8080,8081 | xargs kill -9
```

### Check Logs
```bash
# View saved logs
cat /tmp/mock-cp.log
cat /tmp/agent.log

# Grep for errors
grep -i error /tmp/agent.log
grep -i error /tmp/mock-cp.log
```

### Reset Test Environment
```bash
# Kill all processes
pkill -f mock-control-plane
pkill -f pipeops-vm-agent

# Clean logs
rm -f /tmp/mock-cp.log /tmp/agent.log

# Rebuild
go build -o bin/pipeops-vm-agent cmd/agent/main.go
go build -o bin/mock-control-plane test/mock-control-plane/main-tunnel.go

# Restart test
./test-multi-forward.sh
```

---

## Configuration

### Test Config Location
```
config-test.yaml
```

### Current Forwards (config-test.yaml)
```yaml
forwards:
  - name: "kubernetes-api"
    local_addr: "localhost:6443"
    remote_port: 0
  - name: "agent-http"
    local_addr: "localhost:8080"
    remote_port: 0
```

### Add More Forwards
Edit `config-test.yaml` and add to the forwards array:
```yaml
  - name: "kubelet-metrics"
    local_addr: "localhost:10250"
    remote_port: 0
```

Then update mock control plane to allocate 3 ports.

---

## Documentation

📚 **Detailed Testing Guide:** [docs/MULTI_FORWARD_TESTING.md](docs/MULTI_FORWARD_TESTING.md)

Includes:
- Architecture overview
- Step-by-step testing instructions
- Verification checklist
- Troubleshooting guide
- Configuration reference
- Before/after comparison

---

## Next Steps

After successful testing:

1. ✅ Remove old proxy code
2. ✅ Update production control plane API
3. ✅ Add TLS support
4. ✅ Implement kubectl config generation
5. ✅ Update documentation
6. ✅ Add per-forward metrics

---

## Quick Reference

| Command | Purpose |
|---------|---------|
| `./test-multi-forward.sh` | Automated test script |
| `./bin/mock-control-plane` | Start control plane |
| `./bin/pipeops-vm-agent --config config-test.yaml` | Start agent |
| `curl http://localhost:8081/api/tunnel/status/test-agent-001` | Check tunnel |
| `curl http://localhost:8080/health` | Check agent |
| `cat /tmp/agent.log` | View agent logs |
| `cat /tmp/mock-cp.log` | View CP logs |
