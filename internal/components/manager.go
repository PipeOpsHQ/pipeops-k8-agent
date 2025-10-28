package components

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// MonitoringStack represents the monitoring tools installed on the cluster
type MonitoringStack struct {
	Prometheus *PrometheusConfig
	Loki       *LokiConfig
	OpenCost   *OpenCostConfig
	Grafana    *GrafanaConfig
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
	StorageClass      string `yaml:"storage_class"`
	StorageSize       string `yaml:"storage_size"`
	EnablePersistence bool   `yaml:"enable_persistence"`
}

// OpenCostConfig holds OpenCost configuration
type OpenCostConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Namespace    string `yaml:"namespace"`
	ReleaseName  string `yaml:"release_name"`
	ChartRepo    string `yaml:"chart_repo"`
	ChartName    string `yaml:"chart_name"`
	ChartVersion string `yaml:"chart_version"`
	LocalPort    int    `yaml:"local_port"`
	RemotePort   int    `yaml:"remote_port"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
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
	StorageClass      string `yaml:"storage_class"`
	StorageSize       string `yaml:"storage_size"`
	EnablePersistence bool   `yaml:"enable_persistence"`
	RootURL           string `yaml:"root_url"`
	ServeFromSubPath  bool   `yaml:"serve_from_sub_path"`
}

// Manager manages the monitoring stack lifecycle
type Manager struct {
	stack              *MonitoringStack
	logger             *logrus.Logger
	ctx                context.Context
	cancel             context.CancelFunc
	installer          *HelmInstaller
	ingressController  *IngressController
	componentInstaller *ComponentInstaller
	ingressEnabled     bool
}

// NewManager creates a new monitoring stack manager
func NewManager(stack *MonitoringStack, logger *logrus.Logger) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	installer, err := NewHelmInstaller(logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create helm installer: %w", err)
	}

	// Create ingress controller using installer's k8s client
	ingressController := NewIngressController(installer, installer.k8sClient, logger)

	// Create component installer for essential Kubernetes components
	componentInstaller := NewComponentInstaller(installer, installer.k8sClient, logger)

	ingressEnabled := determineIngressPreference(installer.k8sClient, logger)

	return &Manager{
		stack:              stack,
		logger:             logger,
		ctx:                ctx,
		cancel:             cancel,
		installer:          installer,
		ingressController:  ingressController,
		componentInstaller: componentInstaller,
		ingressEnabled:     ingressEnabled,
	}, nil
}

// Start installs and configures the monitoring stack
func (m *Manager) Start() error {
	m.logger.Info("Starting monitoring stack manager...")

	// Install essential Kubernetes components first
	// The componentInstaller logs its own message
	if err := m.componentInstaller.InstallEssentialComponents(m.ctx); err != nil {
		m.logger.WithError(err).Warn("Some essential components failed to install (non-fatal)")
	}

	// Verify Metrics API is accessible
	if err := m.componentInstaller.VerifyMetricsAPI(m.ctx); err != nil {
		m.logger.WithError(err).Warn("Metrics API verification failed (non-fatal)")
	}

	m.prepareMonitoringDefaults()

	// Install NGINX Ingress Controller if enabled
	if m.ingressEnabled && !m.ingressController.IsInstalled() {
		m.logger.Info("Installing NGINX Ingress Controller...")
		if err := m.ingressController.Install(); err != nil {
			m.logger.WithError(err).Warn("Failed to install ingress controller - continuing without ingress")
			m.ingressEnabled = false
		}
	} else if m.ingressController.IsInstalled() {
		m.logger.Info("✓ Ingress controller already installed")
	}

	// Install Prometheus
	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		if err := m.installPrometheus(); err != nil {
			return fmt.Errorf("failed to install Prometheus: %w", err)
		}
		// Create ingress for Prometheus if enabled
		if m.ingressEnabled {
			m.createPrometheusIngress()
		}
	}

	// Install Loki
	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		if err := m.installLoki(); err != nil {
			return fmt.Errorf("failed to install Loki: %w", err)
		}
		// Create ingress for Loki if enabled
		if m.ingressEnabled {
			m.createLokiIngress()
		}
	}

	// Install OpenCost
	if m.stack.OpenCost != nil && m.stack.OpenCost.Enabled {
		if err := m.installOpenCost(); err != nil {
			return fmt.Errorf("failed to install OpenCost: %w", err)
		}
		// Create ingress for OpenCost if enabled
		if m.ingressEnabled {
			m.createOpenCostIngress()
		}
	}

	// Grafana is now included in kube-prometheus-stack, so we skip separate installation
	// But we still create ingress if enabled
	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		m.logger.Info("✓ Grafana included in kube-prometheus-stack (no separate installation needed)")
		// Create ingress for Grafana if enabled
		if m.ingressEnabled {
			m.createGrafanaIngress()
		}
	}

	m.logger.Info("Monitoring stack started successfully")
	return nil
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
	}
}

func (m *Manager) detectDefaultStorageClass(ctx context.Context) (string, error) {
	if m.installer == nil || m.installer.k8sClient == nil {
		return "", fmt.Errorf("kubernetes client unavailable")
	}

	classes, err := m.installer.k8sClient.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
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

	classes, err := client.NetworkingV1().IngressClasses().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, class := range classes.Items {
			if isDefaultIngressClass(&class) {
				logger.WithField("ingressClass", class.Name).Info("Detected existing default ingress class; skipping bundled ingress controller")
				return false
			}
		}
	} else if !apierrors.IsNotFound(err) {
		logger.WithError(err).Debug("Unable to list ingress classes")
	}

	if exists, err := hasDeployment(ctx, client, "ingress-nginx", "ingress-nginx-controller"); err == nil {
		if exists {
			logger.Info("Detected existing ingress-nginx controller; skipping bundled ingress controller")
			return false
		}
	} else {
		logger.WithError(err).Debug("Unable to inspect ingress-nginx deployment")
	}

	if exists, err := hasDeployment(ctx, client, "kube-system", "traefik"); err == nil {
		if exists {
			logger.Info("Detected existing Traefik ingress controller; skipping bundled ingress controller")
			return false
		}
	} else {
		logger.WithError(err).Debug("Unable to inspect Traefik deployment")
	}

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

	if m.stack.OpenCost != nil && m.stack.OpenCost.Enabled {
		forwards = append(forwards, TunnelForward{
			Name: "opencost",
			LocalAddr: fmt.Sprintf("%s.%s.svc.cluster.local:%d",
				m.stack.OpenCost.ReleaseName,
				m.stack.OpenCost.Namespace,
				m.stack.OpenCost.LocalPort),
			RemotePort: m.stack.OpenCost.RemotePort,
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

// GetMonitoringInfo returns monitoring information for registration
func (m *Manager) GetMonitoringInfo() map[string]interface{} {
	info := make(map[string]interface{})

	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		info["prometheus_url"] = fmt.Sprintf("http://prometheus.%s.svc.cluster.local:%d",
			m.stack.Prometheus.Namespace, m.stack.Prometheus.LocalPort)
		info["prometheus_username"] = m.stack.Prometheus.Username
		info["prometheus_password"] = m.stack.Prometheus.Password
		info["prometheus_ssl"] = m.stack.Prometheus.SSL
	}

	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		info["loki_url"] = fmt.Sprintf("http://loki.%s.svc.cluster.local:%d",
			m.stack.Loki.Namespace, m.stack.Loki.LocalPort)
		info["loki_username"] = m.stack.Loki.Username
		info["loki_password"] = m.stack.Loki.Password
	}

	if m.stack.OpenCost != nil && m.stack.OpenCost.Enabled {
		info["opencost_base_url"] = fmt.Sprintf("http://opencost.%s.svc.cluster.local:%d",
			m.stack.OpenCost.Namespace, m.stack.OpenCost.LocalPort)
		info["opencost_username"] = m.stack.OpenCost.Username
		info["opencost_password"] = m.stack.OpenCost.Password
	}

	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		info["grafana_url"] = fmt.Sprintf("http://grafana.%s.svc.cluster.local:%d",
			m.stack.Grafana.Namespace, m.stack.Grafana.LocalPort)
		info["grafana_username"] = m.stack.Grafana.AdminUser
		info["grafana_password"] = m.stack.Grafana.AdminPassword
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

	if m.stack.OpenCost != nil && m.stack.OpenCost.Enabled {
		health["opencost"] = m.checkOpenCostHealth()
	}

	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		health["grafana"] = m.checkGrafanaHealth()
	}

	return health
}

// installPrometheusCRDs installs Prometheus Operator CRDs from the official kube-prometheus project
func (m *Manager) installPrometheusCRDs() error {
	m.logger.Info("Installing Prometheus Operator CRDs...")

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
	failCount := 0
	var lastError error

	// Use kubectl to apply CRDs
	for _, crdFile := range crdFiles {
		crdURL := fmt.Sprintf("%s/%s", crdBaseURL, crdFile)
		m.logger.WithField("crd", crdFile).Info("Applying CRD from URL")

		cmd := exec.CommandContext(m.ctx, "kubectl", "apply", "--server-side", "--force-conflicts", "-f", crdURL)
		output, err := cmd.CombinedOutput()
		if err != nil {
			failCount++
			lastError = err
			m.logger.WithError(err).WithFields(logrus.Fields{
				"crd":    crdFile,
				"url":    crdURL,
				"output": string(output),
			}).Error("Failed to apply CRD")
			// Continue with other CRDs even if one fails
			continue
		}
		successCount++
		m.logger.WithFields(logrus.Fields{
			"crd":    crdFile,
			"output": string(output),
		}).Info("CRD applied successfully")
	}

	m.logger.WithFields(logrus.Fields{
		"success": successCount,
		"failed":  failCount,
		"total":   len(crdFiles),
	}).Info("Prometheus Operator CRD installation complete")

	if failCount > 0 {
		return fmt.Errorf("failed to install %d out of %d CRDs (last error: %w)", failCount, len(crdFiles), lastError)
	}

	m.logger.Info("✓ All Prometheus Operator CRDs installed successfully")
	return nil
}

// installPrometheus installs kube-prometheus-stack using Helm
// This includes Prometheus, Grafana, Alertmanager, and is Lens-compatible
func (m *Manager) installPrometheus() error {
	m.logger.Info("Installing kube-prometheus-stack (Prometheus + Grafana + Alertmanager)...")

	// Install Prometheus Operator CRDs first
	// These must be installed before the Helm chart since we use SkipCRDs=true
	if err := m.installPrometheusCRDs(); err != nil {
		return fmt.Errorf("failed to install Prometheus CRDs: %w", err)
	}

	values := map[string]interface{}{
		// Prometheus configuration
		"prometheus": map[string]interface{}{
			"prometheusSpec": map[string]interface{}{
				"retention": m.stack.Prometheus.RetentionPeriod,
				"storageSpec": func() map[string]interface{} {
					if m.stack.Prometheus.EnablePersistence {
						return map[string]interface{}{
							"volumeClaimTemplate": map[string]interface{}{
								"spec": map[string]interface{}{
									"storageClassName": m.stack.Prometheus.StorageClass,
									"accessModes":      []string{"ReadWriteOnce"},
									"resources": map[string]interface{}{
										"requests": map[string]interface{}{
											"storage": m.stack.Prometheus.StorageSize,
										},
									},
								},
							},
						}
					}
					return nil
				}(),
				"serviceMonitorSelectorNilUsesHelmValues": false,
				"podMonitorSelectorNilUsesHelmValues":     false,
			},
			"service": map[string]interface{}{
				"type": "ClusterIP",
				"port": m.stack.Prometheus.LocalPort,
			},
		},
		// Grafana configuration (included in kube-prometheus-stack)
		"grafana": func() map[string]interface{} {
			grafanaValues := map[string]interface{}{
				"enabled":       m.stack.Grafana.Enabled,
				"adminUser":     m.stack.Grafana.AdminUser,
				"adminPassword": m.stack.Grafana.AdminPassword,
				"persistence": map[string]interface{}{
					"enabled": m.stack.Grafana.EnablePersistence,
					"storageClassName": func() string {
						if m.stack.Grafana.EnablePersistence {
							return m.stack.Grafana.StorageClass
						}
						return ""
					}(),
					"size": m.stack.Grafana.StorageSize,
				},
				"service": map[string]interface{}{
					"type": "ClusterIP",
					"port": m.stack.Grafana.LocalPort,
				},
				// Configure Loki datasource if enabled
				"additionalDataSources": func() []map[string]interface{} {
					if m.stack.Loki != nil && m.stack.Loki.Enabled {
						return []map[string]interface{}{
							{
								"name": "Loki",
								"type": "loki",
								"url": fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
									m.stack.Loki.ReleaseName,
									m.stack.Loki.Namespace,
									m.stack.Loki.LocalPort),
								"access": "proxy",
							},
						}
					}
					return nil
				}(),
			}

			if m.stack.Grafana != nil && (m.stack.Grafana.RootURL != "" || m.stack.Grafana.ServeFromSubPath) {
				serverConfig := map[string]interface{}{}
				if m.stack.Grafana.RootURL != "" {
					serverConfig["root_url"] = m.stack.Grafana.RootURL
				}
				if m.stack.Grafana.ServeFromSubPath {
					serverConfig["serve_from_sub_path"] = true
				}
				if len(serverConfig) > 0 {
					grafanaValues["grafana.ini"] = map[string]interface{}{
						"server": serverConfig,
					}
				}
			}

			return grafanaValues
		}(),
		// Alertmanager configuration
		"alertmanager": map[string]interface{}{
			"alertmanagerSpec": map[string]interface{}{
				"storage": func() map[string]interface{} {
					if m.stack.Prometheus.EnablePersistence {
						return map[string]interface{}{
							"volumeClaimTemplate": map[string]interface{}{
								"spec": map[string]interface{}{
									"storageClassName": m.stack.Prometheus.StorageClass,
									"accessModes":      []string{"ReadWriteOnce"},
									"resources": map[string]interface{}{
										"requests": map[string]interface{}{
											"storage": "2Gi",
										},
									},
								},
							},
						}
					}
					return nil
				}(),
			},
		},
		// kube-state-metrics (included in kube-prometheus-stack)
		"kube-state-metrics": map[string]interface{}{
			"enabled": true,
		},
		// Node exporter (included in kube-prometheus-stack)
		"prometheus-node-exporter": map[string]interface{}{
			"enabled": true,
		},
	}

	if err := m.installer.Install(m.ctx, &HelmRelease{
		Name:      m.stack.Prometheus.ReleaseName,
		Namespace: m.stack.Prometheus.Namespace,
		Chart:     m.stack.Prometheus.ChartName,
		Repo:      m.stack.Prometheus.ChartRepo,
		Version:   m.stack.Prometheus.ChartVersion,
		Values:    values,
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
func (m *Manager) installLoki() error {
	m.logger.Info("Installing Loki-stack (Loki + Promtail)...")

	values := map[string]interface{}{
		"loki": map[string]interface{}{
			"enabled": true,
			"persistence": map[string]interface{}{
				"enabled": m.stack.Loki.EnablePersistence,
				"storageClassName": func() string {
					if m.stack.Loki.EnablePersistence {
						return m.stack.Loki.StorageClass
					}
					return ""
				}(),
				"size": m.stack.Loki.StorageSize,
			},
			"config": map[string]interface{}{
				"auth_enabled": false,
				"chunk_store_config": map[string]interface{}{
					"max_look_back_period": "0s",
				},
				"table_manager": map[string]interface{}{
					"retention_deletes_enabled": true,
					"retention_period":          "168h", // 7 days
				},
			},
		},
		// Promtail for log collection
		"promtail": map[string]interface{}{
			"enabled": true,
		},
		// Grafana datasource (optional, since Grafana is in kube-prometheus-stack)
		"grafana": map[string]interface{}{
			"enabled": false, // We use Grafana from kube-prometheus-stack
		},
	}

	if err := m.installer.Install(m.ctx, &HelmRelease{
		Name:      m.stack.Loki.ReleaseName,
		Namespace: m.stack.Loki.Namespace,
		Chart:     m.stack.Loki.ChartName,
		Repo:      m.stack.Loki.ChartRepo,
		Version:   m.stack.Loki.ChartVersion,
		Values:    values,
	}); err != nil {
		return err
	}

	m.logger.Info("✓ Loki-stack installed successfully with persistent storage")
	m.logger.WithField("storage_size", m.stack.Loki.StorageSize).Info("  - Loki with persistent storage")
	m.logger.Info("  - Promtail for log collection")
	return nil
}

// installOpenCost installs OpenCost using Helm
func (m *Manager) installOpenCost() error {
	m.logger.Info("Installing OpenCost...")

	// For kube-prometheus-stack, the Prometheus service name is different
	// Format: <release-name>-prometheus
	prometheusServiceName := fmt.Sprintf("%s-prometheus", m.stack.Prometheus.ReleaseName)

	values := map[string]interface{}{
		"opencost": map[string]interface{}{
			"prometheus": map[string]interface{}{
				"external": map[string]interface{}{
					"url": fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
						prometheusServiceName,
						m.stack.Prometheus.Namespace,
						m.stack.Prometheus.LocalPort),
				},
			},
		},
	}

	if err := m.installer.Install(m.ctx, &HelmRelease{
		Name:      m.stack.OpenCost.ReleaseName,
		Namespace: m.stack.OpenCost.Namespace,
		Chart:     m.stack.OpenCost.ChartName,
		Repo:      m.stack.OpenCost.ChartRepo,
		Version:   m.stack.OpenCost.ChartVersion,
		Values:    values,
	}); err != nil {
		return err
	}

	m.logger.Info("✓ OpenCost installed successfully")
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

func (m *Manager) checkOpenCostHealth() bool {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/healthz",
		m.stack.OpenCost.ReleaseName,
		m.stack.OpenCost.Namespace,
		m.stack.OpenCost.LocalPort)

	return m.performHealthCheck("OpenCost", url)
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
	config := IngressConfig{
		Name:        "prometheus-ingress",
		Namespace:   m.stack.Prometheus.Namespace,
		Host:        "prometheus.local", // Change this to your actual domain
		ServiceName: serviceName,
		ServicePort: m.stack.Prometheus.LocalPort,
		TLSEnabled:  false,
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/auth-type":   "basic",
			"nginx.ingress.kubernetes.io/auth-secret": "prometheus-basic-auth",
			"nginx.ingress.kubernetes.io/auth-realm":  "Authentication Required",
		},
	}

	if err := m.ingressController.CreateIngress(config); err != nil {
		m.logger.WithError(err).Warn("Failed to create Prometheus ingress")
	} else {
		m.logger.WithField("host", config.Host).Info("✓ Created Prometheus ingress")
	}
}

func (m *Manager) createLokiIngress() {
	config := IngressConfig{
		Name:        "loki-ingress",
		Namespace:   m.stack.Loki.Namespace,
		Host:        "loki.local", // Change this to your actual domain
		ServiceName: m.stack.Loki.ReleaseName,
		ServicePort: m.stack.Loki.LocalPort,
		TLSEnabled:  false,
	}

	if err := m.ingressController.CreateIngress(config); err != nil {
		m.logger.WithError(err).Warn("Failed to create Loki ingress")
	} else {
		m.logger.WithField("host", config.Host).Info("✓ Created Loki ingress")
	}
}

func (m *Manager) createOpenCostIngress() {
	config := IngressConfig{
		Name:        "opencost-ingress",
		Namespace:   m.stack.OpenCost.Namespace,
		Host:        "opencost.local", // Change this to your actual domain
		ServiceName: m.stack.OpenCost.ReleaseName,
		ServicePort: m.stack.OpenCost.LocalPort,
		TLSEnabled:  false,
	}

	if err := m.ingressController.CreateIngress(config); err != nil {
		m.logger.WithError(err).Warn("Failed to create OpenCost ingress")
	} else {
		m.logger.WithField("host", config.Host).Info("✓ Created OpenCost ingress")
	}
}

func (m *Manager) createGrafanaIngress() {
	// For kube-prometheus-stack, Grafana service name is <release-name>-grafana
	serviceName := fmt.Sprintf("%s-grafana", m.stack.Prometheus.ReleaseName)
	config := IngressConfig{
		Name:        "grafana-ingress",
		Namespace:   m.stack.Grafana.Namespace,
		Host:        "grafana.local", // Change this to your actual domain
		ServiceName: serviceName,
		ServicePort: m.stack.Grafana.LocalPort,
		TLSEnabled:  false,
	}

	if err := m.ingressController.CreateIngress(config); err != nil {
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
