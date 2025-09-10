#!/bin/bash

# PipeOps VM Agent Test Script
# This script runs a complete end-to-end test of the dual connections feature

set -e

echo "🧪 PipeOps VM Agent Integration Test"
echo "===================================="
echo

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Build everything
echo -e "${BLUE}📦 Building components...${NC}"
go build -o bin/agent cmd/agent/main.go
go build -o bin/mock-control-plane test/mock-control-plane/main.go
go build -o bin/mock-runner test/mock-runner/main.go
echo -e "${GREEN}✅ Build complete${NC}"
echo

# Check if we have a k8s cluster (optional)
if kubectl cluster-info &>/dev/null; then
    echo -e "${GREEN}✅ Kubernetes cluster detected${NC}"
    KUBE_AVAILABLE=true
else
    echo -e "${YELLOW}⚠️  No Kubernetes cluster detected (agent will run in mock mode)${NC}"
    KUBE_AVAILABLE=false
fi
echo

# Function to cleanup background processes
cleanup() {
    echo -e "\n${YELLOW}🧹 Cleaning up background processes...${NC}"
    if [ ! -z "$CONTROL_PLANE_PID" ]; then
        kill $CONTROL_PLANE_PID 2>/dev/null || true
    fi
    if [ ! -z "$RUNNER_PID" ]; then
        kill $RUNNER_PID 2>/dev/null || true
    fi
    if [ ! -z "$AGENT_PID" ]; then
        kill $AGENT_PID 2>/dev/null || true
    fi
    echo -e "${GREEN}✅ Cleanup complete${NC}"
}

# Set trap for cleanup
trap cleanup EXIT

# Start mock Control Plane
echo -e "${BLUE}🚀 Starting Mock Control Plane on :8081...${NC}"
./bin/mock-control-plane &
CONTROL_PLANE_PID=$!
sleep 2

# Check if Control Plane is running
if curl -s http://localhost:8081/health > /dev/null; then
    echo -e "${GREEN}✅ Mock Control Plane running${NC}"
else
    echo -e "${RED}❌ Mock Control Plane failed to start${NC}"
    exit 1
fi

# Start mock Runner
echo -e "${BLUE}🚀 Starting Mock Runner on :9090...${NC}"
./bin/mock-runner &
RUNNER_PID=$!
sleep 2

# Check if Runner is running
if curl -s http://localhost:9090/health > /dev/null; then
    echo -e "${GREEN}✅ Mock Runner running${NC}"
else
    echo -e "${RED}❌ Mock Runner failed to start${NC}"
    exit 1
fi

echo

# Start the agent
echo -e "${BLUE}🚀 Starting PipeOps Agent...${NC}"
echo "Expected sequence:"
echo "1. Agent connects to Control Plane"
echo "2. Agent registers"
echo "3. Control Plane assigns Runner (after 3s)"
echo "4. Agent establishes dual connections"
echo "5. Runner sends test operations"
echo

./bin/agent --config config-test.yaml &
AGENT_PID=$!

# Wait for agent to start
sleep 1

# Check if agent is running
if curl -s http://localhost:8080/health > /dev/null; then
    echo -e "${GREEN}✅ Agent HTTP server running${NC}"
else
    echo -e "${RED}❌ Agent HTTP server failed to start${NC}"
    exit 1
fi

echo
echo -e "${BLUE}📊 Monitoring test execution for 30 seconds...${NC}"
echo "Watch the logs above for:"
echo "- ✅ Agent registration"
echo "- ✅ Runner assignment"
echo "- ✅ Dual connections established"
echo "- ✅ Test deployment sent"
echo "- ✅ Heartbeat with connection status"
echo

# Monitor for 30 seconds
for i in {1..30}; do
    printf "\rMonitoring... %2d/30 seconds" $i
    sleep 1
done

echo
echo
echo -e "${GREEN}🎉 Test completed!${NC}"
echo
echo "If you saw the following in the logs, the test was successful:"
echo "✅ 'Agent registration received' in Control Plane"
echo "✅ 'Registration acknowledged' in Control Plane"  
echo "✅ 'Runner assignment sent' in Control Plane"
echo "✅ 'Dual connections enabled' in Agent"
echo "✅ 'New agent connected' in Runner"
echo "✅ 'Test deployment sent' in Runner"
echo

# Manual verification steps
echo -e "${BLUE}🔍 Manual Verification (optional):${NC}"
echo "Test individual endpoints:"
echo "curl http://localhost:8080/health    # Agent health"
echo "curl http://localhost:8080/ready     # Agent readiness"
echo "curl http://localhost:8080/version   # Agent version"
echo "curl http://localhost:8081/health    # Control Plane health"
echo "curl http://localhost:9090/health    # Runner health"
echo

echo -e "${YELLOW}Press any key to stop all services...${NC}"
read -n 1 -s

echo -e "${BLUE}🛑 Stopping services...${NC}"
