# Running the PipeOps connector as a host daemon

Daemon mode runs the **same connector binary** outside Kubernetes (VM, bare metal,
Docker, laptop). Instead of resolving Kubernetes Services, it forwards tunneled
traffic — HTTP and TCP/UDP — to **local origins** (`localhost:port`, `host:port`,
unix sockets), cloudflared-style. It dials only outbound, so no inbound ports.

What you need: a **connector token** and a **config file**. The daemon registers
once and persists its identity to a local file (`PIPEOPS_STATE_FILE`, default
`$HOME/.pipeops-agent/state.yaml`, mode 0600), so it survives restarts with no
Kubernetes.

## 1. Configure

Start from [`examples/daemon-config.yaml`](../../examples/daemon-config.yaml). Minimal:

```yaml
pipeops:
  api_url: "wss://gateway.pipeops.io"
  token: "${PIPEOPS_TOKEN}"
daemon:
  enabled: true
  default_origin: "localhost:3000"
  ingress:
    - hostname: "app.example.com"
      origin: "localhost:8080"
  allowed_origins: ["localhost"]   # SSRF guard; empty = allow any configured origin
```

Edit `ingress:` while running and routes reload live (no restart).

## 2. Run

### Binary
```sh
pipeops-agent --daemon --config ./daemon.yaml
# or fully via env, no config file:
PIPEOPS_DAEMON_ENABLED=true PIPEOPS_TOKEN=... \
  PIPEOPS_DAEMON_ROUTES="app.example.com=localhost:8080" pipeops-agent --daemon
```

### systemd
```sh
sudo install -D -m0755 pipeops-agent /usr/local/bin/pipeops-agent
sudo install -D -m0644 deployments/daemon/pipeops-agent.service /etc/systemd/system/pipeops-agent.service
sudo install -D -m0644 examples/daemon-config.yaml /etc/pipeops/daemon.yaml
sudo install -D -m0600 deployments/daemon/daemon.env.example /etc/pipeops/daemon.env
sudo $EDITOR /etc/pipeops/daemon.env   # set PIPEOPS_TOKEN
sudo systemctl enable --now pipeops-agent
journalctl -u pipeops-agent -f
```
The unit runs as a `DynamicUser` with a hardened sandbox; systemd provisions
`/var/lib/pipeops-agent` for the state file.

### Docker
```sh
docker build -f Dockerfile.daemon -t pipeops-agent:daemon .
docker run -d --name pipeops-daemon \
  -e PIPEOPS_TOKEN="$PIPEOPS_TOKEN" \
  -v "$PWD/daemon.yaml:/etc/pipeops/daemon.yaml:ro" \
  -v pipeops-daemon-state:/var/lib/pipeops-agent \
  --network host \
  pipeops-agent:daemon --daemon --config /etc/pipeops/daemon.yaml
```
`--network host` lets the daemon reach `localhost` origins; or drop it and point
origins at reachable container/host addresses. The named volume persists identity.

## Notes / limits
- **SSRF:** `allowed_origins` restricts what the daemon may dial. Leave empty only
  when you fully trust the config source.
- **Transport:** tunnels are WS+yamux (TCP-over-TCP) — fine on clean links; a QUIC
  transport is a separate, larger effort.
- **Not available in daemon mode:** Kubernetes-only features (component install,
  `kubectl exec`/API proxying, cluster metrics) are simply skipped.
- **Binary size:** the daemon links `client-go` even though it doesn't use it in
  this mode. A build-tag that drops the Kubernetes deps for a slimmer binary is a
  tracked follow-up (it needs interface extraction across the agent); it does not
  affect functionality.
