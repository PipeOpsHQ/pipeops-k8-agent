# Quick Start

Get up and running with the PipeOps Kubernetes Agent in just a few minutes. This guide covers the essential steps to deploy the agent and start managing your cluster.

## Before You Begin

Ensure you have:

- A Kubernetes cluster (local or cloud-based)
- `kubectl` configured with cluster access
- A PipeOps account ([sign up free](https://console.pipeops.io))
- Cluster admin permissions

## 5-Minute Setup

### Step 1: Get Your Cluster Token

1. **Log in to PipeOps Dashboard**: Visit [console.pipeops.io](https://console.pipeops.io)
2. **Navigate to Clusters**: Go to "Infrastructure" → "Clusters"
3. **Add New Cluster**: Click "Add Cluster" → "Kubernetes Agent"
4. **Copy Token**: Save the generated cluster token

### Step 2: Install the Agent

Choose your preferred installation method:

=== "One-Line Install"

    The fastest way to get started:

    ```bash
    # Production k3s on Linux VMs:
    curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
    # Development clusters (k3d/kind/minikube): omit sudo
    # curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
    ```

    This command will:
    - Detect your environment automatically
    - Install all dependencies
    - Deploy the agent with monitoring stack
    - Connect securely to PipeOps control plane

=== "Helm Chart"

    For production environments:

    ```bash
    # Install agent directly from GHCR
    helm install pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent \
      --set agent.pipeops.token="your-pipeops-token" \
      --set agent.cluster.name="my-cluster"
    ```
    
    > **Note:** Helm installation does NOT auto-install monitoring components by default (assumes existing cluster infrastructure). 
    > Add `--set agent.autoInstallComponents=true` to enable auto-installation.

=== "Kubectl Apply"

    Direct Kubernetes deployment:

    ```bash
    # Download manifest
    curl -O https://get.pipeops.dev/k8-agent.yaml
    
    # Configure your token
    sed -i 's/YOUR_TOKEN_HERE/your-actual-token/' agent.yaml
    
    # Deploy
    kubectl apply -f agent.yaml
    ```

### Step 3: Verify Installation

Check that the agent is running:

```bash
# Check agent pod status
kubectl get pods -n pipeops-system

# View agent logs
kubectl logs -f deployment/pipeops-agent -n pipeops-system

# Check agent is connected (look for "connected" in logs)
kubectl logs deployment/pipeops-agent -n pipeops-system | grep -i "connected\|registered"
```

Expected output:
```text
NAME                             READY   STATUS    RESTARTS   AGE
pipeops-agent-xxxxxxxxxx-xxxxx   1/1     Running   0          2m
```

And in the logs, you should see:
```text
INFO Agent registered successfully with control plane
INFO WebSocket tunnel connected
INFO Heartbeat sent successfully
```

## Essential Operations

### Viewing Cluster Status

Get an overview of your cluster health:

```bash
# Check all pods in the cluster
kubectl get pods -A

# Check node status
kubectl get nodes

# Check agent logs
kubectl logs deployment/pipeops-agent -n pipeops-system

# Check agent HTTP endpoints
kubectl port-forward -n pipeops-system deployment/pipeops-agent 8080:8080
# Then visit http://localhost:8080/health
# Or http://localhost:8080/dashboard for the web dashboard
```

### Accessing Monitoring Dashboards

The agent automatically sets up monitoring dashboards:

=== "Grafana"

    ```bash
    # Port-forward to access Grafana (if monitoring is installed)
    kubectl port-forward -n pipeops-monitoring svc/grafana 3000:3000
    ```
    
    Open [http://localhost:3000](http://localhost:3000)
    
    - **Username**: `admin`
    - **Password**: Retrieve with `kubectl get secret -n pipeops-monitoring grafana -o jsonpath='{.data.admin-password}' | base64 -d`

=== "Prometheus"

    ```bash
    # Port-forward to access Prometheus (if monitoring is installed)
    kubectl port-forward -n pipeops-monitoring svc/prometheus-server 9090:9090
    ```
    
    Open [http://localhost:9090](http://localhost:9090)

=== "Agent Dashboard"

    ```bash
    # Port-forward to access Agent Dashboard
    kubectl port-forward -n pipeops-system deployment/pipeops-agent 8080:8080
    ```
    
    Open [http://localhost:8080/dashboard](http://localhost:8080/dashboard)
    
    Available endpoints:
    - `/health` - Health check
    - `/ready` - Readiness status
    - `/metrics` - Prometheus metrics
    - `/dashboard` - Web dashboard

### Managing Applications

Deploy applications through the PipeOps dashboard or using kubectl:

```bash
# Deploy using kubectl
kubectl create deployment my-app --image=nginx:latest

# Expose as a service
kubectl expose deployment my-app --port=80 --type=LoadBalancer

# Check deployment status
kubectl get deployments
kubectl get pods

# Scale application
kubectl scale deployment my-app --replicas=3

# View application logs
kubectl logs -l app=my-app --tail=100 -f
```

**Note**: Application deployment and management is primarily done through the PipeOps web dashboard at [console.pipeops.io](https://console.pipeops.io).

## Configuration Basics

### Environment Variables

Key configuration options:

```bash
# Set cluster name
export PIPEOPS_CLUSTER_NAME="production-cluster"

# Set log level (debug, info, warn, error)
export PIPEOPS_LOG_LEVEL="info"

# Set custom endpoint (for enterprise)
export PIPEOPS_ENDPOINT="https://your-enterprise.pipeops.io"
```

### Configuration File

Create `/etc/pipeops/config.yaml` for persistent settings:

```yaml
cluster:
  name: "production-cluster"
  region: "us-west-2"
  tags:
    environment: "production"
    team: "platform"

agent:
  log_level: "info"
  heartbeat_interval: "30s"
  
monitoring:
  enabled: true
  retention_days: 30
  
resources:
  limits:
    cpu: "1000m"
    memory: "1Gi"
```

## Health Checks

### Quick Health Check

```bash
# Check agent pod health
kubectl get pods -n pipeops-system

# Check agent logs for errors
kubectl logs deployment/pipeops-agent -n pipeops-system --tail=50

# Access agent health endpoint
kubectl port-forward -n pipeops-system deployment/pipeops-agent 8080:8080
# Then: curl http://localhost:8080/health

# Check detailed health status
curl http://localhost:8080/api/health/detailed
```

The health check endpoints verify:
- Agent connectivity to control plane
- Kubernetes API access
- WebSocket tunnel status
- Resource availability
- Internal service health

### Monitoring Key Metrics

Important metrics to watch:

| Metric | Description | Command |
|--------|-------------|---------|
| **Cluster Health** | Overall cluster status | `kubectl get nodes && kubectl get pods -A` |
| **Resource Usage** | CPU, memory, storage | `kubectl top nodes && kubectl top pods -A` |
| **Pod Status** | Running/failed pods | `kubectl get pods -A` |
| **Agent Status** | Agent connectivity | `kubectl logs deployment/pipeops-agent -n pipeops-system \| tail` |

## Troubleshooting Quick Fixes

### Agent Not Connecting

1. **Check network connectivity**:
   ```bash
   # Test connection to PipeOps API
   curl -I https://api.pipeops.sh
   
   # Check from within the cluster
   kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
     curl -v https://api.pipeops.sh/health
   ```

2. **Verify token configuration**:
   ```bash
   # Check secret exists
   kubectl get secret -n pipeops-system pipeops-agent-secret
   
   # View configuration (redacted)
   kubectl get configmap -n pipeops-system pipeops-agent-config -o yaml
   ```

3. **Restart agent**:
   ```bash
   kubectl rollout restart deployment/pipeops-agent -n pipeops-system
   ```

### Monitoring Stack Issues

1. **Check pod status**:
   ```bash
   kubectl get pods -n pipeops-monitoring
   ```

2. **View logs**:
   ```bash
   # Agent logs
   kubectl logs -f deployment/pipeops-agent -n pipeops-system
   
   # Prometheus logs (if installed)
   kubectl logs -f deployment/prometheus-server -n pipeops-monitoring
   
   # Grafana logs (if installed)
   kubectl logs -f deployment/grafana -n pipeops-monitoring
   ```

3. **Restart monitoring components**:
   ```bash
   kubectl rollout restart deployment/prometheus-server -n pipeops-monitoring
   kubectl rollout restart deployment/grafana -n pipeops-monitoring
   ```

### Resource Constraints

1. **Check resource usage**:
   ```bash
   kubectl top nodes
   kubectl top pods -n pipeops-system
   ```

2. **Increase resource limits**:
   ```bash
   helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent \
     --set resources.limits.memory=2Gi \
     --set resources.limits.cpu=1000m
   ```

## Quick Operations

### Upgrading the Agent

Keep your agent updated with the latest features:

```bash
# For Helm installations - update to latest version
helm repo update
helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent

# For kubectl installations - reapply manifest
kubectl apply -f https://get.pipeops.dev/k8-agent.yaml

# For script installations - re-run installer
# Production k3s on Linux VMs:
curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
# Development clusters (k3d/kind/minikube): omit sudo
# curl -fsSL https://get.pipeops.dev/k8-install.sh | bash

# Verify update by checking pod restart
kubectl get pods -n pipeops-system -w
```

### Checking Agent Version

```bash
# Check the running agent version from logs
kubectl logs deployment/pipeops-agent -n pipeops-system | grep -i "version\|starting"

# Or access the version endpoint
kubectl port-forward -n pipeops-system deployment/pipeops-agent 8080:8080
# Then: curl http://localhost:8080/version
```

### Uninstalling the Agent

If you need to remove the agent:

```bash
# For script installations - use uninstall script
curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/uninstall.sh | bash

# For Helm installations
helm uninstall pipeops-agent -n pipeops-system

# For kubectl installations - delete resources
kubectl delete namespace pipeops-system
kubectl delete namespace pipeops-monitoring  # If monitoring was installed

# Manually delete remaining resources
kubectl delete clusterrole pipeops-agent
kubectl delete clusterrolebinding pipeops-agent
```

For detailed upgrade and uninstall procedures, see the [Management Guide](management.md).

## What's Next?

Now that your agent is running, explore these features:

### Security & Compliance
- [Configure RBAC](configuration.md) for fine-grained access control
- [Set up TLS certificates](configuration.md) for secure communications
- [Enable audit logging](configuration.md) for compliance

### Advanced Monitoring
- [Advanced monitoring setup](../advanced/monitoring.md) for comprehensive observability
- [Alerting and notifications](../advanced/monitoring.md) for proactive issue detection
- [Log aggregation](../advanced/monitoring.md) with Loki and Grafana

### CI/CD Integration
- GitHub Actions integration for automated deployments
- GitLab CI pipelines for continuous deployment
- Jenkins plugins for enterprise environments

### Production Deployment
- Scale your applications based on demand
- Monitor performance and resource usage
- Configure alerts and notifications

## Helpful Resources

- **[Complete Installation Guide](installation.md)** - Detailed installation options
- **[Configuration Reference](configuration.md)** - All configuration options
- **[Architecture Guide](../ARCHITECTURE.md)** - Understanding the system design
- **[Advanced Monitoring](../advanced/monitoring.md)** - Monitoring and observability

## Pro Tips

!!! tip "Performance Optimization"
    
    For better performance in production:
    
    ```bash
    # Adjust resource limits via Helm
    helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent \
      --set resources.limits.memory=2Gi \
      --set resources.limits.cpu=1000m
    ```

!!! tip "Development Environment"
    
    For development clusters, enable debug logging:
    
    ```bash
    # Set debug log level
    kubectl set env deployment/pipeops-agent -n pipeops-system PIPEOPS_LOG_LEVEL=debug
    
    # View debug logs
    kubectl logs -f deployment/pipeops-agent -n pipeops-system
    ```

!!! tip "Backup Configuration"
    
    Always backup your cluster configuration:
    
    ```bash
    # Export agent configuration
    kubectl get configmap -n pipeops-system pipeops-agent-config -o yaml > pipeops-config-backup.yaml
    kubectl get secret -n pipeops-system pipeops-agent-secret -o yaml > pipeops-secret-backup.yaml
    
    # Store in version control (redact secrets first!)
    git add pipeops-config-backup.yaml
    git commit -m "Add PipeOps agent configuration backup"
    ```

---

**Need help?** Join our [community Discord](https://discord.gg/pipeops) or check the [installation guide](installation.md) for more details.
