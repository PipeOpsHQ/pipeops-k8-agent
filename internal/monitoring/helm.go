package monitoring

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// HelmInstaller manages Helm chart installations
type HelmInstaller struct {
	logger *logrus.Logger
}

// HelmRelease represents a Helm release to install
type HelmRelease struct {
	Name      string
	Namespace string
	Chart     string
	Repo      string
	Version   string
	Values    map[string]interface{}
}

// NewHelmInstaller creates a new Helm installer
func NewHelmInstaller(logger *logrus.Logger) (*HelmInstaller, error) {
	// Check if Helm is installed
	if _, err := exec.LookPath("helm"); err != nil {
		return nil, fmt.Errorf("helm not found in PATH: %w", err)
	}

	return &HelmInstaller{
		logger: logger,
	}, nil
}

// Install installs or upgrades a Helm release
func (h *HelmInstaller) Install(ctx context.Context, release *HelmRelease) error {
	h.logger.WithFields(logrus.Fields{
		"release":   release.Name,
		"namespace": release.Namespace,
		"chart":     release.Chart,
		"version":   release.Version,
	}).Info("Installing Helm release...")

	// Create namespace if it doesn't exist
	if err := h.createNamespace(ctx, release.Namespace); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Add Helm repo if needed
	if release.Repo != "" {
		if err := h.addRepo(ctx, release.Chart, release.Repo); err != nil {
			h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
		}
	}

	// Update Helm repos
	if err := h.updateRepos(ctx); err != nil {
		h.logger.WithError(err).Warn("Failed to update Helm repos")
	}

	// Create values file
	valuesFile, err := h.createValuesFile(release.Values)
	if err != nil {
		return fmt.Errorf("failed to create values file: %w", err)
	}
	defer os.Remove(valuesFile)

	// Install or upgrade release
	args := []string{
		"upgrade", "--install",
		release.Name,
		release.Chart,
		"--namespace", release.Namespace,
		"--create-namespace",
		"--wait",
		"--timeout", "10m",
	}

	if release.Version != "" {
		args = append(args, "--version", release.Version)
	}

	if valuesFile != "" {
		args = append(args, "--values", valuesFile)
	}

	cmd := exec.CommandContext(ctx, "helm", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm install failed: %w\nOutput: %s", err, string(output))
	}

	h.logger.WithField("release", release.Name).Info("Helm release installed successfully")
	return nil
}

// Uninstall uninstalls a Helm release
func (h *HelmInstaller) Uninstall(ctx context.Context, name, namespace string) error {
	h.logger.WithFields(logrus.Fields{
		"release":   name,
		"namespace": namespace,
	}).Info("Uninstalling Helm release...")

	cmd := exec.CommandContext(ctx, "helm", "uninstall", name, "--namespace", namespace)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm uninstall failed: %w\nOutput: %s", err, string(output))
	}

	h.logger.WithField("release", name).Info("Helm release uninstalled successfully")
	return nil
}

// IsInstalled checks if a Helm release is installed
func (h *HelmInstaller) IsInstalled(ctx context.Context, name, namespace string) bool {
	cmd := exec.CommandContext(ctx, "helm", "status", name, "--namespace", namespace)
	err := cmd.Run()
	return err == nil
}

// GetReleaseStatus gets the status of a Helm release
func (h *HelmInstaller) GetReleaseStatus(ctx context.Context, name, namespace string) (string, error) {
	cmd := exec.CommandContext(ctx, "helm", "status", name, "--namespace", namespace, "--output", "json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get release status: %w", err)
	}
	return string(output), nil
}

// createNamespace creates a Kubernetes namespace if it doesn't exist
func (h *HelmInstaller) createNamespace(ctx context.Context, namespace string) error {
	cmd := exec.CommandContext(ctx, "kubectl", "create", "namespace", namespace, "--dry-run=client", "-o", "yaml")
	yaml, err := cmd.Output()
	if err != nil {
		return err
	}

	applyCmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
	applyCmd.Stdin = bytes.NewReader(yaml)
	if err := applyCmd.Run(); err != nil {
		// Ignore error if namespace already exists
		return nil
	}

	return nil
}

// addRepo adds a Helm repository
func (h *HelmInstaller) addRepo(ctx context.Context, name, url string) error {
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", name, url, "--force-update")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add repo: %w", err)
	}
	return nil
}

// updateRepos updates all Helm repositories
func (h *HelmInstaller) updateRepos(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "helm", "repo", "update")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update repos: %w", err)
	}
	return nil
}

// createValuesFile creates a temporary values file for Helm
func (h *HelmInstaller) createValuesFile(values map[string]interface{}) (string, error) {
	if len(values) == 0 {
		return "", nil
	}

	tmpFile, err := os.CreateTemp("", "helm-values-*.yaml")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	encoder := yaml.NewEncoder(tmpFile)
	if err := encoder.Encode(values); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}
