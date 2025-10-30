# Expose Ingress With Tailscale Funnel

The PipeOps installation provisions an ingress controller inside the cluster. On single-node installs the ingress service is published on the host network at TCP port 80 (HTTP) and 443 (HTTPS). When the node does not have a public IP or inbound firewall rules, you can still share your ingress endpoints securely by using [Tailscale](https://tailscale.com/) and the Tailscale Funnel feature.

Tailscale Funnel terminates TLS on the edge and forwards traffic to your node over the Tailscale mesh. This guide walks through publishing port 80 of your PipeOps host so that anyone on the internet can reach your ingress resources without opening traditional firewall ports.

!!! note "Public IP already available?"
  If your VM or bare-metal host has a routable public IP address, the simplest option is to allow inbound TCP 80/443 through your firewall or cloud security group and point DNS records at the instance. Use Tailscale Funnel primarily when you cannot open ports directly or prefer the managed tunnel and `*.ts.net` endpoints it provides for self-hosted setups.

## Prerequisites

- The PipeOps agent (or k3s/minikube cluster) is running on a Linux host with ingress-nginx listening on `127.0.0.1:80` and/or `0.0.0.0:80`.
- You can run commands with `sudo` on the node.
- A Tailscale account with [Funnel enabled for your tailnet](https://tailscale.com/kb/1223/funnel). The feature currently requires the tailnet to allow funnel either globally or through ACL tags.
- Tailscale v1.50 or newer installed on the node. Earlier versions do not support `tailscale serve`/`tailscale funnel`.

## 1. Install Tailscale on the node

If Tailscale is not already installed, run the official install script:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
```

Confirm the version meets the requirement:

```bash
sudo tailscale version
```

> **Note:** For non-Debian-based systems, follow the [OS-specific installation instructions](https://tailscale.com/download) before continuing.

## 2. Authenticate the node with your tailnet

Log the node into your tailnet and approve it from the Tailscale admin console when prompted:

```bash
sudo tailscale up --ssh
```

Once connected, double-check that the host appears in the [Machines](https://login.tailscale.com/admin/machines) list and that its key is authorized.

## 3. Grant the device permission to run Funnel

Tailscale only allows Funnel on machines that have the capability explicitly enabled. You have two options:

1. **Per-device toggle:** In the admin console, open the machine details and enable **Funnel** in the *Access controls* section.
2. **ACL tag:** Assign a tag (for example `tag:funnel`) to the node and add an ACL entry that grants the `funnel` capability to that tag. An example policy snippet:

   ```json
   {
     "tagOwners": { "tag:funnel": ["user:you@example.com"] },
     "nodeAttrs": [
       {
         "target": ["tag:funnel"],
         "attrs": ["funnel"]
       }
     ]
   }
   ```

Make sure you save the ACL changes before proceeding.

## 4. Publish ingress traffic through Tailscale

First, verify that ingress-nginx is listening locally on port 80:

```bash
sudo ss -tlnp | grep ':80 '
```

Then instruct Tailscale to proxy external connections to the local ingress port:

```bash
# Forward tailnet HTTP traffic to the local ingress endpoint
sudo tailscale serve tcp 80 127.0.0.1:80

# Allow public internet access to the same service
sudo tailscale funnel 80 on
```

The command above keeps requests encrypted to your node while allowing global HTTPS access via a `*.ts.net` URL assigned by Tailscale.

If your ingress expects a specific `Host` header, configure an HTTP reverse proxy rule instead:

```bash
sudo tailscale serve https / https://127.0.0.1 --set-header "Host: app.pipeops.local"
sudo tailscale funnel https on
```

Refer to `tailscale serve --help` for more serving patterns, including HTTP-to-HTTPS redirects and custom certificates.

## 5. Verify external access

Retrieve the public URL and test that it reaches your ingress:

```bash
# Show the active serve/funnel configuration
sudo tailscale funnel status

# Replace <device> with the machine name shown above
curl -I https://<device>.ts.net/
```

If your ingress routes based on hostnames, append the appropriate header:

```bash
curl -I https://<device>.ts.net/ -H 'Host: my-app.example.com'
```

You should receive the same response headers as if you queried the ingress locally.

## 6. Manage and tear down Funnel access

When you no longer need public exposure, disable the funnel and remove the serve rule:

```bash
sudo tailscale funnel 80 off
sudo tailscale serve reset
```

The node will stay connected to your tailnet for private administration, but inbound traffic from the public internet stops immediately.

## Troubleshooting

- `tailscale funnel` returns `permission denied`: confirm the node has the `funnel` capability through the admin console or ACL tags, and re-run `sudo tailscale up` after ACL changes.
- Requests hang or return 502: ensure ingress-nginx is listening on the specified port. On k3s-based installs you can restart ingress with `kubectl rollout restart deployment ingress-nginx-controller -n ingress-nginx`.
- Need HTTPS passthrough: you can forward port 443 the same way, or terminate TLS inside the cluster by pointing `tailscale serve` at `https://127.0.0.1:443`.
- Keep logs for audits: Tailscale exposes serve/funnel logs via `sudo journalctl -u tailscaled`.

That is all that is required to publish your PipeOps ingress endpoints without changing your network perimeter. Disconnect the funnel when you no longer need outside access.
