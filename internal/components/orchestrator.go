package components

import (
	"context"
	"fmt"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	releasepkg "helm.sh/helm/v3/pkg/release"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// InstallStep represents a single installation step in the orchestration plan
type InstallStep struct {
	Name        string
	InstallFunc func() error
	Namespace   string
	ReleaseName string // Used to verify readiness (standard Helm label app.kubernetes.io/instance)
	Critical    bool
}

// executeInstallationPlan runs the installation steps with resource-aware gating
func (m *Manager) executeInstallationPlan(profile types.ResourceProfile, steps []InstallStep) error {
	var failedComponents []string

	for i, step := range steps {
		m.logger.WithFields(logrus.Fields{
			"component": step.Name,
			"step":      fmt.Sprintf("%d/%d", i+1, len(steps)),
			"profile":   profile,
		}).Info("Starting component installation")

		// Execute the installation function
		if err := step.InstallFunc(); err != nil {
			if step.Critical {
				remaining := len(steps) - i - 1
				if remaining > 0 {
					skippedNames := make([]string, 0, remaining)
					for _, s := range steps[i+1:] {
						skippedNames = append(skippedNames, s.Name)
					}
					m.logger.WithFields(logrus.Fields{
						"failed_component":   step.Name,
						"skipped_components": skippedNames,
						"skipped_count":      remaining,
					}).Error("Critical component failure aborted remaining installation steps")
				}
				return fmt.Errorf("failed to install critical component %s: %w", step.Name, err)
			}
			failedComponents = append(failedComponents, step.Name)
			m.logger.WithError(err).WithFields(logrus.Fields{
				"component": step.Name,
				"remaining": len(steps) - i - 1,
			}).Warn("Failed to install component, continuing with remaining steps...")
			continue
		}

		// Fire the early-ready callback as soon as the ingress controller is up.
		// This lets the agent start the ingress watcher / gateway proxy immediately
		// instead of waiting for the rest of the monitoring stack.
		if step.Name == "ingress-controller" && m.OnIngressControllerReady != nil {
			m.logger.Info("Ingress controller ready — invoking early-ready callback")
			go m.OnIngressControllerReady()
		}

		// Stability Gate: Wait for the component to settle before proceeding.
		// The ingress callback above runs before this wait to prioritize end-to-end
		// deployment readiness while remaining serialized for subsequent steps.
		m.waitForComponentReady(step.Namespace, step.ReleaseName, profile)
	}

	if len(failedComponents) > 0 {
		m.logger.WithFields(logrus.Fields{
			"failed_components": failedComponents,
			"failed_count":      len(failedComponents),
			"total_steps":       len(steps),
		}).Warn("Some components failed to install during this run")
	}

	return nil
}

// waitForComponentReady waits for the resources associated with a release to become ready
func (m *Manager) waitForComponentReady(namespace, releaseName string, profile types.ResourceProfile) {
	// Skip if no release name provided (e.g. simple manifests or CRDs)
	if releaseName == "" {
		// Just a static sleep for non-Helm components based on profile
		sleepTime := 10 * time.Second
		if profile == types.ProfileLow {
			sleepTime = 8 * time.Second
		}
		m.logger.WithField("duration", sleepTime).Info("Waiting for static stabilization...")
		time.Sleep(sleepTime)
		return
	}

	// If the release is already healthy and has been deployed for a while, don't stall startup
	// waiting for strict readiness checks that may never converge on constrained clusters.
	if releaseWasHealthyBeforeRun, statusText := m.releaseWasHealthyBeforeRun(namespace, releaseName); releaseWasHealthyBeforeRun {
		m.logger.WithFields(logrus.Fields{
			"release": releaseName,
			"status":  statusText,
		}).Info("Release already healthy before this run, skipping readiness wait")
		return
	}

	selector := labels.Set{"app.kubernetes.io/instance": releaseName}.AsSelector().String()

	// Fast path: if the release's workloads are already ready, return immediately
	// instead of stalling behind the first poll tick + cool-down. This is the common
	// case for an idempotent re-run that upgrades an already-running component
	// (e.g. a no-op Traefik upgrade to the same version), which otherwise looks
	// like the agent is "stuck waiting" on an already-healthy ingress controller.
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 15*time.Second)
	alreadyReady := m.componentReady(readyCtx, namespace, releaseName, selector)
	readyCancel()
	if alreadyReady {
		m.logger.WithField("component", releaseName).Info("✓ Component already ready, skipping readiness wait")
		return
	}

	timeout := 5 * time.Minute
	if profile == types.ProfileLow {
		timeout = 4 * time.Minute
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	m.logger.WithFields(logrus.Fields{
		"release": releaseName,
		"timeout": timeout,
	}).Info("Waiting for component to become ready...")

	// Polling interval
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			statusText := ""
			if rel, err := m.installer.GetReleaseStatus(context.Background(), releaseName, namespace); err == nil && rel != nil && rel.Info != nil {
				statusText = string(rel.Info.Status)
				if rel.Info.Status == releasepkg.StatusDeployed {
					m.logger.WithFields(logrus.Fields{
						"component": releaseName,
						"status":    statusText,
					}).Info("Timed out waiting for resource readiness, but Helm release is deployed; continuing")
					return
				}
			}

			m.logger.WithFields(logrus.Fields{
				"component": releaseName,
				"status":    statusText,
			}).Warn("Timeout waiting for component readiness (continuing anyway)")
			return
		case <-ticker.C:
			if m.componentReady(ctx, namespace, releaseName, selector) {
				m.logger.WithField("component", releaseName).Info("✓ Component is ready")

				// Cool-down period after ready state to allow CPU to drop
				coolDown := 5 * time.Second
				if profile == types.ProfileLow {
					coolDown = 8 * time.Second
				}
				m.logger.WithField("duration", coolDown).Info("Cooling down before next step...")
				time.Sleep(coolDown)
				return
			}
		}
	}
}

func (m *Manager) releaseWasHealthyBeforeRun(namespace, releaseName string) (bool, string) {
	rel, err := m.installer.GetReleaseStatus(context.Background(), releaseName, namespace)
	if err != nil || rel == nil || rel.Info == nil {
		return false, ""
	}

	statusText := string(rel.Info.Status)
	if rel.Info.Status != releasepkg.StatusDeployed {
		return false, statusText
	}

	lastDeployed := rel.Info.LastDeployed.Time
	if lastDeployed.IsZero() {
		return false, statusText
	}

	// Treat as pre-existing healthy release only if it predates this run by a small buffer.
	return time.Since(lastDeployed) > 2*time.Minute, statusText
}

// componentReady reports whether a component's workloads are ready.
//
// When the label selector matches one or more workloads, all of them must be
// ready. When it matches NOTHING, we cannot judge readiness by label — this is
// the case for charts (notably Traefik) that label their workloads with an
// instance value of "<release>-<namespace>" rather than the bare release name,
// so the generic "app.kubernetes.io/instance=<release>" selector finds zero
// objects. Rather than burn the full timeout polling a selector that will never
// match, we fall back to the Helm release status: a `deployed` release is
// treated as ready. This is what was causing the agent to appear "stuck waiting
// for traefik" for the entire 4-minute window on every reconcile.
func (m *Manager) componentReady(ctx context.Context, namespace, releaseName, selector string) bool {
	allReady, matched := m.areResourcesReady(ctx, namespace, selector)
	if matched > 0 {
		return allReady
	}
	return m.isReleaseDeployed(namespace, releaseName)
}

// isReleaseDeployed reports whether the Helm release is in the deployed state.
func (m *Manager) isReleaseDeployed(namespace, releaseName string) bool {
	if releaseName == "" {
		return false
	}
	rel, err := m.installer.GetReleaseStatus(context.Background(), releaseName, namespace)
	return err == nil && rel != nil && rel.Info != nil && rel.Info.Status == releasepkg.StatusDeployed
}

// areResourcesReady checks if Deployments, StatefulSets, and DaemonSets matching
// the selector are ready. It returns whether all matched workloads are ready and
// how many workloads matched the selector (0 means the selector matched nothing,
// which the caller interprets separately).
func (m *Manager) areResourcesReady(ctx context.Context, namespace, selector string) (ready bool, matched int) {
	client := m.installer.K8sClient

	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		m.logger.Debugf("Failed to list deployments: %v", err)
		return false, 0
	}
	statefulsets, err := client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		m.logger.Debugf("Failed to list statefulsets: %v", err)
		return false, 0
	}
	daemonsets, err := client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		m.logger.Debugf("Failed to list daemonsets: %v", err)
		return false, 0
	}

	matched = len(deployments.Items) + len(statefulsets.Items) + len(daemonsets.Items)
	if matched == 0 {
		return false, 0
	}

	for _, d := range deployments.Items {
		if d.Status.Replicas != d.Status.ReadyReplicas {
			return false, matched
		}
	}
	for _, s := range statefulsets.Items {
		if s.Status.Replicas != s.Status.ReadyReplicas {
			return false, matched
		}
	}
	for _, ds := range daemonsets.Items {
		if ds.Status.NumberUnavailable > 0 {
			return false, matched
		}
	}

	return true, matched
}
