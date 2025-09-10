package agent

import (
	"context"
	"testing"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Mock implementations for testing
type mockK8sClient struct{}

func (m *mockK8sClient) GetClusterStatus(ctx context.Context) (*types.ClusterStatus, error) {
	return &types.ClusterStatus{
		Nodes: []types.NodeStatus{
			{
				Name:    "test-node",
				Ready:   true,
				Version: "v1.28.3",
				OS:      "linux",
				Arch:    "amd64",
			},
		},
		Namespaces:  []string{"default", "kube-system", "pipeops-system"},
		Deployments: []types.ResourceInfo{},
		Services:    []types.ResourceInfo{},
		Pods:        []types.PodInfo{},
		Metrics: types.ClusterMetrics{
			CPUUsage:    0.0,
			MemoryUsage: 0.0,
			PodCount:    0,
			NodeCount:   1,
		},
		Timestamp: time.Now(),
	}, nil
}

type mockCommClient struct {
	messages []types.Message
}

func (m *mockCommClient) Connect() error {
	return nil
}

func (m *mockCommClient) Disconnect() error {
	return nil
}

func (m *mockCommClient) SendMessage(msg *types.Message) error {
	m.messages = append(m.messages, *msg)
	return nil
}

func (m *mockCommClient) RegisterHandler(msgType types.MessageType, handler interface{}) {
	// Mock implementation
}

func (m *mockCommClient) IsConnected() bool {
	return true
}

// Test configuration
func getTestConfig() *types.Config {
	return &types.Config{
		Agent: types.AgentConfig{
			Name:         "test-agent",
			ID:           "test-agent-123",
			ClusterName:  "test-cluster",
			Labels:       map[string]string{"test": "true"},
			PollInterval: 30 * time.Second,
		},
		PipeOps: types.PipeOpsConfig{
			APIURL:  "https://api.test.pipeops.io",
			Token:   "test-token",
			Timeout: 30 * time.Second,
			Reconnect: types.ReconnectConfig{
				Enabled:     true,
				MaxAttempts: 5,
				Interval:    5 * time.Second,
				Backoff:     5 * time.Second,
			},
		},
		Kubernetes: types.KubernetesConfig{
			InCluster: true,
			Namespace: "pipeops-system",
		},
		Logging: types.LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
	}
}

func TestAgent_New(t *testing.T) {
	config := getTestConfig()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Note: This test would need proper mocking of the k8s client
	// For now, it's just a structure test
	t.Run("config validation", func(t *testing.T) {
		if config.Agent.Name == "" {
			t.Error("Agent name should not be empty")
		}

		if config.Agent.ClusterName == "" {
			t.Error("Cluster name should not be empty")
		}

		if config.PipeOps.APIURL == "" {
			t.Error("PipeOps API URL should not be empty")
		}

		if config.PipeOps.Token == "" {
			t.Error("PipeOps token should not be empty")
		}
	})
}

func TestMessageHandling(t *testing.T) {
	t.Run("deployment request message", func(t *testing.T) {
		req := &types.DeploymentRequest{
			Name:      "test-app",
			Namespace: "default",
			Image:     "nginx:latest",
			Replicas:  1,
			Ports: []types.Port{
				{
					Name:          "http",
					ContainerPort: 80,
					Protocol:      "TCP",
				},
			},
			Environment: map[string]string{
				"ENV": "test",
			},
			Labels: map[string]string{
				"app": "test-app",
			},
		}

		// Validate deployment request structure
		if req.Name == "" {
			t.Error("Deployment name should not be empty")
		}

		if req.Image == "" {
			t.Error("Deployment image should not be empty")
		}

		if req.Replicas <= 0 {
			t.Error("Replicas should be positive")
		}
	})
}

func TestAgentStatus(t *testing.T) {
	t.Run("agent status types", func(t *testing.T) {
		statuses := []types.AgentStatus{
			types.AgentStatusConnected,
			types.AgentStatusDisconnected,
			types.AgentStatusError,
			types.AgentStatusRegistering,
		}

		for _, status := range statuses {
			if string(status) == "" {
				t.Errorf("Agent status should not be empty: %v", status)
			}
		}
	})
}

func TestMessageTypes(t *testing.T) {
	t.Run("message types", func(t *testing.T) {
		messageTypes := []types.MessageType{
			types.MessageTypeRegister,
			types.MessageTypeHeartbeat,
			types.MessageTypeStatus,
			types.MessageTypeDeploy,
			types.MessageTypeDelete,
			types.MessageTypeScale,
		}

		for _, msgType := range messageTypes {
			if string(msgType) == "" {
				t.Errorf("Message type should not be empty: %v", msgType)
			}
		}
	})
}
