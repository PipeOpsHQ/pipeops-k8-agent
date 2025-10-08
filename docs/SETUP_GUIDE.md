# PipeOps VM Agent Setup Guide

Complete guide for setting up a k3s cluster with the PipeOps VM Agent on your virtual machine or bare metal server.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Quick Setup (Automated Script)](#quick-setup-automated-script)
- [Manual Setup](#manual-setup)
- [Multi-Node Cluster Setup](#multi-node-cluster-setup)
- [Verification](#verification)
- [Accessing the Dashboard](#accessing-the-dashboard)
- [Next Steps](#next-steps)
- [Cluster Management Operations](#cluster-management-operations)
- [Troubleshooting](#troubleshooting)
- [Script Reference](#script-reference)

## Prerequisites

### System Requirements

- **Operating System**: Ubuntu 20.04/22.04, Debian 10/11, CentOS 7/8, RHEL 7/8
- **CPU**: Minimum 2 cores (4 cores recommended for production)
- **RAM**: Minimum 2GB (4GB+ recommended for production)
- **Disk Space**: Minimum 20GB free space
- **Network**: Stable internet connection with outbound access

### Required Ports

- **6443**: Kubernetes API Server (for worker nodes to connect)
- **8080**: PipeOps Agent HTTP/WebSocket server
- **10250**: Kubelet metrics

### PipeOps Account Requirements

Before starting, ensure you have:

1. **PipeOps Account**: Sign up at [https://pipeops.io](https://pipeops.io)
2. **Agent Token**: Generate from PipeOps Dashboard → Settings → Agent Tokens
3. **Cluster Name**: Choose a unique name for your cluster (e.g., `production-cluster`)

## Quick Setup (Automated Script)

The fastest way to get started is using our automated installation script.

### Single Command Installation

```bash
# Export your PipeOps credentials
export PIPEOPS_TOKEN="your-agent-token-here"
export CLUSTER_NAME="my-cluster"
export PIPEOPS_API_URL="https://api.pipeops.io"

# Run the installation script
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
```

### What the Script Does

1. Detects your operating system and architecture
2. Installs k3s (lightweight Kubernetes)
3. Configures kubeconfig for kubectl access
4. Creates necessary namespaces and RBAC
5. Deploys the PipeOps agent pod
6. Configures real-time monitoring
7. Validates the installation

### Script Options

You can customize the installation with environment variables:

```bash
# Full customization example
export PIPEOPS_TOKEN="your-token"
export CLUSTER_NAME="production-east"
export PIPEOPS_API_URL="https://api.pipeops.io"
export PIPEOPS_AGENT_ID="custom-agent-id"
export PIPEOPS_PORT="8080"
export K3S_VERSION="v1.28.3+k3s1"
export LOG_LEVEL="info"

curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
```

### Available Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PIPEOPS_TOKEN` | PipeOps authentication token | **Required** |
| `CLUSTER_NAME` | Unique cluster identifier | `default-cluster` |
| `PIPEOPS_API_URL` | PipeOps Control Plane URL | `https://api.pipeops.io` |
| `PIPEOPS_AGENT_ID` | Custom agent identifier | Auto-generated |
| `PIPEOPS_PORT` | Agent HTTP server port | `8080` |
| `K3S_VERSION` | Specific k3s version to install | Latest stable |
| `LOG_LEVEL` | Logging verbosity | `info` |
| `NODE_TYPE` | Installation type: `server` or `agent` | `server` |
| `K3S_URL` | Master server URL (for worker nodes) | - |
| `K3S_TOKEN` | Cluster token (for worker nodes) | Auto-generated |

## Manual Setup

For users who prefer more control or need to customize the installation.

### Step 1: Install k3s

```bash
# Install k3s server (master node)
curl -sfL https://get.k3s.io | sh -

# Wait for k3s to be ready
sudo k3s kubectl wait --for=condition=Ready node --all --timeout=300s

# Set up kubeconfig for current user
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown $USER:$USER ~/.kube/config
export KUBECONFIG=~/.kube/config
```

### Step 2: Create PipeOps Namespace

```bash
# Create namespace
kubectl create namespace pipeops-system

# Create service account
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pipeops-agent
  namespace: pipeops-system
EOF
```

### Step 3: Set Up RBAC

```bash
# Create cluster role with necessary permissions
cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pipeops-agent
rules:
- apiGroups: ["*"]
  resources: ["*"]
  verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pipeops-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: pipeops-agent
subjects:
- kind: ServiceAccount
  name: pipeops-agent
  namespace: pipeops-system
EOF
```

### Step 4: Deploy PipeOps Agent

```bash
# Create agent secret with credentials
kubectl create secret generic pipeops-agent-config \
  --from-literal=token="your-pipeops-token" \
  --from-literal=api-url="https://api.pipeops.io" \
  --from-literal=cluster-name="my-cluster" \
  -n pipeops-system

# Deploy the agent
kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml
```

### Step 5: Verify Installation

```bash
# Check agent pod status
kubectl get pods -n pipeops-system

# View agent logs
kubectl logs -f deployment/pipeops-agent -n pipeops-system

# Check agent health
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system &
curl http://localhost:8080/health
```

## Multi-Node Cluster Setup

For production environments, you'll want multiple nodes for high availability.

### Master Node Setup

```bash
# On the first master node
export PIPEOPS_TOKEN="your-token"
export CLUSTER_NAME="production-cluster"
export NODE_TYPE="server"

curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash

# Save the cluster token for worker nodes
sudo cat /var/lib/rancher/k3s/server/node-token
```

### Worker Node Setup

```bash
# Get master node IP and token from master node
MASTER_IP="192.168.1.100"  # Replace with your master node IP
CLUSTER_TOKEN="K10xxx..."   # Token from master node

# On each worker node
export NODE_TYPE="agent"
export K3S_URL="https://${MASTER_IP}:6443"
export K3S_TOKEN="${CLUSTER_TOKEN}"

curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
```

### Using the Join Script

For easier worker node setup, use the dedicated join script:

```bash
# Method 1: Get connection info using cluster-info script
./scripts/cluster-info.sh

# Method 2: Direct join on worker node
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh | \
  bash -s <MASTER_IP> <CLUSTER_TOKEN>

# Method 3: Interactive join (script will prompt for details)
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh | bash
```

### Getting Cluster Connection Information

Use the cluster-info script to get all connection details:

```bash
# Display cluster information
./scripts/cluster-info.sh

# Show with cluster token visible (use with caution)
./scripts/cluster-info.sh --show-token

# Output in JSON format (for automation)
./scripts/cluster-info.sh --format json

# Output in YAML format (for configuration files)
./scripts/cluster-info.sh --format yaml
```

The cluster-info script displays:
- Master node IP address
- Cluster token (hidden by default for security)
- Worker join command (ready to copy/paste)
- Agent deployment status
- Quick access commands

## Verification

### Check Cluster Status

```bash
# View all nodes
kubectl get nodes -o wide

# Check system pods
kubectl get pods -A

# Verify PipeOps agent
kubectl get deployment pipeops-agent -n pipeops-system

# Check agent logs
kubectl logs -l app=pipeops-agent -n pipeops-system --tail=50
```

### Test Agent Connectivity

```bash
# Port forward to agent
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system

# Test health endpoint
curl http://localhost:8080/health

# Test detailed health
curl http://localhost:8080/api/health/detailed | jq

# Test features endpoint
curl http://localhost:8080/api/status/features | jq

# Test runtime metrics
curl http://localhost:8080/api/metrics/runtime | jq
```

### Verify Real-Time Features

```bash
# Test WebSocket (requires wscat: npm install -g wscat)
wscat -c ws://localhost:8080/ws

# Test Server-Sent Events
curl -N http://localhost:8080/api/realtime/events
```

## Accessing the Dashboard

The PipeOps agent includes a real-time monitoring dashboard.

### Local Access via Port Forward

```bash
# Port forward to access dashboard
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system

# Open dashboard in browser
open http://localhost:8080/dashboard
# Or visit: http://localhost:8080/static/dashboard.html
```

### Remote Access via Ingress

For production, set up an Ingress:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: pipeops-agent-dashboard
  namespace: pipeops-system
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  ingressClassName: nginx
  tls:
  - hosts:
    - agent.your-domain.com
    secretName: agent-tls
  rules:
  - host: agent.your-domain.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: pipeops-agent
            port:
              number: 8080
EOF
```

## Next Steps

Now that your cluster is set up, here's what you can do next:

### 1. Deploy Your First Application

```bash
# Create a sample deployment
kubectl create deployment nginx --image=nginx:latest --replicas=3

# Expose it as a service
kubectl expose deployment nginx --port=80 --type=LoadBalancer

# Check status
kubectl get pods,svc
```

### 2. Set Up Monitoring Stack

```bash
# Deploy Prometheus and Grafana
kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/examples/monitoring-stack.yaml

# Access Grafana
kubectl port-forward svc/grafana 3000:80 -n monitoring
```

### 3. Configure Ingress Controller

```bash
# NGINX Ingress is usually installed with k3s
# Verify it's running
kubectl get pods -n kube-system | grep ingress

# Install cert-manager for SSL
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
```

### 4. Set Up Continuous Deployment

Connect to your PipeOps dashboard to:
- Configure deployment pipelines
- Set up automated deployments
- Monitor application health
- Manage secrets and configs

### 5. Scale Your Cluster

Add more worker nodes for increased capacity:

```bash
# Get cluster info from master
sudo cat /var/lib/rancher/k3s/server/node-token

# On new worker nodes
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="your-cluster-token"
curl -sSL https://get.k3s.io | sh -s - agent
```

### 6. Backup and Disaster Recovery

```bash
# Backup k3s (on master node)
sudo k3s etcd-snapshot save

# List snapshots
sudo k3s etcd-snapshot ls

# Restore from snapshot (if needed)
sudo k3s etcd-snapshot restore snapshot-name
```

### 7. Security Hardening

```bash
# Apply network policies
kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/examples/network-policies.yaml

# Set up Pod Security Standards
kubectl label namespace default pod-security.kubernetes.io/enforce=restricted

# Configure resource quotas
kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/examples/resource-quotas.yaml
```

## Cluster Management Operations

### Getting Cluster Information

The cluster-info script provides comprehensive cluster details:

```bash
# Display all cluster information
./scripts/cluster-info.sh

# Show cluster token (use with caution - sensitive!)
./scripts/cluster-info.sh --show-token

# Get JSON output for automation/scripting
./scripts/cluster-info.sh --format json | jq '.masterIP'

# Get YAML output for configuration files
./scripts/cluster-info.sh --format yaml > cluster-config.yaml
```

**What it shows:**
- Master node IP address (auto-detected)
- Cluster join token (hidden by default)
- Ready-to-use worker join command
- Agent deployment status (with color indicators)
- Quick access commands for common tasks

**Use cases:**
- Setting up new worker nodes
- Troubleshooting connectivity issues
- Automating cluster expansion
- Documentation and audit trails
- Integration with orchestration tools

### Uninstalling the Agent

The uninstall script provides safe, clean removal:

```bash
# Interactive uninstall (prompts for confirmation)
./scripts/uninstall.sh

# Force uninstall without prompts
./scripts/uninstall.sh --force

# Remove agent AND k3s completely
./scripts/uninstall.sh --uninstall-k3s

# Keep persistent volume claims (data)
./scripts/uninstall.sh --keep-data

# Combine options
./scripts/uninstall.sh --uninstall-k3s --force
```

**What gets removed:**
- PipeOps agent deployment and pods
- Service and endpoints
- RBAC (ClusterRole, ClusterRoleBinding, ServiceAccount)
- Secrets and ConfigMaps
- Namespace (pipeops-system)
- Optionally: Persistent Volume Claims
- Optionally: k3s (complete Kubernetes removal)

**What's preserved by default:**
- k3s installation (unless --uninstall-k3s specified)
- Persistent Volume Claims (unless you remove them manually)
- Other namespaces and applications
- Node configuration

## Troubleshooting

### Agent Not Connecting

```bash
# Check agent logs
kubectl logs -f deployment/pipeops-agent -n pipeops-system

# Verify token is correct
kubectl get secret pipeops-agent-config -n pipeops-system -o jsonpath='{.data.token}' | base64 -d

# Test network connectivity
kubectl exec -it deployment/pipeops-agent -n pipeops-system -- curl -v https://api.pipeops.io/health
```

### Worker Node Won't Join

```bash
# On worker node, check k3s-agent logs
sudo journalctl -u k3s-agent -f

# Verify master node is accessible
curl -k https://master-ip:6443

# Check firewall rules
sudo iptables -L -n | grep 6443
```

### Pod Failures

```bash
# Check pod events
kubectl describe pod <pod-name>

# View pod logs
kubectl logs <pod-name>

# Check node resources
kubectl top nodes
kubectl describe node <node-name>
```

### Dashboard Not Loading

```bash
# Verify agent is running
kubectl get pods -n pipeops-system

# Check port forwarding
kubectl port-forward deployment/pipeops-agent 8080:8080 -n pipeops-system

# Test directly
curl http://localhost:8080/dashboard
```

## Getting Help

- **Documentation**: [https://docs.pipeops.io](https://docs.pipeops.io)
- **GitHub Issues**: [https://github.com/PipeOpsHQ/pipeops-k8-agent/issues](https://github.com/PipeOpsHQ/pipeops-k8-agent/issues)
- **Community Slack**: [https://pipeops.io/slack](https://pipeops.io/slack)
- **Support Email**: support@pipeops.io

## Script Reference

All operational scripts are production-ready and fully tested.

### Installation Script

```bash
# Full script URL
https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh

# View script before running
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | less

# Download and inspect
wget https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh
chmod +x install.sh
./install.sh

# Show all available options
./install.sh --help
```

**Features:**
- Automatic OS and architecture detection
- k3s installation and configuration
- PipeOps agent deployment
- RBAC setup
- Health validation
- Colorized output with progress indicators

### Join Worker Script

```bash
# Full script URL
https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh

# Method 1: Provide master IP and token
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh | \
  bash -s <MASTER_IP> <CLUSTER_TOKEN>

# Method 2: Interactive mode (prompts for details)
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh | bash

# Method 3: Local execution
./scripts/join-worker.sh 192.168.1.100 K10abc123...
./scripts/join-worker.sh --help
```

**Features:**
- Interactive or automated modes
- Automatic k3s agent installation
- Master node connectivity validation
- Token verification
- Success confirmation with instructions

### Uninstall Script

```bash
# Full script URL
https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/uninstall.sh

# Uninstall agent only (interactive confirmation)
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/uninstall.sh | bash

# Force uninstall without confirmation
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/uninstall.sh | bash -s -- --force

# Uninstall agent AND k3s completely
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/uninstall.sh | bash -s -- --uninstall-k3s

# Keep persistent data (PVCs)
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/uninstall.sh | bash -s -- --keep-data

# Local usage with options
./scripts/uninstall.sh --help              # Show all options
./scripts/uninstall.sh                      # Interactive uninstall
./scripts/uninstall.sh --force              # No confirmation
./scripts/uninstall.sh --uninstall-k3s      # Remove everything
./scripts/uninstall.sh --keep-data          # Preserve PVCs
```

### Cluster Info Script

```bash
# Full script URL
https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/cluster-info.sh

# Display cluster information
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/cluster-info.sh | bash

# Show with token visible
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/cluster-info.sh | bash -s -- --show-token

# Get JSON output for automation
curl -sSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/cluster-info.sh | bash -s -- --format json

# Local usage
./scripts/cluster-info.sh                   # Display info (token hidden)
./scripts/cluster-info.sh --show-token      # Display with token
./scripts/cluster-info.sh --format json     # JSON output
./scripts/cluster-info.sh --format yaml     # YAML output
./scripts/cluster-info.sh --help            # Show all options
```

## Additional Resources

- [Architecture Documentation](docs/ARCHITECTURE.md)
- [API Reference](docs/API.md)
- [Development Guide](CONTRIBUTING.md)
- [Security Guidelines](SECURITY.md)
- [Examples](examples/)

---

**Need help?** Open an issue or contact support at support@pipeops.io
