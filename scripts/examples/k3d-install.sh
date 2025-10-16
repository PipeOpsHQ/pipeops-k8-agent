#!/bin/bash

# Example: k3d Installation (Fast Docker-based Cluster)
# 
# This example shows how to install using k3d for fast, lightweight
# Docker-based Kubernetes development.

set -e

# Set your PipeOps token
export PIPEOPS_TOKEN="your-pipeops-token-here"

# Explicitly choose k3d
export CLUSTER_TYPE="k3d"

# Optional: Configure k3d settings
export K3D_CLUSTER_NAME="pipeops-dev"
export K3D_SERVERS="1"
export K3D_AGENTS="2"  # 2 worker nodes

# Run installer
../install.sh

echo ""
echo "k3d cluster created successfully!"
echo "List clusters: k3d cluster list"
echo "Delete cluster: k3d cluster delete $K3D_CLUSTER_NAME"
