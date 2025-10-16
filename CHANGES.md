# Intelligent Cluster Setup - Change Log

## Overview

The PipeOps installation system has been upgraded with intelligent cluster detection and multi-distribution support. The installer now automatically analyzes your environment and chooses the optimal Kubernetes distribution.

## What Changed

### Before (k3s-Only)
- Only supported k3s installation
- Required Linux environment
- Manual configuration needed
- No environment detection
- Limited deployment options

### After (Intelligent Multi-Cluster)
- Supports k3s, minikube, k3d, and kind
- Works on Linux, macOS, Windows (WSL)
- Automatic environment detection
- Intelligent cluster type selection
- Complete monitoring stack included
- Production-ready out of the box

## New Features

### 1. Intelligent Detection System

**What it does:**
- Analyzes CPU, memory, disk space
- Detects Docker availability
- Identifies environment type (VM, container, WSL, macOS)
- Calculates suitability scores
- Recommends optimal cluster type

**Usage:**
```bash
# View system analysis
./scripts/detect-cluster-type.sh info

# Get recommendation
./scripts/detect-cluster-type.sh recommend
```

### 2. Multi-Cluster Support

Four cluster types now supported:

**k3s** - Production Grade
- Best for: VMs, bare metal, cloud instances
- Lightweight, efficient, battle-tested
- Supports multi-node clusters
- Full HA capabilities

**minikube** - Development
- Best for: macOS, local development
- Easy to use, well-documented
- Good for learning Kubernetes
- VM-based isolation

**k3d** - Fast & Lightweight
- Best for: Docker environments, WSL
- Extremely fast startup (< 30s)
- Docker-based, no VM overhead
- Perfect for rapid iteration

**kind** - Testing & CI/CD
- Best for: CI/CD pipelines, testing
- Kubernetes conformance testing
- Multi-node support
- GitHub Actions/GitLab CI friendly

### 3. Complete Monitoring Stack

Automatically installed:
- **Prometheus**: Metrics collection and storage
- **Loki**: Log aggregation and querying
- **Grafana**: Visualization dashboards
- **OpenCost**: Kubernetes cost monitoring

Access after installation:
```bash
# Grafana
kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80

# Prometheus
kubectl port-forward -n monitoring svc/prometheus-kube-prometheus-prometheus 9090:9090

# OpenCost
kubectl port-forward -n monitoring svc/opencost 9003:9003
```

### 4. Environment-Aware Installation

Detects and adapts to:
- Docker containers
- LXC containers (Proxmox)
- WSL (Windows Subsystem for Linux)
- macOS systems
- CI/CD environments (GitHub Actions, GitLab CI)

### 5. Scoring System

Each cluster type receives a score (0-110) based on:
- System resources (RAM, CPU, disk)
- Environment compatibility
- Use case suitability
- Performance characteristics

Example scores:
```
Production VM (8GB RAM, 4 CPU):
  k3s: 110 ← Selected
  k3d: 70
  kind: 80
  minikube: 0 (insufficient disk space)

macOS Developer Machine:
  minikube: 90 ← Selected
  k3d: 70
  kind: 60
  k3s: 0 (not Linux)

WSL2 Environment:
  k3d: 90 ← Selected
  kind: 70
  k3s: 0 (Docker container)
  minikube: 0 (resource constraints)
```

## Migration Guide

### For Existing k3s Users

**Good news:** Fully backward compatible!

Your existing installations will continue to work. The new system adds capabilities without breaking existing functionality.

**If you want to keep using k3s only:**
```bash
export AUTO_DETECT=false
export CLUSTER_TYPE=k3s
./scripts/install.sh
```

**To try auto-detection:**
```bash
export PIPEOPS_TOKEN="your-token"
./scripts/install.sh
# Will detect and recommend best type
```

### For New Users

**Recommended approach (auto-detection):**
```bash
export PIPEOPS_TOKEN="your-token"
curl -sSL https://get.pipeops.io/agent | bash
```

**Manual selection:**
```bash
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="k3d"  # or k3s, minikube, kind
./scripts/install.sh
```

## Configuration Options

### New Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CLUSTER_TYPE` | Cluster type (auto/k3s/minikube/k3d/kind) | `auto` |
| `AUTO_DETECT` | Enable auto-detection | `true` |
| `INSTALL_MONITORING` | Install monitoring stack | `true` |

### Cluster-Specific Variables

**k3s:**
- `K3S_VERSION` - Version to install
- `NODE_TYPE` - server or agent (worker)
- `K3S_URL` - Master URL (for workers)
- `K3S_TOKEN` - Cluster token (for workers)

**minikube:**
- `MINIKUBE_VERSION` - Version to install
- `MINIKUBE_DRIVER` - Driver to use (docker, virtualbox, etc.)
- `MINIKUBE_CPUS` - CPU cores to allocate
- `MINIKUBE_MEMORY` - Memory in MB

**k3d:**
- `K3D_CLUSTER_NAME` - Cluster name
- `K3D_SERVERS` - Number of server nodes
- `K3D_AGENTS` - Number of worker nodes
- `K3D_API_PORT` - API server port

**kind:**
- `KIND_CLUSTER_NAME` - Cluster name
- `KIND_WORKERS` - Number of worker nodes
- `KIND_K8S_VERSION` - Kubernetes version

## Example Use Cases

### 1. Local Development on macOS
```bash
export PIPEOPS_TOKEN="your-token"
# Auto-detects and installs minikube
./scripts/install.sh
```

### 2. Production on Cloud VM
```bash
export PIPEOPS_TOKEN="your-token"
export CLUSTER_NAME="prod-cluster"
# Auto-detects and installs k3s
./scripts/install.sh
```

### 3. Fast Docker-based Dev
```bash
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="k3d"
export K3D_AGENTS="2"  # 2 worker nodes
./scripts/install.sh
```

### 4. CI/CD Pipeline
```bash
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="kind"
# Auto-detected in GitHub Actions
./scripts/install.sh
```

### 5. WSL2 on Windows
```bash
export PIPEOPS_TOKEN="your-token"
# Auto-detects and installs k3d
./scripts/install.sh
```

## Testing

### Run Tests
```bash
cd scripts
./test-detection.sh
```

### Test Results
All 10 tests passing:
- Detection script functionality ✅
- Recommendation validity ✅
- Info command ✅
- Scores calculation ✅
- Installation scripts ✅
- Help command ✅

### Manual Testing
```bash
# Test detection
./scripts/detect-cluster-type.sh info

# Test specific cluster type
export CLUSTER_TYPE="k3d"
export PIPEOPS_TOKEN="test-token"
# ./scripts/install.sh  # (would install)
```

## Documentation

### New Documentation
1. **[INTELLIGENT_CLUSTER_SETUP.md](docs/INTELLIGENT_CLUSTER_SETUP.md)**
   - Complete installation guide
   - Cluster type comparison
   - Scoring system details
   - Use cases and examples

2. **[scripts/README.md](scripts/README.md)**
   - Updated with new features
   - Detection logic explained
   - Installation examples

3. **[scripts/examples/](scripts/examples/)**
   - Basic installation example
   - k3d installation example
   - Production installation example
   - Examples README

### Updated Documentation
- Main README.md with new quick start
- Added documentation links
- Installation instructions updated

## Benefits

### For Users
1. **Zero Configuration** - Works out of the box
2. **Optimal Setup** - Gets the best cluster for your environment
3. **Complete Solution** - Monitoring included
4. **Time Saving** - No manual cluster selection needed
5. **Better Experience** - Works on more platforms

### For Developers
1. **Fast Iteration** - k3d/kind for rapid development
2. **Realistic Testing** - k3s for production-like environments
3. **CI/CD Ready** - Automatic kind selection
4. **Cross-Platform** - Works on macOS, Linux, Windows

### For Operations
1. **Production Ready** - Intelligent k3s selection
2. **Monitoring Included** - Prometheus, Grafana, Loki, OpenCost
3. **Resource Efficient** - Optimal cluster for resources
4. **Well Documented** - Comprehensive guides

## Performance

### Installation Time
- k3d: ~30 seconds (Docker-based)
- kind: ~1 minute (Docker-based)
- k3s: ~2 minutes (native)
- minikube: ~3 minutes (VM-based)

### Resource Usage

**Minimum Requirements by Type:**

| Type | RAM | CPU | Disk | Notes |
|------|-----|-----|------|-------|
| k3d | 1GB | 1 | 5GB | Docker required |
| kind | 2GB | 2 | 10GB | Docker required |
| k3s | 512MB | 1 | 2GB | Linux required |
| minikube | 2GB | 2 | 20GB | Driver required |

## Breaking Changes

**None!** This update is fully backward compatible.

- Existing k3s installations work unchanged
- All previous environment variables supported
- Worker node setup unchanged
- Can explicitly disable auto-detection

## Future Enhancements

Planned improvements:
- Support for microk8s and k0s
- Cloud-specific optimizations (EKS, GKE, AKS)
- Custom scoring rules
- Cluster migration tools
- Advanced multi-node configurations

## Support

### Getting Help
1. Check documentation: [docs/INTELLIGENT_CLUSTER_SETUP.md](docs/INTELLIGENT_CLUSTER_SETUP.md)
2. Review examples: [scripts/examples/](scripts/examples/)
3. Run detection: `./scripts/detect-cluster-type.sh info`
4. File an issue: https://github.com/PipeOpsHQ/pipeops-k8-agent/issues
5. Contact: support@pipeops.io

### Common Questions

**Q: Will this break my existing k3s setup?**
A: No, it's fully backward compatible.

**Q: Can I force a specific cluster type?**
A: Yes, set `CLUSTER_TYPE=k3s` (or minikube, k3d, kind).

**Q: Can I disable auto-detection?**
A: Yes, set `AUTO_DETECT=false`.

**Q: Is monitoring required?**
A: No, set `INSTALL_MONITORING=false` to skip.

**Q: Which cluster type is best?**
A: Run `./scripts/detect-cluster-type.sh info` to see what's recommended for your system.

## Summary

This update transforms the PipeOps installer from a k3s-only tool into an intelligent, multi-distribution installation system that automatically adapts to your environment. It provides:

✅ Intelligent detection and recommendation  
✅ Support for 4 cluster types  
✅ Complete monitoring stack  
✅ Environment awareness  
✅ Production-ready setup  
✅ Comprehensive documentation  
✅ Full backward compatibility  

The result is a significantly improved user experience while maintaining all existing functionality.
