# Local Development Guide

## Why No Token Locally?

When running the agent **locally** (not in a Kubernetes pod), you'll see this warning:

```
"No cluster ServiceAccount token available - control plane will not be able to access cluster"
```

This is **expected** because:

1. **In Kubernetes**: The pod automatically has a ServiceAccount token mounted at:
   ```
   /var/run/secrets/kubernetes.io/serviceaccount/token
   ```

2. **Locally**: This path doesn't exist, so the agent can't find the token.

## Token Loading Priority

The agent tries to load the token in this order:

```
1. K8s mount:  /var/run/secrets/kubernetes.io/serviceaccount/token  (production)
   ↓ If not found...
2. Disk file:  .pipeops-cluster-token                              (local dev)
   ↓ If not found...
3. Warn:       "No ServiceAccount token available"                 (harmless warning)
```

## Testing Token Functionality Locally

### Option 1: Generate Mock Token (Recommended)

Run the mock token generator script:

```bash
./scripts/generate-mock-token.sh
```

This creates a `.pipeops-cluster-token` file with a mock JWT token.

Then restart the agent:

```bash
make run
```

You should now see:

```
"Loaded cluster token from disk"
"has_token": true
```

### Option 2: Extract Real Token from K8s Cluster

If you have access to a K8s cluster with the agent deployed:

```bash
# Get the token from the running pod
kubectl exec -n pipeops-system deployment/pipeops-agent -- \
  cat /var/run/secrets/kubernetes.io/serviceaccount/token > .pipeops-cluster-token

# Restart local agent
make run
```

### Option 3: Manually Create Token File

Create `.pipeops-cluster-token` with any string:

```bash
echo "test-token-for-local-development" > .pipeops-cluster-token
```

## Verifying Token is Loaded

After creating the token file, run the agent and check logs:

```bash
make run
```

**With token:**
```json
{"level":"info","msg":"Loaded cluster token from disk"}
{"has_token":true,"msg":"Using existing cluster ID, skipping re-registration"}
```

**Without token:**
```json
{"level":"warning","msg":"No cluster ServiceAccount token available"}
{"has_token":false,"msg":"Using existing cluster ID, skipping re-registration"}
```

## Testing Token in Requests

### Check Health Endpoint

```bash
curl http://localhost:8080/health | jq .
```

**With token:**
```json
{
  "status": "healthy",
  "cluster_id": "agent-hostname-hash",
  "has_cluster_token": true,  ← Token available
  "last_token_update": "2025-10-14T17:50:23+01:00"
}
```

**Without token:**
```json
{
  "status": "healthy",
  "cluster_id": "agent-hostname-hash",
  "has_cluster_token": false,  ← No token
  "last_token_update": null
}
```

### Inspect Registration Request

If you have a local mock control plane or want to see what's being sent:

```bash
# Enable debug logging
export LOG_LEVEL=debug

# Run agent
make run
```

Look for the registration request body in logs:
```json
{
  "agent_id": "agent-hostname-hash",
  "name": "local-dev-cluster",
  "token": "eyJhbGci...",  ← Token included if available
  "k8s_version": "v1.28.3+k3s1"
}
```

## Production Deployment

In production (K8s pod), the token is automatically available:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pipeops-agent
spec:
  template:
    spec:
      serviceAccountName: pipeops-agent  ← K8s auto-mounts token
      containers:
      - name: agent
        image: ghcr.io/pipeopshq/pipeops-k8-agent:latest
```

**Pod logs will show:**
```
"Loaded ServiceAccount token from Kubernetes mount"
"has_token": true
```

## Token Files Location

The agent looks for token files in these locations (in order):

1. `/var/lib/pipeops/cluster-token` (production)
2. `/etc/pipeops/cluster-token` (system-wide)
3. `.pipeops-cluster-token` (local dev)

For local development, just use option 3 (`.pipeops-cluster-token` in the project root).

## Summary

| Environment | Token Source | Expected Behavior |
|-------------|--------------|-------------------|
| **Local Dev** | `.pipeops-cluster-token` file | Warning if file doesn't exist (harmless) |
| **K8s Pod** | `/var/run/secrets/.../token` | Automatically loaded, no warning |
| **Testing** | Mock token script | Simulates K8s behavior locally |

The warning you saw is **completely normal** for local development! The agent will still work, it just won't send a token to the control plane (which is fine for testing).

To test with a token locally, just run:

```bash
./scripts/generate-mock-token.sh
make run
```

🚀 Happy developing!
