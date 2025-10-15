package monitoring

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
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
	SSL          bool   `yaml:"ssl"`
}

// LokiConfig holds Loki configuration
type LokiConfig struct {
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
	Enabled       bool   `yaml:"enabled"`
	Namespace     string `yaml:"namespace"`
	ReleaseName   string `yaml:"release_name"`
	ChartRepo     string `yaml:"chart_repo"`
	ChartName     string `yaml:"chart_name"`
	ChartVersion  string `yaml:"chart_version"`
	LocalPort     int    `yaml:"local_port"`
	RemotePort    int    `yaml:"remote_port"`
	AdminUser     string `yaml:"admin_user"`
	AdminPassword string `yaml:"admin_password"`
}

// Manager manages the monitoring stack lifecycle
type Manager struct {
	stack     *MonitoringStack
	logger    *logrus.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	installer *HelmInstaller
}

// NewManager creates a new monitoring stack manager
func NewManager(stack *MonitoringStack, logger *logrus.Logger) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	installer, err := NewHelmInstaller(logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create helm installer: %w", err)
	}

	return &Manager{
		stack:     stack,
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
		installer: installer,
	}, nil
}

// Start installs and configures the monitoring stack
func (m *Manager) Start() error {
	m.logger.Info("Starting monitoring stack manager...")

	// Install Prometheus
	if m.stack.Prometheus != nil && m.stack.Prometheus.Enabled {
		if err := m.installPrometheus(); err != nil {
			return fmt.Errorf("failed to install Prometheus: %w", err)
		}
	}

	// Install Loki
	if m.stack.Loki != nil && m.stack.Loki.Enabled {
		if err := m.installLoki(); err != nil {
			return fmt.Errorf("failed to install Loki: %w", err)
		}
	}

	// Install OpenCost
	if m.stack.OpenCost != nil && m.stack.OpenCost.Enabled {
		if err := m.installOpenCost(); err != nil {
			return fmt.Errorf("failed to install OpenCost: %w", err)
		}
	}

	// Install Grafana
	if m.stack.Grafana != nil && m.stack.Grafana.Enabled {
		if err := m.installGrafana(); err != nil {
			return fmt.Errorf("failed to install Grafana: %w", err)
		}
	}

	m.logger.Info("Monitoring stack started successfully")
	return nil
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
		// Prometheus service is typically named "<release-name>-server"
		serviceName := fmt.Sprintf("%s-server", m.stack.Prometheus.ReleaseName)
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
		forwards = append(forwards, TunnelForward{
			Name: "grafana",
			LocalAddr: fmt.Sprintf("%s.%s.svc.cluster.local:%d",
				m.stack.Grafana.ReleaseName,
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

// installPrometheus installs Prometheus using Helm
func (m *Manager) installPrometheus() error {
	m.logger.Info("Installing Prometheus...")

	values := map[string]interface{}{
		"server": map[string]interface{}{
			"service": map[string]interface{}{
				"type": "ClusterIP",
			},
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

	m.logger.Info("Prometheus installed successfully")
	return nil
}

// installLoki installs Loki using Helm
func (m *Manager) installLoki() error {
	m.logger.Info("Installing Loki...")

	values := map[string]interface{}{
		"loki": map[string]interface{}{
			"auth_enabled": false,
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

	m.logger.Info("Loki installed successfully")
	return nil
}

// installOpenCost installs OpenCost using Helm
func (m *Manager) installOpenCost() error {
	m.logger.Info("Installing OpenCost...")

	values := map[string]interface{}{
		"opencost": map[string]interface{}{
			"prometheus": map[string]interface{}{
				"external": map[string]interface{}{
					"url": fmt.Sprintf("http://%s-server.%s.svc.cluster.local:%d",
						m.stack.Prometheus.ReleaseName,
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

	m.logger.Info("OpenCost installed successfully")
	return nil
}

// installGrafana installs Grafana using Helm
func (m *Manager) installGrafana() error {
	m.logger.Info("Installing Grafana...")

	values := map[string]interface{}{
		"adminUser":     m.stack.Grafana.AdminUser,
		"adminPassword": m.stack.Grafana.AdminPassword,
		"datasources": map[string]interface{}{
			"datasources.yaml": map[string]interface{}{
				"apiVersion": 1,
				"datasources": []map[string]interface{}{
					{
						"name": "Prometheus",
						"type": "prometheus",
						"url": fmt.Sprintf("http://%s-server.%s.svc.cluster.local:%d",
							m.stack.Prometheus.ReleaseName,
							m.stack.Prometheus.Namespace,
							m.stack.Prometheus.LocalPort),
						"access": "proxy",
					},
					{
						"name": "Loki",
						"type": "loki",
						"url": fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
							m.stack.Loki.ReleaseName,
							m.stack.Loki.Namespace,
							m.stack.Loki.LocalPort),
						"access": "proxy",
					},
				},
			},
		},
	}

	if err := m.installer.Install(m.ctx, &HelmRelease{
		Name:      m.stack.Grafana.ReleaseName,
		Namespace: m.stack.Grafana.Namespace,
		Chart:     m.stack.Grafana.ChartName,
		Repo:      m.stack.Grafana.ChartRepo,
		Version:   m.stack.Grafana.ChartVersion,
		Values:    values,
	}); err != nil {
		return err
	}

	m.logger.Info("Grafana installed successfully")
	return nil
}

// Health check functions
func (m *Manager) checkPrometheusHealth() bool {
	// TODO: Implement HTTP health check to Prometheus
	return true
}

func (m *Manager) checkLokiHealth() bool {
	// TODO: Implement HTTP health check to Loki
	return true
}

func (m *Manager) checkOpenCostHealth() bool {
	// TODO: Implement HTTP health check to OpenCost
	return true
}

func (m *Manager) checkGrafanaHealth() bool {
	// TODO: Implement HTTP health check to Grafana
	return true
}
