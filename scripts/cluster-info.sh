#!/bin/bash

#######################################
# PipeOps Cluster Info Script
# Display cluster connection information
#######################################

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Default values
NAMESPACE="pipeops-system"
SHOW_TOKEN="false"
OUTPUT_FORMAT="text"

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
    echo -e "${BOLD}${CYAN}========================================${NC}"
    echo -e "${BOLD}${CYAN}$1${NC}"
    echo -e "${BOLD}${CYAN}========================================${NC}"
    echo ""
}

print_section() {
    echo ""
    echo -e "${BOLD}$1${NC}"
    echo "----------------------------------------"
}

#######################################
# Show usage
#######################################
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Display PipeOps cluster connection information.

OPTIONS:
    -n, --namespace NAME    Namespace where agent is installed (default: pipeops-system)
    -t, --show-token        Show cluster token (default: false)
    -f, --format FORMAT     Output format: text, json, yaml (default: text)
    -h, --help              Show this help message

EXAMPLES:
    # Display cluster info
    $0

    # Display with token visible
    $0 --show-token

    # Output as JSON
    $0 --format json

EOF
    exit 0
}

#######################################
# Parse command line arguments
#######################################
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -n|--namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            -t|--show-token)
                SHOW_TOKEN="true"
                shift
                ;;
            -f|--format)
                OUTPUT_FORMAT="$2"
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
        exit 1
    fi
}

#######################################
# Check if k3s is installed
#######################################
check_k3s() {
    if ! command -v k3s &> /dev/null; then
        return 1
    fi
    return 0
}

#######################################
# Get master node IP
#######################################
get_master_ip() {
    # Try multiple methods to get the master IP
    
    # Method 1: Get from kubectl cluster-info
    local master_ip
    master_ip=$(kubectl cluster-info | grep -i "control plane\|kubernetes master" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    
    if [ -z "$master_ip" ]; then
        # Method 2: Get from node with control-plane role
        master_ip=$(kubectl get nodes -o jsonpath='{.items[?(@.metadata.labels.node-role\.kubernetes\.io/control-plane)].status.addresses[?(@.type=="InternalIP")].address}' | awk '{print $1}')
    fi
    
    if [ -z "$master_ip" ]; then
        # Method 3: Get from node with master role
        master_ip=$(kubectl get nodes -o jsonpath='{.items[?(@.metadata.labels.node-role\.kubernetes\.io/master)].status.addresses[?(@.type=="InternalIP")].address}' | awk '{print $1}')
    fi
    
    if [ -z "$master_ip" ]; then
        # Method 4: Get first node IP
        master_ip=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
    fi
    
    echo "$master_ip"
}

#######################################
# Get cluster token
#######################################
get_cluster_token() {
    local token=""
    
    # Try to get token from k3s
    if check_k3s; then
        if [ -f /var/lib/rancher/k3s/server/node-token ]; then
            token=$(sudo cat /var/lib/rancher/k3s/server/node-token 2>/dev/null || cat /var/lib/rancher/k3s/server/node-token 2>/dev/null)
        fi
    fi
    
    if [ -z "$token" ]; then
        token="<token-not-accessible>"
    fi
    
    echo "$token"
}

#######################################
# Get kubeconfig location
#######################################
get_kubeconfig_location() {
    if [ -n "$KUBECONFIG" ]; then
        echo "$KUBECONFIG"
    elif [ -f ~/.kube/config ]; then
        echo "~/.kube/config"
    elif [ -f /etc/rancher/k3s/k3s.yaml ]; then
        echo "/etc/rancher/k3s/k3s.yaml"
    else
        echo "<not-found>"
    fi
}

#######################################
# Get agent status
#######################################
get_agent_status() {
    local status="Unknown"
    local ready="0/0"
    local age="N/A"
    
    if kubectl get namespace "$NAMESPACE" &> /dev/null; then
        if kubectl get deployment pipeops-agent -n "$NAMESPACE" &> /dev/null; then
            ready=$(kubectl get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}/{.spec.replicas}' 2>/dev/null || echo "0/0")
            age=$(kubectl get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.metadata.creationTimestamp}' 2>/dev/null | xargs -I {} date -d {} +%s 2>/dev/null || echo "N/A")
            
            if [ "$age" != "N/A" ]; then
                local now
                now=$(date +%s)
                local diff=$((now - age))
                local days=$((diff / 86400))
                local hours=$(((diff % 86400) / 3600))
                local minutes=$(((diff % 3600) / 60))
                
                if [ $days -gt 0 ]; then
                    age="${days}d${hours}h"
                elif [ $hours -gt 0 ]; then
                    age="${hours}h${minutes}m"
                else
                    age="${minutes}m"
                fi
            fi
            
            local ready_count
            ready_count=$(echo "$ready" | cut -d'/' -f1)
            local desired_count
            desired_count=$(echo "$ready" | cut -d'/' -f2)
            
            if [ "$ready_count" = "$desired_count" ] && [ "$ready_count" != "0" ]; then
                status="Running"
            elif [ "$ready_count" = "0" ]; then
                status="Not Ready"
            else
                status="Degraded"
            fi
        else
            status="Not Installed"
        fi
    else
        status="Not Installed"
    fi
    
    echo "$status|$ready|$age"
}

#######################################
# Get cluster version
#######################################
get_cluster_version() {
    kubectl version --short 2>/dev/null | grep "Server Version" | awk '{print $3}' || echo "Unknown"
}

#######################################
# Get node count
#######################################
get_node_count() {
    kubectl get nodes --no-headers 2>/dev/null | wc -l | xargs
}

#######################################
# Display info in text format
#######################################
display_text_format() {
    local master_ip=$1
    local cluster_token=$2
    local kubeconfig=$3
    local agent_status_info=$4
    local cluster_version=$5
    local node_count=$6
    
    local agent_status
    agent_status=$(echo "$agent_status_info" | cut -d'|' -f1)
    local agent_ready
    agent_ready=$(echo "$agent_status_info" | cut -d'|' -f2)
    local agent_age
    agent_age=$(echo "$agent_status_info" | cut -d'|' -f3)
    
    print_header "PipeOps Cluster Information"
    
    print_section "Cluster Details"
    echo "Master Node IP:      $master_ip"
    echo "Kubernetes Version:  $cluster_version"
    echo "Total Nodes:         $node_count"
    echo "Kubeconfig Location: $kubeconfig"
    
    print_section "Cluster Token"
    if [ "$SHOW_TOKEN" = "true" ]; then
        echo "$cluster_token"
    else
        echo "<hidden> (use --show-token to display)"
    fi
    
    print_section "PipeOps Agent Status"
    
    # Color code the status
    local status_display
    case "$agent_status" in
        "Running")
            status_display="${GREEN}${agent_status}${NC}"
            ;;
        "Degraded")
            status_display="${YELLOW}${agent_status}${NC}"
            ;;
        "Not Ready"|"Not Installed")
            status_display="${RED}${agent_status}${NC}"
            ;;
        *)
            status_display="$agent_status"
            ;;
    esac
    
    echo -e "Status:              $status_display"
    echo "Ready:               $agent_ready"
    echo "Age:                 $agent_age"
    echo "Namespace:           $NAMESPACE"
    
    if [ "$agent_status" = "Running" ]; then
        print_section "Agent Pods"
        kubectl get pods -n "$NAMESPACE" -l app=pipeops-agent 2>/dev/null || echo "No pods found"
    fi
    
    print_section "Worker Node Join Command"
    echo ""
    echo "To add a worker node to this cluster, run the following command on the worker node:"
    echo ""
    if [ "$SHOW_TOKEN" = "true" ]; then
        echo -e "${GREEN}curl -sfL https://get.k3s.io | K3S_URL=https://${master_ip}:6443 K3S_TOKEN=${cluster_token} sh -${NC}"
    else
        echo -e "${GREEN}curl -sfL https://get.k3s.io | K3S_URL=https://${master_ip}:6443 K3S_TOKEN=<cluster-token> sh -${NC}"
        echo ""
        echo "(Use --show-token to see the actual token)"
    fi
    
    print_section "Quick Access Commands"
    echo ""
    echo "# View agent logs"
    echo "  kubectl logs -n $NAMESPACE -l app=pipeops-agent -f"
    echo ""
    echo "# Check agent health"
    echo "  kubectl get pods -n $NAMESPACE -l app=pipeops-agent"
    echo ""
    echo "# View agent configuration"
    echo "  kubectl get configmap pipeops-agent-config -n $NAMESPACE -o yaml"
    echo ""
    echo "# Port forward to agent (access on localhost:8080)"
    echo "  kubectl port-forward -n $NAMESPACE svc/pipeops-agent 8080:8080"
    echo ""
}

#######################################
# Display info in JSON format
#######################################
display_json_format() {
    local master_ip=$1
    local cluster_token=$2
    local kubeconfig=$3
    local agent_status_info=$4
    local cluster_version=$5
    local node_count=$6
    
    local agent_status
    agent_status=$(echo "$agent_status_info" | cut -d'|' -f1)
    local agent_ready
    agent_ready=$(echo "$agent_status_info" | cut -d'|' -f2)
    local agent_age
    agent_age=$(echo "$agent_status_info" | cut -d'|' -f3)
    
    local token_value="<hidden>"
    if [ "$SHOW_TOKEN" = "true" ]; then
        token_value="$cluster_token"
    fi
    
    cat << EOF
{
  "cluster": {
    "masterNodeIP": "$master_ip",
    "kubernetesVersion": "$cluster_version",
    "totalNodes": $node_count,
    "kubeconfigLocation": "$kubeconfig",
    "token": "$token_value"
  },
  "agent": {
    "status": "$agent_status",
    "ready": "$agent_ready",
    "age": "$agent_age",
    "namespace": "$NAMESPACE"
  },
  "joinCommand": "curl -sfL https://get.k3s.io | K3S_URL=https://${master_ip}:6443 K3S_TOKEN=${token_value} sh -"
}
EOF
}

#######################################
# Display info in YAML format
#######################################
display_yaml_format() {
    local master_ip=$1
    local cluster_token=$2
    local kubeconfig=$3
    local agent_status_info=$4
    local cluster_version=$5
    local node_count=$6
    
    local agent_status
    agent_status=$(echo "$agent_status_info" | cut -d'|' -f1)
    local agent_ready
    agent_ready=$(echo "$agent_status_info" | cut -d'|' -f2)
    local agent_age
    agent_age=$(echo "$agent_status_info" | cut -d'|' -f3)
    
    local token_value="<hidden>"
    if [ "$SHOW_TOKEN" = "true" ]; then
        token_value="$cluster_token"
    fi
    
    cat << EOF
cluster:
  masterNodeIP: $master_ip
  kubernetesVersion: $cluster_version
  totalNodes: $node_count
  kubeconfigLocation: $kubeconfig
  token: $token_value

agent:
  status: $agent_status
  ready: $agent_ready
  age: $agent_age
  namespace: $NAMESPACE

joinCommand: "curl -sfL https://get.k3s.io | K3S_URL=https://${master_ip}:6443 K3S_TOKEN=${token_value} sh -"
EOF
}

#######################################
# Main execution
#######################################
main() {
    parse_args "$@"
    
    # Check prerequisites
    check_kubectl
    
    # Gather information
    local master_ip
    master_ip=$(get_master_ip)
    
    local cluster_token
    cluster_token=$(get_cluster_token)
    
    local kubeconfig
    kubeconfig=$(get_kubeconfig_location)
    
    local agent_status_info
    agent_status_info=$(get_agent_status)
    
    local cluster_version
    cluster_version=$(get_cluster_version)
    
    local node_count
    node_count=$(get_node_count)
    
    # Display information based on format
    case "$OUTPUT_FORMAT" in
        "json")
            display_json_format "$master_ip" "$cluster_token" "$kubeconfig" "$agent_status_info" "$cluster_version" "$node_count"
            ;;
        "yaml")
            display_yaml_format "$master_ip" "$cluster_token" "$kubeconfig" "$agent_status_info" "$cluster_version" "$node_count"
            ;;
        "text"|*)
            display_text_format "$master_ip" "$cluster_token" "$kubeconfig" "$agent_status_info" "$cluster_version" "$node_count"
            ;;
    esac
}

# Run main function
main "$@"
