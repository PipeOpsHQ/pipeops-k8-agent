#!/bin/bash

# Gateway API and Istio Installation Script
# Installs Gateway API experimental CRDs and configures Istio with alpha gateway API support
# Uses Helm for Istio installation (no istioctl required)

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

# Check if command exists
command_exists() {
    command -v "$@" > /dev/null 2>&1
}

# Check prerequisites
check_prerequisites() {
    print_status "Checking prerequisites..."
    
    if ! command_exists kubectl; then
        print_error "kubectl is not installed. Please install kubectl first."
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        print_error "Cannot connect to Kubernetes cluster. Please check your kubectl configuration."
        exit 1
    fi
    
    print_success "Prerequisites check passed"
}

# Install Gateway API experimental CRDs
install_gateway_api() {
    print_status "Installing Gateway API experimental CRDs..."
    
    GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.3.0}"
    
    if kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/experimental-install.yaml"; then
        print_success "Gateway API CRDs installed successfully"
    else
        print_error "Failed to install Gateway API CRDs"
        exit 1
    fi
    
    # Verify installation
    print_status "Verifying Gateway API CRDs..."
    sleep 2
    
    local required_crds=("gateways.gateway.networking.k8s.io" "tcproutes.gateway.networking.k8s.io" "udproutes.gateway.networking.k8s.io")
    for crd in "${required_crds[@]}"; do
        if kubectl get crd "$crd" &> /dev/null; then
            print_success "CRD $crd found"
        else
            print_warning "CRD $crd not found"
        fi
    done
}

# Install Istio with Gateway API support using Helm
install_istio() {
    print_status "Installing Istio with Gateway API support (using Helm)..."
    
    if ! command_exists helm; then
        print_error "Helm is not installed."
        print_status "Please install Helm from: https://helm.sh/docs/intro/install/"
        exit 1
    fi
    
    # Add Istio Helm repository
    print_status "Adding Istio Helm repository..."
    helm repo add istio https://istio-release.storage.googleapis.com/charts 2>/dev/null || true
    helm repo update
    
    # Create istio-system namespace
    kubectl create namespace istio-system --dry-run=client -o yaml | kubectl apply -f -
    
    # Install Istio base (CRDs)
    print_status "Installing Istio base components (CRDs)..."
    if helm list -n istio-system | grep -q "istio-base"; then
        print_warning "Istio base already installed, upgrading..."
        helm upgrade istio-base istio/base -n istio-system --wait
    else
        helm install istio-base istio/base -n istio-system --wait
    fi
    
    if [ $? -ne 0 ]; then
        print_error "Failed to install Istio base"
        exit 1
    fi
    print_success "Istio base installed successfully"
    
    # Install Istiod with Gateway API alpha support
    print_status "Installing Istiod with alpha Gateway API support..."
    if helm list -n istio-system | grep -q "istiod"; then
        print_warning "Istiod already installed, upgrading..."
        helm upgrade istiod istio/istiod -n istio-system \
            --set pilot.env.PILOT_ENABLE_ALPHA_GATEWAY_API=true \
            --wait
    else
        helm install istiod istio/istiod -n istio-system \
            --set pilot.env.PILOT_ENABLE_ALPHA_GATEWAY_API=true \
            --wait
    fi
    
    if [ $? -eq 0 ]; then
        print_success "Istiod installed successfully with Gateway API support"
    else
        print_error "Failed to install Istiod"
        exit 1
    fi
    
    # Verify installation
    print_status "Verifying Istio installation..."
    sleep 5
    
    if kubectl get pods -n istio-system | grep -q "istiod"; then
        print_success "Istio control plane is running"
    else
        print_warning "Istio control plane not found"
    fi
}

# Verify complete installation
verify_installation() {
    print_status "Verifying complete installation..."
    
    echo ""
    print_status "Gateway API CRDs:"
    kubectl get crd | grep gateway.networking.k8s.io || print_warning "No Gateway API CRDs found"
    
    echo ""
    print_status "Istio Components:"
    kubectl get pods -n istio-system || print_warning "No Istio pods found"
    
    echo ""
    print_status "GatewayClasses:"
    kubectl get gatewayclass || print_warning "No GatewayClasses found"
}

# Print next steps
print_next_steps() {
    echo ""
    print_success "Installation complete!"
    echo ""
    echo "Next steps:"
    echo "  1. Configure your PipeOps agent with Gateway API settings"
    echo "  2. See docs/advanced/gateway-api-setup.md for configuration examples"
    echo "  3. Deploy your gateway and routes with Helm:"
    echo ""
    echo "     helm install pipeops-agent ./helm/pipeops-agent \\"
    echo "       --set agent.gateway.enabled=true \\"
    echo "       --set agent.gateway.gatewayApi.enabled=true \\"
    echo "       --set agent.pipeops.token='your-token' \\"
    echo "       --set agent.cluster.name='your-cluster'"
    echo ""
}

# Main installation flow
main() {
    echo ""
    print_status "Gateway API and Istio Installation"
    echo ""
    
    # Parse arguments
    SKIP_GATEWAY_API=false
    SKIP_ISTIO=false
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            --skip-gateway-api)
                SKIP_GATEWAY_API=true
                shift
                ;;
            --skip-istio)
                SKIP_ISTIO=true
                shift
                ;;
            --gateway-api-version)
                GATEWAY_API_VERSION="$2"
                shift 2
                ;;
            -h|--help)
                echo "Usage: $0 [OPTIONS]"
                echo ""
                echo "Options:"
                echo "  --skip-gateway-api         Skip Gateway API CRD installation"
                echo "  --skip-istio              Skip Istio installation"
                echo "  --gateway-api-version     Specify Gateway API version (default: v1.3.0)"
                echo "  -h, --help                Show this help message"
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                exit 1
                ;;
        esac
    done
    
    check_prerequisites
    
    if [ "$SKIP_GATEWAY_API" = false ]; then
        install_gateway_api
    else
        print_warning "Skipping Gateway API installation"
    fi
    
    if [ "$SKIP_ISTIO" = false ]; then
        install_istio
    else
        print_warning "Skipping Istio installation"
    fi
    
    verify_installation
    print_next_steps
}

# Run main function
main "$@"
