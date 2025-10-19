# Install the PipeOps Agent Directly from GitHub

This guide shows how to bootstrap a PipeOps-managed Kubernetes environment using the public installation script that lives in this repository (`scripts/install.sh`). The script supports intelligent cluster detection, automated monitoring setup, and both server and worker node flows.

> The commands below download the script straight from GitHub. Feel free to clone the repo instead if you prefer to review the script locally before running it.

## Prerequisites

- `curl` and `bash`
- `sudo` privileges (required when the script installs k3s or configures system services)
- A PipeOps control plane token with permissions to register clusters
- Optional: Docker (for k3d/kind installs) or virtualization enabled (for minikube)

## 1. Export the Required Environment Variables

```bash
export PIPEOPS_TOKEN="your-pipeops-token"
# Optional but recommended for clarity in the dashboard
export CLUSTER_NAME="my-pipeops-cluster"
# Optional: pin a specific distribution (k3s|minikube|k3d|kind|auto)
# export CLUSTER_TYPE="auto"
```

The installer reads additional toggles such as `AUTO_DETECT`, `DISABLE_MONITORING`, or `PIPEOPS_AGENT_VERSION`. See `scripts/README.md` for the full matrix of inputs.

## 2. Run the Installer from GitHub

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh)
```

What happens under the hood:

1. System resources and environment are analyzed (CPU, RAM, disks, Docker, virtualization, cloud vendor, etc.).
2. The optimal Kubernetes distribution is selected (or the value you set in `CLUSTER_TYPE` is honored).
3. A cluster is bootstrapped and the PipeOps agent is deployed.
4. The monitoring stack (Prometheus, Loki, Grafana, OpenCost) is installed unless you disable it.
5. Connection details are printed so additional worker nodes can join safely.

## 3. Verify the Installation

```bash
kubectl get pods -n pipeops-system
kubectl get pods -n pipeops-monitoring
```

You should see the agent pod plus the monitoring components in the `Running` state. The installer logs also display the tunnel endpoints that the control plane will expose.

## 4. Join Additional Worker Nodes (Optional)

On each worker machine run:

```bash
export K3S_URL="https://<server-ip>:6443"
export K3S_TOKEN="<token printed by install.sh>"
bash <(curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/join-worker.sh)
```

Alternatively, rerun `install.sh cluster-info` on the server to display the join command.

## 5. Updating or Uninstalling

```bash
# Update the agent and monitoring stack to the latest release
bash <(curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh) update

# Remove the stack (cluster, agent, monitoring)
bash <(curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh) uninstall
```

## Install Only the Agent on an Existing Cluster

Already running Kubernetes? You can deploy just the PipeOps agent without provisioning a new cluster or installing the monitoring stack.

### Requirements

- `kubectl` configured with cluster-admin privileges
- `sed` (available by default on macOS/Linux) for Option A substitutions
- PipeOps control plane credentials (`PIPEOPS_TOKEN`)

### 1. Export the required values

```bash
export PIPEOPS_TOKEN="your-pipeops-token"
export PIPEOPS_CLUSTER_NAME="my-existing-cluster"
```

### 2. Apply the manifest straight from GitHub

The commands below replace the placeholder values in `deployments/agent.yaml` before piping the manifest to `kubectl`:

**Option A – Bash helpers (quickest):**

```bash
curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml \
  | sed "s/PIPEOPS_TOKEN: \"your-token-here\"/PIPEOPS_TOKEN: \"${PIPEOPS_TOKEN}\"/" \
  | sed "s/token: \"your-token-here\"/token: \"${PIPEOPS_TOKEN}\"/" \
  | sed "s/cluster_name: \"default-cluster\"/cluster_name: \"${PIPEOPS_CLUSTER_NAME}\"/" \
  | kubectl apply -f -
```

**Option B – kubectl only (no sed):**

```bash
# Apply core resources (namespace, RBAC, deployment, etc.)
kubectl apply -f https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml \
  --selector app.kubernetes.io/component!=config

# Create/update the secret with your token and metadata
kubectl create secret generic pipeops-agent-config -n pipeops-system \
  --from-literal=PIPEOPS_TOKEN="${PIPEOPS_TOKEN}" \
  --from-literal=PIPEOPS_CLUSTER_NAME="${PIPEOPS_CLUSTER_NAME}" \
  --from-literal=PIPEOPS_API_URL="https://api.pipeops.sh" \
  --dry-run=client -o yaml | kubectl apply -f -

# Create/update the agent ConfigMap with concrete values
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
      labels:
        environment: "production"
        managed-by: "pipeops"
    pipeops:
      api_url: "https://api.pipeops.sh"
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


### 3. Verify the rollout

```bash
kubectl rollout status deployment/pipeops-agent -n pipeops-system
kubectl logs deployment/pipeops-agent -n pipeops-system
```

### 4. Optional tweaks

- To disable the tunnel or adjust monitoring forwards, update the `pipeops-agent-config` ConfigMap before restarting the deployment.
- To customize TLS validation, patch the `PIPEOPS_TLS_*` keys in the same ConfigMap or secret.

When you no longer need the agent, remove it with:

```bash
kubectl delete namespace pipeops-system --ignore-not-found
```

## Troubleshooting Tips

- Use `LOG_LEVEL=debug` to increase script verbosity.
- Set `PIPEOPS_TLS_INSECURE_SKIP_VERIFY=true` if your control plane uses a self-signed certificate.
- Run `./scripts/install.sh --help` after cloning for a full list of supported flags and environment variables.

For advanced scenarios (air-gapped clusters, custom Helm overrides, external etcd) refer to `scripts/README.md` and the examples under `scripts/examples/`.
