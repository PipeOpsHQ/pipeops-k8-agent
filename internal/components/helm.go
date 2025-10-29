package components

import (
	"context"
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

// HelmInstaller manages Helm chart installations using Helm SDK
type HelmInstaller struct {
	logger    *logrus.Logger
	settings  *cli.EnvSettings
	k8sClient *kubernetes.Clientset
	config    *rest.Config
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

// NewHelmInstaller creates a new Helm installer using the Helm SDK
func NewHelmInstaller(logger *logrus.Logger) (*HelmInstaller, error) {
	// Create in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
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
		k8sClient: k8sClient,
		config:    config,
	}, nil
}

// Install installs or upgrades a Helm release using the Helm SDK
func (h *HelmInstaller) Install(ctx context.Context, release *HelmRelease) error {
	// Create action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.settings.RESTClientGetter(), release.Namespace, "secret", h.debugLog); err != nil {
		return fmt.Errorf("failed to initialize Helm action config: %w", err)
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

				// Try to delete the release from Helm history using Delete action.
				// This handles cases where the release has no deployed resources.
				if deleteErr := h.deleteFromHistory(ctx, release.Name, release.Namespace); deleteErr != nil {
					h.logger.WithError(deleteErr).Warn("Failed to delete release from history; attempting fresh install anyway")
				} else {
					h.logger.WithField("release", release.Name).Info("Release removed from Helm history")
				}

				// Proceed with a fresh install to ensure a clean state.
				h.logger.WithField("release", release.Name).Info("Installing Helm release after cleanup")

				if release.Repo != "" {
					if err := h.addRepo(ctx, release.Chart, release.Repo); err != nil {
						h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
					}
				}

				if err := h.createNamespace(ctx, release.Namespace); err != nil {
					return fmt.Errorf("failed to create namespace: %w", err)
				}

				return h.install(ctx, actionConfig, release, true)
			}

			h.logger.WithFields(logrus.Fields{
				"release":           release.Name,
				"installed_version": existingVersion,
				"requested_version": release.Version,
				"status":            existingStatus,
			}).Info("Upgrading Helm release")
		}

		if release.Repo != "" {
			if err := h.addRepo(ctx, release.Chart, release.Repo); err != nil {
				h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
			}
		}

		if err := h.createNamespace(ctx, release.Namespace); err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}

		return h.upgrade(ctx, actionConfig, release)
	}

	if release.Repo != "" {
		if err := h.addRepo(ctx, release.Chart, release.Repo); err != nil {
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

	return h.install(ctx, actionConfig, release, false)
}

// install performs a fresh Helm install
func (h *HelmInstaller) install(ctx context.Context, actionConfig *action.Configuration, release *HelmRelease, allowNameReuse bool) error {
	client := action.NewInstall(actionConfig)
	client.Namespace = release.Namespace
	client.ReleaseName = release.Name
	client.CreateNamespace = true
	client.Wait = true
	client.Timeout = 10 * time.Minute
	client.Version = release.Version
	client.SkipCRDs = true // Skip CRDs - they may be pre-installed by installer script
	client.Replace = allowNameReuse

	// Locate chart
	chartPath, err := client.ChartPathOptions.LocateChart(release.Chart, h.settings)
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

// upgrade performs a Helm upgrade
func (h *HelmInstaller) upgrade(ctx context.Context, actionConfig *action.Configuration, release *HelmRelease) error {
	client := action.NewUpgrade(actionConfig)
	client.Namespace = release.Namespace
	client.Wait = true
	client.Timeout = 10 * time.Minute
	client.Version = release.Version

	// Locate chart
	chartPath, err := client.ChartPathOptions.LocateChart(release.Chart, h.settings)
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
		return fmt.Errorf("failed to initialize Helm action config: %w", err)
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
		return fmt.Errorf("failed to initialize Helm action config: %w", err)
	}

	// Use Uninstall with KeepHistory=false to completely remove from history
	client := action.NewUninstall(actionConfig)
	client.KeepHistory = false
	client.Wait = false // Don't wait since there might be no resources to delete
	client.Timeout = 30 * time.Second

	_, err := client.Run(name)
	if err != nil {
		// Ignore "not found" errors
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no deployed releases") {
			h.logger.WithField("release", name).Debug("Release already removed or not found")
			return nil
		}
		return fmt.Errorf("failed to delete release from history: %w", err)
	}

	h.logger.WithField("release", name).Debug("Release removed from Helm history")
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
		return nil, fmt.Errorf("failed to initialize Helm action config: %w", err)
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

	_, err := h.k8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		// Ignore error if namespace already exists
		h.logger.WithError(err).Debug("Namespace may already exist")
		return nil
	}

	h.logger.WithField("namespace", namespace).Debug("Created namespace")
	return nil
}

// addRepo adds a Helm repository using the Helm SDK
func (h *HelmInstaller) addRepo(ctx context.Context, chartName, repoURL string) error {
	// Extract repo name from chart name (e.g., "prometheus-community/prometheus" -> "prometheus-community")
	repoName := chartName
	if idx := len(chartName) - 1; idx > 0 {
		for i := 0; i < len(chartName); i++ {
			if chartName[i] == '/' {
				repoName = chartName[:i]
				break
			}
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
	if _, err := chartRepo.DownloadIndexFile(); err != nil {
		return fmt.Errorf("failed to download repository index: %w", err)
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
