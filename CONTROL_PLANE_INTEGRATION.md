# Control Plane Integration - K8s Cluster Registration via Agent

## Overview

Implement a complete token management system in the PipeOps control plane to allow users to generate API tokens for registering K8s clusters. Each agent runs on a server/VM, sets up a K3s cluster with cluster-admin access, and exposes the Kubernetes API through a secure tunnel. Users register their servers/clusters using these tokens.

## Architecture Context

**Important**: The agent is NOT just a monitoring tool - it's a cluster provisioner and gateway:

1. **Server Setup**: Agent runs on a bare server/VM
2. **K3s Installation**: Agent sets up a complete K3s Kubernetes cluster
3. **Cluster Admin Access**: Agent has full cluster-admin role via service account token
4. **API Tunneling**: Agent exposes K8s API (port 6443) + kubelet (port 10250) through secure reverse tunnel
5. **Control Plane Access**: PipeOps control plane accesses K8s API directly through tunnel (Portainer-style)
6. **User Management**: Users can manage multiple clusters/servers via the control plane

**Token Purpose**: Allow users to register their servers which will become managed K8s clusters.

## GitHub Copilot Prompt

```text
I need to implement a K8s cluster registration token management system in the PipeOps control plane.

## Context:
Our PipeOps agent is deployed on servers/VMs and does the following:
1. Sets up a K3s Kubernetes cluster on the server
2. Obtains cluster-admin role access via service account token
3. Exposes Kubernetes API (port 6443) and kubelet (port 10250) through a secure reverse tunnel
4. Enables the control plane to access and manage the K8s cluster directly (Portainer-style architecture)

Users need to register their servers/clusters using API tokens. Each token allows registration of 
one or more clusters and should be associated with the user's account for multi-cluster management.

The system should integrate with our existing OAuth/authentication mechanism.

## Requirements:

### 1. Database Schema (Add to existing user schema)

Create tables for:

**`cluster_tokens` table** (tokens for registering clusters/servers):
  - id (UUID, primary key)
  - user_id (foreign key to users table)
  - organization_id (foreign key, if multi-tenant)
  - name (string) - friendly name (e.g., "Production Servers", "Dev Environment")
  - token (string, unique, indexed) - the actual bearer token (hashed/encrypted)
  - token_prefix (string) - visible prefix for UI display (e.g., "clt_abc...")
  - scopes (array/json) - permissions (e.g., ["cluster:register", "cluster:heartbeat"])
  - max_clusters (integer, nullable) - limit clusters per token (null = unlimited)
  - expires_at (timestamp, nullable) - optional expiration
  - last_used_at (timestamp, nullable) - track usage
  - created_at (timestamp)
  - updated_at (timestamp)
  - revoked_at (timestamp, nullable) - for soft deletion
  - metadata (json) - store additional info like IP whitelist, allowed regions, etc.

**`clusters` table** (registered K8s clusters):
  - id (UUID, primary key)
  - cluster_token_id (foreign key to cluster_tokens)
  - user_id (foreign key to users table)
  - organization_id (foreign key)
  - agent_id (string, unique) - from agent registration
  - name (string) - cluster name
  - k8s_version (string) - K3s version
  - server_ip (string) - server public IP
  - tunnel_status (enum) - 'connected', 'disconnected', 'error'
  - tunnel_ports (json) - {kubernetes_api: 6443, kubelet: 10250, agent_http: 8080}
  - k8s_api_url (string) - tunnel URL for accessing K8s API
  - last_heartbeat_at (timestamp)
  - registered_at (timestamp)
  - metadata (json) - labels, server specs, etc.
  - status (enum) - 'active', 'inactive', 'error'

### 2. API Endpoints

Create REST API endpoints:

#### Cluster Token Management (Requires User OAuth Authentication):
- `POST /api/v1/cluster-tokens` - Generate new cluster registration token
  - Request: `{ "name": "Production Servers", "expires_in_days": 365, "max_clusters": 10, "metadata": {...} }`
  - Response: `{ "id": "...", "name": "...", "token": "clt_xxxxx", "prefix": "clt_abc...", "expires_at": "..." }`
  - Note: Return full token ONLY on creation (never shown again)

- `GET /api/v1/cluster-tokens` - List user's cluster tokens (paginated)
  - Response: List of tokens (WITHOUT full token, only prefix)
  - Shows: name, prefix, clusters_count, max_clusters, last_used, expires_at

- `GET /api/v1/cluster-tokens/:id` - Get specific token details
  - Response: Token metadata + list of clusters registered with this token

- `PATCH /api/v1/cluster-tokens/:id` - Update token (name, max_clusters, expiration)

- `DELETE /api/v1/cluster-tokens/:id` - Revoke token
  - Note: Existing clusters remain registered but token can't register new ones

- `POST /api/v1/cluster-tokens/:id/regenerate` - Regenerate token
  - Returns new token, revokes old one, preserves cluster associations

#### Cluster Registration (Uses Cluster Token - Agent Endpoints):
- `POST /api/v1/clusters/register` - Register new K8s cluster
  - Headers: `Authorization: Bearer clt_xxxxx`
  - Request: Agent data + K8s cluster info + tunnel configuration
  - Response: `{ "cluster_id": "...", "status": "registered", "tunnel_url": "..." }`
  - Validates token, checks max_clusters limit, creates cluster record

- `POST /api/v1/clusters/:cluster_id/heartbeat` - Cluster heartbeat
  - Headers: `Authorization: Bearer clt_xxxxx`
  - Updates: last_heartbeat_at, tunnel_status
  - Response: Any pending commands or configuration updates

- `GET /api/v1/clusters/:cluster_id/tunnel-info` - Get tunnel configuration
  - Headers: `Authorization: Bearer clt_xxxxx`
  - Returns: Tunnel ports, URLs for accessing K8s API

#### Cluster Management (Requires User OAuth Authentication):
- `GET /api/v1/clusters` - List user's clusters (paginated)
  - Query params: status, search, sort_by
  - Response: List of clusters with health status, K8s version, tunnel status

- `GET /api/v1/clusters/:cluster_id` - Get cluster details
  - Response: Full cluster info + K8s API access URL + tunnel status + metrics

- `GET /api/v1/clusters/:cluster_id/access` - Get K8s access credentials
  - Response: `{ "api_url": "https://tunnel.pipeops.io/clusters/xxx", "token": "<service-account-token>" }`
  - Allows user to configure kubectl to access their cluster

- `DELETE /api/v1/clusters/:cluster_id` - Unregister cluster
  - Soft delete, marks cluster as inactive

- `POST /api/v1/clusters/:cluster_id/reconnect` - Trigger cluster reconnection
  - Useful when tunnel is disconnected

### 3. Authentication & Authorization

Implement middleware for:
- **User Authentication**: Verify OAuth/JWT tokens for user-facing endpoints
- **Agent Authentication**: Verify agent tokens for agent-facing endpoints
  - Check token exists and is not revoked
  - Check token is not expired
  - Check token has required scopes
  - Update `last_used_at` timestamp
  - Rate limiting per token

### 4. Token Generation

Create secure cluster token generator:
- Format: `clt_` + 32-character random string (e.g., `clt_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6`)
- Use cryptographically secure random generation
- Store hashed version in database (bcrypt or argon2)
- Store prefix (first 10 chars) for display: `clt_a1b2c3...`
- Each token can register multiple clusters (up to max_clusters limit)

### 5. Security Features

Implement:
- Token hashing (never store plain text)
- Rate limiting per token (e.g., 100 requests/minute)
- IP whitelist support (optional, in metadata)
- Audit logging for token creation, usage, revocation
- Token expiration checks
- Scope validation
- CORS configuration for agent endpoints

### 6. Frontend/UI Components (if applicable)

Create UI pages for:
- Token management dashboard
  - List all tokens with name, prefix, last used, expiration
  - Create new token button (shows modal)
  - Revoke token button
  - Copy token to clipboard (only on creation)
- Token creation modal
  - Token name input
  - Expiration selection (30 days, 90 days, 1 year, never)
  - Scope selection (checkboxes)
  - Generate button
- Token display (one-time view)
  - Show full token with copy button
  - Warning: "Save this token now, you won't see it again"
- Agents dashboard
  - List registered agents
  - Show which token was used
  - Agent health status
  - Last heartbeat time

### 7. Integration with Existing OAuth

Hook into existing auth system:
- Use existing user/organization context
- Respect existing RBAC (role-based access control)
- Only allow users with "agent:manage" permission to create tokens
- Tokens inherit user's organization context

### 8. Migration & Backward Compatibility

If agents are already registered:
- Create migration to generate tokens for existing agents
- Update existing agent records to reference token
- Maintain backward compatibility during transition

### 9. Documentation

Generate:
- API documentation (OpenAPI/Swagger spec)
- Integration guide for agent setup
- Security best practices
- Token rotation guide

### 10. Testing

Create tests for:
- Token generation and uniqueness
- Token validation and expiration
- Agent registration with token
- Token revocation
- Rate limiting
- Scope validation
- Unauthorized access attempts

## Example Flow: Registering a K8s Cluster

1. **User Authentication**: User logs into PipeOps control plane (OAuth)

2. **Token Generation**: 
   - User navigates to Settings > Cluster Tokens
   - Clicks "Generate Token"
   - Fills in: Name="Production Servers", Max Clusters=10, Expires=1 year
   - System generates: `clt_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6`
   - User copies token (shown only once with warning)

3. **Server Setup**:
   - User provisions a server/VM (AWS EC2, DigitalOcean, bare metal, etc.)
   - Deploys agent with config:
   ```yaml
   pipeops:
     api_url: https://api.pipeops.io
     token: clt_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6
   agent:
     name: production-cluster-1
     cluster_name: production
   ```

4. **Automatic Cluster Setup**:
   - Agent installs K3s on the server
   - Creates service account with cluster-admin role
   - Obtains service account token
   - Sets up reverse tunnel (ports 6443, 10250, 8080)

5. **Cluster Registration**:
   - Agent calls `POST /api/v1/clusters/register` with cluster token
   - Sends: cluster info, K8s version, tunnel configuration, server IP
   - Control plane validates token, checks max_clusters limit
   - Creates cluster record, associates with user/organization
   - Returns: cluster_id, tunnel URLs

6. **Ongoing Operations**:
   - Agent sends heartbeats every 30 seconds
   - Control plane tracks tunnel status, last_heartbeat
   - User sees cluster in dashboard: "production-cluster-1 (K3s v1.28.3) - Connected"

7. **User Access**:
   - User clicks "Access Cluster" in dashboard
   - Gets kubectl config with tunnel URL
   - Can deploy applications, manage resources via control plane
   - Control plane uses tunnel to access K8s API directly (Portainer-style)

8. **Multi-Cluster**:
   - User repeats steps 3-6 for additional servers
   - Same token can register up to 10 clusters
   - All clusters visible in unified dashboard

## Technical Stack Assumptions:

- Backend: Go/Gin (match your existing stack)
- Database: PostgreSQL
- Auth: OAuth 2.0 / JWT
- ORM:  GORM (match your existing stack)

Please implement this complete system with proper error handling, validation, 
and security best practices. Start with the database schema and token generation 
utility, then build the API endpoints, and finally the authentication middleware.
```

## Current Agent Implementation

The K8s agent already has the following endpoints configured (see `/internal/controlplane/client.go`):

### Agent API Calls (Need Minor Updates):

1. **Register Cluster**: `POST /api/v1/clusters/register` (was `/agents/register`)
   - Headers: `Authorization: Bearer clt_xxxxx`
   - Sends: 
     - Agent metadata (ID, name, version)
     - Cluster info (K8s version, node count)
     - Server info (IP, hostname)
     - Tunnel configuration (ports: 6443, 10250, 8080)
     - K3s service account token (cluster-admin)
   - Expects: `{ "cluster_id": "...", "status": "registered", "tunnel_url": "..." }`

2. **Send Heartbeat**: `POST /api/v1/clusters/:cluster_id/heartbeat` (was `/agents/:agent_id/heartbeat`)
   - Headers: `Authorization: Bearer clt_xxxxx`
   - Sends: Agent status, timestamp, tunnel status, metadata
   - Expects: 200/204 response

3. **Report Status**: (REMOVED - no longer needed with Portainer-style architecture)
   - Control plane accesses K8s directly through tunnel

4. **Fetch Commands**: `GET /api/v1/clusters/:cluster_id/commands` (optional for future)
   - Expects: List of commands to execute

5. **Send Command Result**: (optional for future)
   - For executing agent-level commands

6. **Health Check**: `GET /api/v1/health`
   - Simple ping endpoint

### Agent Authentication:

Currently uses: `Authorization: Bearer <token>` header

The control plane needs to:
1. Accept this bearer token format (`clt_xxxxx`)
2. Validate token is active and not expired
3. Check token hasn't exceeded max_clusters limit (on registration)
4. Associate cluster with the user/organization who generated the token
5. Track cluster activity and update last_used_at on token
6. Store cluster-to-token-to-user relationship for access control

### What Agent Provides to Control Plane:

**During Registration**:
- K3s cluster with full access (service account token with cluster-admin role)
- Reverse tunnel exposing:
  - Kubernetes API Server (port 6443)
  - Kubelet API (port 10250)  
  - Agent HTTP server (port 8080)
- Server metadata (IP, hostname, specs)
- Cluster metadata (K8s version, labels)

**During Operation**:
- Heartbeats to indicate tunnel is alive
- Tunnel status updates (connected/disconnected)
- Agent version and health

**What Agent Does NOT Provide**:
- K8s metrics/status (control plane queries K8s API directly)
- Pod/deployment management (control plane manages via K8s API)
- Log streaming (control plane accesses via K8s API)

## Data Models Reference

### Agent Registration Data (from agent):
```go
type Agent struct {
    ID          string
    Name        string
    ClusterName string
    Version     string
    Labels      map[string]string
    Status      AgentStatus
    LastSeen    time.Time
}
```

### Heartbeat Data (from agent):
```go
type HeartbeatRequest struct {
    AgentID     string
    ClusterName string
    Status      string
    ProxyStatus string
    Timestamp   time.Time
    Metadata    map[string]interface{}
}
```

## Implementation Priority:

1. **Phase 1**: Token generation & storage
   - Database schema
   - Token generation utility
   - Basic CRUD API for tokens (user-authenticated)

2. **Phase 2**: Agent authentication
   - Middleware to validate agent tokens
   - Update existing agent endpoints to use token auth
   - Associate agents with users via tokens

3. **Phase 3**: UI/Dashboard
   - Token management page
   - Agent monitoring dashboard

4. **Phase 4**: Advanced features
   - Token scopes
   - IP whitelisting
   - Audit logging
   - Token rotation

## Security Considerations:

- Never log full tokens
- Hash tokens in database (bcrypt, argon2)
- Implement rate limiting per token
- Add token expiration checks
- Provide token revocation mechanism
- Audit all token operations
- Consider IP whitelisting
- Implement scope-based permissions

## Success Criteria:

✅ User can generate cluster registration token via UI/API  
✅ Token is securely stored (hashed)  
✅ Token can register multiple clusters (up to max_clusters limit)  
✅ Agent can register K8s cluster using token  
✅ Agent sets up K3s and establishes tunnel automatically  
✅ Agent sends heartbeats with cluster status  
✅ User can see all registered clusters in dashboard  
✅ User can access K8s API through tunnel URL  
✅ User can revoke tokens (existing clusters remain registered)  
✅ Expired tokens are rejected for new registrations  
✅ All operations are logged for audit  
✅ Cluster-to-user association is maintained  
✅ Multi-cluster management works correctly  
✅ API documentation is complete  
✅ Integration tests pass  

## Next Steps:

1. Share this document with control plane team
2. Review and adjust based on existing architecture
3. Begin implementation starting with Phase 1
4. Test integration with deployed agent
5. Deploy and monitor
