# PipeOps Agent Quick Start Guide

## Installation

### Option 1: One-Command Installation (Server Node)

```bash
# Set your PipeOps token
export PIPEOPS_TOKEN="your-token-here"
export CLUSTER_NAME="my-production-cluster"

# Install k3s and deploy agent on server node
curl -sSL https://get.pipeops.io/agent | bash
```

### Option 2: Adding Worker Nodes

After setting up the server node, you can add worker nodes to scale your cluster:

#### Method A: Using the join-worker script (Recommended)

```bash
# On the server node, get cluster connection info
./scripts/install.sh cluster-info

# On each worker node, use the provided information
curl -sSL https://get.pipeops.io/join-worker | bash -s <MASTER_IP> <CLUSTER_TOKEN>
```

#### Method B: Manual worker setup

```bash
# On each worker node, set environment variables
export NODE_TYPE="agent"
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="your-cluster-token"

# Run the installer
curl -sSL https://get.pipeops.io/agent | bash
```

#### Method C: Direct k3s installation

```bash
# On each worker node
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="your-cluster-token"
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="agent" sh -
```

### Option 3: Manual Installation (Server Node)

1. **Install k3s:**

```bash
curl -sfL https://get.k3s.io | sh -
```

2. **Configure agent:**

```bash
# Create namespace
kubectl create namespace pipeops-system

# Create secret with your token
kubectl create secret generic pipeops-agent-config \
  --namespace=pipeops-system \
  --from-literal=PIPEOPS_API_URL="https://api.pipeops.io" \
  --from-literal=PIPEOPS_TOKEN="your-token-here" \
  --from-literal=PIPEOPS_CLUSTER_NAME="my-cluster"
```

3. **Deploy agent:**

```bash
kubectl apply -f https://raw.githubusercontent.com/pipeops/pipeops-vm-agent/main/deployments/agent.yaml
```

## Worker Node Management

### Getting Cluster Connection Information

On the server node, get the information needed to join worker nodes:

```bash
# Show connection details for worker setup
./scripts/install.sh cluster-info

# Or get just the cluster token
sudo cat /var/lib/rancher/k3s/server/node-token
```

### Joining Worker Nodes

#### Option 1: Simple join script

```bash
# Download and run with master IP and token
curl -sSL https://get.pipeops.io/join-worker | bash -s <MASTER_IP> <CLUSTER_TOKEN>
```

#### Option 2: Environment variables

```bash
export NODE_TYPE="agent"
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="your-cluster-token"
curl -sSL https://get.pipeops.io/agent | bash
```

### Verifying Worker Nodes

```bash
# From the server node, check all nodes
k3s kubectl get nodes

# Check node details
k3s kubectl describe nodes

# Check worker node status from the worker
systemctl status k3s-agent
```

## Common Operations

### Deploy an Application

Through PipeOps dashboard or API:

```bash
# Example: Deploy nginx
curl -X POST https://api.pipeops.io/v1/deployments \
  -H "Authorization: Bearer your-api-token" \
  -H "Content-Type: application/json" \
  -d '{
    "cluster": "my-cluster",
    "name": "nginx-app",
    "image": "nginx:latest",
    "replicas": 2,
    "ports": [{"containerPort": 80}]
  }'
```

### Scale Application

```bash
# Scale to 5 replicas
curl -X PATCH https://api.pipeops.io/v1/deployments/nginx-app/scale \
  -H "Authorization: Bearer your-api-token" \
  -H "Content-Type: application/json" \
  -d '{"replicas": 5}'
```

### View Logs

```bash
# Get pod logs through PipeOps API
curl -X GET "https://api.pipeops.io/v1/pods/nginx-app-xxx/logs?lines=100" \
  -H "Authorization: Bearer your-api-token"
```

## Configuration

### Environment Variables

- `PIPEOPS_API_URL` - PipeOps API endpoint (default: https://api.pipeops.io)
- `PIPEOPS_TOKEN` - Your authentication token (required)
- `PIPEOPS_CLUSTER_NAME` - Name for this cluster (required)
- `PIPEOPS_LOG_LEVEL` - Logging level (debug, info, warn, error)

### Config File

Create `~/.pipeops-agent.yaml`:

```yaml
agent:
  cluster_name: "production-east"
  labels:
    environment: "production"
    region: "us-east-1"

pipeops:
  api_url: "https://api.pipeops.io"
  token: "your-token-here"

logging:
  level: "info"
```

## Troubleshooting

### Agent Won't Start

```bash
# Check configuration
kubectl get secret pipeops-agent-config -n pipeops-system -o yaml

# Check logs
kubectl logs deployment/pipeops-agent -n pipeops-system

# Check network connectivity
kubectl run debug --image=busybox -it --rm -- nslookup api.pipeops.io
```

### Connection Issues

```bash
# Test API connectivity
curl -v https://api.pipeops.io/health

# Check token validity
kubectl exec deployment/pipeops-agent -n pipeops-system -- \
  curl -H "Authorization: Bearer $PIPEOPS_TOKEN" \
  https://api.pipeops.io/v1/auth/validate
```

### Deployment Failures

```bash
# Check RBAC permissions
kubectl auth can-i create deployments \
  --as=system:serviceaccount:pipeops-system:pipeops-agent

# Check resource quotas
kubectl describe resourcequota

# Check node resources
kubectl top nodes
kubectl describe nodes
```

## Monitoring

### Health Checks

```bash
# Agent health endpoint
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system
curl http://localhost:8080/health
```

### Metrics

```bash
# Prometheus metrics
kubectl port-forward deployment/pipeops-agent 8090:8090 -n pipeops-system
curl http://localhost:8090/metrics
```

## Uninstalling

```bash
# Remove agent
kubectl delete namespace pipeops-system

# Remove k3s (optional)
/usr/local/bin/k3s-uninstall.sh
```

## Getting Help

- **Documentation**: https://docs.pipeops.io
- **Support**: support@pipeops.io
- **GitHub Issues**: https://github.com/pipeops/pipeops-vm-agent/issues
- **Community**: https://discord.gg/pipeops
