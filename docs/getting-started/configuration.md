# Configuration

Learn how to configure the PipeOps Kubernetes Agent for your specific environment and requirements. This guide covers all configuration options and best practices.

## 📋 Configuration Overview

The PipeOps Agent can be configured through multiple methods:

- **Environment variables** - For runtime configuration
- **Configuration files** - For persistent settings  
- **Helm values** - For Kubernetes deployments
- **Command-line flags** - For one-time overrides

## 🔧 Configuration Methods

=== "Configuration File"

    **Primary method** - Create `/etc/pipeops/config.yaml`:

    ```yaml
    # Complete configuration example
    cluster:
      name: "production-cluster"
      region: "us-west-2"
      environment: "production"
      tags:
        team: "platform"
        cost-center: "engineering"
        
    agent:
      token: "${PIPEOPS_TOKEN}"
      endpoint: "https://api.pipeops.io"
      log_level: "info"
      heartbeat_interval: "30s"
      max_retries: 3
      timeout: "60s"
      
    monitoring:
      enabled: true
      namespace: "pipeops-system"
      retention_days: 30
      prometheus:
        enabled: true
        port: 9090
        scrape_interval: "15s"
      grafana:
        enabled: true
        port: 3000
        admin_password: "${GRAFANA_ADMIN_PASSWORD}"
      loki:
        enabled: true
        port: 3100
        retention_period: "30d"
        
    resources:
      requests:
        cpu: "250m"
        memory: "256Mi"
      limits:
        cpu: "500m"
        memory: "512Mi"
        
    security:
      tls:
        enabled: true
        cert_file: "/etc/ssl/certs/pipeops.crt"
        key_file: "/etc/ssl/private/pipeops.key"
      rbac:
        enabled: true
        service_account: "pipeops-agent"
        
    networking:
      tunnel:
        enabled: true
        port: 8443
        keepalive: "30s"
      proxy:
        http_proxy: "${HTTP_PROXY}"
        https_proxy: "${HTTPS_PROXY}"
        no_proxy: "${NO_PROXY}"
    ```

=== "Environment Variables"

    **Quick configuration** - Set environment variables:

    ```bash
    # Core configuration
    export PIPEOPS_TOKEN="your-cluster-token"
    export PIPEOPS_CLUSTER_NAME="production-cluster"
    export PIPEOPS_LOG_LEVEL="info"
    export PIPEOPS_ENDPOINT="https://api.pipeops.io"
    
    # Monitoring configuration
    export PIPEOPS_MONITORING_ENABLED="true"
    export PIPEOPS_PROMETHEUS_ENABLED="true"
    export PIPEOPS_GRAFANA_ENABLED="true"
    
    # Resource limits
    export PIPEOPS_CPU_LIMIT="500m"
    export PIPEOPS_MEMORY_LIMIT="512Mi"
    
    # Security settings
    export PIPEOPS_TLS_ENABLED="true"
    export PIPEOPS_RBAC_ENABLED="true"
    ```

=== "Helm Values"

    **Kubernetes deployments** - Create `values.yaml`:

    ```yaml
    # Helm chart values
    agent:
      image:
        repository: pipeops/agent
        tag: "latest"
        pullPolicy: IfNotPresent
      
      cluster:
        name: "production-cluster"
        region: "us-west-2"
        environment: "production"
      
      token: "your-cluster-token"
      logLevel: "info"
      
      resources:
        requests:
          cpu: 250m
          memory: 256Mi
        limits:
          cpu: 500m
          memory: 512Mi
    
    monitoring:
      enabled: true
      namespace: pipeops-system
      
      prometheus:
        enabled: true
        retention: "30d"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
      
      grafana:
        enabled: true
        adminPassword: "secure-password"
        persistence:
          enabled: true
          size: 10Gi
      
      loki:
        enabled: true
        persistence:
          enabled: true
          size: 50Gi
    
    security:
      serviceAccount:
        create: true
        name: pipeops-agent
      
      rbac:
        create: true
      
      tls:
        enabled: true
        secretName: pipeops-tls
    
    networking:
      tunnel:
        enabled: true
        port: 8443
      
      service:
        type: ClusterIP
        port: 8080
    ```

=== "Command Line"

    **Override configuration** - Use CLI flags:

    ```bash
    # Start with custom configuration
    pipeops-agent start \
      --cluster-name="test-cluster" \
      --log-level="debug" \
      --endpoint="https://staging.pipeops.io" \
      --monitoring-enabled=false
    
    # Deploy with specific settings
    pipeops-agent deploy \
      --namespace="custom-namespace" \
      --replicas=3 \
      --cpu-limit="1000m" \
      --memory-limit="1Gi"
    ```

## ⚙️ Configuration Sections

### Cluster Configuration

Configure cluster identification and metadata:

```yaml
cluster:
  name: "production-cluster"        # Required: Unique cluster identifier
  region: "us-west-2"              # Optional: AWS/GCP/Azure region
  environment: "production"        # Optional: Environment label
  provider: "aws"                  # Optional: Cloud provider
  version: "1.25"                  # Optional: Kubernetes version
  tags:                           # Optional: Custom labels
    team: "platform"
    cost-center: "engineering"
    project: "main-app"
```

### Agent Configuration

Core agent behavior settings:

```yaml
agent:
  token: "${PIPEOPS_TOKEN}"        # Required: Cluster authentication token
  endpoint: "https://api.pipeops.io"  # Control plane endpoint
  log_level: "info"                # Logging level: debug, info, warn, error
  heartbeat_interval: "30s"        # Health check frequency
  max_retries: 3                   # Connection retry attempts
  timeout: "60s"                   # Request timeout
  user_agent: "PipeOps-Agent/2.1.0"  # Custom user agent
  
  # Advanced settings
  buffer_size: 1024               # Message buffer size
  worker_count: 5                 # Concurrent workers
  batch_size: 100                 # Batch processing size
```

### Monitoring Configuration

Comprehensive monitoring stack setup:

```yaml
monitoring:
  enabled: true                    # Enable monitoring stack
  namespace: "pipeops-system"      # Kubernetes namespace
  retention_days: 30              # Data retention period
  
  prometheus:
    enabled: true
    port: 9090
    scrape_interval: "15s"        # Metrics collection frequency
    evaluation_interval: "15s"    # Rule evaluation frequency
    retention: "30d"              # Metric retention period
    storage_size: "50Gi"          # Storage allocation
    
    # External labels for federation
    external_labels:
      cluster: "production"
      region: "us-west-2"
    
    # Additional scrape configs
    additional_scrape_configs: |
      - job_name: 'custom-app'
        static_configs:
          - targets: ['app:8080']
  
  grafana:
    enabled: true
    port: 3000
    admin_password: "${GRAFANA_ADMIN_PASSWORD}"
    
    # Persistence
    persistence:
      enabled: true
      size: "10Gi"
      storage_class: "fast-ssd"
    
    # Additional dashboards
    dashboards:
      default:
        kubernetes-overview:
          url: https://grafana.com/api/dashboards/7249/revisions/1/download
    
    # Data sources
    datasources:
      prometheus:
        url: "http://prometheus:9090"
        access: proxy
  
  loki:
    enabled: true
    port: 3100
    retention_period: "30d"
    
    # Storage configuration
    storage:
      type: "filesystem"           # filesystem, s3, gcs
      filesystem:
        directory: "/loki/chunks"
      
    # Log ingestion limits
    limits:
      ingestion_rate_mb: 10
      ingestion_burst_size_mb: 20
```

### Security Configuration

Security and access control settings:

```yaml
security:
  # TLS Configuration
  tls:
    enabled: true
    cert_file: "/etc/ssl/certs/pipeops.crt"
    key_file: "/etc/ssl/private/pipeops.key"
    ca_file: "/etc/ssl/certs/ca.crt"
    verify_client: true
    min_version: "1.2"            # Minimum TLS version
  
  # RBAC Configuration
  rbac:
    enabled: true
    service_account: "pipeops-agent"
    cluster_role: "pipeops-agent"
    
    # Custom permissions
    rules:
      - apiGroups: [""]
        resources: ["pods", "services", "configmaps"]
        verbs: ["get", "list", "watch", "create", "update"]
      - apiGroups: ["apps"]
        resources: ["deployments", "replicasets"]
        verbs: ["get", "list", "watch", "create", "update", "delete"]
  
  # Network policies
  network_policy:
    enabled: true
    ingress:
      - from:
          - namespaceSelector:
              matchLabels:
                name: pipeops-system
    egress:
      - to: []  # Allow all outbound traffic
  
  # Pod security
  pod_security:
    security_context:
      run_as_non_root: true
      run_as_user: 1000
      fs_group: 2000
    
    seccomp_profile:
      type: RuntimeDefault
```

### Networking Configuration

Network and connectivity settings:

```yaml
networking:
  # Secure tunnel configuration
  tunnel:
    enabled: true
    port: 8443
    keepalive: "30s"
    compression: true
    
    # Connection settings
    dial_timeout: "10s"
    idle_timeout: "300s"
    max_idle_conns: 100
  
  # Proxy configuration
  proxy:
    http_proxy: "${HTTP_PROXY}"
    https_proxy: "${HTTPS_PROXY}"
    no_proxy: "${NO_PROXY}"
  
  # DNS configuration
  dns:
    nameservers:
      - "8.8.8.8"
      - "8.8.4.4"
    search_domains:
      - "cluster.local"
      - "svc.cluster.local"
  
  # Service mesh integration
  service_mesh:
    enabled: false
    type: "istio"                 # istio, linkerd, consul
    mtls_mode: "strict"
```

### Resource Configuration

Resource limits and requests:

```yaml
resources:
  # Agent resources
  requests:
    cpu: "250m"
    memory: "256Mi"
    ephemeral-storage: "1Gi"
  limits:
    cpu: "500m"
    memory: "512Mi"
    ephemeral-storage: "2Gi"
  
  # Monitoring stack resources
  monitoring:
    prometheus:
      requests:
        cpu: "100m"
        memory: "128Mi"
      limits:
        cpu: "500m"
        memory: "1Gi"
    
    grafana:
      requests:
        cpu: "50m"
        memory: "64Mi"
      limits:
        cpu: "200m"
        memory: "256Mi"
    
    loki:
      requests:
        cpu: "50m"
        memory: "128Mi"
      limits:
        cpu: "200m"
        memory: "512Mi"
```

## 🎯 Environment-Specific Configurations

### Development Environment

```yaml
# dev-config.yaml
cluster:
  name: "dev-cluster"
  environment: "development"

agent:
  log_level: "debug"
  heartbeat_interval: "10s"

monitoring:
  enabled: true
  retention_days: 7

resources:
  requests:
    cpu: "100m"
    memory: "128Mi"
  limits:
    cpu: "250m"
    memory: "256Mi"
```

### Staging Environment

```yaml
# staging-config.yaml
cluster:
  name: "staging-cluster"
  environment: "staging"

agent:
  log_level: "info"
  heartbeat_interval: "30s"

monitoring:
  enabled: true
  retention_days: 14

resources:
  requests:
    cpu: "250m"
    memory: "256Mi"
  limits:
    cpu: "500m"
    memory: "512Mi"
```

### Production Environment

```yaml
# production-config.yaml
cluster:
  name: "production-cluster"
  environment: "production"

agent:
  log_level: "warn"
  heartbeat_interval: "30s"
  max_retries: 5

monitoring:
  enabled: true
  retention_days: 90

security:
  tls:
    enabled: true
  rbac:
    enabled: true

resources:
  requests:
    cpu: "500m"
    memory: "512Mi"
  limits:
    cpu: "1000m"
    memory: "1Gi"
```

## 🔐 Secret Management

### Using Kubernetes Secrets

Create secrets for sensitive configuration:

```bash
# Create secret for agent token
kubectl create secret generic pipeops-token \
  --from-literal=token="your-cluster-token" \
  -n pipeops-system

# Create TLS secret
kubectl create secret tls pipeops-tls \
  --cert=path/to/tls.crt \
  --key=path/to/tls.key \
  -n pipeops-system

# Create Grafana admin password secret
kubectl create secret generic grafana-admin \
  --from-literal=password="secure-grafana-password" \
  -n pipeops-system
```

Reference secrets in configuration:

```yaml
agent:
  token:
    secretKeyRef:
      name: pipeops-token
      key: token

security:
  tls:
    secretName: pipeops-tls

monitoring:
  grafana:
    admin_password:
      secretKeyRef:
        name: grafana-admin
        key: password
```

### Using External Secret Management

=== "HashiCorp Vault"

    ```yaml
    agent:
      token: "vault:secret/pipeops#token"
    
    security:
      tls:
        cert_file: "vault:secret/tls#cert"
        key_file: "vault:secret/tls#key"
    ```

=== "AWS Secrets Manager"

    ```yaml
    agent:
      token: "aws:secretsmanager:pipeops-token:SecretString:token"
    
    monitoring:
      grafana:
        admin_password: "aws:secretsmanager:grafana-admin:SecretString:password"
    ```

=== "Azure Key Vault"

    ```yaml
    agent:
      token: "azure:keyvault:pipeops-vault:pipeops-token"
    
    security:
      tls:
        cert_file: "azure:keyvault:pipeops-vault:tls-cert"
        key_file: "azure:keyvault:pipeops-vault:tls-key"
    ```

## ✅ Configuration Validation

### Validate Configuration

```bash
# Validate configuration file
pipeops-agent config validate --config=/etc/pipeops/config.yaml

# Test connection with configuration
pipeops-agent config test --config=/etc/pipeops/config.yaml

# Show resolved configuration
pipeops-agent config show --config=/etc/pipeops/config.yaml
```

### Configuration Schema

The agent supports JSON Schema validation:

```bash
# Download configuration schema
curl -O https://schemas.pipeops.io/agent/v2/config.schema.json

# Validate against schema
jsonschema -i config.yaml config.schema.json
```

## 🔧 Advanced Configuration

### Custom Resource Definitions

Define custom monitoring targets:

```yaml
# Custom CRD example
apiVersion: monitoring.pipeops.io/v1
kind: ServiceMonitor
metadata:
  name: custom-app-monitor
spec:
  selector:
    matchLabels:
      app: custom-app
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

### Plugin Configuration

Configure agent plugins:

```yaml
plugins:
  enabled:
    - "network-monitor"
    - "cost-optimizer"
    - "security-scanner"
  
  network-monitor:
    config:
      check_interval: "60s"
      endpoints:
        - "https://api.example.com"
        - "https://db.example.com:5432"
  
  cost-optimizer:
    config:
      scan_interval: "24h"
      optimization_mode: "balanced"  # aggressive, balanced, conservative
      
  security-scanner:
    config:
      scan_interval: "12h"
      vulnerability_threshold: "medium"
```

## 📝 Configuration Best Practices

### 1. Use Environment-Specific Configs

```bash
# Directory structure
/etc/pipeops/
├── config.yaml                 # Base configuration
├── environments/
│   ├── dev.yaml                # Development overrides
│   ├── staging.yaml            # Staging overrides
│   └── production.yaml         # Production overrides
└── secrets/
    ├── tokens.yaml             # Secret references
    └── certificates/           # TLS certificates
```

### 2. Configuration Inheritance

```yaml
# Base config.yaml
defaults: &defaults
  agent:
    heartbeat_interval: "30s"
    timeout: "60s"
  monitoring:
    enabled: true

# Environment-specific configs
development:
  <<: *defaults
  agent:
    log_level: "debug"
  
production:
  <<: *defaults
  agent:
    log_level: "warn"
  security:
    tls:
      enabled: true
```

### 3. Version Control Integration

```bash
# Git hooks for configuration validation
#!/bin/bash
# .git/hooks/pre-commit
pipeops-agent config validate --config=config.yaml
if [ $? -ne 0 ]; then
    echo "Configuration validation failed"
    exit 1
fi
```

## 🔍 Troubleshooting Configuration

### Common Configuration Issues

??? question "Agent fails to start with configuration errors"

    **Check configuration syntax**:
    ```bash
    pipeops-agent config validate --config=/etc/pipeops/config.yaml
    ```
    
    **Common issues**:
    - Invalid YAML syntax
    - Missing required fields
    - Incorrect data types
    - Invalid enum values

??? question "Secret references not resolving"

    **Verify secret exists**:
    ```bash
    kubectl get secret pipeops-token -n pipeops-system
    ```
    
    **Check RBAC permissions**:
    ```bash
    kubectl auth can-i get secrets --as=system:serviceaccount:pipeops-system:pipeops-agent
    ```

??? question "Monitoring stack not starting"

    **Check resource limits**:
    ```bash
    kubectl describe pod -l app=prometheus -n pipeops-system
    ```
    
    **Verify storage class**:
    ```bash
    kubectl get storageclass
    ```

### Debug Configuration Loading

Enable debug logging to see configuration loading:

```bash
export PIPEOPS_LOG_LEVEL=debug
pipeops-agent start --config=/etc/pipeops/config.yaml
```

Look for these log messages:
- `Loading configuration from file`
- `Resolving environment variables`
- `Validating configuration schema`
- `Configuration loaded successfully`

---

**Next Steps**: [Architecture Guide](../ARCHITECTURE.md) | [Advanced Monitoring](../advanced/monitoring.md)