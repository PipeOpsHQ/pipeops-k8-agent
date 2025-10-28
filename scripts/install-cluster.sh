#!/bin/bash

# Multi-Cluster Installation Module
# Provides installation functions for k3s, minikube, k3d, and kind

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

# Function to check if command exists
command_exists() {
    command -v "$@" > /dev/null 2>&1
}

# Function to detect if running in Proxmox LXC container
is_proxmox_lxc() {
    if [ -n "$container" ] && [ "$container" = "lxc" ]; then
        return 0
    fi
    
    if grep -q "container=lxc" /proc/1/environ 2>/dev/null; then
        return 0
    fi
    
    return 1
}

# ========================================
# k3s Installation Functions
# ========================================

install_k3s_cluster() {
    local k3s_version="${K3S_VERSION:-v1.28.3+k3s2}"
    local node_type="${NODE_TYPE:-server}"
    
    print_status "Installing k3s $k3s_version as $node_type..."
    
    if command_exists k3s; then
        print_warning "k3s is already installed"
        return 0
    fi

    export INSTALL_K3S_VERSION="$k3s_version"
    export INSTALL_K3S_EXEC=""
    
    # Check if running in Proxmox LXC container
    if is_proxmox_lxc; then
        print_warning "Detected Proxmox LXC container environment!"
        print_warning "Configuring k3s for LXC compatibility..."
        
        if [ "$node_type" = "server" ]; then
            export INSTALL_K3S_EXEC="server --disable=traefik --disable=servicelb --flannel-backend=host-gw --kube-proxy-arg=conntrack-max-per-core=0"
        else
            export INSTALL_K3S_EXEC="agent --flannel-backend=host-gw --kube-proxy-arg=conntrack-max-per-core=0"
        fi
    else
        if [ "$node_type" = "server" ]; then
            export INSTALL_K3S_EXEC="server --disable=traefik"
        else
            export INSTALL_K3S_EXEC="agent"
        fi
    fi

    # Configure for worker node joining
    if [ "$node_type" = "agent" ]; then
        if [ -z "$K3S_URL" ] || [ -z "$K3S_TOKEN" ]; then
            print_error "For worker nodes, K3S_URL and K3S_TOKEN must be set"
            return 1
        fi
        
        export K3S_URL="$K3S_URL"
        export K3S_TOKEN="$K3S_TOKEN"
        print_status "Joining cluster at $K3S_URL"
    fi

    # Download and install k3s
    curl -sfL https://get.k3s.io | sh -

    # Wait for k3s to be ready
    print_status "Waiting for k3s to be ready..."
    local timeout=60
    local count=0
    
    if [ "$node_type" = "server" ]; then
        while [ $count -lt $timeout ]; do
            if k3s kubectl get nodes >/dev/null 2>&1; then
                break
            fi
            sleep 2
            count=$((count + 2))
        done
    else
        while [ $count -lt $timeout ]; do
            if systemctl is-active --quiet k3s-agent; then
                break
            fi
            sleep 2
            count=$((count + 2))
        done
    fi

    if [ $count -ge $timeout ]; then
        print_error "k3s failed to start within ${timeout} seconds"
        return 1
    fi

    # Set up kubectl alias for server nodes
    if [ "$node_type" = "server" ] && ! command_exists kubectl; then
        echo 'alias kubectl="k3s kubectl"' >> ~/.bashrc
        alias kubectl="k3s kubectl"
    fi

    print_success "k3s installed successfully as $node_type"
}

get_k3s_kubeconfig() {
    if [ -f /etc/rancher/k3s/k3s.yaml ]; then
        echo "/etc/rancher/k3s/k3s.yaml"
    else
        print_error "k3s kubeconfig not found"
        return 1
    fi
}

get_k3s_kubectl() {
    if command_exists k3s; then
        echo "k3s kubectl"
    else
        print_error "k3s not installed"
        return 1
    fi
}

# ========================================
# minikube Installation Functions
# ========================================

minikube_is_healthy() {
    if ! command_exists minikube; then
        return 1
    fi

    local status
    status=$(minikube status --format '{{.Host}} {{.Kubelet}} {{.APIServer}} {{.Kubeconfig}}' 2>/dev/null || true)
    if [[ "$status" == "Running Running Running Running" ]]; then
        return 0
    fi

    return 1
}

wait_for_minikube_ready() {
    local timeout="${1:-120}"
    local interval=5
    local elapsed=0

    while [ $elapsed -lt $timeout ]; do
        if minikube kubectl -- get nodes >/dev/null 2>&1; then
            if minikube kubectl -- wait --for=condition=Ready nodes --all --timeout=60s >/dev/null 2>&1; then
                return 0
            fi
        fi

        sleep $interval
        elapsed=$((elapsed + interval))
    done

    return 1
}

install_minikube_cluster() {
    local minikube_version="${MINIKUBE_VERSION:-latest}"
    local k8s_version="${KUBERNETES_VERSION:-stable}"
    local driver="${MINIKUBE_DRIVER:-docker}"
    local cpus="${MINIKUBE_CPUS:-2}"
    local memory="${MINIKUBE_MEMORY:-2048}"
    local start_options=(
        "--driver=${driver}"
        "--cpus=${cpus}"
        "--memory=${memory}mb"
        "--kubernetes-version=${k8s_version}"
    )
    
    print_status "Installing minikube $minikube_version..."
    
    if [ "$(id -u)" = "0" ] && [ "$driver" = "docker" ]; then
        print_error "The minikube Docker driver cannot run with root privileges"
        print_error "Use a non-root user or set MINIKUBE_DRIVER=none if you understand the implications"
        return 1
    fi

    # Install minikube binary if not exists
    if ! command_exists minikube; then
        print_status "Downloading minikube..."
        
        local os=$(uname -s | tr '[:upper:]' '[:lower:]')
        local arch=$(uname -m)
        
        # Map arch names
        case "$arch" in
            x86_64) arch="amd64" ;;
            aarch64) arch="arm64" ;;
        esac
        
        local minikube_url="https://storage.googleapis.com/minikube/releases/latest/minikube-${os}-${arch}"
        
        curl -Lo /tmp/minikube "$minikube_url"
        chmod +x /tmp/minikube
        sudo mv /tmp/minikube /usr/local/bin/minikube
        
        print_success "minikube binary installed"
    else
        print_warning "minikube is already installed"
    fi
    
    # Start or recover minikube cluster
    if minikube_is_healthy; then
        print_warning "minikube cluster is already running"
    else
        if command_exists minikube && minikube status >/dev/null 2>&1; then
            print_warning "Detected minikube but Kubernetes API is unavailable. Restarting cluster..."
            minikube stop >/dev/null 2>&1 || true
        fi

        print_status "Starting minikube cluster..."
        minikube start "${start_options[@]}"
        print_success "minikube cluster started"
    fi

    # Verify cluster is ready, auto-restarting once if needed
    print_status "Waiting for cluster to be ready..."
    if ! wait_for_minikube_ready 180; then
        print_warning "Kubernetes API still unreachable. Attempting minikube restart..."
        minikube stop >/dev/null 2>&1 || true
        print_status "Restarting minikube cluster..."
        minikube start "${start_options[@]}"

        if ! wait_for_minikube_ready 180; then
            print_error "minikube cluster failed to become ready after automatic restart"
            return 1
        fi
    fi

    print_success "minikube installation complete"
}

get_minikube_kubeconfig() {
    if command_exists minikube; then
        echo "$HOME/.kube/config"
    else
        print_error "minikube not installed"
        return 1
    fi
}

get_minikube_kubectl() {
    if command_exists minikube; then
        echo "minikube kubectl --"
    elif command_exists kubectl; then
        echo "kubectl"
    else
        print_error "No kubectl available"
        return 1
    fi
}

# ========================================
# k3d Installation Functions
# ========================================

install_k3d_cluster() {
    local cluster_name="${K3D_CLUSTER_NAME:-pipeops}"
    local k3s_version="${K3D_K3S_VERSION:-v1.28.3-k3s2}"
    local servers="${K3D_SERVERS:-1}"
    local agents="${K3D_AGENTS:-0}"
    local port="${K3D_API_PORT:-6443}"
    
    print_status "Installing k3d cluster '$cluster_name'..."
    
    # Install k3d binary if not exists
    if ! command_exists k3d; then
        print_status "Downloading k3d..."
        curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash
        print_success "k3d binary installed"
    else
        print_warning "k3d is already installed"
    fi
    
    # Check if cluster already exists
    if k3d cluster list | grep -q "^$cluster_name"; then
        print_warning "k3d cluster '$cluster_name' already exists"
    else
        print_status "Creating k3d cluster '$cluster_name'..."
        
        k3d cluster create "$cluster_name" \
            --image "rancher/k3s:${k3s_version}" \
            --servers "$servers" \
            --agents "$agents" \
            --port "${port}:6443@loadbalancer" \
            --wait
        
        print_success "k3d cluster created"
    fi
    
    # Set kubectl context
    k3d kubeconfig merge "$cluster_name" --kubeconfig-switch-context
    
    # Verify cluster is ready
    print_status "Waiting for cluster to be ready..."
    kubectl wait --for=condition=Ready nodes --all --timeout=120s
    
    print_success "k3d installation complete"
}

get_k3d_kubeconfig() {
    local cluster_name="${K3D_CLUSTER_NAME:-pipeops}"
    
    if command_exists k3d; then
        # k3d merges config into default kubeconfig
        echo "$HOME/.kube/config"
    else
        print_error "k3d not installed"
        return 1
    fi
}

get_k3d_kubectl() {
    if command_exists kubectl; then
        echo "kubectl"
    else
        print_error "kubectl not installed"
        return 1
    fi
}

# ========================================
# kind Installation Functions
# ========================================

install_kind_cluster() {
    local cluster_name="${KIND_CLUSTER_NAME:-pipeops}"
    local k8s_version="${KIND_K8S_VERSION:-}"
    local workers="${KIND_WORKERS:-0}"
    
    print_status "Installing kind cluster '$cluster_name'..."
    
    # Install kind binary if not exists
    if ! command_exists kind; then
        print_status "Downloading kind..."
        
        local os=$(uname -s | tr '[:upper:]' '[:lower:]')
        local arch=$(uname -m)
        
        # Map arch names
        case "$arch" in
            x86_64) arch="amd64" ;;
            aarch64) arch="arm64" ;;
        esac
        
        local kind_url="https://kind.sigs.k8s.io/dl/latest/kind-${os}-${arch}"
        
        curl -Lo /tmp/kind "$kind_url"
        chmod +x /tmp/kind
        sudo mv /tmp/kind /usr/local/bin/kind
        
        print_success "kind binary installed"
    else
        print_warning "kind is already installed"
    fi
    
    # Check if cluster already exists
    if kind get clusters | grep -q "^$cluster_name$"; then
        print_warning "kind cluster '$cluster_name' already exists"
    else
        print_status "Creating kind cluster '$cluster_name'..."
        
        # Create cluster config if multi-node
        if [ "$workers" -gt 0 ]; then
            cat > /tmp/kind-config.yaml <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
EOF
            for i in $(seq 1 "$workers"); do
                echo "- role: worker" >> /tmp/kind-config.yaml
            done
            
            if [ -n "$k8s_version" ]; then
                kind create cluster \
                    --name "$cluster_name" \
                    --config /tmp/kind-config.yaml \
                    --image "kindest/node:${k8s_version}" \
                    --wait 120s
            else
                kind create cluster \
                    --name "$cluster_name" \
                    --config /tmp/kind-config.yaml \
                    --wait 120s
            fi
            
            rm -f /tmp/kind-config.yaml
        else
            if [ -n "$k8s_version" ]; then
                kind create cluster \
                    --name "$cluster_name" \
                    --image "kindest/node:${k8s_version}" \
                    --wait 120s
            else
                kind create cluster \
                    --name "$cluster_name" \
                    --wait 120s
            fi
        fi
        
        print_success "kind cluster created"
    fi
    
    # Verify cluster is ready
    print_status "Waiting for cluster to be ready..."
    kubectl wait --for=condition=Ready nodes --all --timeout=120s
    
    print_success "kind installation complete"
}

get_kind_kubeconfig() {
    local cluster_name="${KIND_CLUSTER_NAME:-pipeops}"
    
    if command_exists kind; then
        # kind uses default kubeconfig
        echo "$HOME/.kube/config"
    else
        print_error "kind not installed"
        return 1
    fi
}

get_kind_kubectl() {
    if command_exists kubectl; then
        echo "kubectl"
    else
        print_error "kubectl not installed"
        return 1
    fi
}

# ========================================
# Generic Functions
# ========================================

install_cluster() {
    local cluster_type="${1:-k3s}"
    
    case "$cluster_type" in
        "k3s")
            install_k3s_cluster
            ;;
        "minikube")
            install_minikube_cluster
            ;;
        "k3d")
            install_k3d_cluster
            ;;
        "kind")
            install_kind_cluster
            ;;
        *)
            print_error "Unknown cluster type: $cluster_type"
            print_error "Supported types: k3s, minikube, k3d, kind"
            return 1
            ;;
    esac
}

get_kubeconfig() {
    local cluster_type="${1:-k3s}"
    
    case "$cluster_type" in
        "k3s")
            get_k3s_kubeconfig
            ;;
        "minikube")
            get_minikube_kubeconfig
            ;;
        "k3d")
            get_k3d_kubeconfig
            ;;
        "kind")
            get_kind_kubeconfig
            ;;
        *)
            print_error "Unknown cluster type: $cluster_type"
            return 1
            ;;
    esac
}

get_kubectl_cmd() {
    local cluster_type="${1:-k3s}"
    
    case "$cluster_type" in
        "k3s")
            get_k3s_kubectl
            ;;
        "minikube")
            get_minikube_kubectl
            ;;
        "k3d")
            get_k3d_kubectl
            ;;
        "kind")
            get_kind_kubectl
            ;;
        *)
            print_error "Unknown cluster type: $cluster_type"
            return 1
            ;;
    esac
}

# Check if script is being sourced or executed
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
    # Script is being executed directly
    if [ $# -eq 0 ]; then
        print_error "Please specify a cluster type: k3s, minikube, k3d, or kind"
        exit 1
    fi
    
    install_cluster "$1"
fi
