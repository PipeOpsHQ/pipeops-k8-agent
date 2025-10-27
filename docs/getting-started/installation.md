# Installation

Welcome to the PipeOps Kubernetes Agent installation guide. Choose the installation method that best fits your environment.

## Quick Install (Recommended)

The intelligent installer automatically detects your environment and installs all necessary components:

```bash
# Set your PipeOps token (required)
export PIPEOPS_TOKEN="your-pipeops-token"

# Optional but recommended for clarity in the dashboard
export CLUSTER_NAME="my-pipeops-cluster"

# Optional: pin a specific distribution (k3s|minikube|k3d|kind|auto)
# export CLUSTER_TYPE="auto"

# Run the installer
curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
```

This installer will:

- Detect your operating system and architecture
- Install prerequisites (Docker, kubectl, K3s, etc.)
- Deploy the PipeOps agent as a system service
- Configure monitoring stack (Prometheus, Grafana, Loki)
- Set up secure connection to PipeOps control plane
- Transform your VM into a production-ready Kubernetes server

## Installation Methods

=== "Helm Chart"

    **Prerequisites**: Helm 3.0+ and kubectl configured

    ```bash
    # Add the PipeOps Helm repository
    helm repo add pipeops https://charts.pipeops.io
    helm repo update

    # Install the agent
    helm install pipeops-agent pipeops/pipeops-agent \
      --set agent.cluster.name="your-cluster-name" \
      --set agent.token="your-cluster-token"
    ```

    **Custom Configuration**:
    ```bash
    # Create custom values file
    cat > values-custom.yaml << EOF
    agent:
      cluster:
        name: "production-cluster"
        region: "us-west-2"
      resources:
        requests:
          memory: "256Mi"
          cpu: "250m"
        limits:
          memory: "512Mi"
          cpu: "500m"
    monitoring:
      enabled: true
      prometheus:
        enabled: true
      grafana:
        enabled: true
    EOF

    # Install with custom configuration
    helm install pipeops-agent pipeops/pipeops-agent -f values-custom.yaml
    ```

=== "Docker"

    **Prerequisites**: Docker 20.0+ and access to Docker socket

    ```bash
    # Run the agent in a container
    docker run -d \
      --name pipeops-agent \
      --restart unless-stopped \
      -v /var/run/docker.sock:/var/run/docker.sock \
      -v /etc/pipeops:/etc/pipeops \
      -e PIPEOPS_CLUSTER_NAME="your-cluster" \
      -e PIPEOPS_TOKEN="your-token" \
      pipeops/agent:latest
    ```

    **Docker Compose**:
    ```yaml
    version: '3.8'
    services:
      pipeops-agent:
        image: pipeops/agent:latest
        container_name: pipeops-agent
        restart: unless-stopped
        volumes:
          - /var/run/docker.sock:/var/run/docker.sock
          - ./config:/etc/pipeops
        environment:
          - PIPEOPS_CLUSTER_NAME=your-cluster
          - PIPEOPS_TOKEN=your-token
        ports:
          - "8080:8080"  # Agent dashboard
    ```

=== "Binary"

    **Manual Installation**:

    1. **Download the latest release**:
    ```bash
    # Detect architecture
    ARCH=$(uname -m)
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    
    # Download binary
    curl -LO "https://github.com/PipeOpsHQ/pipeops-k8-agent/releases/latest/download/pipeops-agent-${OS}-${ARCH}.tar.gz"
    ```

    2. **Extract and install**:
    ```bash
    tar -xzf pipeops-agent-${OS}-${ARCH}.tar.gz
    sudo mv pipeops-agent /usr/local/bin/
    sudo chmod +x /usr/local/bin/pipeops-agent
    ```

    3. **Verify installation**:
    ```bash
    pipeops-agent version
    ```

=== "Kubernetes Manifest"

    **Direct Kubernetes deployment**:

    ```bash
    # Download the manifest
    curl -O https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml
    
    # Edit the manifest with your configuration
    vim agent.yaml
    
    # Apply to cluster
    kubectl apply -f agent.yaml
    ```

## Prerequisites

Before installing the PipeOps Agent, ensure your system meets these requirements:

### System Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| CPU | 1 core | 2+ cores |
| Memory | 512MB | 1GB+ |
| Storage | 1GB | 5GB+ |
| Network | Outbound HTTPS (443) | Outbound HTTPS + SSH (22) |

### Software Dependencies

=== "Linux"

    **Ubuntu/Debian**:
    ```bash
    sudo apt update
    sudo apt install -y curl wget gnupg2 software-properties-common
    ```

    **CentOS/RHEL/Fedora**:
    ```bash
    sudo yum update -y
    sudo yum install -y curl wget gnupg2
    ```

=== "macOS"

    **Homebrew** (recommended):
    ```bash
    # Install Homebrew if not present
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    
    # Install dependencies
    brew install curl wget
    ```

=== "Windows"

    **PowerShell** (run as Administrator):
    ```powershell
    # Install Chocolatey
    Set-ExecutionPolicy Bypass -Scope Process -Force
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072
    iex ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))
    
    # Install dependencies
    choco install curl wget -y
    ```

### Kubernetes Requirements

If deploying to an existing Kubernetes cluster:

- **Kubernetes version**: 1.20+
- **kubectl**: Configured with cluster access
- **RBAC permissions**: Cluster admin or sufficient permissions
- **Container runtime**: Docker, containerd, or CRI-O

## Authentication & Configuration

### Obtaining Your PipeOps Token

1. **Log in to PipeOps Dashboard**:
   Visit [console.pipeops.io](https://console.pipeops.io) and navigate to your organization.

2. **Create a new server**:
   - Go to "Infrastructure" → "Servers"
   - Click "Add Server"
   - Select "VM Agent"
   - Copy the generated token

3. **Set environment variables before installation**:
   ```bash
   # Set your PipeOps token (required)
   export PIPEOPS_TOKEN="your-pipeops-token-here"
   
   # Set cluster name (optional but recommended)
   export CLUSTER_NAME="production-cluster"
   
   # Set cluster type (optional)
   export CLUSTER_TYPE="k3s"
   ```

### Configuration File

Create a configuration file at `/etc/pipeops/config.yaml`:

```yaml
# PipeOps Agent Configuration
cluster:
  name: "production-cluster"
  region: "us-west-2"
  environment: "production"
  
agent:
  token: "your-cluster-token"
  endpoint: "https://api.pipeops.io"
  log_level: "info"
  
monitoring:
  enabled: true
  prometheus:
    port: 9090
    retention: "30d"
  grafana:
    port: 3000
    admin_password: "your-secure-password"
  loki:
    port: 3100
```

## Verification

After installation, verify that the agent is running correctly:

### 1. Check Service Status

```bash
# Check if the service is running
sudo systemctl status pipeops-agent

# View service logs
sudo journalctl -u pipeops-agent --no-pager -n 20
```

Expected output:
```
● pipeops-agent.service - PipeOps Kubernetes Agent
   Loaded: loaded (/etc/systemd/system/pipeops-agent.service; enabled)
   Active: active (running) since Mon 2024-01-15 10:30:00 UTC; 2min ago
   Main PID: 12345 (pipeops-agent)
```

### 2. Verify Kubernetes Setup

```bash
# Check K3s status
sudo systemctl status k3s

# List running containers
sudo k3s crictl ps

# Check node status
sudo k3s kubectl get nodes
```

Expected output:
```
NAME                 STATUS   ROLES                  AGE     VERSION
your-server-name     Ready    control-plane,master   2m30s   v1.28.2+k3s1
```

### 3. Access Monitoring Dashboard

The agent includes a local dashboard accessible at `http://localhost:8080`:

- **Agent Status**: Real-time agent health and metrics
- **Cluster Overview**: Kubernetes cluster information
- **Logs**: Agent and application logs
- **Metrics**: Performance and resource usage

## Troubleshooting

### Common Issues

??? question "Service fails to connect to PipeOps platform"

    **Symptoms**: Service logs show connection errors or authentication failures
    
    **Solutions**:
    
    1. Check network connectivity:
    ```bash
    curl -I https://api.pipeops.io
    ```
    
    2. Verify your token is set correctly:
    ```bash
    sudo journalctl -u pipeops-agent | grep -i token
    ```
    
    3. Check firewall settings (outbound HTTPS/443 required)

??? question "Kubernetes permissions denied"

    **Symptoms**: RBAC errors or insufficient permissions
    
    **Solutions**:
    
    1. Ensure cluster admin access:
    ```bash
    kubectl auth can-i "*" "*" --all-namespaces
    ```
    
    2. Apply RBAC configuration:
    ```bash
    kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/rbac.yaml
    ```

??? question "Monitoring stack fails to start"

    **Symptoms**: Prometheus, Grafana, or Loki pods in error state
    
    **Solutions**:
    
    1. Check resource limits:
    ```bash
    kubectl describe pod -n pipeops-system
    ```
    
    2. Increase resource allocation:
    ```bash
    helm upgrade pipeops-agent pipeops/pipeops-agent \
      --set resources.requests.memory=1Gi \
      --set resources.requests.cpu=500m
    ```

### Getting Help

If you encounter issues not covered here:

1. **Check the service logs**:
   ```bash
   sudo journalctl -u pipeops-agent --no-pager -n 50
   ```

2. **Check service status**:
   ```bash
   sudo systemctl status pipeops-agent
   ```

3. **Contact support**:
   - [GitHub Issues](https://github.com/PipeOpsHQ/pipeops-k8-agent/issues)
   - [Support Portal](https://support.pipeops.io)
   - Email: [support@pipeops.io](mailto:support@pipeops.io)

## Upgrading

Keep your PipeOps Agent up to date with the latest features and security updates.

### Automatic Updates

The agent can be configured to update automatically:

```bash
# Enable automatic updates (recommended for development)
pipeops-agent config set auto-update.enabled=true
pipeops-agent config set auto-update.channel=stable

# Check for updates manually
pipeops-agent update check
```

### Manual Updates

=== "Script Installation"

    **Using the install script**:
    ```bash
    # Re-run the installer (preserves configuration)
    curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
    ```

    **Using the built-in update command**:
    ```bash
    # Update to latest version
    sudo pipeops-agent update

    # Update to specific version
    sudo pipeops-agent update --version=v1.2.3

    # Check current version
    pipeops-agent version
    ```

=== "Helm Chart"

    **Update Helm repository and upgrade**:
    ```bash
    # Update Helm repository
    helm repo update pipeops

    # Check available versions
    helm search repo pipeops/pipeops-agent --versions

    # Upgrade to latest version
    helm upgrade pipeops-agent pipeops/pipeops-agent

    # Upgrade to specific version
    helm upgrade pipeops-agent pipeops/pipeops-agent --version=1.2.3
    ```

    **Upgrade with custom values**:
    ```bash
    # Upgrade preserving custom configuration
    helm upgrade pipeops-agent pipeops/pipeops-agent -f values-custom.yaml

    # Check upgrade status
    helm status pipeops-agent
    ```

=== "Docker"

    **Update Docker container**:
    ```bash
    # Pull latest image
    docker pull pipeops/agent:latest

    # Stop and remove old container
    docker stop pipeops-agent
    docker rm pipeops-agent

    # Start with new image (preserving volumes)
    docker run -d \
      --name pipeops-agent \
      --restart unless-stopped \
      -v /var/run/docker.sock:/var/run/docker.sock \
      -v /etc/pipeops:/etc/pipeops \
      -e PIPEOPS_CLUSTER_NAME="your-cluster" \
      -e PIPEOPS_TOKEN="your-token" \
      pipeops/agent:latest
    ```

=== "Binary"

    **Manual binary update**:
    ```bash
    # Download latest version
    ARCH=$(uname -m)
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    curl -LO "https://github.com/PipeOpsHQ/pipeops-k8-agent/releases/latest/download/pipeops-agent-${OS}-${ARCH}.tar.gz"

    # Stop service
    sudo systemctl stop pipeops-agent

    # Backup current binary
    sudo cp /usr/local/bin/pipeops-agent /usr/local/bin/pipeops-agent.backup

    # Install new version
    tar -xzf pipeops-agent-${OS}-${ARCH}.tar.gz
    sudo mv pipeops-agent /usr/local/bin/
    sudo chmod +x /usr/local/bin/pipeops-agent

    # Start service
    sudo systemctl start pipeops-agent

    # Verify update
    pipeops-agent version
    ```

### Update Verification

After updating, verify the installation:

```bash
# Check agent version
pipeops-agent version

# Verify service status
sudo systemctl status pipeops-agent

# Check connectivity
pipeops-agent status

# Run health check
pipeops-agent diagnose
```

### Rolling Back Updates

If you encounter issues after an update:

=== "Script/Binary Installation"

    ```bash
    # Stop service
    sudo systemctl stop pipeops-agent

    # Restore backup
    sudo cp /usr/local/bin/pipeops-agent.backup /usr/local/bin/pipeops-agent

    # Start service
    sudo systemctl start pipeops-agent
    ```

=== "Helm Chart"

    ```bash
    # View upgrade history
    helm history pipeops-agent

    # Rollback to previous version
    helm rollback pipeops-agent

    # Rollback to specific revision
    helm rollback pipeops-agent 2
    ```

=== "Docker"

    ```bash
    # Use specific image version
    docker stop pipeops-agent && docker rm pipeops-agent
    
    docker run -d \
      --name pipeops-agent \
      --restart unless-stopped \
      -v /var/run/docker.sock:/var/run/docker.sock \
      -v /etc/pipeops:/etc/pipeops \
      -e PIPEOPS_CLUSTER_NAME="your-cluster" \
      -e PIPEOPS_TOKEN="your-token" \
      pipeops/agent:v1.1.0  # Specify previous version
    ```

## Uninstalling

If you need to remove the PipeOps Agent from your system:

### Complete Removal

=== "Script Installation"

    **Using the uninstall command**:
    ```bash
    # Uninstall agent and cleanup
    sudo pipeops-agent uninstall

    # Remove all data (optional)
    sudo pipeops-agent uninstall --purge-data
    ```

    **Manual removal**:
    ```bash
    # Stop and disable service
    sudo systemctl stop pipeops-agent
    sudo systemctl disable pipeops-agent

    # Remove service file
    sudo rm /etc/systemd/system/pipeops-agent.service
    sudo systemctl daemon-reload

    # Remove binary
    sudo rm /usr/local/bin/pipeops-agent

    # Remove configuration (optional)
    sudo rm -rf /etc/pipeops

    # Remove logs (optional)
    sudo rm -rf /var/log/pipeops

    # Remove k3s (if installed by agent)
    if [ -f /usr/local/bin/k3s-uninstall.sh ]; then
        sudo /usr/local/bin/k3s-uninstall.sh
    fi
    ```

=== "Helm Chart"

    **Uninstall Helm release**:
    ```bash
    # Uninstall the agent
    helm uninstall pipeops-agent

    # Remove namespace (optional)
    kubectl delete namespace pipeops-system

    # Remove custom resources (optional)
    kubectl delete crd pipeopsagents.pipeops.io
    ```

    **Complete cleanup**:
    ```bash
    # Remove all PipeOps resources
    kubectl delete all -l app.kubernetes.io/name=pipeops-agent --all-namespaces
    kubectl delete configmap -l app.kubernetes.io/name=pipeops-agent --all-namespaces
    kubectl delete secret -l app.kubernetes.io/name=pipeops-agent --all-namespaces

    # Remove RBAC resources
    kubectl delete clusterrole pipeops-agent
    kubectl delete clusterrolebinding pipeops-agent
    ```

=== "Docker"

    **Remove Docker container and data**:
    ```bash
    # Stop and remove container
    docker stop pipeops-agent
    docker rm pipeops-agent

    # Remove image (optional)
    docker rmi pipeops/agent:latest

    # Remove volumes (optional)
    docker volume rm $(docker volume ls -q | grep pipeops)

    # Remove configuration
    sudo rm -rf /etc/pipeops
    ```

=== "Kubernetes Manifest"

    **Remove deployed resources**:
    ```bash
    # Delete the deployment
    kubectl delete -f agent.yaml

    # Or delete by labels
    kubectl delete all -l app=pipeops-agent -n pipeops-system

    # Remove namespace
    kubectl delete namespace pipeops-system
    ```

### Partial Removal

Remove only specific components:

```bash
# Remove only monitoring stack (keep agent)
pipeops-agent monitoring uninstall

# Remove only the agent (keep Kubernetes cluster)
pipeops-agent uninstall --keep-cluster

# Remove configuration but keep logs
sudo rm -rf /etc/pipeops
# Keep: /var/log/pipeops
```

### Cleanup Verification

Verify complete removal:

```bash
# Check for running processes
ps aux | grep pipeops

# Check for remaining files
find / -name "*pipeops*" 2>/dev/null

# Check Kubernetes resources
kubectl get all -A | grep pipeops

# Check system services
systemctl list-units | grep pipeops

# Check Docker containers and images
docker ps -a | grep pipeops
docker images | grep pipeops
```

### Preserving Data

If you plan to reinstall later, you can preserve certain data:

```bash
# Backup configuration
sudo cp -r /etc/pipeops /tmp/pipeops-config-backup

# Backup logs
sudo cp -r /var/log/pipeops /tmp/pipeops-logs-backup

# Export Kubernetes configuration
kubectl get configmap pipeops-agent-config -n pipeops-system -o yaml > pipeops-k8s-config.yaml

# Backup monitoring data (if using persistent volumes)
kubectl get pv | grep pipeops
```

### Post-Uninstall

After uninstalling:

1. **Remove from PipeOps Dashboard**: 
   - Go to [console.pipeops.io](https://console.pipeops.io)
   - Navigate to "Infrastructure" → "Clusters"
   - Remove the disconnected cluster

2. **Clean up firewall rules** (if configured):
   ```bash
   # Remove PipeOps-specific rules
   sudo ufw delete allow from any to any port 6443
   sudo ufw delete allow from any to any port 8080
   ```

3. **Verify system resources**:
   ```bash
   # Check remaining disk usage
   df -h
   
   # Check memory usage
   free -h
   
   # Check running services
   systemctl list-units --state=running
   ```

## Next Steps

After successful installation:

1. [**Quick Start Guide**](quick-start.md) - Get familiar with basic operations
2. [**Configuration**](configuration.md) - Customize your agent setup  
3. [**Architecture Overview**](../ARCHITECTURE.md) - Understand the system design
4. [**Advanced Monitoring**](../advanced/monitoring.md) - Set up comprehensive observability

---

!!! tip "Pro Tip"
    
    Enable debug logging during initial setup to troubleshoot any issues:
    ```bash
    export PIPEOPS_LOG_LEVEL=debug
    pipeops-agent start
    ```