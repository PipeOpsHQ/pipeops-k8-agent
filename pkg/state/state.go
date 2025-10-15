package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentState represents the persistent state of the agent
type AgentState struct {
	AgentID      string `yaml:"agent_id"`
	ClusterID    string `yaml:"cluster_id"`
	ClusterToken string `yaml:"cluster_token"`
}

// StateManager manages persistent agent state
type StateManager struct {
	statePath string
}

// NewStateManager creates a new state manager
func NewStateManager() *StateManager {
	return &StateManager{
		statePath: getStatePath(),
	}
}

// getStatePath returns the path to the state file
// Prioritizes locations based on container environment:
// 1. /tmp/agent-state.yaml (primary - mounted as emptyDir in container)
// 2. tmp/agent-state.yaml (local development)
// 3. /var/lib/pipeops/agent-state.yaml (persistent storage if mounted)
// 4. /var/tmp/agent-state.yaml (alternative temp location)
func getStatePath() string {
	paths := []string{
		"/tmp/agent-state.yaml",             // Primary: emptyDir volume mount in container
		"tmp/agent-state.yaml",              // Local development
		"/var/lib/pipeops/agent-state.yaml", // Persistent volume if mounted
		"/var/tmp/agent-state.yaml",         // Alternative temp location
	}

	for _, path := range paths {
		// Get the directory for this path
		dir := filepath.Dir(path)

		// For relative paths, try to create directory first
		if !filepath.IsAbs(path) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				continue
			}
		}

		// Check if directory exists and is writable
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			// Directory exists, check if writable
			testFile := filepath.Join(dir, ".pipeops-write-test")
			if err := os.WriteFile(testFile, []byte("test"), 0644); err == nil {
				os.Remove(testFile)
				return path
			}
		}
	}

	// Final fallback to /tmp (should always work with emptyDir mount)
	return "/tmp/agent-state.yaml"
}

// Load loads the agent state from disk
func (sm *StateManager) Load() (*AgentState, error) {
	data, err := os.ReadFile(sm.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty state if file doesn't exist
			return &AgentState{}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state AgentState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return &state, nil
}

// Save saves the agent state to disk
func (sm *StateManager) Save(state *AgentState) error {
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Try to write to the configured path first
	if err := sm.tryWriteState(sm.statePath, data); err == nil {
		return nil
	}

	// If that fails, try alternative paths (prioritize /tmp since it's mounted as emptyDir)
	alternativePaths := []string{
		"/tmp/agent-state.yaml",             // Primary: emptyDir volume mount
		"tmp/agent-state.yaml",              // Local development
		"/var/lib/pipeops/agent-state.yaml", // Persistent volume if mounted
		"/var/tmp/agent-state.yaml",         // Alternative temp location
	}

	for _, path := range alternativePaths {
		if path == sm.statePath {
			continue // Already tried this one
		}
		if err := sm.tryWriteState(path, data); err == nil {
			sm.statePath = path // Update to working path
			return nil
		}
	}

	return fmt.Errorf("failed to write state file to any location: all paths are not writable")
}

// tryWriteState attempts to write state to a specific path
func (sm *StateManager) tryWriteState(path string, data []byte) error {
	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write state file with restricted permissions (0600 for security)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}

	return nil
}

// GetAgentID loads the agent ID from state
func (sm *StateManager) GetAgentID() (string, error) {
	state, err := sm.Load()
	if err != nil {
		return "", err
	}
	if state.AgentID == "" {
		return "", fmt.Errorf("no agent ID in state")
	}
	return state.AgentID, nil
}

// SaveAgentID saves the agent ID to state
func (sm *StateManager) SaveAgentID(agentID string) error {
	state, err := sm.Load()
	if err != nil {
		state = &AgentState{}
	}
	state.AgentID = agentID
	return sm.Save(state)
}

// GetClusterID loads the cluster ID from state
func (sm *StateManager) GetClusterID() (string, error) {
	state, err := sm.Load()
	if err != nil {
		return "", err
	}
	if state.ClusterID == "" {
		return "", fmt.Errorf("no cluster ID in state")
	}
	return state.ClusterID, nil
}

// SaveClusterID saves the cluster ID to state
func (sm *StateManager) SaveClusterID(clusterID string) error {
	state, err := sm.Load()
	if err != nil {
		state = &AgentState{}
	}
	state.ClusterID = clusterID
	return sm.Save(state)
}

// GetClusterToken loads the cluster token from state
func (sm *StateManager) GetClusterToken() (string, error) {
	state, err := sm.Load()
	if err != nil {
		return "", err
	}
	if state.ClusterToken == "" {
		return "", fmt.Errorf("no cluster token in state")
	}
	return state.ClusterToken, nil
}

// SaveClusterToken saves the cluster token to state
func (sm *StateManager) SaveClusterToken(token string) error {
	state, err := sm.Load()
	if err != nil {
		state = &AgentState{}
	}
	state.ClusterToken = token
	return sm.Save(state)
}

// GetStatePath returns the current state file path
func (sm *StateManager) GetStatePath() string {
	return sm.statePath
}

// Clear removes the state file
func (sm *StateManager) Clear() error {
	if err := os.Remove(sm.statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}
	return nil
}

// MigrateLegacyState migrates from old separate files to consolidated state
func (sm *StateManager) MigrateLegacyState() error {
	// Legacy file paths
	legacyPaths := map[string][]string{
		"agent_id": {
			"/var/lib/pipeops/agent-id",
			"/etc/pipeops/agent-id",
			".pipeops-agent-id",
		},
		"cluster_id": {
			"/var/lib/pipeops/cluster-id",
			"/etc/pipeops/cluster-id",
			".pipeops-cluster-id",
		},
		"cluster_token": {
			"/var/lib/pipeops/cluster-token",
			"/etc/pipeops/cluster-token",
			".pipeops-cluster-token",
		},
	}

	state := &AgentState{}
	migrated := false

	// Try to load agent ID from legacy files
	for _, path := range legacyPaths["agent_id"] {
		if data, err := os.ReadFile(path); err == nil {
			state.AgentID = strings.TrimSpace(string(data))
			if state.AgentID != "" {
				migrated = true
				break
			}
		}
	}

	// Try to load cluster ID from legacy files
	for _, path := range legacyPaths["cluster_id"] {
		if data, err := os.ReadFile(path); err == nil {
			state.ClusterID = strings.TrimSpace(string(data))
			if state.ClusterID != "" {
				migrated = true
				break
			}
		}
	}

	// Try to load cluster token from legacy files
	for _, path := range legacyPaths["cluster_token"] {
		if data, err := os.ReadFile(path); err == nil {
			state.ClusterToken = strings.TrimSpace(string(data))
			if state.ClusterToken != "" {
				migrated = true
				break
			}
		}
	}

	// Save to new consolidated state file if anything was migrated
	if migrated {
		if err := sm.Save(state); err != nil {
			return fmt.Errorf("failed to save migrated state: %w", err)
		}
	}

	return nil
}
