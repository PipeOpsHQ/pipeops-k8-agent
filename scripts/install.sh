#!/bin/bash

# PipeOps Intelligent Cluster Installation Script
# 
# This script intelligently detects your environment and installs the optimal
# Kubernetes distribution (k3s, minikube, k3d, or kind) along with the PipeOps
# agent and complete monitoring stack.
#
# Features:
# - Automatic cluster type detection based on system resources
# - Support for k3s, minikube, k3d, and kind
# - Integrated monitoring stack (Prometheus, Loki, Grafana, OpenCost)
# - Resource-aware decision making
# - Environment detection (Docker, LXC, WSL, macOS)
#
# Usage:
#   export PIPEOPS_TOKEN="your-token"
#   ./install.sh
#
# For manual cluster selection:
#   export CLUSTER_TYPE="k3d"  # Options: k3s, minikube, k3d, kind
#   ./install.sh

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PIPEOPS_API_URL="${PIPEOPS_API_URL:-https://api.pipeops.sh}"
# Support both PIPEOPS_TOKEN and AGENT_TOKEN for backward compatibility
AGENT_TOKEN="${PIPEOPS_TOKEN:-${AGENT_TOKEN:-}}"
CLUSTER_NAME="${CLUSTER_NAME:-default-cluster}"
K3S_VERSION="${K3S_VERSION:-v1.28.3+k3s2}"
AGENT_IMAGE="${AGENT_IMAGE:-ghcr.io/pipeopshq/pipeops-k8-agent:latest}"
NAMESPACE="${NAMESPACE:-pipeops-system}"
REPO_BASE_URL="${REPO_BASE_URL:-https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts}"

# Cluster type configuration
CLUSTER_TYPE="${CLUSTER_TYPE:-auto}"      # auto, k3s, minikube, k3d, or kind
AUTO_DETECT="${AUTO_DETECT:-true}"        # Enable/disable auto-detection
INSTALL_MONITORING="${INSTALL_MONITORING:-false}"  # Install monitoring stack when explicitly enabled

# Worker node configuration
K3S_URL="${K3S_URL:-}"                    # Master server URL for worker nodes
K3S_TOKEN="${K3S_TOKEN:-}"                # Cluster token for joining
NODE_TYPE="${NODE_TYPE:-server}"          # server or agent (worker)
MASTER_IP="${MASTER_IP:-}"               # Master node IP for worker join

# Track execution privileges
IS_ROOT_USER="false"

# Get script directory for sourcing other scripts
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_SCRIPT_DIR=""

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

# Ensure helper scripts are available even when running via curl | bash
ensure_support_scripts() {
    local missing_helpers=0
    for helper in detect-cluster-type.sh install-cluster.sh; do
        if [ ! -f "$SCRIPT_DIR/$helper" ]; then
            missing_helpers=1
            break
        fi
    done

    if [ "$missing_helpers" -eq 0 ]; then
        return 0
    fi

    print_warning "Local helper scripts not found; downloading from repository"

    TMP_SCRIPT_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t pipeops-scripts)"
    if [ -z "$TMP_SCRIPT_DIR" ] || [ ! -d "$TMP_SCRIPT_DIR" ]; then
        print_error "Failed to create temporary directory for helper scripts"
        exit 1
    fi

    trap 'if [ -n "$TMP_SCRIPT_DIR" ] && [ -d "$TMP_SCRIPT_DIR" ]; then rm -rf "$TMP_SCRIPT_DIR"; fi' EXIT

    for helper in detect-cluster-type.sh install-cluster.sh; do
        if ! curl -fsSL "${REPO_BASE_URL}/${helper}" -o "${TMP_SCRIPT_DIR}/${helper}"; then
            print_error "Failed to download ${helper} from repository"
            exit 1
        fi
        chmod +x "${TMP_SCRIPT_DIR}/${helper}"
    done

    SCRIPT_DIR="$TMP_SCRIPT_DIR"
}

ensure_support_scripts

# Function to detect if running in Proxmox LXC container
is_proxmox_lxc() {
    # Check for LXC in environment
    if [ -n "$container" ] && [ "$container" = "lxc" ]; then
        return 0  # LXC container
    fi
    
    # Check for LXC in /proc/1/environ
    if grep -q "container=lxc" /proc/1/environ 2>/dev/null; then
        return 0  # LXC container
    fi
    
    return 1  # Not LXC
}

# Function to check if command exists
command_exists() {
    command -v "$@" > /dev/null 2>&1
}

fallback_cluster_type() {
    local os_name
    os_name="$(uname)"

    if [ "$os_name" = "Darwin" ]; then
        echo "minikube"
        return
    fi

    if [ "$IS_ROOT_USER" = "true" ]; then
        echo "k3s"
        return
    fi

    if command_exists docker || command_exists nerdctl || command_exists podman; then
        echo "k3d"
    else
        echo "minikube"
    fi
}

# Function to check system requirements
check_requirements() {
    print_status "Checking system requirements..."
    
    # Record whether we are running as root
    if [ "$(id -u)" = "0" ]; then
        IS_ROOT_USER="true"
    else
        IS_ROOT_USER="false"
        print_warning "Running without root privileges - some cluster types may require sudo"
    fi

    # Check OS compatibility
    local os_name="$(uname)"
    if [ "$os_name" = "Darwin" ]; then
        if [ "$IS_ROOT_USER" = "true" ]; then
            print_error "macOS installs must be run as a regular user"
            print_error "Docker Desktop and hypervisor drivers are unavailable to the root account"
            print_error "Please rerun this script without sudo"
            exit 1
        fi
        # macOS is supported for development cluster types only
        if [ "$CLUSTER_TYPE" = "k3s" ] && [ "$AUTO_DETECT" = "true" ]; then
            print_warning "macOS detected - auto-detection will prefer development cluster types"
        elif [ "$CLUSTER_TYPE" = "k3s" ] && [ "$AUTO_DETECT" != "true" ]; then
            print_error "k3s is not supported on macOS. Use minikube, k3d, or kind instead"
            print_error "Set CLUSTER_TYPE=minikube (or k3d/kind) for macOS development"
            exit 1
        fi
    fi

    # Check if running inside a Docker container
    if [ -f /.dockerenv ]; then
        print_error "This script should not be run inside a Docker container"
        exit 1
    fi

    # Check available memory (k3s needs at least 512MB)
    if command_exists free; then
        available_memory=$(free -m | awk 'NR==2{printf "%.0f", $7}')
        if [ "$available_memory" -lt 512 ]; then
            print_warning "Available memory is ${available_memory}MB. k3s requires at least 512MB"
        fi
    elif [ "$(uname)" = "Darwin" ]; then
        # On macOS, we assume sufficient memory for development clusters
        print_status "Memory check skipped on macOS (development environment)"
    else
        print_warning "Could not check available memory"
    fi

    # Check disk space (need at least 2GB)
    if [ "$(uname)" = "Darwin" ]; then
        # macOS df output is different
        available_disk_gb=$(df -g / | awk 'NR==2{print $4}')
    else
        # Linux df output
        available_disk=$(df / | awk 'NR==2{print $4}')
        available_disk_gb=$((available_disk / 1024 / 1024))
    fi
    
    if [ "$available_disk_gb" -lt 2 ]; then
        print_warning "Available disk space is ${available_disk_gb}GB. Recommend at least 2GB"
    fi

    print_success "System requirements check passed"
}

# Function to get server IP address
get_ip() {
    local ip=""
    
    # Try to get private IP first
    ip=$(ip route get 1 2>/dev/null | awk '{print $7; exit}' 2>/dev/null)
    
    # If no private IP, try public IP
    if [ -z "$ip" ]; then
        # Try IPv4 first
        ip=$(curl -4s --connect-timeout 5 https://ifconfig.io 2>/dev/null)
        
        # Second attempt: icanhazip.com
        if [ -z "$ip" ]; then
            ip=$(curl -4s --connect-timeout 5 https://icanhazip.com 2>/dev/null)
        fi
        
        # Third attempt: ipecho.net
        if [ -z "$ip" ]; then
            ip=$(curl -4s --connect-timeout 5 https://ipecho.net/plain 2>/dev/null)
        fi
    fi

    if [ -z "$ip" ]; then
        print_error "Could not determine server IP address automatically."
        print_error "Please set the SERVER_IP environment variable manually."
        print_error "Example: export SERVER_IP=<your-server-ip>"
        exit 1
    fi

    echo "$ip"
}

# Function to detect and set cluster type
detect_and_set_cluster_type() {
    # If cluster type is explicitly set and not auto, use it
    if [ "$CLUSTER_TYPE" != "auto" ]; then
        print_status "Using explicitly set cluster type: $CLUSTER_TYPE"
        return 0
    fi
    
    # Auto-detect cluster type
    if [ "$AUTO_DETECT" = "true" ]; then
        print_status "Auto-detecting optimal cluster type..."
        
        # Source detection script
        local detector_script="$SCRIPT_DIR/detect-cluster-type.sh"

        if [ ! -f "$detector_script" ]; then
            ensure_support_scripts
            detector_script="$SCRIPT_DIR/detect-cluster-type.sh"
        fi

        if [ -f "$detector_script" ]; then
            CLUSTER_TYPE=$("$detector_script" recommend)
            
            if [ "$CLUSTER_TYPE" = "none" ]; then
                CLUSTER_TYPE="$(fallback_cluster_type)"
                print_warning "Detection returned 'none'; falling back to $CLUSTER_TYPE"
            fi
            
            print_success "Auto-detected cluster type: $CLUSTER_TYPE"
            
            # Show detailed info
            "$detector_script" info
        else
            CLUSTER_TYPE="$(fallback_cluster_type)"
            print_warning "Detection script not available, defaulting to $CLUSTER_TYPE"
        fi
    else
        print_status "Auto-detection disabled, using default: k3s"
        CLUSTER_TYPE="k3s"
    fi
}

# Ensure privilege level matches cluster requirements
enforce_privilege_requirements() {
    case "$CLUSTER_TYPE" in
        "k3s")
            if [ "$IS_ROOT_USER" != "true" ]; then
                print_error "k3s installation requires root privileges"
                print_error "Rerun this script with sudo or as the root user"
                exit 1
            fi
            ;;
        "minikube"|"k3d"|"kind")
            if [ "$IS_ROOT_USER" = "true" ] && [ "${ALLOW_DEV_CLUSTERS_AS_ROOT:-false}" != "true" ]; then
                print_error "$CLUSTER_TYPE cannot run reliably with root privileges when using the default Docker driver"
                print_error "Rerun without sudo, or set MINIKUBE_DRIVER=none and ALLOW_DEV_CLUSTERS_AS_ROOT=true if you understand the risks"
                exit 1
            fi
            ;;
    esac
}

# Function to install kubernetes cluster
install_cluster() {
    print_status "Installing $CLUSTER_TYPE cluster..."
    
    # Source cluster installation functions
    local installer_script="$SCRIPT_DIR/install-cluster.sh"

    if [ ! -f "$installer_script" ]; then
        ensure_support_scripts
        installer_script="$SCRIPT_DIR/install-cluster.sh"
    fi

    if [ ! -f "$installer_script" ]; then
        print_error "Cluster installation script not found"
        exit 1
    fi

    # shellcheck source=/dev/null
    source "$installer_script"
    
    # Install the selected cluster type
    install_cluster "$CLUSTER_TYPE"
    
    print_success "$CLUSTER_TYPE cluster installed successfully"
}

install_minikube_components() {
    if ! command_exists minikube; then
        print_warning "minikube binary not detected; skipping addon enablement"
        return 0
    fi

    print_status "Configuring essential minikube addons..."

    local addon_status
    addon_status=$(minikube addons list 2>/dev/null | grep -E "^\s*metrics-server" || true)

    if printf '%s' "$addon_status" | grep -q "enabled"; then
        print_warning "metrics-server addon already enabled"
    else
        if minikube addons enable metrics-server >/dev/null 2>&1; then
            print_success "Enabled minikube metrics-server addon"
        else
            print_warning "Failed to enable metrics-server addon automatically"
        fi
    fi
}

install_post_cluster_components() {
    if [ "$NODE_TYPE" = "agent" ]; then
        return 0
    fi

    case "$CLUSTER_TYPE" in
        "minikube")
            install_minikube_components
            ;;
    esac
}

# Function to get kubectl command for current cluster type
get_kubectl() {
    case "$CLUSTER_TYPE" in
        "k3s")
            if command_exists k3s; then
                echo "k3s kubectl"
            elif command_exists kubectl; then
                echo "kubectl"
            else
                echo "k3s kubectl"
            fi
            ;;
        "minikube")
            if command_exists kubectl; then
                echo "kubectl"
            else
                echo "minikube kubectl --"
            fi
            ;;
        "k3d"|"kind")
            echo "kubectl"
            ;;
        *)
            echo "kubectl"
            ;;
    esac
}

ensure_kubeconfig_env() {
    if [ "$CLUSTER_TYPE" = "k3s" ] && [ -z "${KUBECONFIG:-}" ] && [ -f /etc/rancher/k3s/k3s.yaml ]; then
        export KUBECONFIG="/etc/rancher/k3s/k3s.yaml"
    fi
}

# Function to create agent configuration
create_agent_config() {
    # Only deploy agent on server nodes
    if [ "$NODE_TYPE" = "agent" ]; then
        print_status "Skipping agent deployment on worker node"
        return 0
    fi

    print_status "Creating agent configuration..."

    ensure_kubeconfig_env
    
    if [ -z "$AGENT_TOKEN" ]; then
        print_error "PipeOps token is required"
        print_error "Please set your PipeOps agent token using either:"
        print_error "  export PIPEOPS_TOKEN=your-token-here"
        print_error "  OR"
        print_error "  export AGENT_TOKEN=your-token-here"
        exit 1
    fi

    # Get kubectl command for current cluster
    local KUBECTL=$(get_kubectl)

    # Create namespace
    $KUBECTL create namespace "$NAMESPACE" --dry-run=client -o yaml | $KUBECTL apply -f -

    # Create secret with configuration
    $KUBECTL create secret generic pipeops-agent-config \
        --namespace="$NAMESPACE" \
        --from-literal=PIPEOPS_API_URL="$PIPEOPS_API_URL" \
        --from-literal=PIPEOPS_TOKEN="$AGENT_TOKEN" \
        --from-literal=PIPEOPS_CLUSTER_NAME="$CLUSTER_NAME" \
        --dry-run=client -o yaml | $KUBECTL apply -f -

    print_success "Agent configuration created"
}

# Function to install monitoring components (Prometheus, Loki, Grafana, OpenCost)
install_monitoring_stack() {
    # Only install on server nodes
    if [ "$NODE_TYPE" = "agent" ]; then
        print_status "Skipping monitoring stack installation on worker node"
        return 0
    fi
    
    # Check if monitoring installation is enabled
    if [ "$INSTALL_MONITORING" != "true" ]; then
        print_status "Monitoring stack installation disabled (INSTALL_MONITORING=false)"
        return 0
    fi

    print_status "Installing monitoring stack components..."

    ensure_kubeconfig_env
    
    local KUBECTL=$(get_kubectl)
    
    # Check if Helm is installed
    if ! command_exists helm; then
        print_status "Installing Helm..."
        curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
        print_success "Helm installed"
    fi
    
    # Add required Helm repositories
    print_status "Adding Helm repositories..."
    helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
    helm repo add grafana https://grafana.github.io/helm-charts 2>/dev/null || true
    helm repo update
    
    # Create monitoring namespace
    $KUBECTL create namespace monitoring --dry-run=client -o yaml | $KUBECTL apply -f -
    
    # Install Prometheus
    if ! helm status prometheus -n monitoring >/dev/null 2>&1; then
        print_status "Installing Prometheus..."
        helm install prometheus prometheus-community/kube-prometheus-stack \
            --namespace monitoring \
            --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
            --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false \
            --wait --timeout=300s || print_warning "Prometheus installation may need manual verification"
        print_success "Prometheus installed"
    else
        print_warning "Prometheus already installed"
    fi
    
    # Install Loki
    if ! helm status loki -n monitoring >/dev/null 2>&1; then
        print_status "Installing Loki..."
        helm install loki grafana/loki-stack \
            --namespace monitoring \
            --set promtail.enabled=true \
            --set loki.persistence.enabled=true \
            --set loki.persistence.size=10Gi \
            --wait --timeout=300s || print_warning "Loki installation may need manual verification"
        print_success "Loki installed"
    else
        print_warning "Loki already installed"
    fi
    
    # Install OpenCost
    if ! helm status opencost -n monitoring >/dev/null 2>&1; then
        print_status "Installing OpenCost..."
        helm install opencost prometheus-community/opencost \
            --namespace monitoring \
            --set opencost.prometheus.internal.serviceName=prometheus-kube-prometheus-prometheus \
            --wait --timeout=300s || print_warning "OpenCost installation may need manual verification"
        print_success "OpenCost installed"
    else
        print_warning "OpenCost already installed"
    fi
    
    print_success "Monitoring stack installation complete"
}

# Function to deploy the agent
deploy_agent() {
    # Only deploy agent on server nodes
    if [ "$NODE_TYPE" = "agent" ]; then
        print_status "Skipping agent deployment on worker node"
        return 0
    fi

    print_status "Deploying PipeOps agent..."

    ensure_kubeconfig_env
    
    local KUBECTL=$(get_kubectl)
    
    # Remove existing resources to avoid immutable field conflicts
    print_status "Cleaning up any existing agent resources before redeployment"
    $KUBECTL delete deployment pipeops-agent -n "$NAMESPACE" --ignore-not-found
    $KUBECTL delete clusterrolebinding pipeops-agent --ignore-not-found

    # Create temporary manifest file
    cat > /tmp/pipeops-agent.yaml << EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pipeops-agent
  namespace: $NAMESPACE
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
        name: pipeops-agent
rules:
        - apiGroups:
                - ""
            resources:
                - nodes
                - nodes/status
                - namespaces
                - pods
                - pods/log
                - pods/status
                - services
                - serviceaccounts
                - endpoints
                - configmaps
                - secrets
                - persistentvolumes
                - persistentvolumeclaims
                - events
                - resourcequotas
                - limitranges
                - replicationcontrollers
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - apps
            resources:
                - deployments
                - deployments/status
                - deployments/scale
                - replicasets
                - replicasets/status
                - daemonsets
                - daemonsets/status
                - statefulsets
                - statefulsets/status
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - extensions
            resources:
                - deployments
                - deployments/status
                - deployments/scale
                - replicasets
                - replicasets/status
                - ingresses
                - ingresses/status
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - batch
            resources:
                - jobs
                - jobs/status
                - cronjobs
                - cronjobs/status
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - autoscaling
            resources:
                - horizontalpodautoscalers
            verbs:
                - get
                - list
                - watch
        - apiGroups:
                - networking.k8s.io
            resources:
                - ingresses
                - ingresses/status
                - ingressclasses
                - networkpolicies
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - rbac.authorization.k8s.io
            resources:
                - roles
                - rolebindings
                - clusterroles
                - clusterrolebindings
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - apiregistration.k8s.io
            resources:
                - apiservices
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - apiextensions.k8s.io
            resources:
                - customresourcedefinitions
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - admissionregistration.k8s.io
            resources:
                - mutatingwebhookconfigurations
                - validatingwebhookconfigurations
            verbs:
                - get
                - list
                - watch
        - apiGroups:
                - certificates.k8s.io
            resources:
                - certificatesigningrequests
            verbs:
                - get
                - list
                - watch
        - apiGroups:
                - coordination.k8s.io
            resources:
                - leases
            verbs:
                - get
                - list
                - watch
        - apiGroups:
                - policy
            resources:
                - poddisruptionbudgets
                - podsecuritypolicies
            verbs:
                - get
                - list
                - watch
                - use
        - apiGroups:
                - storage.k8s.io
            resources:
                - storageclasses
                - volumeattachments
            verbs:
                - get
                - list
                - watch
        - apiGroups:
                - metrics.k8s.io
            resources:
                - nodes
                - pods
            verbs:
                - get
                - list
        - apiGroups:
                - monitoring.coreos.com
            resources:
                - servicemonitors
                - podmonitors
                - prometheusrules
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - networking.istio.io
            resources:
                - virtualservices
                - destinationrules
                - gateways
            verbs:
                - get
                - list
                - watch
                - create
                - update
                - patch
                - delete
        - apiGroups:
                - ""
            resources:
                - pods/exec
                - pods/portforward
            verbs:
                - create
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pipeops-agent
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: pipeops-agent
subjects:
- kind: ServiceAccount
  name: pipeops-agent
  namespace: $NAMESPACE
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pipeops-agent
  namespace: $NAMESPACE
  labels:
    app: pipeops-agent
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: pipeops-agent
  template:
    metadata:
      labels:
        app: pipeops-agent
    spec:
      serviceAccountName: pipeops-agent
      containers:
      - name: agent
        image: $AGENT_IMAGE
        imagePullPolicy: Always
        envFrom:
        - secretRef:
            name: pipeops-agent-config
        env:
        - name: PIPEOPS_AGENT_ID
          valueFrom:
            fieldRef:
              fieldPath: metadata.uid
        - name: PIPEOPS_NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: PIPEOPS_POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        args:
        - --log-level=info
        - --in-cluster=true
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          runAsNonRoot: true
          runAsUser: 1000
          capabilities:
            drop:
            - ALL
        volumeMounts:
        - name: tmp
          mountPath: /tmp
      volumes:
      - name: tmp
        emptyDir: {}
      nodeSelector:
        kubernetes.io/os: linux
      tolerations:
      - key: node-role.kubernetes.io/master
        operator: Exists
        effect: NoSchedule
      - key: node-role.kubernetes.io/control-plane
        operator: Exists
        effect: NoSchedule
EOF

    # Apply the manifest
    $KUBECTL apply -f /tmp/pipeops-agent.yaml

    # Wait for deployment to be ready
    print_status "Waiting for agent to be ready..."
    $KUBECTL wait --for=condition=available --timeout=300s deployment/pipeops-agent -n "$NAMESPACE" || print_warning "Agent deployment may need more time"

    # Clean up temporary file
    rm -f /tmp/pipeops-agent.yaml

    print_success "PipeOps agent deployed successfully"
}

# Function to verify installation
verify_installation() {
    print_status "Verifying installation..."
    
    ensure_kubeconfig_env

    local KUBECTL=$(get_kubectl)
    
    if [ "$NODE_TYPE" = "server" ]; then
        # Check cluster status
        if ! $KUBECTL get nodes >/dev/null 2>&1; then
            print_error "Cluster is not running properly"
            return 1
        fi

        # Check agent status
        if ! $KUBECTL get deployment pipeops-agent -n "$NAMESPACE" >/dev/null 2>&1; then
            print_error "PipeOps agent deployment not found"
            return 1
        fi

        local replicas_ready=$($KUBECTL get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
        if [ "$replicas_ready" != "1" ]; then
            print_warning "PipeOps agent may still be starting (ready replicas: $replicas_ready)"
        else
            print_success "PipeOps agent is ready"
        fi
    else
        # Check worker node status (k3s specific)
        if [ "$CLUSTER_TYPE" = "k3s" ]; then
            if ! systemctl is-active --quiet k3s-agent; then
                print_error "k3s-agent service is not running"
                return 1
            fi
        fi
    fi

    print_success "Installation verification passed"
}

# Function to show cluster connection info (for setting up worker nodes)
show_cluster_info() {
    if [ "$NODE_TYPE" != "server" ]; then
        print_error "Cluster info can only be shown on server nodes"
        return 1
    fi

    if ! command_exists k3s; then
        print_error "k3s is not installed"
        return 1
    fi

    local server_ip=$(get_ip)
    local cluster_token=""
    
    # Get cluster token
    if [ -f /var/lib/rancher/k3s/server/node-token ]; then
        cluster_token=$(cat /var/lib/rancher/k3s/server/node-token)
    else
        print_error "Could not find cluster token. Is k3s server running?"
        return 1
    fi

    echo ""
    print_success "📋 Cluster Connection Information"
    echo ""
    echo -e "${BLUE}Use this information to join worker nodes:${NC}"
    echo ""
    echo -e "${YELLOW}On each worker node, run:${NC}"
    echo ""
    echo "export K3S_URL=https://$server_ip:6443"
    echo "export K3S_TOKEN=$cluster_token"
    echo "export NODE_TYPE=agent"
    echo "curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash"
    echo ""
    echo -e "${BLUE}Or manually:${NC}"
    echo "export K3S_URL=https://$server_ip:6443"
    echo "export K3S_TOKEN=$cluster_token"
    echo "curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC=agent sh -"
    echo ""
}

# Function to get cluster token for worker setup
get_cluster_token() {
    if [ "$NODE_TYPE" != "server" ]; then
        print_error "Cluster token can only be retrieved on server nodes"
        return 1
    fi

    if [ -f /var/lib/rancher/k3s/server/node-token ]; then
        cat /var/lib/rancher/k3s/server/node-token
    else
        print_error "Could not find cluster token. Is k3s server running?"
        return 1
    fi
}

# Function to show installation summary
show_summary() {
    local server_ip=$(get_ip)
    ensure_kubeconfig_env
    local KUBECTL=$(get_kubectl)
    
    echo ""
    if [ "$NODE_TYPE" = "server" ]; then
        print_success "🎉 PipeOps Server Installation Complete!"
    else
        print_success "🎉 PipeOps Worker Node Installation Complete!"
    fi
    echo ""
    echo -e "${BLUE}Installation Information:${NC}"
    echo "  • Cluster Type: $CLUSTER_TYPE"
    echo "  • Node Type: $NODE_TYPE"
    echo "  • Server IP: $server_ip"
    
    # Show version info based on cluster type
    case "$CLUSTER_TYPE" in
        "k3s")
            if command_exists k3s; then
                echo "  • k3s Version: $(k3s --version 2>/dev/null | head -1 || echo 'unknown')"
            fi
            ;;
        "minikube")
            if command_exists minikube; then
                echo "  • minikube Version: $(minikube version --short 2>/dev/null || echo 'unknown')"
            fi
            ;;
        "k3d")
            if command_exists k3d; then
                echo "  • k3d Version: $(k3d version 2>/dev/null | head -1 || echo 'unknown')"
            fi
            ;;
        "kind")
            if command_exists kind; then
                echo "  • kind Version: $(kind version 2>/dev/null | head -1 || echo 'unknown')"
            fi
            ;;
    esac
    
    if [ "$NODE_TYPE" = "server" ]; then
        echo "  • Cluster Name: $CLUSTER_NAME"
        echo "  • Agent Namespace: $NAMESPACE"
    fi
    echo ""
    
    if [ "$NODE_TYPE" = "server" ]; then
        echo -e "${BLUE}Useful Commands:${NC}"
        echo "  • Check cluster status: $KUBECTL get nodes"
        echo "  • Check agent status: $KUBECTL get pods -n $NAMESPACE"
        echo "  • View agent logs: $KUBECTL logs -f deployment/pipeops-agent -n $NAMESPACE"
        
        case "$CLUSTER_TYPE" in
            "k3s")
                echo "  • Access kubeconfig: cat /etc/rancher/k3s/k3s.yaml"
                echo "  • Show cluster info: $0 cluster-info"
                ;;
            "minikube")
                echo "  • Access cluster: minikube kubectl -- <command>"
                echo "  • Dashboard: minikube dashboard"
                ;;
            "k3d")
                echo "  • Access cluster: kubectl <command>"
                echo "  • List clusters: k3d cluster list"
                ;;
            "kind")
                echo "  • Access cluster: kubectl <command>"
                echo "  • List clusters: kind get clusters"
                ;;
        esac
        
        echo ""
        echo -e "${YELLOW}Installed Components:${NC}"
        echo "  • PipeOps Agent"
        if [ "$INSTALL_MONITORING" = "true" ]; then
            echo "  • Prometheus (Monitoring)"
            echo "  • Loki (Log Aggregation)"
            echo "  • Grafana (Visualization)"
            echo "  • OpenCost (Cost Monitoring)"
        fi
        echo ""
        echo -e "${YELLOW}Next Steps:${NC}"
        echo "  1. The agent will automatically register with PipeOps"
        echo "  2. Check your PipeOps dashboard to verify the cluster connection"
        echo "  3. Access monitoring at: $KUBECTL port-forward -n monitoring svc/prometheus-grafana 3000:80"
        echo "  4. You can now deploy applications through PipeOps"
    else
        echo -e "${BLUE}Useful Commands:${NC}"
        if [ "$CLUSTER_TYPE" = "k3s" ]; then
            echo "  • Check node status: systemctl status k3s-agent"
            echo "  • View node logs: journalctl -u k3s-agent -f"
        fi
        echo ""
        echo -e "${YELLOW}Next Steps:${NC}"
        echo "  1. This worker node should now appear in your cluster"
        echo "  2. Check from the server node: $KUBECTL get nodes"
    fi
    echo ""
}

# Function to uninstall
uninstall_pipeops() {
    print_status "Uninstalling PipeOps agent and k3s..."
    
    # Remove agent
    if command_exists k3s; then
        k3s kubectl delete namespace "$NAMESPACE" --ignore-not-found=true
    fi
    
    # Uninstall k3s
    if [ -f /usr/local/bin/k3s-uninstall.sh ]; then
        /usr/local/bin/k3s-uninstall.sh
    fi
    
    print_success "Uninstallation complete"
}

# Function to update agent
update_agent() {
    print_status "Updating PipeOps agent..."
    
    if ! command_exists k3s; then
        print_error "k3s is not installed"
        exit 1
    fi
    
    # Update agent image
    k3s kubectl set image deployment/pipeops-agent agent="$AGENT_IMAGE" -n "$NAMESPACE"
    
    # Wait for rollout to complete
    k3s kubectl rollout status deployment/pipeops-agent -n "$NAMESPACE"
    
    print_success "Agent updated successfully"
}

# Main installation function
install_pipeops() {
    echo ""
    if [ "$NODE_TYPE" = "server" ]; then
        echo -e "${BLUE}╔══════════════════════════════════════════════════╗${NC}"
        echo -e "${BLUE}║     PipeOps Intelligent Cluster Installer        ║${NC}"
        echo -e "${BLUE}╚══════════════════════════════════════════════════╝${NC}"
    else
        echo -e "${BLUE}╔══════════════════════════════════════╗${NC}"
        echo -e "${BLUE}║     PipeOps Worker Node Installer    ║${NC}"
        echo -e "${BLUE}╚══════════════════════════════════════╝${NC}"
    fi
    echo ""
    
    check_requirements
    detect_and_set_cluster_type
    enforce_privilege_requirements
    install_cluster
    install_post_cluster_components
    create_agent_config
    install_monitoring_stack
    deploy_agent
    verify_installation
    show_summary
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [COMMAND]"
    echo ""
    echo "Commands:"
    echo "  install       Install Kubernetes cluster and PipeOps agent (default)"
    echo "  uninstall     Remove PipeOps agent and cluster"
    echo "  update        Update PipeOps agent to latest version"
    echo "  cluster-info  Show cluster connection information for worker nodes"
    echo "  help          Show this help message"
    echo ""
    echo "Environment Variables:"
    echo "  CLUSTER_TYPE        Cluster type: auto, k3s, minikube, k3d, kind (default: auto)"
    echo "  AUTO_DETECT         Enable auto-detection (default: true)"
    echo "  NODE_TYPE           server (default) or agent (worker)"
    echo "  PIPEOPS_TOKEN       PipeOps authentication token (required for server)"
    echo "  AGENT_TOKEN         Alias for PIPEOPS_TOKEN (backward compatibility)"
    echo "  CLUSTER_NAME        Cluster identifier (default: default-cluster)"
    echo "  K3S_URL             Master server URL (required for k3s worker nodes)"
    echo "  K3S_TOKEN           Cluster token (required for k3s worker nodes)"
    echo "  PIPEOPS_API_URL     PipeOps API URL (default: https://api.pipeops.sh)"
    echo "  INSTALL_MONITORING  Set to true to provision optional monitoring stack (default: false)"
    echo "  ALLOW_DEV_CLUSTERS_AS_ROOT  Set to true to bypass root safety checks for minikube/k3d/kind"
    echo ""
    echo "Cluster Type Selection:"
    echo "  auto       - Automatically detect best cluster type (default)"
    echo "  k3s        - Lightweight Kubernetes for production (VMs, bare metal)"
    echo "  minikube   - Local development cluster (macOS, development)"
    echo "  k3d        - k3s in Docker (fast, lightweight)"
    echo "  kind       - Kubernetes in Docker (CI/CD, testing)"
    echo ""
    echo "Examples:"
    echo "  # Install with auto-detection (recommended):"
    echo "  export PIPEOPS_TOKEN=your-token"
    echo "  $0"
    echo ""
    echo "  # Install with specific cluster type:"
    echo "  export PIPEOPS_TOKEN=your-token"
    echo "  export CLUSTER_TYPE=k3d"
    echo "  $0"
    echo ""
    echo "  # Install without auto-detection (force k3s):"
    echo "  export PIPEOPS_TOKEN=your-token"
    echo "  export AUTO_DETECT=false"
    echo "  export CLUSTER_TYPE=k3s"
    echo "  $0"
    echo ""
    echo "  # Install k3s worker node:"
    echo "  export NODE_TYPE=agent"
    echo "  export K3S_URL=https://master-ip:6443"
    echo "  export K3S_TOKEN=cluster-token"
    echo "  $0"
    echo ""
    echo "  # Show cluster info for worker setup:"
    echo "  $0 cluster-info"
    echo ""
}

# Main script execution
case "$1" in
    "uninstall")
        uninstall_pipeops
        ;;
    "update")
        update_agent
        ;;
    "cluster-info")
        show_cluster_info
        ;;
    "help"|"-h"|"--help")
        show_usage
        ;;
    "install"|"")
        install_pipeops
        ;;
    *)
        echo "Unknown command: $1"
        echo ""
        show_usage
        exit 1
        ;;
esac
