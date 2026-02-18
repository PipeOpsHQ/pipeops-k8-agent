package components

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/helm"
	"github.com/pipeops/pipeops-vm-agent/internal/ingress"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/kubernetes"
)

// MonitoringStack represents the monitoring tools installed on the cluster
type MonitoringStack struct {
	Prometheus  *PrometheusConfig
	Loki        *LokiConfig
	Grafana     *GrafanaConfig
	CertManager *CertManagerConfig
}

// PrometheusConfig holds Prometheus configuration
type PrometheusConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Namespace         string `yaml:"namespace"`
	ReleaseName       string `yaml:"release_name"`
	ChartRepo         string `yaml:"chart_repo"`
	ChartName         string `yaml:"chart_name"`
	ChartVersion      string `yaml:"chart_version"`
	LocalPort         int    `yaml:"local_port"`
	RemotePort        int    `yaml:"remote_port"`
	Username          string `yaml:"username"`
	Password          string `yaml:"password"`
	SSL               bool   `yaml:"ssl"`
	StorageClass      string `yaml:"storage_class"`
	StorageSize       string `yaml:"storage_size"`
	RetentionPeriod   string `yaml:"retention_period"`
	EnablePersistence bool   `yaml:"enable_persistence"`
}

// LokiConfig holds Loki configuration
type LokiConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Namespace         string `yaml:"namespace"`
	ReleaseName       string `yaml:"release_name"`
	ChartRepo         string `yaml:"chart_repo"`
	ChartName         string `yaml:"chart_name"`
	ChartVersion      string `yaml:"chart_version"`
	LocalPort         int    `yaml:"local_port"`
	RemotePort        int    `yaml:"remote_port"`
	Username          string `yaml:"username"`
	Password          string `yaml:"password"`
	SSL               bool   `yaml:"ssl"`
	StorageClass      string `yaml:"storage_class"`
	StorageSize       string `yaml:"storage_size"`
	EnablePersistence bool   `yaml:"enable_persistence"`
}

// prometheusCacheEntry caches Prometheus service discovery to avoid repetitive logs
type prometheusCacheEntry struct {
	serviceName string
	port        int32
	found       bool
	timestamp   time.Time
}

// GrafanaConfig holds Grafana configuration
type GrafanaConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Namespace         string `yaml:"namespace"`
	ReleaseName       string `yaml:"release_name"`
	ChartRepo         string `yaml:"chart_repo"`
	ChartName         string `yaml:"chart_name"`
	ChartVersion      string `yaml:"chart_version"`
	LocalPort         int    `yaml:"local_port"`
	RemotePort        int    `yaml:"remote_port"`
	AdminUser         string `yaml:"admin_user"`
	AdminPassword     string `yaml:"admin_password"`
	SSL               bool   `yaml:"ssl"`
	StorageClass      string `yaml:"storage_class"`
	StorageSize       string `yaml:"storage_size"`
	EnablePersistence bool   `yaml:"enable_persistence"`
	RootURL           string `yaml:"root_url"`
	ServeFromSubPath  bool   `yaml:"serve_from_sub_path"`
}

// CertManagerConfig holds cert-manager configuration
type CertManagerConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Namespace    string `yaml:"namespace"`
	ReleaseName  string `yaml:"release_name"`
	ChartRepo    string `yaml:"chart_repo"`
	ChartName    string `yaml:"chart_name"`
	ChartVersion string `yaml:"chart_version"`
	InstallCRDs  bool   `yaml:"install_crds"`
}

// Manager manages the monitoring stack lifecycle
type Manager struct {
	stack              *MonitoringStack
	logger             *logrus.Logger
	ctx                context.Context
	cancel             context.CancelFunc
	installer          *helm.HelmInstaller
	ingressController  *ingress.IngressController
	traefikController  *ingress.TraefikController
	componentInstaller *ComponentInstaller
	ingressEnabled     bool
	prometheusCache    *prometheusCacheEntry
}

// NewManager creates a new monitoring stack manager
func NewManager(stack *MonitoringStack, logger *logrus.Logger) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	installer, err := helm.NewHelmInstaller(logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create helm installer: %w", err)
	}

	// Create ingress controller using installer's k8s client
	ingressController := ingress.NewIngressController(installer, installer.K8sClient, logger)

	// Create Traefik controller
	traefikController := ingress.NewTraefikController(installer, logger)

	// Create component installer for essential Kubernetes components
	componentInstaller := NewComponentInstaller(installer, installer.K8sClient, logger)

	ingressEnabled := determineIngressPreference(installer.K8sClient, logger)

	return &Manager{
		stack:              stack,
		logger:             logger,
		ctx:                ctx,
		cancel:             cancel,
		installer:          installer,
		ingressController:  ingressController,
		traefikController:  traefikController,
		componentInstaller: componentInstaller,
		ingressEnabled:     ingressEnabled,
	}, nil
}

// Start installs and configures the monitoring stack
func (m *Manager) Start() error {
	m.logger.Info("Starting monitoring stack manager...")

	// 1. Detect Cluster Resources & Profile
	capacity, err := detectClusterProfile(m.ctx, m.installer.K8sClient, m.logger)
	profile := types.ProfileMedium // Default fallback
	if err != nil {
		m.logger.WithError(err).Warn("Failed to detect cluster resources, defaulting to Medium profile")
	} else {
		profile = capacity.Profile
	}

	// 2. Install Essential Components (Metrics Server, VPA)
	// These are critical and lightweight, so we install them first.
	if err := m.componentInstaller.InstallEssentialComponents(m.ctx); err != nil {
		m.logger.WithError(err).Warn("Some essential components failed to install (non-fatal)")
	}
	// Verify Metrics API availability
	if err := m.componentInstaller.VerifyMetricsAPI(m.ctx); err != nil {
		m.logger.WithError(err).Warn("Metrics API verification failed (non-fatal)")
	}

	m.prepareMonitoringDefaults()

	// 3. Build Installation Plan
	var steps []InstallStep

	// Cert Manager
	if m.stack.CertManager != nil && m.stack.CertManager.Enabled {
		steps = append(steps, InstallStep{
			Name:        "cert-manager",
			Namespace:   m.stack.CertManager.Namespace,
			ReleaseName: m.stack.CertManager.ReleaseName,
			Critical:    false,
			InstallFunc: func() error { return m.installCertManager(profile) },
		})
	}

	// Ingress Controller (Traefik)
	// We are replacing NGINX with Traefik.
	// 1. Check if NGINX is installed. If so, uninstall it to prevent conflicts.
	// 2. Install Traefik.
	if m.ingressEnabled {
		steps = append(steps, InstallStep{
			Name:        "ingress-controller",
			Namespace:   "pipeops-system", // Target namespace
			ReleaseName: "traefik",
			Critical:    false, // Non-critical: failure should not prevent Prometheus/Loki from being attempted
			InstallFunc: func() error {
				// Migration 1: Remove NGINX if present
				if m.ingressController.IsInstalled() {
					m.logger.Warn("Detected NGINX Ingress Controller. Uninstalling it to migrate to Traefik...")
					// We don't have a clean uninstall method exposed on IngressController easily without duplicating logic,
					// but HelmInstaller can do it.
					// Since IngressController hardcodes chart details, we'll use HelmInstaller directly here for safety.
					if err := m.installer.Uninstall(m.ctx, "ingress-nginx", "ingress-nginx"); err != nil {
						m.logger.WithError(err).Warn("Failed to uninstall NGINX Ingress Controller (will attempt to proceed)")
					} else {
						m.logger.Info("✓ NGINX Ingress Controller uninstalled")
						// Wait for port release
						m.waitForStabilization(10 * time.Second)
					}
				}

				// Migration 2: Move Traefik from kube-system to pipeops-system if found there
				// Note: IsInstalled updates the controller's internal namespace to where it found it
				if m.traefikController.IsInstalled(m.ctx) {
					currentNs := m.traefikController.GetInstalledNamespace()
					if currentNs != "pipeops-system" {
						m.logger.WithField("namespace", currentNs).Info("Detected Traefik in legacy/system namespace. Uninstalling to migrate to pipeops-system...")
						if err := m.traefikController.Uninstall(m.ctx); err != nil {
							m.logger.WithError(err).Warn("Failed to uninstall legacy Traefik (will attempt to install new one anyway)")
						} else {
							m.logger.Info("✓ Legacy Traefik uninstalled")
							m.waitForStabilization(10 * time.Second)
						}
					}
				}

				// Ensure we install in the correct target namespace
				m.traefikController.SetNamespace("pipeops-system")

				// Install/Upgrade Traefik
				m.logger.Info("Installing/Upgrading Traefik Ingress Controller...")
				return m.traefikController.Install(m.ctx, profile)
			},
		})
	} else if m.traefikController.IsInstalled(m.ctx) {
		m.logger.Info("✓ Traefik Ingress Controller already installed (managed externally or previously installed)")
	} else if m.ingressController.IsInstalled() {
		// NGINX is installed but ingressEnabled was false (likely due to detection logic in determineIngressPreference)
		// We should probably leave it alone if the user configured it that way,
		// BUT if we want to enforce Traefik, we might want to warn or migrate.
		// For now, if ingressEnabled is false, we respect the decision to not install our ingress.
		m.logger.Info("✓ NGINX Ingress Controller detected (managed externally)")
	} else {
		m.logger.Warn("Ingress Controller installation skipped and no existing controller detected. External routing may fail.")
	}

	// Prometheus (Core Monitoring)
	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		steps = append(steps, InstallStep{
			Name:        "prometheus",
			Namespace:   m.stack.Prometheus.Namespace,
			ReleaseName: m.stack.Prometheus.ReleaseName,
			Critical:    false, // Non-critical: failure should not prevent Loki from being attempted
			InstallFunc: func() error {
				// Create ingress after install (or during if helm supported, but we do it manually)
				defer func() {
					if m.ingressEnabled {
						m.createPrometheusIngress()
						// Create Grafana ingress (Grafana is part of Prometheus stack)
						if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
							m.createGrafanaIngress()
						}
					}
				}()
				return m.installPrometheus(profile)
			},
		})
	}

	// Loki (Logging)
	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		steps = append(steps, InstallStep{
			Name:        "loki",
			Namespace:   m.stack.Loki.Namespace,
			ReleaseName: m.stack.Loki.ReleaseName,
			Critical:    false,
			InstallFunc: func() error {
				defer func() {
					if m.ingressEnabled {
						m.createLokiIngress()
					}
				}()
				return m.installLoki(profile)
			},
		})
	}

	// 4. Execute Plan
	if err := m.executeInstallationPlan(profile, steps); err != nil {
		return err
	}

	m.logger.Info("Monitoring stack started successfully")
	return nil
}

// waitForStabilization waits for the cluster to stabilize after an installation
func (m *Manager) waitForStabilization(duration time.Duration) {
	m.logger.WithField("duration", duration).Info("Waiting for cluster stabilization...")
	time.Sleep(duration)
}

func (m *Manager) prepareMonitoringDefaults() {
	if m.stack == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	storageClass, err := m.detectDefaultStorageClass(ctx)
	if err != nil {
		m.logger.WithError(err).Debug("Unable to detect default storage class")
		storageClass = ""
	}

	// Enable volume expansion on default storage class if possible
	if storageClass != "" {
		m.enableStorageClassExpansion(ctx, storageClass)
	}

	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled && m.stack.Prometheus.EnablePersistence {
		if m.stack.Prometheus.StorageClass == "" {
			if storageClass != "" {
				m.logger.WithField("storageClass", storageClass).Info("Using default storage class for Prometheus persistence")
				m.stack.Prometheus.StorageClass = storageClass
			} else {
				m.logger.Warn("No default storage class detected; disabling Prometheus persistence")
				m.stack.Prometheus.EnablePersistence = false
			}
		}

		// Enable volume expansion on selected storage class
		if m.stack.Prometheus.StorageClass != "" {
			m.enableStorageClassExpansion(ctx, m.stack.Prometheus.StorageClass)
		}
	}

	if m.stack.Grafana != nil && m.stack.Grafana.Enabled && m.stack.Grafana.EnablePersistence {
		if m.stack.Grafana.StorageClass == "" {
			if storageClass != "" {
				m.logger.WithField("storageClass", storageClass).Info("Using default storage class for Grafana persistence")
				m.stack.Grafana.StorageClass = storageClass
			} else {
				m.logger.Warn("No default storage class detected; disabling Grafana persistence")
				m.stack.Grafana.EnablePersistence = false
			}
		}

		// Enable volume expansion on selected storage class
		if m.stack.Grafana.StorageClass != "" {
			m.enableStorageClassExpansion(ctx, m.stack.Grafana.StorageClass)
		}
	}

	if m.stack.Loki != nil && m.stack.Loki.Enabled && m.stack.Loki.EnablePersistence {
		if m.stack.Loki.StorageClass == "" {
			if storageClass != "" {
				m.logger.WithField("storageClass", storageClass).Info("Using default storage class for Loki persistence")
				m.stack.Loki.StorageClass = storageClass
			} else {
				m.logger.Warn("No default storage class detected; disabling Loki persistence")
				m.stack.Loki.EnablePersistence = false
			}
		}

		// Enable volume expansion on selected storage class
		if m.stack.Loki.StorageClass != "" {
			m.enableStorageClassExpansion(ctx, m.stack.Loki.StorageClass)
		}
	}
}

func (m *Manager) detectDefaultStorageClass(ctx context.Context) (string, error) {
	if m.installer == nil || m.installer.K8sClient == nil {
		return "", fmt.Errorf("kubernetes client unavailable")
	}

	classes, err := m.installer.K8sClient.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	for _, class := range classes.Items {
		if isDefaultStorageClass(&class) {
			return class.Name, nil
		}
	}

	return "", nil
}

func (m *Manager) enableStorageClassExpansion(ctx context.Context, name string) {
	if m.installer == nil || m.installer.K8sClient == nil {
		return
	}

	class, err := m.installer.K8sClient.StorageV1().StorageClasses().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		m.logger.WithError(err).WithField("storageClass", name).Debug("Unable to get storage class for expansion check")
		return
	}

	if class.AllowVolumeExpansion == nil || !*class.AllowVolumeExpansion {
		m.logger.WithField("storageClass", name).Info("Enabling volume expansion for storage class")

		trueVal := true
		class.AllowVolumeExpansion = &trueVal

		_, err := m.installer.K8sClient.StorageV1().StorageClasses().Update(ctx, class, metav1.UpdateOptions{})
		if err != nil {
			m.logger.WithError(err).WithField("storageClass", name).Warn("Failed to enable volume expansion for storage class")
		} else {
			m.logger.WithField("storageClass", name).Info("✓ Successfully enabled volume expansion for storage class")
		}
	}
}

func isDefaultStorageClass(class *storagev1.StorageClass) bool {
	if class == nil || class.Annotations == nil {
		return false
	}

	if class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
		return true
	}

	if class.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
		return true
	}

	return false
}

func determineIngressPreference(client *kubernetes.Clientset, logger *logrus.Logger) bool {
	if client == nil {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check for existing default IngressClass
	classes, err := client.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, class := range classes.Items {
			if isDefaultIngressClass(&class) {
				// If it's NGINX or Traefik, we might want to take it over, so we don't automatically skip.
				// But if it's something else (e.g. AWS ALB, GCE), we should respect it.
				if class.Spec.Controller != "k8s.io/ingress-nginx" &&
					class.Spec.Controller != "traefik.io/ingress-controller" &&
					class.Spec.Controller != "rancher.io/traefik" {
					logger.WithFields(logrus.Fields{
						"ingressClass": class.Name,
						"controller":   class.Spec.Controller,
					}).Info("Detected third-party default ingress class; skipping bundled ingress controller")
					return false
				}
			}
		}
	} else if !apierrors.IsNotFound(err) {
		logger.WithError(err).Debug("Unable to list ingress classes")
	}

	// We used to check for specific deployments here, but we now want to potentially
	// replace NGINX or update Traefik, so we don't return false just because they exist.
	// The Start() method handles the logic of uninstalling NGINX or upgrading Traefik.

	return true
}

func hasDeployment(ctx context.Context, client *kubernetes.Clientset, namespace, name string) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("nil kubernetes client")
	}

	_, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func isDefaultIngressClass(class *networkingv1.IngressClass) bool {
	if class == nil || class.Annotations == nil {
		return false
	}

	if class.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
		return true
	}

	return false
}

// Stop stops the monitoring stack manager
func (m *Manager) Stop() error {
	m.logger.Info("Stopping monitoring stack manager...")
	m.cancel()
	return nil
}

// helmTimeout returns a profile-aware timeout for Helm install/upgrade operations.
// Low-profile clusters get a shorter timeout to avoid stalling the pipeline when
// resources are too scarce for a chart to converge.
func helmTimeout(profile types.ResourceProfile) time.Duration {
	if profile == types.ProfileLow {
		return 10 * time.Minute
	}
	return 15 * time.Minute
}

// GetTunnelForwards returns the tunnel forwards configuration for monitoring stack
// Returns Kubernetes service DNS names (since agent runs in-cluster)
func (m *Manager) GetTunnelForwards() []TunnelForward {
	forwards := []TunnelForward{}

	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		// For kube-prometheus-stack, the service name is "<release-name>-prometheus"
		serviceName := fmt.Sprintf("%s-prometheus", m.stack.Prometheus.ReleaseName)
		forwards = append(forwards, TunnelForward{
			Name: "prometheus",
			// Use Kubernetes service DNS (accessible from agent pod in-cluster)
			LocalAddr: fmt.Sprintf("%s.%s.svc.cluster.local:%d",
				serviceName,
				m.stack.Prometheus.Namespace,
				m.stack.Prometheus.LocalPort),
			RemotePort: m.stack.Prometheus.RemotePort,
		})
	}

	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		forwards = append(forwards, TunnelForward{
			Name: "loki",
			LocalAddr: fmt.Sprintf("%s.%s.svc.cluster.local:%d",
				m.stack.Loki.ReleaseName,
				m.stack.Loki.Namespace,
				m.stack.Loki.LocalPort),
			RemotePort: m.stack.Loki.RemotePort,
		})
	}

	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		// For kube-prometheus-stack, Grafana service name is "<release-name>-grafana"
		serviceName := fmt.Sprintf("%s-grafana", m.stack.Prometheus.ReleaseName)
		forwards = append(forwards, TunnelForward{
			Name: "grafana",
			LocalAddr: fmt.Sprintf("%s.%s.svc.cluster.local:%d",
				serviceName,
				m.stack.Grafana.Namespace,
				m.stack.Grafana.LocalPort),
			RemotePort: m.stack.Grafana.RemotePort,
		})
	}

	return forwards
}

// TunnelForward represents a tunnel forward configuration
type TunnelForward struct {
	Name       string
	LocalAddr  string
	RemotePort int
}

// discoverPrometheusService attempts to find the actual Prometheus service name
// This helps handle different deployments (kube-prometheus-stack, prometheus-operator, etc.)
func (m *Manager) discoverPrometheusService(namespace string) (serviceName string, port int32, found bool) {
	if m.installer == nil || m.installer.K8sClient == nil {
		return "", 0, false
	}

	// Common Prometheus service name patterns
	servicePatterns := []string{
		fmt.Sprintf("%s-prometheus", m.stack.Prometheus.ReleaseName), // kube-prometheus-stack pattern
		"prometheus-server",            // Legacy prometheus chart
		"prometheus-k8s",               // prometheus-operator pattern
		"prometheus-operated",          // Operated by prometheus-operator
		"prometheus",                   // Simple prometheus
		m.stack.Prometheus.ReleaseName, // Exact release name
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Retry loop for service discovery
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.WithFields(logrus.Fields{
				"namespace": namespace,
				"patterns":  servicePatterns,
			}).Warn("⚠️  Could not discover Prometheus service after retries, falling back to configured name")
			return "", 0, false
		case <-ticker.C:
			for _, pattern := range servicePatterns {
				svc, err := m.installer.K8sClient.CoreV1().Services(namespace).Get(ctx, pattern, metav1.GetOptions{})
				if err == nil {
					// Found the service, extract the port
					for _, p := range svc.Spec.Ports {
						// Look for common Prometheus port names
						if p.Name == "http-web" || p.Name == "web" || p.Name == "http" || p.Port == 9090 {
							m.logger.WithFields(logrus.Fields{
								"service":   pattern,
								"namespace": namespace,
								"port":      p.Port,
							}).Debug("Discovered Prometheus service")
							return pattern, p.Port, true
						}
					}
					// If no specific port found, use the first one
					if len(svc.Spec.Ports) > 0 {
						m.logger.WithFields(logrus.Fields{
							"service":   pattern,
							"namespace": namespace,
							"port":      svc.Spec.Ports[0].Port,
						}).Debug("Discovered Prometheus service (using first port)")
						return pattern, svc.Spec.Ports[0].Port, true
					}
				}
			}
		}
	}
}

// GetMonitoringInfo returns monitoring information for registration
func (m *Manager) GetMonitoringInfo() map[string]interface{} {
	info := make(map[string]interface{})

	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		var serviceName string
		var port int32
		var found bool

		// Check cache first (valid for 5 minutes)
		if m.prometheusCache != nil && time.Since(m.prometheusCache.timestamp) < 5*time.Minute {
			serviceName = m.prometheusCache.serviceName
			port = m.prometheusCache.port
			found = m.prometheusCache.found
		} else {
			// Try to discover the actual service name (handles K3s, different deployments, etc.)
			serviceName, port, found = m.discoverPrometheusService(m.stack.Prometheus.Namespace)

			// Cache the result
			m.prometheusCache = &prometheusCacheEntry{
				serviceName: serviceName,
				port:        port,
				found:       found,
				timestamp:   time.Now(),
			}
		}

		if !found {
			// Fall back to expected kube-prometheus-stack naming
			serviceName = fmt.Sprintf("%s-prometheus", m.stack.Prometheus.ReleaseName)
			port = int32(m.stack.Prometheus.LocalPort)
			if m.prometheusCache == nil || time.Since(m.prometheusCache.timestamp) > 5*time.Minute {
				m.logger.WithFields(logrus.Fields{
					"serviceName": serviceName,
					"namespace":   m.stack.Prometheus.Namespace,
				}).Warn("Using default Prometheus service name (discovery failed)")
			}
		}

		info["prometheus_url"] = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
			serviceName, m.stack.Prometheus.Namespace, port)
		// Also provide structured data for Kubernetes API proxy construction
		info["prometheus_service_name"] = serviceName
		info["prometheus_namespace"] = m.stack.Prometheus.Namespace
		info["prometheus_port"] = port
		info["prometheus_username"] = m.stack.Prometheus.Username
		info["prometheus_password"] = m.stack.Prometheus.Password
		info["prometheus_ssl"] = m.stack.Prometheus.SSL
	}

	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		// Loki service name is the release name
		info["loki_url"] = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
			m.stack.Loki.ReleaseName, m.stack.Loki.Namespace, m.stack.Loki.LocalPort)
		// Also provide structured data for Kubernetes API proxy construction
		info["loki_service_name"] = m.stack.Loki.ReleaseName
		info["loki_namespace"] = m.stack.Loki.Namespace
		info["loki_port"] = m.stack.Loki.LocalPort
		info["loki_username"] = m.stack.Loki.Username
		info["loki_password"] = m.stack.Loki.Password
		info["loki_ssl"] = m.stack.Loki.SSL
	}

	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		// For kube-prometheus-stack, Grafana service name is "<release-name>-grafana"
		serviceName := fmt.Sprintf("%s-grafana", m.stack.Prometheus.ReleaseName)
		info["grafana_url"] = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
			serviceName, m.stack.Grafana.Namespace, m.stack.Grafana.LocalPort)
		// Also provide structured data for Kubernetes API proxy construction
		info["grafana_service_name"] = serviceName
		info["grafana_namespace"] = m.stack.Grafana.Namespace
		info["grafana_port"] = m.stack.Grafana.LocalPort
		info["grafana_username"] = m.stack.Grafana.AdminUser
		info["grafana_password"] = m.stack.Grafana.AdminPassword
		info["grafana_ssl"] = m.stack.Grafana.SSL
	}

	return info
}

// HealthCheck checks the health of all monitoring components
func (m *Manager) HealthCheck() map[string]bool {
	health := make(map[string]bool)

	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		health["prometheus"] = m.checkPrometheusHealth()
	}

	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		health["loki"] = m.checkLokiHealth()
	}

	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		health["grafana"] = m.checkGrafanaHealth()
	}

	return health
}

// installPrometheusCRDs installs Prometheus Operator CRDs from the official kube-prometheus project
func (m *Manager) installPrometheusCRDs() error {
	m.logger.Info("Installing Prometheus Operator CRDs...")

	// Create apiextensions client for managing CRDs
	apiextensionsClient, err := apiextensionsclientset.NewForConfig(m.installer.Config)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	// Get the kube-prometheus-stack chart version to determine CRD version
	// For now, use a stable version URL
	crdBaseURL := "https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/v0.70.0/example/prometheus-operator-crd"

	crdFiles := []string{
		"monitoring.coreos.com_alertmanagerconfigs.yaml",
		"monitoring.coreos.com_alertmanagers.yaml",
		"monitoring.coreos.com_podmonitors.yaml",
		"monitoring.coreos.com_probes.yaml",
		"monitoring.coreos.com_prometheusagents.yaml",
		"monitoring.coreos.com_prometheuses.yaml",
		"monitoring.coreos.com_prometheusrules.yaml",
		"monitoring.coreos.com_scrapeconfigs.yaml",
		"monitoring.coreos.com_servicemonitors.yaml",
		"monitoring.coreos.com_thanosrulers.yaml",
	}

	successCount := 0
	skipCount := 0
	failCount := 0
	var lastError error

	// HTTP client for downloading CRD manifests
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Use Kubernetes API client to create CRDs (not apply, to avoid annotation bloat)
	for _, crdFile := range crdFiles {
		crdURL := fmt.Sprintf("%s/%s", crdBaseURL, crdFile)
		crdName := getCRDNameFromFile(crdFile)

		m.logger.WithField("crd", crdName).Info("Installing CRD")

		// First, check if CRD already exists
		_, err := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(m.ctx, crdName, metav1.GetOptions{})
		if err == nil {
			// CRD already exists, skip it
			skipCount++
			m.logger.WithField("crd", crdName).Info("CRD already exists, skipping")
			continue
		} else if !apierrors.IsNotFound(err) {
			// Unexpected error checking for CRD
			failCount++
			lastError = fmt.Errorf("failed to check if CRD %s exists: %w", crdName, err)
			m.logger.WithError(lastError).Error("Failed to check CRD existence")
			continue
		}

		// Download CRD manifest
		resp, err := httpClient.Get(crdURL)
		if err != nil {
			failCount++
			lastError = fmt.Errorf("failed to download CRD %s from %s: %w", crdName, crdURL, err)
			m.logger.WithError(lastError).Error("Failed to download CRD")
			continue
		}

		// Read response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			failCount++
			lastError = fmt.Errorf("failed to read CRD %s content: %w", crdName, err)
			m.logger.WithError(lastError).Error("Failed to read CRD content")
			continue
		}

		// Parse YAML to CRD object
		decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
		obj := &apiextensionsv1.CustomResourceDefinition{}
		_, _, err = decoder.Decode(body, nil, obj)
		if err != nil {
			failCount++
			lastError = fmt.Errorf("failed to decode CRD %s YAML: %w", crdName, err)
			m.logger.WithError(lastError).Error("Failed to decode CRD YAML")
			continue
		}

		// Create the CRD
		_, err = apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(m.ctx, obj, metav1.CreateOptions{})
		if err != nil {
			// Check if it's an "already exists" error (race condition)
			if apierrors.IsAlreadyExists(err) {
				skipCount++
				m.logger.WithField("crd", crdName).Info("CRD already exists (race condition), skipping")
				continue
			}

			// Check if it's the "Too long" annotation error
			if apierrors.IsInvalid(err) && contains(err.Error(), "Too long") {
				m.logger.WithField("crd", crdName).Warn("CRD has corrupted annotations, attempting cleanup...")

				// Delete the corrupted CRD and recreate it
				deleteTimeout := int64(30)
				deleteErr := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Delete(m.ctx, crdName, metav1.DeleteOptions{
					GracePeriodSeconds: &deleteTimeout,
				})
				if deleteErr != nil {
					failCount++
					lastError = fmt.Errorf("failed to delete corrupted CRD %s: %w", crdName, deleteErr)
					m.logger.WithError(lastError).Error("Failed to delete corrupted CRD")
					continue
				}

				// Wait a bit for deletion to complete
				time.Sleep(2 * time.Second)

				// Retry creation
				_, retryErr := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(m.ctx, obj, metav1.CreateOptions{})
				if retryErr != nil {
					failCount++
					lastError = fmt.Errorf("failed to recreate CRD %s: %w", crdName, retryErr)
					m.logger.WithError(lastError).Error("Failed to recreate CRD after cleanup")
					continue
				}
				successCount++
				m.logger.WithField("crd", crdName).Info("CRD recreated successfully after cleanup")
				continue
			}

			failCount++
			lastError = fmt.Errorf("failed to create CRD %s: %w", crdName, err)
			m.logger.WithError(lastError).WithFields(logrus.Fields{
				"crd": crdName,
				"url": crdURL,
			}).Error("Failed to create CRD")
			// Continue with other CRDs even if one fails
			continue
		}
		successCount++
		m.logger.WithField("crd", crdName).Info("CRD created successfully")
	}

	m.logger.WithFields(logrus.Fields{
		"created": successCount,
		"skipped": skipCount,
		"failed":  failCount,
		"total":   len(crdFiles),
	}).Info("Prometheus Operator CRD installation complete")

	if failCount > 0 {
		return fmt.Errorf("failed to install %d out of %d CRDs (last error: %w)", failCount, len(crdFiles), lastError)
	}

	m.logger.Info("✓ All Prometheus Operator CRDs ready")
	return nil
}

// getCRDNameFromFile extracts the CRD name from the filename
// e.g., "monitoring.coreos.com_alertmanagers.yaml" -> "alertmanagers.monitoring.coreos.com"
func getCRDNameFromFile(filename string) string {
	// Remove .yaml extension
	name := filename[:len(filename)-5]
	// Split by underscore: "monitoring.coreos.com_alertmanagers" -> ["monitoring.coreos.com", "alertmanagers"]
	parts := splitString(name, "_")
	if len(parts) == 2 {
		// Return as "alertmanagers.monitoring.coreos.com"
		return parts[1] + "." + parts[0]
	}
	return name
}

// splitString splits a string by delimiter (helper to avoid strings import)
func splitString(s, delim string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if i+len(delim) <= len(s) && s[i:i+len(delim)] == delim {
			result = append(result, s[start:i])
			start = i + len(delim)
			i += len(delim) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

// contains checks if a string contains a substring (helper to avoid strings import)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOfSubstring(s, substr) >= 0
}

// indexOfSubstring finds the index of a substring
func indexOfSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// installPrometheus installs kube-prometheus-stack using Helm
// This includes Prometheus, Grafana, Alertmanager, and is Lens-compatible
func (m *Manager) installPrometheus(profile types.ResourceProfile) error {
	m.logger.Info("Installing kube-prometheus-stack (Prometheus + Grafana + Alertmanager)...")

	// Install Prometheus Operator CRDs first
	// These must be installed before the Helm chart since we use SkipCRDs=true
	if err := m.installPrometheusCRDs(); err != nil {
		return fmt.Errorf("failed to install Prometheus CRDs: %w", err)
	}

	values := m.getPrometheusValues(profile)

	if err := m.installer.Install(m.ctx, &helm.HelmRelease{
		Name:      m.stack.Prometheus.ReleaseName,
		Namespace: m.stack.Prometheus.Namespace,
		Chart:     m.stack.Prometheus.ChartName,
		Repo:      m.stack.Prometheus.ChartRepo,
		Version:   m.stack.Prometheus.ChartVersion,
		Values:    values,
		SkipCRDs:  true, // CRDs are pre-installed by installPrometheusCRDs()
		Timeout:   helmTimeout(profile),
	}); err != nil {
		return err
	}

	m.logger.Info("✓ kube-prometheus-stack installed successfully (Lens-compatible)")
	m.logger.Info("  - Prometheus with persistent storage")
	m.logger.Info("  - Grafana with persistent storage")
	m.logger.Info("  - Alertmanager")
	m.logger.Info("  - kube-state-metrics")
	m.logger.Info("  - Node Exporter")
	return nil
}

// installLoki installs Loki-stack using Helm (includes Loki + Promtail)
func (m *Manager) installLoki(profile types.ResourceProfile) error {
	m.logger.Info("Installing Loki-stack (Loki + Promtail)...")

	values := m.getLokiValues(profile)

	if err := m.installer.Install(m.ctx, &helm.HelmRelease{
		Name:      m.stack.Loki.ReleaseName,
		Namespace: m.stack.Loki.Namespace,
		Chart:     m.stack.Loki.ChartName,
		Repo:      m.stack.Loki.ChartRepo,
		Version:   m.stack.Loki.ChartVersion,
		Values:    values,
		Timeout:   helmTimeout(profile),
	}); err != nil {
		return err
	}

	m.logger.Info("✓ Loki-stack installed successfully with persistent storage")
	m.logger.WithField("storage_size", m.stack.Loki.StorageSize).Info("  - Loki with persistent storage")
	m.logger.Info("  - Promtail for log collection")
	return nil
}

// installCertManager installs cert-manager for automatic TLS certificate management
func (m *Manager) installCertManager(profile types.ResourceProfile) error {
	// Check if cert-manager is already installed
	if m.isCertManagerInstalled() {
		m.logger.Info("✓ cert-manager already installed")
		return nil
	}

	m.logger.Info("Installing cert-manager for TLS certificate management...")

	// Add Jetstack Helm repository
	if err := m.installer.AddRepo(m.ctx, "jetstack", m.stack.CertManager.ChartRepo); err != nil {
		return fmt.Errorf("failed to add jetstack repo: %w", err)
	}

	// Prepare values for cert-manager
	values := m.getCertManagerValues(profile)

	// Install cert-manager
	if err := m.installer.Install(m.ctx, &helm.HelmRelease{
		Name:      m.stack.CertManager.ReleaseName,
		Namespace: m.stack.CertManager.Namespace,
		Chart:     m.stack.CertManager.ChartName,
		Repo:      m.stack.CertManager.ChartRepo,
		Version:   m.stack.CertManager.ChartVersion,
		Values:    values,
		Timeout:   helmTimeout(profile),
	}); err != nil {
		return fmt.Errorf("failed to install cert-manager: %w", err)
	}

	m.logger.Info("✓ cert-manager installed successfully")
	m.logger.WithField("version", m.stack.CertManager.ChartVersion).Info("  - Automatic TLS certificate management enabled")
	m.logger.Info("  - Supports Let's Encrypt, self-signed, and custom CA certificates")

	// Wait for cert-manager webhook to be ready before creating ClusterIssuers
	m.logger.Info("Waiting for cert-manager webhook to be ready...")
	if err := m.waitForCertManagerWebhook(); err != nil {
		m.logger.WithError(err).Warn("cert-manager webhook not ready, skipping ClusterIssuer creation")
		return nil
	}

	// Create default ClusterIssuers for Let's Encrypt
	if err := m.createDefaultClusterIssuers(); err != nil {
		m.logger.WithError(err).Warn("Failed to create default ClusterIssuers (non-fatal)")
	}

	return nil
}

// isCertManagerInstalled checks if cert-manager is already installed
func (m *Manager) isCertManagerInstalled() bool {
	if m.stack.CertManager == nil {
		return false
	}

	// Check if cert-manager deployment exists
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := hasDeployment(ctx, m.installer.K8sClient, m.stack.CertManager.Namespace, "cert-manager")
	if err != nil {
		m.logger.WithError(err).Debug("Error checking cert-manager deployment")
		return false
	}

	return exists
}

// waitForCertManagerWebhook waits for cert-manager webhook to be ready
func (m *Manager) waitForCertManagerWebhook() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	m.logger.Debug("Waiting for cert-manager-webhook deployment to be ready...")

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for cert-manager webhook")
		default:
			deployment, err := m.installer.K8sClient.AppsV1().Deployments(m.stack.CertManager.Namespace).
				Get(ctx, "cert-manager-webhook", metav1.GetOptions{})
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}

			if deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == deployment.Status.Replicas {
				m.logger.Info("✓ cert-manager webhook is ready")
				// Give it a few more seconds to ensure webhook is fully operational
				time.Sleep(5 * time.Second)
				return nil
			}

			time.Sleep(2 * time.Second)
		}
	}
}

// createDefaultClusterIssuers creates default Let's Encrypt ClusterIssuers
func (m *Manager) createDefaultClusterIssuers() error {
	m.logger.Info("Creating default Let's Encrypt ClusterIssuers...")

	// ClusterIssuer for Let's Encrypt production (letsencrypt-prod)
	prodIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@pipeops.io
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          class: traefik
`

	// ClusterIssuer for Let's Encrypt production (letsencrypt-production - alias)
	prodProductionIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-production
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@pipeops.io
    privateKeySecretRef:
      name: letsencrypt-production
    solvers:
    - http01:
        ingress:
          class: traefik
`

	// Apply letsencrypt-prod ClusterIssuer
	if err := m.applyClusterIssuer(prodIssuer); err != nil {
		m.logger.WithError(err).Warn("Failed to create letsencrypt-prod ClusterIssuer")
	} else {
		m.logger.Info("✓ Created ClusterIssuer: letsencrypt-prod")
	}

	// Apply letsencrypt-production ClusterIssuer
	if err := m.applyClusterIssuer(prodProductionIssuer); err != nil {
		m.logger.WithError(err).Warn("Failed to create letsencrypt-production ClusterIssuer")
	} else {
		m.logger.Info("✓ Created ClusterIssuer: letsencrypt-production")
	}

	m.logger.Info("✓ Default ClusterIssuers created - automatic TLS enabled for ingresses")
	return nil
}

// applyClusterIssuer applies a ClusterIssuer YAML to the cluster
func (m *Manager) applyClusterIssuer(yamlContent string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Parse YAML to unstructured object
	decode := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, _, err := decode.Decode([]byte(yamlContent), nil, obj)
	if err != nil {
		return fmt.Errorf("failed to decode YAML: %w", err)
	}

	name := obj.GetName()

	// Convert to JSON for REST API
	jsonBytes, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	// Try to create the ClusterIssuer using REST API
	result := m.installer.K8sClient.RESTClient().
		Post().
		AbsPath("/apis/cert-manager.io/v1/clusterissuers").
		Body(jsonBytes).
		Do(ctx)

	if err := result.Error(); err != nil {
		// Check if it already exists
		if apierrors.IsAlreadyExists(err) {
			m.logger.WithField("name", name).Debug("ClusterIssuer already exists, skipping")
			return nil
		}
		return fmt.Errorf("failed to create ClusterIssuer %s: %w", name, err)
	}

	return nil
}

// Health check functions
func (m *Manager) checkPrometheusHealth() bool {
	// For kube-prometheus-stack, the service name is <release-name>-prometheus
	serviceName := fmt.Sprintf("%s-prometheus", m.stack.Prometheus.ReleaseName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/-/healthy",
		serviceName,
		m.stack.Prometheus.Namespace,
		m.stack.Prometheus.LocalPort)

	return m.performHealthCheck("Prometheus", url)
}

func (m *Manager) checkLokiHealth() bool {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/ready",
		m.stack.Loki.ReleaseName,
		m.stack.Loki.Namespace,
		m.stack.Loki.LocalPort)

	return m.performHealthCheck("Loki", url)
}

func (m *Manager) checkGrafanaHealth() bool {
	// For kube-prometheus-stack, Grafana service name is <release-name>-grafana
	serviceName := fmt.Sprintf("%s-grafana", m.stack.Prometheus.ReleaseName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/api/health",
		serviceName,
		m.stack.Grafana.Namespace,
		m.stack.Grafana.LocalPort)

	return m.performHealthCheck("Grafana", url)
}

// performHealthCheck performs an HTTP GET request to check service health
func (m *Manager) performHealthCheck(serviceName, url string) bool {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		m.logger.WithFields(logrus.Fields{
			"service": serviceName,
			"url":     url,
			"error":   err.Error(),
		}).Debug("Health check failed")
		return false
	}
	defer resp.Body.Close()

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300

	if healthy {
		m.logger.WithFields(logrus.Fields{
			"service": serviceName,
			"status":  resp.StatusCode,
		}).Debug("Health check passed")
	} else {
		m.logger.WithFields(logrus.Fields{
			"service": serviceName,
			"status":  resp.StatusCode,
		}).Warn("Health check failed - unhealthy status code")
	}

	return healthy
}

// Ingress creation methods

func (m *Manager) createPrometheusIngress() {
	// For kube-prometheus-stack, the service name is <release-name>-prometheus
	serviceName := fmt.Sprintf("%s-prometheus", m.stack.Prometheus.ReleaseName)

	const authSecretName = "prometheus-basic-auth"
	var annotations map[string]string

	if m.stack.Prometheus != nil && m.stack.Prometheus.Namespace != "" && m.installer != nil && m.installer.K8sClient != nil {
		// Only check for NGINX basic auth secret if NGINX is likely to be used
		if !m.traefikController.IsInstalled(context.Background()) {
			secretCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := m.installer.K8sClient.CoreV1().Secrets(m.stack.Prometheus.Namespace).Get(secretCtx, authSecretName, metav1.GetOptions{}); err == nil {
				annotations = map[string]string{
					"nginx.ingress.kubernetes.io/auth-type":   "basic",
					"nginx.ingress.kubernetes.io/auth-secret": authSecretName,
					"nginx.ingress.kubernetes.io/auth-realm":  "Authentication Required",
				}
				m.logger.WithField("secret", authSecretName).Debug("Prometheus basic auth enabled for ingress")
			} else {
				if apierrors.IsNotFound(err) {
					m.logger.WithField("secret", authSecretName).Info("Prometheus basic auth secret not found; creating ingress without authentication")
				} else {
					m.logger.WithError(err).WithField("secret", authSecretName).Warn("Failed to verify Prometheus basic auth secret; creating ingress without authentication")
				}
			}
		} else {
			// TODO: Implement Traefik Basic Auth Middleware creation if needed
			// For now, we proceed without auth on Traefik or let the user configure it via Middleware
		}
	}
	config := ingress.IngressConfig{
		Name:        "prometheus-ingress",
		Namespace:   m.stack.Prometheus.Namespace,
		Host:        "prometheus.local", // Change this to your actual domain
		ServiceName: serviceName,
		ServicePort: m.stack.Prometheus.LocalPort,
		TLSEnabled:  false,
		Annotations: annotations,
	}

	var err error
	if m.traefikController.IsInstalled(context.Background()) {
		err = m.traefikController.CreateIngress(config)
	} else {
		err = m.ingressController.CreateIngress(config)
	}

	if err != nil {
		m.logger.WithError(err).Warn("Failed to create Prometheus ingress")
	} else {
		m.logger.WithField("host", config.Host).Info("✓ Created Prometheus ingress")
	}
}

func (m *Manager) createLokiIngress() {
	config := ingress.IngressConfig{
		Name:        "loki-ingress",
		Namespace:   m.stack.Loki.Namespace,
		Host:        "loki.local", // Change this to your actual domain
		ServiceName: m.stack.Loki.ReleaseName,
		ServicePort: m.stack.Loki.LocalPort,
		TLSEnabled:  false,
	}

	var err error
	if m.traefikController.IsInstalled(context.Background()) {
		err = m.traefikController.CreateIngress(config)
	} else {
		err = m.ingressController.CreateIngress(config)
	}

	if err != nil {
		m.logger.WithError(err).Warn("Failed to create Loki ingress")
	} else {
		m.logger.WithField("host", config.Host).Info("✓ Created Loki ingress")
	}
}

func (m *Manager) createGrafanaIngress() {
	// For kube-prometheus-stack, Grafana service name is <release-name>-grafana
	serviceName := fmt.Sprintf("%s-grafana", m.stack.Prometheus.ReleaseName)
	config := ingress.IngressConfig{
		Name:        "grafana-ingress",
		Namespace:   m.stack.Grafana.Namespace,
		Host:        "grafana.local", // Change this to your actual domain
		ServiceName: serviceName,
		ServicePort: m.stack.Grafana.LocalPort,
		TLSEnabled:  false,
	}

	var err error
	if m.traefikController.IsInstalled(context.Background()) {
		err = m.traefikController.CreateIngress(config)
	} else {
		err = m.ingressController.CreateIngress(config)
	}

	if err != nil {
		m.logger.WithError(err).Warn("Failed to create Grafana ingress")
	} else {
		m.logger.WithField("host", config.Host).Info("✓ Created Grafana ingress")
	}
}

// GetComponentsStatus returns the status of all essential components
func (m *Manager) GetComponentsStatus() ([]ComponentStatus, error) {
	return m.componentInstaller.GetAllComponentsStatus(m.ctx)
}

// LogComponentsStatus logs the status of all essential components
func (m *Manager) LogComponentsStatus() {
	statuses, err := m.GetComponentsStatus()
	if err != nil {
		m.logger.WithError(err).Warn("Failed to get components status")
		return
	}

	m.logger.Info("Essential Kubernetes Components Status:")
	for _, status := range statuses {
		if status.Installed {
			readyStatus := "✓"
			if !status.Ready {
				readyStatus = "✗"
			}
			m.logger.WithFields(logrus.Fields{
				"component": status.Name,
				"ready":     readyStatus,
				"replicas":  status.Replicas,
				"available": status.Available,
			}).Info("Component status")
		} else {
			m.logger.WithField("component", status.Name).Warn("Component not installed")
		}
	}
}
