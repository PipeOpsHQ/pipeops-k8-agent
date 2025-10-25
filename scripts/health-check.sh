#!/usr/bin/env bash
#
# PipeOps Agent Health Check Script
# Performs comprehensive health checks on the PipeOps agent installation
#
# Usage: ./health-check.sh [--verbose]
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
VERBOSE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --verbose|-v)
            VERBOSE=true
            shift
            ;;
        --namespace|-n)
            NAMESPACE="$2"
            shift 2
            ;;
        --help|-h)
            cat << EOF
PipeOps Agent Health Check Script

Usage: $0 [OPTIONS]

Options:
    --verbose, -v          Enable verbose output
    --namespace, -n NAME   Namespace to check (default: pipeops-system)
    --help, -h            Show this help message

Examples:
    $0                    # Basic health check
    $0 --verbose          # Detailed health check
    $0 -n custom-ns       # Check in custom namespace

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

# Check counters
CHECKS_PASSED=0
CHECKS_FAILED=0
CHECKS_WARNING=0
CHECKS_TOTAL=0

# Print functions
print_header() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_check() {
    local status=$1
    local message=$2
    CHECKS_TOTAL=$((CHECKS_TOTAL + 1))
    
    case $status in
        pass|PASS)
            echo -e "  ${GREEN}✓${NC} $message"
            CHECKS_PASSED=$((CHECKS_PASSED + 1))
            ;;
        fail|FAIL)
            echo -e "  ${RED}✗${NC} $message"
            CHECKS_FAILED=$((CHECKS_FAILED + 1))
            ;;
        warn|WARN)
            echo -e "  ${YELLOW}⚠${NC} $message"
            CHECKS_WARNING=$((CHECKS_WARNING + 1))
            ;;
        info|INFO)
            echo -e "  ${BLUE}ℹ${NC} $message"
            ;;
    esac
}

verbose() {
    if [ "$VERBOSE" = true ]; then
        echo -e "    ${BLUE}→${NC} $1"
    fi
}

# Check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Detect kubectl command
if command_exists kubectl; then
    KUBECTL="kubectl"
elif command_exists k3s && k3s kubectl version >/dev/null 2>&1; then
    KUBECTL="k3s kubectl"
else
    echo -e "${RED}Error: kubectl not found${NC}"
    echo "Please install kubectl or k3s first"
    exit 1
fi

# Start health check
print_header "PipeOps Agent Health Check"
echo -e "Namespace: ${BLUE}$NAMESPACE${NC}"
echo -e "Time: $(date)"

# 1. Check k3s/Kubernetes
print_header "1. Kubernetes Cluster"

if command_exists k3s; then
    if systemctl is-active --quiet k3s 2>/dev/null; then
        print_check pass "k3s service is running"
        verbose "Service status: active"
    else
        print_check fail "k3s service is not running"
        verbose "Try: sudo systemctl start k3s"
    fi
else
    print_check info "k3s not installed (using external Kubernetes)"
fi

# Check cluster connectivity
if $KUBECTL cluster-info >/dev/null 2>&1; then
    print_check pass "Kubernetes API is accessible"
    if [ "$VERBOSE" = true ]; then
        KUBE_VERSION=$($KUBECTL version --short 2>/dev/null | grep "Server Version" || echo "unknown")
        verbose "$KUBE_VERSION"
    fi
else
    print_check fail "Cannot connect to Kubernetes API"
    verbose "Check your kubeconfig or k3s installation"
fi

# Check nodes
NODE_COUNT=$($KUBECTL get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$NODE_COUNT" -gt 0 ]; then
    print_check pass "Cluster has $NODE_COUNT node(s)"
    if [ "$VERBOSE" = true ]; then
        $KUBECTL get nodes --no-headers | while read -r line; do
            verbose "Node: $line"
        done
    fi
else
    print_check fail "No nodes found in cluster"
fi

# Check node health
READY_NODES=$($KUBECTL get nodes --no-headers 2>/dev/null | grep -c " Ready " || echo "0")
if [ "$READY_NODES" -eq "$NODE_COUNT" ]; then
    print_check pass "All nodes are Ready"
else
    print_check warn "$READY_NODES/$NODE_COUNT nodes are Ready"
fi

# 2. Check Namespace
print_header "2. PipeOps Namespace"

if $KUBECTL get namespace "$NAMESPACE" >/dev/null 2>&1; then
    print_check pass "Namespace '$NAMESPACE' exists"
else
    print_check fail "Namespace '$NAMESPACE' not found"
    echo ""
    echo "The namespace doesn't exist. Agent is not installed."
    exit 1
fi

# 3. Check RBAC Resources
print_header "3. RBAC Configuration"

if $KUBECTL get serviceaccount pipeops-agent -n "$NAMESPACE" >/dev/null 2>&1; then
    print_check pass "ServiceAccount exists"
else
    print_check fail "ServiceAccount not found"
fi

if $KUBECTL get clusterrole pipeops-agent >/dev/null 2>&1; then
    print_check pass "ClusterRole exists"
else
    print_check fail "ClusterRole not found"
fi

if $KUBECTL get clusterrolebinding pipeops-agent >/dev/null 2>&1; then
    print_check pass "ClusterRoleBinding exists"
else
    print_check fail "ClusterRoleBinding not found"
fi

# 4. Check Secret
print_header "4. Configuration Secret"

if $KUBECTL get secret pipeops-agent-config -n "$NAMESPACE" >/dev/null 2>&1; then
    print_check pass "Secret 'pipeops-agent-config' exists"
    
    # Check secret keys
    if [ "$VERBOSE" = true ]; then
        SECRET_KEYS=$($KUBECTL get secret pipeops-agent-config -n "$NAMESPACE" -o jsonpath='{.data}' 2>/dev/null | grep -o '"[^"]*"' | tr -d '"' | head -20)
        echo "$SECRET_KEYS" | while read -r key; do
            if [ -n "$key" ]; then
                verbose "Key present: $key"
            fi
        done
    fi
    
    # Verify required keys
    for key in PIPEOPS_API_URL PIPEOPS_TOKEN PIPEOPS_CLUSTER_NAME; do
        if $KUBECTL get secret pipeops-agent-config -n "$NAMESPACE" -o jsonpath="{.data.$key}" 2>/dev/null | grep -q .; then
            print_check pass "Secret has '$key'"
        else
            print_check fail "Secret missing '$key'"
        fi
    done
else
    print_check fail "Secret 'pipeops-agent-config' not found"
fi

# 5. Check Deployment
print_header "5. Agent Deployment"

if $KUBECTL get deployment pipeops-agent -n "$NAMESPACE" >/dev/null 2>&1; then
    print_check pass "Deployment exists"
    
    # Check replicas
    DESIRED=$($KUBECTL get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.spec.replicas}' 2>/dev/null)
    READY=$($KUBECTL get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
    
    if [ "$READY" = "$DESIRED" ] && [ "$READY" != "" ]; then
        print_check pass "Deployment is ready ($READY/$DESIRED replicas)"
    elif [ "$READY" = "" ]; then
        print_check fail "No replicas are ready (0/$DESIRED)"
    else
        print_check warn "Deployment not fully ready ($READY/$DESIRED replicas)"
    fi
    
    # Check image
    if [ "$VERBOSE" = true ]; then
        IMAGE=$($KUBECTL get deployment pipeops-agent -n "$NAMESPACE" -o jsonpath='{.spec.template.spec.containers[0].image}')
        verbose "Image: $IMAGE"
    fi
else
    print_check fail "Deployment not found"
fi

# 6. Check Pods
print_header "6. Agent Pod Status"

POD_NAME=$($KUBECTL get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

if [ -n "$POD_NAME" ]; then
    print_check pass "Pod found: $POD_NAME"
    
    # Check pod status
    POD_STATUS=$($KUBECTL get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}' 2>/dev/null)
    
    case $POD_STATUS in
        Running)
            print_check pass "Pod is Running"
            ;;
        Pending)
            print_check warn "Pod is Pending"
            if [ "$VERBOSE" = true ]; then
                $KUBECTL describe pod "$POD_NAME" -n "$NAMESPACE" | grep -A 5 "Events:" | tail -5
            fi
            ;;
        *)
            print_check fail "Pod is in $POD_STATUS state"
            ;;
    esac
    
    # Check container status
    CONTAINER_READY=$($KUBECTL get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].ready}' 2>/dev/null)
    if [ "$CONTAINER_READY" = "true" ]; then
        print_check pass "Container is ready"
    else
        print_check fail "Container is not ready"
        
        # Check for errors
        if [ "$VERBOSE" = true ]; then
            STATE=$($KUBECTL get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].state}' 2>/dev/null)
            verbose "Container state: $STATE"
        fi
    fi
    
    # Check restart count
    RESTART_COUNT=$($KUBECTL get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].restartCount}' 2>/dev/null)
    if [ "$RESTART_COUNT" -eq 0 ]; then
        print_check pass "No restarts"
    elif [ "$RESTART_COUNT" -lt 5 ]; then
        print_check warn "Container has restarted $RESTART_COUNT time(s)"
    else
        print_check fail "Container has restarted $RESTART_COUNT time(s)"
    fi
    
    # Check resource usage (if metrics-server is available)
    if $KUBECTL top pod "$POD_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
        if [ "$VERBOSE" = true ]; then
            METRICS=$($KUBECTL top pod "$POD_NAME" -n "$NAMESPACE" --no-headers 2>/dev/null)
            verbose "Resources: $METRICS"
        fi
    fi
else
    print_check fail "No agent pod found"
fi

# 7. Check Service
print_header "7. Agent Service"

if $KUBECTL get service pipeops-agent -n "$NAMESPACE" >/dev/null 2>&1; then
    print_check pass "Service exists"
    
    if [ "$VERBOSE" = true ]; then
        SERVICE_IP=$($KUBECTL get service pipeops-agent -n "$NAMESPACE" -o jsonpath='{.spec.clusterIP}')
        SERVICE_PORT=$($KUBECTL get service pipeops-agent -n "$NAMESPACE" -o jsonpath='{.spec.ports[0].port}')
        verbose "ClusterIP: $SERVICE_IP:$SERVICE_PORT"
    fi
else
    print_check fail "Service not found"
fi

# 8. Check API Endpoints
print_header "8. API Endpoints"

if [ -n "$POD_NAME" ] && [ "$POD_STATUS" = "Running" ]; then
    # Test /health endpoint
    if $KUBECTL exec "$POD_NAME" -n "$NAMESPACE" -- wget -q -O- http://host.docker.internal:8080/health >/dev/null 2>&1; then
        print_check pass "/health endpoint responding"
    else
        print_check fail "/health endpoint not responding"
    fi
    
    # Test /ready endpoint
    if $KUBECTL exec "$POD_NAME" -n "$NAMESPACE" -- wget -q -O- http://host.docker.internal:8080/ready >/dev/null 2>&1; then
        print_check pass "/ready endpoint responding"
    else
        print_check fail "/ready endpoint not responding"
    fi
    
    # Test /version endpoint (if available)
    if $KUBECTL exec "$POD_NAME" -n "$NAMESPACE" -- wget -q -O- http://host.docker.internal:8080/version >/dev/null 2>&1; then
        print_check pass "/version endpoint responding"
        if [ "$VERBOSE" = true ]; then
            VERSION=$($KUBECTL exec "$POD_NAME" -n "$NAMESPACE" -- wget -q -O- http://host.docker.internal:8080/version 2>/dev/null)
            verbose "$VERSION"
        fi
    else
        print_check info "/version endpoint not available"
    fi
else
    print_check info "Skipping endpoint checks (pod not running)"
fi

# 9. Check Logs
print_header "9. Recent Logs"

if [ -n "$POD_NAME" ]; then
    # Check for errors in logs
    ERROR_COUNT=$($KUBECTL logs "$POD_NAME" -n "$NAMESPACE" --tail=100 2>/dev/null | grep -c "error\|Error\|ERROR" || echo "0")
    
    if [ "$ERROR_COUNT" -eq 0 ]; then
        print_check pass "No errors in recent logs"
    else
        print_check warn "Found $ERROR_COUNT error(s) in recent logs"
    fi
    
    if [ "$VERBOSE" = true ]; then
        echo ""
        echo "  Last 10 log lines:"
        $KUBECTL logs "$POD_NAME" -n "$NAMESPACE" --tail=10 2>/dev/null | while read -r line; do
            verbose "$line"
        done
    fi
else
    print_check info "No pod to check logs"
fi

# 10. Check Control Plane Connectivity
print_header "10. Control Plane Connectivity"

if [ -n "$POD_NAME" ] && [ "$POD_STATUS" = "Running" ]; then
    # Check if we can resolve PipeOps API
    API_URL=$($KUBECTL get secret pipeops-agent-config -n "$NAMESPACE" -o jsonpath='{.data.PIPEOPS_API_URL}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
    
    if [ -n "$API_URL" ]; then
        verbose "API URL: $API_URL"
        
        # Extract hostname from URL
        HOSTNAME=$(echo "$API_URL" | sed -e 's|^[^/]*//||' -e 's|/.*$||' -e 's|:.*$||')
        
        # Test DNS resolution
        if $KUBECTL exec "$POD_NAME" -n "$NAMESPACE" -- nslookup "$HOSTNAME" >/dev/null 2>&1 || \
           $KUBECTL exec "$POD_NAME" -n "$NAMESPACE" -- getent hosts "$HOSTNAME" >/dev/null 2>&1; then
            print_check pass "Can resolve control plane hostname"
        else
            print_check fail "Cannot resolve control plane hostname: $HOSTNAME"
        fi
        
        # Check for heartbeat/connection logs
        if $KUBECTL logs "$POD_NAME" -n "$NAMESPACE" --tail=100 2>/dev/null | grep -q "Starting PipeOps Agent\|Control plane client initialized"; then
            print_check pass "Agent initialized successfully"
        else
            print_check warn "No initialization logs found"
        fi
    else
        print_check info "Cannot read API URL from secret"
    fi
else
    print_check info "Skipping connectivity checks (pod not running)"
fi

# Summary
print_header "Health Check Summary"

echo ""
echo -e "  Total Checks:  ${BLUE}$CHECKS_TOTAL${NC}"
echo -e "  Passed:        ${GREEN}$CHECKS_PASSED${NC}"
echo -e "  Warnings:      ${YELLOW}$CHECKS_WARNING${NC}"
echo -e "  Failed:        ${RED}$CHECKS_FAILED${NC}"
echo ""

# Overall status
if [ "$CHECKS_FAILED" -eq 0 ]; then
    if [ "$CHECKS_WARNING" -eq 0 ]; then
        echo -e "${GREEN}✓ All checks passed! Agent is healthy.${NC}"
        EXIT_CODE=0
    else
        echo -e "${YELLOW}⚠ Agent is running but has warnings.${NC}"
        EXIT_CODE=0
    fi
else
    echo -e "${RED}✗ Some checks failed. Agent may not be functioning correctly.${NC}"
    EXIT_CODE=1
fi

# Helpful commands
if [ "$CHECKS_FAILED" -gt 0 ] || [ "$CHECKS_WARNING" -gt 0 ]; then
    echo ""
    echo -e "${BLUE}Troubleshooting commands:${NC}"
    echo ""
    echo "  # View pod details"
    echo "  $KUBECTL describe pod -n $NAMESPACE -l app.kubernetes.io/name=pipeops-agent"
    echo ""
    echo "  # View logs"
    echo "  $KUBECTL logs -n $NAMESPACE deployment/pipeops-agent --tail=50 --follow"
    echo ""
    echo "  # Check events"
    echo "  $KUBECTL get events -n $NAMESPACE --sort-by='.lastTimestamp'"
    echo ""
fi

echo ""
exit $EXIT_CODE
