#!/bin/bash

#######################################
# PipeOps Agent Uninstall Script
# Removes PipeOps agent and optionally k3s
#######################################

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values (can be overridden by environment variables)
NAMESPACE="${NAMESPACE:-pipeops-system}"
UNINSTALL_K3S="${UNINSTALL_K3S:-false}"
FORCE="${FORCE:-false}"
KEEP_DATA="${KEEP_DATA:-false}"

#######################################
# Print colored output
#######################################
print_info() {
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

print_header() {
    echo ""
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}========================================${NC}"
    echo ""
}

#######################################
# Show usage
#######################################
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Uninstall PipeOps agent from the cluster.

OPTIONS:
    -k, --uninstall-k3s     Also uninstall k3s (default: false)
    -f, --force             Skip confirmation prompts (default: false)
    -d, --keep-data         Keep PersistentVolumeClaims and data (default: false)
    -n, --namespace NAME    Namespace where agent is installed (default: pipeops-system)
    -h, --help              Show this help message

ENVIRONMENT VARIABLES:
    UNINSTALL_K3S          Set to "true" to uninstall k3s
    FORCE                  Set to "true" to skip confirmation
    KEEP_DATA              Set to "true" to keep PVCs
    NAMESPACE              Namespace where agent is installed

EXAMPLES:
    # Remove only PipeOps agent (keep k3s)
    $0

    # Remove agent and uninstall k3s
    $0 --uninstall-k3s

    # Force removal without confirmation
    $0 --force

    # Remove agent but keep data volumes
    $0 --keep-data

    # Using environment variables (useful when piping from curl)
    UNINSTALL_K3S=true FORCE=true $0

EXIT CODES:
    0 - Success
    1 - User cancelled
    2 - Removal failed
    3 - k3s uninstall failed

EOF
    exit 0
}

#######################################
# Parse command line arguments
#######################################
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -k|--uninstall-k3s)
                UNINSTALL_K3S="true"
                shift
                ;;
            -f|--force)
                FORCE="true"
                shift
                ;;
            -d|--keep-data)
                KEEP_DATA="true"
                shift
                ;;
            -n|--namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            -h|--help)
                usage
                ;;
            *)
                print_error "Unknown option: $1"
                usage
                ;;
        esac
    done
}

#######################################
# Check if kubectl is available
#######################################
check_kubectl() {
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl not found. Please install kubectl first."
        exit 2
    fi
}

#######################################
# Check if namespace exists
#######################################
check_namespace() {
    if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
        print_warning "Namespace '$NAMESPACE' does not exist."
        return 1
    fi
    return 0
}

#######################################
# Get agent status
#######################################
get_agent_status() {
    print_info "Checking PipeOps agent status..."
    
    if ! check_namespace; then
        print_info "Agent is not installed."
        return
    fi
    
    echo ""
    echo "Namespace: $NAMESPACE"
    
    # Check deployment
    if kubectl get deployment pipeops-agent -n "$NAMESPACE" &> /dev/null; then
        echo "Deployment: Found"
        kubectl get deployment pipeops-agent -n "$NAMESPACE" 2>/dev/null || true
    else
        echo "Deployment: Not found"
    fi
    
    echo ""
    
    # Check pods
    local pod_count
    pod_count=$(kubectl get pods -n "$NAMESPACE" -l app=pipeops-agent --no-headers 2>/dev/null | wc -l)
    if [ "$pod_count" -gt 0 ]; then
        echo "Pods: $pod_count found"
        kubectl get pods -n "$NAMESPACE" -l app=pipeops-agent 2>/dev/null || true
    else
        echo "Pods: None found"
    fi
    
    echo ""
}

#######################################
# Prompt for confirmation
#######################################
prompt_confirmation() {
    if [ "$FORCE" = "true" ]; then
        return 0
    fi
    
    # Check if we're running in a non-interactive environment (piped from curl)
    if [ ! -t 0 ]; then
        print_error "Cannot prompt for confirmation in non-interactive mode."
        print_info "To uninstall without confirmation, use one of these methods:"
        echo "  1. FORCE=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash"
        echo "  2. curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash -s -- --force"
        echo "  3. Download and run manually:"
        echo "     curl -fsSL https://get.pipeops.dev/k8-uninstall.sh -o uninstall.sh"
        echo "     bash uninstall.sh"
        exit 1
    fi
    
    echo ""
    print_warning "This will remove the following resources:"
    echo "  - PipeOps agent deployment"
    echo "  - Namespace: $NAMESPACE"
    echo "  - ServiceAccount, ClusterRole, and ClusterRoleBinding"
    echo "  - ConfigMaps and Secrets"
    
    if [ "$KEEP_DATA" = "false" ]; then
        echo "  - PersistentVolumeClaims (if any)"
    fi
    
    if [ "$UNINSTALL_K3S" = "true" ]; then
        echo ""
        print_warning "k3s will also be COMPLETELY REMOVED from this system!"
    fi
    
    echo ""
    read -p "Are you sure you want to continue? (yes/no): " -r
    echo ""
    
    if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
        print_info "Uninstall cancelled."
        exit 1
    fi
}

#######################################
# Remove agent deployment
#######################################
remove_agent_deployment() {
    print_info "Removing PipeOps agent deployment..."
    
    if ! check_namespace; then
        print_warning "Namespace does not exist, skipping deployment removal."
        return
    fi
    
    # Delete deployment
    if kubectl get deployment pipeops-agent -n "$NAMESPACE" &> /dev/null; then
        kubectl delete deployment pipeops-agent -n "$NAMESPACE" --ignore-not-found=true
        print_success "Deployment removed"
    fi
    
    # Delete service
    if kubectl get service pipeops-agent -n "$NAMESPACE" &> /dev/null; then
        kubectl delete service pipeops-agent -n "$NAMESPACE" --ignore-not-found=true
        print_success "Service removed"
    fi
    
    # Wait for pods to terminate
    print_info "Waiting for pods to terminate..."
    kubectl wait --for=delete pod -l app=pipeops-agent -n "$NAMESPACE" --timeout=60s 2>/dev/null || true
}

#######################################
# Remove ConfigMaps and Secrets
#######################################
remove_configs() {
    print_info "Removing ConfigMaps and Secrets..."
    
    if ! check_namespace; then
        return
    fi
    
    kubectl delete configmap pipeops-agent-config -n "$NAMESPACE" --ignore-not-found=true
    kubectl delete secret pipeops-agent-secret -n "$NAMESPACE" --ignore-not-found=true
    
    print_success "ConfigMaps and Secrets removed"
}

#######################################
# Remove PersistentVolumeClaims
#######################################
remove_pvcs() {
    if [ "$KEEP_DATA" = "true" ]; then
        print_info "Keeping PersistentVolumeClaims as requested"
        return
    fi
    
    print_info "Removing PersistentVolumeClaims..."
    
    if ! check_namespace; then
        return
    fi
    
    local pvc_count
    pvc_count=$(kubectl get pvc -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l)
    
    if [ "$pvc_count" -gt 0 ]; then
        kubectl delete pvc --all -n "$NAMESPACE" --ignore-not-found=true
        print_success "PersistentVolumeClaims removed"
    else
        print_info "No PersistentVolumeClaims found"
    fi
}

#######################################
# Remove RBAC resources
#######################################
remove_rbac() {
    print_info "Removing RBAC resources..."
    
    # Delete ClusterRoleBinding
    kubectl delete clusterrolebinding pipeops-agent --ignore-not-found=true
    
    # Delete ClusterRole
    kubectl delete clusterrole pipeops-agent --ignore-not-found=true
    
    # ServiceAccount will be deleted with namespace
    
    print_success "RBAC resources removed"
}

#######################################
# Remove namespace
#######################################
remove_namespace() {
    print_info "Removing namespace '$NAMESPACE'..."
    
    if ! check_namespace; then
        print_info "Namespace already removed"
        return
    fi
    
    kubectl delete namespace "$NAMESPACE" --ignore-not-found=true
    
    # Wait for namespace to be fully deleted
    print_info "Waiting for namespace to be fully deleted..."
    local timeout=60
    local elapsed=0
    while kubectl get namespace "$NAMESPACE" &> /dev/null; do
        if [ $elapsed -ge $timeout ]; then
            print_warning "Namespace deletion timeout, but it will eventually be removed"
            break
        fi
        sleep 2
        elapsed=$((elapsed + 2))
    done
    
    print_success "Namespace removed"
}

#######################################
# Uninstall k3s
#######################################
uninstall_k3s() {
    if [ "$UNINSTALL_K3S" != "true" ]; then
        print_info "Skipping k3s uninstall (use --uninstall-k3s to remove k3s)"
        return
    fi
    
    print_info "Uninstalling k3s..."
    
    # Check if k3s is installed
    if [ ! -f /usr/local/bin/k3s-uninstall.sh ] && [ ! -f /usr/local/bin/k3s-agent-uninstall.sh ]; then
        print_warning "k3s uninstall script not found. k3s may not be installed."
        return
    fi
    
    # Uninstall k3s server
    if [ -f /usr/local/bin/k3s-uninstall.sh ]; then
        print_info "Running k3s server uninstall script..."
        /usr/local/bin/k3s-uninstall.sh || {
            print_error "k3s server uninstall failed"
            exit 3
        }
        print_success "k3s server uninstalled"
    fi
    
    # Uninstall k3s agent
    if [ -f /usr/local/bin/k3s-agent-uninstall.sh ]; then
        print_info "Running k3s agent uninstall script..."
        /usr/local/bin/k3s-agent-uninstall.sh || {
            print_error "k3s agent uninstall failed"
            exit 3
        }
        print_success "k3s agent uninstalled"
    fi
    
    # Clean up any remaining k3s files
    print_info "Cleaning up k3s files..."
    rm -rf /etc/rancher/k3s
    rm -rf /var/lib/rancher/k3s
    rm -f /usr/local/bin/k3s
    rm -f /usr/local/bin/kubectl
    rm -f /usr/local/bin/crictl
    rm -f /usr/local/bin/ctr
    
    print_success "k3s completely removed"
}

#######################################
# Display removal summary
#######################################
display_summary() {
    print_header "Uninstall Summary"
    
    echo "The following resources have been removed:"
    echo "  ✓ PipeOps agent deployment"
    echo "  ✓ Namespace: $NAMESPACE"
    echo "  ✓ RBAC resources (ServiceAccount, ClusterRole, ClusterRoleBinding)"
    echo "  ✓ ConfigMaps and Secrets"
    
    if [ "$KEEP_DATA" = "false" ]; then
        echo "  ✓ PersistentVolumeClaims"
    else
        echo "  - PersistentVolumeClaims (kept as requested)"
    fi
    
    if [ "$UNINSTALL_K3S" = "true" ]; then
        echo "  ✓ k3s completely removed"
    fi
    
    echo ""
    print_success "PipeOps agent has been successfully uninstalled!"
    
    if [ "$UNINSTALL_K3S" = "false" ]; then
        echo ""
        print_info "k3s is still installed. To remove k3s, run:"
        echo "  $0 --uninstall-k3s"
    fi
    
    echo ""
}

#######################################
# Main execution
#######################################
main() {
    parse_args "$@"
    
    print_header "PipeOps Agent Uninstall"
    
    # Debug: Show parsed options
    if [ "$UNINSTALL_K3S" = "true" ]; then
        print_info "Option: Will also uninstall k3s"
    fi
    if [ "$FORCE" = "true" ]; then
        print_info "Option: Force mode (no confirmation)"
    fi
    if [ "$KEEP_DATA" = "true" ]; then
        print_info "Option: Keeping persistent data (PVCs)"
    fi
    
    # Check prerequisites
    check_kubectl
    
    # Show current status
    get_agent_status
    
    # Prompt for confirmation
    prompt_confirmation
    
    # Perform uninstallation
    print_header "Removing PipeOps Agent"
    
    remove_agent_deployment
    remove_configs
    remove_pvcs
    remove_rbac
    remove_namespace
    uninstall_k3s
    
    # Display summary
    display_summary
}

# Run main function
main "$@"
