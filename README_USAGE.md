# PipeOps VM Agent

A secure Kubernetes agent for PipeOps that provides K8s API proxy functionality without exposing the cluster API directly.

## Overview

The PipeOps VM Agent enables secure communication between PipeOps Control Plane/Runner and Kubernetes clusters by:

1. **Agent-based Architecture**: Outbound-only connections from the VM to PipeOps Control Plane
2. **K8s API Proxy**: WebSocket-based proxy that allows Runner to interact with Kubernetes API
3. **Secure Communication**: TLS-encrypted connections with token-based authentication
4. **Full API Compatibility**: Supports both read and write operations, including streaming (WATCH)

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   PipeOps       │    │   VM Agent      │    │   Kubernetes    │
│   Runner        │◄──►│   (This Code)   │◄──►│   Cluster       │
│                 │    │                 │    │   (k3s/etc)     │
└─────────────────┘    └─────────────────┘    └─────────────────┘
       │                        │                        │
       │                        │                        │
   WebSocket                HTTP/gRPC                 K8s API
   K8s API Proxy          to Control Plane
```

## Features

- **Full Kubernetes API Proxy**: Support for all K8s API operations (GET, POST, PUT, DELETE, PATCH)
- **Streaming Support**: WATCH operations for real-time cluster monitoring
- **WebSocket Communication**: Efficient, persistent connections
- **Security**: Token-based authentication, TLS encryption
- **Monitoring**: Health checks, metrics, status reporting
- **Graceful Shutdown**: Proper cleanup of connections and resources

## Quick Start

### 1. Build the Agent

```bash
make build
```

### 2. Configure the Agent

Copy the example configuration and customize it:

```bash
cp config.example.yaml config.yaml
# Edit config.yaml with your settings
```

Key configuration fields:
- `pipeops.api_url`: PipeOps Control Plane URL
- `pipeops.token`: Authentication token for PipeOps
- `agent.cluster_name`: Name of your Kubernetes cluster
- `agent.port`: Port for the HTTP server (default: 8080)

### 3. Run the Agent

```bash
# Using config file
./bin/pipeops-agent --config config.yaml

# Using environment variables
export PIPEOPS_PIPEOPS_API_URL="https://api.pipeops.io"
export PIPEOPS_PIPEOPS_TOKEN="your-token"
export PIPEOPS_AGENT_CLUSTER_NAME="my-cluster"
./bin/pipeops-agent

# Using command line flags
./bin/pipeops-agent \\
  --pipeops-url "https://api.pipeops.io" \\
  --token "your-token" \\
  --cluster-name "my-cluster"
```

## API Endpoints

The VM Agent exposes several HTTP endpoints:

### Health and Status
- `GET /health` - Health check
- `GET /ready` - Readiness check  
- `GET /metrics` - Agent and cluster metrics

### WebSocket Connections
- `GET /ws/control-plane` - Control Plane connection
- `GET /ws/runner?runner_id=<id>` - Runner connection
- `GET /ws/k8s` - Kubernetes API proxy (for Runner)

## Kubernetes API Proxy Usage

The Runner connects to `/ws/k8s` to interact with the Kubernetes cluster. Example usage:

### Basic Request
```javascript
{
  "id": "unique-request-id",
  "method": "GET",
  "path": "/api/v1/namespaces/default/pods",
  "headers": {
    "Accept": "application/json"
  }
}
```

### Streaming Request (WATCH)
```javascript
{
  "id": "watch-request-id", 
  "method": "GET",
  "path": "/api/v1/namespaces/default/pods",
  "query": {"watch": ["true"]},
  "stream": true
}
```

See `examples/runner_client.go` for a complete example.

## Development

### Prerequisites
- Go 1.21+
- Kubernetes cluster (k3s, kind, etc.)
- kubectl configured

### Running Tests
```bash
make test
```

### Running Locally
```bash
# In one terminal, start the agent
go run cmd/agent/main.go --debug --port 8080

# In another terminal, test the runner client
go run examples/runner_client.go
```

## Configuration Reference

### Agent Configuration
```yaml
agent:
  id: "unique-agent-id"           # Auto-generated if not specified
  name: "my-agent"                # Human-readable name
  cluster_name: "my-cluster"      # Required: cluster identifier
  poll_interval: "30s"            # Status reporting interval
  port: 8080                      # HTTP server port
  debug: false                    # Enable debug logging
  labels:                         # Key-value labels for the agent
    environment: "production"
```

### PipeOps Configuration
```yaml
pipeops:
  api_url: "https://api.pipeops.io"  # Required: PipeOps API URL
  token: "your-token"                # Required: Authentication token
  timeout: "30s"                     # Connection timeout
  tls:
    enabled: true                    # Enable TLS
    insecure_skip_verify: false      # Skip TLS verification (dev only)
  reconnect:
    enabled: true                    # Enable auto-reconnect
    max_attempts: 10                 # Max reconnection attempts
    interval: "5s"                   # Reconnect interval
```

### Kubernetes Configuration
```yaml
kubernetes:
  kubeconfig: "/path/to/kubeconfig"  # Path to kubeconfig (optional if in-cluster)
  in_cluster: false                  # Use in-cluster config
  namespace: "pipeops-system"        # Default namespace
```

## Security Considerations

1. **Network Security**: Agent only makes outbound connections
2. **Authentication**: Token-based auth for all communications
3. **TLS**: All connections encrypted in transit
4. **RBAC**: Configure appropriate Kubernetes RBAC for the agent
5. **Firewall**: Only outbound HTTPS/WSS connections required

## Troubleshooting

### Common Issues

1. **Connection refused**: Check network connectivity to PipeOps Control Plane
2. **Authentication failed**: Verify your PipeOps token
3. **K8s API errors**: Check kubeconfig and RBAC permissions
4. **WebSocket errors**: Ensure firewall allows WebSocket connections

### Debug Mode
Enable debug logging:
```bash
./bin/pipeops-agent --log-level debug
```

### Logs
The agent logs important events including:
- Connection status with Control Plane
- Runner connections
- K8s API proxy requests
- Error conditions

## License

[License information here]
