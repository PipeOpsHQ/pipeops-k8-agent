package state

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewStateManager(t *testing.T) {
	sm := NewStateManager()
	assert.NotNil(t, sm)
	assert.Equal(t, "pipeops-agent-state", sm.configMapName)
	// Namespace should have a default value
	assert.NotEmpty(t, sm.namespace)
}

func TestGetNamespace(t *testing.T) {
	// Test default namespace when no env vars or files
	ns := getNamespace()
	assert.NotEmpty(t, ns)
	assert.Equal(t, "pipeops-system", ns)
}

func TestGetStatePath(t *testing.T) {
	sm := NewStateManager()
	path := sm.GetStatePath()
	assert.NotEmpty(t, path)
	// Should indicate either ConfigMap or in-memory
	// The path should contain one of these strings
	hasConfigMap := strings.Contains(path, "ConfigMap:")
	hasInMemory := strings.Contains(path, "in-memory")
	assert.True(t, hasConfigMap || hasInMemory, "Path should indicate ConfigMap or in-memory storage: %s", path)
}

func TestIsUsingConfigMap(t *testing.T) {
	sm := NewStateManager()
	// Just verify the method works (may be true or false depending on K8s availability)
	_ = sm.IsUsingConfigMap()
}

func TestAgentStateStructure(t *testing.T) {
	// Test that AgentState can be created and used
	state := &AgentState{
		AgentID:      "test-agent-123",
		ClusterID:    "550e8400-e29b-41d4-a716-446655440000",
		ClusterToken: "test-token-abc",
	}

	assert.Equal(t, "test-agent-123", state.AgentID)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", state.ClusterID)
	assert.Equal(t, "test-token-abc", state.ClusterToken)
}

func TestStateManagerLoad(t *testing.T) {
	sm := NewStateManager()

	// Load should return empty state if ConfigMap not available or doesn't exist
	state, err := sm.Load()
	assert.NoError(t, err)
	assert.NotNil(t, state)
}

func TestStateManagerSave(t *testing.T) {
	sm := NewStateManager()

	state := &AgentState{
		AgentID:      "test-agent",
		ClusterID:    "test-cluster",
		ClusterToken: "test-token",
	}

	// Save should not error even if ConfigMap not available
	err := sm.Save(state)
	assert.NoError(t, err)
}

func TestStateManagerAgentID(t *testing.T) {
	sm := NewStateManager()

	// GetAgentID should return error if not set
	_, err := sm.GetAgentID()
	assert.Error(t, err)

	// SaveAgentID should work
	err = sm.SaveAgentID("test-agent-456")
	assert.NoError(t, err)
}

func TestStateManagerClusterID(t *testing.T) {
	sm := NewStateManager()

	// GetClusterID should return error if not set
	_, err := sm.GetClusterID()
	assert.Error(t, err)

	// SaveClusterID should work
	err = sm.SaveClusterID("test-cluster-789")
	assert.NoError(t, err)
}

func TestStateManagerClusterToken(t *testing.T) {
	sm := NewStateManager()

	// GetClusterToken should return error if not set
	_, err := sm.GetClusterToken()
	assert.Error(t, err)

	// SaveClusterToken should work
	err = sm.SaveClusterToken("test-token-xyz")
	assert.NoError(t, err)
}

func TestStateManagerClear(t *testing.T) {
	sm := NewStateManager()

	// Clear should not error even if ConfigMap not available
	err := sm.Clear()
	assert.NoError(t, err)
}
