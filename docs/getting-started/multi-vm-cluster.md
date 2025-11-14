# Multi-VM Cluster Setup for High Availability

This guide explains how to create a multi-node Kubernetes cluster across multiple VMs or physical machines for disaster recovery (DR) and high availability (HA) purposes.

## Overview

A multi-node cluster provides:

- **High Availability**: Services continue running if a node fails
- **Disaster Recovery**: Data and workloads distributed across multiple machines
- **Load Distribution**: Workloads spread across multiple nodes
- **Fault Tolerance**: Automatic pod rescheduling on node failure

## Architecture Types

### 1. Single Master with Worker Nodes

Best for: Development, staging environments

```
┌─────────────────┐
│  Master Node    │  ← Control plane + workloads
│  (VM/Machine 1) │
└─────────────────┘
         │
    ┌────┴────┐
    │         │
┌───▼───┐ ┌──▼────┐
│Worker │ │Worker │  ← Workloads only
│Node 1 │ │Node 2 │
└───────┘ └───────┘
```

### 2. HA Control Plane (Multi-Master)

Best for: Production, critical workloads

```
┌─────────┐  ┌─────────┐  ┌─────────┐
│Master 1 │  │Master 2 │  │Master 3 │  ← 3+ masters for quorum
└─────────┘  └─────────┘  └─────────┘
      │           │           │
      └───────────┴───────────┘
                  │
         ┌────────┴────────┐
         │                 │
    ┌────▼────┐      ┌────▼────┐
    │Worker 1 │      │Worker 2 │  ← Worker nodes
    └─────────┘      └─────────┘
```

## Prerequisites

### Hardware Requirements

**Minimum for Production HA Cluster:**
- 3 master nodes (for etcd quorum)
- 2+ worker nodes
- Each node: 2 CPU cores, 4GB RAM, 20GB disk

**Minimum for Development Cluster:**
- 1 master node
- 1+ worker nodes
- Each node: 2 CPU cores, 2GB RAM, 20GB disk

### Network Requirements

All nodes must:
- Be on the same network or have network connectivity
- Have unique hostnames
- Have unique IP addresses (static recommended)
- Allow traffic between nodes on these ports:
  - **6443**: Kubernetes API server
  - **10250**: Kubelet API
  - **2379-2380**: etcd (master nodes only)
  - **8472**: VXLAN/Flannel (K3s default)

### Operating Systems

Supported on:
- Ubuntu 20.04+ (recommended)
- Debian 11+
- RHEL/CentOS 8+
- Windows Server 2019+ (via WSL2 or native)

## Setup Guide

### Step 1: Prepare Master Node

On your first VM/machine (will become the master):

```bash
# Update system
sudo apt update && sudo apt upgrade -y

# Install curl
sudo apt install -y curl

# Set environment variables
export PIPEOPS_TOKEN="your-pipeops-token-here"
export CLUSTER_NAME="production-ha-cluster"

# Install K3s master with PipeOps agent
curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
```

The installer will:
- Install K3s in server mode
- Deploy PipeOps agent
- Display join token and command for workers

### Step 2: Get Cluster Join Information

After the master installation completes, get the join information:

```bash
# Method 1: From installer output (shown after installation)
# Look for: "To join worker nodes, run on each worker machine:"

# Method 2: Manually retrieve token
sudo cat /var/lib/rancher/k3s/server/node-token

# Method 3: Use cluster-info script
curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/cluster-info.sh | bash
```

Example output:
```
Master IP: 192.168.1.100
Node Token: K10abc123def456::server:abc123def456789
Join Command:
  curl -fsSL https://get.pipeops.dev/join-worker.sh | bash -s 192.168.1.100 K10abc123def456::server:abc123def456789
```

### Step 3: Join Worker Nodes

On each additional VM/machine:

**Option A: Automated (Recommended)**

```bash
# Replace with your master IP and token
curl -fsSL https://get.pipeops.dev/join-worker.sh | bash -s <MASTER_IP> <TOKEN>

# Example:
curl -fsSL https://get.pipeops.dev/join-worker.sh | bash -s 192.168.1.100 K10abc123def456::server:abc123def456789
```

**Option B: Manual**

```bash
# Set join information
export K3S_URL="https://<MASTER_IP>:6443"
export K3S_TOKEN="<TOKEN_FROM_MASTER>"

# Install K3s in agent mode
curl -sfL https://get.k3s.io | sh -
```

**Option C: Using PipeOps Script**

```bash
# Download join script
curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh -o join-worker.sh
chmod +x join-worker.sh

# Run with master IP and token
./join-worker.sh 192.168.1.100 K10abc123def456::server:abc123def456789
```

### Step 4: Verify Cluster

On the master node, verify all nodes joined:

```bash
# List all nodes
kubectl get nodes

# Expected output:
# NAME        STATUS   ROLES                  AGE   VERSION
# master      Ready    control-plane,master   10m   v1.28.4+k3s1
# worker-1    Ready    <none>                 2m    v1.28.4+k3s1
# worker-2    Ready    <none>                 1m    v1.28.4+k3s1

# Check node details
kubectl get nodes -o wide

# View cluster info
kubectl cluster-info
```

## High Availability Setup (Multi-Master)

For true HA, deploy 3 or more master nodes.

### Step 1: Prepare First Master

```bash
# Install first master with embedded etcd
export PIPEOPS_TOKEN="your-token"
export CLUSTER_NAME="ha-cluster"
export K3S_INIT="true"  # Initialize HA cluster

curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
```

### Step 2: Get HA Join Token

```bash
# Get server token for additional masters
sudo cat /var/lib/rancher/k3s/server/node-token
```

### Step 3: Join Additional Masters

On each additional master node:

```bash
export K3S_URL="https://<FIRST_MASTER_IP>:6443"
export K3S_TOKEN="<SERVER_TOKEN>"

# Install as server (not agent)
curl -sfL https://get.k3s.io | sh -s - server
```

### Step 4: Join Worker Nodes

Same as single-master setup - use any master IP:

```bash
export K3S_URL="https://<ANY_MASTER_IP>:6443"
export K3S_TOKEN="<NODE_TOKEN>"

curl -sfL https://get.k3s.io | sh -
```

### Step 5: Load Balancer (Optional but Recommended)

For production HA, use a load balancer in front of masters:

**Using HAProxy:**

```bash
# Install HAProxy on a separate node
sudo apt install haproxy

# Configure /etc/haproxy/haproxy.cfg
frontend kubernetes-api
    bind *:6443
    mode tcp
    default_backend kubernetes-masters

backend kubernetes-masters
    mode tcp
    balance roundrobin
    option tcp-check
    server master1 192.168.1.101:6443 check
    server master2 192.168.1.102:6443 check
    server master3 192.168.1.103:6443 check

# Restart HAProxy
sudo systemctl restart haproxy
```

Then configure K3s to use the load balancer:
```bash
export K3S_URL="https://<LOADBALANCER_IP>:6443"
```

## Disaster Recovery Configuration

### 1. Configure Pod Distribution

Ensure pods are distributed across nodes:

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 3
  template:
    spec:
      # Anti-affinity to spread pods across nodes
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchLabels:
                  app: my-app
              topologyKey: kubernetes.io/hostname
```

### 2. Set Up etcd Backups (HA Clusters)

K3s automatically backs up etcd. Configure backup location:

```bash
# On master nodes, configure etcd snapshots
sudo systemctl edit k3s

# Add to override.conf:
[Service]
Environment="K3S_ETCD_SNAPSHOT_SCHEDULE_CRON=0 */6 * * *"
Environment="K3S_ETCD_SNAPSHOT_RETENTION=10"
Environment="K3S_ETCD_SNAPSHOT_DIR=/var/lib/rancher/k3s/server/db/snapshots"
```

Restore from snapshot:

```bash
# Stop K3s on all nodes
sudo systemctl stop k3s

# On first master, restore from snapshot
sudo k3s server --cluster-reset --cluster-reset-restore-path=/path/to/snapshot

# Restart K3s
sudo systemctl start k3s

# Rejoin other nodes
```

### 3. Configure Persistent Storage

Use distributed storage for data persistence:

**Option A: Longhorn (Recommended)**

```bash
kubectl apply -f https://raw.githubusercontent.com/longhorn/longhorn/master/deploy/longhorn.yaml

# Set as default storage class
kubectl patch storageclass longhorn -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
```

**Option B: NFS**

```bash
# Install NFS provisioner
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/nfs-subdir-external-provisioner/master/deploy/rbac.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/nfs-subdir-external-provisioner/master/deploy/deployment.yaml
```

### 4. Enable Pod Disruption Budgets

Protect critical workloads during maintenance:

```yaml
# pdb.yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: my-app-pdb
spec:
  minAvailable: 2  # Keep at least 2 pods running
  selector:
    matchLabels:
      app: my-app
```

## Node Labels and Taints

### Label Nodes by Role

```bash
# Label nodes for workload placement
kubectl label nodes worker-1 node-role.kubernetes.io/worker=true
kubectl label nodes worker-2 node-role.kubernetes.io/worker=true

# Label by zone/datacenter
kubectl label nodes worker-1 topology.kubernetes.io/zone=us-east-1a
kubectl label nodes worker-2 topology.kubernetes.io/zone=us-east-1b

# Label by machine type
kubectl label nodes worker-1 node-type=compute
kubectl label nodes worker-2 node-type=storage
```

### Taint Masters (Optional)

Prevent workloads from running on masters:

```bash
# Taint master nodes
kubectl taint nodes master node-role.kubernetes.io/master:NoSchedule

# Allow specific pods on master (toleration in pod spec)
tolerations:
- key: "node-role.kubernetes.io/master"
  operator: "Exists"
  effect: "NoSchedule"
```

## Monitoring and Alerts

The PipeOps agent automatically deploys monitoring stack (Prometheus, Grafana). Configure alerts for:

### Node Failure Alerts

```yaml
# prometheus-rules.yaml
groups:
- name: node-alerts
  rules:
  - alert: NodeDown
    expr: up{job="node-exporter"} == 0
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "Node {% raw %}{{ $labels.instance }}{% endraw %} is down"
```

### Cluster Health Checks

```bash
# Check cluster health
kubectl get componentstatuses

# Check node status
kubectl get nodes

# Check PipeOps agent
kubectl get pods -n pipeops-system

# View cluster events
kubectl get events --all-namespaces --sort-by='.lastTimestamp'
```

## Scaling the Cluster

### Add More Worker Nodes

Repeat Step 3 from setup to add additional workers at any time.

### Remove a Node

```bash
# Drain node (move pods to other nodes)
kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data

# Remove from cluster
kubectl delete node <node-name>

# On the node being removed, stop and uninstall K3s
sudo /usr/local/bin/k3s-uninstall.sh  # For worker nodes
sudo /usr/local/bin/k3s-server-uninstall.sh  # For master nodes
```

## Testing Disaster Recovery

### Simulate Node Failure

```bash
# Method 1: Stop K3s on a node
sudo systemctl stop k3s

# Method 2: Power off a VM
sudo poweroff

# Watch pods reschedule
kubectl get pods -A -o wide --watch
```

### Test Failover

```bash
# Deploy test application with 3 replicas
kubectl create deployment nginx --image=nginx --replicas=3

# Check pod distribution
kubectl get pods -o wide

# Stop one node
# Verify pods reschedule to remaining nodes
kubectl get pods -o wide --watch
```

## Best Practices

### Network

- Use static IP addresses for all nodes
- Configure firewalls to allow inter-node traffic
- Use private networks when possible
- Set up VPN for geographically distributed nodes

### Storage

- Use distributed storage (Longhorn, Ceph, or cloud provider storage)
- Regular backups of etcd and persistent volumes
- Test restore procedures regularly

### Security

- Keep nodes updated with security patches
- Use RBAC for access control
- Enable Pod Security Standards
- Regular security audits

### Monitoring

- Monitor node resource usage (CPU, RAM, disk)
- Set up alerts for critical conditions
- Log aggregation for troubleshooting
- Regular health checks

## Platform-Specific Guides

### Windows Multi-VM Setup

See [Windows Installation Guide](windows-installation.md) for Windows-specific instructions using:
- WSL2 on multiple Windows machines
- Windows Server nodes
- Hybrid Windows/Linux clusters

### Cloud Provider Setup

For cloud-managed nodes:

**AWS:**
```bash
# Use EC2 instances with security groups allowing:
# - 6443 (K8s API)
# - 10250 (Kubelet)
# - 8472 (Flannel/VXLAN)
```

**Azure:**
```bash
# Use Azure VMs with NSG rules for K3s ports
# Enable accelerated networking for better performance
```

**GCP:**
```bash
# Use Compute Engine VMs
# Configure VPC firewall rules for inter-node traffic
```

## Troubleshooting

### Worker Node Won't Join

**Check connectivity:**
```bash
# From worker, test connection to master
curl -k https://<MASTER_IP>:6443

# Check if port 6443 is open
nc -zv <MASTER_IP> 6443
```

**Verify token:**
```bash
# Token might have expired or be incorrect
# Get fresh token from master:
sudo cat /var/lib/rancher/k3s/server/node-token
```

**Check logs:**
```bash
# On worker node
sudo journalctl -u k3s-agent -f
```

### Pods Not Distributing Across Nodes

**Check node capacity:**
```bash
kubectl describe nodes
```

**Verify scheduler is running:**
```bash
kubectl get pods -n kube-system | grep scheduler
```

**Check pod affinity/anti-affinity rules**

### etcd Issues (HA Clusters)

**Check etcd health:**
```bash
# On a master node
sudo k3s kubectl get nodes
sudo k3s etcd-snapshot ls
```

**Restore from snapshot:**
```bash
# List available snapshots
sudo k3s etcd-snapshot ls

# Restore
sudo k3s etcd-snapshot restore snapshot-name
```

## Next Steps

- [Configure monitoring](../advanced/monitoring.md)
- [Set up gateway proxy](../advanced/pipeops-gateway-proxy.md)
- [Configure backups](management.md)
- [Security hardening](configuration.md)

## Additional Resources

- [K3s High Availability Documentation](https://docs.k3s.io/datastore/ha)
- [Kubernetes Best Practices](https://kubernetes.io/docs/concepts/configuration/overview/)
- [PipeOps Support](https://support.pipeops.io)
