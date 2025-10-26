# PipeOps Installation Scripts

This directory contains scripts for installing and managing PipeOps agents on Kubernetes clusters with **intelligent cluster type detection**.

## 🎯 Key Features

- **Intelligent Cluster Detection**: Automatically detects your environment and chooses the best Kubernetes distribution
- **Multi-Cluster Support**: Supports k3s, minikube, k3d, and kind
- **Complete Setup**: Installs monitoring stack (Prometheus, Loki, Grafana, OpenCost) by default
- **Production Ready**: Works on bare metal, VMs, cloud instances, and local development
- **Resource-Aware**: Adapts to your available resources (CPU, memory, disk)

## Scripts Overview

### `install.sh` - Intelligent Installation Script

The primary script for installing Kubernetes clusters and the PipeOps agent. Automatically detects the best cluster type for your environment or allows manual selection.

**New in this version:**
- Automatic cluster type detection based on system resources
- Support for k3s, minikube, k3d, and kind
- Integrated monitoring stack installation
- Smart environment detection (Docker, LXC, WSL, macOS)

### `detect-cluster-type.sh` - Environment Detection

Analyzes system resources and environment to recommend the optimal Kubernetes distribution.

#### Quick Start - Auto-Detection (Recommended)

```bash
# Let the installer detect the best cluster type for your system
export PIPEOPS_TOKEN="your-pipeops-token"
export CLUSTER_NAME="my-cluster"

./install.sh
```

The installer will:
1. Analyze your system resources (CPU, memory, disk)
2. Detect your environment (Docker, LXC, WSL, macOS)
3. Choose the optimal Kubernetes distribution
4. Install the cluster and all required components
5. Deploy the PipeOps agent
6. Install monitoring stack (Prometheus, Loki, Grafana, OpenCost)

#### Manual Cluster Type Selection

```bash
# Explicitly choose a cluster type
export PIPEOPS_TOKEN="your-pipeops-token"
export CLUSTER_TYPE=k3d  # Options: k3s, minikube, k3d, kind

./install.sh
```

#### Server Node Installation (Traditional)

```bash
# Set required environment variables
export PIPEOPS_TOKEN="your-pipeops-token"
export CLUSTER_NAME="my-production-cluster"

# Run the installer
./install.sh
```

#### Worker Node Installation

```bash
# Set environment variables for worker node
export NODE_TYPE="agent"
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="cluster-token-from-master"

# Run the installer
./install.sh
```

#### Available Commands

```bash
./install.sh                # Install (default)
./install.sh install        # Install explicitly
./install.sh uninstall      # Remove k3s and agent
./install.sh update          # Update agent to latest version
./install.sh cluster-info    # Show cluster connection info for workers
./install.sh help            # Show usage information
```

### `join-worker.sh` - Simplified Worker Join Script

A simplified script specifically for joining worker nodes to an existing cluster.

```bash
# Usage
./join-worker.sh <MASTER_IP> <CLUSTER_TOKEN>

# Example
./join-worker.sh 192.168.1.100 K10abc123def456::server:abc123
```

## Environment Variables

### Cluster Detection Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `CLUSTER_TYPE` | Cluster type to install | No | `auto` |
| `AUTO_DETECT` | Enable automatic detection | No | `true` |

**Cluster Types:**
- `auto` - Automatically detect best type (recommended)
- `k3s` - Lightweight Kubernetes for production
- `minikube` - Local development cluster
- `k3d` - k3s in Docker (fast setup)
- `kind` - Kubernetes in Docker (testing)

### Server Node Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `PIPEOPS_TOKEN` | PipeOps authentication token | Yes | - |
| `AGENT_TOKEN` | Alias for PIPEOPS_TOKEN (backward compatibility) | Yes | - |
| `CLUSTER_NAME` | Cluster identifier | No | `default-cluster` |
| `PIPEOPS_API_URL` | PipeOps API endpoint | No | `https://api.pipeops.sh` |
| `K3S_VERSION` | k3s version to install | No | `v1.28.3+k3s2` |
| `AGENT_IMAGE` | PipeOps agent Docker image | No | `ghcr.io/pipeopshq/pipeops-k8-agent:latest` |
| `NAMESPACE` | Kubernetes namespace for agent | No | `pipeops-system` |

### Worker Node Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `NODE_TYPE` | Set to `agent` for worker nodes | Yes | `server` |
| `K3S_URL` | Master server URL | Yes | - |
| `K3S_TOKEN` | Cluster token from master | Yes | - |
| `K3S_VERSION` | k3s version to install | No | `v1.28.3+k3s2` |

## Cluster Type Selection Logic

The installer uses a sophisticated scoring system to recommend the best cluster type:

### k3s (Production-Grade)
**Best for:** Production VMs, bare metal, cloud instances

**Requirements:**
- 512MB+ RAM (1GB+ recommended)
- 1+ CPU core
- 2GB+ disk space
- Linux OS
- Not in Docker container

**Score Bonuses:**
- Production environments: +30
- Good resources (2GB+ RAM): +10
- Multi-core CPU: +5
- LXC containers: +10 (with proper config)

### minikube (Development)
**Best for:** macOS, local development, testing

**Requirements:**
- 2GB+ RAM
- 2+ CPU cores
- 20GB+ disk space
- Docker or VM driver

**Score Bonuses:**
- macOS systems: +40
- Docker available: +10
- Development workloads: +10

### k3d (Fast & Lightweight)
**Best for:** Docker environments, resource-constrained systems, fast iteration

**Requirements:**
- 1GB+ RAM
- 1+ CPU core
- 5GB+ disk space
- Docker

**Score Bonuses:**
- Docker available: +15
- Limited resources (1-2GB RAM): +20
- Docker/WSL environments: +25
- Fast startup: +10

### kind (Testing & CI/CD)
**Best for:** CI/CD pipelines, multi-node testing, Kubernetes conformance testing

**Requirements:**
- 2GB+ RAM
- 2+ CPU cores
- 10GB+ disk space
- Docker

**Score Bonuses:**
- CI environment detected: +20
- Good resources (4GB+ RAM): +15
- Docker available: +10

### View Detection Results

```bash
# Show detailed system analysis and recommendation
./detect-cluster-type.sh info

# Get just the recommendation
./detect-cluster-type.sh recommend

# See scoring breakdown
./detect-cluster-type.sh scores
```

## Deployment Scenarios

### Single Node Cluster

Perfect for development or small workloads:

```bash
export AGENT_TOKEN="your-token"
./install.sh
```

### Multi-Node Cluster

1. **Setup master node:**
   ```bash
   export AGENT_TOKEN="your-token"
   export CLUSTER_NAME="production-cluster"
   ./install.sh
   ```

2. **Get cluster connection info:**
   ```bash
   ./install.sh cluster-info
   ```

3. **Join worker nodes:**
   ```bash
   # On each worker node
   ./join-worker.sh <master-ip> <cluster-token>
   ```

### High Availability Cluster

For production environments with multiple master nodes:

1. **First master (bootstrap):**
   ```bash
   export AGENT_TOKEN="your-token"
   export CLUSTER_NAME="ha-cluster"
   # Add cluster-init flag manually after installation
   ```

2. **Additional masters:**
   ```bash
   export NODE_TYPE="server"
   export K3S_URL="https://first-master:6443"
   export K3S_TOKEN="cluster-token"
   export AGENT_TOKEN="your-token"
   ./install.sh
   ```

## Special Environments

### Proxmox LXC Containers

The script automatically detects LXC environments and applies appropriate configurations:

- Disables Traefik and ServiceLB
- Uses host-gw flannel backend
- Sets appropriate kube-proxy settings

### Cloud Providers

Works on all major cloud providers:
- AWS EC2
- Google Cloud Compute Engine
- Azure Virtual Machines
- DigitalOcean Droplets
- Linode
- Vultr

## Monitoring Stack

The installer automatically deploys a complete monitoring stack:

### Components Installed

1. **Prometheus** (Metrics Collection)
   - Collects cluster and application metrics
   - Stores time-series data
   - Provides query interface

2. **Loki** (Log Aggregation)
   - Collects logs from all pods
   - Efficient log storage and querying
   - Integrates with Grafana

3. **Grafana** (Visualization)
   - Pre-configured dashboards
   - Metrics and logs visualization
   - Alerting capabilities

4. **OpenCost** (Cost Monitoring)
   - Kubernetes cost allocation
   - Resource usage tracking
   - Budget monitoring

### Accessing Monitoring

```bash
# Access Grafana dashboard
kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80
# Then open http://host.docker.internal:3000 in your browser

# Default Grafana credentials:
# Username: admin
# Password: prom-operator (check with: kubectl get secret -n monitoring prometheus-grafana -o jsonpath="{.data.admin-password}" | base64 -d)

# Access Prometheus UI
kubectl port-forward -n monitoring svc/prometheus-kube-prometheus-prometheus 9090:9090
# Then open http://host.docker.internal:9090

# Access OpenCost
kubectl port-forward -n monitoring svc/opencost 9003:9003
# Then open http://host.docker.internal:9003
```

### Customizing Monitoring

To disable monitoring stack installation:

```bash
export INSTALL_MONITORING=false
./install.sh
```

To install monitoring separately:

```bash
# Source the install functions
source ./install.sh

# Install only monitoring
install_monitoring_stack
```

## Troubleshooting

### Common Issues

#### "AGENT_TOKEN is required"
```bash
export AGENT_TOKEN="your-actual-token-here"
./install.sh
```

#### "Could not determine server IP"
```bash
export SERVER_IP="your-server-ip"
./install.sh
```

#### Worker node connection fails
```bash
# Check master node firewall allows port 6443
# Verify token and URL are correct
./install.sh cluster-info  # Run on master to get correct values
```

#### k3s fails to start in LXC
```bash
# Ensure LXC container has appropriate capabilities
# Check if the script detected LXC correctly
```

### Debug Mode

Enable debug logging:

```bash
export PIPEOPS_LOG_LEVEL=debug
./install.sh
```

### Log Locations

- **k3s server logs:** `journalctl -u k3s -f`
- **k3s agent logs:** `journalctl -u k3s-agent -f`
- **PipeOps agent logs:** `kubectl logs -f deployment/pipeops-agent -n pipeops-system`

## Security Considerations

### Network Security

- Only outbound connections required from worker nodes
- k3s API (port 6443) only needs to be accessible from worker nodes
- Agent communicates securely with PipeOps control plane via TLS

### Firewall Rules

#### Master Node
```bash
# Allow k3s API access from worker nodes
ufw allow from <worker-subnet> to any port 6443

# Optional: Allow kubectl access from management networks
ufw allow from <management-subnet> to any port 6443
```

#### Worker Nodes
```bash
# No inbound ports required
# Only outbound HTTPS (443) to PipeOps API needed
```

### Token Security

- Store cluster tokens securely
- Rotate tokens regularly using k3s built-in token rotation
- Use unique tokens per cluster

## Performance Tuning

### Resource Requirements

#### Minimum (Development)
- **CPU:** 1 core
- **Memory:** 512 MB
- **Disk:** 2 GB

#### Recommended (Production)
- **CPU:** 2+ cores
- **Memory:** 2+ GB
- **Disk:** 10+ GB SSD

### Network Requirements

- **Bandwidth:** 1 Mbps minimum, 10 Mbps recommended
- **Latency:** <100ms between nodes, <500ms to PipeOps API

## Backup and Recovery

### Cluster Backup

```bash
# Backup k3s data
sudo tar czf k3s-backup-$(date +%Y%m%d).tar.gz /var/lib/rancher/k3s

# Backup PipeOps config
kubectl get secret pipeops-agent-config -n pipeops-system -o yaml > pipeops-config-backup.yaml
```

### Cluster Recovery

```bash
# Restore k3s data (on clean system)
sudo tar xzf k3s-backup-YYYYMMDD.tar.gz -C /

# Reinstall k3s and agent
./install.sh

# Restore PipeOps config if needed
kubectl apply -f pipeops-config-backup.yaml
```

## Support

For issues with these scripts:

1. Check the troubleshooting section above
2. Review logs for error messages
3. Ensure all prerequisites are met
4. Contact PipeOps support with detailed error information

## Contributing

When modifying these scripts:

1. Test on multiple Linux distributions
2. Verify both server and worker installation modes
3. Test in LXC/container environments
4. Update documentation for any new features or requirements
