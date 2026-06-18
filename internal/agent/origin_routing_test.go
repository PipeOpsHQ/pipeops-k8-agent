package agent

import (
	"reflect"
	"testing"

	"github.com/pipeops/pipeops-vm-agent/internal/controlplane"
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
