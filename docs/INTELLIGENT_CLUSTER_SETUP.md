# Intelligent Cluster Setup Guide

This guide explains the intelligent cluster detection and installation system for PipeOps agents.

## Overview

The PipeOps installer now automatically detects your environment and chooses the optimal Kubernetes distribution for your system. It supports:

- **k3s**: Lightweight Kubernetes for production workloads
- **minikube**: Local development cluster
- **k3d**: k3s in Docker for fast iteration
- **kind**: Kubernetes in Docker for testing and CI/CD

## Quick Start

### Automatic Detection (Recommended)

Simply set your PipeOps token and run the installer:

```bash
export PIPEOPS_TOKEN="your-token-here"
export CLUSTER_NAME="my-cluster"

curl -sSL https://get.pipeops.io/agent | bash
```

Or download and run locally:

```bash
git clone https://github.com/PipeOpsHQ/pipeops-k8-agent.git
cd pipeops-k8-agent/scripts

export PIPEOPS_TOKEN="your-token-here"
./install.sh
```

The installer will:
1. Analyze your system (CPU, memory, disk, environment)
2. Calculate suitability scores for each cluster type
3. Choose the best match
4. Install the cluster
5. Deploy the PipeOps agent
6. Install monitoring stack (Prometheus, Loki, Grafana, OpenCost)

### Manual Cluster Selection

If you prefer to choose the cluster type manually:

```bash
export PIPEOPS_TOKEN="your-token-here"
export CLUSTER_TYPE="k3d"  # Choose: k3s, minikube, k3d, kind

./install.sh
```

### Disable Auto-Detection

To force a specific cluster type without detection:

```bash
export PIPEOPS_TOKEN="your-token-here"
export AUTO_DETECT="false"
export CLUSTER_TYPE="k3s"

./install.sh
```

## Detection Logic

### How It Works

The detection system analyzes multiple factors:

1. **System Resources**
   - Total memory (RAM)
   - Available memory
   - CPU cores
   - Disk space

2. **Environment**
   - Operating system (Linux, macOS)
   - Docker availability
   - Container type (Docker, LXC, WSL)
   - CI/CD environment detection

3. **Suitability Scoring**
   - Each cluster type gets a base score
   - Bonuses added based on compatibility
   - Highest score wins

### Cluster Type Selection

#### k3s (Production-Grade)

**Best for:**
- Production VMs and bare metal
- Cloud instances (AWS, GCP, Azure)
- Private data centers
- Edge computing

**Requirements:**
- 512MB+ RAM (1GB+ recommended)
- 1+ CPU core
- 2GB+ disk space
- Linux operating system
- Not running in Docker container

**Scoring:**
- Base score: 50
- Production environment: +30
- Good resources (2GB+ RAM): +10
- Multi-core CPU: +5
- 4GB+ RAM & 2+ CPUs: +15
- LXC container: +10

**Example environments:**
- Ubuntu 20.04 VM with 2GB RAM, 2 CPUs → Score: 110
- Debian bare metal with 4GB RAM, 4 CPUs → Score: 110
- Proxmox LXC container with 2GB RAM → Score: 100

#### minikube (Development)

**Best for:**
- Local development on macOS
- Testing on workstations
- Learning Kubernetes

**Requirements:**
- 2GB+ RAM
- 2+ CPU cores
- 20GB+ disk space
- Docker or VM driver (VirtualBox, KVM, Hyperkit)

**Scoring:**
- Base score: 40
- macOS system: +40
- Docker available: +10
- Development workload (4-8GB RAM): +10

**Example environments:**
- macOS with Docker Desktop → Score: 90
- Linux workstation with KVM → Score: 60

#### k3d (Fast & Lightweight)

**Best for:**
- Docker-based development
- Resource-constrained systems
- Fast cluster creation/deletion
- WSL2 on Windows

**Requirements:**
- 1GB+ RAM
- 1+ CPU core
- 5GB+ disk space
- Docker

**Scoring:**
- Base score: 45
- Docker available: +15
- Limited resources (1-2GB RAM): +20
- Docker/WSL environment: +25
- Fast startup bonus: +10

**Example environments:**
- WSL2 with Docker → Score: 90
- Linux laptop with 2GB RAM → Score: 80
- Development VM with Docker → Score: 70

#### kind (Testing & CI/CD)

**Best for:**
- CI/CD pipelines
- Multi-node testing
- Kubernetes conformance testing
- GitHub Actions, GitLab CI

**Requirements:**
- 2GB+ RAM
- 2+ CPU cores
- 10GB+ disk space
- Docker

**Scoring:**
- Base score: 35
- Docker available: +10
- Good resources (4GB+ RAM, 2+ CPUs): +15
- CI environment detected: +20

**Example environments:**
- GitHub Actions runner → Score: 70
- GitLab CI with Docker → Score: 70
- Local testing setup → Score: 60

### View Detection Details

Use the detection script to see what the system recommends:

```bash
cd scripts

# Show full analysis
./detect-cluster-type.sh info

# Get just the recommendation
./detect-cluster-type.sh recommend

# See raw scores
./detect-cluster-type.sh scores
```

Example output:

```
System Resources:
  • Operating System: ubuntu (Linux)
  • Total Memory: 8192MB
  • Available Memory: 6144MB
  • CPU Cores: 4
  • Available Disk Space: 50GB

Environment:
  • Running in Docker container: No
  • Running in LXC container: No
  • Running in WSL: No
  • Docker available: Yes

Cluster Type Compatibility:
  • k3s: ✓ Compatible
  • minikube: ✗ Not compatible (disk space < 20GB)
  • k3d: ✓ Compatible
  • kind: ✓ Compatible

Suitability Scores (higher is better):
  • k3s: 110
  • minikube: 0
  • k3d: 70
  • kind: 80

Recommended: k3s
```

## Monitoring Stack

The installer automatically deploys a complete monitoring stack:

### Components

1. **Prometheus** - Metrics collection and storage
2. **Loki** - Log aggregation
3. **Grafana** - Visualization and dashboards
4. **OpenCost** - Kubernetes cost monitoring

### Accessing Monitoring

#### Grafana Dashboard

```bash
# Port forward Grafana
kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80

# Open in browser
http://localhost:3000

# Default credentials
Username: admin
Password: Get with: kubectl get secret -n monitoring prometheus-grafana \
  -o jsonpath="{.data.admin-password}" | base64 -d
```

#### Prometheus UI

```bash
# Port forward Prometheus
kubectl port-forward -n monitoring svc/prometheus-kube-prometheus-prometheus 9090:9090

# Open in browser
http://localhost:9090
```

#### OpenCost

```bash
# Port forward OpenCost
kubectl port-forward -n monitoring svc/opencost 9003:9003

# Open in browser
http://localhost:9003
```

### Disabling Monitoring

To install without the monitoring stack:

```bash
export INSTALL_MONITORING=false
./install.sh
```

### Installing Monitoring Separately

If you skipped monitoring during installation:

```bash
# Source the installation functions
source scripts/install.sh

# Install monitoring
install_monitoring_stack
```

## Advanced Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CLUSTER_TYPE` | Cluster type to install | `auto` |
| `AUTO_DETECT` | Enable auto-detection | `true` |
| `INSTALL_MONITORING` | Install monitoring stack | `true` |
| `PIPEOPS_TOKEN` | PipeOps authentication token | Required |
| `CLUSTER_NAME` | Cluster identifier | `default-cluster` |
| `NAMESPACE` | Agent namespace | `pipeops-system` |

### Cluster-Specific Variables

#### k3s

| Variable | Description | Default |
|----------|-------------|---------|
| `K3S_VERSION` | k3s version | `v1.28.3+k3s2` |
| `NODE_TYPE` | `server` or `agent` | `server` |
| `K3S_URL` | Master URL (for workers) | - |
| `K3S_TOKEN` | Cluster token (for workers) | - |

#### minikube

| Variable | Description | Default |
|----------|-------------|---------|
| `MINIKUBE_VERSION` | minikube version | `latest` |
| `MINIKUBE_DRIVER` | Driver to use | `docker` |
| `MINIKUBE_CPUS` | CPU cores | `2` |
| `MINIKUBE_MEMORY` | Memory in MB | `2048` |
| `KUBERNETES_VERSION` | Kubernetes version | `stable` |

#### k3d

| Variable | Description | Default |
|----------|-------------|---------|
| `K3D_CLUSTER_NAME` | Cluster name | `pipeops` |
| `K3D_K3S_VERSION` | k3s version | `v1.28.3-k3s2` |
| `K3D_SERVERS` | Number of servers | `1` |
| `K3D_AGENTS` | Number of agents | `0` |
| `K3D_API_PORT` | API server port | `6443` |

#### kind

| Variable | Description | Default |
|----------|-------------|---------|
| `KIND_CLUSTER_NAME` | Cluster name | `pipeops` |
| `KIND_K8S_VERSION` | Kubernetes version | Latest |
| `KIND_WORKERS` | Number of workers | `0` |

## Use Cases

### Local Development (macOS)

```bash
# Auto-detection will choose minikube
export PIPEOPS_TOKEN="your-token"
./install.sh
```

### Local Development (Linux with Docker)

```bash
# Auto-detection likely chooses k3d for speed
export PIPEOPS_TOKEN="your-token"
./install.sh
```

### Production VM

```bash
# Auto-detection will choose k3s
export PIPEOPS_TOKEN="your-token"
export CLUSTER_NAME="prod-cluster"
./install.sh
```

### CI/CD Pipeline

```bash
# Auto-detection will choose kind
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="kind"  # Or let it auto-detect
./install.sh
```

### WSL2 on Windows

```bash
# Auto-detection will choose k3d
export PIPEOPS_TOKEN="your-token"
./install.sh
```

### Resource-Constrained Server

```bash
# Force k3s (most lightweight for non-Docker)
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="k3s"
./install.sh
```

### Multi-Node Testing

```bash
# Use kind with workers
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="kind"
export KIND_WORKERS="2"
./install.sh
```

## Troubleshooting

### No Compatible Cluster Type

**Problem:** Detection shows "none" - no cluster type compatible

**Solution:**
- Check system resources (RAM, CPU, disk)
- Ensure Docker is installed for minikube/k3d/kind
- For k3s, ensure running on Linux (not in Docker container)
- Manually install missing requirements

### Wrong Cluster Type Selected

**Problem:** Auto-detection chose wrong cluster type

**Solution:**
```bash
# Manually specify cluster type
export CLUSTER_TYPE="k3d"
./install.sh
```

### Installation Fails

**Problem:** Cluster installation fails

**Solution:**
1. Check requirements for the chosen cluster type
2. Review installation logs
3. Try a different cluster type:
   ```bash
   export CLUSTER_TYPE="k3d"  # Try alternative
   ./install.sh
   ```

### Monitoring Stack Issues

**Problem:** Monitoring installation fails or times out

**Solution:**
```bash
# Skip monitoring during initial install
export INSTALL_MONITORING=false
./install.sh

# Install monitoring separately later
source scripts/install.sh
install_monitoring_stack
```

## Testing

Run the test suite to verify the detection system:

```bash
cd scripts
./test-detection.sh
```

This will test:
- Script existence and executability
- Detection logic
- Recommendation validity
- Score calculation
- Installation script integration

## Migration from k3s-Only Setup

If you have an existing k3s-only setup:

1. The new installer is backward compatible
2. Setting `CLUSTER_TYPE=k3s` or `AUTO_DETECT=false` gives same behavior
3. All existing environment variables work as before
4. Worker node setup unchanged

## Future Enhancements

Planned improvements:

- [ ] Support for additional cluster types (microk8s, k0s)
- [ ] Cloud-specific optimizations (EKS, GKE, AKS detection)
- [ ] Custom scoring rules
- [ ] Cluster migration tools
- [ ] Performance benchmarking for each type

## Support

For issues or questions:
- Check logs: `kubectl logs -n pipeops-system deployment/pipeops-agent`
- Run detection: `./detect-cluster-type.sh info`
- File an issue: https://github.com/PipeOpsHQ/pipeops-k8-agent/issues
- Contact: support@pipeops.io
