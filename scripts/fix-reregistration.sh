#!/bin/bash

# Production Agent State Recovery Script
# This script helps recover from persistent re-registration issues

set -e

NAMESPACE="${1:-pipeops-system}"
RELEASE_NAME="${2:-pipeops-agent}"

echo "🔧 PipeOps Agent State Recovery"
echo "==============================="
echo "Namespace: $NAMESPACE"
echo "Release: $RELEASE_NAME"
echo ""

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    echo "❌ Namespace '$NAMESPACE' does not exist"
    exit 1
fi

echo "🛠️  Step 1: Checking current agent state..."

# Check current deployment
if kubectl get deployment -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent >/dev/null 2>&1; then
    echo "✅ Agent deployment found"
    
    # Get pod status
    POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [ -n "$POD_NAME" ]; then
        echo "✅ Agent pod: $POD_NAME"
        
        # Check if agent is re-registering
        if kubectl logs "$POD_NAME" -n "$NAMESPACE" --tail=10 | grep -q "Generated new agent ID\|Will register as new cluster"; then
            echo "⚠️  Agent is re-registering on every restart!"
            
            echo ""
            echo "🛠️  Step 2: Applying fixes..."
            
            # Fix 1: Ensure state ConfigMap has proper structure
            STATE_CM_NAME="${RELEASE_NAME}-state"
            echo "   - Checking state ConfigMap: $STATE_CM_NAME"
            
            if kubectl get configmap "$STATE_CM_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
                echo "   ✅ State ConfigMap exists"
                
                # Check if it has agent_id
                if kubectl get configmap "$STATE_CM_NAME" -n "$NAMESPACE" -o jsonpath='{.data.agent_id}' | grep -q .; then
                    echo "   ✅ State ConfigMap has agent_id"
                else
                    echo "   ❌ State ConfigMap missing agent_id"
                    echo "   🔧 Extracting agent_id from current pod..."
                    
                    # Get agent ID from pod environment or generate one
                    AGENT_ID=$(kubectl exec "$POD_NAME" -n "$NAMESPACE" -- env | grep PIPEOPS_AGENT_ID | cut -d'=' -f2 || echo "")
                    if [ -z "$AGENT_ID" ]; then
                        AGENT_ID="agent-$(openssl rand -hex 4)"
                        echo "   🆕 Generated new agent_id: $AGENT_ID"
                    else
                        echo "   📋 Using existing agent_id: $AGENT_ID"
                    fi
                    
                    # Update ConfigMap with agent_id
                    kubectl patch configmap "$STATE_CM_NAME" -n "$NAMESPACE" --type='merge' -p="{\"data\":{\"agent_id\":\"$AGENT_ID\"}}"
                    echo "   ✅ Updated state ConfigMap with agent_id"
                fi
            else
                echo "   ❌ State ConfigMap does not exist"
                echo "   🔧 Creating state ConfigMap..."
                
                AGENT_ID="agent-$(openssl rand -hex 4)"
                kubectl create configmap "$STATE_CM_NAME" -n "$NAMESPACE" \
                    --from-literal=agent_id="$AGENT_ID" \
                    --dry-run=client -o yaml | kubectl apply -f -
                
                # Add proper labels
                kubectl label configmap "$STATE_CM_NAME" -n "$NAMESPACE" \
                    app.kubernetes.io/name=pipeops-agent \
                    app.kubernetes.io/component=state \
                    app.kubernetes.io/managed-by=pipeops-agent \
                    --overwrite
                
                echo "   ✅ Created state ConfigMap with agent_id: $AGENT_ID"
            fi
            
            # Fix 2: Verify RBAC permissions
            echo "   - Verifying RBAC permissions..."
            SERVICE_ACCOUNT="${RELEASE_NAME}"
            
            if kubectl auth can-i create configmaps --as="system:serviceaccount:$NAMESPACE:$SERVICE_ACCOUNT" -n "$NAMESPACE" >/dev/null 2>&1; then
                echo "   ✅ Agent can create ConfigMaps"
            else
                echo "   ❌ Agent cannot create ConfigMaps"
                echo "   🔧 This requires updating RBAC permissions in your Helm chart"
            fi
            
            if kubectl auth can-i update configmaps --as="system:serviceaccount:$NAMESPACE:$SERVICE_ACCOUNT" -n "$NAMESPACE" >/dev/null 2>&1; then
                echo "   ✅ Agent can update ConfigMaps"
            else
                echo "   ❌ Agent cannot update ConfigMaps"
                echo "   🔧 This requires updating RBAC permissions in your Helm chart"
            fi
            
            # Fix 3: Restart the agent to pick up state
            echo ""
            echo "🛠️  Step 3: Restarting agent to apply fixes..."
            kubectl rollout restart deployment -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent
            
            echo "   ⏳ Waiting for rollout to complete..."
            kubectl rollout status deployment -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent --timeout=120s
            
            # Wait a bit for the agent to start
            sleep 15
            
            # Check if the issue is resolved
            NEW_POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
            if [ -n "$NEW_POD_NAME" ]; then
                echo ""
                echo "🔍 Checking if re-registration issue is resolved..."
                
                if kubectl logs "$NEW_POD_NAME" -n "$NAMESPACE" --tail=20 | grep -q "Agent ID loaded from persistent state"; then
                    echo "✅ SUCCESS: Agent is now loading ID from persistent state!"
                    echo "✅ Re-registration issue should be resolved"
                else
                    echo "⚠️  Agent may still be having state persistence issues"
                    echo "📝 Recent logs:"
                    kubectl logs "$NEW_POD_NAME" -n "$NAMESPACE" --tail=10 | grep -i -E "(agent.*id|state|registration)"
                fi
            fi
            
        else
            echo "✅ Agent appears to be stable (not re-registering)"
        fi
    else
        echo "❌ No agent pods found"
    fi
else
    echo "❌ No agent deployment found"
    echo "💡 Please deploy the agent first using Helm"
fi

echo ""
echo "🔗 For persistent issues, check:"
echo "   1. RBAC permissions for ConfigMap management"
echo "   2. Agent logs: kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=pipeops-agent -f"
echo "   3. State ConfigMap: kubectl get configmap ${RELEASE_NAME}-state -n $NAMESPACE -o yaml"
echo ""
echo "📖 Documentation: https://docs.pipeops.io/troubleshooting"