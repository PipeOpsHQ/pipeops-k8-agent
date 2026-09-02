# Daemon mode — running the PipeOps connector outside Kubernetes (cloudflared-style)

Status: Phase 0–1 implemented; Phase 2 partial (see "Implementation status" below). Goal: let the existing agent run as a **host daemon** (VM, bare metal,
Docker, laptop) that tunnels arbitrary local origins (`localhost:port`, `host:port`, unix socket),
in addition to its current in-cluster mode — without changing the gateway/control-plane side.

## TL;DR
The hard part (outbound multiplexed tunnel, registration, reconnect/resume, request_id muxing,
yamux L4) is **already origin-agnostic**. Kubernetes coupling is concentrated in **three seams**:
route discovery, origin dialing, and identity/state. The agent is already written defensively
around a nil k8s client, L4 routes can already come from JSON flags, and there's a `--kubeconfig` /
`--in-cluster` flag plus an in-memory state fallback. So this is **interface extraction + a run
mode**, not new distributed-systems work. Estimate: a few focused weeks.

## What already works in our favor (verified)
- `internal/agent/agent.go` guards `if a.k8sClient != nil` at every k8s touchpoint
  (e.g. lines ~387, 579, 763, 1043). Much of the agent already tolerates no cluster.
- Ingress sync is already gated: `a.config.Agent.EnableIngressSync && a.k8sClient != nil`.
- L4 routes can already be supplied as config, not just CRDs:
  `--gateway-gwapi-tcp-routes-json`, `--gateway-istio-tcp-routes-json`, etc. (cmd/agent/main.go).
- `cmd/agent/main.go` already has `--kubeconfig`, `--in-cluster`, a `--config` (viper) file.
- `pkg/state` already supports ConfigMap **and** in-memory modes.
- TCP/UDP tunnel handlers already dial `localhost:<port>` (`tcp_tunnel.go:29`, `udp_tunnel.go:66`)
  and there's a generic reverse-tunnel primitive in `internal/tunnel/client.go`
  (`LocalAddr`, `R:8000:localhost:6443`).
- The gateway server (in `pipeops-controller/services/gateway/`) is **connector-agnostic** — it sees
  a websocket + a connector UUID and routes by hostname. **Daemon mode needs zero gateway changes.**

## The three seams (where Kubernetes is actually wired in)

### 1. Route discovery — `RouteProvider`
Today: only `internal/ingress/*` (watches K8s `Ingress` + Gateway-API `Gateway`/`TCPRoute`/`UDPRoute`).
Daemon needs a config-file provider (cloudflared-style ingress rules).

```go
// internal/routes/provider.go
type Route struct {
    Hostname    string // public host the gateway routes by (HTTP/WS)
    Path        string // optional path prefix
    Origin      Origin // where the connector forwards to locally
    TLS         bool
}
type Origin struct {
    Scheme  string // http | https | tcp | udp | unix
    Address string // "localhost:3000" | "/run/app.sock" | "127.0.0.1:5432"
}
type RouteProvider interface {
    // Routes returns the current route table and pushes updates on change.
    Routes(ctx context.Context) (<-chan []Route, error)
}
```
Implementations:
- `K8sIngressProvider` — wraps the existing `internal/ingress` watcher (no behavior change in-cluster).
- `ConfigFileProvider` — reads `ingress:` from the agent config / `--ingress-json`, watches the file.
- (later) `APIProvider` — routes pushed from the control plane.

Both feed the **same** `SyncIngresses` call to the gateway (`internal/ingress/client.go`), so the
gateway route registry is identical regardless of source.

### 2. Origin dialing — `OriginDialer`
Today: two hard-coded cluster-DNS sites:
- HTTP: `internal/agent/agent.go` → `buildServiceFQDN(ServiceName, Namespace)` → `*.svc.cluster.local`
  (used in `proxyToService`, agent.go:2694).
- L4: `internal/tunnel/yamux_client.go:190` → `fmt.Sprintf("%s.%s.svc.cluster.local:%d", ...)`.

```go
// internal/origin/dialer.go
type Target struct {
    Protocol string // tcp | udp
    // In k8s mode: ServiceName/Namespace/Port. In daemon mode: Address.
    ServiceName, Namespace string
    Port                   int
    Address                string // explicit host:port / unix path (daemon)
}
type OriginDialer interface {
    DialContext(ctx context.Context, t Target) (net.Conn, error)
    // ResolveHTTPHost returns scheme+host:port for the HTTP proxy path.
    ResolveHTTPHost(t Target) (scheme, hostport string, err error)
}
```
Implementations:
- `ClusterDialer` — current behavior: `buildServiceFQDN` + `svc.cluster.local`. Keeps SSRF
  validation (`validateServiceName/Namespace/Port`).
- `HostDialer` — daemon: dials `Target.Address` (host:port or `unix://`), still validated/allowlisted.

Refactor: replace the two hard-coded dial sites with `a.originDialer.DialContext(...)` /
`ResolveHTTPHost(...)`. The route table (seam 1) supplies `Target.Address` in daemon mode.

### 3. Identity & state — `Identity` + `StateStore`
Today: `rest.InClusterConfig()` (`pkg/k8s/client.go:42`, `controlplane/websocket_proxy.go:186`) +
ConfigMap-backed `pkg/state`.

```go
type StateStore interface { // already largely exists in pkg/state
    GetClusterID() (string, error); SaveClusterID(string) error
    GetGatewayWSURL() (string, error); SaveGatewayWSURL(string) error
    Clear() error
}
```
- `ConfigMapStore` — current.
- `FileStore` — daemon: `~/.pipeops-agent/state.json` (creds + clusterID + cached gateway URL),
  mirroring cloudflared's `credentials.json` + tunnel token. The in-memory fallback already exists.
- Auth token: daemon reads it from `--token` / config / a credentials file (already supported).
  The gateway already accepts the connector token by `Authorization: Bearer` — unchanged.

### Plus: skip the k8s-only modules in daemon mode
- Component bootstrap (Helm install Traefik/cert-manager/Prometheus/Loki) — already gated by
  `PIPEOPS_AUTO_INSTALL_COMPONENTS`; daemon mode forces it off.
- K8s-API proxying (`kubectl exec`/API) and cluster metrics — only registered when `k8sClient != nil`.
- `monitoringMgr`, ingress watcher — only started in k8s mode.

## Run mode wiring
Add `--mode=k8s|daemon` (default `k8s`; infer `daemon` when no kubeconfig and not in-cluster).
In `cmd/agent/main.go` / agent construction, pick implementations:

| Component       | k8s mode                | daemon mode             |
|-----------------|-------------------------|-------------------------|
| RouteProvider   | K8sIngressProvider      | ConfigFileProvider      |
| OriginDialer    | ClusterDialer           | HostDialer              |
| StateStore      | ConfigMapStore          | FileStore               |
| k8sClient       | NewInClusterClient      | nil (guards already exist) |
| Components/metrics | on (gated)           | off                     |

Everything else — `internal/controlplane/*` (registration, WS protocol, heartbeat, reconnect/resume,
request_id mux, proxy_request/response framing) and `internal/tunnel/*` (yamux, wsconn, stream
headers) — is **reused unchanged**.

## Config schema (daemon)
```yaml
# ~/.pipeops-agent.yaml
pipeops:
  api_url: https://api.pipeops.io
  token: <connector-token>
agent:
  id: my-vm-1
  mode: daemon
ingress:
  - hostname: app.example.com
    origin: { scheme: http, address: localhost:3000 }
  - hostname: db.example.com           # L4
    origin: { scheme: tcp,  address: localhost:5432 }
  - hostname: api.example.com
    path: /v1
    origin: { scheme: http, address: 127.0.0.1:8080 }
```

## Phased plan
- **Phase 0 (spike, ~days):** swap the two dial sites behind `OriginDialer`; hard-code a `HostDialer`
  to one `localhost:port`; prove an HTTP request flows gateway → daemon (no k8s) → local service.
  This de-risks the whole effort by validating the gateway is truly connector-agnostic.
- **Phase 1 (~1–1.5 wk):** `RouteProvider` + `ConfigFileProvider` (config-driven HTTP/WS + L4 routes),
  feeding the existing `SyncIngresses`. Unify the `OriginDialer` across HTTP and yamux paths.
- **Phase 2 (~1 wk):** `StateStore`/`FileStore` + local credentials; `--mode=daemon` that nils the
  k8s client and disables component/metrics modules; clean startup with zero k8s API calls.
- **Phase 3 (~few days):** build/packaging — a slim daemon binary (optional build tag to drop the
  k8s client-go deps for size), `pipeops tunnel run`-style UX, systemd unit, Docker image.

## Implementation status

- **Phase 0 — done.** `internal/origin` (`Dialer`, `ClusterDialer`, `HostDialer`); HTTP proxy path
  resolves origins via the dialer.
- **Phase 1 — done.** `OriginDialer` threaded to the L4 yamux path
  (`agent → controlplane.Client → WebSocketClient → YamuxConfig`); per-host routing from the request
  `Host`; multi-route `HostDialer`.
- **Phase 2 — done.** Config-driven routes: a `daemon:` config section
  (`enabled`/`default_origin`/`ingress`/`allowed_origins`), a `--daemon` flag, `PIPEOPS_DAEMON_*`
  env, an SSRF allowlist on `HostDialer`, and **config-file hot reload** (`viper.WatchConfig` →
  `Agent.ReloadDaemonConfig` → `HostDialer.Update`, a concurrency-safe atomic swap of the route
  table — no restart to add/remove ingress rules). See `examples/daemon-config.yaml`.
- **Phase 3 — remaining.** Local `FileStore` credentials/state (so a daemon needs no kubeconfig at
  all), a slim build that drops client-go via a build tag, and packaging (systemd/Docker,
  `pipeops tunnel run` UX).

### Configuration (shipped)

```yaml
daemon:
  enabled: true
  default_origin: "localhost:3000"
  ingress:
    - hostname: "app.example.com"
      origin: "localhost:8080"
  allowed_origins: ["localhost"]   # SSRF guard; empty = allow any configured origin
```

Equivalent quick-start via env: `PIPEOPS_DAEMON_ENABLED=true`, `PIPEOPS_DAEMON_ORIGIN=localhost:3000`,
`PIPEOPS_DAEMON_ROUTES="app.example.com=localhost:8080"` (or the `--daemon` / `--daemon-default-origin`
flags). `PIPEOPS_ORIGIN_MODE=host` remains accepted as a back-compat alias for enabling daemon mode.

## Risks / watch-items
- **SSRF/allowlisting:** daemon dials arbitrary local addresses — keep the existing validation and add
  an origin allowlist from config (don't let the gateway pick arbitrary hosts).
- **Transport:** still WS+yamux (TCP-over-TCP). Fine on clean links; the QUIC upgrade is a separate,
  larger effort (orthogonal to k8s decoupling) and the real perf moat.
- **Feature parity:** `kubectl exec`/API proxying is inherently k8s; daemon mode simply omits it.
- The decoupling is the easy half. The expensive half — a global anycast edge + QUIC — is unchanged
  by this work.
