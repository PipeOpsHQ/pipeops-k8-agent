#!/bin/bash

# PipeOps VM Agent Installation Script
# This script installs k3s and deploys the PipeOps agent for secure cluster management

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PIPEOPS_API_URL="${PIPEOPS_API_URL:-https://api.pipeops.io}"
AGENT_TOKEN="${AGENT_TOKEN:-}"
CLUSTER_NAME="${CLUSTER_NAME:-default-cluster}"
K3S_VERSION="${K3S_VERSION:-v1.28.3+k3s2}"
AGENT_IMAGE="${AGENT_IMAGE:-ghcr.io/pipeopshq/pipeops-k8-agent:latest}"
NAMESPACE="${NAMESPACE:-pipeops-system}"

# Worker node configuration
K3S_URL="${K3S_URL:-}"                    # Master server URL for worker nodes
K3S_TOKEN="${K3S_TOKEN:-}"                # Cluster token for joining
NODE_TYPE="${NODE_TYPE:-server}"          # server or agent (worker)
MASTER_IP="${MASTER_IP:-}"               # Master node IP for worker join

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

# Function to check system requirements
check_requirements() {
    print_status "Checking system requirements..."
    
    # Check if running as root
    if [ "$(id -u)" != "0" ]; then
        print_error "This script must be run as root"
        exit 1
    fi

    # Check if running on Linux
    if [ "$(uname)" = "Darwin" ]; then
        print_error "This script must be run on Linux"
        exit 1
    fi

    # Check if running inside a Docker container
    if [ -f /.dockerenv ]; then
        print_error "This script should not be run inside a Docker container"
        exit 1
    fi

    # Check available memory (k3s needs at least 512MB)
    available_memory=$(free -m | awk 'NR==2{printf "%.0f", $7}')
    if [ "$available_memory" -lt 512 ]; then
        print_warning "Available memory is ${available_memory}MB. k3s requires at least 512MB"
    fi

    # Check disk space (need at least 2GB)
    available_disk=$(df / | awk 'NR==2{print $4}')
    available_disk_gb=$((available_disk / 1024 / 1024))
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

# Function to install k3s
install_k3s() {
    print_status "Installing k3s as $NODE_TYPE..."
    
    if command_exists k3s; then
        print_warning "k3s is already installed"
        return 0
    fi

    # Set k3s installation options
    export INSTALL_K3S_VERSION="$K3S_VERSION"
    export INSTALL_K3S_EXEC=""
    
    # Check if running in Proxmox LXC container
    if is_proxmox_lxc; then
        print_warning "Detected Proxmox LXC container environment!"
        print_warning "Configuring k3s for LXC compatibility..."
        
        if [ "$NODE_TYPE" = "server" ]; then
            export INSTALL_K3S_EXEC="server --disable=traefik --disable=servicelb --flannel-backend=host-gw --kube-proxy-arg=conntrack-max-per-core=0"
        else
            export INSTALL_K3S_EXEC="agent --flannel-backend=host-gw --kube-proxy-arg=conntrack-max-per-core=0"
        fi
    else
        # Standard installation
        if [ "$NODE_TYPE" = "server" ]; then
            export INSTALL_K3S_EXEC="server --disable=traefik"
        else
            export INSTALL_K3S_EXEC="agent"
        fi
    fi

    # Configure for worker node joining
    if [ "$NODE_TYPE" = "agent" ]; then
        if [ -z "$K3S_URL" ] || [ -z "$K3S_TOKEN" ]; then
            print_error "For worker nodes, K3S_URL and K3S_TOKEN must be set"
            print_error "Example:"
            print_error "  export K3S_URL=https://master-ip:6443"
            print_error "  export K3S_TOKEN=your-cluster-token"
            exit 1
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
    
    if [ "$NODE_TYPE" = "server" ]; then
        # For server nodes, wait for kubectl to be available
        while [ $count -lt $timeout ]; do
            if k3s kubectl get nodes >/dev/null 2>&1; then
                break
            fi
            sleep 2
            count=$((count + 2))
        done
    else
        # For worker nodes, just wait for k3s service to start
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
        exit 1
    fi

    # Set up kubectl alias for server nodes
    if [ "$NODE_TYPE" = "server" ] && ! command_exists kubectl; then
        echo 'alias kubectl="k3s kubectl"' >> ~/.bashrc
        alias kubectl="k3s kubectl"
    fi

    print_success "k3s installed successfully as $NODE_TYPE"
}

# Function to create agent configuration
create_agent_config() {
    # Only deploy agent on server nodes
    if [ "$NODE_TYPE" = "agent" ]; then
        print_status "Skipping agent deployment on worker node"
        return 0
    fi

    print_status "Creating agent configuration..."
    
    if [ -z "$AGENT_TOKEN" ]; then
        print_error "AGENT_TOKEN environment variable is required"
        print_error "Please set your PipeOps agent token:"
        print_error "export AGENT_TOKEN=your-token-here"
        exit 1
    fi

    # Create namespace
    k3s kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | k3s kubectl apply -f -

    # Create secret with configuration
    k3s kubectl create secret generic pipeops-agent-config \
        --namespace="$NAMESPACE" \
        --from-literal=PIPEOPS_API_URL="$PIPEOPS_API_URL" \
        --from-literal=PIPEOPS_TOKEN="$AGENT_TOKEN" \
        --from-literal=PIPEOPS_CLUSTER_NAME="$CLUSTER_NAME" \
        --dry-run=client -o yaml | k3s kubectl apply -f -

    print_success "Agent configuration created"
}

# Function to deploy the agent
deploy_agent() {
    # Only deploy agent on server nodes
    if [ "$NODE_TYPE" = "agent" ]; then
        print_status "Skipping agent deployment on worker node"
        return 0
    fi

    print_status "Deploying PipeOps agent..."
    
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
- apiGroups: [""]
  resources: ["nodes", "nodes/status", "namespaces", "pods", "pods/log", "pods/status", "services", "endpoints", "configmaps", "secrets", "events"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments", "deployments/status", "deployments/scale", "replicasets", "replicasets/status", "daemonsets", "statefulsets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["extensions"]
  resources: ["deployments", "deployments/status", "deployments/scale", "replicasets", "ingresses"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["networking.k8s.io"]
  resources: ["ingresses", "networkpolicies"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["batch"]
  resources: ["jobs", "cronjobs"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["pods/exec", "pods/portforward"]
  verbs: ["create"]
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
    k3s kubectl apply -f /tmp/pipeops-agent.yaml

    # Wait for deployment to be ready
    print_status "Waiting for agent to be ready..."
    k3s kubectl wait --for=condition=available --timeout=300s deployment/pipeops-agent -n "$NAMESPACE"

    # Clean up temporary file
    rm -f /tmp/pipeops-agent.yaml

    print_success "PipeOps agent deployed successfully"
}

# Function to verify installation
verify_installation() {
    print_status "Verifying installation..."
    
    if [ "$NODE_TYPE" = "server" ]; then
        # Check k3s status on server
        if ! k3s kubectl get nodes >/dev/null 2>&1; then
            print_error "k3s is not running properly"
            return 1
        fi

        # Check agent status
        if ! k3s kubectl get deployment pipeops-agent -n "$NAMESPACE" >/dev/null 2>&1; then
            print_error "PipeOps agent deployment not found"
            return 1
        fi

        local replicas_ready=$(k3s kubectl get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}')
        if [ "$replicas_ready" != "1" ]; then
            print_error "PipeOps agent is not ready"
            return 1
        fi
    else
        # Check k3s agent status on worker
        if ! systemctl is-active --quiet k3s-agent; then
            print_error "k3s-agent service is not running"
            return 1
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
    echo "curl -sSL https://get.pipeops.io/agent | bash"
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
    
    echo ""
    if [ "$NODE_TYPE" = "server" ]; then
        print_success "🎉 PipeOps Server Installation Complete!"
    else
        print_success "🎉 PipeOps Worker Node Installation Complete!"
    fi
    echo ""
    echo -e "${BLUE}Node Information:${NC}"
    echo "  • Node Type: $NODE_TYPE"
    echo "  • Server IP: $server_ip"
    echo "  • k3s Version: $(k3s --version | head -1)"
    if [ "$NODE_TYPE" = "server" ]; then
        echo "  • Cluster Name: $CLUSTER_NAME"
        echo "  • Agent Namespace: $NAMESPACE"
    fi
    echo ""
    
    if [ "$NODE_TYPE" = "server" ]; then
        echo -e "${BLUE}Useful Commands:${NC}"
        echo "  • Check cluster status: k3s kubectl get nodes"
        echo "  • Check agent status: k3s kubectl get pods -n $NAMESPACE"
        echo "  • View agent logs: k3s kubectl logs -f deployment/pipeops-agent -n $NAMESPACE"
        echo "  • Access kubeconfig: cat /etc/rancher/k3s/k3s.yaml"
        echo "  • Show cluster info: $0 cluster-info"
        echo ""
        echo -e "${YELLOW}Next Steps:${NC}"
        echo "  1. The agent will automatically register with PipeOps"
        echo "  2. Check your PipeOps dashboard to verify the cluster connection"
        echo "  3. To add worker nodes, run: $0 cluster-info"
        echo "  4. You can now deploy applications through PipeOps"
    else
        echo -e "${BLUE}Useful Commands:${NC}"
        echo "  • Check node status: systemctl status k3s-agent"
        echo "  • View node logs: journalctl -u k3s-agent -f"
        echo ""
        echo -e "${YELLOW}Next Steps:${NC}"
        echo "  1. This worker node should now appear in your cluster"
        echo "  2. Check from the server node: k3s kubectl get nodes"
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
        echo -e "${BLUE}╔══════════════════════════════════════╗${NC}"
        echo -e "${BLUE}║     PipeOps Server Node Installer    ║${NC}"
        echo -e "${BLUE}╚══════════════════════════════════════╝${NC}"
    else
        echo -e "${BLUE}╔══════════════════════════════════════╗${NC}"
        echo -e "${BLUE}║     PipeOps Worker Node Installer    ║${NC}"
        echo -e "${BLUE}╚══════════════════════════════════════╝${NC}"
    fi
    echo ""
    
    check_requirements
    install_k3s
    create_agent_config
    deploy_agent
    verify_installation
    show_summary
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [COMMAND]"
    echo ""
    echo "Commands:"
    echo "  install       Install k3s and PipeOps agent (default)"
    echo "  uninstall     Remove PipeOps agent and k3s"
    echo "  update        Update PipeOps agent to latest version"
    echo "  cluster-info  Show cluster connection information for worker nodes"
    echo "  help          Show this help message"
    echo ""
    echo "Environment Variables:"
    echo "  NODE_TYPE           server (default) or agent (worker)"
    echo "  AGENT_TOKEN         PipeOps authentication token (required for server)"
    echo "  CLUSTER_NAME        Cluster identifier (default: default-cluster)"
    echo "  K3S_URL             Master server URL (required for worker nodes)"
    echo "  K3S_TOKEN           Cluster token (required for worker nodes)"
    echo "  PIPEOPS_API_URL     PipeOps API URL (default: https://api.pipeops.io)"
    echo ""
    echo "Examples:"
    echo "  # Install server node:"
    echo "  export AGENT_TOKEN=your-token"
    echo "  $0"
    echo ""
    echo "  # Install worker node:"
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
