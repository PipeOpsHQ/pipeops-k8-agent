# Operations Overview# Operations Reference# Operations Reference# Operations Reference# Operations Reference



The PipeOps Kubernetes Agent operates as a background system service that automatically transforms your virtual machine into a production-ready Kubernetes server.



## Service OverviewThe PipeOps Kubernetes Agent operates as a background system service that automatically transforms your virtual machine into a production-ready Kubernetes server.



The agent runs as a daemon service and handles all Kubernetes setup and management automatically. Most operations are performed without user intervention.



## Key Operations## Service OverviewThe PipeOps Kubernetes Agent operates as a background system service that automatically transforms your virtual machine into a production-ready Kubernetes server. This section covers how to monitor, configure, and manage the agent service.



- **Service Management** — Start, stop, and manage the agent service  

- **Configuration** — Runtime configuration and settings management

- **Monitoring** — Health checks, metrics, and system observabilityThe agent runs as a daemon service and handles all Kubernetes setup and management automatically. Most operations are performed without user intervention.

- **Maintenance** — Updates, backups, and system maintenance



## Quick Reference

## Key Operations## Service OperationsThe PipeOps Kubernetes Agent operates as a background system service that automatically transforms your virtual machine into a production-ready Kubernetes server. This section covers how to monitor, configure, and manage the agent service.The PipeOps Kubernetes Agent operates as a background system service that automatically transforms your virtual machine into a production-ready Kubernetes server. This section covers how to monitor, configure, and manage the agent service.

| Operation | Command | Description |

|-----------|---------|-------------|

| Check Status | `systemctl status pipeops-agent` | View service status |

| View Logs | `journalctl -u pipeops-agent -f` | Monitor agent logs |- **Service Management** — Start, stop, and manage the agent service  

| Restart Service | `sudo systemctl restart pipeops-agent` | Restart the agent |

| Edit Config | `sudo nano /etc/pipeops/config.yaml` | Modify configuration |- **Configuration** — Runtime configuration and settings management



## Service Architecture- **Monitoring** — Health checks, metrics, and system observabilityThe agent runs as a system service and provides several operational capabilities:



The agent operates as a background service with these responsibilities:- **Maintenance** — Updates, backups, and system maintenance



- **Kubernetes Setup** — Automatic K3s installation and configuration

- **Platform Integration** — Connection to PipeOps control plane

- **Deployment Handling** — Processing application deployment requests## Quick Reference

- **System Monitoring** — Health checks and resource monitoring

- **Maintenance** — Automatic updates and system optimization- **Service Management** — Start, stop, and manage the agent service## Service Operations## Quick Reference



## What's Next| Operation | Command | Description |



- [Service Management](service.md) — Detailed service operations|-----------|---------|-------------|- **Configuration** — Runtime configuration and settings management  

- [Configuration](config.md) — Configuration options and settings  

- [Monitoring](monitoring.md) — Health checks and system metrics| Check Status | `systemctl status pipeops-agent` | View service status |

| View Logs | `journalctl -u pipeops-agent -f` | Monitor agent logs |- **Monitoring** — Health checks, metrics, and system observability

| Restart Service | `sudo systemctl restart pipeops-agent` | Restart the agent |

| Edit Config | `sudo nano /etc/pipeops/config.yaml` | Modify configuration |- **Maintenance** — Updates, backups, and system maintenance



## Service ArchitectureThe agent runs as a system service and provides several operational capabilities:| Command | Description | Example |



The agent operates as a background service with these responsibilities:## Service Architecture



- **Kubernetes Setup** — Automatic K3s installation and configuration|---------|-------------|---------|

- **Platform Integration** — Connection to PipeOps control plane

- **Deployment Handling** — Processing application deployment requestsThe PipeOps agent operates as a daemon service:

- **System Monitoring** — Health checks and resource monitoring

- **Maintenance** — Automatic updates and system optimization- **Service Management** — Start, stop, and manage the agent service| [`status`](#status) | Show agent and cluster status | `pipeops-agent status` |



## What's Next```text



- [Service Management](service.md) — Detailed service operations┌─────────────────────────────────────────┐- **Configuration** — Runtime configuration and settings management  | [`start`](#start) | Start the agent daemon | `pipeops-agent start` |

- [Configuration](config.md) — Configuration options and settings  

- [Monitoring](monitoring.md) — Health checks and system metrics│              PipeOps Agent              │

├─────────────────────────────────────────┤- **Monitoring** — Health checks, metrics, and system observability| [`stop`](#stop) | Stop the agent daemon | `pipeops-agent stop` |

│  • Kubernetes Setup & Management       │

│  • PipeOps Platform Integration        │- **Maintenance** — Updates, backups, and system maintenance| [`restart`](#restart) | Restart the agent daemon | `pipeops-agent restart` |

│  • Application Deployment Handling     │

│  • System Monitoring & Health Checks   │| [`config`](#config) | Manage configuration | `pipeops-agent config validate` |

│  • Auto-Updates & Maintenance          │

└─────────────────────────────────────────┘## Service Architecture| [`cluster`](#cluster) | Cluster management commands | `pipeops-agent cluster info` |

```

| [`apps`](#apps) | Application deployment | `pipeops-agent apps deploy` |

### Service Interaction

The PipeOps agent operates as a daemon service:| [`logs`](#logs) | View logs | `pipeops-agent logs --tail=100` |

Most operations are handled automatically, but you can interact with the service for:

| [`diagnose`](#diagnose) | Run diagnostics | `pipeops-agent diagnose` |

- **Status Monitoring** — Check service health and operational status

- **Configuration Updates** — Modify runtime settings and parameters```| [`version`](#version) | Show version information | `pipeops-agent version` |

- **Log Analysis** — Review system logs and troubleshoot issues

- **Manual Operations** — Perform maintenance tasks when needed┌─────────────────────────────────────────┐



## Quick Operations Reference│              PipeOps Agent              │## Global Options



| Operation | Description | Method |├─────────────────────────────────────────┤

|-----------|-------------|--------|

| Check Status | View service status | `systemctl status pipeops-agent` |│  • Kubernetes Setup & Management       │Available for all commands:

| View Logs | Monitor agent logs | `journalctl -u pipeops-agent -f` |

| Restart Service | Restart the agent | `sudo systemctl restart pipeops-agent` |│  • PipeOps Platform Integration        │

| Update Config | Modify configuration | Edit `/etc/pipeops/config.yaml` |

| Health Check | Verify system health | Check monitoring endpoints |│  • Application Deployment Handling     │```bash



## Service Management│  • System Monitoring & Health Checks   │Global Flags:



The agent integrates with your system's service manager:│  • Auto-Updates & Maintenance          │  --config string     Configuration file path (default "/etc/pipeops/config.yaml")



```bash└─────────────────────────────────────────┘  --log-level string  Log level: debug, info, warn, error (default "info")

# Check service status

sudo systemctl status pipeops-agent```  --output string     Output format: table, json, yaml (default "table")



# View service logs  --quiet             Suppress non-essential output

sudo journalctl -u pipeops-agent -f

### Service Interaction  --verbose           Enable verbose output

# Restart if needed

sudo systemctl restart pipeops-agent  --help              Show help information

```

Most operations are handled automatically, but you can interact with the service for:```

## Configuration Locations



Key configuration and data locations:

- **Status Monitoring** — Check service health and operational status## Core Commands

| Path | Purpose | Description |

|------|---------|-------------|- **Configuration Updates** — Modify runtime settings and parameters

| `/etc/pipeops/config.yaml` | Main configuration | Service settings and parameters |

| `/var/log/pipeops/` | Log files | Service logs and system events |- **Log Analysis** — Review system logs and troubleshoot issues### `status`

| `/var/lib/pipeops/` | Data directory | Runtime data and state |

- **Manual Operations** — Perform maintenance tasks when needed

## Automatic Operations

Display current agent and cluster status.

The agent handles these operations automatically:

## Quick Operations Reference

### Kubernetes Setup

```bash

- **K3s Installation** — Automatic lightweight Kubernetes installation

- **Node Configuration** — System optimization for Kubernetes workloads| Operation | Description | Method |pipeops-agent status [flags]

- **Network Setup** — Container networking and ingress configuration

- **Storage Provisioning** — Persistent volume setup and management|-----------|-------------|--------|```



### PipeOps Integration| Check Status | View service status | `systemctl status pipeops-agent` |



- **Platform Registration** — Automatic registration with PipeOps control plane| View Logs | Monitor agent logs | `journalctl -u pipeops-agent -f` |**Flags:**

- **Secure Communication** — TLS-encrypted communication channels

- **Deployment Handling** — Receives and processes deployment requests| Restart Service | Restart the agent | `sudo systemctl restart pipeops-agent` |```bash

- **Status Reporting** — Regular health and status updates

| Update Config | Modify configuration | Edit `/etc/pipeops/config.yaml` |  --format string   Output format: table, json, yaml (default "table")

### System Maintenance

| Health Check | Verify system health | Check monitoring endpoints |  --watch          Watch for status changes

- **Health Monitoring** — Continuous system health checks

- **Log Rotation** — Automatic log management and cleanup  --refresh int    Refresh interval in seconds (default 5)

- **Resource Monitoring** — CPU, memory, and disk usage tracking

- **Update Management** — Automatic agent updates and security patches## Service Management```



## Manual Operations



While most operations are automatic, you may need to perform manual tasks for:The agent integrates with your system's service manager:**Examples:**



### Troubleshooting```bash



```bash```bash# Basic status check

# Check service status

sudo systemctl status pipeops-agent# Check service statuspipeops-agent status



# View detailed logssudo systemctl status pipeops-agent

sudo journalctl -u pipeops-agent --no-pager

# Watch status in real-time

# Check system resources

df -h && free -h# View service logspipeops-agent status --watch

```

sudo journalctl -u pipeops-agent -f

### Configuration Updates

# JSON output for automation

```bash

# Edit main configuration# Restart if neededpipeops-agent status --format=json

sudo nano /etc/pipeops/config.yaml

sudo systemctl restart pipeops-agent

# Validate configuration

sudo pipeops-agent config validate```# Specific component status



# Apply changespipeops-agent status --component=monitoring

sudo systemctl restart pipeops-agent

```## Configuration Locations```



### Maintenance Tasks



```bashKey configuration and data locations:**Output:**

# Update the agent

sudo pipeops-agent update```text



# Backup configuration| Path | Purpose | Description |PipeOps Agent Status: Running

sudo cp /etc/pipeops/config.yaml /etc/pipeops/config.yaml.backup

|------|---------|-------------|Control Plane Connection: Connected

# Check system connectivity

curl -f https://api.pipeops.io/health| `/etc/pipeops/config.yaml` | Main configuration | Service settings and parameters |Kubernetes API: Accessible  

```

| `/var/log/pipeops/` | Log files | Service logs and system events |Monitoring Stack: Healthy

## Monitoring Integration

| `/var/lib/pipeops/` | Data directory | Runtime data and state |Clusters Managed: 1

The agent provides several monitoring endpoints:

Last Heartbeat: 2 seconds ago

| Endpoint | Purpose | Access |

|----------|---------|--------|## Automatic OperationsUptime: 2d 5h 32m

| `:8080/health` | Basic health check | HTTP GET |

| `:8080/ready` | Readiness probe | HTTP GET |```

| `:8080/metrics` | Prometheus metrics | HTTP GET |

| `:3000/` | Grafana dashboard | Web Browser |The agent handles these operations automatically:



## Service States### `start`



The agent operates in several states:### Kubernetes Setup



### Starting- **K3s Installation** — Automatic lightweight Kubernetes installationStart the PipeOps agent daemon.



- **Initializing** — Loading configuration and checking prerequisites- **Node Configuration** — System optimization for Kubernetes workloads

- **Connecting** — Establishing connection to PipeOps platform

- **Preparing** — Setting up Kubernetes environment- **Network Setup** — Container networking and ingress configuration```bash

- **Ready** — Fully operational and ready for deployments

- **Storage Provisioning** — Persistent volume setup and managementpipeops-agent start [flags]

### Running

```

- **Active** — Processing deployment requests and maintaining system

- **Monitoring** — Continuous health checks and resource monitoring### PipeOps Integration

- **Updating** — Automatic updates and maintenance operations

- **Scaling** — Handling resource scaling and optimization- **Platform Registration** — Automatic registration with PipeOps control plane**Flags:**



### Stopping- **Secure Communication** — TLS-encrypted communication channels```bash



- **Draining** — Gracefully stopping running workloads- **Deployment Handling** — Receives and processes deployment requests  --daemon          Run as daemon in background

- **Cleanup** — Cleaning up temporary resources

- **Shutdown** — Safe service termination- **Status Reporting** — Regular health and status updates  --pid-file string PID file location (default "/var/run/pipeops-agent.pid")



## Error Recovery  --dev-mode        Enable development mode



The agent includes automatic error recovery:### System Maintenance  --dry-run         Show what would be done without executing



- **Service Restart** — Automatic restart on failure- **Health Monitoring** — Continuous system health checks```

- **State Recovery** — Restoration of previous operational state

- **Connection Retry** — Automatic reconnection to PipeOps platform- **Log Rotation** — Automatic log management and cleanup

- **Health Recovery** — Self-healing capabilities for common issues

- **Resource Monitoring** — CPU, memory, and disk usage tracking**Examples:**

## What's Next

- **Update Management** — Automatic agent updates and security patches```bash

- [Service Management](service.md) — Detailed service operations

- [Configuration](config.md) — Configuration options and settings# Start agent in foreground

- [Monitoring](monitoring.md) — Health checks and system metrics
## Manual Operationspipeops-agent start



While most operations are automatic, you may need to perform manual tasks for:# Start as daemon

pipeops-agent start --daemon

### Troubleshooting

```bash# Development mode with debug logging

# Check service statuspipeops-agent start --dev-mode --log-level=debug

sudo systemctl status pipeops-agent

# Test configuration without starting

# View detailed logspipeops-agent start --dry-run

sudo journalctl -u pipeops-agent --no-pager```



# Check system resources### `stop`

df -h && free -h

```Stop the PipeOps agent daemon.



### Configuration Updates```bash

```bashpipeops-agent stop [flags]

# Edit main configuration```

sudo nano /etc/pipeops/config.yaml

**Flags:**

# Validate configuration```bash

sudo pipeops-agent config validate  --force           Force stop without graceful shutdown

  --timeout int     Graceful shutdown timeout in seconds (default 30)

# Apply changes  --pid-file string PID file location (default "/var/run/pipeops-agent.pid")

sudo systemctl restart pipeops-agent```

```

**Examples:**

### Maintenance Tasks```bash

```bash# Graceful stop

# Update the agentpipeops-agent stop

sudo pipeops-agent update

# Force stop immediately

# Backup configurationpipeops-agent stop --force

sudo cp /etc/pipeops/config.yaml /etc/pipeops/config.yaml.backup

# Custom shutdown timeout

# Check system connectivitypipeops-agent stop --timeout=60

curl -f https://api.pipeops.io/health```

```

### `restart`

## Monitoring Integration

Restart the PipeOps agent daemon.

The agent provides several monitoring endpoints:

```bash

| Endpoint | Purpose | Access |pipeops-agent restart [flags]

|----------|---------|--------|```

| `:8080/health` | Basic health check | HTTP GET |

| `:8080/ready` | Readiness probe | HTTP GET |**Flags:**

| `:8080/metrics` | Prometheus metrics | HTTP GET |```bash

| `:3000/` | Grafana dashboard | Web Browser |  --timeout int     Shutdown timeout in seconds (default 30)

  --force           Force restart without graceful shutdown

## Service States```



The agent operates in several states:**Examples:**

```bash

### Starting# Standard restart

- **Initializing** — Loading configuration and checking prerequisitespipeops-agent restart

- **Connecting** — Establishing connection to PipeOps platform

- **Preparing** — Setting up Kubernetes environment# Quick restart

- **Ready** — Fully operational and ready for deploymentspipeops-agent restart --force



### Running# Restart with custom timeout

- **Active** — Processing deployment requests and maintaining systempipeops-agent restart --timeout=60

- **Monitoring** — Continuous health checks and resource monitoring```

- **Updating** — Automatic updates and maintenance operations

- **Scaling** — Handling resource scaling and optimization## Configuration Commands



### Stopping### `config`

- **Draining** — Gracefully stopping running workloads

- **Cleanup** — Cleaning up temporary resourcesManage agent configuration.

- **Shutdown** — Safe service termination

#### `config validate`

## Error Recovery

Validate configuration file syntax and values.

The agent includes automatic error recovery:

```bash

- **Service Restart** — Automatic restart on failurepipeops-agent config validate [flags]

- **State Recovery** — Restoration of previous operational state```

- **Connection Retry** — Automatic reconnection to PipeOps platform

- **Health Recovery** — Self-healing capabilities for common issues**Flags:**

```bash

## What's Next  --config string   Configuration file to validate

  --strict          Enable strict validation mode

- [Service Management](service.md) — Detailed service operations  --schema string   JSON schema file for validation

- [Configuration](config.md) — Configuration options and settings```

- [Monitoring](monitoring.md) — Health checks and system metrics
**Examples:**
```bash
# Validate default configuration
pipeops-agent config validate

# Validate specific file
pipeops-agent config validate --config=/path/to/config.yaml

# Strict validation with schema
pipeops-agent config validate --strict --schema=config.schema.json
```

#### `config show`

Display resolved configuration.

```bash
pipeops-agent config show [flags]
```

**Examples:**
```bash
# Show full configuration
pipeops-agent config show

# Show specific section
pipeops-agent config show --section=monitoring

# JSON output
pipeops-agent config show --format=json
```

#### `config test`

Test configuration by connecting to services.

```bash
pipeops-agent config test [flags]
```

**Examples:**
```bash
# Test all connections
pipeops-agent config test

# Test specific component
pipeops-agent config test --component=control-plane
```

## Cluster Commands

### `cluster`

Kubernetes cluster management commands.

#### `cluster info`

Display cluster information.

```bash
pipeops-agent cluster info [flags]
```

**Examples:**
```bash
# Basic cluster info
pipeops-agent cluster info

# Detailed info with resources
pipeops-agent cluster info --detailed

# JSON output
pipeops-agent cluster info --format=json
```

#### `cluster health`

Check cluster health status.

```bash
pipeops-agent cluster health [flags]
```

**Examples:**
```bash
# Health check
pipeops-agent cluster health

# Detailed health report
pipeops-agent cluster health --detailed

# Continuous monitoring
pipeops-agent cluster health --watch
```

#### `cluster resources`

Display cluster resource usage.

```bash
pipeops-agent cluster resources [flags]
```

**Examples:**
```bash
# Resource overview
pipeops-agent cluster resources

# Specific resource type
pipeops-agent cluster resources --type=nodes

# Historical usage
pipeops-agent cluster resources --history=24h
```

## Application Commands

### `apps`

Application deployment and management.

#### `apps list`

List deployed applications.

```bash
pipeops-agent apps list [flags]
```

**Flags:**
```bash
  --namespace string   Filter by namespace
  --status string      Filter by status: running, stopped, failed
  --labels string      Filter by labels (key=value)
```

**Examples:**
```bash
# List all applications
pipeops-agent apps list

# Filter by namespace
pipeops-agent apps list --namespace=production

# Filter by status
pipeops-agent apps list --status=running

# Filter by labels
pipeops-agent apps list --labels=env=production,team=backend
```

#### `apps deploy`

Deploy an application.

```bash
pipeops-agent apps deploy [flags]
```

**Flags:**
```bash
  --name string        Application name (required)
  --image string       Container image (required)
  --replicas int       Number of replicas (default 1)
  --port int           Container port (default 8080)
  --env stringArray    Environment variables (key=value)
  --namespace string   Deployment namespace (default "default")
```

**Examples:**
```bash
# Basic deployment
pipeops-agent apps deploy --name=web-app --image=nginx:latest

# Advanced deployment
pipeops-agent apps deploy \
  --name=api-server \
  --image=myapp:v1.2.3 \
  --replicas=3 \
  --port=8080 \
  --env=DATABASE_URL=postgres://... \
  --env=REDIS_URL=redis://... \
  --namespace=production
```

#### `apps status`

Check application status.

```bash
pipeops-agent apps status <app-name> [flags]
```

**Examples:**
```bash
# Application status
pipeops-agent apps status web-app

# Watch status changes
pipeops-agent apps status web-app --watch

# Detailed status with events
pipeops-agent apps status web-app --detailed
```

#### `apps scale`

Scale application replicas.

```bash
pipeops-agent apps scale <app-name> [flags]
```

**Flags:**
```bash
  --replicas int   Number of replicas (required)
  --timeout int    Scaling timeout in seconds (default 300)
```

**Examples:**
```bash
# Scale to 5 replicas
pipeops-agent apps scale web-app --replicas=5

# Scale with custom timeout
pipeops-agent apps scale web-app --replicas=3 --timeout=600
```

#### `apps delete`

Delete an application.

```bash
pipeops-agent apps delete <app-name> [flags]
```

**Flags:**
```bash
  --force              Force delete without confirmation
  --cascade            Delete dependent resources (default true)
  --grace-period int   Graceful deletion period in seconds (default 30)
```

**Examples:**
```bash
# Delete with confirmation
pipeops-agent apps delete web-app

# Force delete
pipeops-agent apps delete web-app --force

# Delete with custom grace period
pipeops-agent apps delete web-app --grace-period=60
```

## Monitoring Commands

### `logs`

View agent and application logs.

```bash
pipeops-agent logs [flags]
```

**Flags:**
```bash
  --tail int           Number of lines to show (default 100)
  --follow             Follow log output
  --since string       Show logs since timestamp (e.g., "2h", "2023-01-01T10:00:00Z")
  --component string   Filter by component: agent, prometheus, grafana, loki
  --level string       Filter by log level: debug, info, warn, error
```

**Examples:**
```bash
# View recent logs
pipeops-agent logs

# Follow logs in real-time
pipeops-agent logs --follow

# View last 500 lines
pipeops-agent logs --tail=500

# View logs since 1 hour ago
pipeops-agent logs --since=1h

# Filter by component
pipeops-agent logs --component=prometheus

# Filter by log level
pipeops-agent logs --level=error
```

### `metrics`

Display system metrics.

```bash
pipeops-agent metrics [flags]
```

**Flags:**
```bash
  --type string     Metric type: cpu, memory, disk, network
  --duration string Time range: 1h, 24h, 7d (default "1h")
  --format string   Output format: table, json, csv
```

**Examples:**
```bash
# All metrics for last hour
pipeops-agent metrics

# CPU metrics for last 24 hours
pipeops-agent metrics --type=cpu --duration=24h

# JSON output for automation
pipeops-agent metrics --format=json
```

## Utility Commands

### `diagnose`

Run comprehensive system diagnostics.

```bash
pipeops-agent diagnose [flags]
```

**Flags:**
```bash
  --component string   Diagnose specific component
  --output-file string Save report to file
  --include-logs       Include recent logs in report
```

**Examples:**
```bash
# Full diagnostic report
pipeops-agent diagnose

# Diagnose specific component
pipeops-agent diagnose --component=monitoring

# Save report to file
pipeops-agent diagnose --output-file=diagnostic-report.txt

# Include logs in report
pipeops-agent diagnose --include-logs
```

### `version`

Show version and build information.

```bash
pipeops-agent version [flags]
```

**Flags:**
```bash
  --short      Show short version only
  --build      Show build information
  --check      Check for updates
```

**Examples:**
```bash
# Full version info
pipeops-agent version

# Short version
pipeops-agent version --short

# Build details
pipeops-agent version --build

# Check for updates
pipeops-agent version --check
```

### `completion`

Generate shell completion scripts.

```bash
pipeops-agent completion [bash|zsh|fish|powershell]
```

**Examples:**
```bash
# Bash completion
pipeops-agent completion bash > /etc/bash_completion.d/pipeops-agent

# Zsh completion
pipeops-agent completion zsh > ~/.zsh/completions/_pipeops-agent

# Fish completion
pipeops-agent completion fish > ~/.config/fish/completions/pipeops-agent.fish
```

## Security Commands

### `token`

Manage authentication tokens.

#### `token validate`

Validate authentication token.

```bash
pipeops-agent token validate [flags]
```

**Examples:**
```bash
# Validate current token
pipeops-agent token validate

# Validate specific token
pipeops-agent token validate --token=<token-string>
```

#### `token refresh`

Refresh authentication token.

```bash
pipeops-agent token refresh [flags]
```

**Examples:**
```bash
# Refresh token
pipeops-agent token refresh

# Refresh and save to file
pipeops-agent token refresh --save-to=/etc/pipeops/token
```

## Command Examples

### Common Workflows

#### Initial Setup
```bash
# Validate configuration
pipeops-agent config validate

# Test connectivity
pipeops-agent config test

# Start agent
pipeops-agent start --daemon

# Verify status
pipeops-agent status
```

#### Deploy Application
```bash
# Deploy application
pipeops-agent apps deploy \
  --name=web-app \
  --image=nginx:latest \
  --replicas=3 \
  --port=80

# Check deployment status
pipeops-agent apps status web-app

# View application logs
pipeops-agent logs --component=web-app --follow
```

#### Monitor System
```bash
# Check cluster health
pipeops-agent cluster health

# View system metrics
pipeops-agent metrics --duration=24h

# Monitor logs
pipeops-agent logs --follow --level=warn
```

#### Troubleshooting
```bash
# Run diagnostics
pipeops-agent diagnose --include-logs

# Check specific component
pipeops-agent diagnose --component=monitoring

# View detailed logs
pipeops-agent logs --tail=1000 --level=error
```

## Tips & Best Practices

### 1. Use Output Formats for Automation

```bash
# JSON output for scripts
STATUS=$(pipeops-agent status --format=json)
echo $STATUS | jq '.cluster.health'

# YAML output for configuration
pipeops-agent config show --format=yaml > current-config.yaml
```

### 2. Combine Commands with Shell Tools

```bash
# Monitor specific metrics
pipeops-agent metrics --format=csv | grep "cpu" | sort -k2 -n

# Find high-resource applications
pipeops-agent apps list --format=json | jq '.[] | select(.resources.cpu > 0.5)'
```

### 3. Use Aliases for Common Operations

```bash
# Add to ~/.bashrc or ~/.zshrc
alias pa='pipeops-agent'
alias pas='pipeops-agent status'
alias pal='pipeops-agent logs --follow'
alias pad='pipeops-agent diagnose'
```

---

**Next Steps**: [Agent Commands](agent.md) | [Cluster Management](cluster.md) | [Configuration](../getting-started/configuration.md)