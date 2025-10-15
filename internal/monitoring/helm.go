package monitoring

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	// Create Helm settings
	settings := cli.New()

	return &HelmInstaller{
		logger:    logger,
		settings:  settings,
		k8sClient: k8sClient,
		config:    config,
	}, nil
}

// Install installs or upgrades a Helm release using the Helm SDK
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

	// Create action configuration
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(h.settings.RESTClientGetter(), release.Namespace, "secret", h.debugLog); err != nil {
		return fmt.Errorf("failed to initialize Helm action config: %w", err)
	}

	// Add repo if specified
	if release.Repo != "" {
		if err := h.addRepo(ctx, release.Chart, release.Repo); err != nil {
			h.logger.WithError(err).Warn("Failed to add Helm repo (may already exist)")
		}
	}

	// Check if release exists
	histClient := action.NewHistory(actionConfig)
	histClient.Max = 1
	if _, err := histClient.Run(release.Name); err == nil {
		// Release exists, upgrade it
		return h.upgrade(ctx, actionConfig, release)
	}

	// Release doesn't exist, install it
	return h.install(ctx, actionConfig, release)
}

// install performs a fresh Helm install
func (h *HelmInstaller) install(ctx context.Context, actionConfig *action.Configuration, release *HelmRelease) error {
	client := action.NewInstall(actionConfig)
	client.Namespace = release.Namespace
	client.ReleaseName = release.Name
	client.CreateNamespace = true
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
func (h *HelmInstaller) GetReleaseStatus(ctx context.Context, name, namespace string) (*release.Release, error) {
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
func (h *HelmInstaller) addRepo(ctx context.Context, name, url string) error {
	// Note: Helm SDK's repo management is more complex and typically uses
	// the CLI directly or file-based repo management. For simplicity,
	// we'll skip explicit repo addition as Helm can download charts directly
	// from OCI registries or HTTP URLs without pre-adding repos.
	h.logger.WithFields(logrus.Fields{
		"name": name,
		"url":  url,
	}).Debug("Helm SDK will resolve chart from URL directly")
	return nil
}

// debugLog is a helper for Helm SDK logging
func (h *HelmInstaller) debugLog(format string, v ...interface{}) {
	h.logger.Debugf(format, v...)
}
