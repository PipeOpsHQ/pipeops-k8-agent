package components

import (
	"context"
	"fmt"
	"github.com/pipeops/pipeops-vm-agent/internal/helm"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	vpaNamespace      = "pipeops-system"
	coreDNSNamespace  = "kube-system"
	coreDNSConfigName = "coredns"
)

const pipeOpsRewriteBlock = `        # Format 1: service.namespace.svc.pipeops.internal
        # Example: myapp.production.svc.pipeops.internal -> myapp.production.svc.cluster.local
        rewrite stop {
            name regex ^(.+)\.svc\.pipeops\.internal\.$ {1}.svc.cluster.local.
            answer auto
        }

        # Format 2: service.namespace.pipeops.internal (auto-add .svc)
        # Example: myapp.production.pipeops.internal -> myapp.production.svc.cluster.local
        # Also handles: pod-0.myapp.production.pipeops.internal (StatefulSet pods)
        rewrite stop {
            name regex ^([a-z0-9-]+)\.([a-z0-9-]+)\.pipeops\.internal\.$ {1}.{2}.svc.cluster.local.
            answer auto
        }

        # Format 3: Headless service pod with dots
        # Example: pod-0.myapp.production.pipeops.internal
        rewrite stop {
            name regex ^([a-z0-9-]+\.[a-z0-9-]+)\.([a-z0-9-]+)\.pipeops\.internal\.$ {1}.{2}.svc.cluster.local.
            answer auto
        }

        # Fallback: any other *.pipeops.internal -> *.cluster.local
        # Handles edge cases and multi-level subdomains
        rewrite stop {
            name suffix .pipeops.internal. .cluster.local.
            answer auto
        }`

// ComponentInstaller manages installation of essential Kubernetes components
type ComponentInstaller struct {
	installer *helm.HelmInstaller
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
func NewComponentInstaller(installer *helm.HelmInstaller, k8sClient *kubernetes.Clientset, logger *logrus.Logger) *ComponentInstaller {
	return &ComponentInstaller{
		installer: installer,
		k8sClient: k8sClient,
		logger:    logger,
	}
}

// InstallEssentialComponents installs all essential Kubernetes components
func (ci *ComponentInstaller) InstallEssentialComponents(ctx context.Context) error {
	metricsInstalled := ci.isMetricsServerInstalled(ctx)
	vpaInstalled := ci.isVPAInstalled(ctx, vpaNamespace)

	if metricsInstalled && vpaInstalled {
		ci.logger.Info("✓ Essential components already installed")
		return nil
	}

	ci.logger.Info("Installing essential Kubernetes components...")

	if metricsInstalled {
		ci.logger.Info("✓ Metrics Server already installed")
	} else if err := ci.InstallMetricsServer(ctx); err != nil {
		ci.logger.WithError(err).Warn("Failed to install Metrics Server (non-fatal)")
	}

	if vpaInstalled {
		ci.logger.WithField("namespace", vpaNamespace).Info("✓ VPA already installed")
	} else if err := ci.InstallVPA(ctx); err != nil {
		ci.logger.WithError(err).Warn("Failed to install VPA (non-fatal)")
	}

	if err := ci.installCoreDNSRewrite(ctx); err != nil {
		ci.logger.WithError(err).Warn("Failed to configure CoreDNS rewrite (non-fatal)")
	}

	ci.logger.Info("✓ Essential components installation completed")
	return nil
}

// InstallMetricsServer installs the Kubernetes Metrics Server
func (ci *ComponentInstaller) InstallMetricsServer(ctx context.Context) error {
	// Check if already installed
	if ci.isMetricsServerInstalled(ctx) {
		ci.logger.Info("✓ Metrics Server already installed")
		return nil
	}

	ci.logger.Info("Installing Metrics Server...")

	namespace := "kube-system"
	chartRepo := "https://kubernetes-sigs.github.io/metrics-server/"
	chartName := "metrics-server/metrics-server"

	// Add Helm repository
	if err := ci.installer.AddRepo(ctx, chartName, chartRepo); err != nil {
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
	release := &helm.HelmRelease{
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
		return fmt.Errorf("metrics Server did not become ready: %w", err)
	}

	ci.logger.Info("✓ Metrics Server installed successfully")
	return nil
}

// installCoreDNSRewrite ensures CoreDNS knows how to resolve *.pipeops.internal
func (ci *ComponentInstaller) installCoreDNSRewrite(ctx context.Context) error {
	if ci.k8sClient == nil {
		return fmt.Errorf("kubernetes client unavailable")
	}

	cm, err := ci.k8sClient.CoreV1().ConfigMaps(coreDNSNamespace).Get(ctx, coreDNSConfigName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			ci.logger.WithField("configmap", coreDNSConfigName).Debug("CoreDNS ConfigMap not found; skipping rewrite configuration")
			return nil
		}
		return fmt.Errorf("failed to fetch CoreDNS ConfigMap: %w", err)
	}

	corefile, ok := cm.Data["Corefile"]
	if !ok {
		return fmt.Errorf("CoreDNS ConfigMap missing Corefile entry")
	}

	updated, changed := ensurePipeOpsRewrite(corefile)
	if !changed {
		ci.logger.Debug("CoreDNS rewrite for pipeops.internal already configured")
		return nil
	}

	cmCopy := cm.DeepCopy()
	cmCopy.Data["Corefile"] = updated

	if _, err := ci.k8sClient.CoreV1().ConfigMaps(coreDNSNamespace).Update(ctx, cmCopy, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update CoreDNS ConfigMap: %w", err)
	}

	ci.logger.Info("✓ CoreDNS configured to rewrite pipeops.internal to cluster services")
	return nil
}

// InstallVPA installs Vertical Pod Autoscaler for automatic resource recommendations

func (ci *ComponentInstaller) InstallVPA(ctx context.Context) error {
	namespace := vpaNamespace // Install in pipeops-system namespace with other monitoring

	// Check if already installed
	if ci.isVPAInstalled(ctx, namespace) {
		ci.logger.WithField("namespace", namespace).Info("✓ VPA already installed")
		return nil
	}

	ci.logger.Info("Installing Vertical Pod Autoscaler (VPA)...")
	ci.logger.WithField("namespace", namespace).Info("Installing VPA in namespace...")

	chartRepo := "https://charts.fairwinds.com/stable"
	chartName := "fairwinds-stable/vpa"

	// Add Helm repository
	if err := ci.installer.AddRepo(ctx, chartName, chartRepo); err != nil {
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
			"enabled": false, // Disabled due to RBAC complexity with cert generation
			// The admission controller requires complex RBAC setup for certificate generation
			// across namespaces. The recommender and updater provide the core VPA functionality.
			// Enable this manually if needed with proper cert-manager setup.
		},
		// Metrics server integration
		"metricsServer": map[string]interface{}{
			"enabled": false, // We're using separate metrics-server installation
		},
	}

	// Install the chart
	release := &helm.HelmRelease{
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

	// Wait for VPA components to be ready (only recommender and updater)
	components := []string{"vpa-recommender", "vpa-updater"}
	for _, component := range components {
		ci.logger.WithField("component", component).Debug("Waiting for VPA component...")
		if err := ci.waitForDeployment(ctx, namespace, component, 2*time.Minute); err != nil {
			ci.logger.WithError(err).Warnf("VPA component %s did not become ready (may still be starting)", component)
		}
	}

	ci.logger.Info("✓ Vertical Pod Autoscaler installed successfully (recommender + updater)")
	ci.logger.Info("ℹ️  VPA admission controller disabled - core VPA functionality available via recommender")
	return nil
}

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

func (ci *ComponentInstaller) isVPAInstalled(ctx context.Context, namespace string) bool {
	// Check if VPA recommender deployment exists
	_, err := ci.k8sClient.AppsV1().Deployments(namespace).Get(
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

	// VPA components (admission controller disabled due to RBAC complexity)
	// VPA is installed in pipeops-system namespace alongside other monitoring
	vpaComponents := []string{"vpa-recommender", "vpa-updater"}
	for _, component := range vpaComponents {
		if status, err := ci.getDeploymentStatus(ctx, "pipeops-system", component); err == nil {
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

func ensurePipeOpsRewrite(corefile string) (string, bool) {
	if strings.Contains(corefile, "pipeops.internal") {
		return corefile, false
	}

	idx := strings.Index(corefile, "log")
	if idx == -1 {
		if strings.HasSuffix(corefile, "\n") {
			return corefile + "\n" + pipeOpsRewriteBlock + "\n", true
		}
		return corefile + "\n\n" + pipeOpsRewriteBlock + "\n", true
	}

	lineEnd := strings.Index(corefile[idx:], "\n")
	if lineEnd == -1 {
		lineEnd = len(corefile) - idx
	}
	insertPos := idx + lineEnd

	var builder strings.Builder
	builder.WriteString(corefile[:insertPos])
	builder.WriteString("\n\n")
	builder.WriteString(pipeOpsRewriteBlock)
	builder.WriteString("\n")
	builder.WriteString(corefile[insertPos:])

	return builder.String(), true
}
