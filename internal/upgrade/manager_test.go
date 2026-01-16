package upgrade

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDefaultUpgradeConfig(t *testing.T) {
	config := DefaultUpgradeConfig()

	assert.False(t, config.Enabled)
	assert.Equal(t, ChannelStable, config.Channel)
	assert.Equal(t, 1, config.Concurrency)
	assert.Equal(t, 5*time.Minute, config.DrainTimeout)
	assert.True(t, config.AutoInstallController)
	assert.Nil(t, config.Window)
}

func TestUpgradeChannels(t *testing.T) {
	assert.Equal(t, UpgradeChannel("stable"), ChannelStable)
	assert.Equal(t, UpgradeChannel("latest"), ChannelLatest)
	assert.Equal(t, UpgradeChannel("v1.30"), ChannelV1_30)
	assert.Equal(t, UpgradeChannel("v1.31"), ChannelV1_31)
	assert.Equal(t, UpgradeChannel("v1.32"), ChannelV1_32)
	assert.Equal(t, UpgradeChannel("v1.33"), ChannelV1_33)
}

func TestManager_getChannelURL(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	testCases := []struct {
		name     string
		channel  UpgradeChannel
		expected string
	}{
		{
			name:     "stable channel",
			channel:  ChannelStable,
			expected: "https://update.k3s.io/v1-release/channels/stable",
		},
		{
			name:     "latest channel",
			channel:  ChannelLatest,
			expected: "https://update.k3s.io/v1-release/channels/latest",
		},
		{
			name:     "v1.30 channel",
			channel:  ChannelV1_30,
			expected: "https://update.k3s.io/v1-release/channels/v1.30",
		},
		{
			name:     "empty channel defaults to stable",
			channel:  "",
			expected: "https://update.k3s.io/v1-release/channels/stable",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{
				config: UpgradeConfig{
					Channel: tc.channel,
				},
				logger: logger,
			}

			url := m.getChannelURL()
			assert.Equal(t, tc.expected, url)
		})
	}
}

func TestManager_buildPlanSpec_Server(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	m := &Manager{
		config: UpgradeConfig{
			Channel:     ChannelStable,
			Concurrency: 2,
		},
		logger: logger,
	}

	spec := m.buildPlanSpec(true)

	assert.Equal(t, 2, spec["concurrency"])
	assert.Equal(t, true, spec["cordon"])
	assert.Equal(t, "system-upgrade", spec["serviceAccountName"])
	assert.Contains(t, spec["channel"], "stable")

	// Server should have control-plane node selector
	nodeSelector := spec["nodeSelector"].(map[string]interface{})
	expressions := nodeSelector["matchExpressions"].([]interface{})
	require.Len(t, expressions, 1)

	expr := expressions[0].(map[string]interface{})
	assert.Equal(t, "node-role.kubernetes.io/control-plane", expr["key"])
	assert.Equal(t, "In", expr["operator"])
}

func TestManager_buildPlanSpec_Agent(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	m := &Manager{
		config: UpgradeConfig{
			Channel:     ChannelStable,
			Concurrency: 1,
		},
		logger: logger,
	}

	spec := m.buildPlanSpec(false)

	assert.Equal(t, 1, spec["concurrency"])
	assert.Equal(t, true, spec["cordon"])

	// Agent should have DoesNotExist for control-plane
	nodeSelector := spec["nodeSelector"].(map[string]interface{})
	expressions := nodeSelector["matchExpressions"].([]interface{})
	require.Len(t, expressions, 1)

	expr := expressions[0].(map[string]interface{})
	assert.Equal(t, "node-role.kubernetes.io/control-plane", expr["key"])
	assert.Equal(t, "DoesNotExist", expr["operator"])

	// Agent should have prepare step
	prepare := spec["prepare"].(map[string]interface{})
	assert.NotNil(t, prepare)
	assert.Contains(t, prepare["args"], "prepare")
	assert.Contains(t, prepare["args"], "k3s-server")
}

func TestManager_buildPlanSpec_WithVersion(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	m := &Manager{
		config: UpgradeConfig{
			Channel: ChannelStable,
			Version: "v1.33.4+k3s1",
		},
		logger: logger,
	}

	spec := m.buildPlanSpec(true)

	// When version is set, it should use version instead of channel
	assert.Equal(t, "v1.33.4+k3s1", spec["version"])
	assert.Nil(t, spec["channel"])
}

func TestManager_buildPlanSpec_WithWindow(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	m := &Manager{
		config: UpgradeConfig{
			Channel: ChannelStable,
			Window: &UpgradeWindow{
				Days:      []string{"monday", "tuesday", "wednesday"},
				StartTime: "19:00",
				EndTime:   "21:00",
				TimeZone:  "UTC",
			},
		},
		logger: logger,
	}

	spec := m.buildPlanSpec(true)

	window := spec["window"].(map[string]interface{})
	assert.NotNil(t, window)
	assert.Equal(t, "19:00", window["startTime"])
	assert.Equal(t, "21:00", window["endTime"])
	assert.Equal(t, "UTC", window["timeZone"])

	days := window["days"].([]interface{})
	assert.Len(t, days, 3)
}

func TestManager_ensureNamespace(t *testing.T) {
	ctx := context.Background()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	clientset := fake.NewSimpleClientset()

	m := &Manager{
		clientset: clientset,
		config:    DefaultUpgradeConfig(),
		logger:    logger,
	}

	// First call should create namespace
	err := m.ensureNamespace(ctx)
	require.NoError(t, err)

	// Verify namespace exists
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, SystemUpgradeNamespace, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, SystemUpgradeNamespace, ns.Name)
	assert.Equal(t, "privileged", ns.Labels["pod-security.kubernetes.io/enforce"])

	// Second call should not error (idempotent)
	err = m.ensureNamespace(ctx)
	require.NoError(t, err)
}

func TestManager_createRBAC(t *testing.T) {
	ctx := context.Background()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create namespace first
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: SystemUpgradeNamespace,
		},
	}
	clientset := fake.NewSimpleClientset(ns)

	m := &Manager{
		clientset: clientset,
		config:    DefaultUpgradeConfig(),
		logger:    logger,
	}

	err := m.createRBAC(ctx)
	require.NoError(t, err)

	// Verify service account
	sa, err := clientset.CoreV1().ServiceAccounts(SystemUpgradeNamespace).Get(ctx, "system-upgrade", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "system-upgrade", sa.Name)

	// Verify cluster role binding
	crb, err := clientset.RbacV1().ClusterRoleBindings().Get(ctx, "system-upgrade", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "cluster-admin", crb.RoleRef.Name)

	// Second call should not error (idempotent)
	err = m.createRBAC(ctx)
	require.NoError(t, err)
}

func TestManager_createControllerDeployment(t *testing.T) {
	ctx := context.Background()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create namespace first
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: SystemUpgradeNamespace,
		},
	}
	clientset := fake.NewSimpleClientset(ns)

	m := &Manager{
		clientset: clientset,
		config:    DefaultUpgradeConfig(),
		logger:    logger,
	}

	err := m.createControllerDeployment(ctx)
	require.NoError(t, err)

	// Verify deployment
	deploy, err := clientset.AppsV1().Deployments(SystemUpgradeNamespace).Get(ctx, "system-upgrade-controller", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "system-upgrade-controller", deploy.Name)
	assert.Equal(t, int32(1), *deploy.Spec.Replicas)
	assert.Equal(t, "rancher/system-upgrade-controller:v0.14.2", deploy.Spec.Template.Spec.Containers[0].Image)

	// Second call should not error (idempotent)
	err = m.createControllerDeployment(ctx)
	require.NoError(t, err)
}

func TestManager_TriggerUpgrade_EmptyVersion(t *testing.T) {
	ctx := context.Background()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	m := &Manager{
		config: DefaultUpgradeConfig(),
		logger: logger,
	}

	err := m.TriggerUpgrade(ctx, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version is required")
}

func TestUpgradeWindow_Validation(t *testing.T) {
	window := &UpgradeWindow{
		Days:      []string{"monday", "wednesday", "friday"},
		StartTime: "19:00",
		EndTime:   "21:00",
		TimeZone:  "UTC",
	}

	assert.Len(t, window.Days, 3)
	assert.Equal(t, "19:00", window.StartTime)
	assert.Equal(t, "21:00", window.EndTime)
	assert.Equal(t, "UTC", window.TimeZone)
}
