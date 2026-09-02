package origin

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClusterDialer_HTTPHostPort(t *testing.T) {
	d := NewClusterDialer("") // default cluster.local
	got, err := d.HTTPHostPort(Target{ServiceName: "api", Namespace: "default", Port: 8080})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "api.default.svc.cluster.local:8080"; got != want {
		t.Fatalf("HTTPHostPort = %q, want %q", got, want)
	}

	d2 := NewClusterDialer("k8s.internal")
	got2, _ := d2.HTTPHostPort(Target{ServiceName: "web", Namespace: "prod", Port: 80})
	if want := "web.prod.svc.k8s.internal:80"; got2 != want {
		t.Fatalf("custom domain = %q, want %q", got2, want)
	}

	if _, err := d.HTTPHostPort(Target{ServiceName: "", Namespace: "default", Port: 80}); err == nil {
		t.Fatal("expected error for missing service name")
	}
}

func TestHostDialer_HTTPHostPort(t *testing.T) {
	d := NewHostDialer("localhost:3000", map[string]string{"db.example.com": "127.0.0.1:5432"})

	// Falls back to default when hostname isn't in the route table.
	if got, _ := d.HTTPHostPort(Target{Hostname: "app.example.com"}); got != "localhost:3000" {
		t.Fatalf("default = %q, want localhost:3000", got)
	}
	// Uses the per-host route when present.
	if got, _ := d.HTTPHostPort(Target{Hostname: "db.example.com"}); got != "127.0.0.1:5432" {
		t.Fatalf("routed = %q, want 127.0.0.1:5432", got)
	}
	// No default and no match -> error.
	empty := NewHostDialer("", nil)
	if _, err := empty.HTTPHostPort(Target{Hostname: "x"}); err == nil {
		t.Fatal("expected error when no origin configured")
	}
}

// TestHostDialer_EndToEnd proves the daemon path works with NO Kubernetes: a
// real local HTTP server is reached purely via the HostDialer-resolved address.
func TestHostDialer_EndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("from local origin"))
	}))
	defer srv.Close()

	// srv.URL is like http://127.0.0.1:PORT — strip the scheme for the dialer.
	addr := srv.Listener.Addr().String()
	d := NewHostDialer(addr, nil)

	hostport, err := d.HTTPHostPort(Target{Hostname: "anything"})
	if err != nil {
		t.Fatalf("HTTPHostPort: %v", err)
	}

	// Dial it the way the proxy path would.
	conn, err := d.DialContext(context.Background(), Target{Hostname: "anything"}, 2*time.Second)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()

	resp, err := http.Get("http://" + hostport + "/")
	if err != nil {
		t.Fatalf("GET via host dialer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 (proves request reached the local origin)", resp.StatusCode)
	}
}

func TestHostDialer_Allowlist(t *testing.T) {
	d := NewHostDialer("localhost:3000", map[string]string{
		"ok.example.com":  "127.0.0.1:8080",
		"bad.example.com": "10.0.0.5:9000",
	})
	d.AllowedOrigins = []string{"127.0.0.1:8080", "localhost"} // exact addr + bare host

	// Allowed: exact host:port match.
	if _, err := d.HTTPHostPort(Target{Hostname: "ok.example.com"}); err != nil {
		t.Fatalf("expected ok.example.com allowed, got %v", err)
	}
	// Allowed: bare-host entry matches default localhost:3000.
	if _, err := d.HTTPHostPort(Target{Hostname: "unknown"}); err != nil {
		t.Fatalf("expected default localhost allowed, got %v", err)
	}
	// Blocked: not in allowlist.
	if _, err := d.HTTPHostPort(Target{Hostname: "bad.example.com"}); err == nil {
		t.Fatal("expected bad.example.com to be blocked by allowlist")
	}

	// Empty allowlist allows everything.
	open := NewHostDialer("localhost:3000", map[string]string{"x": "10.0.0.5:9000"})
	if _, err := open.HTTPHostPort(Target{Hostname: "x"}); err != nil {
		t.Fatalf("empty allowlist should permit any address, got %v", err)
	}
}

var _ Dialer = (*ClusterDialer)(nil)
var _ Dialer = (*HostDialer)(nil)
var _ net.Conn = nil
