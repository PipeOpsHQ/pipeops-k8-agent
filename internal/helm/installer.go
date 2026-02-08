package helm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	releasepkg "helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/repo"
	"helm.sh/helm/v3/pkg/storage/driver"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

// HelmInstaller manages Helm chart installations using Helm SDK
type HelmInstaller struct {
	logger    *logrus.Logger
	settings  *cli.EnvSettings
	K8sClient *kubernetes.Clientset
	Config    *rest.Config
}

// HelmRelease represents a Helm release to install
type HelmRelease struct {
	Name      string
	Namespace string
	Chart     string
	Repo      string
	Version   string
	Values    map[string]interface{}
	SkipCRDs  bool // When true, Helm will not install CRDs bundled with the chart
}

// NewHelmInstaller creates a new Helm installer using the Helm SDK
func NewHelmInstaller(logger *logrus.Logger) (*HelmInstaller, error) {
	// Create in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster Config: %w", err)
	}

	// Create Kubernetes clientset
	k8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Create Helm settings with writable cache directory
	settings := cli.New()
	// Use /tmp for Helm cache as it's writable in container environments
	settings.RepositoryCache = "/tmp/helm/cache"
	settings.RepositoryConfig = "/tmp/helm/repositories.yaml"

	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(settings.RepositoryCache, 0755); err != nil {
		return nil, fmt.Errorf("failed to create Helm cache directory: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"cache_dir":  settings.RepositoryCache,
		"config_dir": settings.RepositoryConfig,
	}).Debug("Configured Helm cache directories")

	return &HelmInstaller{
		logger:    logger,
		settings:  settings,
		K8sClient: k8sClient,
		Config:    config,
	}, nil
}

// Install installs or upgrades a Helm release using the Helm SDK
func (h *HelmInstaller) Install(ctx context.Context, release *HelmRelease) error {
	// Create action configuration with warning suppression
	actionConfig := new(action.Configuration)
	// Use a custom log function that filters out benign Kubernetes warnings
	logFunc := func(format string, v ...interface{}) {
		msg := fmt.Sprintf(format, v...)
		// Filter out common benign warnings from Kubernetes
		if strings.Contains(msg, "spec.SessionAffinity is ignored for headless services") ||
			strings.Contains(msg, "unknown field \"spec.automountServiceAccountToken\"") ||
			strings.Contains(msg, "unknown field \"spec.enableOTLPReceiver\"") {
			// Suppress these warnings - they're harmless but clutter logs
			return
		}
		h.debugLog(format, v...)
	}
	if err := actionConfig.Init(h.settings.RESTClientGetter(), release.Namespace, "secret", logFunc); err != nil {
		return fmt.Errorf("failed to initialize Helm action Config: %w", err)
	}

	histClient := action.NewHistory(actionConfig)
	histClient.Max = 1
	if _, err := histClient.Run(release.Name); err == nil {
		getClient := action.NewGet(actionConfig)
		existingRelease, err := getClient.Run(release.Name)
		if err == nil {
			existingVersion := existingRelease.Chart.Metadata.Version
			existingStatus := existingRelease.Info.Status

			switch existingStatus {
			case releasepkg.StatusPendingInstall, releasepkg.StatusPendingUpgrade, releasepkg.StatusPendingRollback:
				h.logger.WithFields(logrus.Fields{
					"release":           release.Name,
					"installed_version": existingVersion,
					"requested_version": release.Version,
					"status":            existingStatus,
				}).Warn("Helm release has a pending operation; skipping upgrade to avoid conflict")
				return nil
			case releasepkg.StatusDeployed:
				if release.Version == "" || release.Version == existingVersion {
					h.logger.WithFields(logrus.Fields{
						"release":           release.Name,
						"installed_version": existingVersion,
						"requested_version": release.Version,
						"status":            existingStatus,
					}).Info("Release already installed and healthy - skipping Helm upgrade")
					return nil
				}
			case releasepkg.StatusFailed, releasepkg.StatusUninstalled, releasepkg.StatusUninstalling, releasepkg.StatusSuperseded, releasepkg.StatusUnknown:
				h.logger.WithFields(logrus.Fields{
					"release": release.Name,
					"status":  existingStatus,
				}).Warn("Helm release is not healthy; attempting to remove from history before reinstall")

				if err := h.cleanupRelease(ctx, release.Name, release.Namespace); err != nil {
					h.logger.WithError(err).Warn("Release cleanup encountered issues; continuing with reinstall attempts")
				} else {
					h.logger.WithField("release", release.Name).Info("Release cleanup completed")
				}

				if release.Repo != "" {
					if err := h.AddRepo(ctx, release.Chart, release.Repo); err != nil {
						h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
					}
				}

				if err := h.createNamespace(ctx, release.Namespace); err != nil {
					return fmt.Errorf("failed to create namespace: %w", err)
				}

				if err := h.install(ctx, actionConfig, release, true); err != nil {
					if strings.Contains(err.Error(), "cannot re-use a name") {
						h.logger.WithField("release", release.Name).Warn("Release name still present after cleanup; attempting forced upgrade")
						return h.upgradeWithForce(ctx, actionConfig, release)
					}
					return err
				}

				return nil
			}

			h.logger.WithFields(logrus.Fields{
				"release":           release.Name,
				"installed_version": existingVersion,
				"requested_version": release.Version,
				"status":            existingStatus,
			}).Info("Upgrading Helm release")
		}

		if release.Repo != "" {
			if err := h.AddRepo(ctx, release.Chart, release.Repo); err != nil {
				h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
			}
		}

		if err := h.createNamespace(ctx, release.Namespace); err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}

		return h.upgrade(ctx, actionConfig, release)
	}

	if release.Repo != "" {
		if err := h.AddRepo(ctx, release.Chart, release.Repo); err != nil {
			h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
		}
	}

	if err := h.createNamespace(ctx, release.Namespace); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"release":   release.Name,
		"namespace": release.Namespace,
		"chart":     release.Chart,
		"version":   release.Version,
	}).Info("Installing Helm release...")

	// Clean up conflicting cluster-scoped resources before install
	if err := h.cleanupConflictingClusterResources(ctx, release); err != nil {
		h.logger.WithError(err).Warn("Failed to cleanup conflicting cluster resources, continuing anyway")
	}

	return h.install(ctx, actionConfig, release, false)
}

// cleanupConflictingClusterResources removes cluster-scoped resources with wrong namespace annotations
func (h *HelmInstaller) cleanupConflictingClusterResources(ctx context.Context, release *HelmRelease) error {
	// Check for common cluster-scoped resources that might have namespace conflicts
	// Focus on resources commonly created by monitoring charts like Loki

	resourceName := fmt.Sprintf("%s-promtail", release.Name)

	// Try to get the ClusterRole
	clusterRole, err := h.K8sClient.RbacV1().ClusterRoles().Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// No conflict - resource doesn't exist
			return nil
		}
		return fmt.Errorf("failed to check ClusterRole: %w", err)
	}

	// Check if it has a different namespace annotation
	if annotations := clusterRole.GetAnnotations(); annotations != nil {
		if releaseNs, exists := annotations["meta.helm.sh/release-namespace"]; exists && releaseNs != release.Namespace {
			h.logger.WithFields(logrus.Fields{
				"resource":         resourceName,
				"currentNamespace": releaseNs,
				"targetNamespace":  release.Namespace,
			}).Info("Deleting ClusterRole with conflicting namespace annotation")

			// Delete the ClusterRole
			if err := h.K8sClient.RbacV1().ClusterRoles().Delete(ctx, resourceName, metav1.DeleteOptions{}); err != nil {
				return fmt.Errorf("failed to delete ClusterRole: %w", err)
			}
		}
	}

	// Also check ClusterRoleBinding
	clusterRoleBinding, err := h.K8sClient.RbacV1().ClusterRoleBindings().Get(ctx, resourceName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to check ClusterRoleBinding: %w", err)
	}

	if annotations := clusterRoleBinding.GetAnnotations(); annotations != nil {
		if releaseNs, exists := annotations["meta.helm.sh/release-namespace"]; exists && releaseNs != release.Namespace {
			h.logger.WithFields(logrus.Fields{
				"resource":         resourceName,
				"currentNamespace": releaseNs,
				"targetNamespace":  release.Namespace,
			}).Info("Deleting ClusterRoleBinding with conflicting namespace annotation")

			if err := h.K8sClient.RbacV1().ClusterRoleBindings().Delete(ctx, resourceName, metav1.DeleteOptions{}); err != nil {
				return fmt.Errorf("failed to delete ClusterRoleBinding: %w", err)
			}
		}
	}

	return nil
}

// install performs a fresh Helm install
func (h *HelmInstaller) install(ctx context.Context, actionConfig *action.Configuration, release *HelmRelease, allowNameReuse bool) error {
	client := action.NewInstall(actionConfig)
	client.Namespace = release.Namespace
	client.ReleaseName = release.Name
	client.CreateNamespace = true
	client.Wait = true
	client.Timeout = 15 * time.Minute // Increased from 10 to 15 minutes for slow environments
	client.Version = release.Version
	client.SkipCRDs = release.SkipCRDs
	client.Replace = allowNameReuse
	client.Force = true // Force resource adoption to handle namespace mismatches

	// Determine chart reference for locating
	chartRef := release.Chart
	// If chart doesn't contain a repo prefix and we have a repo URL, add the prefix
	if !strings.Contains(release.Chart, "/") && release.Repo != "" {
		// Infer repo name from URL
		repoName := ""
		if strings.Contains(release.Repo, "opencost.github.io") {
			repoName = "opencost"
		} else if strings.Contains(release.Repo, "prometheus-community.github.io") {
			repoName = "prometheus-community"
		} else if strings.Contains(release.Repo, "grafana.github.io") {
			repoName = "grafana"
		}
		if repoName != "" {
			chartRef = fmt.Sprintf("%s/%s", repoName, release.Chart)
		}
	}

	// Locate chart
	chartPath, err := client.ChartPathOptions.LocateChart(chartRef, h.settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart: %w", err)
	}

	// Load chart
	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	// Install chart
	rel, err := client.RunWithContext(ctx, chart, release.Values)
	if err != nil {
		return fmt.Errorf("helm install failed: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"release": release.Name,
		"version": rel.Chart.Metadata.Version,
		"status":  rel.Info.Status,
	}).Info("Helm release installed successfully")

	return nil
}

// upgradeWithForce runs a Helm upgrade with force to recover stuck releases
func (h *HelmInstaller) upgradeWithForce(ctx context.Context, actionConfig *action.Configuration, release *HelmRelease) error {
	client := action.NewUpgrade(actionConfig)
	client.Namespace = release.Namespace
	client.Wait = true
	client.Timeout = 15 * time.Minute // Increased from 10 to 15 minutes for slow environments
	client.Version = release.Version
	client.Install = true
	client.Force = true
	client.ReuseValues = true

	// Determine chart reference for locating
	chartRef := release.Chart
	if !strings.Contains(release.Chart, "/") && release.Repo != "" {
		repoName := ""
		if strings.Contains(release.Repo, "opencost.github.io") {
			repoName = "opencost"
		} else if strings.Contains(release.Repo, "prometheus-community.github.io") {
			repoName = "prometheus-community"
		} else if strings.Contains(release.Repo, "grafana.github.io") {
			repoName = "grafana"
		}
		if repoName != "" {
			chartRef = fmt.Sprintf("%s/%s", repoName, release.Chart)
		}
	}

	chartPath, err := client.ChartPathOptions.LocateChart(chartRef, h.settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart: %w", err)
	}

	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	rel, err := client.RunWithContext(ctx, release.Name, chart, release.Values)
	if err != nil {
		return fmt.Errorf("helm forced upgrade failed: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"release": release.Name,
		"version": rel.Chart.Metadata.Version,
		"status":  rel.Info.Status,
	}).Info("Helm release upgraded with force successfully")

	return nil
}

// upgrade performs a Helm upgrade
func (h *HelmInstaller) upgrade(ctx context.Context, actionConfig *action.Configuration, release *HelmRelease) error {
	client := action.NewUpgrade(actionConfig)
	client.Namespace = release.Namespace
	client.Wait = true
	client.Timeout = 15 * time.Minute // Increased from 10 to 15 minutes for slow environments
	client.Version = release.Version

	// Determine chart reference for locating
	chartRef := release.Chart
	if !strings.Contains(release.Chart, "/") && release.Repo != "" {
		repoName := ""
		if strings.Contains(release.Repo, "opencost.github.io") {
			repoName = "opencost"
		} else if strings.Contains(release.Repo, "prometheus-community.github.io") {
			repoName = "prometheus-community"
		} else if strings.Contains(release.Repo, "grafana.github.io") {
			repoName = "grafana"
		}
		if repoName != "" {
			chartRef = fmt.Sprintf("%s/%s", repoName, release.Chart)
		}
	}

	// Locate chart
	chartPath, err := client.ChartPathOptions.LocateChart(chartRef, h.settings)
	if err != nil {
		return fmt.Errorf("failed to locate chart: %w", err)
	}

	// Load chart
	chart, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	// Upgrade chart
	rel, err := client.RunWithContext(ctx, release.Name, chart, release.Values)
	if err != nil {
		return fmt.Errorf("helm upgrade failed: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"release": release.Name,
		"version": rel.Chart.Metadata.Version,
		"status":  rel.Info.Status,
	}).Info("Helm release upgraded successfully")

	return nil
}

// Uninstall uninstalls a Helm release using the Helm SDK
func (h *HelmInstaller) Uninstall(ctx context.Context, name, namespace string) error {
	h.logger.WithFields(logrus.Fields{
		"release":   name,
		"namespace": namespace,
	}).Info("Uninstalling Helm release...")

	// Create action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, "secret", h.debugLog); err != nil {
		return fmt.Errorf("failed to initialize Helm action Config: %w", err)
	}

	// Uninstall release
	client := action.NewUninstall(actionConfig)
	client.Wait = true
	client.Timeout = 5 * time.Minute

	_, err := client.Run(name)
	if err != nil {
		return fmt.Errorf("helm uninstall failed: %w", err)
	}

	h.logger.WithField("release", name).Info("Helm release uninstalled successfully")
	return nil
}

// deleteFromHistory removes a failed Helm release from history
// This is useful for releases stuck in failed state with no deployed resources
func (h *HelmInstaller) deleteFromHistory(ctx context.Context, name, namespace string) error {
	h.logger.WithFields(logrus.Fields{
		"release":   name,
		"namespace": namespace,
	}).Debug("Removing Helm release from history...")

	// Create action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, "secret", h.debugLog); err != nil {
		return fmt.Errorf("failed to initialize Helm action Config: %w", err)
	}

	history, err := actionConfig.Releases.History(name)
	if err != nil {
		if errors.Is(err, driver.ErrReleaseNotFound) || strings.Contains(err.Error(), "not found") {
			h.logger.WithField("release", name).Debug("Release history already absent")
			return nil
		}
		return fmt.Errorf("failed to read release history: %w", err)
	}

	removedAny := false
	for _, rel := range history {
		if rel == nil {
			continue
		}

		if _, err := actionConfig.Releases.Delete(name, rel.Version); err != nil {
			if errors.Is(err, driver.ErrReleaseNotFound) || strings.Contains(err.Error(), "not found") {
				continue
			}
			return fmt.Errorf("failed to delete release revision %d: %w", rel.Version, err)
		}

		removedAny = true
		h.logger.WithFields(logrus.Fields{
			"release":  name,
			"revision": rel.Version,
		}).Debug("Removed Helm release revision from history")
	}

	if !removedAny {
		h.logger.WithField("release", name).Debug("No Helm release revisions found to remove")
	}

	return nil
}

// cleanupRelease attempts to uninstall a release and remove it from history
func (h *HelmInstaller) cleanupRelease(ctx context.Context, name, namespace string) error {
	uninstallErr := h.Uninstall(ctx, name, namespace)
	if uninstallErr != nil {
		// Ignore benign errors during uninstall
		errMsg := uninstallErr.Error()
		if !strings.Contains(errMsg, "not found") && !strings.Contains(errMsg, "no deployed releases") {
			h.logger.WithError(uninstallErr).Warn("Helm uninstall reported an error")
		}
	}

	if err := h.deleteFromHistory(ctx, name, namespace); err != nil {
		return err
	}

	return nil
}

// IsInstalled checks if a Helm release is installed using the Helm SDK
func (h *HelmInstaller) IsInstalled(ctx context.Context, name, namespace string) bool {
	// Create action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, "secret", h.debugLog); err != nil {
		return false
	}

	// Get release status
	client := action.NewGet(actionConfig)
	_, err := client.Run(name)
	return err == nil
}

// GetReleaseStatus gets the status of a Helm release using the Helm SDK
func (h *HelmInstaller) GetReleaseStatus(ctx context.Context, name, namespace string) (*releasepkg.Release, error) {
	// Create action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, "secret", h.debugLog); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action Config: %w", err)
	}

	// Get release
	client := action.NewGet(actionConfig)
	rel, err := client.Run(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get release status: %w", err)
	}

	return rel, nil
}

// createNamespace creates a Kubernetes namespace if it doesn't exist using the Kubernetes client
func (h *HelmInstaller) createNamespace(ctx context.Context, namespace string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err := h.K8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		// Ignore error if namespace already exists
		h.logger.WithError(err).Debug("Namespace may already exist")
		return nil
	}

	h.logger.WithField("namespace", namespace).Debug("Created namespace")
	return nil
}

// AddRepo adds a Helm repository using the Helm SDK
func (h *HelmInstaller) AddRepo(ctx context.Context, chartName, repoURL string) error {
	// Determine the repo name from the repo URL
	// For URLs like "https://opencost.github.io/opencost-helm-chart", use "opencost"
	// For URLs like "https://prometheus-community.github.io/helm-charts", use "prometheus-community"
	repoName := chartName

	// If chart name contains a slash, extract the repo prefix
	if idx := len(chartName) - 1; idx > 0 {
		for i := 0; i < len(chartName); i++ {
			if chartName[i] == '/' {
				repoName = chartName[:i]
				break
			}
		}
	} else {
		// No slash in chart name - infer repo name from URL
		// Extract the organization/project name from GitHub Pages URLs
		if strings.Contains(repoURL, "opencost.github.io") {
			repoName = "opencost"
		} else if strings.Contains(repoURL, "prometheus-community.github.io") {
			repoName = "prometheus-community"
		} else if strings.Contains(repoURL, "grafana.github.io") {
			repoName = "grafana"
		}
	}

	h.logger.WithFields(logrus.Fields{
		"repo_name": repoName,
		"repo_url":  repoURL,
	}).Debug("Adding Helm repository")

	// Load existing repository file
	repoFile := h.settings.RepositoryConfig
	var repoFileData *repo.File

	// Check if repo file exists
	if _, err := os.Stat(repoFile); err == nil {
		data, err := os.ReadFile(repoFile)
		if err != nil {
			return fmt.Errorf("failed to read repository file: %w", err)
		}
		repoFileData = &repo.File{}
		if err := yaml.Unmarshal(data, repoFileData); err != nil {
			return fmt.Errorf("failed to parse repository file: %w", err)
		}
	} else {
		// Create new repo file
		repoFileData = repo.NewFile()
	}

	// Check if repo already exists
	if repoFileData.Has(repoName) {
		h.logger.WithField("repo", repoName).Debug("Repository already exists")
		return nil
	}

	// Add repository entry
	entry := &repo.Entry{
		Name: repoName,
		URL:  repoURL,
	}

	// Create chart repository with proper initialization
	// Use all getter providers (http, https, etc.)
	getterProviders := getter.All(h.settings)
	chartRepo, err := repo.NewChartRepository(entry, getterProviders)
	if err != nil {
		return fmt.Errorf("failed to create chart repository: %w", err)
	}
	chartRepo.CachePath = h.settings.RepositoryCache

	h.logger.WithField("repo", repoName).Debug("Downloading repository index")
	var lastErr error
	backoff := 2 * time.Second
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := chartRepo.DownloadIndexFile(); err != nil {
			lastErr = err
			if attempt < 3 {
				h.logger.WithFields(logrus.Fields{
					"repo":    repoName,
					"attempt": attempt,
					"error":   err,
				}).Warn("Failed to download repository index; retrying")
				select {
				case <-ctx.Done():
					return fmt.Errorf("failed to download repository index: %w", ctx.Err())
				case <-time.After(backoff):
				}
				backoff *= 2
				continue
			}
			return fmt.Errorf("failed to download repository index: %w", lastErr)
		}
		lastErr = nil
		break
	}

	// Add to repo file
	repoFileData.Update(entry)

	// Save repository file
	data, err := yaml.Marshal(repoFileData)
	if err != nil {
		return fmt.Errorf("failed to marshal repository file: %w", err)
	}

	if err := os.WriteFile(repoFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write repository file: %w", err)
	}

	h.logger.WithFields(logrus.Fields{
		"repo": repoName,
		"url":  repoURL,
	}).Info("Helm repository added successfully")

	return nil
}

// debugLog is a helper for Helm SDK logging
func (h *HelmInstaller) debugLog(format string, v ...interface{}) {
	h.logger.Debugf(format, v...)
}
