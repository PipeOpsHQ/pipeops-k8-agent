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

// Dialer resolves and connects to a Target.
type Dialer interface {
	// HTTPHostPort returns the "host:port" the HTTP proxy path should dial.
	HTTPHostPort(t Target) (string, error)
	// DialContext opens an L4 (tcp/udp) connection to the Target.
	DialContext(ctx context.Context, t Target, timeout time.Duration) (net.Conn, error)
}

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

// HostDialer is the daemon behaviour: forward to local origins. For the Phase-0
// spike it resolves every Target to a single configured address; a later phase
// swaps in a full route table (hostname/service -> origin address).
type HostDialer struct {
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

func (d *HostDialer) resolve(t Target) (network, address string, err error) {
	addr := d.DefaultAddress
	if d.Routes != nil && t.Hostname != "" {
		if a, ok := d.Routes[t.Hostname]; ok {
			addr = a
		}
	}
	if addr == "" {
		return "", "", fmt.Errorf("origin: no local address configured for host %q", t.Hostname)
	}
	if sock, ok := strings.CutPrefix(addr, "unix://"); ok {
		network, address = "unix", sock
	} else {
		network = t.Protocol
		if network == "" {
			network = "tcp"
		}
		address = addr
	}
	if !d.allowed(network, address) {
		return "", "", fmt.Errorf("origin: address %q not permitted by daemon allowed_origins (host %q)", address, t.Hostname)
	}
	return network, address, nil
}

// allowed reports whether the resolved address passes the SSRF allowlist. An
// empty allowlist permits everything (the operator is trusted to configure
// origins); a non-empty allowlist requires an exact "host:port"/"unix:///path"
// match, or a bare "host" entry matching the address's host (any port).
func (d *HostDialer) allowed(network, address string) bool {
	if len(d.AllowedOrigins) == 0 {
		return true
	}
	for _, a := range d.AllowedOrigins {
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
	_, addr, err := d.resolve(t)
	if err != nil {
		return "", err
	}
	return addr, nil
}

func (d *HostDialer) DialContext(ctx context.Context, t Target, timeout time.Duration) (net.Conn, error) {
	network, addr, err := d.resolve(t)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: timeout}
	return dialer.DialContext(ctx, network, addr)
}
