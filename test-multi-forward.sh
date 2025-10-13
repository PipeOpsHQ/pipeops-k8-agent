#!/bin/bash

# Test script for Portainer-style multi-port forwarding
# This script tests the new tunnel implementation with multiple port forwards

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}ℹ${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

cleanup() {
    log_info "Cleaning up processes..."
    
    # Kill mock control plane
    if [ ! -z "$MOCK_CP_PID" ] && ps -p $MOCK_CP_PID > /dev/null 2>&1; then
        log_info "Stopping mock control plane (PID: $MOCK_CP_PID)"
        kill $MOCK_CP_PID 2>/dev/null || true
        wait $MOCK_CP_PID 2>/dev/null || true
    fi
    
    # Kill agent
    if [ ! -z "$AGENT_PID" ] && ps -p $AGENT_PID > /dev/null 2>&1; then
        log_info "Stopping agent (PID: $AGENT_PID)"
        kill $AGENT_PID 2>/dev/null || true
        wait $AGENT_PID 2>/dev/null || true
    fi
    
    log_info "Cleanup complete"
}

# Set trap for cleanup on exit
trap cleanup EXIT INT TERM

echo "=========================================="
echo "  Multi-Port Forward Tunnel Test"
echo "=========================================="
echo ""

# Step 1: Check binaries exist
log_info "Checking binaries..."
if [ ! -f "bin/pipeops-vm-agent" ]; then
    log_error "Agent binary not found. Run: go build -o bin/pipeops-vm-agent cmd/agent/main.go"
    exit 1
fi
if [ ! -f "bin/mock-control-plane" ]; then
    log_error "Mock control plane binary not found. Run: go build -o bin/mock-control-plane test/mock-control-plane/main-tunnel.go"
    exit 1
fi
log_success "Binaries found"

# Step 2: Check config file
log_info "Checking test configuration..."
if [ ! -f "config-test.yaml" ]; then
    log_error "config-test.yaml not found"
    exit 1
fi
log_success "Configuration file found"

# Display configuration
log_info "Configuration forwards:"
grep -A 10 "forwards:" config-test.yaml | grep -E "(name:|local_addr:)" | sed 's/^/  /'

echo ""

# Step 3: Start mock control plane
log_info "Starting mock control plane on port 8081..."
./bin/mock-control-plane > /tmp/mock-cp.log 2>&1 &
MOCK_CP_PID=$!
log_success "Mock control plane started (PID: $MOCK_CP_PID)"

# Wait for control plane to be ready
sleep 2

# Check if control plane is running
if ! ps -p $MOCK_CP_PID > /dev/null 2>&1; then
    log_error "Mock control plane failed to start"
    cat /tmp/mock-cp.log
    exit 1
fi

# Test control plane health
log_info "Testing control plane health..."
if curl -s http://localhost:8081/health > /dev/null 2>&1; then
    log_success "Control plane is healthy"
else
    log_error "Control plane health check failed"
    exit 1
fi

echo ""

# Step 4: Start agent
log_info "Starting agent with test configuration..."
./bin/pipeops-vm-agent --config config-test.yaml > /tmp/agent.log 2>&1 &
AGENT_PID=$!
log_success "Agent started (PID: $AGENT_PID)"

# Wait for agent to initialize
sleep 3

# Check if agent is running
if ! ps -p $AGENT_PID > /dev/null 2>&1; then
    log_error "Agent failed to start"
    cat /tmp/agent.log
    exit 1
fi

echo ""

# Step 5: Monitor tunnel creation
log_info "Monitoring tunnel creation (watching for 10 seconds)..."
sleep 10

echo ""

# Step 6: Check logs for multi-forward evidence
log_info "Analyzing logs..."

echo ""
log_info "Mock Control Plane Log (last 20 lines):"
echo "----------------------------------------"
tail -20 /tmp/mock-cp.log | sed 's/^/  /'
echo ""

log_info "Agent Log (last 20 lines):"
echo "----------------------------------------"
tail -20 /tmp/agent.log | sed 's/^/  /'
echo ""

# Step 7: Verify multi-port forwards
log_info "Checking for multi-forward indicators..."

# Check if control plane allocated multiple ports
if grep -q "Allocated port" /tmp/mock-cp.log; then
    PORT_COUNT=$(grep -c "Allocated port" /tmp/mock-cp.log)
    log_success "Control plane allocated $PORT_COUNT ports"
else
    log_warning "No port allocations found in control plane log"
fi

# Check if agent created tunnel with multiple forwards
if grep -q "forwards" /tmp/agent.log; then
    log_success "Agent logs mention forwards configuration"
else
    log_warning "No forwards configuration found in agent log"
fi

# Check if tunnel was created
if grep -q -i "tunnel.*created\|tunnel.*established\|tunnel.*connected" /tmp/agent.log; then
    log_success "Tunnel creation detected"
else
    log_warning "No tunnel creation detected"
fi

echo ""

# Step 8: Query control plane for tunnel status
log_info "Querying tunnel status from control plane..."
TUNNEL_STATUS=$(curl -s http://localhost:8081/api/tunnel/status/test-agent-001)
if [ ! -z "$TUNNEL_STATUS" ]; then
    echo "$TUNNEL_STATUS" | python3 -m json.tool 2>/dev/null || echo "$TUNNEL_STATUS"
    
    # Check if forwards array exists
    if echo "$TUNNEL_STATUS" | grep -q "forwards"; then
        log_success "Tunnel status contains forwards array"
        
        # Count forwards
        FORWARD_COUNT=$(echo "$TUNNEL_STATUS" | grep -o '"name"' | wc -l)
        log_info "Number of forwards in status: $FORWARD_COUNT"
    else
        log_warning "No forwards array in tunnel status"
    fi
else
    log_warning "Could not retrieve tunnel status"
fi

echo ""

# Step 9: Test agent health endpoint
log_info "Testing agent health endpoint..."
if curl -s http://localhost:8080/health > /dev/null 2>&1; then
    log_success "Agent health endpoint responding"
else
    log_warning "Agent health endpoint not responding"
fi

echo ""
echo "=========================================="
echo "  Test Summary"
echo "=========================================="
echo ""

log_info "Test completed. Review the logs above for details."
log_info "Logs saved to:"
echo "  - Mock Control Plane: /tmp/mock-cp.log"
echo "  - Agent: /tmp/agent.log"
echo ""

log_info "To view full logs:"
echo "  cat /tmp/mock-cp.log"
echo "  cat /tmp/agent.log"
echo ""

log_info "Press Enter to stop processes and exit, or Ctrl+C to exit now..."
read

exit 0
