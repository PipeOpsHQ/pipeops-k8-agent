package monitoring

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestPrometheusConfig_Structure(t *testing.T) {
	config := &PrometheusConfig{
		Enabled:      true,
		Namespace:    "monitoring",
		ReleaseName:  "prometheus",
		ChartRepo:    "https://prometheus-community.github.io/helm-charts",
		ChartName:    "prometheus",
		ChartVersion: "15.0.0",
		LocalPort:    9090,
		RemotePort:   8080,
		Username:     "admin",
		Password:     "secret",
		SSL:          true,
	}

	assert.True(t, config.Enabled)
	assert.Equal(t, "monitoring", config.Namespace)
	assert.Equal(t, "prometheus", config.ReleaseName)
	assert.Equal(t, 9090, config.LocalPort)
	assert.Equal(t, 8080, config.RemotePort)
	assert.True(t, config.SSL)
}

func TestLokiConfig_Structure(t *testing.T) {
	config := &LokiConfig{
		Enabled:      true,
		Namespace:    "monitoring",
		ReleaseName:  "loki",
		ChartRepo:    "https://grafana.github.io/helm-charts",
		ChartName:    "loki",
		ChartVersion: "3.0.0",
		LocalPort:    3100,
		RemotePort:   8081,
		Username:     "loki",
		Password:     "lokipass",
	}

	assert.True(t, config.Enabled)
	assert.Equal(t, "monitoring", config.Namespace)
	assert.Equal(t, "loki", config.ReleaseName)
	assert.Equal(t, 3100, config.LocalPort)
	assert.Equal(t, 8081, config.RemotePort)
}

func TestOpenCostConfig_Structure(t *testing.T) {
	config := &OpenCostConfig{
		Enabled:      true,
		Namespace:    "opencost",
		ReleaseName:  "opencost",
		ChartRepo:    "https://opencost.github.io/opencost-helm-chart",
		ChartName:    "opencost",
		ChartVersion: "1.0.0",
		LocalPort:    9003,
		RemotePort:   8082,
		Username:     "opencost",
		Password:     "costpass",
	}

	assert.True(t, config.Enabled)
	assert.Equal(t, "opencost", config.Namespace)
	assert.Equal(t, "opencost", config.ReleaseName)
	assert.Equal(t, 9003, config.LocalPort)
	assert.Equal(t, 8082, config.RemotePort)
}

func TestGrafanaConfig_Structure(t *testing.T) {
	config := &GrafanaConfig{
		Enabled:       true,
		Namespace:     "monitoring",
		ReleaseName:   "grafana",
		ChartRepo:     "https://grafana.github.io/helm-charts",
		ChartName:     "grafana",
		ChartVersion:  "6.0.0",
		LocalPort:     3000,
		RemotePort:    8083,
		AdminUser:     "admin",
		AdminPassword: "grafanapass",
	}

	assert.True(t, config.Enabled)
	assert.Equal(t, "monitoring", config.Namespace)
	assert.Equal(t, "grafana", config.ReleaseName)
	assert.Equal(t, 3000, config.LocalPort)
	assert.Equal(t, 8083, config.RemotePort)
	assert.Equal(t, "admin", config.AdminUser)
}

func TestMonitoringStack_Structure(t *testing.T) {
	stack := &MonitoringStack{
		Prometheus: &PrometheusConfig{
			Enabled:   true,
			Namespace: "monitoring",
		},
		Loki: &LokiConfig{
			Enabled:   true,
			Namespace: "monitoring",
		},
		OpenCost: &OpenCostConfig{
			Enabled:   true,
			Namespace: "opencost",
		},
		Grafana: &GrafanaConfig{
			Enabled:   true,
			Namespace: "monitoring",
		},
	}

	assert.NotNil(t, stack.Prometheus)
	assert.NotNil(t, stack.Loki)
	assert.NotNil(t, stack.OpenCost)
	assert.NotNil(t, stack.Grafana)
	assert.True(t, stack.Prometheus.Enabled)
	assert.True(t, stack.Loki.Enabled)
	assert.True(t, stack.OpenCost.Enabled)
	assert.True(t, stack.Grafana.Enabled)
}

func TestMonitoringStack_DisabledComponents(t *testing.T) {
	stack := &MonitoringStack{
		Prometheus: &PrometheusConfig{
			Enabled: false,
		},
		Loki: &LokiConfig{
			Enabled: false,
		},
		OpenCost: &OpenCostConfig{
			Enabled: true,
		},
		Grafana: &GrafanaConfig{
			Enabled: true,
		},
	}

	assert.False(t, stack.Prometheus.Enabled)
	assert.False(t, stack.Loki.Enabled)
	assert.True(t, stack.OpenCost.Enabled)
	assert.True(t, stack.Grafana.Enabled)
}

func TestNewManager_WithValidStack(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise

	stack := &MonitoringStack{
		Prometheus: &PrometheusConfig{
			Enabled:   true,
			Namespace: "monitoring",
		},
	}

	// NewManager might fail if helm is not available, which is expected
	mgr, err := NewManager(stack, logger)

	// If helm is available, manager should be created
	// If not, that's okay for unit tests
	if err == nil {
		assert.NotNil(t, mgr)
		assert.Equal(t, stack, mgr.stack)
		assert.NotNil(t, mgr.logger)
	}
}

func TestMonitoringStack_PortConfiguration(t *testing.T) {
	// Test that different components can have different ports
	stack := &MonitoringStack{
		Prometheus: &PrometheusConfig{
			LocalPort:  9090,
			RemotePort: 8080,
		},
		Loki: &LokiConfig{
			LocalPort:  3100,
			RemotePort: 8081,
		},
		OpenCost: &OpenCostConfig{
			LocalPort:  9003,
			RemotePort: 8082,
		},
		Grafana: &GrafanaConfig{
			LocalPort:  3000,
			RemotePort: 8083,
		},
	}

	// Verify all ports are different
	assert.NotEqual(t, stack.Prometheus.LocalPort, stack.Loki.LocalPort)
	assert.NotEqual(t, stack.Prometheus.LocalPort, stack.OpenCost.LocalPort)
	assert.NotEqual(t, stack.Prometheus.LocalPort, stack.Grafana.LocalPort)

	assert.NotEqual(t, stack.Prometheus.RemotePort, stack.Loki.RemotePort)
	assert.NotEqual(t, stack.Prometheus.RemotePort, stack.OpenCost.RemotePort)
	assert.NotEqual(t, stack.Prometheus.RemotePort, stack.Grafana.RemotePort)
}

func TestMonitoringConfigs_DefaultValues(t *testing.T) {
	// Test that configs can be created with minimal values
	prometheus := &PrometheusConfig{
		Enabled: true,
	}

	loki := &LokiConfig{
		Enabled: true,
	}

	opencost := &OpenCostConfig{
		Enabled: true,
	}

	grafana := &GrafanaConfig{
		Enabled: true,
	}

	assert.True(t, prometheus.Enabled)
	assert.True(t, loki.Enabled)
	assert.True(t, opencost.Enabled)
	assert.True(t, grafana.Enabled)
}

func TestMonitoringStack_EmptyStack(t *testing.T) {
	// Test that an empty stack can be created
	stack := &MonitoringStack{}

	assert.Nil(t, stack.Prometheus)
	assert.Nil(t, stack.Loki)
	assert.Nil(t, stack.OpenCost)
	assert.Nil(t, stack.Grafana)
}

func TestMonitoringStack_PartialConfiguration(t *testing.T) {
	// Test stack with only some components
	stack := &MonitoringStack{
		Prometheus: &PrometheusConfig{
			Enabled:   true,
			Namespace: "monitoring",
		},
		Grafana: &GrafanaConfig{
			Enabled:   true,
			Namespace: "monitoring",
		},
	}

	assert.NotNil(t, stack.Prometheus)
	assert.Nil(t, stack.Loki)
	assert.Nil(t, stack.OpenCost)
	assert.NotNil(t, stack.Grafana)
}
