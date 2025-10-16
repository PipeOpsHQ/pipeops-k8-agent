# PipeOps Controller Documentation

## ⚠️ IMPORTANT: HTTP Endpoints Deprecated

**All HTTP agent endpoints are deprecated.** Agents must migrate to WebSocket.

See the **[Agent Migration Guide](./agent-migration-guide.md)** for step-by-step instructions.

## Agent APIs

### WebSocket API (REQUIRED)
- **[Agent WebSocket API](./agent-websocket-api.md)** - Real-time bidirectional communication for agents
  - Native ping/pong health checks
  - Lower latency than HTTP polling
  - Efficient resource usage
  - Connection state awareness
  - Full backward compatibility with HTTP (during transition period)

### ⚠️ HTTP API (DEPRECATED - Will be removed)
- **[Migration Guide](./agent-migration-guide.md)** - Step-by-step guide to migrate from HTTP to WebSocket
- HTTP endpoints return deprecation warnings
- All new agents MUST use WebSocket
- Existing agents MUST migrate within 30 days

## Quick Start

### For New Agents
Use the WebSocket API from the start:
```
wss://api.pipeops.io/api/v1/clusters/agent/ws?token=<your-token>
```

### For Existing Agents (URGENT)
1. Read the **[Agent Migration Guide](./agent-migration-guide.md)**
2. Implement WebSocket support using code examples
3. Test in staging environment
4. Deploy to production
5. Monitor for successful migration

**Deadline: 30 days from deprecation announcement**

## Features

- ✅ Real-time bidirectional communication
- ✅ Native WebSocket ping/pong for health checks
- ✅ Lower latency (<100ms vs 30s+ with polling)
- ✅ Efficient resource usage
- ✅ Instant connection state awareness
- ✅ Deprecation warnings on old HTTP endpoints
- ✅ Comprehensive error handling
- ✅ Token-based authentication
- ✅ Production-ready implementation

## Documentation

- **[WebSocket API](./agent-websocket-api.md)** - Complete API reference
- **[Migration Guide](./agent-migration-guide.md)** - HTTP to WebSocket migration steps
- Code examples in Go, Node.js, and Python

## Support

For questions or migration assistance:
1. Review the [Migration Guide](./agent-migration-guide.md)
2. Check the [WebSocket API documentation](./agent-websocket-api.md)
3. Review code examples in the migration guide
4. Contact the platform team if needed
