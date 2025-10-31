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
    curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
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
# Check agent status
kubectl get pods -n pipeops-system

# Verify connectivity
pipeops-agent status
```

Expected output:
```text
PipeOps Agent Status: Running
Control Plane Connection: Connected  
Kubernetes API: Accessible
Monitoring Stack: Healthy
```

## Essential Operations

### Viewing Cluster Status

Get an overview of your cluster health:

```bash
# Agent status
pipeops-agent status

# Detailed cluster info
pipeops-agent cluster info

# Resource usage
pipeops-agent cluster resources
```

### Accessing Monitoring Dashboards

The agent automatically sets up monitoring dashboards:

=== "Grafana"

    ```bash
    # Port-forward to access Grafana
    kubectl port-forward -n pipeops-system svc/grafana 3000:3000
    ```
    
    Open [http://localhost:3000](http://localhost:3000)
    
    - **Username**: `admin`
    - **Password**: Check with `kubectl get secret -n pipeops-system grafana-admin-password -o jsonpath='{.data.password}' | base64 -d`

=== "Prometheus"

    ```bash
    # Port-forward to access Prometheus
    kubectl port-forward -n pipeops-system svc/prometheus 9090:9090
    ```
    
    Open [http://localhost:9090](http://localhost:9090)

=== "Agent Dashboard"

    ```bash
    # Port-forward to access Agent Dashboard
    kubectl port-forward -n pipeops-system svc/pipeops-agent 8080:8080
    ```
    
    Open [http://localhost:8080](http://localhost:8080)

### Managing Applications

Deploy your first application through PipeOps:

```bash
# List available applications
pipeops-agent apps list

# Deploy an application
pipeops-agent apps deploy --name="my-app" --image="nginx:latest"

# Check deployment status
pipeops-agent apps status my-app

# Scale application
pipeops-agent apps scale my-app --replicas=3
```

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
# Run comprehensive health check
pipeops-agent diagnose
```

This command checks:
- Agent connectivity to control plane
- Kubernetes API access
- Monitoring stack health
- Resource availability
- Network connectivity

### Monitoring Key Metrics

Important metrics to watch:

| Metric | Description | Command |
|--------|-------------|---------|
| **Cluster Health** | Overall cluster status | `pipeops-agent cluster health` |
| **Resource Usage** | CPU, memory, storage | `pipeops-agent resources` |
| **Pod Status** | Running/failed pods | `kubectl get pods -A` |
| **Network** | Connectivity status | `pipeops-agent network test` |

## Troubleshooting Quick Fixes

### Agent Not Connecting

1. **Check network connectivity**:
   ```bash
   curl -I https://api.pipeops.io
   ```

2. **Verify token**:
   ```bash
   pipeops-agent validate-token
   ```

3. **Restart agent**:
   ```bash
   kubectl rollout restart deployment/pipeops-agent -n pipeops-system
   ```

### Monitoring Stack Issues

1. **Check pod status**:
   ```bash
   kubectl get pods -n pipeops-system
   ```

2. **View logs**:
   ```bash
   kubectl logs -f deployment/pipeops-agent -n pipeops-system
   ```

3. **Restart monitoring**:
   ```bash
   kubectl rollout restart deployment/prometheus -n pipeops-system
   kubectl rollout restart deployment/grafana -n pipeops-system
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
# Check current version
pipeops-agent version

# Update to latest version
sudo pipeops-agent update

# For Helm installations
helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent

# Verify update
pipeops-agent status
```

### Uninstalling the Agent

If you need to remove the agent:

```bash
# Complete uninstall (removes everything)
sudo pipeops-agent uninstall

# Helm uninstall
helm uninstall pipeops-agent

# Kubernetes manifest uninstall  
kubectl delete -f agent.yaml
```

For detailed upgrade and uninstall procedures, see the [Installation Guide](installation.md).

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
    # Enable resource monitoring
    helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent \
      --set monitoring.resources.enabled=true \
      --set agent.performance.mode=optimized
    ```

!!! tip "Development Environment"
    
    For development clusters, enable debug mode:
    
    ```bash
    export PIPEOPS_LOG_LEVEL=debug
    pipeops-agent start --dev-mode
    ```

!!! tip "Backup Configuration"
    
    Always backup your configuration:
    
    ```bash
    # Export current configuration
    pipeops-agent config export > pipeops-config-backup.yaml
    
    # Store in version control
    git add pipeops-config-backup.yaml
    git commit -m "Add PipeOps agent configuration backup"
    ```

---

**Need help?** Join our [community Discord](https://discord.gg/pipeops) or check the [installation guide](installation.md) for more details.
