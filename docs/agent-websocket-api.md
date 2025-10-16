# Agent WebSocket API Documentation

## Overview

The Agent WebSocket API provides a persistent, bidirectional communication channel between PipeOps agents and the controller. This replaces the need for HTTP polling and provides real-time communication capabilities.

## Benefits

- **Native Health Checks**: Uses WebSocket ping/pong for health monitoring (no HTTP polling required)
- **Real-time Communication**: Instant bidirectional message passing
- **Lower Latency**: Persistent connections eliminate HTTP request overhead
- **Efficient Resource Usage**: Single connection vs multiple HTTP requests
- **Connection State Awareness**: Immediate detection of disconnections

## Endpoint

```
GET /api/v1/clusters/agent/ws
```

### Authentication

Agents authenticate using a cluster token in one of two ways:

1. **Query Parameter** (Recommended):
   ```
   wss://api.pipeops.io/api/v1/clusters/agent/ws?token=<cluster_token>
   ```

2. **Authorization Header**:
   ```
   Authorization: Bearer <cluster_token>
   ```

## WebSocket Protocol

### Connection Flow

1. Agent connects to WebSocket endpoint with cluster token
2. Controller validates token
3. Connection established with 30-second ping/pong health checks
4. Agent sends registration message
5. Bidirectional communication begins

### Message Format

All messages are JSON objects with the following structure:

```json
{
  "type": "message_type",
  "request_id": "optional-unique-id",
  "payload": {},
  "timestamp": "2025-10-16T12:00:00Z"
}
```

#### Fields

- `type` (string, required): Message type identifier
- `request_id` (string, optional): Unique ID for request/response correlation
- `payload` (object, required): Message-specific data
- `timestamp` (string, required): ISO 8601 timestamp

## Message Types

### 1. Registration

**Type**: `register`

**Direction**: Agent → Controller

**Purpose**: Register a new cluster or update existing cluster information

**Payload**:
```json
{
  "agent_id": "unique-agent-identifier",
  "name": "cluster-name",
  "k8s_version": "1.27.0",
  "hostname": "agent-hostname",
  "agent_version": "1.0.0",
  "server_ip": "10.0.0.1",
  "region": "us-east-1",
  "cloud_provider": "aws",
  "server_specs": {
    "cpu_cores": 4,
    "memory_gb": 16,
    "disk_gb": 100,
    "os": "Ubuntu 22.04"
  },
  "tunnel_port_config": {
    "kubernetes_api": 6443,
    "kubelet": 10250,
    "agent_http": 8080
  },
  "labels": {
    "environment": "production",
    "team": "platform"
  }
}
```

**Response**:
```json
{
  "type": "register_success",
  "request_id": "matching-request-id",
  "payload": {
    "cluster_id": "cluster-uuid",
    "cluster_uuid": "cluster-uuid",
    "name": "cluster-name",
    "status": "registered",
    "tunnel_url": "https://api.pipeops.io/clusters/agent-id",
    "registered_at": "2025-10-16T12:00:00Z",
    "workspace_id": 123
  },
  "timestamp": "2025-10-16T12:00:00Z"
}
```

### 2. Heartbeat

**Type**: `heartbeat`

**Direction**: Agent → Controller

**Purpose**: Update cluster status and metrics

**Payload**:
```json
{
  "tunnel_status": "connected",
  "status": "available",
  "metadata": {
    "version": "1.0.0",
    "k8s_nodes": 3,
    "k8s_pods": 25,
    "cpu_usage": "45%",
    "memory_usage": "60%"
  }
}
```

**Response**:
```json
{
  "type": "heartbeat_ack",
  "request_id": "matching-request-id",
  "payload": {
    "received_at": "2025-10-16T12:00:00Z"
  },
  "timestamp": "2025-10-16T12:00:00Z"
}
```

**Note**: In addition to application-level heartbeats, the WebSocket connection uses native ping/pong frames every 30 seconds for connection health monitoring.

### 3. Status Update

**Type**: `status`

**Direction**: Agent → Controller

**Purpose**: Send detailed status updates

**Payload**: Same as heartbeat

**Response**: Same as heartbeat_ack

### 4. Ping

**Type**: `ping`

**Direction**: Agent → Controller or Controller → Agent

**Purpose**: Application-level ping (in addition to WebSocket ping/pong)

**Payload**: Empty object `{}`

**Response**:
```json
{
  "type": "pong",
  "request_id": "matching-request-id",
  "payload": {
    "timestamp": "2025-10-16T12:00:00Z"
  },
  "timestamp": "2025-10-16T12:00:00Z"
}
```

### 5. Error

**Type**: `error`

**Direction**: Controller → Agent

**Purpose**: Communicate errors to the agent

**Payload**:
```json
{
  "error": "Error message description"
}
```

## Connection Management

### Ping/Pong Health Checks

The controller sends WebSocket ping frames every 30 seconds. Agents must respond with pong frames to maintain the connection. If no pong is received within 60 seconds, the connection is considered stale and will be closed.

### Reconnection

Agents should implement exponential backoff reconnection logic:

1. Initial retry: 1 second
2. Subsequent retries: Double the delay up to 60 seconds
3. Maximum retry interval: 60 seconds

### Graceful Shutdown

When shutting down:

1. Agent sends close frame
2. Controller responds with close frame
3. Connection terminates

## Migration from HTTP API

### HTTP Endpoints (Legacy - Still Supported)

The following HTTP endpoints remain available for backward compatibility:

- `POST /api/v1/clusters/agent/register` - Registration
- `POST /api/v1/clusters/agent/:cluster_id/heartbeat` - Heartbeat
- `GET /api/v1/clusters/agent/:cluster_id/poll` - Polling for commands
- `GET /api/v1/clusters/agent/:cluster_id/tunnel-info` - Tunnel info

### Migration Steps

1. Update agent to support WebSocket connection
2. Implement message handlers for each message type
3. Test WebSocket communication in staging
4. Gradual rollout to production
5. Monitor connection stability and performance
6. Eventually deprecate HTTP polling endpoints

## Example: Node.js Agent

```javascript
const WebSocket = require('ws');

class PipeOpsAgent {
  constructor(token, apiUrl) {
    this.token = token;
    this.apiUrl = apiUrl;
    this.ws = null;
    this.reconnectDelay = 1000;
  }

  connect() {
    const wsUrl = `${this.apiUrl}/api/v1/clusters/agent/ws?token=${this.token}`;
    this.ws = new WebSocket(wsUrl);

    this.ws.on('open', () => {
      console.log('Connected to PipeOps controller');
      this.reconnectDelay = 1000;
      this.register();
    });

    this.ws.on('message', (data) => {
      const message = JSON.parse(data);
      this.handleMessage(message);
    });

    this.ws.on('close', () => {
      console.log('Disconnected from PipeOps controller');
      this.reconnect();
    });

    this.ws.on('error', (error) => {
      console.error('WebSocket error:', error);
    });

    // Handle ping/pong
    this.ws.on('ping', () => {
      this.ws.pong();
    });
  }

  register() {
    this.send({
      type: 'register',
      request_id: this.generateRequestId(),
      payload: {
        agent_id: 'my-agent-123',
        name: 'my-cluster',
        k8s_version: '1.27.0',
        hostname: os.hostname(),
        agent_version: '1.0.0',
        // ... other fields
      },
      timestamp: new Date().toISOString()
    });
  }

  sendHeartbeat() {
    this.send({
      type: 'heartbeat',
      payload: {
        tunnel_status: 'connected',
        status: 'available',
        metadata: {
          // ... metrics
        }
      },
      timestamp: new Date().toISOString()
    });
  }

  handleMessage(message) {
    switch (message.type) {
      case 'register_success':
        console.log('Registration successful:', message.payload);
        this.startHeartbeat();
        break;
      case 'heartbeat_ack':
        console.log('Heartbeat acknowledged');
        break;
      case 'pong':
        console.log('Pong received');
        break;
      case 'error':
        console.error('Error from controller:', message.payload.error);
        break;
      default:
        console.warn('Unknown message type:', message.type);
    }
  }

  send(message) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    }
  }

  reconnect() {
    setTimeout(() => {
      console.log(`Reconnecting in ${this.reconnectDelay}ms...`);
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 60000);
      this.connect();
    }, this.reconnectDelay);
  }

  startHeartbeat() {
    setInterval(() => {
      this.sendHeartbeat();
    }, 30000); // Every 30 seconds
  }

  generateRequestId() {
    return `req_${Date.now()}_${Math.random().toString(36).substring(7)}`;
  }
}

// Usage
const agent = new PipeOpsAgent(
  'your-cluster-token',
  'wss://api.pipeops.io'
);
agent.connect();
```

## Example: Go Agent

```go
package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type WebSocketMessage struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"request_id,omitempty"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

type Agent struct {
	token string
	apiURL string
	conn *websocket.Conn
}

func (a *Agent) Connect() error {
	wsURL := a.apiURL + "/api/v1/clusters/agent/ws?token=" + a.token
	
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}
	
	a.conn = conn
	log.Println("Connected to PipeOps controller")
	
	// Handle messages
	go a.readMessages()
	
	// Register
	a.register()
	
	// Start heartbeat
	go a.heartbeat()
	
	return nil
}

func (a *Agent) register() {
	msg := WebSocketMessage{
		Type: "register",
		RequestID: generateRequestID(),
		Payload: map[string]interface{}{
			"agent_id": "my-agent-123",
			"name": "my-cluster",
			"k8s_version": "1.27.0",
			// ... other fields
		},
		Timestamp: time.Now(),
	}
	
	a.send(msg)
}

func (a *Agent) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for range ticker.C {
		msg := WebSocketMessage{
			Type: "heartbeat",
			Payload: map[string]interface{}{
				"tunnel_status": "connected",
				"status": "available",
			},
			Timestamp: time.Now(),
		}
		
		a.send(msg)
	}
}

func (a *Agent) readMessages() {
	for {
		var msg WebSocketMessage
		err := a.conn.ReadJSON(&msg)
		if err != nil {
			log.Println("Read error:", err)
			return
		}
		
		a.handleMessage(msg)
	}
}

func (a *Agent) handleMessage(msg WebSocketMessage) {
	switch msg.Type {
	case "register_success":
		log.Println("Registration successful:", msg.Payload)
	case "heartbeat_ack":
		log.Println("Heartbeat acknowledged")
	case "pong":
		log.Println("Pong received")
	case "error":
		log.Println("Error:", msg.Payload["error"])
	default:
		log.Println("Unknown message type:", msg.Type)
	}
}

func (a *Agent) send(msg WebSocketMessage) error {
	return a.conn.WriteJSON(msg)
}

func generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}
```

## Security Considerations

1. **Token Security**: Always use HTTPS/WSS in production
2. **Token Rotation**: Implement regular token rotation
3. **Rate Limiting**: Controller enforces rate limits on connections
4. **Connection Limits**: Maximum connections per token enforced
5. **Timeout**: Idle connections closed after 60 seconds of inactivity

## Troubleshooting

### Connection Fails

- Check token validity and permissions
- Verify network connectivity and firewall rules
- Ensure WebSocket support in infrastructure (load balancers, proxies)

### Frequent Disconnections

- Check network stability
- Verify ping/pong responses
- Review connection logs for errors

### Registration Fails

- Verify all required fields are provided
- Check token has `cluster:register` scope
- Ensure cluster name is unique or handle re-registration

## Future Enhancements

- Command push from controller to agent
- File transfer over WebSocket
- Metrics streaming
- Log streaming
- Interactive shell sessions
