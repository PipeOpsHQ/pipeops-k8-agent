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
    echo "Usage: $0 <MASTER_IP> <CLUSTER_TOKEN>"
    echo ""
    echo "Arguments:"
    echo "  MASTER_IP      IP address of the k3s master node"
    echo "  CLUSTER_TOKEN  Cluster token from the master node"
    echo ""
    echo "Example:"
    echo "  $0 192.168.1.100 K10abc123def456::server:abc123"
    echo ""
    echo "To get the cluster token from the master node, run:"
    echo "  sudo cat /var/lib/rancher/k3s/server/node-token"
    echo ""
    echo "Or use the main installer:"
    echo "  ./install.sh cluster-info"
    echo ""
}

# Validate arguments
if [ $# -ne 2 ]; then
    print_error "Invalid number of arguments"
    echo ""
    show_usage
    exit 1
fi

MASTER_IP="$1"
CLUSTER_TOKEN="$2"

# Validate IP address format (basic check)
if ! echo "$MASTER_IP" | grep -E '^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$' >/dev/null; then
    print_error "Invalid IP address format: $MASTER_IP"
    exit 1
fi

# Validate cluster token (basic check)
if [ ${#CLUSTER_TOKEN} -lt 20 ]; then
    print_error "Cluster token seems too short. Please check the token."
    exit 1
fi

# Set environment variables for worker installation
export NODE_TYPE="agent"
export K3S_URL="https://$MASTER_IP:6443"
export K3S_TOKEN="$CLUSTER_TOKEN"

echo ""
echo -e "${BLUE}╔══════════════════════════════════════╗${NC}"
echo -e "${BLUE}║     PipeOps Worker Node Setup        ║${NC}"
echo -e "${BLUE}╚══════════════════════════════════════╝${NC}"
echo ""

print_status "Master IP: $MASTER_IP"
print_status "Cluster URL: $K3S_URL"
print_status "Token: ${CLUSTER_TOKEN:0:10}..."
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
    curl -sSL https://get.pipeops.io/agent | bash
elif command -v wget >/dev/null 2>&1; then
    wget -qO- https://get.pipeops.io/agent | bash
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
