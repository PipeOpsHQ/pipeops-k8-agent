# Agent Migration Guide: HTTP to WebSocket

## ⚠️ URGENT: HTTP Endpoints Deprecated

**All HTTP agent endpoints are deprecated and will be removed in 30 days.**

This guide will help you migrate your agents from HTTP polling to WebSocket communication.

## Why Migrate?

### Current HTTP Approach (Deprecated)
- ❌ Requires polling every 5-30 seconds
- ❌ High latency (30+ seconds for commands)
- ❌ Inefficient resource usage
- ❌ No native health checks
- ❌ Delayed connection state awareness

### New WebSocket Approach (Required)
- ✅ Real-time bidirectional communication
- ✅ Native ping/pong health checks
- ✅ Lower latency (<100ms)
- ✅ Efficient resource usage
- ✅ Instant connection state awareness
- ✅ Full backward compatibility (during transition)

## Timeline

- **Day 0**: HTTP endpoints deprecated (warnings added)
- **Day 30**: HTTP endpoints will be removed
- **Action Required**: Migrate within 30 days

## Migration Overview

### Quick Migration Checklist

- [ ] Review this migration guide
- [ ] Understand the WebSocket API (see [agent-websocket-api.md](./agent-websocket-api.md))
- [ ] Update agent code to use WebSocket
- [ ] Implement message handlers
- [ ] Test in staging environment
- [ ] Deploy to production
- [ ] Monitor for successful migration
- [ ] Remove HTTP polling code

### Estimated Migration Time

- **Simple agents**: 2-4 hours
- **Complex agents**: 1-2 days
- **Testing and deployment**: 1-2 days

**Total**: 3-5 days for most teams

## Step-by-Step Migration

### Step 1: Understand the Current HTTP Flow

Your agent currently uses HTTP endpoints:

```
1. Registration (once at startup):
   POST /api/v1/clusters/agent/register

2. Heartbeat (every 5 seconds):
   POST /api/v1/clusters/agent/:cluster_id/heartbeat

3. Polling (every 5-30 seconds):
   GET /api/v1/clusters/agent/:cluster_id/poll
```

### Step 2: Understand the New WebSocket Flow

The WebSocket API consolidates everything into one persistent connection:

```
1. Connect to WebSocket endpoint:
   wss://api.pipeops.io/api/v1/clusters/agent/ws?token=<token>

2. Send registration message (once)

3. Send heartbeats (every 30 seconds)

4. Receive commands in real-time (no polling needed)
```

### Step 3: Update Your Code

#### Before: HTTP Registration

```go
// HTTP-based registration (deprecated)
func (a *Agent) register() error {
    endpoint := fmt.Sprintf("%s/api/v1/clusters/agent/register", a.apiURL)
    
    payload := map[string]interface{}{
        "agent_id": a.agentID,
        "name": a.clusterName,
        // ... other fields
    }
    
    resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(jsonData))
    // ... handle response
}
```

#### After: WebSocket Registration

```go
// WebSocket-based registration (new)
func (a *Agent) connect() error {
    wsURL := fmt.Sprintf("%s/api/v1/clusters/agent/ws?token=%s", a.apiURL, a.token)
    
    conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        return err
    }
    
    a.conn = conn
    
    // Send registration message
    msg := WebSocketMessage{
        Type: "register",
        RequestID: generateRequestID(),
        Payload: map[string]interface{}{
            "agent_id": a.agentID,
            "name": a.clusterName,
            // ... other fields
        },
        Timestamp: time.Now(),
    }
    
    return a.conn.WriteJSON(msg)
}
```

#### Before: HTTP Heartbeat

```go
// HTTP-based heartbeat (deprecated)
func (a *Agent) sendHeartbeat() error {
    endpoint := fmt.Sprintf("%s/api/v1/clusters/agent/%s/heartbeat", 
        a.apiURL, a.clusterID)
    
    payload := map[string]interface{}{
        "cluster_id": a.clusterID,
        "status": "active",
        // ... other fields
    }
    
    resp, err := http.Post(endpoint, "application/json", bytes.NewBuffer(jsonData))
    // ... handle response
}

// Called in a loop
func (a *Agent) startHeartbeat() {
    ticker := time.NewTicker(5 * time.Second)
    for range ticker.C {
        a.sendHeartbeat()
    }
}
```

#### After: WebSocket Heartbeat

```go
// WebSocket-based heartbeat (new)
func (a *Agent) sendHeartbeat() error {
    msg := WebSocketMessage{
        Type: "heartbeat",
        Payload: map[string]interface{}{
            "tunnel_status": "connected",
            "status": "available",
            "metadata": map[string]interface{}{
                "version": a.version,
                // ... other metrics
            },
        },
        Timestamp: time.Now(),
    }
    
    return a.conn.WriteJSON(msg)
}

// Called in a loop (same as before)
func (a *Agent) startHeartbeat() {
    ticker := time.NewTicker(30 * time.Second)
    for range ticker.C {
        a.sendHeartbeat()
    }
}
```

#### Before: HTTP Polling

```go
// HTTP-based polling (deprecated)
func (a *Agent) poll() error {
    endpoint := fmt.Sprintf("%s/api/v1/clusters/agent/%s/poll", 
        a.apiURL, a.clusterID)
    
    resp, err := http.Get(endpoint)
    // ... handle commands
}

// Called in a loop
func (a *Agent) startPolling() {
    ticker := time.NewTicker(30 * time.Second)
    for range ticker.C {
        a.poll()
    }
}
```

#### After: WebSocket Message Handling

```go
// WebSocket-based message handling (new)
func (a *Agent) readMessages() {
    for {
        var msg WebSocketMessage
        err := a.conn.ReadJSON(&msg)
        if err != nil {
            log.Println("Read error:", err)
            a.reconnect()
            return
        }
        
        a.handleMessage(msg)
    }
}

func (a *Agent) handleMessage(msg WebSocketMessage) {
    switch msg.Type {
    case "register_success":
        a.handleRegistrationSuccess(msg)
    case "heartbeat_ack":
        // Heartbeat acknowledged
    case "command":
        a.handleCommand(msg)
    case "ping":
        a.sendPong(msg.RequestID)
    case "error":
        log.Println("Error:", msg.Payload["error"])
    default:
        log.Println("Unknown message type:", msg.Type)
    }
}
```

### Step 4: Implement Reconnection Logic

WebSocket connections can drop, so implement exponential backoff:

```go
func (a *Agent) reconnect() {
    delay := 1 * time.Second
    maxDelay := 60 * time.Second
    
    for {
        log.Printf("Reconnecting in %v...", delay)
        time.Sleep(delay)
        
        if err := a.connect(); err != nil {
            log.Println("Reconnection failed:", err)
            delay = time.Duration(math.Min(float64(delay*2), float64(maxDelay)))
            continue
        }
        
        log.Println("Reconnected successfully")
        break
    }
}
```

### Step 5: Handle Ping/Pong

The controller sends ping frames every 30 seconds. Most WebSocket libraries handle this automatically:

```go
// gorilla/websocket handles ping/pong automatically
// No additional code needed in most cases

// If you need custom handling:
func (a *Agent) setupPingHandler() {
    a.conn.SetPingHandler(func(appData string) error {
        log.Println("Received ping, sending pong")
        return a.conn.WriteControl(
            websocket.PongMessage,
            []byte(appData),
            time.Now().Add(time.Second),
        )
    })
}
```

### Step 6: Update Error Handling

WebSocket errors are different from HTTP errors:

```go
// Check for connection errors
func (a *Agent) send(msg WebSocketMessage) error {
    if a.conn == nil {
        return fmt.Errorf("connection not established")
    }
    
    err := a.conn.WriteJSON(msg)
    if err != nil {
        // Connection lost, trigger reconnection
        go a.reconnect()
        return err
    }
    
    return nil
}
```

## Complete Migration Examples

### Go Agent (Complete Example)

```go
package main

import (
    "encoding/json"
    "fmt"
    "log"
    "math"
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
    token       string
    apiURL      string
    agentID     string
    clusterName string
    conn        *websocket.Conn
}

func NewAgent(token, apiURL, agentID, clusterName string) *Agent {
    return &Agent{
        token:       token,
        apiURL:      apiURL,
        agentID:     agentID,
        clusterName: clusterName,
    }
}

func (a *Agent) Start() error {
    if err := a.connect(); err != nil {
        return err
    }
    
    // Start message reader
    go a.readMessages()
    
    // Start heartbeat
    go a.startHeartbeat()
    
    return nil
}

func (a *Agent) connect() error {
    wsURL := fmt.Sprintf("%s/api/v1/clusters/agent/ws?token=%s", 
        a.apiURL, a.token)
    
    conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        return fmt.Errorf("failed to connect: %w", err)
    }
    
    a.conn = conn
    log.Println("Connected to PipeOps controller")
    
    // Send registration message
    return a.register()
}

func (a *Agent) register() error {
    msg := WebSocketMessage{
        Type:      "register",
        RequestID: generateRequestID(),
        Payload: map[string]interface{}{
            "agent_id":      a.agentID,
            "name":          a.clusterName,
            "k8s_version":   "1.27.0",
            "agent_version": "1.0.0",
            // Add other required fields
        },
        Timestamp: time.Now(),
    }
    
    return a.conn.WriteJSON(msg)
}

func (a *Agent) startHeartbeat() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        msg := WebSocketMessage{
            Type: "heartbeat",
            Payload: map[string]interface{}{
                "tunnel_status": "connected",
                "status":        "available",
            },
            Timestamp: time.Now(),
        }
        
        if err := a.conn.WriteJSON(msg); err != nil {
            log.Println("Failed to send heartbeat:", err)
            go a.reconnect()
            return
        }
    }
}

func (a *Agent) readMessages() {
    for {
        var msg WebSocketMessage
        err := a.conn.ReadJSON(&msg)
        if err != nil {
            log.Println("Read error:", err)
            go a.reconnect()
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

func (a *Agent) reconnect() {
    if a.conn != nil {
        a.conn.Close()
    }
    
    delay := 1 * time.Second
    maxDelay := 60 * time.Second
    
    for {
        log.Printf("Reconnecting in %v...", delay)
        time.Sleep(delay)
        
        if err := a.connect(); err != nil {
            log.Println("Reconnection failed:", err)
            delay = time.Duration(math.Min(float64(delay*2), float64(maxDelay)))
            continue
        }
        
        log.Println("Reconnected successfully")
        
        // Restart message reader and heartbeat
        go a.readMessages()
        go a.startHeartbeat()
        
        break
    }
}

func generateRequestID() string {
    return fmt.Sprintf("req_%d", time.Now().UnixNano())
}

func main() {
    agent := NewAgent(
        "your-cluster-token",
        "wss://api.pipeops.io",
        "my-agent-123",
        "my-cluster",
    )
    
    if err := agent.Start(); err != nil {
        log.Fatal("Failed to start agent:", err)
    }
    
    // Keep running
    select {}
}
```

### Node.js Agent (Complete Example)

```javascript
const WebSocket = require('ws');
const os = require('os');

class PipeOpsAgent {
  constructor(token, apiUrl, agentId, clusterName) {
    this.token = token;
    this.apiUrl = apiUrl;
    this.agentId = agentId;
    this.clusterName = clusterName;
    this.ws = null;
    this.reconnectDelay = 1000;
    this.heartbeatInterval = null;
  }

  start() {
    this.connect();
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
      try {
        const message = JSON.parse(data);
        this.handleMessage(message);
      } catch (error) {
        console.error('Failed to parse message:', error);
      }
    });

    this.ws.on('close', () => {
      console.log('Disconnected from PipeOps controller');
      this.cleanup();
      this.reconnect();
    });

    this.ws.on('error', (error) => {
      console.error('WebSocket error:', error);
    });

    this.ws.on('ping', () => {
      this.ws.pong();
    });
  }

  register() {
    this.send({
      type: 'register',
      request_id: this.generateRequestId(),
      payload: {
        agent_id: this.agentId,
        name: this.clusterName,
        k8s_version: '1.27.0',
        hostname: os.hostname(),
        agent_version: '1.0.0',
        // Add other required fields
      },
      timestamp: new Date().toISOString()
    });
  }

  startHeartbeat() {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
    }

    this.heartbeatInterval = setInterval(() => {
      this.send({
        type: 'heartbeat',
        payload: {
          tunnel_status: 'connected',
          status: 'available',
          metadata: {
            version: '1.0.0',
            // Add other metrics
          }
        },
        timestamp: new Date().toISOString()
      });
    }, 30000); // Every 30 seconds
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
    } else {
      console.error('Cannot send message: WebSocket not connected');
    }
  }

  reconnect() {
    setTimeout(() => {
      console.log(`Reconnecting in ${this.reconnectDelay}ms...`);
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 60000);
      this.connect();
    }, this.reconnectDelay);
  }

  cleanup() {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = null;
    }
  }

  generateRequestId() {
    return `req_${Date.now()}_${Math.random().toString(36).substring(7)}`;
  }
}

// Usage
const agent = new PipeOpsAgent(
  'your-cluster-token',
  'wss://api.pipeops.io',
  'my-agent-123',
  'my-cluster'
);

agent.start();
```

### Python Agent (Complete Example)

```python
import asyncio
import json
import time
import websockets
from typing import Dict, Any

class PipeOpsAgent:
    def __init__(self, token: str, api_url: str, agent_id: str, cluster_name: str):
        self.token = token
        self.api_url = api_url
        self.agent_id = agent_id
        self.cluster_name = cluster_name
        self.ws = None
        self.reconnect_delay = 1
        self.max_reconnect_delay = 60

    async def start(self):
        await self.connect()

    async def connect(self):
        ws_url = f"{self.api_url}/api/v1/clusters/agent/ws?token={self.token}"
        
        try:
            self.ws = await websockets.connect(ws_url)
            print("Connected to PipeOps controller")
            self.reconnect_delay = 1
            
            await self.register()
            
            # Start tasks
            await asyncio.gather(
                self.read_messages(),
                self.heartbeat_loop()
            )
        except Exception as e:
            print(f"Connection failed: {e}")
            await self.reconnect()

    async def register(self):
        message = {
            "type": "register",
            "request_id": self.generate_request_id(),
            "payload": {
                "agent_id": self.agent_id,
                "name": self.cluster_name,
                "k8s_version": "1.27.0",
                "agent_version": "1.0.0",
                # Add other required fields
            },
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        }
        
        await self.send(message)

    async def heartbeat_loop(self):
        while True:
            try:
                await asyncio.sleep(30)  # Every 30 seconds
                
                message = {
                    "type": "heartbeat",
                    "payload": {
                        "tunnel_status": "connected",
                        "status": "available",
                        "metadata": {
                            "version": "1.0.0",
                            # Add other metrics
                        }
                    },
                    "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
                }
                
                await self.send(message)
            except Exception as e:
                print(f"Heartbeat failed: {e}")
                break

    async def read_messages(self):
        try:
            async for message in self.ws:
                data = json.loads(message)
                await self.handle_message(data)
        except Exception as e:
            print(f"Read error: {e}")
            await self.reconnect()

    async def handle_message(self, message: Dict[str, Any]):
        msg_type = message.get("type")
        
        if msg_type == "register_success":
            print(f"Registration successful: {message.get('payload')}")
        elif msg_type == "heartbeat_ack":
            print("Heartbeat acknowledged")
        elif msg_type == "pong":
            print("Pong received")
        elif msg_type == "error":
            print(f"Error: {message.get('payload', {}).get('error')}")
        else:
            print(f"Unknown message type: {msg_type}")

    async def send(self, message: Dict[str, Any]):
        if self.ws:
            await self.ws.send(json.dumps(message))

    async def reconnect(self):
        if self.ws:
            await self.ws.close()
        
        print(f"Reconnecting in {self.reconnect_delay} seconds...")
        await asyncio.sleep(self.reconnect_delay)
        
        self.reconnect_delay = min(self.reconnect_delay * 2, self.max_reconnect_delay)
        await self.connect()

    @staticmethod
    def generate_request_id() -> str:
        return f"req_{int(time.time() * 1000)}"

# Usage
async def main():
    agent = PipeOpsAgent(
        token="your-cluster-token",
        api_url="wss://api.pipeops.io",
        agent_id="my-agent-123",
        cluster_name="my-cluster"
    )
    
    await agent.start()

if __name__ == "__main__":
    asyncio.run(main())
```

## Testing Your Migration

### Step 1: Test in Staging

1. Deploy the new WebSocket agent to staging
2. Verify registration succeeds
3. Monitor heartbeats
4. Test reconnection logic
5. Verify command handling (if applicable)

### Step 2: Verify WebSocket Connection

```bash
# Check WebSocket connection
wscat -c "wss://api.pipeops.io/api/v1/clusters/agent/ws?token=YOUR_TOKEN"

# Send registration message
{"type":"register","payload":{"agent_id":"test-agent","name":"test-cluster"},"timestamp":"2025-10-16T12:00:00Z"}
```

### Step 3: Monitor Logs

Look for these log messages:

```
✓ Connected to PipeOps controller
✓ Registration successful
✓ Heartbeat acknowledged
✓ Reconnected successfully (after disconnect)
```

### Step 4: Load Testing

Test under realistic conditions:

```go
// Simulate network issues
func (a *Agent) testReconnection() {
    // Force disconnect
    a.conn.Close()
    
    // Verify automatic reconnection
    time.Sleep(5 * time.Second)
    
    // Verify heartbeat resumes
}
```

## Production Deployment

### Deployment Strategy

1. **Canary Deployment** (Recommended):
   - Deploy WebSocket agent to 10% of clusters
   - Monitor for 24 hours
   - Gradually increase to 50%, then 100%

2. **Blue-Green Deployment**:
   - Deploy new version alongside old
   - Switch traffic when ready
   - Keep old version for quick rollback

3. **Rolling Update**:
   - Update agents one at a time
   - Verify each agent before continuing

### Monitoring

Monitor these metrics:

- **Connection Success Rate**: Should be >99%
- **Reconnection Time**: Should be <10 seconds
- **Heartbeat Success Rate**: Should be >99.9%
- **Message Latency**: Should be <100ms

### Rollback Plan

If issues occur:

1. Identify the problem
2. Roll back to HTTP version if needed
3. Fix the issue
4. Re-deploy WebSocket version

## Common Issues and Solutions

### Issue: Connection Refused

**Symptom**: Agent cannot connect to WebSocket endpoint

**Solution**:
- Verify URL is correct (should start with `wss://`)
- Check firewall rules allow outbound WebSocket connections
- Verify token is valid

### Issue: Frequent Disconnections

**Symptom**: Agent disconnects and reconnects frequently

**Solution**:
- Check network stability
- Verify ping/pong handling
- Increase timeout values if needed
- Check for firewall/proxy interference

### Issue: Registration Fails

**Symptom**: Registration message returns error

**Solution**:
- Verify all required fields are present
- Check token has correct permissions
- Ensure cluster name is unique

### Issue: No Heartbeat Acknowledgments

**Symptom**: Heartbeats sent but no acknowledgments received

**Solution**:
- Verify cluster_id from registration is used
- Check message format matches spec
- Verify connection is still open

## Support and Resources

### Documentation

- **[WebSocket API Reference](./agent-websocket-api.md)** - Complete API documentation
- **[Example Code](https://github.com/PipeOpsHQ/agent-examples)** - Sample implementations

### Getting Help

1. **Check the documentation** first
2. **Review example code** for your language
3. **Contact support** if issues persist:
   - Email: support@pipeops.io
   - Slack: #agent-migration channel

### Migration Assistance

Need help with migration? We're here to help:

- **Migration Support**: Dedicated support during migration period
- **Code Review**: We can review your implementation
- **Pair Programming**: Available for complex migrations

## FAQ

### Q: Can I run both HTTP and WebSocket simultaneously?

**A**: Yes, during the transition period. However, pick one as the primary and use the other as a fallback.

### Q: What happens if I don't migrate?

**A**: After 30 days, HTTP endpoints will be removed and your agents will stop working.

### Q: Is WebSocket harder to debug than HTTP?

**A**: No, WebSocket libraries have excellent logging. Use tools like `wscat` for testing.

### Q: How do I test locally?

**A**: Use `wscat` or write a simple test client:

```bash
npm install -g wscat
wscat -c "wss://api.pipeops.io/api/v1/clusters/agent/ws?token=YOUR_TOKEN"
```

### Q: What about rate limiting?

**A**: WebSocket connections have the same rate limits as HTTP, but are more efficient.

### Q: Can I use any WebSocket library?

**A**: Yes, any standard WebSocket client library will work. We recommend:
- Go: `github.com/gorilla/websocket`
- Node.js: `ws`
- Python: `websockets`

### Q: What if my agent restarts frequently?

**A**: WebSocket handles reconnections automatically. Each restart will:
1. Connect to WebSocket
2. Send registration message
3. Resume normal operation

## Next Steps

1. **Review the WebSocket API**: Read [agent-websocket-api.md](./agent-websocket-api.md)
2. **Choose your migration strategy**: Canary, blue-green, or rolling
3. **Update your code**: Use the examples above
4. **Test thoroughly**: Verify in staging first
5. **Deploy to production**: Monitor closely
6. **Celebrate**: You're now using modern real-time communication! 🎉

## Conclusion

Migrating to WebSocket is straightforward and provides significant benefits:

- ✅ Better performance
- ✅ Lower latency
- ✅ Efficient resource usage
- ✅ Modern architecture

**Start your migration today to ensure uninterrupted service!**
