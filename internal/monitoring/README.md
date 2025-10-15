# Monitoring Stack Package

This package provides automatic installation and management of monitoring tools (Prometheus, Loki, OpenCost, Grafana) on Kubernetes clusters managed by PipeOps Agent.

## Overview

The monitoring package automatically:
- Installs monitoring tools using Helm charts
- Configures Chisel tunnels for secure access
- Reports service URLs and credentials to control plane
- Performs health checks on services

## Components

### Manager (`manager.go`)
Main orchestrator for the monitoring stack. Handles:
- Installation lifecycle
- Health monitoring
- Credential management
- Tunnel configuration

### Helm Installer (`helm.go`)
Manages Helm chart installations:
- Repository management
- Chart installation and upgrades
- Namespace creation
- Values file handling

### Port Forwarder (`forward.go`)
Configures network access:
- Port forward management
- Health check endpoints
- Service connectivity monitoring

### Defaults (`defaults.go`)
Default configurations for:
- Monitoring stack settings
- Chart versions
- Port assignments
- Credentials

## Usage

### Basic Usage

```go
import "github.com/pipeops/pipeops-vm-agent/internal/monitoring"

// Create default monitoring stack
stack := monitoring.DefaultMonitoringStack()

// Initialize manager
manager, err := monitoring.NewManager(stack, logger)
if err != nil {
    log.Fatal(err)
}

// Start monitoring stack (installs all components)
if err := manager.Start(); err != nil {
    log.Fatal(err)
}

// Get monitoring info for control plane
info := manager.GetMonitoringInfo()
// Returns map with URLs, credentials, etc.

// Get tunnel forwards configuration
forwards := manager.GetTunnelForwards()
// Returns tunnel configuration for Chisel

// Health check
health := manager.HealthCheck()
// Returns health status of all services
```

### Custom Configuration

```go
stack := &monitoring.MonitoringStack{
    Prometheus: &monitoring.PrometheusConfig{
        Enabled:      true,
        Namespace:    "custom-monitoring",
        ReleaseName:  "prom",
        ChartRepo:    "https://prometheus-community.github.io/helm-charts",
        ChartName:    "prometheus-community/prometheus",
        ChartVersion: "15.0.0",
        LocalPort:    9090,
        RemotePort:   19090,
        Username:     "admin",
        Password:     "secure-password",
        SSL:          true,
    },
    // ... configure other services
}
```

## Monitoring Services

### Prometheus
- **Purpose**: Metrics collection and time-series storage
- **Default Port**: 9090 (local), 19090 (tunnel)
- **Chart**: `prometheus-community/prometheus`
- **Health Endpoint**: `http://localhost:9090/-/healthy`

### Loki
- **Purpose**: Log aggregation and storage
- **Default Port**: 3100 (local), 13100 (tunnel)
- **Chart**: `grafana/loki-stack`
- **Health Endpoint**: `http://localhost:3100/ready`

### OpenCost
- **Purpose**: Kubernetes cost monitoring
- **Default Port**: 9003 (local), 19003 (tunnel)
- **Chart**: `opencost/opencost`
- **Health Endpoint**: `http://localhost:9003/healthz`

### Grafana
- **Purpose**: Metrics visualization and dashboards
- **Default Port**: 3000 (local), 13000 (tunnel)
- **Chart**: `grafana/grafana`
- **Health Endpoint**: `http://localhost:3000/api/health`

## Integration with Agent

The monitoring package integrates with the main agent:

```go
// In agent.go
type Agent struct {
    // ... other fields
    monitoringManager *monitoring.Manager
}

func New(config *types.Config) (*Agent, error) {
    // ... initialize agent
    
    if config.Monitoring != nil && config.Monitoring.Enabled {
        stack := convertConfigToStack(config.Monitoring)
        mgr, err := monitoring.NewManager(stack, logger)
        if err != nil {
            return nil, err
        }
        agent.monitoringManager = mgr
    }
    
    return agent, nil
}

func (a *Agent) Start() error {
    // ... start other services
    
    if a.monitoringManager != nil {
        if err := a.monitoringManager.Start(); err != nil {
            logger.WithError(err).Error("Failed to start monitoring")
        }
    }
    
    // ... continue startup
}
```

## Control Plane Integration

Monitoring information is sent to control plane via heartbeat:

```go
func (a *Agent) sendHeartbeat() error {
    heartbeat := &controlplane.HeartbeatRequest{
        // ... standard fields
    }
    
    if a.monitoringManager != nil {
        info := a.monitoringManager.GetMonitoringInfo()
        heartbeat.PrometheusURL = info["prometheus_url"].(string)
        heartbeat.PrometheusUsername = info["prometheus_username"].(string)
        // ... populate other monitoring fields
    }
    
    return a.controlPlane.SendHeartbeat(ctx, heartbeat)
}
```

## Tunnel Configuration

Monitoring services are exposed via Chisel tunnels:

```go
forwards := monitoringManager.GetTunnelForwards()
// Returns:
// [
//   {Name: "prometheus", LocalAddr: "localhost:9090", RemotePort: 19090},
//   {Name: "loki", LocalAddr: "localhost:3100", RemotePort: 13100},
//   {Name: "opencost", LocalAddr: "localhost:9003", RemotePort: 19003},
//   {Name: "grafana", LocalAddr: "localhost:3000", RemotePort: 13000}
// ]

// Add to tunnel manager
for _, fwd := range forwards {
    tunnelMgr.AddForward(fwd)
}
```

## Health Monitoring

```go
// Continuous health monitoring
go func() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            health := manager.HealthCheck()
            for service, status := range health {
                if !status {
                    logger.WithField("service", service).Warn("Service unhealthy")
                }
            }
        case <-ctx.Done():
            return
        }
    }
}()
```

## Error Handling

The monitoring package is designed to be non-fatal:
- Installation errors are logged but don't stop agent startup
- Health check failures are reported but don't affect agent operation
- Missing Helm is detected and logged clearly

## Dependencies

### External Tools
- **Helm**: Required for chart installation
- **kubectl**: Used for namespace creation

### Go Packages
- `github.com/sirupsen/logrus`: Logging
- `gopkg.in/yaml.v3`: Configuration parsing
- Standard library: `os/exec`, `context`, `net/http`

## Future Enhancements

- [ ] Custom dashboard deployment
- [ ] AlertManager integration
- [ ] Prometheus recording rules
- [ ] Loki log forwarding to external systems
- [ ] OpenCost report generation
- [ ] Multi-cluster aggregation
- [ ] High-availability configurations
- [ ] Backup and restore functionality

## Testing

```bash
# Run unit tests
go test ./internal/monitoring/...

# Run with coverage
go test -cover ./internal/monitoring/...

# Run specific test
go test -v -run TestManager ./internal/monitoring/
```

## Documentation

- [Monitoring Stack Architecture](../../docs/MONITORING_STACK.md)
- [Implementation Plan](../../docs/MONITORING_IMPLEMENTATION_PLAN.md)
- [Control Plane Integration](../../docs/ARCHITECTURE.md)

## License

Copyright © 2025 PipeOps. All rights reserved.
