# Testing Strategy for PipeOps VM Agent

## 🧪 Testing Levels

### 1. Unit Tests
### 2. Integration Tests  
### 3. End-to-End Tests
### 4. Manual Testing
### 5. Load Testing

---

## 🔬 **1. Unit Tests**

### Current Status
✅ Basic agent tests exist in `internal/agent/agent_test.go`

### Run Unit Tests
```bash
cd /Users/nitrocode/PipeOps/pipeopsv1/pipeops-vm-agent

# Run all unit tests
go test ./...

# Run with coverage
go test -cover ./...

# Run with verbose output
go test -v ./...

# Run specific package tests
go test ./internal/agent/
go test ./internal/communication/
```

### Expand Unit Tests
We should add more unit tests for:
- Communication client connection management
- Message routing logic
- K8s proxy request/response handling
- Error handling scenarios

---

## 🔗 **2. Integration Tests**

### Test Components Integration

#### A. Mock Control Plane Server
Create a mock Control Plane for testing agent registration and dual connections:

```bash
# Create integration test
mkdir -p test/integration
```

#### B. Mock Runner Server
Test Runner assignment and operational message flow

#### C. Mock Kubernetes API
Test K8s proxy functionality without real cluster

---

## 🎯 **3. End-to-End Tests**

### Prerequisites Setup

#### A. Local k3s Cluster
```bash
# Install k3s (if not already installed)
curl -sfL https://get.k3s.io | sh -

# Or using Docker
docker run --privileged --name k3s-server -p 6443:6443 \
  rancher/k3s:latest server --disable traefik
```

#### B. Configuration Setup
```bash
# Copy example config
cp config.example.yaml config.yaml

# Edit config.yaml with your settings
```

### Test Scenarios

#### Scenario 1: Basic Agent Startup
```bash
# Build agent
go build -o bin/agent cmd/agent/main.go

# Run agent with test config
./bin/agent --config config.yaml --debug

# Expected: Agent starts, tries to connect to Control Plane
```

#### Scenario 2: Mock Control Plane Test
```bash
# Terminal 1: Start mock Control Plane
go run test/mock-control-plane/main.go

# Terminal 2: Start agent pointing to mock Control Plane
./bin/agent --config config-test.yaml
```

#### Scenario 3: Full Dual Connection Test
```bash
# Terminal 1: Mock Control Plane
go run test/mock-control-plane/main.go

# Terminal 2: Mock Runner  
go run test/mock-runner/main.go

# Terminal 3: Agent
./bin/agent --config config-test.yaml

# Terminal 4: Test client (simulate operations)
go run examples/runner_client.go
```

---

## 🛠️ **4. Manual Testing**

### A. Health Check Testing
```bash
# Start agent
./bin/agent --config config.yaml

# Test health endpoints
curl http://localhost:8080/health
curl http://localhost:8080/ready
curl http://localhost:8080/version
```

### B. WebSocket Connection Testing
```bash
# Test WebSocket upgrade
wscat -c ws://localhost:8080/ws/k8s \
  -H "Authorization: Bearer test-token"
```

### C. K8s API Proxy Testing
```bash
# Test via runner example
go run examples/runner_client.go

# Or test directly with WebSocket tool
# Send K8s API requests through WebSocket
```

### D. Dual Connections Testing
```bash
# Monitor logs for dual connection establishment
./bin/agent --config config.yaml --debug 2>&1 | grep -i "dual\|runner\|connection"
```

---

## ⚡ **5. Load Testing**

### Connection Load Test
```bash
# Test multiple concurrent WebSocket connections
for i in {1..10}; do
  go run test/load/concurrent_connections.go &
done
wait
```

### Message Throughput Test
```bash
# Test message processing rate
go run test/load/message_throughput.go
```

---

## 🐛 **Testing Specific Features**

### Dual Connections Feature
```bash
# Test sequence:
# 1. Agent connects to Control Plane only
# 2. Control Plane sends runner assignment
# 3. Agent establishes Runner connection
# 4. Both connections active simultaneously
# 5. Messages routed to correct endpoints
```

### K8s Proxy Feature
```bash
# Test all HTTP methods:
# GET, POST, PUT, DELETE, PATCH
# Test streaming (WATCH operations)
# Test authentication passthrough
# Test error handling
```

### Reconnection Logic
```bash
# Test scenarios:
# 1. Control Plane disconnects
# 2. Runner disconnects  
# 3. Both disconnect
# 4. Network interruption
# 5. Agent restart
```

---

## 📊 **Test Data & Monitoring**

### Metrics to Monitor
- Connection establishment time
- Message latency
- Reconnection frequency
- Memory usage
- CPU usage
- Goroutine count
- WebSocket connection count

### Log Analysis
```bash
# Monitor key events
tail -f /var/log/agent.log | grep -E "(ERROR|WARN|connection|dual|runner)"

# Connection status
tail -f /var/log/agent.log | grep -E "(heartbeat|status)"
```

---

## 🔧 **Quick Test Commands**

### Development Workflow
```bash
# 1. Build and test
go build ./... && go test ./...

# 2. Run integration tests
go test ./test/integration/...

# 3. Start local test environment
docker-compose -f test/docker-compose.yaml up

# 4. Run end-to-end tests
go test ./test/e2e/...

# 5. Manual verification
./bin/agent --config config-test.yaml --debug
```

### Debug Mode Testing
```bash
# Enable verbose logging
export PIPEOPS_DEBUG=true
export LOG_LEVEL=debug

./bin/agent --config config.yaml
```

---

## 🎯 **Test Checklist**

### ✅ Basic Functionality
- [ ] Agent starts successfully
- [ ] Configuration loading works
- [ ] Health endpoints respond
- [ ] Graceful shutdown works

### ✅ Connection Management  
- [ ] Control Plane connection establishes
- [ ] Runner assignment triggers dual connection
- [ ] Both connections maintain heartbeat
- [ ] Reconnection works after failure

### ✅ Message Handling
- [ ] Registration messages work
- [ ] Heartbeat messages work
- [ ] Runner assignment messages work
- [ ] Operational messages route correctly

### ✅ K8s Integration
- [ ] K8s API proxy works for all methods
- [ ] Streaming (WATCH) operations work
- [ ] Authentication passes through
- [ ] Errors handled gracefully

### ✅ Security
- [ ] Origin checking works
- [ ] Token authentication works
- [ ] TLS connections work
- [ ] Input validation works

### ✅ Performance
- [ ] Low latency message routing
- [ ] Efficient connection management
- [ ] Proper resource cleanup
- [ ] Memory usage stable
