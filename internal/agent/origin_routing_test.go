package agent

import (
	"reflect"
	"testing"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
)

func TestParseDaemonRoutes(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"", nil},
		{"   ", nil},
		{"app.example.com=localhost:3000", map[string]string{"app.example.com": "localhost:3000"}},
		{
			"app.example.com=localhost:3000, db.example.com=127.0.0.1:5432",
			map[string]string{"app.example.com": "localhost:3000", "db.example.com": "127.0.0.1:5432"},
		},
		{"bad,=x,y=,ok=localhost:8080", map[string]string{"ok": "localhost:8080"}},
	}
	for _, c := range cases {
		if got := parseDaemonRoutes(c.in); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("parseDaemonRoutes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDaemonOriginEnabled(t *testing.T) {
	t.Setenv("PIPEOPS_ORIGIN_MODE", "")
	if daemonOriginEnabled(nil) {
		t.Fatal("nil config + no env should be cluster mode")
	}
	if daemonOriginEnabled(&types.Config{}) {
		t.Fatal("empty config should be cluster mode")
	}
	if !daemonOriginEnabled(&types.Config{Daemon: &types.DaemonConfig{Enabled: true}}) {
		t.Fatal("daemon.enabled should select daemon mode")
	}
	t.Setenv("PIPEOPS_ORIGIN_MODE", "host")
	if !daemonOriginEnabled(&types.Config{}) {
		t.Fatal("PIPEOPS_ORIGIN_MODE=host should select daemon mode")
	}
}

func TestBuildHostDialer_FromConfig(t *testing.T) {
	t.Setenv("PIPEOPS_DAEMON_ORIGIN", "")
	t.Setenv("PIPEOPS_DAEMON_ROUTES", "")
	cfg := &types.Config{Daemon: &types.DaemonConfig{
		DefaultOrigin: "localhost:3000",
		Ingress: []types.DaemonIngressRule{
			{Hostname: "app.example.com", Origin: "localhost:8080"},
			{Hostname: "", Origin: "skip"},  // skipped: no hostname
			{Hostname: "skip2", Origin: ""}, // skipped: no origin
		},
		AllowedOrigins: []string{"localhost:8080"},
	}}
	hd := buildHostDialer(cfg)
	if hd.DefaultAddress != "localhost:3000" {
		t.Fatalf("default = %q, want localhost:3000", hd.DefaultAddress)
	}
	if got := hd.Routes["app.example.com"]; got != "localhost:8080" {
		t.Fatalf("route = %q, want localhost:8080", got)
	}
	if len(hd.Routes) != 1 {
		t.Fatalf("routes = %d, want 1 (invalid rules skipped)", len(hd.Routes))
	}
	if len(hd.AllowedOrigins) != 1 {
		t.Fatalf("allowlist = %d, want 1", len(hd.AllowedOrigins))
	}
}

func TestRequestHost(t *testing.T) {
	if got := requestHost(nil); got != "" {
		t.Fatalf("nil req = %q, want empty", got)
	}
	req := &controlplane.ProxyRequest{Headers: map[string][]string{"Host": {"app.example.com"}}}
	if got := requestHost(req); got != "app.example.com" {
		t.Fatalf("Host header = %q, want app.example.com", got)
	}
	req2 := &controlplane.ProxyRequest{Headers: map[string][]string{"X-Forwarded-Host": {"fwd.example.com"}}}
	if got := requestHost(req2); got != "fwd.example.com" {
		t.Fatalf("X-Forwarded-Host = %q, want fwd.example.com", got)
	}
	if got := requestHost(&controlplane.ProxyRequest{}); got != "" {
		t.Fatalf("no headers = %q, want empty", got)
	}
}
