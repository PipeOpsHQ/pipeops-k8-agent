package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_RoundTripAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.yaml")

	sm := NewStateManagerWithFile(path)
	if !sm.useFileStore || sm.useConfigMap {
		t.Fatal("expected file-store mode, not configmap")
	}
	if got := sm.GetStatePath(); got != "file:"+path {
		t.Fatalf("GetStatePath = %q", got)
	}

	// First run: empty.
	if _, err := sm.GetAgentID(); err == nil {
		t.Fatal("expected no agent id on first run")
	}

	// Persist identity + sensitive token.
	if err := sm.SaveAgentID("agent-123"); err != nil {
		t.Fatalf("SaveAgentID: %v", err)
	}
	if err := sm.SaveClusterToken("secret-token"); err != nil {
		t.Fatalf("SaveClusterToken: %v", err)
	}
	if err := sm.SaveClusterID("cluster-xyz"); err != nil {
		t.Fatalf("SaveClusterID: %v", err)
	}

	// Simulate a restart: a fresh manager over the same file must see the state.
	sm2 := NewStateManagerWithFile(path)
	if id, err := sm2.GetAgentID(); err != nil || id != "agent-123" {
		t.Fatalf("GetAgentID after restart = %q, %v", id, err)
	}
	if tok, err := sm2.GetClusterToken(); err != nil || tok != "secret-token" {
		t.Fatalf("GetClusterToken after restart = %q, %v", tok, err)
	}
	if cid, err := sm2.GetClusterID(); err != nil || cid != "cluster-xyz" {
		t.Fatalf("GetClusterID after restart = %q, %v", cid, err)
	}

	// The state file holds the token, so it must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file perm = %o, want 600", perm)
	}

	// Clear removes the file.
	if err := sm2.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected state file removed after Clear, stat err = %v", err)
	}
}

func TestDefaultStateFilePath_EnvOverride(t *testing.T) {
	t.Setenv("PIPEOPS_STATE_FILE", "/custom/state.yaml")
	if got := defaultStateFilePath(); got != "/custom/state.yaml" {
		t.Fatalf("defaultStateFilePath = %q, want /custom/state.yaml", got)
	}
}
