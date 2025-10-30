# Talos Integration

Talos Linux is an immutable operating system that delivers a hardened, Kubernetes-first experience. The PipeOps agent can work alongside Talos in two primary scenarios:

1. **Docker-based Talos clusters for local testing** – quick to spin up with `talosctl cluster create`.
2. **Production Talos installations** – bare-metal or cloud images booted directly into Talos.

This guide explains how the agent’s installer interacts with Talos, the prerequisites you need, and the recommended workflows for both development and production.

## Before You Start

- Install `talosctl` (see [talos.dev](https://www.talos.dev/latest/introduction/getting-started/#install-talosctl)). The PipeOps installer can download it automatically when using the Docker-based flow.
- Ensure Docker is available if you plan to use the lightweight Talos-in-Docker option. For bare-metal Talos, follow the official Talos installation steps to provision nodes first.
- Keep your PipeOps agent token handy; you will need it regardless of the cluster type.

## Using the PipeOps Installer with Talos (Docker Mode)

The installer can create a disposable Talos cluster inside Docker for demonstrations or local development. This is not intended for production workloads.

```bash
export PIPEOPS_TOKEN="your-token"
export CLUSTER_NAME="talos-demo"
export CLUSTER_TYPE="talos"
export TALOS_USE_DOCKER="true"

curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/scripts/install.sh | bash
```

What the installer does in this mode:

- Downloads or verifies `talosctl`.
- Creates a Talos control plane and worker nodes inside Docker.
- Generates kubeconfig and configures `kubectl` access.
- Deploys the PipeOps agent and monitoring stack to the Talos Kubernetes API.

### Limitations

- Docker-based Talos clusters are ephemeral and resource-limited. They are perfect for workshops or quick validation but should not be exposed to real workloads.
- The Docker backend listens on localhost; remote access must go through `kubectl port-forward` or SSH tunnelling.

## Bringing Your Own Talos Cluster (Production)

For production deployments you should provision Talos nodes following the official documentation—either by booting cloud images or installing from ISO on bare metal. Once you have a functional Talos control plane:

1. **Obtain cluster credentials**
   - Use `talosctl kubeconfig --nodes <control-plane-ip>` to generate a kubeconfig.
   - Point `KUBECONFIG` to the generated file so `kubectl` communicates with the Talos cluster.

2. **Install the PipeOps agent**

   Choose either the Helm chart or direct manifest deployment:

   === "Helm"

    ```bash
    helm upgrade --install pipeops-agent oci://ghcr.io/pipeopshq/pipeops-agent \
      --namespace pipeops-system \
      --create-namespace \
      --set agent.pipeops.token="your-pipeops-token" \
      --set agent.cluster.name="production-talos"
    ```

   === "Manifest"

    ```bash
    curl -fsSL https://raw.githubusercontent.com/PipeOpsHQ/pipeops-k8-agent/main/deployments/agent.yaml \
      | PIPEOPS_TOKEN="your-pipeops-token" \
        PIPEOPS_CLUSTER_NAME="production-talos" \
        envsubst \
      | kubectl apply -f -
    ```

3. **Verify agent registration**
   - `kubectl get pods -n pipeops-system` should show the agent and monitoring components.
   - The PipeOps dashboard should list the Talos-backed server within a few minutes.

### Why the install script warns on Talos

The intelligent installer cannot install Talos on top of an existing OS; it can only drive the Docker-based variant. When it detects a Talos-friendly environment it will suggest Talos but still fall back to k3s if prerequisites are missing. The warning message is a reminder that production Talos nodes must be provisioned outside the script.

## Troubleshooting

| Symptom | Resolution |
| ------- | ---------- |
| `talosctl: command not found` | Install `talosctl` or let the installer run with sufficient privileges to place it in `/usr/local/bin`. |
| Docker mode fails with networking errors | Ensure Docker is running and `talosctl cluster destroy <name>` any previous clusters before retrying. |
| Agent pods crash on Talos | Confirm the agent chart version is up to date and that Talos cluster nodes allow the required container images (`ghcr.io/pipeopshq/...`). |
| Installer suggests Talos but exits | Export `CLUSTER_TYPE=k3s` for traditional k3s installs, or follow the production Talos instructions above. |

For additional help deploying Talos itself, consult the official [Talos documentation](https://www.talos.dev/latest/). For PipeOps agent issues, open a ticket via the PipeOps support portal or GitHub.
