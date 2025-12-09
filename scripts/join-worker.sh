#!/bin/bash

# PipeOps Worker Node Join Script
# Simple script to join a worker node to an existing k3s cluster

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to show usage
show_usage() {
    echo "Usage: $0"
    echo ""
    echo "This script joins a worker node to an existing k3s cluster."
    echo "It expects the K3S_URL and K3S_TOKEN to be provided as environment variables."
    echo ""
    echo "Environment Variables:"
    echo "  K3S_URL        URL of the k3s master node (e.g., https://192.168.1.100:6443)"
    echo "  K3S_TOKEN      Cluster token from the master node"
    echo ""
    echo "Example:"
    echo "  K3S_URL="https://192.168.1.100:6443" K3S_TOKEN="K10abc123def456::server:abc123" $0"
    echo ""
    echo "To get the cluster token from the master node, run:"
    echo "  sudo cat /var/lib/rancher/k3s/server/node-token"
    echo ""
    echo "Or use the main installer: (this provides the K3S_URL and K3S_TOKEN env vars)"
    echo "  ./install.sh cluster-info"
    echo ""
}

# Validate environment variables
if [ -z "$K3S_URL" ]; then
    print_error "K3S_URL environment variable is not set."
    show_usage
    exit 1
fi

if [ -z "$K3S_TOKEN" ]; then
    print_error "K3S_TOKEN environment variable is not set."
    show_usage
    exit 1
fi

# Set environment variables for worker installation
export NODE_TYPE="agent"
# K3S_URL and K3S_TOKEN are already expected to be set as environment variables

echo ""
echo -e "${BLUE}╔══════════════════════════════════════╗${NC}"
echo -e "${BLUE}║     PipeOps Worker Node Setup        ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════╝${NC}"
echo ""

print_status "Cluster URL: $K3S_URL"
print_status "Token: ${K3S_TOKEN:0:10}..."
echo ""

# Test connectivity to master
print_status "Testing connectivity to master node..."
if ! curl -k --connect-timeout 5 "$K3S_URL" >/dev/null 2>&1; then
    print_warning "Could not connect to $K3S_URL"
    print_warning "This is normal if the master node firewall blocks external access"
    print_warning "Proceeding with installation..."
fi

# Download and run the main installer
print_status "Downloading and running PipeOps installer..."
if command -v curl >/dev/null 2>&1; then
    curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
elif command -v wget >/dev/null 2>&1; then
    wget -qO- https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
else
    print_error "Neither curl nor wget is available"
    print_error "Please install curl or wget and try again"
    exit 1
fi

print_success "Worker node setup complete!"
echo ""
print_status "To verify the node joined successfully, run on the master node:"
echo "  k3s kubectl get nodes"
echo ""
