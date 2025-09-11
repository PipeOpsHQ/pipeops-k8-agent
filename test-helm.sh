#!/bin/bash

# Test script for Helm chart
# This script tests the Helm chart locally before CI/CD

set -e

echo "🧪 Testing PipeOps Agent Helm Chart"
echo "=================================="

# Change to the repository root
cd "$(dirname "$0")"

echo "📁 Current directory: $(pwd)"

# Check if Helm is installed
if ! command -v helm &> /dev/null; then
    echo "❌ Helm is not installed. Please install Helm first."
    exit 1
fi

echo "✅ Helm version: $(helm version --short)"

# Test chart linting
echo ""
echo "🔍 Linting Helm chart..."
helm lint helm/pipeops-agent/

# Test template rendering
echo ""
echo "🎨 Testing template rendering..."
helm template test-release helm/pipeops-agent/ \
    --set agent.pipeops.token="test-token" \
    --set agent.cluster.name="test-cluster" \
    --dry-run > /tmp/rendered-templates.yaml

echo "✅ Templates rendered successfully to /tmp/rendered-templates.yaml"

# Test packaging
echo ""
echo "📦 Testing chart packaging..."
TEMP_DIR=$(mktemp -d)
helm package helm/pipeops-agent/ --destination "$TEMP_DIR"

PACKAGE=$(find "$TEMP_DIR" -name "*.tgz")
if [ -f "$PACKAGE" ]; then
    echo "✅ Chart packaged successfully: $(basename "$PACKAGE")"
    echo "📊 Package size: $(du -h "$PACKAGE" | cut -f1)"
else
    echo "❌ Chart packaging failed"
    exit 1
fi

# Cleanup
rm -rf "$TEMP_DIR"

echo ""
echo "🎉 All tests passed! The Helm chart is ready."
echo ""
echo "💡 To install the chart locally:"
echo "   helm install pipeops-agent ./helm/pipeops-agent/ \\"
echo "     --set agent.pipeops.token=\"your-token\" \\"
echo "     --set agent.cluster.name=\"your-cluster\""
echo ""
