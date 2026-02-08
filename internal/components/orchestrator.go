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
	for i, step := range steps {
		m.logger.WithFields(logrus.Fields{
			"component": step.Name,
			"step":      fmt.Sprintf("%d/%d", i+1, len(steps)),
			"profile":   profile,
		}).Info("Starting component installation")

		// Execute the installation function
		if err := step.InstallFunc(); err != nil {
			if step.Critical {
				return fmt.Errorf("failed to install critical component %s: %w", step.Name, err)
			}
			m.logger.WithError(err).WithField("component", step.Name).Warn("Failed to install non-critical component, continuing...")
			continue
		}

		// Stability Gate: Wait for the component to settle before proceeding
		// This prevents resource spikes from overlapping startups
		m.waitForComponentReady(step.Namespace, step.ReleaseName, profile)
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
			sleepTime = 30 * time.Second
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

	timeout := 5 * time.Minute
	if profile == types.ProfileLow {
		timeout = 10 * time.Minute // Give low resource nodes more time
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

	// Label selector for Helm release
	selector := labels.Set{"app.kubernetes.io/instance": releaseName}.AsSelector().String()

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
			if m.areResourcesReady(ctx, namespace, selector) {
				m.logger.WithField("component", releaseName).Info("✓ Component is ready")

				// Cool-down period after ready state to allow CPU to drop
				coolDown := 5 * time.Second
				if profile == types.ProfileLow {
					coolDown = 30 * time.Second // Significant cool-down for low resource nodes
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

// areResourcesReady checks if Deployments, StatefulSets, and DaemonSets matching the selector are ready
func (m *Manager) areResourcesReady(ctx context.Context, namespace, selector string) bool {
	client := m.installer.K8sClient

	// Check Deployments
	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		m.logger.Debugf("Failed to list deployments: %v", err)
		return false
	}
	for _, d := range deployments.Items {
		if d.Status.Replicas != d.Status.ReadyReplicas {
			return false
		}
	}

	// Check StatefulSets
	statefulsets, err := client.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		m.logger.Debugf("Failed to list statefulsets: %v", err)
		return false
	}
	for _, s := range statefulsets.Items {
		if s.Status.Replicas != s.Status.ReadyReplicas {
			return false
		}
	}

	// Check DaemonSets
	daemonsets, err := client.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		m.logger.Debugf("Failed to list daemonsets: %v", err)
		return false
	}
	for _, ds := range daemonsets.Items {
		if ds.Status.NumberUnavailable > 0 {
			return false
		}
	}

	// If we found nothing, maybe the selector is wrong or resources aren't created yet.
	// However, if we found nothing, we shouldn't block indefinitely if it's a valid "empty" state,
	// but usually we expect something.
	// For simplicity, if count is 0, we verify if we expect resources.
	// Assuming if the install function succeeded, resources should exist.
	// But giving it a few seconds to appear is handled by the polling loop.

	totalCount := len(deployments.Items) + len(statefulsets.Items) + len(daemonsets.Items)
	return totalCount > 0
}
