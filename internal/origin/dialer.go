// Package origin abstracts "where the connector forwards a tunneled request to"
// so the same agent can run either in-cluster (dialing Kubernetes Services via
// cluster DNS) or as a host daemon (dialing localhost / host:port / unix
// sockets), without the proxy/tunnel hot paths knowing which mode they're in.
//
// It is intentionally dependency-free (no agent/tunnel/controlplane imports) so
// every layer can import it without creating an import cycle.
package origin

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Target identifies the origin a tunneled request should reach.
//
//   - In Kubernetes mode the gateway sends ServiceName/Namespace/Port (and the
//     ClusterDialer resolves them to <svc>.<ns>.svc.<clusterDomain>).
//   - In daemon mode the HostDialer maps the request to a configured local
//     address (host:port or unix socket), keyed by Hostname (and, later, by a
//     full route table).
type Target struct {
	Protocol    string // "tcp" | "udp" — for L4 dials; empty for HTTP
	ServiceName string
	Namespace   string
	Port        int
	Hostname    string // public host the request arrived for (daemon route key)
}

// HTTPOrigin describes how the HTTP proxy path should reach an origin: the host
// to place in the request URL, plus the network/address to actually dial. For
// TCP origins URLHost == Address; for unix-socket origins URLHost is a cosmetic
// placeholder and the HTTP client must dial Network="unix" at Address (the
// socket path) via a custom transport.
type HTTPOrigin struct {
	URLHost string // host[:port] for the request URL
	Network string // "tcp" | "unix"
	Address string // dial target: host:port, or a unix socket path
	Scheme  string // "http"|"https" when the route pinned one; "" = use request scheme
}

// Dialer resolves and connects to a Target.
type Dialer interface {
	// HTTPHostPort returns the host[:port] to place in the request URL. Kept for
	// callers that only need the URL host; see HTTPOrigin for the dial details.
	HTTPHostPort(t Target) (string, error)
	// HTTPOrigin returns the URL host plus the network/address to dial (so the
	// HTTP path can support unix-socket origins, not just host:port).
	HTTPOrigin(t Target) (HTTPOrigin, error)
	// DialContext opens an L4 (tcp/udp) connection to the Target.
	DialContext(ctx context.Context, t Target, timeout time.Duration) (net.Conn, error)
}

// httpURLPlaceholderHost is the cosmetic URL host used for unix-socket origins;
// the real Host header is preserved separately and the dial goes to the socket.
const httpURLPlaceholderHost = "unix-origin.invalid"

// ClusterDialer is the in-cluster behaviour: resolve Kubernetes Services through
// cluster DNS (<service>.<namespace>.svc.<clusterDomain>). It preserves the
// exact addressing the agent used before the origin abstraction existed.
type ClusterDialer struct {
	// ClusterDomain is the cluster DNS suffix, e.g. "cluster.local".
	ClusterDomain string
}

// NewClusterDialer returns a ClusterDialer; clusterDomain defaults to
// "cluster.local" when empty.
func NewClusterDialer(clusterDomain string) *ClusterDialer {
	if clusterDomain == "" {
		clusterDomain = "cluster.local"
	}
	return &ClusterDialer{ClusterDomain: clusterDomain}
}

func (d *ClusterDialer) fqdn(t Target) (string, error) {
	if t.ServiceName == "" || t.Namespace == "" {
		return "", fmt.Errorf("origin: service name and namespace are required in cluster mode")
	}
	return fmt.Sprintf("%s.%s.svc.%s", t.ServiceName, t.Namespace, d.ClusterDomain), nil
}

func (d *ClusterDialer) HTTPHostPort(t Target) (string, error) {
	host, err := d.fqdn(t)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d", host, t.Port), nil
}

func (d *ClusterDialer) HTTPOrigin(t Target) (HTTPOrigin, error) {
	hp, err := d.HTTPHostPort(t)
	if err != nil {
		return HTTPOrigin{}, err
	}
	return HTTPOrigin{URLHost: hp, Network: "tcp", Address: hp}, nil
}

func (d *ClusterDialer) DialContext(ctx context.Context, t Target, timeout time.Duration) (net.Conn, error) {
	host, err := d.fqdn(t)
	if err != nil {
		return nil, err
	}
	proto := t.Protocol
	if proto == "" {
		proto = "tcp"
	}
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, proto, fmt.Sprintf("%s:%d", host, t.Port))
}

// HostDialer is the daemon behaviour: forward to local origins, resolving each
// Target to a configured local address (per-host route table + a default).
//
// It is safe for concurrent use: serve goroutines read the route table while a
// config reload swaps it via Update. Reads snapshot under an RLock; Update
// replaces (never mutates) the table/allowlist under a write lock.
type HostDialer struct {
	mu sync.RWMutex
	// DefaultAddress is the fallback origin, e.g. "localhost:3000" or
	// "unix:///run/app.sock".
	DefaultAddress string
	// Routes optionally maps a public hostname to a specific local address.
	Routes map[string]string
	// AllowedOrigins, when non-empty, restricts which resolved addresses may be
	// dialed (SSRF guard). Entries are "host:port", bare "host" (any port), or
	// "unix:///path". Empty = allow any configured address (trusted config).
	AllowedOrigins []string
}

// NewHostDialer returns a HostDialer with a default origin address.
func NewHostDialer(defaultAddress string, routes map[string]string) *HostDialer {
	return &HostDialer{DefaultAddress: defaultAddress, Routes: routes}
}

// Update atomically replaces the default origin, route table, and allowlist.
// Used by config hot-reload. The provided maps/slices are taken as-is (the
// caller must not mutate them afterwards).
func (d *HostDialer) Update(defaultAddress string, routes map[string]string, allowed []string) {
	d.mu.Lock()
	d.DefaultAddress = defaultAddress
	d.Routes = routes
	d.AllowedOrigins = allowed
	d.mu.Unlock()
}

// routeKeys returns the candidate route-table keys for a target, in priority
// order. HTTP requests carry a Hostname; L4 (tcp/udp) tunnels carry only
// service/namespace, so they match by service name — otherwise every L4 service
// would collapse onto DefaultAddress.
func routeKeys(t Target) []string {
	keys := make([]string, 0, 3)
	if t.Hostname != "" {
		keys = append(keys, t.Hostname)
	}
	if t.ServiceName != "" {
		keys = append(keys, t.ServiceName)
		if t.Namespace != "" {
			keys = append(keys, t.ServiceName+"."+t.Namespace)
		}
	}
	return keys
}

// resolve maps a Target to a dial network+address, plus an optional HTTP scheme
// when the configured origin carried one (e.g. "https://localhost:8443").
func (d *HostDialer) resolve(t Target) (network, address, scheme string, err error) {
	d.mu.RLock()
	addr := d.DefaultAddress
	if d.Routes != nil {
		for _, k := range routeKeys(t) {
			if a, ok := d.Routes[k]; ok {
				addr = a
				break
			}
		}
	}
	allowlist := d.AllowedOrigins // slice header copy; Update replaces, never mutates
	d.mu.RUnlock()

	if addr == "" {
		return "", "", "", fmt.Errorf("origin: no local address configured for target %q", routeLabel(t))
	}
	if sock, ok := strings.CutPrefix(addr, "unix://"); ok {
		network, address = "unix", sock
	} else {
		// Optional scheme prefix lets a route pin a local origin as http/https
		// (otherwise the request's own scheme is used).
		switch {
		case strings.HasPrefix(addr, "https://"):
			scheme, addr = "https", strings.TrimPrefix(addr, "https://")
		case strings.HasPrefix(addr, "http://"):
			scheme, addr = "http", strings.TrimPrefix(addr, "http://")
		}
		// Reject scheme-only ("https://") or path-bearing ("host:port/x") origins
		// here with a clear message, instead of failing opaquely at dial time.
		if addr == "" || strings.Contains(addr, "/") {
			return "", "", "", fmt.Errorf("origin: malformed origin for target %q: want host:port, http(s)://host:port, or unix:///path", routeLabel(t))
		}
		network = t.Protocol
		if network == "" {
			network = "tcp"
		}
		address = addr
	}
	if !addrAllowed(allowlist, network, address) {
		return "", "", "", fmt.Errorf("origin: address %q not permitted by daemon allowed_origins (target %q)", address, routeLabel(t))
	}
	return network, address, scheme, nil
}

// routeLabel is a human-readable identifier for a target, for error messages.
func routeLabel(t Target) string {
	if t.Hostname != "" {
		return t.Hostname
	}
	if t.ServiceName != "" {
		if t.Namespace != "" {
			return t.ServiceName + "." + t.Namespace
		}
		return t.ServiceName
	}
	return "(default)"
}

// addrAllowed reports whether the resolved address passes the SSRF allowlist. An
// empty allowlist permits everything (the operator is trusted to configure
// origins); a non-empty allowlist requires an exact "host:port"/"unix:///path"
// match, or a bare "host" entry matching the address's host (any port).
func addrAllowed(allowlist []string, network, address string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, a := range allowlist {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if network == "unix" {
			if a == address || strings.TrimPrefix(a, "unix://") == address {
				return true
			}
			continue
		}
		if a == address {
			return true
		}
		if host, _, err := net.SplitHostPort(address); err == nil && host == a {
			return true
		}
	}
	return false
}

func (d *HostDialer) HTTPHostPort(t Target) (string, error) {
	o, err := d.HTTPOrigin(t)
	if err != nil {
		return "", err
	}
	return o.URLHost, nil
}

func (d *HostDialer) HTTPOrigin(t Target) (HTTPOrigin, error) {
	network, addr, scheme, err := d.resolve(t)
	if err != nil {
		return HTTPOrigin{}, err
	}
	if network == "unix" {
		// The HTTP client dials the socket via a custom transport; the URL host
		// is a placeholder (the real Host header is preserved by the proxy path).
		return HTTPOrigin{URLHost: httpURLPlaceholderHost, Network: "unix", Address: addr, Scheme: scheme}, nil
	}
	return HTTPOrigin{URLHost: addr, Network: "tcp", Address: addr, Scheme: scheme}, nil
}

func (d *HostDialer) DialContext(ctx context.Context, t Target, timeout time.Duration) (net.Conn, error) {
	network, addr, _, err := d.resolve(t)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, network, addr)
}
