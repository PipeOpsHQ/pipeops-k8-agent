#!/usr/bin/env bash
#
# k3s Cluster Upgrade Script
# Safely upgrades k3s Kubernetes distribution
#
# Usage: ./upgrade-k3s.sh [--version VERSION] [--force]
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
K3S_VERSION="${K3S_VERSION:-}"
FORCE=false
BACKUP_DIR="/tmp/k3s-backup-$(date +%Y%m%d-%H%M%S)"
DRAIN_TIMEOUT="300s"
UPGRADE_METHOD="${UPGRADE_METHOD:-manual}"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --version|-v)
            K3S_VERSION="$2"
            shift 2
            ;;
        --force|-f)
            FORCE=true
            shift
            ;;
        --method|-m)
            UPGRADE_METHOD="$2"
            shift 2
            ;;
        --help|-h)
            cat << EOF
k3s Cluster Upgrade Script

Usage: $0 [OPTIONS]

Options:
    --version, -v VERSION  Target k3s version (e.g., v1.30.0+k3s1)
                          If not specified, upgrades to latest stable
    --force, -f           Skip confirmation prompts
    --method, -m METHOD   Upgrade method: manual, controller (default: manual)
    --help, -h            Show this help message

Upgrade Methods:
    manual      - Direct upgrade using k3s install script (recommended for single node)
    controller  - Deploy system-upgrade-controller for automated rolling upgrades

Examples:
    $0                           # Upgrade to latest stable (interactive)
    $0 --version v1.30.0+k3s1   # Upgrade to specific version
    $0 --force                   # Upgrade without confirmation
    $0 --method controller       # Use automated upgrade controller

IMPORTANT:
    - This will upgrade the Kubernetes control plane and kubelet
    - Existing workloads will continue running during upgrade
    - For multi-node clusters, consider using --method controller
    - Always backup critical data before upgrading
    - Test upgrades in a non-production environment first

EOF
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Print functions
print_status() {
    echo -e "${BLUE}==>${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

# Check if running as root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        print_error "This script must be run as root"
        echo "Please run with sudo: sudo $0"
        exit 1
    fi
}

# Detect kubectl command
detect_kubectl() {
    if command -v kubectl >/dev/null 2>&1; then
        KUBECTL="kubectl"
    elif command -v k3s >/dev/null 2>&1 && k3s kubectl version >/dev/null 2>&1; then
        KUBECTL="k3s kubectl"
    else
        print_error "kubectl not found"
        exit 1
    fi
}

# Check if k3s is installed
check_k3s_installed() {
    if ! command -v k3s >/dev/null 2>&1; then
        print_error "k3s is not installed"
        exit 1
    fi
    
    print_success "k3s installation found"
}

# Get current k3s version
get_current_version() {
    local version
    version=$(k3s --version 2>/dev/null | head -n1 | awk '{print $3}' || echo "unknown")
    echo "$version"
}

# Get latest k3s version
get_latest_version() {
    local latest
    latest=$(curl -sL https://update.k3s.io/v1-release/channels/stable | grep -oP '"latest"\s*:\s*"\K[^"]+' || echo "")
    
    if [ -z "$latest" ]; then
        print_warning "Could not fetch latest version, using 'stable' channel"
        echo "stable"
    else
        echo "$latest"
    fi
}

# Check cluster health
check_cluster_health() {
    print_status "Checking cluster health..."
    
    # Check if API server is responding
    if ! $KUBECTL cluster-info >/dev/null 2>&1; then
        print_error "Kubernetes API server is not responding"
        return 1
    fi
    
    # Check node status
    local node_status
    node_status=$($KUBECTL get nodes -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
    
    if [ "$node_status" != "True" ]; then
        print_warning "Node is not in Ready state: $node_status"
    else
        print_success "Node is Ready"
    fi
    
    # Count running pods
    local pod_count
    pod_count=$($KUBECTL get pods --all-namespaces --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l || echo 0)
    print_status "Running pods: $pod_count"
    
    return 0
}

# Get node information
get_node_info() {
    print_status "Gathering node information..."
    
    local node_name
    node_name=$($KUBECTL get nodes -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "unknown")
    
    local node_role
    node_role=$($KUBECTL get nodes -o jsonpath='{.items[0].metadata.labels.node-role\.kubernetes\.io/master}' 2>/dev/null || echo "")
    
    if [ -z "$node_role" ]; then
        node_role=$($KUBECTL get nodes -o jsonpath='{.items[0].metadata.labels.node-role\.kubernetes\.io/control-plane}' 2>/dev/null || echo "worker")
    else
        node_role="master"
    fi
    
    echo "  Node: $node_name"
    echo "  Role: $node_role"
}

# Create backup
create_backup() {
    print_status "Creating backup of cluster state..."
    
    mkdir -p "$BACKUP_DIR"
    
    # Backup k3s configuration
    if [ -f /etc/rancher/k3s/k3s.yaml ]; then
        cp /etc/rancher/k3s/k3s.yaml "$BACKUP_DIR/k3s.yaml"
    fi
    
    # Backup server configuration
    if [ -f /etc/systemd/system/k3s.service ]; then
        cp /etc/systemd/system/k3s.service "$BACKUP_DIR/k3s.service"
    fi
    
    # Export all resources
    $KUBECTL get all --all-namespaces -o yaml > "$BACKUP_DIR/all-resources.yaml" 2>/dev/null || true
    
    # Export critical resources
    $KUBECTL get namespaces -o yaml > "$BACKUP_DIR/namespaces.yaml" 2>/dev/null || true
    $KUBECTL get pv -o yaml > "$BACKUP_DIR/persistent-volumes.yaml" 2>/dev/null || true
    $KUBECTL get pvc --all-namespaces -o yaml > "$BACKUP_DIR/persistent-volume-claims.yaml" 2>/dev/null || true
    
    # Backup etcd snapshot (if k3s is using embedded etcd)
    if k3s etcd-snapshot save --name "pre-upgrade-backup" >/dev/null 2>&1; then
        print_success "Created etcd snapshot: pre-upgrade-backup"
        
        # Copy snapshot to backup directory
        local snapshot_dir="/var/lib/rancher/k3s/server/db/snapshots"
        if [ -d "$snapshot_dir" ]; then
            cp -r "$snapshot_dir" "$BACKUP_DIR/etcd-snapshots" 2>/dev/null || true
        fi
    else
        print_warning "Could not create etcd snapshot (might be using external datastore)"
    fi
    
    print_success "Backup created at: $BACKUP_DIR"
}

# Manual upgrade method
manual_upgrade() {
    local target_version="$1"
    
    print_status "Performing manual upgrade to $target_version..."
    
    # Download and run k3s install script
    local install_cmd="curl -sfL https://get.k3s.io | sh -s -"
    
    if [ -n "$target_version" ] && [ "$target_version" != "stable" ]; then
        install_cmd="curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=$target_version sh -s -"
    fi
    
    print_status "Running: $install_cmd"
    
    if eval "$install_cmd"; then
        print_success "k3s upgraded successfully"
        return 0
    else
        print_error "k3s upgrade failed"
        return 1
    fi
}

# Controller-based upgrade method
controller_upgrade() {
    local target_version="$1"
    
    print_status "Deploying system-upgrade-controller..."
    
    # Check if controller is already installed
    if $KUBECTL get deployment system-upgrade-controller -n system-upgrade >/dev/null 2>&1; then
        print_success "system-upgrade-controller already installed"
    else
        # Install controller
        if $KUBECTL apply -f https://github.com/rancher/system-upgrade-controller/releases/latest/download/system-upgrade-controller.yaml; then
            print_success "system-upgrade-controller deployed"
            
            # Wait for controller to be ready
            print_status "Waiting for controller to be ready..."
            $KUBECTL wait --for=condition=available --timeout=300s deployment/system-upgrade-controller -n system-upgrade || true
        else
            print_error "Failed to deploy system-upgrade-controller"
            return 1
        fi
    fi
    
    # Create upgrade plan
    print_status "Creating upgrade plan..."
    
    local plan_version="$target_version"
    if [ -z "$plan_version" ] || [ "$plan_version" = "stable" ]; then
        plan_version="stable"
    fi
    
    cat <<EOF | $KUBECTL apply -f -
apiVersion: upgrade.cattle.io/v1
kind: Plan
metadata:
  name: k3s-server
  namespace: system-upgrade
spec:
  concurrency: 1
  cordon: true
  nodeSelector:
    matchExpressions:
    - key: node-role.kubernetes.io/control-plane
      operator: In
      values:
      - "true"
  serviceAccountName: system-upgrade
  upgrade:
    image: rancher/k3s-upgrade
  version: $plan_version
---
apiVersion: upgrade.cattle.io/v1
kind: Plan
metadata:
  name: k3s-agent
  namespace: system-upgrade
spec:
  concurrency: 1
  cordon: true
  nodeSelector:
    matchExpressions:
    - key: node-role.kubernetes.io/control-plane
      operator: DoesNotExist
  prepare:
    args:
    - prepare
    - k3s-server
    image: rancher/k3s-upgrade
  serviceAccountName: system-upgrade
  upgrade:
    image: rancher/k3s-upgrade
  version: $plan_version
EOF
    
    if [ $? -eq 0 ]; then
        print_success "Upgrade plan created"
        
        echo ""
        print_status "Upgrade will proceed automatically"
        print_status "Monitor progress with:"
        echo "  $KUBECTL get plans -n system-upgrade"
        echo "  $KUBECTL get jobs -n system-upgrade"
        echo "  $KUBECTL logs -n system-upgrade -l upgrade.cattle.io/plan=k3s-server --follow"
        
        return 0
    else
        print_error "Failed to create upgrade plan"
        return 1
    fi
}

# Verify upgrade
verify_upgrade() {
    local expected_version="$1"
    
    print_status "Verifying upgrade..."
    
    # Wait a moment for k3s to restart
    sleep 10
    
    local retries=0
    local max_retries=30
    
    while [ $retries -lt $max_retries ]; do
        # Check if k3s is responding
        if systemctl is-active --quiet k3s; then
            # Check version
            local current_version
            current_version=$(get_current_version)
            
            print_status "Current version: $current_version"
            
            # Check if cluster is accessible
            if $KUBECTL cluster-info >/dev/null 2>&1; then
                # Check node status
                local node_ready
                node_ready=$($KUBECTL get nodes -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "False")
                
                if [ "$node_ready" = "True" ]; then
                    print_success "Node is Ready"
                    
                    # Check if workloads are running
                    sleep 5
                    local running_pods
                    running_pods=$($KUBECTL get pods --all-namespaces --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l || echo 0)
                    
                    print_success "Running pods: $running_pods"
                    
                    return 0
                fi
            fi
        fi
        
        retries=$((retries + 1))
        print_status "Waiting for cluster to be ready... ($retries/$max_retries)"
        sleep 10
    done
    
    print_error "Verification failed after $max_retries attempts"
    return 1
}

# Rollback
rollback() {
    print_warning "Attempting to rollback..."
    print_warning "Automatic rollback is not supported for k3s upgrades"
    
    echo ""
    echo "To manually rollback:"
    echo "1. Restore etcd snapshot:"
    echo "   k3s server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/pre-upgrade-backup"
    echo ""
    echo "2. Or reinstall previous version:"
    echo "   curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=<old-version> sh -"
    echo ""
    echo "3. Restore configuration from backup:"
    echo "   cp $BACKUP_DIR/k3s.yaml /etc/rancher/k3s/k3s.yaml"
    echo ""
    
    return 1
}

# Main upgrade process
main() {
    echo ""
    echo "╔═══════════════════════════════════════════════════════╗"
    echo "║         k3s Cluster Upgrade Utility                  ║"
    echo "╚═══════════════════════════════════════════════════════╝"
    echo ""
    
    # Check root
    check_root
    
    # Detect kubectl
    detect_kubectl
    
    # Check k3s installation
    check_k3s_installed
    
    # Get versions
    CURRENT_VERSION=$(get_current_version)
    
    if [ -z "$K3S_VERSION" ]; then
        K3S_VERSION=$(get_latest_version)
        print_status "No version specified, will upgrade to: $K3S_VERSION"
    fi
    
    TARGET_VERSION="$K3S_VERSION"
    
    # Display information
    echo ""
    echo "  Current version: $CURRENT_VERSION"
    echo "  Target version:  $TARGET_VERSION"
    echo "  Upgrade method:  $UPGRADE_METHOD"
    echo ""
    
    # Get node info
    get_node_info
    echo ""
    
    # Check cluster health
    if ! check_cluster_health; then
        print_warning "Cluster health check failed"
        
        if [ "$FORCE" != true ]; then
            read -p "Continue anyway? (yes/no): " -r
            echo
            if [[ ! $REPLY =~ ^[Yy]es$ ]]; then
                echo "Upgrade cancelled."
                exit 1
            fi
        fi
    fi
    
    echo ""
    print_warning "IMPORTANT: This will upgrade your Kubernetes cluster"
    print_warning "Workloads may experience brief disruption during upgrade"
    echo ""
    
    # Confirmation
    if [ "$FORCE" != true ]; then
        read -p "Do you want to proceed with the upgrade? (yes/no): " -r
        echo
        if [[ ! $REPLY =~ ^[Yy]es$ ]]; then
            echo "Upgrade cancelled."
            exit 0
        fi
    fi
    
    # Create backup
    create_backup
    
    # Perform upgrade based on method
    case "$UPGRADE_METHOD" in
        manual)
            if ! manual_upgrade "$TARGET_VERSION"; then
                print_error "Upgrade failed"
                rollback
                exit 1
            fi
            
            # Verify upgrade
            if verify_upgrade "$TARGET_VERSION"; then
                echo ""
                echo -e "${GREEN}╔═══════════════════════════════════════════════════════╗${NC}"
                echo -e "${GREEN}║         Upgrade Completed Successfully!              ║${NC}"
                echo -e "${GREEN}╚═══════════════════════════════════════════════════════╝${NC}"
                echo ""
                
                NEW_VERSION=$(get_current_version)
                echo "  Old version: $CURRENT_VERSION"
                echo "  New version: $NEW_VERSION"
                echo "  Backup: $BACKUP_DIR"
                echo ""
                
                print_success "Cluster is running and healthy"
                
                # Show cluster info
                echo ""
                print_status "Cluster status:"
                $KUBECTL get nodes
                
                exit 0
            else
                print_error "Verification failed"
                rollback
                exit 1
            fi
            ;;
            
        controller)
            if controller_upgrade "$TARGET_VERSION"; then
                echo ""
                echo -e "${GREEN}╔═══════════════════════════════════════════════════════╗${NC}"
                echo -e "${GREEN}║         Upgrade Controller Deployed!                 ║${NC}"
                echo -e "${GREEN}╚═══════════════════════════════════════════════════════╝${NC}"
                echo ""
                echo "  Backup: $BACKUP_DIR"
                echo ""
                
                print_success "Automated upgrade will proceed in background"
                
                exit 0
            else
                print_error "Failed to deploy upgrade controller"
                exit 1
            fi
            ;;
            
        *)
            print_error "Unknown upgrade method: $UPGRADE_METHOD"
            exit 1
            ;;
    esac
}

# Run main function
main
