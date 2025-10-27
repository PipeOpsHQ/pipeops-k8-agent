#!/bin/bash

# PipeOps Agent State Troubleshooting Script
# This script helps diagnose why the agent keeps re-registering

set -e

NAMESPACE="${1:-pipeops-system}"
RELEASE_NAME="${2:-pipeops-agent}"

echo "🔍 PipeOps Agent State Diagnostics"
echo "=================================="
echo "Namespace: $NAMESPACE"
echo "Release: $RELEASE_NAME"
echo ""

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    echo "❌ Namespace '$NAMESPACE' does not exist"
    exit 1
fi

# Check deployment status
echo "📊 Deployment Status:"
kubectl get deployment -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent || {
    echo "❌ No PipeOps agent deployment found in namespace $NAMESPACE"
    exit 1
}
echo ""

# Check pod status
echo "🔄 Pod Status:"
kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent
echo ""

# Check agent logs for state-related messages
echo "📝 Recent Agent Logs (State-related):"
POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l app.kubernetes.io/name=pipeops-agent -o jsonpath='{.items[0].metadata.name}')
if [ -n "$POD_NAME" ]; then
    kubectl logs "$POD_NAME" -n "$NAMESPACE" --tail=50 | grep -i -E "(state|configmap|agent.*id|registration|generated)" || echo "No state-related log entries found"
else
    echo "❌ No running pods found"
fi
echo ""

# Check state ConfigMap
echo "🗂️  State ConfigMap Status:"
STATE_CM_NAME="${RELEASE_NAME}-state"
if kubectl get configmap "$STATE_CM_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
    echo "✅ State ConfigMap '$STATE_CM_NAME' exists"
    kubectl get configmap "$STATE_CM_NAME" -n "$NAMESPACE" -o yaml | grep -E "(agent_id|cluster_id|data:)" || echo "ConfigMap exists but appears empty"
else
    echo "❌ State ConfigMap '$STATE_CM_NAME' does not exist"
    echo "   This is likely why the agent keeps re-registering!"
fi
echo ""

# Check RBAC permissions
echo "🔐 RBAC Permissions Check:"
SERVICE_ACCOUNT="${RELEASE_NAME}"
echo "Service Account: $SERVICE_ACCOUNT"

# Check if service account can create configmaps
if kubectl auth can-i create configmaps --as="system:serviceaccount:$NAMESPACE:$SERVICE_ACCOUNT" -n "$NAMESPACE" 2>/dev/null; then
    echo "✅ Can create ConfigMaps in namespace $NAMESPACE"
else
    echo "❌ Cannot create ConfigMaps in namespace $NAMESPACE"
    echo "   This will prevent state persistence!"
fi

# Check if service account can update configmaps
if kubectl auth can-i update configmaps --as="system:serviceaccount:$NAMESPACE:$SERVICE_ACCOUNT" -n "$NAMESPACE" 2>/dev/null; then
    echo "✅ Can update ConfigMaps in namespace $NAMESPACE"
else
    echo "❌ Cannot update ConfigMaps in namespace $NAMESPACE"
    echo "   This will prevent state persistence!"
fi

# Check if service account can get configmaps
if kubectl auth can-i get configmaps --as="system:serviceaccount:$NAMESPACE:$SERVICE_ACCOUNT" -n "$NAMESPACE" 2>/dev/null; then
    echo "✅ Can read ConfigMaps in namespace $NAMESPACE"
else
    echo "❌ Cannot read ConfigMaps in namespace $NAMESPACE"
    echo "   This will prevent state loading!"
fi
echo ""

# Environment variables check
echo "🌍 Environment Variables (in pod):"
if [ -n "$POD_NAME" ]; then
    kubectl exec "$POD_NAME" -n "$NAMESPACE" -- env | grep -E "PIPEOPS.*STATE|PIPEOPS.*NAMESPACE|PIPEOPS_POD" || echo "No state-related environment variables found"
else
    echo "❌ No running pods to check environment variables"
fi
echo ""

# Recommendations
echo "💡 Recommendations:"
echo "1. If state ConfigMap doesn't exist, check RBAC permissions"
echo "2. If RBAC is missing, ensure Role and RoleBinding are created"
echo "3. If agent logs show 'Generated new agent ID', state persistence is failing"
echo "4. Try deleting the pod to force restart: kubectl delete pod $POD_NAME -n $NAMESPACE"
echo ""

echo "🔗 For more help, see: https://docs.pipeops.io/troubleshooting"