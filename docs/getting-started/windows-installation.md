# Windows Installation Guide

This guide covers installing the PipeOps Kubernetes Agent on Windows systems using Windows Subsystem for Linux (WSL2) or native Windows Kubernetes distributions.

## Installation Methods

There are three primary methods to run the PipeOps agent on Windows:

1. **WSL2 with K3s/K3d** (Recommended) - Best performance and compatibility
2. **Docker Desktop Kubernetes** - Easiest for local development
3. **Minikube on Windows** - Alternative for development environments

## Method 1: WSL2 with K3s/K3d (Recommended)

WSL2 provides a full Linux kernel and is the recommended approach for running production-grade Kubernetes on Windows.

### Prerequisites

- Windows 10 version 2004+ or Windows 11
- WSL2 installed and configured
- At least 4GB RAM and 20GB disk space

### Step 1: Install WSL2

Open PowerShell as Administrator and run:

```powershell
# Enable WSL2
wsl --install

# Or if WSL is already installed, update to WSL2
wsl --set-default-version 2
```

Install Ubuntu (recommended distribution):

```powershell
wsl --install -d Ubuntu-22.04
```

Restart your computer if prompted.

### Step 2: Configure WSL2 Resources

Create or edit `C:\Users\<YourUsername>\.wslconfig`:

```ini
[wsl2]
memory=4GB
processors=2
swap=2GB
```

Restart WSL:

```powershell
wsl --shutdown
```

### Step 3: Install PipeOps Agent in WSL2

Open your WSL2 Ubuntu terminal:

```bash
# Update package list
sudo apt update && sudo apt upgrade -y

# Install prerequisites
sudo apt install -y curl wget

# Set your PipeOps token
export PIPEOPS_TOKEN="your-pipeops-token-here"
export CLUSTER_NAME="windows-k3s-cluster"

# Run the automated installer
curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

The installer will automatically:
- Detect WSL2 environment
- Install K3d (Kubernetes in Docker) or K3s
- Deploy the PipeOps agent
- Set up monitoring stack

### Step 4: Verify Installation

```bash
# Check cluster status
kubectl get nodes

# Check PipeOps agent
kubectl get pods -n pipeops-system

# View agent logs
kubectl logs -f deployment/pipeops-agent -n pipeops-system
```

### Uninstalling from WSL2

When you need to remove the agent and/or cluster:

#### Remove Only the Agent (Keep Kubernetes)

```bash
# Inside WSL2 terminal
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash -s -- --force
```

#### Remove Agent AND Kubernetes Cluster

```bash
# Inside WSL2 terminal - removes everything
FORCE=true UNINSTALL_K3S=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash
```

#### Interactive Uninstall

```bash
# Download and run interactively
curl -fsSL https://get.pipeops.dev/k8-uninstall.sh -o uninstall.sh
bash uninstall.sh

# View all uninstall options
bash uninstall.sh --help
```

### Accessing Kubernetes from Windows

To use `kubectl` from Windows PowerShell:

1. Install kubectl for Windows:

```powershell
# Using Chocolatey
choco install kubernetes-cli

# Or download manually
curl.exe -LO "https://dl.k8s.io/release/v1.28.0/bin/windows/amd64/kubectl.exe"
```

2. Copy kubeconfig from WSL2:

```powershell
# Create .kube directory
mkdir $HOME\.kube

# Copy config from WSL2
wsl cat ~/.kube/config > $HOME\.kube\config
```

3. Test access:

```powershell
kubectl get nodes
```

## Method 2: Docker Desktop Kubernetes

Docker Desktop provides an integrated Kubernetes distribution that's easy to set up.

### Prerequisites

- Windows 10 Pro/Enterprise/Education or Windows 11
- Docker Desktop for Windows installed
- At least 4GB RAM available for Docker

### Step 1: Install Docker Desktop

1. Download Docker Desktop from [docker.com](https://www.docker.com/products/docker-desktop/)
2. Run the installer
3. Restart your computer

### Step 2: Enable Kubernetes

1. Open Docker Desktop
2. Go to Settings → Kubernetes
3. Check "Enable Kubernetes"
4. Click "Apply & Restart"
5. Wait for Kubernetes to start (green indicator)

### Step 3: Install PipeOps Agent

Open PowerShell and run:

```powershell
# Set environment variables
$env:PIPEOPS_TOKEN = "your-pipeops-token-here"
$env:PIPEOPS_CLUSTER_NAME = "docker-desktop-cluster"

# Download and apply agent manifest
kubectl apply -f https://get.pipeops.dev/k8-agent.yaml
```

Or deploy via WSL2 with the automated installer:

```bash
# Open WSL2 terminal
wsl

# Run installer (will detect Docker Desktop Kubernetes)
export PIPEOPS_TOKEN="your-pipeops-token-here"
export CLUSTER_NAME="docker-desktop-cluster"
curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

### Step 4: Verify Installation

```powershell
# Check nodes
kubectl get nodes

# Check PipeOps agent
kubectl get pods -n pipeops-system

# View logs
kubectl logs -l app=pipeops-agent -n pipeops-system
```

### Uninstalling from Docker Desktop

#### Remove Agent Only

```powershell
# Using kubectl
kubectl delete namespace pipeops-system --ignore-not-found
kubectl delete namespace pipeops-monitoring --ignore-not-found
kubectl delete clusterrolebinding pipeops-agent --ignore-not-found
kubectl delete clusterrole pipeops-agent --ignore-not-found
```

#### Using Uninstall Script (via WSL2)

```powershell
# Open WSL2 terminal
wsl

# In WSL2, run the uninstall script
FORCE=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash
```

#### Disable Kubernetes in Docker Desktop

1. Open Docker Desktop
2. Go to Settings → Kubernetes
3. Uncheck "Enable Kubernetes"
4. Click "Apply & Restart"

## Method 3: Minikube on Windows

Minikube is a lightweight Kubernetes distribution suitable for development.

### Prerequisites

- Windows 10/11
- Hyper-V or Docker Desktop
- At least 2GB RAM and 20GB disk space

### Step 1: Install Minikube

Using Chocolatey:

```powershell
choco install minikube
```

Or download manually from [minikube.sigs.k8s.io](https://minikube.sigs.k8s.io/docs/start/)

### Step 2: Start Minikube

```powershell
# Start with Docker driver (recommended if Docker Desktop is installed)
minikube start --driver=docker --memory=4096 --cpus=2

# Or with Hyper-V driver
minikube start --driver=hyperv --memory=4096 --cpus=2
```

### Step 3: Install PipeOps Agent

```powershell
# Set environment variables
$env:PIPEOPS_TOKEN = "your-pipeops-token-here"
$env:PIPEOPS_CLUSTER_NAME = "minikube-cluster"

# Switch to WSL2 for installation (recommended)
wsl

# In WSL2:
export PIPEOPS_TOKEN="your-pipeops-token-here"
export CLUSTER_NAME="minikube-cluster"
export CLUSTER_TYPE="minikube"
curl -fsSL https://get.pipeops.dev/k8-install.sh | bash
```

### Uninstalling from Minikube

#### Remove Agent Only

```bash
# In WSL2 terminal
FORCE=true curl -fsSL https://get.pipeops.dev/k8-uninstall.sh | bash
```

#### Remove Agent and Delete Minikube Cluster

```powershell
# In PowerShell - stops and deletes the entire minikube cluster
minikube stop
minikube delete

# This removes everything including the VM
```

#### Remove Minikube Completely

```powershell
# Stop and delete cluster
minikube delete --all

# Uninstall minikube (if installed via Chocolatey)
choco uninstall minikube

# Remove minikube directory
Remove-Item -Recurse -Force $HOME\.minikube
```

## Networking Considerations

### Firewall Configuration

The PipeOps agent only requires **outbound connections**:

- HTTPS (443) to `api.pipeops.sh`
- WebSocket (WSS) connections for tunneling

No inbound firewall rules are required.

### WSL2 Networking

WSL2 uses a virtualized network. To access services from Windows:

```bash
# Get WSL2 IP address
hostname -I | awk '{print $1}'
```

Access services at: `http://<wsl2-ip>:<port>`

### Port Forwarding

To expose services from WSL2 to Windows network:

```powershell
# Forward port 80 from WSL2 to Windows
netsh interface portproxy add v4tov4 listenport=80 listenaddress=0.0.0.0 connectport=80 connectaddress=<wsl2-ip>
```

## Performance Optimization

### WSL2 Performance Tips

1. **Store files in WSL2 filesystem** (not Windows drives):
   ```bash
   # Good: /home/user/projects
   # Bad: /mnt/c/Users/user/projects
   ```

2. **Increase WSL2 resources** in `.wslconfig`:
   ```ini
   [wsl2]
   memory=8GB
   processors=4
   swap=4GB
   localhostForwarding=true
   ```

3. **Enable systemd** (Ubuntu 22.04+):
   ```bash
   echo -e "[boot]\nsystemd=true" | sudo tee /etc/wsl.conf
   ```

### Docker Desktop Performance

1. Increase resources in Docker Desktop settings:
   - Memory: 4GB minimum, 8GB recommended
   - CPUs: 2 minimum, 4 recommended
   - Swap: 2GB
   - Disk: 60GB

2. Enable WSL2 backend for better performance

## Troubleshooting

### WSL2 Issues

**Issue: WSL2 not starting**

```powershell
# Check WSL status
wsl --status

# Update WSL
wsl --update

# Restart WSL
wsl --shutdown
```

**Issue: Network connectivity problems**

```bash
# In WSL2, check DNS
cat /etc/resolv.conf

# Reset network
sudo ip addr flush dev eth0
sudo dhclient eth0
```

**Issue: K3s/K3d not accessible**

```bash
# Check K3s status
sudo systemctl status k3s

# Restart K3s
sudo systemctl restart k3s

# Check logs
sudo journalctl -u k3s -f
```

### Docker Desktop Issues

**Issue: Kubernetes not starting**

1. Reset Kubernetes: Settings → Kubernetes → Reset Kubernetes Cluster
2. Restart Docker Desktop
3. Check disk space (need 10GB+ free)

**Issue: kubectl not connecting**

```powershell
# Verify kubectl context
kubectl config current-context

# Should show: docker-desktop

# Switch context if needed
kubectl config use-context docker-desktop
```

### Agent Installation Issues

**Issue: Agent pod in CrashLoopBackOff**

```bash
# Check logs
kubectl logs -f deployment/pipeops-agent -n pipeops-system

# Common causes:
# - Invalid PIPEOPS_TOKEN
# - Network connectivity to api.pipeops.sh
# - Insufficient resources
```

**Issue: Cannot connect to PipeOps control plane**

```bash
# Test connectivity
curl -v https://api.pipeops.sh/health

# Check from pod
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl -v https://api.pipeops.sh/health
```

## Multi-VM Setup on Windows

To create a multi-node cluster for high availability, see the [Multi-VM Cluster Setup Guide](multi-vm-cluster.md).

## Next Steps

- [Configure your cluster](configuration.md)
- [Set up monitoring](../advanced/monitoring.md)
- [Join additional nodes](multi-vm-cluster.md)
- [Enable gateway proxy](../advanced/pipeops-gateway-proxy.md)

## Additional Resources

- [WSL2 Documentation](https://docs.microsoft.com/en-us/windows/wsl/)
- [Docker Desktop Documentation](https://docs.docker.com/desktop/windows/)
- [Minikube Documentation](https://minikube.sigs.k8s.io/docs/)
- [K3s Documentation](https://docs.k3s.io/)
