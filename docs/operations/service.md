# Service Management# Service Management# Service Management



The PipeOps Kubernetes Agent runs as a system service that automatically manages your Kubernetes infrastructure. This page covers service management operations.



## Service ControlThe PipeOps Kubernetes Agent runs as a system service that automatically manages your Kubernetes infrastructure. This page covers service management operations.The PipeOps Kubernetes Agent runs as a system service that automatically manages your Kubernetes infrastructure. This page covers service management operations.



### Checking Service Status



```bash## Service Control## Service Control

# Check if the service is running

sudo systemctl status pipeops-agent



# Check service health### Checking Service Status### Checking Service Status

sudo systemctl is-active pipeops-agent



# Check if service is enabled for startup

sudo systemctl is-enabled pipeops-agent```bash```bash

```

# Check if the service is runningpipeops-agent start [options]

### Starting and Stopping

sudo systemctl status pipeops-agent```

```bash

# Start the service

sudo systemctl start pipeops-agent

# Check service health#### Options

# Stop the service

sudo systemctl stop pipeops-agentsudo systemctl is-active pipeops-agent



# Restart the service| Flag | Description | Default |

sudo systemctl restart pipeops-agent

# Check if service is enabled for startup|------|-------------|---------|

# Reload configuration without stopping

sudo systemctl reload pipeops-agentsudo systemctl is-enabled pipeops-agent| `--daemon` | Run as background daemon | `false` |

```

```| `--pid-file` | PID file location | `/var/run/pipeops-agent.pid` |

### Service Logs

| `--log-file` | Log file path | `/var/log/pipeops-agent.log` |

```bash

# View recent logs### Starting and Stopping| `--dev-mode` | Enable development mode | `false` |

sudo journalctl -u pipeops-agent

| `--dry-run` | Validate configuration without starting | `false` |

# Follow logs in real-time

sudo journalctl -u pipeops-agent -f```bash



# View logs from last hour# Start the service#### Examples

sudo journalctl -u pipeops-agent --since "1 hour ago"

sudo systemctl start pipeops-agent

# View logs with specific priority

sudo journalctl -u pipeops-agent -p err=== "Foreground"

```

# Stop the service

## Service Configuration

sudo systemctl stop pipeops-agent    ```bash

The service is configured through systemd unit files and configuration files.

    # Start in foreground (useful for debugging)

### Service Unit File

# Restart the service    pipeops-agent start

Location: `/etc/systemd/system/pipeops-agent.service`

sudo systemctl restart pipeops-agent    

```ini

[Unit]    # With debug logging

Description=PipeOps Kubernetes Agent

After=network.target# Reload configuration without stopping    pipeops-agent start --log-level=debug

Wants=network.target

sudo systemctl reload pipeops-agent    ```

[Service]

Type=simple```

User=pipeops

Group=pipeops=== "Background Daemon"

WorkingDirectory=/opt/pipeops

ExecStart=/opt/pipeops/bin/pipeops-agent### Service Logs

ExecReload=/bin/kill -HUP $MAINPID

Restart=always    ```bash

RestartSec=5

LimitNOFILE=65536```bash    # Start as daemon



[Install]# View recent logs    pipeops-agent start --daemon

WantedBy=multi-user.target

```sudo journalctl -u pipeops-agent    



### Environment Variables    # Custom PID file location



The service can be configured using environment variables:# Follow logs in real-time    pipeops-agent start --daemon --pid-file=/opt/pipeops/pipeops-agent.pid



| Variable | Description | Default |sudo journalctl -u pipeops-agent -f    ```

|----------|-------------|---------|

| `PIPEOPS_CONFIG` | Configuration file path | `/etc/pipeops/config.yaml` |

| `PIPEOPS_LOG_LEVEL` | Logging level | `info` |

| `PIPEOPS_DATA_DIR` | Data directory | `/var/lib/pipeops` |# View logs from last hour=== "Development Mode"



## Service Statessudo journalctl -u pipeops-agent --since "1 hour ago"



The agent service operates in several states:    ```bash



### Active States# View logs with specific priority    # Development mode with enhanced logging



- **active (running)** — Service is running normallysudo journalctl -u pipeops-agent -p err    pipeops-agent start --dev-mode

- **active (exited)** — Service completed successfully

- **active (waiting)** — Service is waiting for events```    



### Inactive States    # Dry run to test configuration



- **inactive (dead)** — Service is stopped## Service Configuration    pipeops-agent start --dry-run

- **inactive (failed)** — Service failed to start or crashed

    ```

### Transitional States

The service is configured through systemd unit files and configuration files:

- **activating** — Service is starting up

- **deactivating** — Service is shutting down#### Output

- **reloading** — Service is reloading configuration

### Service Unit File

## Troubleshooting

```text

### Common Issues

Location: `/etc/systemd/system/pipeops-agent.service`Starting PipeOps Kubernetes Agent...

#### Service Won't Start

Configuration loaded: /etc/pipeops/config.yaml

```bash

# Check detailed status```iniKubernetes API connection established

sudo systemctl status pipeops-agent -l

[Unit]Control plane connection established

# Check configuration

sudo pipeops-agent config validateDescription=PipeOps Kubernetes AgentMonitoring stack initialized



# Check system resourcesAfter=network.targetAgent started successfully (PID: 12345)

df -h && free -h

```Wants=network.target



#### Service CrashesAgent Status:



```bash[Service]   • Cluster: production-cluster

# View crash logs

sudo journalctl -u pipeops-agent --since "1 hour ago" -p errType=simple   • Region: us-west-2  



# Check system resourcesUser=pipeops   • Version: v2.1.0

sudo systemctl status pipeops-agent

Group=pipeops   • Uptime: 0s

# Restart with debug logging

sudo systemctl edit pipeops-agentWorkingDirectory=/opt/pipeops   • Last Heartbeat: just now

```

ExecStart=/opt/pipeops/bin/pipeops-agent```

#### Configuration Issues

ExecReload=/bin/kill -HUP $MAINPID

```bash

# Validate configurationRestart=always### `stop` - Stop Agent

sudo pipeops-agent config validate

RestartSec=5

# Test configuration

sudo pipeops-agent config testLimitNOFILE=65536Gracefully stop the PipeOps agent daemon.



# Check file permissions

sudo ls -la /etc/pipeops/

```[Install]```bash



## MaintenanceWantedBy=multi-user.targetpipeops-agent stop [options]



### Service Updates``````



```bash

# Update the agent

sudo pipeops-agent update### Environment Variables#### Options



# Restart after update

sudo systemctl restart pipeops-agent

The service can be configured using environment variables:| Flag | Description | Default |

# Verify update

pipeops-agent version|------|-------------|---------|

```

| Variable | Description | Default || `--force` | Force stop without graceful shutdown | `false` |

### Log Rotation

|----------|-------------|---------|| `--timeout` | Graceful shutdown timeout (seconds) | `30` |

```bash

# Manual log rotation| `PIPEOPS_CONFIG` | Configuration file path | `/etc/pipeops/config.yaml` || `--pid-file` | PID file location | `/var/run/pipeops-agent.pid` |

sudo journalctl --vacuum-time=7d

| `PIPEOPS_LOG_LEVEL` | Logging level | `info` |

# Configure automatic rotation

sudo systemctl edit systemd-journald| `PIPEOPS_DATA_DIR` | Data directory | `/var/lib/pipeops` |#### Examples

```



### Backup and Restore

## Service States```bash

```bash

# Backup configuration# Graceful stop

sudo cp -r /etc/pipeops /backup/pipeops-config-$(date +%Y%m%d)

The agent service operates in several states:pipeops-agent stop

# Backup service state

sudo cp -r /var/lib/pipeops /backup/pipeops-state-$(date +%Y%m%d)



# Restore configuration### Active States# Force stop immediately  

sudo cp -r /backup/pipeops-config-20240101 /etc/pipeops

sudo systemctl restart pipeops-agentpipeops-agent stop --force

```

- **active (running)** — Service is running normally

## Advanced Operations

- **active (exited)** — Service completed successfully# Custom shutdown timeout

### Service Debugging

- **active (waiting)** — Service is waiting for eventspipeops-agent stop --timeout=60

```bash

# Enable debug logging

sudo mkdir -p /etc/systemd/system/pipeops-agent.service.d

sudo tee /etc/systemd/system/pipeops-agent.service.d/debug.conf << EOF### Inactive States# Stop using custom PID file

[Service]

Environment=PIPEOPS_LOG_LEVEL=debugpipeops-agent stop --pid-file=/opt/pipeops/pipeops-agent.pid

EOF

- **inactive (dead)** — Service is stopped```

# Reload systemd and restart

sudo systemctl daemon-reload- **inactive (failed)** — Service failed to start or crashed

sudo systemctl restart pipeops-agent

```#### Output



### Performance Tuning### Transitional States



```bash```text

# Increase file descriptor limits

sudo mkdir -p /etc/systemd/system/pipeops-agent.service.d- **activating** — Service is starting upStopping PipeOps Kubernetes Agent...

sudo tee /etc/systemd/system/pipeops-agent.service.d/limits.conf << EOF

[Service]- **deactivating** — Service is shutting downGraceful shutdown initiated

LimitNOFILE=131072

LimitNPROC=131072- **reloading** — Service is reloading configurationActive connections closed

EOF

Monitoring stack stopped

# Apply changes

sudo systemctl daemon-reload## TroubleshootingAgent stopped successfully

sudo systemctl restart pipeops-agent

``````



## Service Health Checks### Common Issues



The agent service provides health check endpoints.### `restart` - Restart Agent



### HTTP Health Endpoints#### Service Won't Start



```bashRestart the PipeOps agent daemon (stop + start).

# Basic health check

curl http://localhost:8080/health```bash



# Readiness check# Check detailed status```bash

curl http://localhost:8080/ready

sudo systemctl status pipeops-agent -lpipeops-agent restart [options]

# Metrics endpoint

curl http://localhost:8080/metrics```

```

# Check configuration

### Health Check Responses

sudo pipeops-agent config validate#### Options

**Healthy Service:**



```json

{# Check system resources| Flag | Description | Default |

  "status": "healthy",

  "timestamp": "2024-01-15T10:30:00Z",df -h && free -h|------|-------------|---------|

  "uptime": "2d5h30m",

  "version": "v1.0.0"```| `--timeout` | Shutdown timeout (seconds) | `30` |

}

```| `--force` | Force restart without graceful shutdown | `false` |



**Service Issues:**#### Service Crashes



```json#### Examples

{

  "status": "unhealthy",```bash

  "timestamp": "2024-01-15T10:30:00Z",

  "errors": [# View crash logs```bash

    "kubernetes_api_unreachable",

    "control_plane_disconnected"sudo journalctl -u pipeops-agent --since "1 hour ago" -p err# Standard restart

  ]

}pipeops-agent restart

```

# Check system resources

## Monitoring Integration

sudo systemctl status pipeops-agent# Quick restart (force stop)

The agent integrates with system monitoring tools.

pipeops-agent restart --force

### SystemD Integration

# Restart with debug logging

```bash

# Monitor service with systemdsudo systemctl edit pipeops-agent# Restart with extended timeout

sudo systemctl enable pipeops-agent

```pipeops-agent restart --timeout=120

# Set up service dependencies

sudo systemctl edit pipeops-agent```

```

#### Configuration Issues

### Process Monitoring

### `status` - Agent Status

```bash

# Check process status```bash

ps aux | grep pipeops-agent

# Validate configurationDisplay comprehensive agent and cluster status information.

# Monitor resource usage

top -p $(pgrep pipeops-agent)sudo pipeops-agent config validate



# Check open files```bash

sudo lsof -p $(pgrep pipeops-agent)

```# Test configurationpipeops-agent status [options]



## Next Stepssudo pipeops-agent config test```



- [Configuration Management](config.md) — Detailed configuration options

- [Monitoring Setup](monitoring.md) — Health checks and metrics
# Check file permissions#### Options

sudo ls -la /etc/pipeops/

```| Flag | Description | Default |

|------|-------------|---------|

## Maintenance| `--format` | Output format: `table`, `json`, `yaml` | `table` |

| `--watch` | Watch for status changes | `false` |

### Service Updates| `--refresh` | Refresh interval in seconds | `5` |

| `--component` | Show specific component status | `all` |

```bash

# Update the agent#### Examples

sudo pipeops-agent update

=== "Basic Status"

# Restart after update

sudo systemctl restart pipeops-agent    ```bash

    # Standard status display

# Verify update    pipeops-agent status

pipeops-agent version    ```

```

    Output:

### Log Rotation    ```text

    PipeOps Agent Status: Running

```bash    Control Plane Connection: Connected

# Manual log rotation    Kubernetes API: Accessible

sudo journalctl --vacuum-time=7d    Monitoring Stack: Healthy

    Clusters Managed: 1

# Configure automatic rotation    Last Heartbeat: 3 seconds ago

sudo systemctl edit systemd-journald    Uptime: 2d 5h 32m

```    Memory Usage: 256MB / 512MB (50%)

    CPU Usage: 0.25 / 0.50 cores (50%)

### Backup and Restore    ```



```bash=== "Watch Mode"

# Backup configuration

sudo cp -r /etc/pipeops /backup/pipeops-config-$(date +%Y%m%d)    ```bash

    # Watch status in real-time

# Backup service state    pipeops-agent status --watch

sudo cp -r /var/lib/pipeops /backup/pipeops-state-$(date +%Y%m%d)    

    # Custom refresh interval

# Restore configuration    pipeops-agent status --watch --refresh=10

sudo cp -r /backup/pipeops-config-20240101 /etc/pipeops    ```

sudo systemctl restart pipeops-agent

```=== "JSON Output"



## Advanced Operations    ```bash

    # JSON format for automation

### Service Debugging    pipeops-agent status --format=json

    ```

```bash

# Enable debug logging    Output:

sudo mkdir -p /etc/systemd/system/pipeops-agent.service.d    ```json

sudo tee /etc/systemd/system/pipeops-agent.service.d/debug.conf << EOF    {

[Service]      "agent": {

Environment=PIPEOPS_LOG_LEVEL=debug        "status": "running",

EOF        "version": "v2.1.0",

        "uptime": "2d5h32m",

# Reload systemd and restart        "pid": 12345

sudo systemctl daemon-reload      },

sudo systemctl restart pipeops-agent      "cluster": {

```        "name": "production-cluster",

        "region": "us-west-2",

### Performance Tuning        "kubernetes_version": "v1.25.0",

        "node_count": 3,

```bash        "health": "healthy"

# Increase file descriptor limits      },

sudo mkdir -p /etc/systemd/system/pipeops-agent.service.d      "monitoring": {

sudo tee /etc/systemd/system/pipeops-agent.service.d/limits.conf << EOF        "prometheus": "healthy",

[Service]        "grafana": "healthy", 

LimitNOFILE=131072        "loki": "healthy"

LimitNPROC=131072      },

EOF      "resources": {

        "cpu_usage": 0.25,

# Apply changes        "memory_usage": 268435456,

sudo systemctl daemon-reload        "disk_usage": 1073741824

sudo systemctl restart pipeops-agent      }

```    }

    ```

## Service Health Checks

=== "Component Specific"

The agent service provides health check endpoints:

    ```bash

### HTTP Health Endpoints    # Check specific component

    pipeops-agent status --component=monitoring

```bash    pipeops-agent status --component=cluster

# Basic health check    pipeops-agent status --component=networking

curl http://localhost:8080/health    ```



# Readiness check## Diagnostics & Troubleshooting

curl http://localhost:8080/ready

### `diagnose` - System Diagnostics

# Metrics endpoint

curl http://localhost:8080/metricsRun comprehensive system diagnostics and health checks.

```

```bash

### Health Check Responsespipeops-agent diagnose [options]

```

**Healthy Service:**

```json#### Options

{

  "status": "healthy",| Flag | Description | Default |

  "timestamp": "2024-01-15T10:30:00Z",|------|-------------|---------|

  "uptime": "2d5h30m",| `--component` | Diagnose specific component | `all` |

  "version": "v1.0.0"| `--output-file` | Save report to file | `` |

}| `--include-logs` | Include recent logs in report | `false` |

```| `--verbose` | Detailed diagnostic output | `false` |



**Service Issues:**#### Examples

```json

{=== "Full Diagnostics"

  "status": "unhealthy",

  "timestamp": "2024-01-15T10:30:00Z",    ```bash

  "errors": [    # Complete diagnostic report

    "kubernetes_api_unreachable",    pipeops-agent diagnose

    "control_plane_disconnected"    

  ]    # Verbose diagnostics

}    pipeops-agent diagnose --verbose

```    ```



## Monitoring Integration=== "Component Specific"



### SystemD Integration    ```bash

    # Diagnose monitoring stack

```bash    pipeops-agent diagnose --component=monitoring

# Monitor service with systemd    

sudo systemctl enable pipeops-agent    # Diagnose networking

    pipeops-agent diagnose --component=networking

# Set up service dependencies    

sudo systemctl edit pipeops-agent    # Diagnose Kubernetes connectivity

```    pipeops-agent diagnose --component=kubernetes

    ```

### Process Monitoring

=== "Save Report"

```bash

# Check process status    ```bash

ps aux | grep pipeops-agent    # Save diagnostic report to file

    pipeops-agent diagnose --output-file=diagnostic-report.txt

# Monitor resource usage    

top -p $(pgrep pipeops-agent)    # Include logs in report

    pipeops-agent diagnose --include-logs --output-file=full-report.txt

# Check open files    ```

sudo lsof -p $(pgrep pipeops-agent)

```#### Sample Output



## Next Steps```text

PipeOps Agent Diagnostics Report

- [Configuration Management](config.md) — Detailed configuration options=================================

- [Monitoring Setup](monitoring.md) — Health checks and metrics
System Information
   • OS: Linux 5.4.0-74-generic
   • Architecture: x86_64
   • Agent Version: v2.1.0
   • Config File: /etc/pipeops/config.yaml

Agent Health
   • Process Status: Running (PID: 12345)
   • Memory Usage: 256MB / 512MB (50%)
   • CPU Usage: 0.25 cores (25%)
   • Uptime: 2d 5h 32m

Configuration
   • Config File: Valid
   • Required Fields: Present
   • Secret References: Resolved
   • Schema Validation: Passed

Connectivity
   • Control Plane: Connected (latency: 45ms)
   • Kubernetes API: Accessible
   • DNS Resolution: Working
   • Internet Access: Available

Kubernetes Cluster
   • Cluster Name: production-cluster
   • Kubernetes Version: v1.25.0
   • Node Count: 3 (all ready)
   • Pod Count: 47 (45 running, 2 pending)

Monitoring Stack
   • Prometheus: Healthy (9090)
   • Grafana: Healthy (3000)  
   • Loki: Healthy (3100)
   • Metrics Collection: Active

Warnings
   • Disk usage is at 85% on /var/log
   • 2 pods are in pending state

Resource Usage
   • CPU: 2.5 / 4.0 cores (62%)
   • Memory: 6.2GB / 8GB (77%)
   • Disk: 42GB / 50GB (84%)

Recommendations
   1. Consider increasing disk space for /var/log
   2. Investigate pending pods in kube-system namespace
   3. Monitor memory usage trends
```

### `logs` - View Logs

Access agent and component logs with filtering and real-time monitoring.

```bash
pipeops-agent logs [options]
```

#### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--tail` | Number of lines to show | `100` |
| `--follow` | Follow log output | `false` |
| `--since` | Show logs since timestamp | `` |
| `--component` | Filter by component | `agent` |
| `--level` | Filter by log level | `all` |
| `--format` | Output format: `text`, `json` | `text` |

#### Examples

=== "Basic Log Viewing"

    ```bash
    # View recent logs
    pipeops-agent logs
    
    # Show last 500 lines
    pipeops-agent logs --tail=500
    
    # Follow logs in real-time
    pipeops-agent logs --follow
    ```

=== "Time-based Filtering"

    ```bash
    # Logs from last hour
    pipeops-agent logs --since=1h
    
    # Logs from specific timestamp
    pipeops-agent logs --since="2023-10-26T10:00:00Z"
    
    # Logs from last 30 minutes
    pipeops-agent logs --since=30m
    ```

=== "Component Filtering"

    ```bash
    # Agent logs only
    pipeops-agent logs --component=agent
    
    # Prometheus logs
    pipeops-agent logs --component=prometheus
    
    # Grafana logs
    pipeops-agent logs --component=grafana
    
    # All monitoring components
    pipeops-agent logs --component=monitoring
    ```

=== "Level Filtering"

    ```bash
    # Error logs only
    pipeops-agent logs --level=error
    
    # Warning and error logs
    pipeops-agent logs --level=warn
    
    # Debug logs (verbose)
    pipeops-agent logs --level=debug
    ```

#### Sample Output

```text
2023-10-26T15:30:15Z [INFO] Agent started successfully
2023-10-26T15:30:16Z [INFO] Kubernetes API connection established
2023-10-26T15:30:17Z [INFO] Control plane connection established  
2023-10-26T15:30:18Z [INFO] Monitoring stack initialized
2023-10-26T15:30:20Z [INFO] Heartbeat sent to control plane
2023-10-26T15:30:45Z [WARN] High memory usage detected: 85%
2023-10-26T15:31:00Z [INFO] Metrics collection completed
2023-10-26T15:31:15Z [INFO] Health check passed
```

## Security Commands

### `token` - Token Management

Manage authentication tokens for secure communication with the PipeOps control plane.

#### `token validate` - Validate Token

Verify that the authentication token is valid and has required permissions.

```bash
pipeops-agent token validate [options]
```

#### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--token` | Token to validate (overrides config) | `` |
| `--verbose` | Show detailed validation results | `false` |

#### Examples

```bash
# Validate current token from config
pipeops-agent token validate

# Validate specific token
pipeops-agent token validate --token="eyJhbGciOiJIUzI1NiIs..."

# Verbose validation
pipeops-agent token validate --verbose
```

#### Output

```text
Token Validation Results
   • Token Format: Valid JWT
   • Signature: Valid
   • Expiration: Valid (expires in 29 days)
   • Permissions: cluster:read,write monitoring:read
   • Issuer: api.pipeops.io
   • Subject: cluster-prod-001
```

#### `token refresh` - Refresh Token

Refresh the authentication token to extend its validity period.

```bash
pipeops-agent token refresh [options]
```

#### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--save-to` | Save new token to file | `` |
| `--update-config` | Update token in configuration file | `false` |

#### Examples

```bash
# Refresh token
pipeops-agent token refresh

# Refresh and save to file
pipeops-agent token refresh --save-to=/etc/pipeops/new-token

# Refresh and update config
pipeops-agent token refresh --update-config
```

## Monitoring Commands

### `metrics` - System Metrics

Display comprehensive system and application metrics.

```bash
pipeops-agent metrics [options]
```

#### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--type` | Metric type: `cpu`, `memory`, `disk`, `network` | `all` |
| `--duration` | Time range: `1h`, `24h`, `7d` | `1h` |
| `--format` | Output format: `table`, `json`, `csv` | `table` |
| `--watch` | Watch metrics in real-time | `false` |

#### Examples

=== "All Metrics"

    ```bash
    # All metrics for last hour
    pipeops-agent metrics
    
    # Metrics for last 24 hours
    pipeops-agent metrics --duration=24h
    ```

=== "Specific Metrics"

    ```bash
    # CPU metrics only
    pipeops-agent metrics --type=cpu
    
    # Memory usage
    pipeops-agent metrics --type=memory
    
    # Network statistics
    pipeops-agent metrics --type=network
    ```

=== "Real-time Monitoring"

    ```bash
    # Watch metrics in real-time
    pipeops-agent metrics --watch
    
    # Watch specific metric type
    pipeops-agent metrics --type=cpu --watch
    ```

## Utility Commands

### `version` - Version Information

Display version and build information for the agent.

```bash
pipeops-agent version [options]
```

#### Options

| Flag | Description | Default |
|------|-------------|---------|
| `--short` | Show version number only | `false` |
| `--build` | Show build information | `false` |
| `--check` | Check for available updates | `false` |

#### Examples

```bash
# Full version information
pipeops-agent version

# Short version only
pipeops-agent version --short

# Build details
pipeops-agent version --build

# Check for updates
pipeops-agent version --check
```

#### Output

```text
PipeOps Kubernetes Agent
Version: v2.1.0
Build Date: 2023-10-26T10:30:00Z
Git Commit: a1b2c3d4e5f6
Go Version: go1.21.3
Platform: linux/amd64
```

---

**Related Commands**: [Cluster Management](cluster.md) | [Configuration](../getting-started/configuration.md) | [Troubleshooting](../advanced/troubleshooting.md)