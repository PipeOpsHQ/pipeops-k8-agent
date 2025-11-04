package components

import (
	"crypto/rand"
	"encoding/base64"
)

// DefaultMonitoringStack returns the default monitoring stack configuration
func DefaultMonitoringStack() *MonitoringStack {
	return &MonitoringStack{
		Prometheus: &PrometheusConfig{
			Enabled:      true,
			Namespace:    "pipeops-monitoring",
			ReleaseName:  "kube-prometheus-stack",
			ChartRepo:    "https://prometheus-community.github.io/helm-charts",
			ChartName:    "prometheus-community/kube-prometheus-stack",
			ChartVersion: "", // latest
			LocalPort:    9090,
			RemotePort:   19090, // Port on control plane side
			Username:     "admin",
			Password:     generatePassword(),
			SSL:          true,
			// Storage configuration for persistence
			StorageClass:      "",     // Use cluster's default StorageClass (empty = default)
			StorageSize:       "20Gi", // Default storage size for Prometheus
			RetentionPeriod:   "15d",  // Data retention period
			EnablePersistence: true,   // Enable persistent storage
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
			// Storage configuration for persistence
			StorageClass:      "",     // Use cluster's default StorageClass (empty = default)
			StorageSize:       "10Gi", // Default storage size for Loki
			EnablePersistence: true,   // Enable persistent storage
		},
		Grafana: &GrafanaConfig{
			Enabled:       true,
			Namespace:     "pipeops-monitoring",
			ReleaseName:   "kube-prometheus-stack", // Same as Prometheus since it's part of the stack
			ChartRepo:     "",                      // Not used, installed with kube-prometheus-stack
			ChartName:     "",                      // Not used, installed with kube-prometheus-stack
			ChartVersion:  "",                      // Not used, installed with kube-prometheus-stack
			LocalPort:     3000,
			RemotePort:    13000, // Port on control plane side
			AdminUser:     "admin",
			AdminPassword: generatePassword(),
			// Storage configuration for persistence
			StorageClass:      "",    // Default StorageClass
			StorageSize:       "5Gi", // Default storage size for Grafana
			EnablePersistence: true,  // Enable persistent storage
		},
	}
}

// generatePassword generates a cryptographically secure random password for monitoring services
func generatePassword() string {
	// Generate 32 bytes of random data (256 bits of entropy)
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		// Fallback to a default password if random generation fails
		// This should rarely happen, but provides a safe fallback
		return "changeme123!"
	}

	// Encode to base64 for a URL-safe, printable password
	// This results in a 44-character password with high entropy
	password := base64.URLEncoding.EncodeToString(randomBytes)

	// Remove padding characters for cleaner password
	return password[:43]
}
