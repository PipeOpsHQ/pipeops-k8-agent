#!/usr/bin/env bash
#
# PipeOps Agent Upgrade Script
# Safely upgrades the PipeOps agent to a new version
#
# Usage: ./upgrade.sh [--version VERSION] [--force]
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
NAMESPACE="${NAMESPACE:-pipeops-system}"
AGENT_IMAGE="${AGENT_IMAGE:-ghcr.io/pipeopshq/pipeops-k8-agent}"
VERSION="${VERSION:-latest}"
FORCE=false
BACKUP_DIR="/tmp/pipeops-agent-backup-$(date +%Y%m%d-%H%M%S)"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --version|-v)
            VERSION="$2"
            shift 2
            ;;
        --force|-f)
            FORCE=true
            shift
            ;;
        --namespace|-n)
            NAMESPACE="$2"
            shift 2
            ;;
        --help|-h)
            cat << EOF
PipeOps Agent Upgrade Script

Usage: $0 [OPTIONS]

Options:
    --version, -v VERSION  Target version to upgrade to (default: latest)
    --force, -f           Skip confirmation prompts
    --namespace, -n NAME   Namespace where agent is installed (default: pipeops-system)
    --help, -h            Show this help message

Examples:
    $0                    # Upgrade to latest version
    $0 --version v1.2.3   # Upgrade to specific version
    $0 --force            # Upgrade without confirmation

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

# Detect kubectl command
if command -v kubectl >/dev/null 2>&1; then
    KUBECTL="kubectl"
elif command -v k3s >/dev/null 2>&1 && k3s kubectl version >/dev/null 2>&1; then
    KUBECTL="k3s kubectl"
else
    print_error "kubectl not found"
    exit 1
fi

# Check if agent is installed
check_installation() {
    print_status "Checking current installation..."
    
    if ! $KUBECTL get namespace "$NAMESPACE" >/dev/null 2>&1; then
        print_error "Namespace '$NAMESPACE' not found"
        echo "Agent does not appear to be installed."
        exit 1
    fi
    
    if ! $KUBECTL get deployment pipeops-agent -n "$NAMESPACE" >/dev/null 2>&1; then
        print_error "Agent deployment not found in namespace '$NAMESPACE'"
        exit 1
    fi
    
    print_success "Agent installation found"
}

# Get current version
get_current_version() {
    local current_image
    current_image=$($KUBECTL get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)
    
    if [ -n "$current_image" ]; then
        echo "$current_image"
    else
        echo "unknown"
    fi
}

# Create backup
create_backup() {
    print_status "Creating backup of current deployment..."
    
    mkdir -p "$BACKUP_DIR"
    
    # Backup deployment
    $KUBECTL get deployment pipeops-agent -n "$NAMESPACE" -o yaml > "$BACKUP_DIR/deployment.yaml" 2>/dev/null || true
    
    # Backup configmap (if exists)
    $KUBECTL get configmap pipeops-agent-config -n "$NAMESPACE" -o yaml > "$BACKUP_DIR/configmap.yaml" 2>/dev/null || true
    
    # Backup secret (without revealing values)
    $KUBECTL get secret pipeops-agent-config -n "$NAMESPACE" -o yaml > "$BACKUP_DIR/secret.yaml" 2>/dev/null || true
    
    # Backup service
    $KUBECTL get service pipeops-agent -n "$NAMESPACE" -o yaml > "$BACKUP_DIR/service.yaml" 2>/dev/null || true
    
    # Backup RBAC resources
    $KUBECTL get serviceaccount pipeops-agent -n "$NAMESPACE" -o yaml > "$BACKUP_DIR/serviceaccount.yaml" 2>/dev/null || true
    $KUBECTL get clusterrole pipeops-agent -o yaml > "$BACKUP_DIR/clusterrole.yaml" 2>/dev/null || true
    $KUBECTL get clusterrolebinding pipeops-agent -o yaml > "$BACKUP_DIR/clusterrolebinding.yaml" 2>/dev/null || true
    
    print_success "Backup created at: $BACKUP_DIR"
}

# Perform upgrade
perform_upgrade() {
    local new_image="$AGENT_IMAGE:$VERSION"
    
    print_status "Upgrading agent to $new_image..."
    
    # Update the image
    if $KUBECTL set image deployment/pipeops-agent agent="$new_image" -n "$NAMESPACE"; then
        print_success "Image updated successfully"
    else
        print_error "Failed to update image"
        return 1
    fi
    
    # Wait for rollout
    print_status "Waiting for rollout to complete..."
    
    if $KUBECTL rollout status deployment/pipeops-agent -n "$NAMESPACE" --timeout=5m; then
        print_success "Rollout completed successfully"
        return 0
    else
        print_error "Rollout failed or timed out"
        return 1
    fi
}

# Verify upgrade
verify_upgrade() {
    print_status "Verifying upgrade..."
    
    local retries=0
    local max_retries=30
    
    while [ $retries -lt $max_retries ]; do
        # Check if pod is running
        local pod_status
        pod_status=$($KUBECTL get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "")
        
        if [ "$pod_status" = "Running" ]; then
            # Check if pod is ready
            local pod_ready
            pod_ready=$($KUBECTL get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].status.containerStatuses[0].ready}' 2>/dev/null || echo "false")
            
            if [ "$pod_ready" = "true" ]; then
                print_success "Pod is running and ready"
                
                # Test health endpoint
                local pod_name
                pod_name=$($KUBECTL get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
                
                if [ -n "$pod_name" ]; then
                    if $KUBECTL exec "$pod_name" -n "$NAMESPACE" -- wget -q -O- http://localhost:8080/health >/dev/null 2>&1; then
                        print_success "Health endpoint responding"
                        return 0
                    else
                        print_warning "Health endpoint not responding yet"
                    fi
                fi
            fi
        fi
        
        retries=$((retries + 1))
        sleep 2
    done
    
    print_error "Verification failed after $max_retries attempts"
    return 1
}

# Rollback
rollback() {
    print_warning "Rolling back to previous version..."
    
    if $KUBECTL rollout undo deployment/pipeops-agent -n "$NAMESPACE"; then
        print_success "Rollback initiated"
        
        print_status "Waiting for rollback to complete..."
        if $KUBECTL rollout status deployment/pipeops-agent -n "$NAMESPACE" --timeout=5m; then
            print_success "Rollback completed successfully"
            
            # Restore from backup if needed
            print_status "Backup available at: $BACKUP_DIR"
            echo "You can manually restore using:"
            echo "  kubectl apply -f $BACKUP_DIR/"
            
            return 0
        else
            print_error "Rollback timed out"
            return 1
        fi
    else
        print_error "Rollback failed"
        print_warning "You may need to manually restore from backup:"
        echo "  kubectl apply -f $BACKUP_DIR/"
        return 1
    fi
}

# Main upgrade process
main() {
    echo ""
    echo "╔═══════════════════════════════════════════════════════╗"
    echo "║         PipeOps Agent Upgrade Utility                ║"
    echo "╚═══════════════════════════════════════════════════════╝"
    echo ""
    
    # Check installation
    check_installation
    
    # Get current version
    CURRENT_VERSION=$(get_current_version)
    NEW_IMAGE="$AGENT_IMAGE:$VERSION"
    
    echo ""
    echo "  Namespace:       $NAMESPACE"
    echo "  Current version: $CURRENT_VERSION"
    echo "  Target version:  $NEW_IMAGE"
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
    
    # Perform upgrade
    if ! perform_upgrade; then
        print_error "Upgrade failed"
        
        if [ "$FORCE" != true ]; then
            read -p "Do you want to rollback? (yes/no): " -r
            echo
            if [[ $REPLY =~ ^[Yy]es$ ]]; then
                rollback
            fi
        fi
        
        exit 1
    fi
    
    # Verify upgrade
    if verify_upgrade; then
        echo ""
        echo -e "${GREEN}╔═══════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║         Upgrade Completed Successfully!              ║${NC}"
        echo -e "${GREEN}╚═══════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo "  New version: $NEW_IMAGE"
        echo "  Backup: $BACKUP_DIR"
        echo ""
        print_success "Agent is running and healthy"
        
        # Show pod info
        echo ""
        print_status "Current pod status:"
        $KUBECTL get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent
        
        echo ""
        print_status "View logs with:"
        echo "  $KUBECTL logs -n $NAMESPACE deployment/pipeops-agent --tail=50 --follow"
        
        exit 0
    else
        print_error "Verification failed"
        
        if [ "$FORCE" != true ]; then
            echo ""
            read -p "Do you want to rollback? (yes/no): " -r
            echo
            if [[ $REPLY =~ ^[Yy]es$ ]]; then
                if rollback; then
                    exit 0
                else
                    exit 1
                fi
            fi
        fi
        
        print_warning "Upgrade completed but verification failed"
        print_status "Check the pod status and logs:"
        echo "  $KUBECTL get pods -n $NAMESPACE -l app.kubernetes.io/name=pipeops-agent"
        echo "  $KUBECTL logs -n $NAMESPACE deployment/pipeops-agent"
        
        exit 1
    fi
}

# Run main function
main
