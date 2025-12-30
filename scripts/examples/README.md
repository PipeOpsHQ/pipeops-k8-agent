# Installation Examples

This directory contains example scripts showing different ways to use the intelligent cluster installer.

## Examples

### 1. Basic Installation (Auto-Detection)

**File:** `basic-install.sh`

The simplest approach - let the system detect the best cluster type:

```bash
export PIPEOPS_TOKEN="your-token"
./basic-install.sh
```

**What it does:**
- Analyzes your system resources
- Detects environment (Docker, macOS, etc.)
- Chooses optimal cluster type
- Installs everything automatically

### 2. k3d Installation (Docker-Based)

**File:** `k3d-install.sh`

Fast Docker-based cluster for development:

```bash
export PIPEOPS_TOKEN="your-token"
./k3d-install.sh
```

**Use cases:**
- Local development
- Fast iteration
- Limited resources
- WSL2 on Windows

**Features:**
- Very fast startup (< 30 seconds)
- Multiple worker nodes
- Easy to create/destroy
- No VM overhead

### 3. Production k3s Installation

**File:** `production-install.sh`

Production-grade k3s for VMs and servers:

```bash
export PIPEOPS_TOKEN="your-token"
./production-install.sh
```

**Use cases:**
- Production workloads
- Cloud VMs (AWS, GCP, Azure)
- Bare metal servers
- Edge computing

**Features:**
- Lightweight and efficient
- Battle-tested
- Multi-node support
- HA capabilities

## Customization

All examples can be customized by setting environment variables:

### Common Variables

```bash
# Required
export PIPEOPS_TOKEN="your-token"

# Optional
export CLUSTER_NAME="my-cluster"
export PIPEOPS_API_URL="https://api.pipeops.io"
export NAMESPACE="pipeops-system"
export INSTALL_MONITORING="true"  # or "false" to skip
```

### Cluster-Specific Variables

#### k3s
```bash
export K3S_VERSION="v1.28.3+k3s2"
export NODE_TYPE="server"  # or "agent" for workers
```

#### k3d
```bash
export K3D_CLUSTER_NAME="pipeops"
export K3D_SERVERS="1"
export K3D_AGENTS="2"  # number of worker nodes
```

#### minikube
```bash
export MINIKUBE_DRIVER="docker"
export MINIKUBE_CPUS="2"
export MINIKUBE_MEMORY="2048"
```

#### kind
```bash
export KIND_CLUSTER_NAME="pipeops"
export KIND_WORKERS="2"
```

## Advanced Examples

### Multi-Node k3d Cluster

```bash
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="k3d"
export K3D_CLUSTER_NAME="dev-cluster"
export K3D_SERVERS="1"
export K3D_AGENTS="3"  # 3 worker nodes

../install.sh
```

### Without Monitoring Stack

```bash
export PIPEOPS_TOKEN="your-token"
export INSTALL_MONITORING="false"  # Skip monitoring

../install.sh
```

### Force Specific Type (No Auto-Detection)

```bash
export PIPEOPS_TOKEN="your-token"
export AUTO_DETECT="false"
export CLUSTER_TYPE="k3s"

../install.sh
```

## Testing Your Environment

Before installing, you can check what the system would recommend:

```bash
# Show detailed analysis
../detect-cluster-type.sh info

# Just get the recommendation
RECOMMENDED=$(../detect-cluster-type.sh recommend)
echo "System recommends: $RECOMMENDED"

# See scoring breakdown
../detect-cluster-type.sh scores
```

## Creating Your Own Examples

1. Copy one of the existing examples
2. Modify environment variables
3. Add any custom logic
4. Run and test

Example structure:

```bash
#!/bin/bash
set -e

# Set required variables
export PIPEOPS_TOKEN="your-token"
export CLUSTER_TYPE="your-choice"

# Configure cluster
export YOUR_VARIABLE="value"

# Run installer
../install.sh

# Post-installation steps
echo "Installation complete!"
```

## Troubleshooting Examples

If an example fails:

1. Check the error message
2. Verify prerequisites (Docker for k3d/kind, etc.)
3. Check system resources
4. Try a different cluster type:
   ```bash
   export CLUSTER_TYPE="k3d"  # Try alternative
   ```
5. Run with auto-detection to see what's recommended

## Support

For help with examples:
- Check main documentation: [INTELLIGENT_CLUSTER_SETUP.md](../../docs/INTELLIGENT_CLUSTER_SETUP.md)
- Review scripts README: [scripts/README.md](../README.md)
- File an issue: https://github.com/PipeOpsHQ/pipeops-k8-agent/issues
