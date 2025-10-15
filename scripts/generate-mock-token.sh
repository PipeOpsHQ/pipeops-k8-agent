#!/bin/bash

# Mock Token Generator for Local Development
# This creates a fake K8s ServiceAccount token for testing the agent locally

set -e

echo "🔧 Creating mock ServiceAccount token for local development..."

# Generate a base64-encoded mock JWT token
# This simulates what Kubernetes would mount in the pod
MOCK_TOKEN="eyJhbGciOiJSUzI1NiIsImtpZCI6IjEyMzQ1Njc4OTAifQ.eyJpc3MiOiJrdWJlcm5ldGVzL3NlcnZpY2VhY2NvdW50Iiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9uYW1lc3BhY2UiOiJwaXBlb3BzLXN5c3RlbSIsImt1YmVybmV0ZXMuaW8vc2VydmljZWFjY291bnQvc2VjcmV0Lm5hbWUiOiJwaXBlb3BzLWFnZW50LXRva2VuIiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9zZXJ2aWNlLWFjY291bnQubmFtZSI6InBpcGVvcHMtYWdlbnQiLCJrdWJlcm5ldGVzLmlvL3NlcnZpY2VhY2NvdW50L3NlcnZpY2UtYWNjb3VudC51aWQiOiI1NTBlODQwMC1lMjliLTQxZDQtYTcxNi00NDY2NTU0NDAwMDAiLCJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6cGlwZW9wcy1zeXN0ZW06cGlwZW9wcy1hZ2VudCJ9.MOCK-SIGNATURE-FOR-LOCAL-DEV-ONLY"

# Create tmp directory if it doesn't exist
mkdir -p tmp

# Create consolidated state file with mock token
cat > tmp/agent-state.yaml <<EOF
agent_id: ""
cluster_id: ""
cluster_token: $MOCK_TOKEN
EOF

# Set secure permissions
chmod 600 tmp/agent-state.yaml

echo "✅ Mock token saved to: tmp/agent-state.yaml"
echo ""
echo "📋 Token preview:"
echo "$MOCK_TOKEN" | cut -c1-50
echo "..."
echo ""
echo "⚠️  NOTE: This is a MOCK token for LOCAL DEVELOPMENT only!"
echo "    In production, the real K8s ServiceAccount token will be used."
echo ""
echo "🚀 Now run the agent:"
echo "   make run"
