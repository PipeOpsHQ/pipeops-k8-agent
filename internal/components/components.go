package components

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ComponentInstaller manages installation of essential Kubernetes components
type ComponentInstaller struct {
	installer *HelmInstaller
	k8sClient *kubernetes.Clientset
	logger    *logrus.Logger
}

// ComponentConfig holds configuration for a component
type ComponentConfig struct {
	Name      string
	Namespace string
	Enabled   bool
}

// NewComponentInstaller creates a new component installer
func NewComponentInstaller(installer *HelmInstaller, k8sClient *kubernetes.Clientset, logger *logrus.Logger) *ComponentInstaller {
	return &ComponentInstaller{
		installer: installer,
		k8sClient: k8sClient,
		logger:    logger,
	}
}

// InstallEssentialComponents installs all essential Kubernetes components
func (ci *ComponentInstaller) InstallEssentialComponents(ctx context.Context) error {
	ci.logger.Info("Installing essential Kubernetes components...")

	// Install Metrics Server
	if err := ci.InstallMetricsServer(ctx); err != nil {
		ci.logger.WithError(err).Warn("Failed to install Metrics Server (non-fatal)")
	}

	// Install kube-state-metrics
	if err := ci.InstallKubeStateMetrics(ctx); err != nil {
		ci.logger.WithError(err).Warn("Failed to install kube-state-metrics (non-fatal)")
	}

	// Install VPA (Vertical Pod Autoscaler)
	if err := ci.InstallVPA(ctx); err != nil {
		ci.logger.WithError(err).Warn("Failed to install VPA (non-fatal)")
	}

	// // Install Node Exporter (for detailed node metrics)
	// if err := ci.InstallNodeExporter(ctx); err != nil {
	// 	ci.logger.WithError(err).Warn("Failed to install Node Exporter (non-fatal)")
	// }

	ci.logger.Info("✓ Essential components installation completed")
	return nil
}

// InstallMetricsServer installs the Kubernetes Metrics Server
func (ci *ComponentInstaller) InstallMetricsServer(ctx context.Context) error {
	ci.logger.Info("Installing Metrics Server...")

	// Check if already installed
	if ci.isMetricsServerInstalled(ctx) {
		ci.logger.Info("✓ Metrics Server already installed")
		return nil
	}

	namespace := "kube-system"
	chartRepo := "https://kubernetes-sigs.github.io/metrics-server/"
	chartName := "metrics-server/metrics-server"

	// Add Helm repository
	if err := ci.installer.addRepo(ctx, chartName, chartRepo); err != nil {
		return fmt.Errorf("failed to add metrics-server repo: %w", err)
	}

	// Prepare values for Metrics Server
	values := map[string]interface{}{
		"args": []string{
			"--cert-dir=/tmp",
			"--kubelet-preferred-address-types=InternalIP,ExternalIP,Hostname",
			"--kubelet-use-node-status-port",
			"--metric-resolution=15s",
			"--kubelet-insecure-tls", // Required for minikube/self-signed certs
		},
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    "100m",
				"memory": "200Mi",
			},
			"limits": map[string]interface{}{
				"cpu":    "200m",
				"memory": "400Mi",
			},
		},
		"hostNetwork": map[string]interface{}{
			"enabled": false,
		},
		"containerPort": 10250,
		"replicas":      1,
		"updateStrategy": map[string]interface{}{
			"type": "RollingUpdate",
		},
	}

	// Install the chart
	release := &HelmRelease{
		Name:      "metrics-server",
		Namespace: namespace,
		Chart:     chartName,
		Repo:      chartRepo,
		Version:   "", // latest stable
		Values:    values,
	}

	if err := ci.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install Metrics Server: %w", err)
	}

	// Wait for Metrics Server to be ready
	if err := ci.waitForDeployment(ctx, namespace, "metrics-server", 3*time.Minute); err != nil {
		return fmt.Errorf("Metrics Server did not become ready: %w", err)
	}

	ci.logger.Info("✓ Metrics Server installed successfully")
	return nil
}

// InstallKubeStateMetrics installs kube-state-metrics for cluster-level metrics
func (ci *ComponentInstaller) InstallKubeStateMetrics(ctx context.Context) error {
	ci.logger.Info("Installing kube-state-metrics...")

	// Check if already installed
	if ci.isKubeStateMetricsInstalled(ctx) {
		ci.logger.Info("✓ kube-state-metrics already installed")
		return nil
	}

	namespace := "kube-system"
	chartRepo := "https://prometheus-community.github.io/helm-charts"
	chartName := "prometheus-community/kube-state-metrics"

	// Add Helm repository
	if err := ci.installer.addRepo(ctx, chartName, chartRepo); err != nil {
		return fmt.Errorf("failed to add kube-state-metrics repo: %w", err)
	}

	// Prepare values for kube-state-metrics
	values := map[string]interface{}{
		"replicas": 1,
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"cpu":    "10m",
				"memory": "32Mi",
			},
			"limits": map[string]interface{}{
				"cpu":    "100m",
				"memory": "128Mi",
			},
		},
		"prometheus": map[string]interface{}{
			"monitor": map[string]interface{}{
				"enabled": false, // Enable if using Prometheus Operator
			},
		},
		"selfMonitor": map[string]interface{}{
			"enabled": true,
		},
		// Collectors to enable
		"collectors": []string{
			"certificatesigningrequests",
			"configmaps",
			"cronjobs",
			"daemonsets",
			"deployments",
			"endpoints",
			"horizontalpodautoscalers",
			"ingresses",
			"jobs",
			"leases",
			"limitranges",
			"mutatingwebhookconfigurations",
			"namespaces",
			"networkpolicies",
			"nodes",
			"persistentvolumeclaims",
			"persistentvolumes",
			"poddisruptionbudgets",
			"pods",
			"replicasets",
			"replicationcontrollers",
			"resourcequotas",
			"secrets",
			"services",
			"statefulsets",
			"storageclasses",
			"validatingwebhookconfigurations",
			"volumeattachments",
		},
	}

	// Install the chart
	release := &HelmRelease{
		Name:      "kube-state-metrics",
		Namespace: namespace,
		Chart:     chartName,
		Repo:      chartRepo,
		Version:   "", // latest stable
		Values:    values,
	}

	if err := ci.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install kube-state-metrics: %w", err)
	}

	// Wait for kube-state-metrics to be ready
	if err := ci.waitForDeployment(ctx, namespace, "kube-state-metrics", 2*time.Minute); err != nil {
		return fmt.Errorf("kube-state-metrics did not become ready: %w", err)
	}

	ci.logger.Info("✓ kube-state-metrics installed successfully")
	return nil
}

// InstallVPA installs Vertical Pod Autoscaler for automatic resource recommendations
func (ci *ComponentInstaller) InstallVPA(ctx context.Context) error {
	ci.logger.Info("Installing Vertical Pod Autoscaler (VPA)...")

	// Check if already installed
	if ci.isVPAInstalled(ctx) {
		ci.logger.Info("✓ VPA already installed")
		return nil
	}

	namespace := "kube-system"
	chartRepo := "https://charts.fairwinds.com/stable"
	chartName := "fairwinds-stable/vpa"

	// Add Helm repository
	if err := ci.installer.addRepo(ctx, chartName, chartRepo); err != nil {
		return fmt.Errorf("failed to add VPA repo: %w", err)
	}

	// Prepare values for VPA
	values := map[string]interface{}{
		"recommender": map[string]interface{}{
			"enabled": true,
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "50m",
					"memory": "100Mi",
				},
				"limits": map[string]interface{}{
					"cpu":    "200m",
					"memory": "512Mi",
				},
			},
		},
		"updater": map[string]interface{}{
			"enabled": true,
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "50m",
					"memory": "100Mi",
				},
				"limits": map[string]interface{}{
					"cpu":    "200m",
					"memory": "512Mi",
				},
			},
		},
		"admissionController": map[string]interface{}{
			"enabled": true,
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "50m",
					"memory": "100Mi",
				},
				"limits": map[string]interface{}{
					"cpu":    "200m",
					"memory": "512Mi",
				},
			},
			"generateCertificate": true, // Auto-generate TLS certificates
		},
		// Metrics server integration
		"metricsServer": map[string]interface{}{
			"enabled": false, // We're using separate metrics-server installation
		},
	}

	// Install the chart
	release := &HelmRelease{
		Name:      "vpa",
		Namespace: namespace,
		Chart:     chartName,
		Repo:      chartRepo,
		Version:   "", // latest stable
		Values:    values,
	}

	if err := ci.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install VPA: %w", err)
	}

	// Wait for VPA components to be ready
	components := []string{"vpa-recommender", "vpa-updater", "vpa-admission-controller"}
	for _, component := range components {
		ci.logger.WithField("component", component).Debug("Waiting for VPA component...")
		if err := ci.waitForDeployment(ctx, namespace, component, 2*time.Minute); err != nil {
			ci.logger.WithError(err).Warnf("VPA component %s did not become ready (may still be starting)", component)
		}
	}

	ci.logger.Info("✓ Vertical Pod Autoscaler installed successfully")
	return nil
}

// // InstallNodeExporter installs Prometheus Node Exporter for detailed node metrics
// func (ci *ComponentInstaller) InstallNodeExporter(ctx context.Context) error {
// 	ci.logger.Info("Installing Prometheus Node Exporter...")

// 	// Check if already installed
// 	if ci.isNodeExporterInstalled(ctx) {
// 		ci.logger.Info("✓ Node Exporter already installed")
// 		return nil
// 	}

// 	namespace := "kube-system"
// 	chartRepo := "https://prometheus-community.github.io/helm-charts"
// 	chartName := "prometheus-community/prometheus-node-exporter"

// 	// Add Helm repository
// 	if err := ci.installer.addRepo(ctx, chartName, chartRepo); err != nil {
// 		return fmt.Errorf("failed to add node-exporter repo: %w", err)
// 	}

// 	// Prepare values for Node Exporter
// 	values := map[string]interface{}{
// 		"hostNetwork": true,
// 		"hostPID":     true,
// 		"hostRootFsMount": map[string]interface{}{
// 			"enabled": true,
// 		},
// 		"resources": map[string]interface{}{
// 			"requests": map[string]interface{}{
// 				"cpu":    "50m",
// 				"memory": "32Mi",
// 			},
// 			"limits": map[string]interface{}{
// 				"cpu":    "200m",
// 				"memory": "128Mi",
// 			},
// 		},
// 		"service": map[string]interface{}{
// 			"type": "ClusterIP",
// 			"port": 9100,
// 			"annotations": map[string]interface{}{
// 				"prometheus.io/scrape": "true",
// 			},
// 		},
// 		"prometheus": map[string]interface{}{
// 			"monitor": map[string]interface{}{
// 				"enabled": false, // Enable if using Prometheus Operator
// 			},
// 		},
// 		// DaemonSet configuration - runs on all nodes
// 		"updateStrategy": map[string]interface{}{
// 			"type": "RollingUpdate",
// 			"rollingUpdate": map[string]interface{}{
// 				"maxUnavailable": 1,
// 			},
// 		},
// 	}

// 	// Install the chart
// 	release := &HelmRelease{
// 		Name:      "prometheus-node-exporter",
// 		Namespace: namespace,
// 		Chart:     chartName,
// 		Repo:      chartRepo,
// 		Version:   "", // latest stable
// 		Values:    values,
// 	}

// 	if err := ci.installer.Install(ctx, release); err != nil {
// 		return fmt.Errorf("failed to install Node Exporter: %w", err)
// 	}

// 	// Wait for Node Exporter to be ready (it's a DaemonSet)
// 	if err := ci.waitForDaemonSet(ctx, namespace, "prometheus-node-exporter", 2*time.Minute); err != nil {
// 		return fmt.Errorf("Node Exporter did not become ready: %w", err)
// 	}

// 	ci.logger.Info("✓ Node Exporter installed successfully")
// 	return nil
// }

// Helper functions to check if components are installed

func (ci *ComponentInstaller) isMetricsServerInstalled(ctx context.Context) bool {
	_, err := ci.k8sClient.AppsV1().Deployments("kube-system").Get(
		ctx,
		"metrics-server",
		metav1.GetOptions{},
	)
	return err == nil
}

func (ci *ComponentInstaller) isKubeStateMetricsInstalled(ctx context.Context) bool {
	_, err := ci.k8sClient.AppsV1().Deployments("kube-system").Get(
		ctx,
		"kube-state-metrics",
		metav1.GetOptions{},
	)
	return err == nil
}

func (ci *ComponentInstaller) isVPAInstalled(ctx context.Context) bool {
	// Check if VPA recommender deployment exists
	_, err := ci.k8sClient.AppsV1().Deployments("kube-system").Get(
		ctx,
		"vpa-recommender",
		metav1.GetOptions{},
	)
	return err == nil
}

func (ci *ComponentInstaller) isNodeExporterInstalled(ctx context.Context) bool {
	_, err := ci.k8sClient.AppsV1().DaemonSets("kube-system").Get(
		ctx,
		"prometheus-node-exporter",
		metav1.GetOptions{},
	)
	return err == nil
}

// waitForDeployment waits for a deployment to be ready
func (ci *ComponentInstaller) waitForDeployment(ctx context.Context, namespace, name string, timeout time.Duration) error {
	ci.logger.WithFields(logrus.Fields{
		"deployment": name,
		"namespace":  namespace,
	}).Debug("Waiting for deployment to be ready...")

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for deployment %s/%s", namespace, name)
		case <-ticker.C:
			deployment, err := ci.k8sClient.AppsV1().Deployments(namespace).Get(
				context.Background(),
				name,
				metav1.GetOptions{},
			)
			if err != nil {
				ci.logger.WithError(err).Debug("Deployment not found yet")
				continue
			}

			if deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == deployment.Status.Replicas {
				ci.logger.WithField("deployment", name).Debug("Deployment is ready")
				return nil
			}

			ci.logger.WithFields(logrus.Fields{
				"deployment": name,
				"ready":      deployment.Status.ReadyReplicas,
				"expected":   deployment.Status.Replicas,
			}).Debug("Waiting for deployment replicas to be ready")
		}
	}
}

// waitForDaemonSet waits for a DaemonSet to be ready
func (ci *ComponentInstaller) waitForDaemonSet(ctx context.Context, namespace, name string, timeout time.Duration) error {
	ci.logger.WithFields(logrus.Fields{
		"daemonset": name,
		"namespace": namespace,
	}).Debug("Waiting for DaemonSet to be ready...")

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout waiting for DaemonSet %s/%s", namespace, name)
		case <-ticker.C:
			daemonset, err := ci.k8sClient.AppsV1().DaemonSets(namespace).Get(
				context.Background(),
				name,
				metav1.GetOptions{},
			)
			if err != nil {
				ci.logger.WithError(err).Debug("DaemonSet not found yet")
				continue
			}

			if daemonset.Status.NumberReady > 0 && daemonset.Status.NumberReady == daemonset.Status.DesiredNumberScheduled {
				ci.logger.WithField("daemonset", name).Debug("DaemonSet is ready")
				return nil
			}

			ci.logger.WithFields(logrus.Fields{
				"daemonset": name,
				"ready":     daemonset.Status.NumberReady,
				"expected":  daemonset.Status.DesiredNumberScheduled,
			}).Debug("Waiting for DaemonSet pods to be ready")
		}
	}
}

// GetMetricsServerStatus returns the status of Metrics Server
func (ci *ComponentInstaller) GetMetricsServerStatus(ctx context.Context) (*ComponentStatus, error) {
	deployment, err := ci.k8sClient.AppsV1().Deployments("kube-system").Get(
		ctx,
		"metrics-server",
		metav1.GetOptions{},
	)
	if err != nil {
		return &ComponentStatus{
			Name:      "metrics-server",
			Installed: false,
			Ready:     false,
		}, nil
	}

	return &ComponentStatus{
		Name:      "metrics-server",
		Installed: true,
		Ready:     deployment.Status.ReadyReplicas > 0,
		Replicas:  deployment.Status.Replicas,
		Available: deployment.Status.AvailableReplicas,
	}, nil
}

// ComponentStatus represents the status of a component
type ComponentStatus struct {
	Name      string
	Installed bool
	Ready     bool
	Replicas  int32
	Available int32
}

// GetAllComponentsStatus returns status of all essential components
func (ci *ComponentInstaller) GetAllComponentsStatus(ctx context.Context) ([]ComponentStatus, error) {
	var statuses []ComponentStatus

	// Metrics Server
	if status, err := ci.getDeploymentStatus(ctx, "kube-system", "metrics-server"); err == nil {
		statuses = append(statuses, status)
	}

	// kube-state-metrics
	if status, err := ci.getDeploymentStatus(ctx, "kube-system", "kube-state-metrics"); err == nil {
		statuses = append(statuses, status)
	}

	// VPA components
	vpaComponents := []string{"vpa-recommender", "vpa-updater", "vpa-admission-controller"}
	for _, component := range vpaComponents {
		if status, err := ci.getDeploymentStatus(ctx, "kube-system", component); err == nil {
			statuses = append(statuses, status)
		}
	}

	// Node Exporter (DaemonSet)
	if status, err := ci.getDaemonSetStatus(ctx, "kube-system", "prometheus-node-exporter"); err == nil {
		statuses = append(statuses, status)
	}

	return statuses, nil
}

func (ci *ComponentInstaller) getDeploymentStatus(ctx context.Context, namespace, name string) (ComponentStatus, error) {
	deployment, err := ci.k8sClient.AppsV1().Deployments(namespace).Get(
		ctx,
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		return ComponentStatus{
			Name:      name,
			Installed: false,
			Ready:     false,
		}, err
	}

	return ComponentStatus{
		Name:      name,
		Installed: true,
		Ready:     deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == deployment.Status.Replicas,
		Replicas:  deployment.Status.Replicas,
		Available: deployment.Status.AvailableReplicas,
	}, nil
}

func (ci *ComponentInstaller) getDaemonSetStatus(ctx context.Context, namespace, name string) (ComponentStatus, error) {
	daemonset, err := ci.k8sClient.AppsV1().DaemonSets(namespace).Get(
		ctx,
		name,
		metav1.GetOptions{},
	)
	if err != nil {
		return ComponentStatus{
			Name:      name,
			Installed: false,
			Ready:     false,
		}, err
	}

	return ComponentStatus{
		Name:      name,
		Installed: true,
		Ready:     daemonset.Status.NumberReady > 0 && daemonset.Status.NumberReady == daemonset.Status.DesiredNumberScheduled,
		Replicas:  daemonset.Status.DesiredNumberScheduled,
		Available: daemonset.Status.NumberReady,
	}, nil
}

// VerifyMetricsAPI verifies that the Metrics API is accessible
func (ci *ComponentInstaller) VerifyMetricsAPI(ctx context.Context) error {
	ci.logger.Debug("Verifying Metrics API accessibility...")

	// Try to get node metrics
	_, err := ci.k8sClient.RESTClient().
		Get().
		AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").
		DoRaw(ctx)

	if err != nil {
		return fmt.Errorf("Metrics API not accessible: %w", err)
	}

	ci.logger.Info("✓ Metrics API is accessible")
	return nil
}
