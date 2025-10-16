#!/bin/bash

# Example: Basic Installation with Auto-Detection
# 
# This example shows the simplest way to install PipeOps agent
# with automatic cluster type detection.

set -e

# Set your PipeOps token (required)
export PIPEOPS_TOKEN="your-pipeops-token-here"

# Optional: Set cluster name
export CLUSTER_NAME="my-cluster"

# Run installer (will auto-detect best cluster type)
../install.sh

# After installation, you can access:
# - PipeOps dashboard to see your cluster
# - Grafana at: kubectl port-forward -n monitoring svc/prometheus-grafana 3000:80
# - Prometheus at: kubectl port-forward -n monitoring svc/prometheus-kube-prometheus-prometheus 9090:9090
