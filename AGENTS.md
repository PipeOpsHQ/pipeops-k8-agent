# AGENTS.md - PipeOps VM Agent

## Project Overview

Go 1.24 agent that connects to the PipeOps control plane via WebSocket, manages K3s/Kubernetes clusters, handles TCP/UDP tunneling, ingress configuration, and proxies requests. Uses cobra/viper for CLI, gin for HTTP, gorilla/websocket for real-time communication, and logrus for structured logging.

## Build & Run

```bash
make build                # Build binary to ./bin/pipeops-vm-agent
make run                  # Build and run with config-local.yaml
go run cmd/agent/main.go --config config-local.yaml  # Run without building
```

## Testing

```bash
make test                 # Run all tests: go test -v ./...
make test-coverage        # Tests with race detector + coverage report

# Run a single test by name
go test -v -run TestAgent_New ./internal/agent/

# Run a single test file's package
go test -v ./internal/tunnel/

# Run a specific subtest
go test -v -run TestAgent_New/config_validation ./internal/agent/

# Run tests with race detection (matches CI)
go test -v -race -coverprofile=coverage.out ./...
```

## Linting & Formatting

```bash
make lint                 # Runs: go fmt ./... && go vet ./...
go fmt ./...              # Format all Go files
go vet ./...              # Static analysis
```

CI also runs `golangci-lint` (with `--timeout=5m`, continue-on-error) and fails on `gofmt` drift (excluding `vendor/`).

## Project Structure

```
cmd/agent/main.go         # Entrypoint (cobra CLI, viper config)
internal/                  # Private packages
  agent/                   # Core agent logic, proxy, tunnel manager
  components/              # Component lifecycle management
  controlplane/            # WebSocket client, control plane API
  encryption/              # K3s secrets encryption
  helm/                    # Helm chart installer
  ingress/                 # Traefik/ingress management
  server/                  # Gin HTTP server, health endpoints
  tunnel/                  # TCP/UDP tunnel management (yamux)
  upgrade/                 # K3s upgrade management
  version/                 # Build version info (ldflags)
  websocket/               # WebSocket framework (frame, pool, flow)
pkg/                       # Public/shared packages
  cloud/                   # GeoIP, region detection
  k8s/                     # Kubernetes client, token helpers
  state/                   # Agent state persistence
  types/                   # Shared config and domain types
vendor/                    # Vendored dependencies (committed)
```

## Code Style Guidelines

### Imports

Two groups separated by a blank line: stdlib first, then third-party and internal mixed alphabetically. Use aliases only for disambiguation.

```go
import (
    "context"
    "fmt"
    "net/http"
    "sync"
    "time"

    "github.com/gorilla/websocket"
    "github.com/pipeops/pipeops-vm-agent/internal/components"
    wsocket "github.com/pipeops/pipeops-vm-agent/internal/websocket"
    "github.com/pipeops/pipeops-vm-agent/pkg/types"
    "github.com/sirupsen/logrus"
)
```

### Naming Conventions

- **Structs**: PascalCase descriptive nouns -- `Agent`, `WebSocketClient`, `TunnelService`
- **Factory functions**: `New*()` returning `(*Type, error)` or `*Type` -- `NewClient()`, `NewServer()`
- **Methods**: camelCase unexported, PascalCase exported -- `sendHeartbeat()`, `RegisterAgent()`
- **Constants**: PascalCase exported -- `StateDisconnected`, `ProtocolTCP`
- **String-typed enums**: `type AgentStatus string` with const blocks
- **Int enums with iota**: `type ConnectionState int` with `String()` method
- **Receiver names**: Single letter matching type initial -- `a *Agent`, `c *Client`, `s *Server`, `m *Manager`
- **Bool methods**: `Is*()` prefix -- `IsConnected()`, `IsGatewayMode()`
- **Mutex naming**: `fieldMutex` protects `field` -- `connMutex` protects `conn`

### Error Handling

Always wrap errors with `fmt.Errorf` and `%w` for context:

```go
return nil, fmt.Errorf("failed to create WebSocket client: %w", err)
return fmt.Errorf("failed to register agent with control plane: %w", err)
```

- Sentinel errors: `var errReRegistrationInProgress = errors.New("re-registration in progress")`
- Non-fatal errors: log with `.WithError(err).Warn(...)` and continue
- Fatal errors: return the wrapped error to the caller

### Logging

Uses `logrus` exclusively. Logger is injected via constructor and stored on structs.

```go
a.logger.WithFields(logrus.Fields{
    "agent_id":   a.agentID,
    "cluster_id": a.clusterID,
}).Info("Agent registered successfully")

a.logger.WithError(err).Warn("Failed to connect, retrying")
a.logger.WithField("port", port).Debug("Listening on port")
```

- **Log levels**: `Debug` for internals, `Info` for state changes, `Warn` for non-fatal failures, `Error` for critical failures
- **Emoji in messages**: Used for visibility -- `"Agent registered"`, `"Connected to control plane"`, `"Failed to persist agent ID"`
- **Sensitive data**: Passwords and tokens are `[REDACTED]` before logging
- **Tests**: Set `logger.SetLevel(logrus.ErrorLevel)` to suppress noise

### Struct Initialization

Factory functions for complex types; composite literals with named fields:

```go
agent := &Agent{
    config:          config,
    logger:          logger,
    ctx:             ctx,
    cancel:          cancel,
    connectionState: StateDisconnected,
    wsStreams:        make(map[string]chan []byte),
}
```

Use `make()` for maps and channels. Use `sync.Pool` for buffer reuse.

### Context Patterns

- Root: `context.WithCancel(context.Background())` stored as `ctx`/`cancel` on structs
- Timeouts: `context.WithTimeout(a.ctx, 30*time.Second)` with `defer cancel()`
- Goroutine exit: `case <-a.ctx.Done(): return`
- Graceful shutdown: cancel root context, then `sync.WaitGroup.Wait()` with 30s timeout

### Configuration

Viper with `SetDefault()`, `BindPFlag()`, `BindEnv()` in `init()`. Config structs use dual tags:

```go
type AgentConfig struct {
    Name string `yaml:"name" mapstructure:"name"`
}
```

- Optional sections use pointer fields: `*TunnelConfig`, `*GatewayConfig`
- Defaults via `DefaultTimeouts()` with a `Merge()` method for user overrides
- Separate `validateConfig()` function for required field checks

### Interface Design

Interfaces defined at point of use (consumer-side), not in the implementing package. Keep small (3-5 methods). Use descriptive noun names, not `-er` suffix.

```go
// Defined in server.go, not in pkg/k8s/
type KubernetesProxy interface {
    ProxyRequest(c *gin.Context, path string)
    IsAvailable() bool
}
```

For single-method behaviors, use callback functions instead of interfaces:

```go
activityRecorder     func()
healthStatusProvider func() HealthStatus
```

### Concurrency

- `sync.WaitGroup` for goroutine lifecycle (`wg.Add(1)`, `defer wg.Done()`)
- `sync.RWMutex` with separate mutexes per concern (`stateMutex`, `connMutex`)
- `sync.Once` for one-time init (`clusterDNSDomainOnce`)
- `atomic.Bool` for lock-free flags (`reconnecting`, `closed`)
- Pointer receivers exclusively

### Testing Patterns

Tests use `testing` + `github.com/stretchr/testify` (`assert` for non-fatal, `require` for fatal).

```go
func TestManager_New(t *testing.T) {
    tests := []struct {
        name        string
        config      ManagerConfig
        wantErr     bool
        errContains string
    }{
        {name: "valid config", config: validConfig},
        {name: "empty gateway URL", config: badConfig, wantErr: true, errContains: "gateway"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            m, err := NewManager(tt.config)
            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errContains)
                return
            }
            require.NoError(t, err)
            assert.NotNil(t, m)
        })
    }
}
```

- **Table-driven tests** with struct slices and `t.Run()`
- **Mocks/stubs** defined in test files (not shared): `mockCommClient`, `fakeKubernetesProxy`
- **Test helpers**: `getTestConfig()` returns pre-built `*types.Config`
- **HTTP tests**: `httptest.NewServer()` for WebSocket/HTTP, `gin.CreateTestContext()` for gin
- **Async assertions**: `sync/atomic` types, `time.Sleep()` for goroutine completion
- **Comment markers**: `// CRITICAL:`, `// NOTE:`, `// PERFORMANCE FIX:` for important annotations

## CI Pipeline

GitHub Actions (`.github/workflows/ci.yml`): test (race + coverage + golangci-lint + gofmt), security (go vet + Trivy), build (cross-compile linux/darwin x amd64/arm64), Docker multi-arch, Helm package/publish, auto-release.

## Docker

```bash
make docker               # Build production image (multi-stage: golang:1.24-alpine -> scratch)
docker compose up         # Dev environment with live reload (Air)
```
