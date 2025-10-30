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

# Detect host CPU count for resource-aware provisioning
get_host_cpu_count() {
    if command_exists nproc; then
        nproc
    elif [ -f /proc/cpuinfo ]; then
        grep -c ^processor /proc/cpuinfo
    elif command_exists sysctl; then
        sysctl -n hw.ncpu 2>/dev/null || echo "1"
    else
        echo "1"
    fi
}

# Detect host memory in MB for resource-aware provisioning
get_host_memory_mb() {
    if command_exists free; then
        free -m | awk 'NR==2{print $2}'
    elif [ -f /proc/meminfo ]; then
        awk '/MemTotal/ {printf "%.0f", $2/1024}' /proc/meminfo
    elif [ "$(uname)" = "Darwin" ] && command_exists sysctl; then
        sysctl -n hw.memsize | awk '{printf "%.0f", $1/1024/1024}'
    else
        echo "0"
    fi
}

normalize_environment_profile() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

determine_profile_cpus() {
    local profile
    profile=$(normalize_environment_profile "$1")
    local host_cpus="${2:-0}"

    if [ -z "$profile" ]; then
        if [ "$host_cpus" -le 0 ]; then
            echo "2"
        else
            echo "$host_cpus"
        fi
        return
    fi

    if [ "$host_cpus" -le 0 ]; then
        host_cpus=2
    fi

    case "$profile" in
        dev|development)
            if [ "$host_cpus" -lt 2 ]; then
                echo "$host_cpus"
            else
                echo "2"
            fi
            ;;
        staging|stage|test|qa|uat|preprod|pre-production)
            if [ "$host_cpus" -lt 4 ]; then
                echo "$host_cpus"
            else
                echo "4"
            fi
            ;;
        prod|production)
            echo "$host_cpus"
            ;;
        *)
            echo "$host_cpus"
            ;;
    esac
}

determine_profile_memory() {
    local profile
    profile=$(normalize_environment_profile "$1")
    local host_memory="${2:-0}"

    if [ -z "$profile" ]; then
        if [ "$host_memory" -le 0 ]; then
            echo "2048"
        else
            echo "$host_memory"
        fi
        return
    fi

    if [ "$host_memory" -le 0 ]; then
        host_memory=2048
    fi

    case "$profile" in
        dev|development)
            if [ "$host_memory" -le 0 ]; then
                echo "4096"
            elif [ "$host_memory" -lt 4096 ]; then
                echo "$host_memory"
            else
                echo "4096"
            fi
            ;;
        staging|stage|test|qa|uat|preprod|pre-production)
            if [ "$host_memory" -le 0 ]; then
                echo "8192"
            elif [ "$host_memory" -lt 8192 ]; then
                echo "$host_memory"
            else
                echo "8192"
            fi
            ;;
        prod|production)
            echo "$host_memory"
            ;;
        *)
            echo "$host_memory"
            ;;
    esac
}

calculate_minikube_resources() {
    local env_profile="$1"
    local explicit_cpus="$2"
    local explicit_memory="$3"

    local host_cpus
    host_cpus=$(get_host_cpu_count)
    local host_memory
    host_memory=$(get_host_memory_mb)

    local resolved_cpus="$explicit_cpus"
    local resolved_memory="$explicit_memory"

    if [ -z "$resolved_cpus" ] || [ "$resolved_cpus" -le 0 ]; then
        resolved_cpus=$(determine_profile_cpus "$env_profile" "$host_cpus")
    fi

    if [ -z "$resolved_memory" ] || [ "$resolved_memory" -le 0 ]; then
        resolved_memory=$(determine_profile_memory "$env_profile" "$host_memory")
    fi

    if [ -z "$resolved_cpus" ] || [ "$resolved_cpus" -le 0 ]; then
        resolved_cpus=2
    fi

    if [ -z "$resolved_memory" ] || [ "$resolved_memory" -le 0 ]; then
        resolved_memory=2048
    fi

    echo "$resolved_cpus $resolved_memory"
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

install_minikube_cluster() {
    local minikube_version="${MINIKUBE_VERSION:-latest}"
    local k8s_version="${KUBERNETES_VERSION:-stable}"
    local driver="${MINIKUBE_DRIVER:-docker}"
    local env_profile="${PIPEOPS_ENVIRONMENT:-${CLUSTER_ENVIRONMENT:-${ENVIRONMENT:-}}}"
    local cpus
    local memory

    read -r cpus memory <<< "$(calculate_minikube_resources "$env_profile" "${MINIKUBE_CPUS:-}" "${MINIKUBE_MEMORY:-}")"

    local start_args=(
        "--driver=${driver}"
        "--cpus=${cpus}"
        "--memory=${memory}mb"
        "--kubernetes-version=${k8s_version}"
    )

    local profile_label="${env_profile:-auto}"
    if [ -n "${MINIKUBE_CPUS:-}" ] || [ -n "${MINIKUBE_MEMORY:-}" ]; then
        print_status "Minikube resources resolved to ${cpus} CPU(s) and ${memory}MB (user override)"
    else
        print_status "Minikube resources resolved to ${cpus} CPU(s) and ${memory}MB (profile: ${profile_label})"
    fi
    
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
    
    # Start minikube cluster
    if minikube status >/dev/null 2>&1; then
        print_warning "minikube cluster is already running"
        if ! minikube kubectl -- get nodes >/dev/null 2>&1; then
            print_warning "Existing minikube control plane is unresponsive; restarting..."
            minikube stop >/dev/null 2>&1 || true
            if ! minikube start "${start_args[@]}"; then
                print_error "Failed to restart existing minikube cluster"
                return 1
            fi
            print_success "minikube cluster restarted"
        fi
    else
        print_status "Starting minikube cluster..."
        if ! minikube start "${start_args[@]}"; then
            print_error "Failed to start minikube cluster"
            return 1
        fi
        print_success "minikube cluster started"
    fi
    
    # Verify cluster is ready
    print_status "Waiting for cluster to be ready..."
    local WAIT_OUTPUT
    if ! WAIT_OUTPUT=$(minikube kubectl -- wait --for=condition=Ready nodes --all --timeout=120s 2>&1); then
        print_warning "Kubernetes API not reachable: ${WAIT_OUTPUT}"
        print_status "Restarting minikube cluster..."
        minikube stop >/dev/null 2>&1 || true
        if ! minikube start "${start_args[@]}"; then
            print_error "Failed to restart minikube cluster"
            printf '%s\n' "$WAIT_OUTPUT"
            return 1
        fi
        print_status "Waiting for cluster to be ready after restart..."
        if ! WAIT_OUTPUT=$(minikube kubectl -- wait --for=condition=Ready nodes --all --timeout=120s 2>&1); then
            print_error "minikube cluster failed to become ready after restart"
            printf '%s\n' "$WAIT_OUTPUT"
            print_status "Deleting minikube cluster for a clean retry..."
            minikube delete >/dev/null 2>&1 || true
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
# Talos Installation Functions
# ========================================

install_talos_cluster() {
    local cluster_name="${TALOS_CLUSTER_NAME:-pipeops}"
    local docker_based="${TALOS_USE_DOCKER:-false}"
    
    # Check if Docker is available for Docker-based Talos
    if [ "$docker_based" = "true" ] && command_exists docker; then
        print_status "Installing Docker-based Talos cluster for testing/development..."
        install_talos_docker_cluster
        return $?
    fi
    
    # Default: Show error for bare-metal Talos installation
    print_error "Talos Linux installation is not supported by this installer"
    print_error ""
    print_error "Talos Linux is an immutable OS that must be installed as the base operating system."
    print_error "It cannot be installed on top of an existing Linux distribution."
    print_error ""
    print_error "To use Talos Linux, you have these options:"
    print_error ""
    print_error "1. Docker-based Talos (testing/development only):"
    print_error "   export TALOS_USE_DOCKER=true"
    print_error "   export CLUSTER_TYPE=talos"
    print_error "   $0"
    print_error ""
    print_error "2. Bare metal/VM Talos (production):"
    print_error "   - Boot from Talos ISO image"
    print_error "   - Use cloud provider Talos images (AWS AMI, Azure, etc.)"
    print_error "   - Visit: https://www.talos.dev/latest/introduction/getting-started/"
    print_error ""
    print_status "Recommendation: Use k3s for production on existing Linux systems"
    print_status "  export CLUSTER_TYPE=k3s"
    print_status "  $0"
    
    return 1
}

install_talos_docker_cluster() {
    local cluster_name="${TALOS_CLUSTER_NAME:-pipeops}"
    
    print_status "Setting up Docker-based Talos cluster '$cluster_name'..."
    
    # Install talosctl if not present
    if ! command_exists talosctl; then
        print_status "Installing talosctl..."
        
        local os=$(uname -s | tr '[:upper:]' '[:lower:]')
        local arch=$(uname -m)
        
        case "$arch" in
            x86_64) arch="amd64" ;;
            aarch64) arch="arm64" ;;
        esac
        
        local talos_version="${TALOS_VERSION:-v1.7.0}"
        local talosctl_url="https://github.com/siderolabs/talos/releases/download/${talos_version}/talosctl-${os}-${arch}"
        
        curl -Lo /tmp/talosctl "$talosctl_url"
        chmod +x /tmp/talosctl
        sudo mv /tmp/talosctl /usr/local/bin/talosctl
        
        print_success "talosctl installed"
    fi
    
    # Check for existing cluster
    if talosctl cluster show "$cluster_name" >/dev/null 2>&1; then
        print_warning "Talos cluster '$cluster_name' already exists"
        echo ""
        read -p "Do you want to destroy and recreate it? (y/N): " -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            print_status "Destroying existing cluster..."
            talosctl cluster destroy "$cluster_name" || true
            sleep 2
        else
            print_status "Using existing cluster"
            return 0
        fi
    fi
    
    # Check for existing Talos configuration
    if [ -d "$HOME/.talos" ] && [ -f "$HOME/.talos/controlplane.yaml" ]; then
        print_warning "Existing Talos configuration found in ~/.talos/"
        echo ""
        read -p "Overwrite existing configuration? (y/N): " -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            print_status "Backing up existing configuration..."
            mv "$HOME/.talos" "$HOME/.talos.backup.$(date +%s)"
            print_success "Backup created at ~/.talos.backup.*"
        else
            print_status "Using existing configuration"
        fi
    fi
    
    # Create Docker-based Talos cluster
    print_status "Creating Docker-based Talos cluster..."
    print_warning "This cluster is for testing/development only"
    print_warning "Use bare-metal Talos for production deployments"
    echo ""
    
    talosctl cluster create \
        --name "$cluster_name" \
        --wait \
        --wait-timeout 5m
    
    if [ $? -ne 0 ]; then
        print_error "Failed to create Talos cluster"
        return 1
    fi
    
    # Wait for cluster to be ready
    print_status "Waiting for cluster to be ready..."
    sleep 10
    
    # Get kubeconfig
    print_status "Configuring kubectl access..."
    talosctl kubeconfig --nodes 127.0.0.1 --force
    
    # Verify cluster
    print_status "Verifying cluster..."
    kubectl get nodes
    
    print_success "Docker-based Talos cluster created successfully"
    print_status "Cluster details:"
    print_status "  • Name: $cluster_name"
    print_status "  • Type: Docker-based (development)"
    print_status "  • Kubeconfig: ~/.kube/config"
    print_status ""
    print_status "Management commands:"
    print_status "  • Show cluster: talosctl cluster show $cluster_name"
    print_status "  • Get nodes: kubectl get nodes"
    print_status "  • Destroy: talosctl cluster destroy $cluster_name"
    
    return 0
}

get_talos_kubeconfig() {
    if command_exists talosctl; then
        echo "$HOME/.kube/config"
    else
        print_error "talosctl not installed"
        return 1
    fi
}

get_talos_kubectl() {
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
        "talos")
            install_talos_cluster
            ;;
        *)
            print_error "Unknown cluster type: $cluster_type"
            print_error "Supported types: k3s, minikube, k3d, kind, talos"
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
        "talos")
            get_talos_kubeconfig
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
        "talos")
            get_talos_kubectl
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
