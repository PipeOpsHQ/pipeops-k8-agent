package state

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var logger = logrus.WithField("component", "StateManager")

// AgentState represents the persistent state of the agent
type AgentState struct {
	AgentID         string `yaml:"agent_id"`
	ClusterID       string `yaml:"cluster_id"`
	ClusterToken    string `yaml:"cluster_token"`
	ClusterCertData string `yaml:"cluster_cert_data"`
	GatewayWSURL    string `yaml:"gateway_ws_url"`
}

// StateManager manages persistent agent state using Kubernetes ConfigMap for non-sensitive data
// and Kubernetes Secret for sensitive data (tokens, certificates)
type StateManager struct {
	config        *types.Config
	state         AgentState
	statePath     string
	useConfigMap  bool
	namespace     string
	configMapName string
	secretName    string
	k8sClient     *kubernetes.Clientset // For ConfigMap operations
	logger        *logrus.Logger
	mu            sync.Mutex   // Mutex to protect state modifications
	cacheMutex    sync.RWMutex // Mutex to protect cached state
	cachedState   AgentState   // In-memory cache of state
}

// NewStateManager creates a new state manager
// Attempts to use ConfigMap/Secret for persistence, falls back to in-memory only if unavailable
func NewStateManager() *StateManager {
	// Get ConfigMap name from environment or use default
	configMapName := "pipeops-agent-state"
	if envName := os.Getenv("PIPEOPS_STATE_CONFIGMAP_NAME"); envName != "" {
		configMapName = envName
	}

	// Secret name for sensitive data (token, cert)
	secretName := "pipeops-agent-secrets"
	if envName := os.Getenv("PIPEOPS_STATE_SECRET_NAME"); envName != "" {
		secretName = envName
	}

	sm := &StateManager{
		namespace:     getNamespace(),
		configMapName: configMapName,
		secretName:    secretName,
		useConfigMap:  false,
	}

	// Try to create Kubernetes client for ConfigMap/Secret-based state
	if config, err := rest.InClusterConfig(); err == nil {
		if client, err := kubernetes.NewForConfig(config); err == nil {
			sm.k8sClient = client
			sm.useConfigMap = true
			logger.WithFields(logrus.Fields{
				"namespace":     sm.namespace,
				"configmap":     sm.configMapName,
				"secret":        sm.secretName,
				"use_configmap": true,
			}).Info("✅ Using ConfigMap/Secret for state persistence")

			// Test ConfigMap and Secret access immediately
			if err := sm.testConfigMapAccess(); err != nil {
				logger.WithError(err).Warn("⚠️  ConfigMap access test failed - state may not persist")
			} else {
				logger.Debug("✅ ConfigMap access verified")
			}
			if err := sm.testSecretAccess(); err != nil {
				logger.WithError(err).Warn("⚠️  Secret access test failed - sensitive state may not persist")
			} else {
				logger.Debug("✅ Secret access verified")
			}
		} else {
			logger.WithError(err).Warn("❌ Failed to create K8s client - using in-memory state only")
		}
	} else {
		logger.WithError(err).Warn("❌ Not running in cluster - using in-memory state only")
	}

	sm.mu = sync.Mutex{}
	return sm
}

// getNamespace returns the namespace the agent is running in
func getNamespace() string {
	// First check explicit state namespace environment variable
	if ns := os.Getenv("PIPEOPS_STATE_NAMESPACE"); ns != "" {
		return ns
	}
	// Try to read from service account mount
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(data))
	}
	// Fallback to pod namespace environment variable
	if ns := os.Getenv("PIPEOPS_POD_NAMESPACE"); ns != "" {
		return ns
	}
	// Default namespace
	return "pipeops-system"
}

// Load loads the agent state from ConfigMap (non-sensitive) and Secret (sensitive)
func (sm *StateManager) Load() (*AgentState, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.useConfigMap {
		logger.Debug("ConfigMap not available, returning empty state")
		// Return empty state if ConfigMap not available
		return &AgentState{}, nil
	}

	ctx := context.Background()
	logger.WithFields(logrus.Fields{
		"namespace": sm.namespace,
		"configmap": sm.configMapName,
		"secret":    sm.secretName,
	}).Debug("Attempting to read state from ConfigMap and Secret")

	state := &AgentState{}

	// Load non-sensitive data from ConfigMap
	cm, err := sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Get(ctx, sm.configMapName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.WithError(err).Error("Error reading ConfigMap")
			return nil, fmt.Errorf("failed to get state ConfigMap: %w", err)
		}
		logger.Debug("ConfigMap not found (will be created on save)")
	} else {
		state.AgentID = cm.Data["agent_id"]
		state.ClusterID = cm.Data["cluster_id"]
		state.GatewayWSURL = cm.Data["gateway_ws_url"]
	}

	// Load sensitive data from Secret
	secret, err := sm.k8sClient.CoreV1().Secrets(sm.namespace).Get(ctx, sm.secretName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			logger.WithError(err).Error("Error reading Secret")
			return nil, fmt.Errorf("failed to get state Secret: %w", err)
		}
		logger.Debug("Secret not found (will be created on save)")
	} else {
		state.ClusterToken = string(secret.Data["cluster_token"])
		state.ClusterCertData = string(secret.Data["cluster_cert_data"])
	}

	sm.updateCache(state)

	logger.WithFields(logrus.Fields{
		"agent_id":   state.AgentID,
		"cluster_id": state.ClusterID,
		"has_token":  state.ClusterToken != "",
		"has_cert":   state.ClusterCertData != "",
	}).Debug("Loaded state from ConfigMap/Secret")

	return state, nil
}

// Save saves the agent state to ConfigMap (non-sensitive) and Secret (sensitive)
func (sm *StateManager) Save(state *AgentState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.useConfigMap {
		// Silently skip if ConfigMap not available (state will be in-memory only)
		return nil
	}

	ctx := context.Background()

	// Save non-sensitive data to ConfigMap
	if err := sm.saveToConfigMap(ctx, state); err != nil {
		return err
	}

	// Save sensitive data to Secret
	if err := sm.saveToSecret(ctx, state); err != nil {
		return err
	}

	sm.updateCache(state)
	return nil
}

// saveToConfigMap saves non-sensitive state data to ConfigMap
func (sm *StateManager) saveToConfigMap(ctx context.Context, state *AgentState) error {
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
			"agent_id":       state.AgentID,
			"cluster_id":     state.ClusterID,
			"gateway_ws_url": state.GatewayWSURL,
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

// saveToSecret saves sensitive state data to Secret
func (sm *StateManager) saveToSecret(ctx context.Context, state *AgentState) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sm.secretName,
			Namespace: sm.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "pipeops-agent",
				"app.kubernetes.io/component":  "secrets",
				"app.kubernetes.io/managed-by": "pipeops-agent",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"cluster_token":     []byte(state.ClusterToken),
			"cluster_cert_data": []byte(state.ClusterCertData),
		},
	}

	// Try to get existing Secret
	existingSecret, err := sm.k8sClient.CoreV1().Secrets(sm.namespace).Get(ctx, sm.secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new Secret
			_, err = sm.k8sClient.CoreV1().Secrets(sm.namespace).Create(ctx, secret, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create state Secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to check state Secret: %w", err)
	}

	// Update existing Secret
	existingSecret.Data = secret.Data
	_, err = sm.k8sClient.CoreV1().Secrets(sm.namespace).Update(ctx, existingSecret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update state Secret: %w", err)
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
		logger.WithError(err).Debug("Falling back to cached state while saving agent ID")
		state = sm.cloneCachedState()
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
		logger.WithError(err).Debug("Falling back to cached state while saving cluster ID")
		state = sm.cloneCachedState()
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
		logger.WithError(err).Debug("Falling back to cached state while saving cluster token")
		state = sm.cloneCachedState()
	}
	state.ClusterToken = token
	return sm.Save(state)
}

// GetClusterCertData loads the cluster CA bundle from state
func (sm *StateManager) GetClusterCertData() (string, error) {
	state, err := sm.Load()
	if err != nil {
		return "", err
	}
	if state.ClusterCertData == "" {
		return "", fmt.Errorf("no cluster cert data in state")
	}
	return state.ClusterCertData, nil
}

// SaveClusterCertData saves the cluster CA bundle to state
func (sm *StateManager) SaveClusterCertData(certData string) error {
	state, err := sm.Load()
	if err != nil {
		logger.WithError(err).Debug("Falling back to cached state while saving cluster cert data")
		state = sm.cloneCachedState()
	}
	state.ClusterCertData = certData
	return sm.Save(state)
}

// GetGatewayWSURL loads the gateway WebSocket URL from state
func (sm *StateManager) GetGatewayWSURL() (string, error) {
	state, err := sm.Load()
	if err != nil {
		return "", err
	}
	return state.GatewayWSURL, nil
}

// SaveGatewayWSURL saves the gateway WebSocket URL to state
func (sm *StateManager) SaveGatewayWSURL(url string) error {
	state, err := sm.Load()
	if err != nil {
		logger.WithError(err).Debug("Falling back to cached state while saving gateway WS URL")
		state = sm.cloneCachedState()
	}
	state.GatewayWSURL = url
	return sm.Save(state)
}

// GetStatePath returns info about state storage location
func (sm *StateManager) GetStatePath() string {
	if sm.useConfigMap {
		return fmt.Sprintf("ConfigMap:%s/%s, Secret:%s/%s", sm.namespace, sm.configMapName, sm.namespace, sm.secretName)
	}
	return "in-memory (ConfigMap/Secret unavailable)"
}

// Clear removes the state ConfigMap and Secret
func (sm *StateManager) Clear() error {
	if !sm.useConfigMap {
		// Nothing to clear for in-memory state
		return nil
	}

	ctx := context.Background()

	// Delete ConfigMap
	err := sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Delete(ctx, sm.configMapName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete state ConfigMap: %w", err)
	}

	// Delete Secret
	err = sm.k8sClient.CoreV1().Secrets(sm.namespace).Delete(ctx, sm.secretName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete state Secret: %w", err)
	}

	sm.updateCache(&AgentState{})
	return nil
}

// IsUsingConfigMap returns whether state is persisted in ConfigMap
func (sm *StateManager) IsUsingConfigMap() bool {
	return sm.useConfigMap
}

func (sm *StateManager) updateCache(state *AgentState) {
	if state == nil {
		return
	}
	sm.cacheMutex.Lock()
	sm.cachedState.AgentID = state.AgentID
	sm.cachedState.ClusterID = state.ClusterID
	sm.cachedState.ClusterToken = state.ClusterToken
	sm.cachedState.ClusterCertData = state.ClusterCertData
	sm.cachedState.GatewayWSURL = state.GatewayWSURL
	sm.cacheMutex.Unlock()
}

func (sm *StateManager) cloneCachedState() *AgentState {
	sm.cacheMutex.RLock()
	defer sm.cacheMutex.RUnlock()
	return &AgentState{
		AgentID:         sm.cachedState.AgentID,
		ClusterID:       sm.cachedState.ClusterID,
		ClusterToken:    sm.cachedState.ClusterToken,
		ClusterCertData: sm.cachedState.ClusterCertData,
		GatewayWSURL:    sm.cachedState.GatewayWSURL,
	}
}

// testConfigMapAccess tests if the agent can create/read ConfigMaps in its namespace
func (sm *StateManager) testConfigMapAccess() error {
	if !sm.useConfigMap {
		return fmt.Errorf("ConfigMap not available")
	}

	ctx := context.Background()
	testCMName := sm.configMapName + "-test"

	// Try to create a test ConfigMap
	testCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCMName,
			Namespace: sm.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "pipeops-agent",
				"app.kubernetes.io/component": "state-test",
			},
		},
		Data: map[string]string{
			"test": "access-test",
		},
	}

	// Create test ConfigMap
	_, err := sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Create(ctx, testCM, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create test ConfigMap: %w", err)
	}

	// Clean up test ConfigMap
	err = sm.k8sClient.CoreV1().ConfigMaps(sm.namespace).Delete(ctx, testCMName, metav1.DeleteOptions{})
	if err != nil {
		logger.WithError(err).Warn("Failed to clean up test ConfigMap")
	}

	return nil
}

// testSecretAccess tests if the agent can create/read Secrets in its namespace
func (sm *StateManager) testSecretAccess() error {
	if !sm.useConfigMap {
		return fmt.Errorf("Secret access not available")
	}

	ctx := context.Background()
	testSecretName := sm.secretName + "-test"

	// Try to create a test Secret
	testSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: sm.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "pipeops-agent",
				"app.kubernetes.io/component": "secret-test",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"test": []byte("access-test"),
		},
	}

	// Create test Secret
	_, err := sm.k8sClient.CoreV1().Secrets(sm.namespace).Create(ctx, testSecret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create test Secret: %w", err)
	}

	// Clean up test Secret
	err = sm.k8sClient.CoreV1().Secrets(sm.namespace).Delete(ctx, testSecretName, metav1.DeleteOptions{})
	if err != nil {
		logger.WithError(err).Warn("Failed to clean up test Secret")
	}

	return nil
}
