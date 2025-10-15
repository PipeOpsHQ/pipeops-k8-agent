package monitoring

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// PortForwarder manages kubectl port-forwards for monitoring services
type PortForwarder struct {
	logger *logrus.Logger
	ctx    context.Context
	cancel context.CancelFunc
	stack  *MonitoringStack
}

// NewPortForwarder creates a new port forwarder
func NewPortForwarder(stack *MonitoringStack, logger *logrus.Logger) *PortForwarder {
	ctx, cancel := context.WithCancel(context.Background())
	return &PortForwarder{
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
		stack:  stack,
	}
}

// Start starts all port forwards
func (pf *PortForwarder) Start() error {
	pf.logger.Info("Starting port forwards for monitoring services...")

	// Start Prometheus port forward
	if pf.stack.Prometheus != nil && pf.stack.Prometheus.Enabled {
		if err := pf.startPrometheusPortForward(); err != nil {
			return fmt.Errorf("failed to start Prometheus port forward: %w", err)
		}
	}

	// Start Loki port forward
	if pf.stack.Loki != nil && pf.stack.Loki.Enabled {
		if err := pf.startLokiPortForward(); err != nil {
			return fmt.Errorf("failed to start Loki port forward: %w", err)
		}
	}

	// Start OpenCost port forward
	if pf.stack.OpenCost != nil && pf.stack.OpenCost.Enabled {
		if err := pf.startOpenCostPortForward(); err != nil {
			return fmt.Errorf("failed to start OpenCost port forward: %w", err)
		}
	}

	// Start Grafana port forward
	if pf.stack.Grafana != nil && pf.stack.Grafana.Enabled {
		if err := pf.startGrafanaPortForward(); err != nil {
			return fmt.Errorf("failed to start Grafana port forward: %w", err)
		}
	}

	pf.logger.Info("All port forwards started successfully")
	return nil
}

// Stop stops all port forwards
func (pf *PortForwarder) Stop() {
	pf.logger.Info("Port forwarder stopped (services accessible via k8s service DNS)")
	pf.cancel()
}

// startPrometheusPortForward validates Prometheus service accessibility
func (pf *PortForwarder) startPrometheusPortForward() error {
	// When agent runs in-cluster, services are accessible via DNS
	// prometheus-server.pipeops-monitoring.svc.cluster.local:9090
	serviceName := fmt.Sprintf("%s-server", pf.stack.Prometheus.ReleaseName)
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		serviceName,
		pf.stack.Prometheus.Namespace,
		pf.stack.Prometheus.LocalPort)

	pf.logger.WithFields(logrus.Fields{
		"service": "prometheus",
		"url":     serviceURL,
	}).Info("Prometheus accessible via Kubernetes service DNS")

	return nil
}

// startLokiPortForward validates Loki service accessibility
func (pf *PortForwarder) startLokiPortForward() error {
	// loki.pipeops-monitoring.svc.cluster.local:3100
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		pf.stack.Loki.ReleaseName,
		pf.stack.Loki.Namespace,
		pf.stack.Loki.LocalPort)

	pf.logger.WithFields(logrus.Fields{
		"service": "loki",
		"url":     serviceURL,
	}).Info("Loki accessible via Kubernetes service DNS")

	return nil
}

// startOpenCostPortForward validates OpenCost service accessibility
func (pf *PortForwarder) startOpenCostPortForward() error {
	// opencost.pipeops-monitoring.svc.cluster.local:9003
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		pf.stack.OpenCost.ReleaseName,
		pf.stack.OpenCost.Namespace,
		pf.stack.OpenCost.LocalPort)

	pf.logger.WithFields(logrus.Fields{
		"service": "opencost",
		"url":     serviceURL,
	}).Info("OpenCost accessible via Kubernetes service DNS")

	return nil
}

// startGrafanaPortForward validates Grafana service accessibility
func (pf *PortForwarder) startGrafanaPortForward() error {
	// grafana.pipeops-monitoring.svc.cluster.local:3000
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		pf.stack.Grafana.ReleaseName,
		pf.stack.Grafana.Namespace,
		pf.stack.Grafana.LocalPort)

	pf.logger.WithFields(logrus.Fields{
		"service": "grafana",
		"url":     serviceURL,
	}).Info("Grafana accessible via Kubernetes service DNS")

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
	// Use Kubernetes service DNS when running in-cluster
	serviceName := fmt.Sprintf("%s-server", hc.stack.Prometheus.ReleaseName)
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
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/api/health",
		hc.stack.Grafana.ReleaseName,
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
