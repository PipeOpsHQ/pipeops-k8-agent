package state

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var logger = logrus.WithField("component", "StateManager")

// AgentState represents the persistent state of the agent
type AgentState struct {
	AgentID      string `yaml:"agent_id"`
	ClusterID    string `yaml:"cluster_id"`
	ClusterToken string `yaml:"cluster_token"`
}

// StateManager manages persistent agent state using Kubernetes ConfigMap
type StateManager struct {
	k8sClient     *kubernetes.Clientset
	namespace     string
	configMapName string
	useConfigMap  bool
}

// NewStateManager creates a new state manager
// Attempts to use ConfigMap for persistence, falls back to in-memory only if unavailable
func NewStateManager() *StateManager {
	sm := &StateManager{
		namespace:     getNamespace(),
		configMapName: "pipeops-agent-state",
		useConfigMap:  false,
	}

	// Try to create Kubernetes client for ConfigMap-based state
	if config, err := rest.InClusterConfig(); err == nil {
		if client, err := kubernetes.NewForConfig(config); err == nil {
			sm.k8sClient = client
			sm.useConfigMap = true
			logger.WithFields(logrus.Fields{
				"namespace":     sm.namespace,
				"configmap":     sm.configMapName,
				"use_configmap": true,
			}).Info("Using ConfigMap for persistence")
		} else {
			logger.WithError(err).Warn("Failed to create K8s client - using in-memory state only")
		}
	} else {
		logger.WithError(err).Warn("Not running in cluster - using in-memory state only")
	}

	return sm
}

// getNamespace returns the namespace the agent is running in
func getNamespace() string {
	// Try to read from service account mount
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(data))
	}
	// Fallback to environment variable
	if ns := os.Getenv("PIPEOPS_POD_NAMESPACE"); ns != "" {
		return ns
	}
	// Default namespace
	return "pipeops-system"
}

// Load loads the agent state from ConfigMap or returns empty state
func (sm *StateManager) Load() (*AgentState, error) {
	if !sm.useConfigMap {
		logger.Debug("ConfigMap not available, returning empty state")
		// Return empty state if ConfigMap not available
		return &AgentState{}, nil
	}

	ctx := context.Background()
	logger.WithFields(logrus.Fields{
		"namespace": sm.namespace,
		"configmap": sm.configMapName,
	}).Debug("Attempting to read ConfigMap")

	cm, err := sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Get(ctx, sm.configMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Debug("ConfigMap not found (will be created on save)")
			// ConfigMap doesn't exist yet, return empty state
			return &AgentState{}, nil
		}
		logger.WithError(err).Error("Error reading ConfigMap")
		return nil, fmt.Errorf("failed to get state ConfigMap: %w", err)
	}

	// Parse state from ConfigMap data
	state := &AgentState{
		AgentID:      cm.Data["agent_id"],
		ClusterID:    cm.Data["cluster_id"],
		ClusterToken: cm.Data["cluster_token"],
	}

	logger.WithFields(logrus.Fields{
		"agent_id":   state.AgentID,
		"cluster_id": state.ClusterID,
		"has_token":  state.ClusterToken != "",
	}).Info("Successfully loaded state from ConfigMap")

	return state, nil
}

// Save saves the agent state to ConfigMap
func (sm *StateManager) Save(state *AgentState) error {
	if !sm.useConfigMap {
		// Silently skip if ConfigMap not available (state will be in-memory only)
		return nil
	}

	ctx := context.Background()

	// Prepare ConfigMap data
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sm.configMapName,
			Namespace: sm.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "pipeops-agent",
				"app.kubernetes.io/component":  "state",
				"app.kubernetes.io/managed-by": "pipeops-agent",
			},
		},
		Data: map[string]string{
			"agent_id":      state.AgentID,
			"cluster_id":    state.ClusterID,
			"cluster_token": state.ClusterToken,
		},
	}

	// Try to get existing ConfigMap
	existingCM, err := sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Get(ctx, sm.configMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new ConfigMap
			_, err = sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Create(ctx, cm, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create state ConfigMap: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to check state ConfigMap: %w", err)
	}

	// Update existing ConfigMap
	existingCM.Data = cm.Data
	_, err = sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Update(ctx, existingCM, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update state ConfigMap: %w", err)
	}

	return nil
}

// GetAgentID loads the agent ID from state
func (sm *StateManager) GetAgentID() (string, error) {
	state, err := sm.Load()
	if err != nil {
		logger.WithError(err).Debug("Failed to load state")
		return "", err
	}
	if state.AgentID == "" {
		logger.Debug("Agent ID is empty in state")
		return "", fmt.Errorf("no agent ID in state")
	}
	logger.WithField("agent_id", state.AgentID).Debug("Loaded agent_id from state")
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
		return "", fmt.Errorf("failed to load state: %w", err)
	}
	if state.ClusterID == "" {
		return "", fmt.Errorf("cluster ID is empty in state (agent_id=%s, has_token=%v)", state.AgentID, state.ClusterToken != "")
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

// ClearClusterID removes the cluster ID from state
// Used when control plane rejects the cluster_id and a fresh registration is needed
func (sm *StateManager) ClearClusterID() error {
	return sm.SaveClusterID("")
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

// GetStatePath returns info about state storage location
func (sm *StateManager) GetStatePath() string {
	if sm.useConfigMap {
		return fmt.Sprintf("ConfigMap:%s/%s", sm.namespace, sm.configMapName)
	}
	return "in-memory (ConfigMap unavailable)"
}

// Clear removes the state ConfigMap
func (sm *StateManager) Clear() error {
	if !sm.useConfigMap {
		// Nothing to clear for in-memory state
		return nil
	}

	ctx := context.Background()
	err := sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Delete(ctx, sm.configMapName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete state ConfigMap: %w", err)
	}
	return nil
}

// IsUsingConfigMap returns whether state is persisted in ConfigMap
func (sm *StateManager) IsUsingConfigMap() bool {
	return sm.useConfigMap
}
