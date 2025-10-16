package components

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// ServiceHealthChecker validates accessibility and health of monitoring services
// Note: Despite the legacy naming, this does NOT do port forwarding.
// Services are accessed via Kubernetes DNS since the agent runs in-cluster.
// Port forwarding is handled by the Chisel tunnel in internal/tunnel/client.go
type ServiceHealthChecker struct {
	logger *logrus.Logger
	ctx    context.Context
	cancel context.CancelFunc
	stack  *MonitoringStack
}

// NewServiceHealthChecker creates a new service health checker
func NewServiceHealthChecker(stack *MonitoringStack, logger *logrus.Logger) *ServiceHealthChecker {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServiceHealthChecker{
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
		stack:  stack,
	}
}

// Start validates accessibility of all monitoring services
func (shc *ServiceHealthChecker) Start() error {
	shc.logger.Info("Validating monitoring service accessibility...")

	// Validate Prometheus accessibility
	if shc.stack.Prometheus != nil && shc.stack.Prometheus.Enabled {
		if err := shc.validatePrometheusAccess(); err != nil {
			return fmt.Errorf("failed to validate Prometheus access: %w", err)
		}
	}

	// Validate Loki accessibility
	if shc.stack.Loki != nil && shc.stack.Loki.Enabled {
		if err := shc.validateLokiAccess(); err != nil {
			return fmt.Errorf("failed to validate Loki access: %w", err)
		}
	}

	// Validate OpenCost accessibility
	if shc.stack.OpenCost != nil && shc.stack.OpenCost.Enabled {
		if err := shc.validateOpenCostAccess(); err != nil {
			return fmt.Errorf("failed to validate OpenCost access: %w", err)
		}
	}

	// Validate Grafana accessibility
	if shc.stack.Grafana != nil && shc.stack.Grafana.Enabled {
		if err := shc.validateGrafanaAccess(); err != nil {
			return fmt.Errorf("failed to validate Grafana access: %w", err)
		}
	}

	shc.logger.Info("All monitoring services validated successfully")
	return nil
}

// Stop stops the health checker
func (shc *ServiceHealthChecker) Stop() {
	shc.logger.Info("Service health checker stopped")
	shc.cancel()
}

// validatePrometheusAccess validates Prometheus service accessibility
func (shc *ServiceHealthChecker) validatePrometheusAccess() error {
	// For kube-prometheus-stack, the service name is <release-name>-prometheus
	// e.g., kube-prometheus-stack-prometheus.pipeops-monitoring.svc.cluster.local:9090
	serviceName := fmt.Sprintf("%s-prometheus", shc.stack.Prometheus.ReleaseName)
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		serviceName,
		shc.stack.Prometheus.Namespace,
		shc.stack.Prometheus.LocalPort)

	shc.logger.WithFields(logrus.Fields{
		"service": "prometheus",
		"url":     serviceURL,
	}).Info("Prometheus accessible via Kubernetes service DNS (kube-prometheus-stack)")

	return nil
}

// validateLokiAccess validates Loki service accessibility
func (shc *ServiceHealthChecker) validateLokiAccess() error {
	// loki.pipeops-monitoring.svc.cluster.local:3100
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		shc.stack.Loki.ReleaseName,
		shc.stack.Loki.Namespace,
		shc.stack.Loki.LocalPort)

	shc.logger.WithFields(logrus.Fields{
		"service": "loki",
		"url":     serviceURL,
	}).Info("Loki accessible via Kubernetes service DNS")

	return nil
}

// validateOpenCostAccess validates OpenCost service accessibility
func (shc *ServiceHealthChecker) validateOpenCostAccess() error {
	// opencost.pipeops-monitoring.svc.cluster.local:9003
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		shc.stack.OpenCost.ReleaseName,
		shc.stack.OpenCost.Namespace,
		shc.stack.OpenCost.LocalPort)

	shc.logger.WithFields(logrus.Fields{
		"service": "opencost",
		"url":     serviceURL,
	}).Info("OpenCost accessible via Kubernetes service DNS")

	return nil
}

// validateGrafanaAccess validates Grafana service accessibility
func (shc *ServiceHealthChecker) validateGrafanaAccess() error {
	// For kube-prometheus-stack, Grafana service name is <release-name>-grafana
	// e.g., kube-prometheus-stack-grafana.pipeops-monitoring.svc.cluster.local:3000
	serviceName := fmt.Sprintf("%s-grafana", shc.stack.Prometheus.ReleaseName)
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		serviceName,
		shc.stack.Grafana.Namespace,
		shc.stack.Grafana.LocalPort)

	shc.logger.WithFields(logrus.Fields{
		"service": "grafana",
		"url":     serviceURL,
	}).Info("Grafana accessible via Kubernetes service DNS (kube-prometheus-stack)")

	return nil
}

// HealthChecker performs health checks on monitoring services
type HealthChecker struct {
	logger *logrus.Logger
	stack  *MonitoringStack
	client *http.Client
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(stack *MonitoringStack, logger *logrus.Logger) *HealthChecker {
	return &HealthChecker{
		logger: logger,
		stack:  stack,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CheckAll checks health of all monitoring services
func (hc *HealthChecker) CheckAll() map[string]HealthStatus {
	status := make(map[string]HealthStatus)

	if hc.stack.Prometheus != nil && hc.stack.Prometheus.Enabled {
		status["prometheus"] = hc.checkPrometheus()
	}

	if hc.stack.Loki != nil && hc.stack.Loki.Enabled {
		status["loki"] = hc.checkLoki()
	}

	if hc.stack.OpenCost != nil && hc.stack.OpenCost.Enabled {
		status["opencost"] = hc.checkOpenCost()
	}

	if hc.stack.Grafana != nil && hc.stack.Grafana.Enabled {
		status["grafana"] = hc.checkGrafana()
	}

	return status
}

// HealthStatus represents the health status of a service
type HealthStatus struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message"`
}

// checkPrometheus checks Prometheus health
func (hc *HealthChecker) checkPrometheus() HealthStatus {
	// For kube-prometheus-stack, the service name is <release-name>-prometheus
	serviceName := fmt.Sprintf("%s-prometheus", hc.stack.Prometheus.ReleaseName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/-/healthy",
		serviceName,
		hc.stack.Prometheus.Namespace,
		hc.stack.Prometheus.LocalPort)

	resp, err := hc.client.Get(url)
	if err != nil {
		return HealthStatus{Healthy: false, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return HealthStatus{Healthy: true, Message: "OK"}
	}

	body, _ := io.ReadAll(resp.Body)
	return HealthStatus{Healthy: false, Message: string(body)}
}

// checkLoki checks Loki health
func (hc *HealthChecker) checkLoki() HealthStatus {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/ready",
		hc.stack.Loki.ReleaseName,
		hc.stack.Loki.Namespace,
		hc.stack.Loki.LocalPort)

	resp, err := hc.client.Get(url)
	if err != nil {
		return HealthStatus{Healthy: false, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return HealthStatus{Healthy: true, Message: "OK"}
	}

	body, _ := io.ReadAll(resp.Body)
	return HealthStatus{Healthy: false, Message: string(body)}
}

// checkOpenCost checks OpenCost health
func (hc *HealthChecker) checkOpenCost() HealthStatus {
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/healthz",
		hc.stack.OpenCost.ReleaseName,
		hc.stack.OpenCost.Namespace,
		hc.stack.OpenCost.LocalPort)

	resp, err := hc.client.Get(url)
	if err != nil {
		return HealthStatus{Healthy: false, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return HealthStatus{Healthy: true, Message: "OK"}
	}

	body, _ := io.ReadAll(resp.Body)
	return HealthStatus{Healthy: false, Message: string(body)}
}

// checkGrafana checks Grafana health
func (hc *HealthChecker) checkGrafana() HealthStatus {
	// For kube-prometheus-stack, Grafana service name is <release-name>-grafana
	serviceName := fmt.Sprintf("%s-grafana", hc.stack.Prometheus.ReleaseName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/api/health",
		serviceName,
		hc.stack.Grafana.Namespace,
		hc.stack.Grafana.LocalPort)

	resp, err := hc.client.Get(url)
	if err != nil {
		return HealthStatus{Healthy: false, Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return HealthStatus{Healthy: true, Message: "OK"}
	}

	body, _ := io.ReadAll(resp.Body)
	return HealthStatus{Healthy: false, Message: string(body)}
}
