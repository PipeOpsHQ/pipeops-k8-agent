# Production Deployment Guide - Preventing Agent Re-registration

This guide helps you deploy the PipeOps Agent in production environments and prevent the common re-registration issue.

## The Re-registration Problem

**Symptoms:**
- Agent logs show "Generated new agent ID" on every restart
- Agent appears as multiple instances in the PipeOps dashboard
- Constant registration attempts in the logs
- Agent never maintains stable identity across pod restarts

**Root Cause:**
The agent cannot persist its identity (agent_id and cluster_id) across pod restarts due to missing state persistence.

## Solution Overview

The agent uses a Kubernetes ConfigMap to persist its state across restarts. When this fails, the agent generates a new identity on every startup, causing re-registration.

## Prerequisites for Production

### 1. RBAC Permissions

Ensure your Helm deployment includes proper RBAC permissions:

```yaml
# Required in your values.yaml
rbac:
  create: true
```

The agent needs these permissions in its namespace:
- `configmaps`: `get`, `list`, `create`, `update`, `patch`
- `secrets`: `get`, `list` (for reading tokens)

### 2. Persistent State Configuration

Configure state persistence in your `values.yaml`:

```yaml
agent:
  state:
    enabled: true
    configMapName: "state"  # Will be prefixed with release name
```

### 3. Health Check Configuration

Configure proper health checks to prevent premature restarts:

```yaml
startupProbe:
  enabled: true
  failureThreshold: 30  # Allow 5 minutes for startup
  
livenessProbe:
  enabled: true
  initialDelaySeconds: 60
  failureThreshold: 3

readinessProbe:
  enabled: true
  initialDelaySeconds: 15
  failureThreshold: 3
```

## Deployment Steps

### 1. Deploy with Helm

```bash
helm install pipeops-agent ./helm/pipeops-agent \
  --namespace pipeops-system \
  --create-namespace \
  --set agent.pipeops.token="your-pipeops-token" \
  --set agent.cluster.name="your-cluster-name" \
  --set rbac.create=true \
  --set agent.state.enabled=true
```

### 2. Verify State Persistence

Check if the state ConfigMap is created:

```bash
kubectl get configmap pipeops-agent-state -n pipeops-system
```

Should contain:
```yaml
data:
  agent_id: "agent-xxxxxxxx"
  cluster_id: "uuid-from-control-plane"
  # ... other state data
```

### 3. Monitor Agent Logs

```bash
kubectl logs -n pipeops-system deployment/pipeops-agent -f
```

**Good logs (state working):**
```
✅ Agent ID loaded from persistent state - will maintain identity across restarts
✅ Loaded existing cluster registration from state
✅ Cluster ID persisted to state - will prevent re-registration on restart
```

**Bad logs (state failing):**
```
⚠️  Generated new agent ID - this will cause re-registration!
❌ CRITICAL: Failed to persist agent ID to state
   Check RBAC permissions for ConfigMap creation/update in agent namespace
```

## Troubleshooting Re-registration

### Automated Fix Script

Run the automated fix script:

```bash
./scripts/fix-reregistration.sh [namespace] [release-name]
```

This script will:
1. Diagnose the current state
2. Fix ConfigMap permissions
3. Create missing state ConfigMap
4. Restart the agent
5. Verify the fix

### Manual Troubleshooting

#### Step 1: Check RBAC Permissions

```bash
# Check if agent can create ConfigMaps
kubectl auth can-i create configmaps --as=system:serviceaccount:pipeops-system:pipeops-agent -n pipeops-system

# Check if agent can update ConfigMaps
kubectl auth can-i update configmaps --as=system:serviceaccount:pipeops-system:pipeops-agent -n pipeops-system
```

Both should return "yes". If not, check your RBAC configuration.

#### Step 2: Check State ConfigMap

```bash
kubectl get configmap pipeops-agent-state -n pipeops-system -o yaml
```

If missing or empty, create it manually:

```bash
# Generate a stable agent ID
AGENT_ID="agent-$(openssl rand -hex 4)"

# Create state ConfigMap
kubectl create configmap pipeops-agent-state -n pipeops-system \
  --from-literal=agent_id="$AGENT_ID"

# Add proper labels
kubectl label configmap pipeops-agent-state -n pipeops-system \
  app.kubernetes.io/name=pipeops-agent \
  app.kubernetes.io/component=state
```

#### Step 3: Restart Agent

```bash
kubectl rollout restart deployment pipeops-agent -n pipeops-system
kubectl rollout status deployment pipeops-agent -n pipeops-system
```

#### Step 4: Verify Fix

Wait 30 seconds, then check logs:

```bash
kubectl logs -n pipeops-system deployment/pipeops-agent --tail=20
```

Look for "Agent ID loaded from persistent state" instead of "Generated new agent ID".

## Production Best Practices

### 1. Resource Limits

Set appropriate resource limits to prevent OOM kills:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### 2. Single Replica

Always use a single replica to avoid conflicts:

```yaml
replicaCount: 1
strategy:
  type: Recreate  # Ensures only one instance at a time
```

### 3. Monitoring

Monitor these metrics:
- Agent restart count (should be minimal)
- Registration frequency (should be once per agent lifecycle)
- State ConfigMap presence and content

### 4. Backup State

Consider backing up the state ConfigMap:

```bash
kubectl get configmap pipeops-agent-state -n pipeops-system -o yaml > agent-state-backup.yaml
```

## Environment Variables for State Control

These environment variables control state behavior:

```yaml
env:
  - name: PIPEOPS_STATE_CONFIGMAP_NAME
    value: "pipeops-agent-state"
  - name: PIPEOPS_STATE_NAMESPACE
    value: "pipeops-system"
  - name: PIPEOPS_ENABLE_STATE_PERSISTENCE
    value: "true"
```

## Common Issues and Solutions

### Issue: Agent keeps generating new IDs

**Solution:** Check RBAC permissions for ConfigMap operations.

### Issue: State ConfigMap exists but agent doesn't use it

**Solution:** Verify ConfigMap name matches environment variables.

### Issue: Agent crashes during startup

**Solution:** Increase startup probe failure threshold and resource limits.

### Issue: Multiple agent instances in dashboard

**Solution:** Clean up old registrations and ensure single replica deployment.

## Support

If issues persist:
1. Run `./scripts/diagnose-state.sh` for detailed analysis
2. Run `./scripts/fix-reregistration.sh` for automated fixes
3. Check the troubleshooting logs for specific error messages
4. Contact support with agent logs and ConfigMap state

## Validation Checklist

Before marking deployment as stable:

- [ ] Agent logs show "Agent ID loaded from persistent state"
- [ ] State ConfigMap exists and contains agent_id
- [ ] RBAC permissions allow ConfigMap operations
- [ ] Agent maintains same identity across restarts
- [ ] Only one agent instance appears in PipeOps dashboard
- [ ] No "Generated new agent ID" messages in logs