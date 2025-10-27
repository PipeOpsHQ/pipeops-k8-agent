# Agent Management

Essential operations for managing your PipeOps Kubernetes Agent throughout its lifecycle.

## Quick Reference

| Operation | Command | Description |
|-----------|---------|-------------|
| **Check Status** | `pipeops-agent status` | View agent health and connectivity |
| **Update** | `sudo pipeops-agent update` | Upgrade to latest version |
| **Restart** | `sudo systemctl restart pipeops-agent` | Restart the agent service |
| **Uninstall** | `sudo pipeops-agent uninstall` | Complete removal |
| **Logs** | `sudo journalctl -u pipeops-agent -f` | View real-time logs |
| **Diagnose** | `pipeops-agent diagnose` | Run comprehensive health check |

## Status & Health Monitoring

### Check Agent Status

```bash
# Basic status check
pipeops-agent status

# Detailed health check
pipeops-agent diagnose

# Service status (Linux)  
sudo systemctl status pipeops-agent

# View recent logs
sudo journalctl -u pipeops-agent --no-pager -n 20
```

Expected healthy output:
```text
PipeOps Agent Status: ✅ Running
Control Plane: ✅ Connected (api.pipeops.io)
Kubernetes API: ✅ Accessible
Cluster Name: production-cluster
Agent Version: v1.2.3
Uptime: 2d 14h 32m
```

### Monitoring Key Metrics

```bash
# Resource usage
pipeops-agent resources

# Cluster information
pipeops-agent cluster info

# Network connectivity test
pipeops-agent network test

# Check for configuration issues
pipeops-agent validate config
```

## Updates & Upgrades

### Checking for Updates

```bash
# Check for available updates
pipeops-agent update check

# View current version
pipeops-agent version

# View update history
pipeops-agent update history
```

### Updating the Agent

=== "Automatic Update"

    ```bash
    # Enable automatic updates (development environments)
    pipeops-agent config set auto-update.enabled=true
    pipeops-agent config set auto-update.channel=stable
    
    # Check auto-update status
    pipeops-agent config get auto-update
    ```

=== "Manual Update"

    ```bash
    # Update to latest stable version
    sudo pipeops-agent update
    
    # Update to specific version
    sudo pipeops-agent update --version=v1.2.3
    
    # Update with backup (recommended)
    sudo pipeops-agent update --backup
    ```

=== "Helm Update"

    ```bash
    # Update Helm repository
    helm repo update pipeops
    
    # Upgrade to latest version
    helm upgrade pipeops-agent pipeops/pipeops-agent
    
    # Upgrade with custom values
    helm upgrade pipeops-agent pipeops/pipeops-agent -f values.yaml
    ```

### Post-Update Verification

```bash
# Verify new version
pipeops-agent version

# Check service status
sudo systemctl status pipeops-agent

# Run health check
pipeops-agent diagnose

# Verify connectivity
pipeops-agent status
```

## Configuration Management

### Viewing Configuration

```bash
# Show current configuration
pipeops-agent config show

# Export configuration to file
pipeops-agent config export > config-backup.yaml

# Show specific configuration section
pipeops-agent config get agent.cluster.name
```

### Updating Configuration

```bash
# Set configuration values
pipeops-agent config set agent.log_level=debug
pipeops-agent config set monitoring.enabled=true

# Load configuration from file
pipeops-agent config load config.yaml

# Reset to defaults
pipeops-agent config reset
```

### Configuration File Locations

| Installation Method | Config Location |
|-------------------|-----------------|
| **Script Install** | `/etc/pipeops/config.yaml` |
| **Helm Chart** | ConfigMap in release namespace |
| **Docker** | `/etc/pipeops/config.yaml` (mounted) |
| **Binary** | `~/.pipeops/config.yaml` or `/etc/pipeops/config.yaml` |

## Service Management

### Controlling the Service

```bash
# Start the service
sudo systemctl start pipeops-agent

# Stop the service
sudo systemctl stop pipeops-agent

# Restart the service
sudo systemctl restart pipeops-agent

# Enable auto-start on boot
sudo systemctl enable pipeops-agent

# Disable auto-start
sudo systemctl disable pipeops-agent
```

### Service Information

```bash
# View service status
sudo systemctl status pipeops-agent

# View service configuration
sudo systemctl show pipeops-agent

# View service logs
sudo journalctl -u pipeops-agent -f

# View service dependencies
sudo systemctl list-dependencies pipeops-agent
```

## Log Management

### Viewing Logs

```bash
# Real-time log viewing
sudo journalctl -u pipeops-agent -f

# View recent logs
sudo journalctl -u pipeops-agent --no-pager -n 50

# View logs for specific time period
sudo journalctl -u pipeops-agent --since "1 hour ago"

# View logs with specific priority
sudo journalctl -u pipeops-agent -p err
```

### Log Configuration

```bash
# Set log level
pipeops-agent config set agent.log_level=debug

# Set log format
pipeops-agent config set agent.log_format=json

# Set log output
pipeops-agent config set agent.log_output=/var/log/pipeops/agent.log
```

### Log Rotation

Logs are automatically rotated. To configure:

```bash
# Configure log rotation
sudo tee /etc/logrotate.d/pipeops-agent << EOF
/var/log/pipeops/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    postrotate
        systemctl reload pipeops-agent
    endscript
}
EOF
```

## Backup & Restore

### Creating Backups

```bash
# Backup configuration
pipeops-agent backup create --type=config --output=/tmp/pipeops-config-backup.tar.gz

# Backup complete state
pipeops-agent backup create --type=full --output=/tmp/pipeops-full-backup.tar.gz

# Backup specific components
pipeops-agent backup create --type=monitoring --output=/tmp/pipeops-monitoring-backup.tar.gz
```

### Restoring from Backup

```bash
# Restore configuration
pipeops-agent backup restore --input=/tmp/pipeops-config-backup.tar.gz

# Restore with confirmation
pipeops-agent backup restore --input=/tmp/pipeops-full-backup.tar.gz --confirm

# Restore specific components
pipeops-agent backup restore --type=monitoring --input=/tmp/pipeops-monitoring-backup.tar.gz
```

## Troubleshooting

### Common Issues

=== "Agent Not Starting"

    **Check service logs**:
    ```bash
    sudo journalctl -u pipeops-agent --no-pager -n 50
    ```
    
    **Verify configuration**:
    ```bash
    pipeops-agent validate config
    ```
    
    **Check permissions**:
    ```bash
    ls -la /etc/pipeops/
    sudo chown -R pipeops:pipeops /etc/pipeops/
    ```

=== "Connection Issues"

    **Test network connectivity**:
    ```bash
    curl -I https://api.pipeops.io
    pipeops-agent network test
    ```
    
    **Verify token**:
    ```bash
    pipeops-agent validate token
    ```
    
    **Check firewall**:
    ```bash
    sudo ufw status verbose
    ```

=== "High Resource Usage"

    **Check resource consumption**:
    ```bash
    pipeops-agent resources
    top -p $(pgrep pipeops-agent)
    ```
    
    **Adjust resource limits**:
    ```bash
    pipeops-agent config set agent.resources.limits.memory=512Mi
    pipeops-agent config set agent.resources.limits.cpu=500m
    ```

### Diagnostic Commands

```bash
# Comprehensive health check
pipeops-agent diagnose

# Check specific components
pipeops-agent diagnose --component=network
pipeops-agent diagnose --component=kubernetes
pipeops-agent diagnose --component=monitoring

# Generate support bundle
pipeops-agent support bundle --output=/tmp/pipeops-support.tar.gz
```

## Complete Removal

### Uninstalling

=== "Script Installation"

    ```bash
    # Standard uninstall
    sudo pipeops-agent uninstall
    
    # Complete removal with data purge
    sudo pipeops-agent uninstall --purge-data
    
    # Keep configuration for future reinstall
    sudo pipeops-agent uninstall --keep-config
    ```

=== "Helm Installation"

    ```bash
    # Uninstall Helm release
    helm uninstall pipeops-agent
    
    # Remove namespace
    kubectl delete namespace pipeops-system
    
    # Clean up custom resources
    kubectl delete crd pipeopsagents.pipeops.io
    ```

=== "Docker Installation"

    ```bash
    # Stop and remove container
    docker stop pipeops-agent
    docker rm pipeops-agent
    
    # Remove image
    docker rmi pipeops/agent:latest
    
    # Remove volumes
    docker volume prune
    ```

### Cleanup Verification

```bash
# Check for remaining processes
ps aux | grep pipeops

# Check for remaining files
find / -name "*pipeops*" 2>/dev/null

# Check system services
systemctl list-units | grep pipeops

# Verify complete removal
pipeops-agent version 2>/dev/null && echo "Still installed" || echo "Successfully removed"
```

## Security Management

### Token Management

```bash
# Rotate agent token
pipeops-agent token rotate

# Update token
pipeops-agent token update --token=new-token-here

# Validate current token
pipeops-agent token validate
```

### Certificate Management

```bash
# Update TLS certificates
pipeops-agent certs update

# View certificate information
pipeops-agent certs info

# Validate certificates
pipeops-agent certs validate
```

### Security Audit

```bash
# Run security audit
pipeops-agent security audit

# Check RBAC permissions
pipeops-agent security rbac-check

# Validate security configuration
pipeops-agent security validate
```

## Performance Tuning

### Resource Optimization

```bash
# View resource usage
pipeops-agent resources

# Optimize for low resource environments
pipeops-agent config set agent.performance.mode=low-resource

# Optimize for high performance
pipeops-agent config set agent.performance.mode=high-performance

# Custom resource limits
pipeops-agent config set agent.resources.limits.memory=1Gi
pipeops-agent config set agent.resources.limits.cpu=1000m
```

### Monitoring Performance

```bash
# View performance metrics
pipeops-agent metrics

# Enable performance monitoring
pipeops-agent config set monitoring.performance.enabled=true

# Set monitoring interval
pipeops-agent config set monitoring.interval=30s
```

## Getting Help

### Support Resources

- **Documentation**: [docs.pipeops.io](https://docs.pipeops.io)
- **GitHub Issues**: [github.com/PipeOpsHQ/pipeops-k8-agent/issues](https://github.com/PipeOpsHQ/pipeops-k8-agent/issues)
- **Community Discord**: [discord.gg/pipeops](https://discord.gg/pipeops)
- **Support Email**: [support@pipeops.io](mailto:support@pipeops.io)

### Generating Support Information

```bash
# Create comprehensive support bundle
pipeops-agent support bundle --output=/tmp/support-$(date +%Y%m%d-%H%M%S).tar.gz

# Include system information
pipeops-agent support bundle --include-system --output=/tmp/support-full.tar.gz

# Upload support bundle (requires support ticket ID)
pipeops-agent support upload --ticket=12345 --file=/tmp/support-bundle.tar.gz
```

---

!!! tip "Pro Tip"
    
    Set up monitoring alerts to be notified of agent issues:
    
    ```bash
    # Enable email alerts
    pipeops-agent config set alerts.email.enabled=true
    pipeops-agent config set alerts.email.recipient=admin@yourdomain.com
    
    # Enable Slack notifications
    pipeops-agent config set alerts.slack.webhook=https://hooks.slack.com/your-webhook
    ```