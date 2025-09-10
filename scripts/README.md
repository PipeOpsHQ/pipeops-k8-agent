# PipeOps Installation Scripts

This directory contains scripts for installing and managing PipeOps agents on k3s clusters.

## Scripts Overview

### `install.sh` - Main Installation Script

The primary script for installing k3s and the PipeOps agent. Supports both server (master) and worker node installation.

#### Server Node Installation

```bash
# Set required environment variables
export AGENT_TOKEN="your-pipeops-token"
export CLUSTER_NAME="my-production-cluster"

# Run the installer
./install.sh
```

#### Worker Node Installation

```bash
# Set environment variables for worker node
export NODE_TYPE="agent"
export K3S_URL="https://master-ip:6443"
export K3S_TOKEN="cluster-token-from-master"

# Run the installer
./install.sh
```

#### Available Commands

```bash
./install.sh                # Install (default)
./install.sh install        # Install explicitly
./install.sh uninstall      # Remove k3s and agent
./install.sh update          # Update agent to latest version
./install.sh cluster-info    # Show cluster connection info for workers
./install.sh help            # Show usage information
```

### `join-worker.sh` - Simplified Worker Join Script

A simplified script specifically for joining worker nodes to an existing cluster.

```bash
# Usage
./join-worker.sh <MASTER_IP> <CLUSTER_TOKEN>

# Example
./join-worker.sh 192.168.1.100 K10abc123def456::server:abc123
```

## Environment Variables

### Server Node Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `AGENT_TOKEN` | PipeOps authentication token | Yes | - |
| `CLUSTER_NAME` | Cluster identifier | No | `default-cluster` |
| `PIPEOPS_API_URL` | PipeOps API endpoint | No | `https://api.pipeops.io` |
| `K3S_VERSION` | k3s version to install | No | `v1.28.3+k3s2` |
| `AGENT_IMAGE` | PipeOps agent Docker image | No | `pipeops/agent:latest` |
| `NAMESPACE` | Kubernetes namespace for agent | No | `pipeops-system` |

### Worker Node Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `NODE_TYPE` | Set to `agent` for worker nodes | Yes | `server` |
| `K3S_URL` | Master server URL | Yes | - |
| `K3S_TOKEN` | Cluster token from master | Yes | - |
| `K3S_VERSION` | k3s version to install | No | `v1.28.3+k3s2` |

## Deployment Scenarios

### Single Node Cluster

Perfect for development or small workloads:

```bash
export AGENT_TOKEN="your-token"
./install.sh
```

### Multi-Node Cluster

1. **Setup master node:**
   ```bash
   export AGENT_TOKEN="your-token"
   export CLUSTER_NAME="production-cluster"
   ./install.sh
   ```

2. **Get cluster connection info:**
   ```bash
   ./install.sh cluster-info
   ```

3. **Join worker nodes:**
   ```bash
   # On each worker node
   ./join-worker.sh <master-ip> <cluster-token>
   ```

### High Availability Cluster

For production environments with multiple master nodes:

1. **First master (bootstrap):**
   ```bash
   export AGENT_TOKEN="your-token"
   export CLUSTER_NAME="ha-cluster"
   # Add cluster-init flag manually after installation
   ```

2. **Additional masters:**
   ```bash
   export NODE_TYPE="server"
   export K3S_URL="https://first-master:6443"
   export K3S_TOKEN="cluster-token"
   export AGENT_TOKEN="your-token"
   ./install.sh
   ```

## Special Environments

### Proxmox LXC Containers

The script automatically detects LXC environments and applies appropriate configurations:

- Disables Traefik and ServiceLB
- Uses host-gw flannel backend
- Sets appropriate kube-proxy settings

### Cloud Providers

Works on all major cloud providers:
- AWS EC2
- Google Cloud Compute Engine
- Azure Virtual Machines
- DigitalOcean Droplets
- Linode
- Vultr

## Troubleshooting

### Common Issues

#### "AGENT_TOKEN is required"
```bash
export AGENT_TOKEN="your-actual-token-here"
./install.sh
```

#### "Could not determine server IP"
```bash
export SERVER_IP="your-server-ip"
./install.sh
```

#### Worker node connection fails
```bash
# Check master node firewall allows port 6443
# Verify token and URL are correct
./install.sh cluster-info  # Run on master to get correct values
```

#### k3s fails to start in LXC
```bash
# Ensure LXC container has appropriate capabilities
# Check if the script detected LXC correctly
```

### Debug Mode

Enable debug logging:

```bash
export PIPEOPS_LOG_LEVEL=debug
./install.sh
```

### Log Locations

- **k3s server logs:** `journalctl -u k3s -f`
- **k3s agent logs:** `journalctl -u k3s-agent -f`
- **PipeOps agent logs:** `kubectl logs -f deployment/pipeops-agent -n pipeops-system`

## Security Considerations

### Network Security

- Only outbound connections required from worker nodes
- k3s API (port 6443) only needs to be accessible from worker nodes
- Agent communicates securely with PipeOps control plane via TLS

### Firewall Rules

#### Master Node
```bash
# Allow k3s API access from worker nodes
ufw allow from <worker-subnet> to any port 6443

# Optional: Allow kubectl access from management networks
ufw allow from <management-subnet> to any port 6443
```

#### Worker Nodes
```bash
# No inbound ports required
# Only outbound HTTPS (443) to PipeOps API needed
```

### Token Security

- Store cluster tokens securely
- Rotate tokens regularly using k3s built-in token rotation
- Use unique tokens per cluster

## Performance Tuning

### Resource Requirements

#### Minimum (Development)
- **CPU:** 1 core
- **Memory:** 512 MB
- **Disk:** 2 GB

#### Recommended (Production)
- **CPU:** 2+ cores
- **Memory:** 2+ GB
- **Disk:** 10+ GB SSD

### Network Requirements

- **Bandwidth:** 1 Mbps minimum, 10 Mbps recommended
- **Latency:** <100ms between nodes, <500ms to PipeOps API

## Backup and Recovery

### Cluster Backup

```bash
# Backup k3s data
sudo tar czf k3s-backup-$(date +%Y%m%d).tar.gz /var/lib/rancher/k3s

# Backup PipeOps config
kubectl get secret pipeops-agent-config -n pipeops-system -o yaml > pipeops-config-backup.yaml
```

### Cluster Recovery

```bash
# Restore k3s data (on clean system)
sudo tar xzf k3s-backup-YYYYMMDD.tar.gz -C /

# Reinstall k3s and agent
./install.sh

# Restore PipeOps config if needed
kubectl apply -f pipeops-config-backup.yaml
```

## Support

For issues with these scripts:

1. Check the troubleshooting section above
2. Review logs for error messages
3. Ensure all prerequisites are met
4. Contact PipeOps support with detailed error information

## Contributing

When modifying these scripts:

1. Test on multiple Linux distributions
2. Verify both server and worker installation modes
3. Test in LXC/container environments
4. Update documentation for any new features or requirements
