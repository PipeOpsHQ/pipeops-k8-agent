# Development Roadmap - Next Steps

This document outlines the development tasks needed to complete the PipeOps VM Agent codebase.

**STATUS: ~75% Complete** | See [IMPLEMENTATION_STATUS.md](../IMPLEMENTATION_STATUS.md) for detailed progress.

**MAJOR UPDATE:** Control Plane Communication now COMPLETE! See [CONTROL_PLANE_INTEGRATION.md](../CONTROL_PLANE_INTEGRATION.md).

## Legend

- ✓ **Implemented** - Feature is complete and working
- ⚠️ **Partial** - Feature exists but needs work
- ✗ **Missing** - Feature needs to be implemented
- 🔥 **Critical** - Blocks production use

## Table of Contents

- [Critical Tasks](#critical-tasks)
- [Installation Scripts](#installation-scripts)
- [Core Agent Features](#core-agent-features)
- [Real-Time Features](#real-time-features)
- [API Endpoints](#api-endpoints)
- [Testing](#testing)
- [Documentation](#documentation)
- [CI/CD Pipeline](#cicd-pipeline)
- [Security Hardening](#security-hardening)
- [Performance Optimization](#performance-optimization)

---

## Critical Tasks

### 1. Create Installation Scripts ⚠️ (90% Complete)

Location: `scripts/`

#### `scripts/install.sh` ✓ **IMPLEMENTED**

Main installation script for setting up k3s and PipeOps agent.

**Status:** Fully functional (652 lines) with comprehensive features.

**Requirements:**

- Detect OS and architecture (Ubuntu, Debian, CentOS, RHEL, macOS)
- Install k3s with appropriate configuration
- Create `pipeops-system` namespace
- Set up RBAC (ServiceAccount, ClusterRole, ClusterRoleBinding)
- Create secrets from environment variables
- Deploy agent using manifest
- Validate installation with health checks
- Handle both server and agent (worker) node types

**Environment Variables:**

- `PIPEOPS_TOKEN` (required) - Authentication token
- `CLUSTER_NAME` - Cluster identifier (default: "default-cluster")
- `PIPEOPS_API_URL` - Control plane URL (default: "https://api.pipeops.io")
- `PIPEOPS_AGENT_ID` - Agent ID (auto-generated if not provided)
- `PIPEOPS_PORT` - HTTP server port (default: 8080)
- `K3S_VERSION` - k3s version (default: latest)
- `LOG_LEVEL` - Logging level (default: "info")
- `NODE_TYPE` - "server" or "agent" (default: "server")
- `K3S_URL` - Master URL for worker nodes
- `K3S_TOKEN` - Cluster token for worker nodes

**Exit Codes:**

- 0: Success
- 1: Missing required environment variables
- 2: OS not supported
- 3: k3s installation failed
- 4: Agent deployment failed

#### `scripts/join-worker.sh` ✓ **IMPLEMENTED**

Script to join worker nodes to existing cluster.

**Usage:**

```bash
./join-worker.sh <MASTER_IP> <CLUSTER_TOKEN>
```

**Requirements:**

- Validate master IP is reachable
- Install k3s in agent mode
- Configure kubelet to join cluster
- Verify node successfully joined

#### `scripts/uninstall.sh` ✗ **MISSING**

Clean uninstallation script.

**Requirements:**

- Prompt for confirmation
- Remove PipeOps agent deployment
- Delete namespace and RBAC resources
- Optionally uninstall k3s (prompt user)
- Clean up any remaining resources
- Display removal summary

#### `scripts/cluster-info.sh`

Display cluster connection information.

**Requirements:**

- Display master node IP
- Show cluster token
- Generate join command for worker nodes
- Show kubeconfig location
- Display agent status

### 2. Create Kubernetes Deployment Manifests

Location: `deployments/`

#### `deployments/agent.yaml`

Complete Kubernetes manifest for the agent.

**Components:**

```yaml
# Namespace
apiVersion: v1
kind: Namespace
metadata:
  name: pipeops-system
  labels:
    name: pipeops-system

---
# ServiceAccount
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pipeops-agent
  namespace: pipeops-system

---
# ClusterRole (full access for now, can be restricted later)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pipeops-agent
rules:
- apiGroups: ["*"]
  resources: ["*"]
  verbs: ["*"]

---
# ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pipeops-agent
subjects:
- kind: ServiceAccount
  name: pipeops-agent
  namespace: pipeops-system
roleRef:
  kind: ClusterRole
  name: pipeops-agent
  apiGroup: rbac.authorization.k8s.io

---
# ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipeops-agent-config
  namespace: pipeops-system
data:
  log_level: "info"
  port: "8080"

---
# Secret (to be created by install script)
# kubectl create secret generic pipeops-agent-secret \
#   --from-literal=token="xxx" \
#   --from-literal=api-url="https://api.pipeops.io" \
#   --from-literal=cluster-name="xxx" \
#   -n pipeops-system

---
# Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pipeops-agent
  namespace: pipeops-system
  labels:
    app: pipeops-agent
spec:
  replicas: 1
  selector:
    matchLabels:
      app: pipeops-agent
  template:
    metadata:
      labels:
        app: pipeops-agent
    spec:
      serviceAccountName: pipeops-agent
      containers:
      - name: agent
        image: pipeops/agent:latest
        imagePullPolicy: Always
        ports:
        - containerPort: 8080
          name: http
          protocol: TCP
        env:
        - name: PIPEOPS_TOKEN
          valueFrom:
            secretKeyRef:
              name: pipeops-agent-secret
              key: token
        - name: PIPEOPS_API_URL
          valueFrom:
            secretKeyRef:
              name: pipeops-agent-secret
              key: api-url
        - name: PIPEOPS_CLUSTER_NAME
          valueFrom:
            secretKeyRef:
              name: pipeops-agent-secret
              key: cluster-name
        - name: PIPEOPS_PORT
          valueFrom:
            configMapKeyRef:
              name: pipeops-agent-config
              key: port
        - name: PIPEOPS_LOG_LEVEL
          valueFrom:
            configMapKeyRef:
              name: pipeops-agent-config
              key: log_level
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        securityContext:
          runAsNonRoot: true
          runAsUser: 1000
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop:
            - ALL

---
# Service
apiVersion: v1
kind: Service
metadata:
  name: pipeops-agent
  namespace: pipeops-system
  labels:
    app: pipeops-agent
spec:
  type: ClusterIP
  ports:
  - port: 8080
    targetPort: 8080
    protocol: TCP
    name: http
  selector:
    app: pipeops-agent
```

#### `deployments/rbac-minimal.yaml`

Minimal RBAC configuration for production (principle of least privilege).

**Requirements:**

- Read-only access to most resources
- Write access only to specific namespaces
- No cluster-admin privileges
- Document required permissions

### 3. Implement Control Plane Communication ✓ **IMPLEMENTED**

Location: `internal/controlplane/`

**Status: COMPLETE** - See [CONTROL_PLANE_INTEGRATION.md](../CONTROL_PLANE_INTEGRATION.md) for full details.

The agent now has full control plane communication implemented with comprehensive testing.

#### `internal/controlplane/client.go` ✓ **IMPLEMENTED**

**Features implemented:**

- ✓ HTTP client for control plane API with TLS 1.2+
- ✓ Registration endpoint handler
- ✓ Heartbeat sender (every 30s)
- ✓ Status reporter (every 60s)
- ✓ Token-based authentication (Bearer token)
- ✓ TLS certificate validation
- ✓ Timeout protection (10-30s per request)
- ✓ Connection health monitoring (Ping endpoint)
- ✓ Graceful degradation (standalone mode when not configured)
- ✓ Comprehensive unit tests with mock servers

**Methods implemented:**

```go
type Client struct {
    apiURL     string
    token      string
    httpClient *http.Client
    agentID    string
    logger     *logger.Logger
}

func NewClient(apiURL, token, agentID string, logger *logger.Logger) (*Client, error)
func (c *Client) RegisterAgent(ctx context.Context, agent *types.Agent) error
func (c *Client) SendHeartbeat(ctx context.Context, req *HeartbeatRequest) error
func (c *Client) ReportStatus(ctx context.Context, status *types.ClusterStatus) error
func (c *Client) FetchCommands(ctx context.Context) ([]Command, error)
func (c *Client) SendCommandResult(ctx context.Context, cmdID string, result *CommandResult) error
func (c *Client) Ping(ctx context.Context) error
```

**Test Coverage:** All methods tested with mock HTTP servers ✓

---

## Installation Scripts

### Additional Helper Scripts

#### `scripts/upgrade.sh`

Upgrade existing agent installation.

**Requirements:**

- Detect current agent version
- Download new version
- Create backup of current deployment
- Apply new deployment
- Verify upgrade successful
- Rollback on failure

#### `scripts/health-check.sh`

Comprehensive health check script.

**Requirements:**

- Check k3s is running
- Verify agent pod is healthy
- Test all API endpoints
- Check connectivity to control plane
- Verify RBAC permissions
- Display summary report

#### `scripts/generate-config.sh`

Generate configuration files interactively.

**Requirements:**

- Prompt for required values
- Validate inputs
- Generate YAML configuration
- Generate environment file
- Display configuration summary

---

## Core Agent Features

### Registration Flow

**Priority: HIGH**

Location: `internal/agent/registration.go`

**Requirements:**

- Agent registers with control plane on startup
- Send cluster information (nodes, version, capacity)
- Receive agent ID if not provided
- Store registration state
- Handle registration failures
- Re-register on reconnection

### Heartbeat Mechanism

**Priority: HIGH**

Location: `internal/agent/heartbeat.go`

**Requirements:**

- Send heartbeat every 30 seconds
- Include cluster health status
- Include resource usage
- Handle network failures gracefully
- Exponential backoff on failures
- Alert on extended disconnection

### Kubernetes Cluster Monitoring

**Priority: HIGH**

Location: `internal/k8s/monitor.go`

**Requirements:**

- Watch cluster nodes status
- Monitor pod health across namespaces
- Track resource usage (CPU, memory)
- Detect new/deleted resources
- Stream changes to control plane
- Cache cluster state locally

### Command Execution

**Priority: MEDIUM**

Location: `internal/agent/commands.go`

**Requirements:**

- Poll control plane for commands
- Execute kubectl commands
- Stream command output
- Handle long-running commands
- Timeout protection
- Return results to control plane

---

## Real-Time Features

### WebSocket Server

**Priority: MEDIUM**

Location: `internal/server/websocket.go`

**Current Status:** Partially implemented

**Needs:**

- Connection authentication
- Message validation
- Broadcasting to multiple clients
- Connection lifecycle management
- Reconnection handling
- Rate limiting per connection

**Message Types:**

```go
type WSMessage struct {
    Type    string          `json:"type"`    // "command", "status", "log", "event"
    Payload json.RawMessage `json:"payload"`
}
```

### Server-Sent Events (SSE)

**Priority: MEDIUM**

Location: `internal/server/sse.go`

**Requirements:**

- SSE endpoint for live updates
- Multiple concurrent connections
- Event types: status, logs, metrics
- Heartbeat mechanism
- Connection timeout handling
- Memory-efficient buffering

### Static Dashboard

**Priority: LOW**

Location: `web/static/`

**Requirements:**

- Real-time cluster overview
- Node status visualization
- Pod status by namespace
- Resource usage charts (CPU, memory)
- Live log viewer
- WebSocket connection indicator
- Responsive design
- No external dependencies (single HTML file)

---

## API Endpoints

### Health and Status Endpoints

**Priority: HIGH**

Location: `internal/server/handlers.go`

#### Endpoints to Implement

**`GET /health`** - Basic health check

- Status: 200 OK
- Response: `{"status": "healthy"}`

**`GET /ready`** - Readiness probe

- Check Kubernetes API connectivity
- Check control plane connectivity
- Status: 200 if ready, 503 if not

**`GET /version`** - Version information

- Agent version
- Git commit
- Build time
- Go version

**`GET /api/health/detailed`** - Comprehensive health

- Kubernetes API status
- Control plane connection status
- WebSocket connections count
- Memory usage
- Goroutine count
- Uptime

**`GET /api/status/features`** - Feature flags

- Enabled features list
- Capabilities
- Supported operations

**`GET /api/metrics/runtime`** - Runtime metrics

- CPU usage
- Memory usage
- Goroutine count
- HTTP request statistics
- WebSocket connections

**`GET /api/status/connectivity`** - Connectivity tests

- Control plane reachable
- Kubernetes API reachable
- Network latency
- DNS resolution

### Kubernetes Proxy Endpoints

**Priority: MEDIUM**

Location: `internal/server/k8s_proxy.go`

#### Endpoints to Implement

**`POST /api/k8s/proxy`** - Proxy Kubernetes API requests

Request body:

```json
{
  "method": "GET",
  "path": "/api/v1/namespaces/default/pods",
  "body": null
}
```

**`GET /api/k8s/nodes`** - List cluster nodes

**`GET /api/k8s/pods`** - List all pods

**`GET /api/k8s/namespaces`** - List namespaces

**`GET /api/k8s/logs/:namespace/:pod`** - Get pod logs

### Real-Time Endpoints

**Priority: MEDIUM**

**`GET /ws`** - WebSocket connection

- Bidirectional communication
- Authentication via query param or header
- Multiple message types

**`GET /api/realtime/events`** - SSE event stream

- Cluster events
- Status changes
- Deployment updates

**`GET /api/realtime/logs`** - SSE log stream

- Aggregated logs from multiple pods
- Filter by namespace
- Filter by pod label

---

## Testing

### Unit Tests

**Priority: HIGH**

**Target Coverage: 80%+**

#### Tests Needed

**`internal/agent/`**

- Agent initialization
- Registration logic
- Heartbeat mechanism
- Command execution

**`internal/server/`**

- HTTP handlers
- WebSocket connection handling
- SSE streaming
- Middleware

**`internal/k8s/`**

- Kubernetes client operations
- Cluster monitoring
- Resource watching

**`pkg/types/`**

- Type validations
- JSON serialization

#### Test Structure

```bash
# Run all tests
go test ./...

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run specific package
go test -v ./internal/agent/
```

### Integration Tests

**Priority: MEDIUM**

Location: `tests/integration/`

**Requirements:**

- Test against real k3s cluster (kind or k3d)
- Test full registration flow
- Test WebSocket communication
- Test SSE streaming
- Test Kubernetes operations
- Mock control plane server

### End-to-End Tests

**Priority: LOW**

Location: `tests/e2e/`

**Requirements:**

- Full deployment to test cluster
- Test complete workflows
- Test failure scenarios
- Test upgrade process
- Performance tests

---

## Documentation

### Code Documentation

**Priority: MEDIUM**

**Requirements:**

- Add godoc comments to all exported functions
- Document package purpose in package comments
- Add examples in documentation
- Document error conditions

**Generate Documentation:**

```bash
godoc -http=:6060
# Visit http://localhost:6060/pkg/github.com/pipeops/pipeops-vm-agent/
```

### API Documentation

**Priority: MEDIUM**

Location: `docs/API.md`

**Requirements:**

- Document all HTTP endpoints
- Include request/response examples
- Document WebSocket message format
- Document SSE event format
- Include curl examples
- Document error codes

### Architecture Diagrams

**Priority: LOW**

Location: `docs/diagrams/`

**Needed Diagrams:**

- Component architecture (PlantUML or Mermaid)
- Sequence diagrams for key flows
- Network topology
- Data flow diagrams

---

## CI/CD Pipeline

### GitHub Actions Workflows

**Priority: MEDIUM**

Location: `.github/workflows/`

#### `ci.yml` - Continuous Integration

**Triggers:** Push, Pull Request

**Jobs:**

- Lint (golangci-lint)
- Test (go test)
- Build (go build)
- Security scan (gosec)
- Dependency audit (govulncheck)

#### `release.yml` - Release Automation

**Trigger:** Tag push (v*)

**Jobs:**

- Build multi-arch binaries (amd64, arm64)
- Create Docker images
- Push to registry
- Create GitHub release
- Upload artifacts
- Update changelog

#### `docker.yml` - Docker Image Build

**Triggers:** Push to main, Tag

**Jobs:**

- Build Docker image
- Run container tests
- Security scan (Trivy)
- Push to Docker Hub
- Push to GitHub Container Registry

### Dockerfiles

**Priority: HIGH**

Location: `build/`

#### `build/Dockerfile`

Multi-stage Dockerfile:

```dockerfile
# Build stage
FROM golang:1.21-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o pipeops-agent ./cmd/agent

# Final stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /build/pipeops-agent .
USER 1000:1000
EXPOSE 8080
ENTRYPOINT ["/app/pipeops-agent"]
```

#### `build/Dockerfile.dev`

Development Dockerfile with hot reload:

```dockerfile
FROM golang:1.21
WORKDIR /app
RUN go install github.com/cosmtrek/air@latest
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["air"]
```

---

## Security Hardening

### Security Scanning

**Priority: HIGH**

**Tools to Integrate:**

- gosec - Go security checker
- govulncheck - Go vulnerability checker
- Trivy - Container scanning
- dependabot - Dependency updates

### RBAC Refinement

**Priority: MEDIUM**

Location: `deployments/rbac-minimal.yaml`

**Requirements:**

- Document minimum required permissions
- Create role per operation type
- Implement namespace isolation
- Add audit logging

### Secret Management

**Priority: HIGH**

**Requirements:**

- Never log secrets
- Encrypt secrets at rest
- Support external secret managers (Vault, AWS Secrets Manager)
- Rotate tokens automatically
- Secure storage of kubeconfig

### Network Policies

**Priority: MEDIUM**

Location: `deployments/network-policies.yaml`

**Requirements:**

- Restrict ingress to agent pod
- Allow only necessary egress
- Block pod-to-pod unless needed
- Document all allowed connections

---

## Performance Optimization

### Metrics Collection

**Priority: MEDIUM**

**Requirements:**

- Prometheus metrics endpoint
- Custom metrics for agent operations
- Kubernetes metrics collection
- Control plane API latency tracking

### Resource Optimization

**Priority: LOW**

**Requirements:**

- Profile CPU usage
- Profile memory usage
- Optimize Kubernetes watch patterns
- Implement caching for frequent queries
- Connection pooling

### Benchmarking

**Priority: LOW**

Location: `tests/benchmarks/`

**Requirements:**

- Benchmark critical paths
- Load testing WebSocket connections
- Load testing SSE connections
- Stress testing Kubernetes operations

**Run Benchmarks:**

```bash
go test -bench=. -benchmem ./...
```

---

## Development Environment

### Local Development Setup

**Priority: HIGH**

Create `docs/DEVELOPMENT.md` with:

- Prerequisites installation
- Setting up local k3s
- Running agent locally
- Testing against mock control plane
- Debugging tips

### Makefile

**Priority: MEDIUM**

Location: `Makefile`

**Targets:**

```makefile
.PHONY: all build test lint clean docker-build docker-run install

all: lint test build

build:
	go build -o bin/pipeops-agent ./cmd/agent

test:
	go test -v -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ coverage.out

docker-build:
	docker build -t pipeops/agent:latest -f build/Dockerfile .

docker-run:
	docker run -p 8080:8080 pipeops/agent:latest

install:
	go install ./cmd/agent

dev:
	air
```

---

## Priority Summary

### Immediate (This Week)

1. Create `scripts/install.sh` - Basic functionality
2. Create `deployments/agent.yaml` - Complete manifest
3. Implement control plane client (`internal/controlplane/client.go`)
4. Implement registration and heartbeat
5. Add unit tests for core functionality

### Short Term (This Month)

1. Complete all installation scripts
2. Implement all documented API endpoints
3. Complete WebSocket and SSE features
4. Set up CI/CD pipeline
5. Reach 80% test coverage
6. Security scanning integration

### Medium Term (Next Quarter)

1. Create static dashboard
2. Implement advanced monitoring features
3. Performance optimization
4. Complete documentation
5. E2E testing framework
6. Multi-arch builds

### Long Term (Future)

1. Plugin system for extensions
2. Multi-cluster management
3. Advanced analytics
4. Cost optimization features
5. Compliance reporting

---

## Contributing

See `CONTRIBUTING.md` for:

- Development workflow
- Code style guidelines
- Pull request process
- Release process

---

## Questions or Issues?

- Open an issue on GitHub
- Discussion forum: <https://github.com/PipeOpsHQ/pipeops-k8-agent/discussions>
- Email: dev@pipeops.io
