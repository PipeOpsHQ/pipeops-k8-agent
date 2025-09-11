# PipeOps Agent Helm Chart

A Helm chart for deploying the PipeOps Kubernetes Agent.

## Description

The PipeOps Agent is a Kubernetes agent that connects your cluster to the PipeOps platform, enabling cluster management, monitoring, and deployment capabilities.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.2.0+
- A PipeOps account and API token

## Installing the Chart

To install the chart with the release name `pipeops-agent`:

```bash
helm install pipeops-agent ./helm/pipeops-agent \
  --set agent.pipeops.token="your-pipeops-token" \
  --set agent.cluster.name="your-cluster-name"
```

The command deploys the PipeOps Agent on the Kubernetes cluster in the default configuration. The [Parameters](#parameters) section lists the parameters that can be configured during installation.

## Uninstalling the Chart

To uninstall/delete the `pipeops-agent` deployment:

```bash
helm delete pipeops-agent
```

The command removes all the Kubernetes components associated with the chart and deletes the release.

## Parameters

### Global parameters

| Name                      | Description                                     | Value |
| ------------------------- | ----------------------------------------------- | ----- |
| `nameOverride`            | String to partially override common.names.name | `""`  |
| `fullnameOverride`        | String to fully override common.names.fullname | `""`  |

### Image parameters

| Name                | Description                                          | Value                              |
| ------------------- | ---------------------------------------------------- | ---------------------------------- |
| `image.repository`  | PipeOps Agent image repository                       | `ghcr.io/pipeops/pipeops-k8-agent` |
| `image.tag`         | PipeOps Agent image tag (immutable tags recommended) | `latest`                           |
| `image.pullPolicy`  | PipeOps Agent image pull policy                      | `IfNotPresent`                     |
| `imagePullSecrets`  | PipeOps Agent image pull secrets                     | `[]`                               |

### Deployment parameters

| Name           | Description                                      | Value |
| -------------- | ------------------------------------------------ | ----- |
| `replicaCount` | Number of PipeOps Agent replicas to deploy       | `1`   |

### Namespace parameters

| Name              | Description                           | Value              |
| ----------------- | ------------------------------------- | ------------------ |
| `namespace.create`| Create namespace                      | `true`             |
| `namespace.name`  | Namespace name                        | `pipeops-system`   |

### ServiceAccount parameters

| Name                         | Description                                                            | Value   |
| ---------------------------- | ---------------------------------------------------------------------- | ------- |
| `serviceAccount.create`      | Specifies whether a ServiceAccount should be created                  | `true`  |
| `serviceAccount.name`        | The name of the ServiceAccount to use                                 | `""`    |
| `serviceAccount.annotations` | Additional Service Account annotations                                 | `{}`    |

### RBAC parameters

| Name          | Description                     | Value   |
| ------------- | ------------------------------- | ------- |
| `rbac.create` | Specifies whether RBAC resources should be created | `true`  |

### PipeOps Agent Configuration

| Name                            | Description                                | Value                      |
| ------------------------------- | ------------------------------------------ | -------------------------- |
| `agent.pipeops.apiUrl`          | PipeOps API URL                            | `wss://api.pipeops.io`     |
| `agent.pipeops.token`           | PipeOps API token                          | `""`                       |
| `agent.cluster.name`            | Cluster name in PipeOps                    | `""`                       |
| `agent.id`                      | Optional agent ID                          | `""`                       |
| `agent.kubernetes.inCluster`    | Use in-cluster Kubernetes configuration   | `true`                     |

### Security Context parameters

| Name                                | Description                                      | Value     |
| ----------------------------------- | ------------------------------------------------ | --------- |
| `podSecurityContext.runAsNonRoot`   | Set PipeOps Agent pod's Security Context runAsNonRoot | `true`    |
| `podSecurityContext.runAsUser`      | Set PipeOps Agent pod's Security Context runAsUser    | `65532`   |
| `podSecurityContext.runAsGroup`     | Set PipeOps Agent pod's Security Context runAsGroup   | `65532`   |
| `podSecurityContext.fsGroup`        | Set PipeOps Agent pod's Security Context fsGroup      | `65532`   |
| `securityContext.allowPrivilegeEscalation` | Allow privilege escalation for PipeOps Agent container | `false` |
| `securityContext.capabilities.drop` | List of capabilities to be dropped               | `["ALL"]` |
| `securityContext.readOnlyRootFilesystem` | Mount container's root filesystem as read-only | `true`    |
| `securityContext.runAsNonRoot`      | Run container as non-root user                   | `true`    |
| `securityContext.runAsUser`         | User ID to run the container                     | `65532`   |

### Resource Limits

| Name                    | Description                                     | Value    |
| ----------------------- | ----------------------------------------------- | -------- |
| `resources.limits.cpu`  | The CPU limit for the PipeOps Agent container  | `500m`   |
| `resources.limits.memory` | The memory limit for the PipeOps Agent container | `512Mi` |
| `resources.requests.cpu` | The requested CPU for the PipeOps Agent container | `100m`  |
| `resources.requests.memory` | The requested memory for the PipeOps Agent container | `128Mi` |

### Service parameters

| Name                  | Description                                        | Value     |
| --------------------- | -------------------------------------------------- | --------- |
| `service.enabled`     | Enable service                                     | `false`   |
| `service.type`        | PipeOps Agent service type                         | `ClusterIP` |
| `service.annotations` | Additional custom annotations for PipeOps Agent service | `{}`   |

### Autoscaling parameters

| Name                                            | Description                                                                            | Value   |
| ----------------------------------------------- | -------------------------------------------------------------------------------------- | ------- |
| `autoscaling.enabled`                           | Enable Horizontal Pod Autoscaler (HPA)                                                | `false` |
| `autoscaling.minReplicas`                       | Minimum number of PipeOps Agent replicas                                              | `1`     |
| `autoscaling.maxReplicas`                       | Maximum number of PipeOps Agent replicas                                              | `100`   |
| `autoscaling.targetCPUUtilizationPercentage`    | Target CPU utilization percentage                                                      | `80`    |

### Other Parameters

| Name           | Description                                      | Value |
| -------------- | ------------------------------------------------ | ----- |
| `nodeSelector` | Node labels for PipeOps Agent pods assignment   | `{}`  |
| `tolerations`  | Tolerations for PipeOps Agent pods assignment   | `[]`  |
| `affinity`     | Affinity for PipeOps Agent pods assignment      | `{}`  |

## Configuration and installation details

### Setting up the PipeOps Agent

1. **Get your PipeOps API token**: Log into your PipeOps dashboard and generate an API token for cluster integration.

2. **Install the chart with required values**:
   ```bash
   helm install pipeops-agent ./helm/pipeops-agent \
     --set agent.pipeops.token="your-api-token-here" \
     --set agent.cluster.name="my-production-cluster"
   ```

3. **Verify the installation**:
   ```bash
   kubectl get pods -n pipeops-system
   kubectl logs -n pipeops-system deployment/pipeops-agent
   ```

### Upgrading

To upgrade the chart:

```bash
helm upgrade pipeops-agent ./helm/pipeops-agent \
  --set agent.pipeops.token="your-api-token" \
  --set agent.cluster.name="your-cluster-name"
```

### Customizing the installation

You can customize the installation by creating a `values.yaml` file:

```yaml
# values.yaml
agent:
  pipeops:
    token: "your-pipeops-token"
  cluster:
    name: "production-cluster"

resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 1000m
    memory: 1Gi

nodeSelector:
  node-type: monitoring
```

Then install with:

```bash
helm install pipeops-agent ./helm/pipeops-agent -f values.yaml
```

## Troubleshooting

### Agent not connecting to PipeOps

1. Check if the API token is correct
2. Verify network connectivity to `api.pipeops.io`
3. Check the agent logs for authentication errors

### Permission issues

The agent requires cluster-wide permissions to manage resources. Ensure RBAC is enabled (`rbac.create=true`) and the service account has the necessary permissions.

### Pod not starting

Check the pod events and logs:

```bash
kubectl describe pod -n pipeops-system -l app.kubernetes.io/name=pipeops-agent
kubectl logs -n pipeops-system -l app.kubernetes.io/name=pipeops-agent
```

Common issues:
- Image pull failures: Check `imagePullSecrets` configuration
- Resource constraints: Adjust `resources` values
- Security context issues: Review `securityContext` settings

## License

This chart is licensed under the same license as the PipeOps Agent project.
