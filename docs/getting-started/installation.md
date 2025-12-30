# Installation

Welcome to the PipeOps Kubernetes Agent installation guide. The agent provides secure cluster management and optional gateway proxy access for your Kubernetes clusters.

## Platform-Specific Guides

- **[Windows Installation](windows-installation.md)** - Complete guide for Windows 10/11 with WSL2, Docker Desktop, and Minikube
- **[Multi-VM Cluster Setup](multi-vm-cluster.md)** - High availability and disaster recovery across multiple machines
- **Linux Installation** - Continue reading below for Ubuntu, Debian, RHEL, and other Linux distributions

## Installation Overview

The PipeOps agent can be installed in two ways:

1. **Automated Installer** (Recommended) - Bootstraps a complete Kubernetes environment with auto-detection
2. **Existing Cluster** - Deploy agent-only to your existing Kubernetes cluster

**Important Security Note**: The PipeOps Gateway Proxy feature is **DISABLED by default**. The agent provides secure cluster access via WebSocket tunnel without exposing your cluster externally unless you explicitly enable ingress sync.

## Method 1: Automated Installer (Recommended)

The intelligent installer automatically detects your environment and creates a production-ready Kubernetes cluster with the PipeOps agent.

### Prerequisites

- `curl` and `bash`
- A PipeOps control plane token with cluster registration permissions
- **For k3s (production)**: Root privileges (run with `sudo` or as root)
- **For k3d/kind/minikube (development)**: Docker installed and **run as regular user** (do not use `sudo`)
- Optional: Virtualization enabled (for minikube with VM driver)

### Step 1: Export Environment Variables

```bash
# Required: Your PipeOps token
export PIPEOPS_TOKEN="your-pipeops-token"

# Optional but recommended for clarity in the dashboard
export CLUSTER_NAME="my-pipeops-cluster"

# Optional: pin a specific distribution (k3s|minikube|k3d|kind|auto)
# export CLUSTER_TYPE="auto"
```

### Step 2: Run the Installer

#### Auto-Detection (Recommended)

```bash
# The installer will automatically detect the best cluster type for your environment
# Production k3s on Linux VMs:
curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
# Development clusters (k3d/kind/minikube): omit sudo
# curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

#### Production k3s (Requires Root)

```bash
# Use sudo for k3s on production servers/VMs
curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
```

#### Development Clusters (Do NOT Use Sudo)

```bash
# For k3d, kind, or minikube - run as regular user WITHOUT sudo
export CLUSTER_TYPE="k3d"  # or "kind" or "minikube"
curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

> **Why pipe into `bash`?** Some hardened distros disable `/dev/fd`, which breaks process-substitution (`bash <(curl ...)`) with errors like `bash: /dev/fd/63: No such file or directory`. Streaming the script into `bash` avoids that limitation.
>
> **Important:** Development cluster types (k3d, kind, minikube) use Docker and **must NOT** be run with sudo, as Docker Desktop is not available to the root user on macOS and will fail.

### What the Installer Does

The automated installer:

1. Analyzes system resources and environment (CPU, RAM, disks, Docker, virtualization, cloud vendor)
2. Selects the optimal Kubernetes distribution (or honors your `CLUSTER_TYPE` setting)
3. Bootstraps a Kubernetes cluster
4. Deploys the PipeOps agent in `pipeops-system` namespace
5. Installs monitoring stack (Prometheus, Loki, Grafana) in `pipeops-monitoring` namespace - **Optional**, disable with `INSTALL_MONITORING=false`
6. Detects cluster type (private vs public) for optimal routing
7. Prints connection details for additional worker nodes

**Note on Gateway Proxy**: The installer detects if your cluster is private (no public LoadBalancer IP) or public. However, the gateway proxy feature (ingress sync) remains **DISABLED by default** for security. See the [Gateway Proxy Configuration](#gateway-proxy-configuration) section to enable it.

### Component Installation Behavior

The agent's component auto-installation behaves differently based on the installation method:

#### Bash Installer (Fresh Clusters)
When using the bash installer, the agent automatically installs monitoring and essential components:

- **Auto-install enabled** (`PIPEOPS_AUTO_INSTALL_COMPONENTS=true` is set automatically)
- **Components installed**: Metrics Server, Vertical Pod Autoscaler (VPA), Prometheus, Loki, Grafana, NGINX Ingress Controller (if needed)
- **Best for**: Fresh cluster installations, development environments, all-in-one deployments

#### Helm/Kubernetes Manifests (Existing Clusters)
When deploying via Helm or raw Kubernetes manifests:

- **Auto-install disabled by default** (`agent.autoInstallComponents: false`)
- **Agent only provides**: Secure tunnel, cluster management, heartbeat
- **Assumes**: You have existing monitoring infrastructure
- **Best for**: Production clusters with existing monitoring, enterprise environments

To enable auto-installation with Helm:
```bash
helm install pipeops-agent ./helm/pipeops-agent \
  --set agent.pipeops.token="your-token" \
  --set agent.cluster.name="my-cluster" \
  --set agent.autoInstallComponents=true  # Enable auto-install
```

**Why this matters**: This design prevents conflicts with existing monitoring stacks while still providing easy setup for fresh installations.

### Step 3: Verify the Installation

```bash
kubectl get pods -n pipeops-system
kubectl get pods -n pipeops-monitoring
```

You should see the agent pod in `Running` state. If monitoring was installed, you'll also see Prometheus, Grafana, Loki pods.

### Step 4: Join Additional Worker Nodes (Optional)

For multi-node clusters, run this on each worker machine:

```bash
export K3S_URL="https://<server-ip>:6443"
export K3S_TOKEN="<token printed by install.sh>"
curl -fsSL https://get.pipeops.dev/k8-join-worker.sh | bash
```

Alternatively, rerun `install.sh cluster-info` on the server to display the join command.

### Step 5: Verify the Installation

```bash
kubectl get pods -n pipeops-system
kubectl get pods -n pipeops-monitoring
```

You should see the agent pod in `Running` state. If monitoring was installed, you'll also see Prometheus, Grafana, Loki pods.

### Uninstalling

When you need to remove the agent and/or cluster:

#### Remove Only the Agent (Keep Kubernetes Cluster)

```bash
# Remove agent only (keeps k3s/k3d/kind/minikube)
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash -s -- --force

# Or with environment variable
FORCE=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash
```

#### Remove Agent AND Kubernetes Cluster

```bash
# Remove everything (agent + cluster)
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash -s -- --force --uninstall-k3s

# Or with environment variables
FORCE=true UNINSTALL_K3S=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash
```

#### Keep Data While Uninstalling

```bash
# Remove agent but keep persistent volumes
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash -s -- --force --keep-data
```

#### Interactive Uninstall (For Downloaded Script)

```bash
# Download the uninstall script
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh -o uninstall.sh
chmod +x uninstall.sh

# Run interactively (will prompt for confirmation)
./uninstall.sh

# View all options
./uninstall.sh --help
```

## Method 2: Install on Existing Kubernetes Cluster

Already running Kubernetes? Deploy just the PipeOps agent without creating a new cluster.

**Use Case**: When you want to securely expose an existing cluster to PipeOps for management without any external gateway access (secure admin access only).

### Prerequisites

- `kubectl` configured with cluster-admin privileges
- `sed` (available by default on macOS/Linux) for manifest substitution
- PipeOps control plane credentials (`PIPEOPS_TOKEN`)

### Step 1: Export Required Values

```bash
export PIPEOPS_TOKEN="your-pipeops-token"
export PIPEOPS_CLUSTER_NAME="my-existing-cluster"
```

#### Environment Variables

| Variable | Required | Description |
| --- | --- | --- |
| `PIPEOPS_TOKEN` | Yes | Control plane token with cluster registration permissions |
| `PIPEOPS_CLUSTER_NAME` | Yes | Friendly name displayed in PipeOps dashboard |
| `PIPEOPS_ENVIRONMENT` | No | Set to `dev`, `staging`, or `production` for resource profiling |
| `PIPEOPS_API_URL` | No | Override API endpoint (default: `https://api.pipeops.io`) |
| `INSTALL_MONITORING` | No | Set to `false` to skip monitoring stack (default: `false` for existing clusters) |
| `ENABLE_INGRESS_SYNC` | No | Set to `true` to enable PipeOps Gateway Proxy (default: `false`) |

**Security Default**: When installing on an existing cluster via manifest, `INSTALL_MONITORING` and `ENABLE_INGRESS_SYNC` both default to `false`. This treats the installation as secure admin access only without component installation or external exposure.

### Step 2: Apply the Manifest

#### Option A: Using sed (Recommended)

```bash
curl -fsSL https://get.pipeops.dev/k8-agent.yaml \
  | sed "s/PIPEOPS_TOKEN: \"your-token-here\"/PIPEOPS_TOKEN: \"${PIPEOPS_TOKEN}\"/" \
  | sed "s/token: \"your-token-here\"/token: \"${PIPEOPS_TOKEN}\"/" \
  | sed "s/cluster_name: \"default-cluster\"/cluster_name: \"${PIPEOPS_CLUSTER_NAME}\"/" \
  | kubectl apply -f -
```

#### Option B: kubectl only (No sed required)

```bash
# Apply core resources (namespace, RBAC, deployment)
kubectl apply -f https://get.pipeops.dev/k8-agent.yaml \
  --selector app.kubernetes.io/component!=config

# Create/update the secret with your token
kubectl create secret generic pipeops-agent-config -n pipeops-system \
  --from-literal=PIPEOPS_TOKEN="${PIPEOPS_TOKEN}" \
  --from-literal=PIPEOPS_CLUSTER_NAME="${PIPEOPS_CLUSTER_NAME}" \
  --from-literal=PIPEOPS_API_URL="https://api.pipeops.io" \
  --dry-run=client -o yaml | kubectl apply -f -

# Create/update the ConfigMap
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: pipeops-agent-config
  namespace: pipeops-system
data:
  config.yaml: |
    agent:
      id: ""
      name: "pipeops-agent"
      cluster_name: "${PIPEOPS_CLUSTER_NAME}"
      enable_ingress_sync: true  # Enabled by default - auto-detects routing mode
      labels:
        environment: "production"
        managed-by: "pipeops"
    pipeops:
      api_url: "https://api.pipeops.io"
      token: "${PIPEOPS_TOKEN}"
      timeout: "30s"
      reconnect:
        enabled: true
        max_attempts: 10
        interval: "5s"
        backoff: "5s"
      tls:
        enabled: true
        insecure_skip_verify: false
    tunnel:
      enabled: true
      inactivity_timeout: "5m"
      forwards:
        - name: "kubernetes-api"
          local_addr: "localhost:6443"
          remote_port: 0
        - name: "kubelet-metrics"
          local_addr: "localhost:10250"
          remote_port: 0
        - name: "agent-http"
          local_addr: "localhost:8080"
          remote_port: 0
    kubernetes:
      in_cluster: true
      namespace: "pipeops-system"
    logging:
      level: "info"
      format: "json"
      output: "stdout"
EOF
```

### Step 3: Verify the Installation

```bash
kubectl rollout status deployment/pipeops-agent -n pipeops-system
kubectl logs deployment/pipeops-agent -n pipeops-system
```

Expected logs:
```text
INFO  Agent registered successfully via WebSocket
INFO  Cluster registered successfully
INFO  Connection state changed new_state=connected
INFO  Ingress sync disabled - agent will not expose cluster via gateway proxy
```

### Step 4: Uninstall (If Needed)

#### Remove Agent Only

```bash
# Delete the agent namespace
kubectl delete namespace pipeops-system --ignore-not-found

# Delete monitoring namespace if it was installed
kubectl delete namespace pipeops-monitoring --ignore-not-found

# Clean up RBAC resources
kubectl delete clusterrolebinding pipeops-agent --ignore-not-found
kubectl delete clusterrole pipeops-agent --ignore-not-found
```

#### Using the Uninstall Script (Recommended)

```bash
# Remove agent only (interactive - will prompt for confirmation)
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh -o uninstall.sh
bash uninstall.sh

# Force removal without confirmation
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash -s -- --force

# Or with environment variable
FORCE=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash
```

## Gateway Proxy Configuration

**By default, the gateway proxy is DISABLED** for security. The agent provides secure admin access to your cluster via WebSocket tunnel without exposing it externally.

### When to Enable Gateway Proxy

Enable the gateway proxy only if you want to:
- Expose services in private clusters without VPN
- Use custom domains for cluster services
- Provide external access to applications via PipeOps gateway

### How to Enable Gateway Proxy

Add this to your agent ConfigMap:

```yaml
agent:
  enable_ingress_sync: true  # Default: true (automatically detects routing mode)
```

Or set via environment variable:
```bash
export ENABLE_INGRESS_SYNC=true
```

Then restart the agent:
```bash
kubectl rollout restart deployment/pipeops-agent -n pipeops-system
```

### What Happens When Enabled

When enabled, the agent:

1. Detects cluster type (private vs public)
2. Watches all ingress resources across namespaces
3. Extracts route information (host, path, service, port)
4. Registers routes with PipeOps control plane via REST API
5. Enables external access through PipeOps gateway

**Routing Modes:**
- **Private Cluster** (no public LoadBalancer IP): Uses tunnel routing through WebSocket
- **Public Cluster** (has LoadBalancer IP): Uses direct routing (3-5x faster)

See [PipeOps Gateway Proxy Documentation](../advanced/pipeops-gateway-proxy.md) for complete details.

## Alternative Installation Methods

=== "Helm Chart"

    **Prerequisites**: Helm 3.8+ and kubectl configured

    **Installation**:
    ```bash
    # Install the agent directly from GHCR
    helm install pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent \
      --set agent.pipeops.token="your-pipeops-token" \
      --set agent.cluster.name="your-cluster-name"
    ```

    **Custom Configuration**:
    ```bash
    # Create custom values file
    cat > values-custom.yaml << EOF
    agent:
      pipeops:
        token: "your-pipeops-token"
      cluster:
        name: "production-cluster"
      enable_ingress_sync: true  # Default: enabled (auto-detects routing mode)
      resources:
        requests:
          memory: "256Mi"
          cpu: "250m"
        limits:
          memory: "512Mi"
          cpu: "500m"
    EOF

    # Install with custom configuration
    helm install pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent -f values-custom.yaml
    ```

    **Uninstallation**:
    ```bash
    # Uninstall the Helm release
    helm uninstall pipeops-agent

    # Clean up namespace if needed
    kubectl delete namespace pipeops-system --ignore-not-found
    kubectl delete namespace pipeops-monitoring --ignore-not-found

    # Remove RBAC resources
    kubectl delete clusterrolebinding pipeops-agent --ignore-not-found
    kubectl delete clusterrole pipeops-agent --ignore-not-found
    ```

=== "Docker"

    **Prerequisites**: Docker 20.0+ and access to Docker socket

    **Installation**:
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

    **Uninstallation**:
    ```bash
    # Stop and remove the container
    docker stop pipeops-agent
    docker rm pipeops-agent

    # Remove the image (optional)
    docker rmi pipeops/agent:latest

    # Clean up volumes if needed
    docker volume prune

    # For Docker Compose
    docker-compose down
    docker-compose down -v  # Also remove volumes
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

    **Uninstallation**:
    ```bash
    # Stop the agent if running as a service
    sudo systemctl stop pipeops-agent
    sudo systemctl disable pipeops-agent

    # Remove the binary
    sudo rm /usr/local/bin/pipeops-agent

    # Remove config files
    sudo rm -rf /etc/pipeops

    # Remove systemd service file if created
    sudo rm /etc/systemd/system/pipeops-agent.service
    sudo systemctl daemon-reload

    # Remove data directory
    sudo rm -rf /var/lib/pipeops
    ```

## System Requirements

### Minimum Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| CPU | 1 core | 2+ cores |
| Memory | 512MB | 1GB+ |
| Storage | 1GB | 5GB+ |
| Network | Outbound HTTPS (443) | Outbound HTTPS + SSH (22) |

### Software Dependencies

**Linux (Ubuntu/Debian)**:
```bash
sudo apt update
sudo apt install -y curl wget gnupg2 software-properties-common
```

**Linux (CentOS/RHEL/Fedora)**:
```bash
sudo yum update -y
sudo yum install -y curl wget gnupg2
```

**macOS** (Homebrew):
```bash
# Install Homebrew if not present
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Install dependencies
brew install curl wget
```

### Kubernetes Requirements

For existing Kubernetes clusters:

- **Kubernetes version**: 1.20+
- **kubectl**: Configured with cluster access
- **RBAC permissions**: Cluster admin or sufficient permissions
- **Container runtime**: Docker, containerd, or CRI-O

## Verification

After installation, verify the agent is running correctly:

### 1. Check Pod Status

```bash
# Check agent pod
kubectl get pods -n pipeops-system

# Expected output
NAME                             READY   STATUS    RESTARTS   AGE
pipeops-agent-xxxxxxxxxx-xxxxx   1/1     Running   0          2m
```

### 2. Check Agent Logs

```bash
kubectl logs deployment/pipeops-agent -n pipeops-system | tail -20
```

Expected output:
```text
INFO  Starting PipeOps Agent version=1.x.x
INFO  Agent ID loaded from persistent state
INFO  Kubernetes client initialized
INFO  Tunnel disabled - agent will not establish reverse tunnels
INFO  Starting PipeOps agent...
INFO  Agent registered successfully via WebSocket
INFO  Cluster registered successfully
INFO  Connection state changed new_state=connected
INFO  Ingress sync disabled - agent will not expose cluster via gateway proxy
INFO  PipeOps agent started successfully
```

### 3. Test Connectivity

```bash
# Check agent health endpoint
kubectl port-forward -n pipeops-system deployment/pipeops-agent 8080:8080 &
curl http://localhost:8080/health

# Expected response
{"status":"healthy","timestamp":"2024-01-15T10:30:00Z"}
```

## Troubleshooting

### Agent Not Connecting

**Symptoms**: Agent logs show connection errors

**Solutions**:
1. Verify token is correct:
```bash
kubectl get secret pipeops-agent-config -n pipeops-system -o yaml
```

2. Check network connectivity:
```bash
kubectl exec -n pipeops-system deployment/pipeops-agent -- curl -I https://api.pipeops.io
```

3. Verify firewall allows outbound HTTPS (port 443)

### Pod CrashLoopBackOff

**Symptoms**: Agent pod keeps restarting

**Solutions**:
1. Check pod logs:
```bash
kubectl logs -n pipeops-system deployment/pipeops-agent --previous
```

2. Verify ConfigMap is correct:
```bash
kubectl get configmap pipeops-agent-config -n pipeops-system -o yaml
```

3. Check resource constraints:
```bash
kubectl describe pod -n pipeops-system -l app=pipeops-agent
```

### Monitoring Stack Not Starting

**Symptoms**: Monitoring pods in error state (if monitoring was enabled)

**Solutions**:
1. Check storage class:
```bash
kubectl get storageclass
```

2. Verify Helm is installed:
```bash
helm version
```

3. Check CRDs:
```bash
kubectl get crd | grep monitoring.coreos.com
```

## Upgrading

### Automated Installer Upgrade

```bash
# Re-run the installer (preserves configuration)
# Production k3s on Linux VMs:
curl -fsSL https://get.pipeops.dev/k8-install.sh | sudo bash
# Development clusters (k3d/kind/minikube): omit sudo
# curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

### Manual Upgrade

```bash
# Update to latest version
kubectl set image deployment/pipeops-agent pipeops-agent=pipeops/agent:latest -n pipeops-system

# Check rollout status
kubectl rollout status deployment/pipeops-agent -n pipeops-system
```

### Helm Upgrade

```bash
# Upgrade to latest version
helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent

# Upgrade with custom values
helm upgrade pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent -f values-custom.yaml
```

## Next Steps

After successful installation:

1. [Quick Start Guide](quick-start.md) - Get familiar with basic operations
2. [Configuration](configuration.md) - Customize your agent setup
3. [Multi-VM Cluster Setup](multi-vm-cluster.md) - Add worker nodes for high availability
4. [Windows Installation](windows-installation.md) - Install on Windows systems
5. [FAQ](faq.md) - Frequently asked questions
6. [Architecture Overview](../ARCHITECTURE.md) - Understand the system design
7. [PipeOps Gateway Proxy](../advanced/pipeops-gateway-proxy.md) - Learn about optional external access
8. [Monitoring](../advanced/monitoring.md) - Set up comprehensive observability

---

**Pro Tip**: Enable debug logging during initial setup to troubleshoot issues:
```bash
export PIPEOPS_LOG_LEVEL=debug
kubectl rollout restart deployment/pipeops-agent -n pipeops-system
```
