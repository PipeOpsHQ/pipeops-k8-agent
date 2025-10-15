package monitoring

// DefaultMonitoringStack returns the default monitoring stack configuration
func DefaultMonitoringStack() *MonitoringStack {
	return &MonitoringStack{
		Prometheus: &PrometheusConfig{
			Enabled:      true,
			Namespace:    "pipeops-monitoring",
			ReleaseName:  "prometheus",
			ChartRepo:    "https://prometheus-community.github.io/helm-charts",
			ChartName:    "prometheus-community/prometheus",
			ChartVersion: "", // latest
			LocalPort:    9090,
			RemotePort:   19090, // Port on control plane side
			Username:     "admin",
			Password:     generatePassword(),
			SSL:          true,
		},
		Loki: &LokiConfig{
			Enabled:      true,
			Namespace:    "pipeops-monitoring",
			ReleaseName:  "loki",
			ChartRepo:    "https://grafana.github.io/helm-charts",
			ChartName:    "grafana/loki-stack",
			ChartVersion: "", // latest
			LocalPort:    3100,
			RemotePort:   13100, // Port on control plane side
			Username:     "admin",
			Password:     generatePassword(),
		},
		OpenCost: &OpenCostConfig{
			Enabled:      true,
			Namespace:    "pipeops-monitoring",
			ReleaseName:  "opencost",
			ChartRepo:    "https://opencost.github.io/opencost-helm-chart",
			ChartName:    "opencost/opencost",
			ChartVersion: "", // latest
			LocalPort:    9003,
			RemotePort:   19003, // Port on control plane side
			Username:     "admin",
			Password:     generatePassword(),
		},
		Grafana: &GrafanaConfig{
			Enabled:       true,
			Namespace:     "pipeops-monitoring",
			ReleaseName:   "grafana",
			ChartRepo:     "https://grafana.github.io/helm-charts",
			ChartName:     "grafana/grafana",
			ChartVersion:  "", // latest
			LocalPort:     3000,
			RemotePort:    13000, // Port on control plane side
			AdminUser:     "admin",
			AdminPassword: generatePassword(),
		},
	}
}

// generatePassword generates a random password for monitoring services
func generatePassword() string {
	// TODO: Implement secure password generation
	// For now, return a placeholder
	return "changeme123!"
}
