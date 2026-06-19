# Slim (client-go-free) daemon build — scoping

Status: **proposal / not yet implemented.** This is the one remaining daemon-mode
follow-up. It is a **binary-size optimization with no functional impact** — daemon
mode already works with the normal binary (`--daemon`). Do not let this block the
daemon work; it is a separate, sizeable effort.

## Why
Daemon mode never talks to a Kubernetes API, yet the binary still links the entire
`client-go` / `apimachinery` / Helm / Gateway-API tree.

Measured (this repo, linux/amd64):

| Build | Size |
|---|---|
| `go build` (no flags) | **95 MB** |
| `go build -ldflags="-w -s" -trimpath` (what the image uses) | **64 MB** |
| k8s/helm/apimachinery packages in the build graph | **487 packages** |

A networking-only daemon would typically land in the **15–25 MB** range. The win is
real; the cost is a cross-cutting refactor.

## The coupling surface (first-party packages)
8 of our 15 packages pull in k8s/helm. What each needs in daemon mode:

| Package | k8s/helm import | Used in daemon mode? | Action |
|---|---|---|---|
| `internal/helm` | helm.sh/* | No | exclude under `nok8s` |
| `internal/components` | helm + client-go | No (no component install) | exclude under `nok8s` |
| `internal/ingress` | client-go (Ingress watcher) | No (routes come from config) | exclude under `nok8s` |
| `pkg/cloud` | client-go | No (cloud detection) | exclude under `nok8s` |
| `pkg/k8s` | client-go/kubernetes, rest | No (`k8sClient` is nil) | stub under `nok8s` |
| `pkg/state` | client-go (ConfigMap/Secret) | No (uses `FileStore`) | split: file path is k8s-free |
| `internal/tunnel` | client-go + **gateway-api** (GatewayWatcher) | No (L4 routes from `--gateway-gwapi-*-json`) | split: yamux core is k8s-free, GatewayWatcher is not |
| `internal/controlplane` | only `client-go/rest` (InClusterConfig) | Minimal | gate the one InClusterConfig call |

Plus `internal/agent` imports `gateway-api/.../versioned` directly (the
`gatewayAPIWatcher` field) and holds k8s-typed fields (`k8sClient *k8s.Client`,
`monitoringMgr *components.Manager`, `gatewayWatcher *ingress.IngressWatcher`) — so
the **struct definition itself** drags client-go in regardless of runtime mode.
This is the crux: the fields must become interfaces (or be tag-gated) before the
build can drop client-go.

## Mechanism
Go build tags (`//go:build nok8s` vs `//go:build !nok8s`), one pair per coupling site:

1. **Interface-ize the k8s-typed Agent fields.** Define small interfaces in a leaf
   package (e.g. `K8sClient`, `ComponentManager`, `IngressWatcher`) and have the
   Agent hold those. The concrete client-go implementations live in
   `*_k8s.go` (`//go:build !nok8s`); `nok8s` stubs return nil / no-op.
2. **Split the mixed packages** (`pkg/state`, `internal/tunnel`) so the k8s file(s)
   are `!nok8s` and the daemon path (`FileStore`, yamux core) compiles alone.
3. **Stub the leaf k8s constructors** (`pkg/k8s`, `pkg/cloud`) under `nok8s`.
4. **Gate** `controlplane`'s single `rest.InClusterConfig()` call.
5. A `cmd/agent` build with `-tags nok8s` then imports none of the 487 packages.

Verify with: `go list -tags nok8s -deps ./cmd/agent | grep -cE '^k8s.io/|^helm.sh/|^sigs.k8s.io/'` → must be **0**, and `go build -tags nok8s` must produce a working `--daemon` binary.

## Phased plan
- **P1 — leaves (low risk):** `nok8s` stubs for `pkg/k8s`, `pkg/cloud`, `internal/helm`,
  `internal/components`, `internal/ingress`. None are used in daemon mode.
- **P2 — split mixed packages:** carve `pkg/state` (file vs ConfigMap) and
  `internal/tunnel` (yamux vs GatewayWatcher) along the tag.
- **P3 — Agent fields → interfaces:** the bulk of the work; the struct stops naming
  client-go types. Gate `controlplane`'s InClusterConfig.
- **P4 — build + CI:** `make build-slim` (`-tags nok8s`), a `Dockerfile.daemon`
  variant, and a CI assertion that the slim graph contains **0** k8s packages.

## Risks / notes
- **Maintenance:** every k8s touchpoint becomes two code paths. Keep the interface
  surface minimal so the `nok8s` stubs are trivial.
- **No behaviour change:** the default (non-`nok8s`) build is byte-for-byte the same;
  this only adds an opt-in slimmer artifact.
- **Estimate:** ~1–2 weeks, dominated by P3 (interface extraction across the Agent).
- This is **orthogonal** to the QUIC/edge transport work and to all shipped daemon
  functionality.
