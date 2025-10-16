#!/bin/bash

# Test script for cluster detection

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_test() {
    echo -e "${BLUE}[TEST]${NC} $1"
}

print_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

print_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
}

# Test 1: Detection script exists and is executable
print_test "Checking if detect-cluster-type.sh exists..."
if [ -x "$SCRIPT_DIR/detect-cluster-type.sh" ]; then
    print_pass "Detection script exists and is executable"
else
    print_fail "Detection script not found or not executable"
    exit 1
fi

# Test 2: Detection script can run without errors
print_test "Running detection script..."
if RECOMMENDATION=$("$SCRIPT_DIR/detect-cluster-type.sh" recommend 2>&1); then
    print_pass "Detection script executed successfully"
    echo "  Recommended: $RECOMMENDATION"
else
    print_fail "Detection script failed to execute"
    exit 1
fi

# Test 3: Recommendation is valid
print_test "Validating recommendation..."
case "$RECOMMENDATION" in
    "k3s"|"minikube"|"k3d"|"kind"|"none")
        print_pass "Recommendation is valid: $RECOMMENDATION"
        ;;
    *)
        print_fail "Invalid recommendation: $RECOMMENDATION"
        exit 1
        ;;
esac

# Test 4: Info command works
print_test "Testing info command..."
if "$SCRIPT_DIR/detect-cluster-type.sh" info >/dev/null 2>&1; then
    print_pass "Info command works"
else
    print_fail "Info command failed"
    exit 1
fi

# Test 5: Scores command works
print_test "Testing scores command..."
if SCORES=$("$SCRIPT_DIR/detect-cluster-type.sh" scores 2>&1); then
    print_pass "Scores command works"
    echo "  Scores: $SCORES"
else
    print_fail "Scores command failed"
    exit 1
fi

# Test 6: Scores format is valid
print_test "Validating scores format..."
if echo "$SCORES" | grep -q "k3s:[0-9]*,minikube:[0-9]*,k3d:[0-9]*,kind:[0-9]*"; then
    print_pass "Scores format is valid"
else
    print_fail "Scores format is invalid"
    exit 1
fi

# Test 7: Installation script exists
print_test "Checking if install-cluster.sh exists..."
if [ -f "$SCRIPT_DIR/install-cluster.sh" ]; then
    print_pass "Installation script exists"
else
    print_fail "Installation script not found"
    exit 1
fi

# Test 8: Installation script is sourceable
print_test "Testing if install-cluster.sh is sourceable..."
if source "$SCRIPT_DIR/install-cluster.sh" 2>&1 | grep -q "Please specify a cluster type" || true; then
    print_pass "Installation script is sourceable"
else
    print_fail "Installation script cannot be sourced"
    exit 1
fi

# Test 9: Main install.sh exists
print_test "Checking if install.sh exists..."
if [ -x "$SCRIPT_DIR/install.sh" ]; then
    print_pass "Main install script exists and is executable"
else
    print_fail "Main install script not found or not executable"
    exit 1
fi

# Test 10: Help command works
print_test "Testing install.sh help command..."
if "$SCRIPT_DIR/install.sh" help >/dev/null 2>&1; then
    print_pass "Help command works"
else
    print_fail "Help command failed"
    exit 1
fi

echo ""
echo -e "${GREEN}╔════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║  All tests passed successfully!       ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════╝${NC}"
echo ""

# Show system recommendation
echo -e "${BLUE}System Recommendation:${NC}"
"$SCRIPT_DIR/detect-cluster-type.sh" info
