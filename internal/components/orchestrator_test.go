package components

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager creates a minimal Manager suitable for orchestrator unit tests.
// It only sets the logger and context — no Helm or K8s dependencies.
func newTestManager() *Manager {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}
}

func TestExecuteInstallationPlan_CallbackFiresAfterIngressController(t *testing.T) {
	mgr := newTestManager()
	defer mgr.cancel()

	var callbackFired atomic.Bool

	mgr.OnIngressControllerReady = func() {
		callbackFired.Store(true)
	}

	steps := []InstallStep{
		{
			Name:        "ingress-controller",
			InstallFunc: func() error { return nil },
			// Empty ReleaseName triggers a short static sleep in waitForComponentReady
		},
		{
			Name:        "cert-manager",
			InstallFunc: func() error { return nil },
		},
	}

	err := mgr.executeInstallationPlan(types.ProfileHigh, steps)
	require.NoError(t, err)

	// The callback is invoked in a goroutine; give it a moment to land.
	time.Sleep(50 * time.Millisecond)
	assert.True(t, callbackFired.Load(), "OnIngressControllerReady callback should have fired after ingress-controller step")
}

func TestExecuteInstallationPlan_CallbackNotFiredForOtherSteps(t *testing.T) {
	mgr := newTestManager()
	defer mgr.cancel()

	var callbackFired atomic.Bool

	mgr.OnIngressControllerReady = func() {
		callbackFired.Store(true)
	}

	steps := []InstallStep{
		{
			Name:        "cert-manager",
			InstallFunc: func() error { return nil },
		},
		{
			Name:        "prometheus",
			InstallFunc: func() error { return nil },
		},
	}

	err := mgr.executeInstallationPlan(types.ProfileHigh, steps)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.False(t, callbackFired.Load(), "OnIngressControllerReady callback should NOT fire for non-ingress-controller steps")
}

func TestExecuteInstallationPlan_NoCallbackWhenNil(t *testing.T) {
	mgr := newTestManager()
	defer mgr.cancel()

	// OnIngressControllerReady is nil by default — should not panic.
	steps := []InstallStep{
		{
			Name:        "ingress-controller",
			InstallFunc: func() error { return nil },
		},
	}

	err := mgr.executeInstallationPlan(types.ProfileHigh, steps)
	require.NoError(t, err)
}

func TestExecuteInstallationPlan_CallbackNotFiredOnIngressControllerFailure(t *testing.T) {
	mgr := newTestManager()
	defer mgr.cancel()

	var callbackFired atomic.Bool

	mgr.OnIngressControllerReady = func() {
		callbackFired.Store(true)
	}

	steps := []InstallStep{
		{
			Name:        "ingress-controller",
			Critical:    true,
			InstallFunc: func() error { return fmt.Errorf("traefik install failed") },
		},
		{
			Name:        "cert-manager",
			InstallFunc: func() error { return nil },
		},
	}

	err := mgr.executeInstallationPlan(types.ProfileHigh, steps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ingress-controller")

	time.Sleep(50 * time.Millisecond)
	assert.False(t, callbackFired.Load(), "OnIngressControllerReady callback should NOT fire when ingress-controller install fails")
}

func TestExecuteInstallationPlan_CallbackNotFiredOnNonCriticalIngressFailure(t *testing.T) {
	mgr := newTestManager()
	defer mgr.cancel()

	var callbackFired atomic.Bool

	mgr.OnIngressControllerReady = func() {
		callbackFired.Store(true)
	}

	// Even if ingress-controller is non-critical and fails, the callback should
	// not fire because the install didn't succeed.
	steps := []InstallStep{
		{
			Name:        "ingress-controller",
			Critical:    false,
			InstallFunc: func() error { return fmt.Errorf("traefik install failed") },
		},
		{
			Name:        "cert-manager",
			InstallFunc: func() error { return nil },
		},
	}

	err := mgr.executeInstallationPlan(types.ProfileHigh, steps)
	require.NoError(t, err) // non-critical failure doesn't abort

	time.Sleep(50 * time.Millisecond)
	assert.False(t, callbackFired.Load(), "OnIngressControllerReady callback should NOT fire when ingress-controller install fails (even non-critical)")
}
