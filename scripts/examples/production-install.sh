#!/bin/bash

# Example: Production k3s Installation
# 
# This example shows how to install k3s for production use
# on a VM or bare metal server.

set -e

# Set your PipeOps token
export PIPEOPS_TOKEN="your-pipeops-token-here"

# Set production cluster name
export CLUSTER_NAME="production-cluster"

# Force k3s (production-grade)
export CLUSTER_TYPE="k3s"

# Optional: Specify k3s version
export K3S_VERSION="v1.28.3+k3s2"

# Run installer
../install.sh

echo ""
echo "Production cluster installed!"
echo "Get cluster token for worker nodes: cat /var/lib/rancher/k3s/server/node-token"
echo "Or use: ./install.sh cluster-info"
