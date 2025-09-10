# Dual Connections Feature

## Overview

The PipeOps VM Agent now supports dual connections architecture, allowing simultaneous WebSocket connections to both the Control Plane and Runner. This provides better scalability and separation of concerns.

## Architecture

```
Agent
├── Control Plane Connection (Management & Control)
│   ├── Registration
│   ├── Heartbeat & Status
│   ├── Runner Assignment
│   └── Management Commands
└── Runner Connection (Application Operations)
    ├── Deployment Operations
    ├── Scaling Commands
    ├── Resource Queries
    └── Kubernetes API Proxy
```

## Key Components

### 1. Communication Client (`internal/communication/client.go`)

**Dual Connection Support:**
- `EnableDualConnections(runnerEndpoint, runnerToken string)` - Enables dual mode and connects to Runner
- `IsControlPlaneConnected()` - Checks Control Plane connection status
- `IsRunnerConnected()` - Checks Runner connection status
- `SendMessageTo(msg *Message, endpoint MessageEndpoint)` - Send to specific endpoint

**Connection Management:**
- Automatic reconnection for both connections
- Message routing based on endpoint
- Graceful handling of partial connectivity

### 2. Agent Logic (`internal/agent/agent.go`)

**Runner Assignment Handler:**
- Processes `MessageTypeRunnerAssigned` from Control Plane
- Automatically establishes Runner connection
- Reports connection status back to Control Plane

**Enhanced Heartbeat:**
- Reports both Control Plane and Runner connection status
- Provides visibility into dual connection health

## Message Flow

### 1. Initial Registration
```
Agent → Control Plane: Registration (single connection)
Control Plane → Agent: Registration Acknowledgment
```

### 2. Runner Assignment
```
Control Plane → Agent: Runner Assignment (runner_assigned)
Agent: EnableDualConnections(runnerEndpoint, runnerToken)
Agent → Runner: Establish Connection
Agent → Control Plane: Assignment Success Response
```

### 3. Operational Messages
```
# Management via Control Plane
Control Plane → Agent: Management Commands

# Operations via Runner  
Runner → Agent: Deploy/Scale/Query Commands
Agent → Kubernetes: Execute Operations
Agent → Runner: Operation Results
```

## Configuration

### Control Plane Connection
- Configured via `config.ControlPlane.Endpoint`
- Uses agent token for authentication

### Runner Connection  
- Endpoint and token provided via Runner Assignment message
- Connection established automatically when assignment received

## Error Handling

### Partial Connectivity
- Agent continues operating with available connections
- Heartbeat reports individual connection status
- Automatic reconnection attempts for failed connections

### Connection Failures
- Control Plane failure: Agent retries registration
- Runner failure: Agent retries connection, reports to Control Plane
- Both failures: Agent enters retry mode for both connections

## Monitoring

### Connection Status
Monitor via heartbeat messages:
```json
{
  "connections": {
    "control_plane": true,
    "runner": false
  }
}
```

### Logs
- Connection establishment/failure logs
- Message routing logs  
- Reconnection attempt logs

## Future Enhancements

1. **Load Balancing**: Support multiple Runner connections
2. **Failover**: Automatic Runner failover capabilities
3. **Metrics**: Detailed connection and message metrics
4. **Health Checks**: Active health checking for both connections
