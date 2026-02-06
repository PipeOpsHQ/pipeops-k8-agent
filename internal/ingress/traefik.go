package ingress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/helm"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
)

// TraefikController manages the Traefik ingress controller installation
type TraefikController struct {
	installer    *helm.HelmInstaller
	logger       *logrus.Logger
	namespace    string
	chartRepo    string
	chartName    string
	releaseName  string
	ingressClass string
}

// NewTraefikController creates a new Traefik controller manager
func NewTraefikController(installer *helm.HelmInstaller, logger *logrus.Logger) *TraefikController {
	return &TraefikController{
		installer:    installer,
		logger:       logger,
		namespace:    "pipeops-system", // Use pipeops-system to avoid Control Plane security restrictions on kube-system
		chartRepo:    "https://traefik.github.io/charts",
		chartName:    "traefik/traefik",
		releaseName:  "traefik",
		ingressClass: "traefik",
	}
}

// IsInstalled checks if Traefik is already installed
func (tc *TraefikController) IsInstalled(ctx context.Context) bool {
	// Check for deployment in common namespaces
	namespaces := []string{"pipeops-system", "kube-system", "traefik", "default", "ingress-nginx"}

	for _, ns := range namespaces {
		_, err := tc.installer.K8sClient.AppsV1().Deployments(ns).Get(ctx, tc.releaseName, metav1.GetOptions{})
		if err == nil {
			tc.logger.WithField("namespace", ns).Info("Detected existing Traefik installation")
			tc.namespace = ns // Update namespace to where we found it
			return true
		}
	}
	return false
}

// GetInstalledNamespace returns the namespace where Traefik is installed (after IsInstalled is called)
func (tc *TraefikController) GetInstalledNamespace() string {
	return tc.namespace
}

// SetNamespace sets the namespace for installation
func (tc *TraefikController) SetNamespace(ns string) {
	tc.namespace = ns
}

// Install installs or upgrades Traefik using a two-phase approach:
// Phase 1: Install Traefik via Helm (this also installs CRDs automatically)
// Phase 2: Create default backend + error middleware, then upgrade to wire them in
func (tc *TraefikController) Install(ctx context.Context, profile types.ResourceProfile) error {
	tc.logger.Info("Installing/Upgrading Traefik Ingress Controller...")

	// Add Helm repository
	if err := tc.installer.AddRepo(ctx, "traefik", tc.chartRepo); err != nil {
		return fmt.Errorf("failed to add traefik helm repo: %w", err)
	}

	// Build base Helm values (no middleware references yet — CRDs may not exist)
	values := tc.buildBaseValues(profile)

	// Phase 1: Install Traefik (Helm chart installs its own CRDs)
	release := &helm.HelmRelease{
		Name:      tc.releaseName,
		Namespace: tc.namespace,
		Chart:     tc.chartName,
		Repo:      tc.chartRepo,
		Version:   "v33.1.0",
		Values:    values,
	}

	if err := tc.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install Traefik: %w", err)
	}

	tc.logger.Info("✓ Traefik Ingress Controller installed successfully (Phase 1)")

	// Phase 2: Now that CRDs exist, create default backend + middleware, then upgrade
	if err := tc.setupErrorPages(ctx, profile); err != nil {
		// Non-fatal — Traefik still works, just without custom error pages
		tc.logger.WithError(err).Warn("Custom error pages setup failed (Traefik is still functional)")
	}

	return nil
}

// buildBaseValues returns the Helm values for Traefik without middleware references.
func (tc *TraefikController) buildBaseValues(profile types.ResourceProfile) map[string]interface{} {
	values := map[string]interface{}{
		"deployment": map[string]interface{}{
			"enabled":  true,
			"replicas": 1,
		},
		"providers": map[string]interface{}{
			"kubernetesCRD": map[string]interface{}{
				"enabled": true,
			},
			"kubernetesIngress": map[string]interface{}{
				"enabled": true,
				"publishedService": map[string]interface{}{
					"enabled": true,
				},
			},
		},
		"service": map[string]interface{}{
			"enabled": true,
			"type":    "LoadBalancer",
		},
		"metrics": map[string]interface{}{
			"prometheus": map[string]interface{}{
				"enabled": true,
			},
		},
	}

	if profile == types.ProfileLow {
		tc.logger.Info("Applying Low Profile tuning for Traefik")
		values["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]interface{}{"cpu": "300m", "memory": "128Mi"},
		}
	} else {
		values["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
			"limits":   map[string]interface{}{"cpu": "500m", "memory": "256Mi"},
		}
	}

	return values
}

// setupErrorPages creates the default backend, error middleware, and upgrades
// Traefik to wire them in. Called after Phase 1 when CRDs are guaranteed to exist.
func (tc *TraefikController) setupErrorPages(ctx context.Context, profile types.ResourceProfile) error {
	// Wait briefly for CRDs to become established
	if err := tc.waitForCRDs(ctx); err != nil {
		return fmt.Errorf("traefik CRDs not ready: %w", err)
	}

	// Create default backend deployment + service
	if err := tc.ensureDefaultBackend(ctx); err != nil {
		return fmt.Errorf("failed to create default backend: %w", err)
	}

	// Create the Middleware CRD resource
	if err := tc.ensureErrorMiddleware(ctx); err != nil {
		return fmt.Errorf("failed to create error middleware: %w", err)
	}

	// Now upgrade Traefik to reference the middleware in entrypoints
	values := tc.buildBaseValues(profile)
	values["additionalArguments"] = []interface{}{
		"--entrypoints.web.middlewares=" + tc.namespace + "-error-pages@kubernetescrd",
		"--entrypoints.websecure.middlewares=" + tc.namespace + "-error-pages@kubernetescrd",
	}

	release := &helm.HelmRelease{
		Name:      tc.releaseName,
		Namespace: tc.namespace,
		Chart:     tc.chartName,
		Repo:      tc.chartRepo,
		Version:   "v33.1.0",
		Values:    values,
	}

	if err := tc.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to upgrade Traefik with error middleware: %w", err)
	}

	tc.logger.Info("✓ Custom error pages configured successfully (Phase 2)")
	return nil
}

// waitForCRDs polls until the Traefik Middleware CRD is established or the context expires.
func (tc *TraefikController) waitForCRDs(ctx context.Context) error {
	apiextensionsClient, err := apiextensionsclientset.NewForConfig(tc.installer.Config)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	crdName := "middlewares.traefik.io"
	tc.logger.WithField("crd", crdName).Debug("Waiting for Traefik CRD to become established")

	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out waiting for CRD %s to become established", crdName)
		case <-ticker.C:
			crd, err := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
			if err != nil {
				tc.logger.WithError(err).Debug("CRD not found yet, retrying...")
				continue
			}

			for _, cond := range crd.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					tc.logger.WithField("crd", crdName).Debug("CRD is established")
					return nil
				}
			}
		}
	}
}

// ensureDefaultBackend creates a deployment and service for custom error pages
func (tc *TraefikController) ensureDefaultBackend(ctx context.Context) error {
	tc.logger.WithField("namespace", tc.namespace).Info("Ensuring default backend deployment and service for custom error pages...")

	// Deployment
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-backend",
			Namespace: tc.namespace,
			Labels: map[string]string{
				"app":                          "default-backend",
				"app.kubernetes.io/name":       "default-backend",
				"app.kubernetes.io/managed-by": "pipeops-agent",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "default-backend"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "default-backend"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "error-pages",
						Image: "ghcr.io/tarampampam/error-pages:2.26", // Lightweight, customizable error pages
						Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
						Env: []corev1.EnvVar{
							{Name: "TEMPLATE_NAME", Value: "ghost"}, // Clean, simple template
							{Name: "SHOW_DETAILS", Value: "false"},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("16Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
						},
					}},
				},
			},
		},
	}

	// Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-backend",
			Namespace: tc.namespace,
			Labels:    map[string]string{"app": "default-backend"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "default-backend"},
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt(8080),
			}},
		},
	}

	// Create Deployment
	_, err := tc.installer.K8sClient.AppsV1().Deployments(tc.namespace).Create(ctx, deployment, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			tc.logger.WithField("name", "default-backend").Debug("Default backend deployment already exists")
		} else {
			return fmt.Errorf("failed to create default backend deployment: %w", err)
		}
	} else {
		tc.logger.Info("✓ Created default backend deployment")
	}

	// Create Service
	_, err = tc.installer.K8sClient.CoreV1().Services(tc.namespace).Create(ctx, service, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			tc.logger.WithField("name", "default-backend").Debug("Default backend service already exists")
		} else {
			return fmt.Errorf("failed to create default backend service: %w", err)
		}
	} else {
		tc.logger.Info("✓ Created default backend service")
	}

	tc.logger.Info("✓ Default backend setup completed successfully")
	return nil
}

// ensureErrorMiddleware creates the Traefik Middleware CRD for error pages
func (tc *TraefikController) ensureErrorMiddleware(ctx context.Context) error {
	tc.logger.WithField("namespace", tc.namespace).Debug("Creating Traefik error middleware")

	// We use Unstructured because we don't have generated clients for Traefik CRDs
	middleware := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]interface{}{
				// Name should match the reference format (namespace-name)
				// Since we reference it as "namespace-error-pages@kubernetescrd" in entrypoint config
				"name":      "error-pages",
				"namespace": tc.namespace,
			},
			"spec": map[string]interface{}{
				"errors": map[string]interface{}{
					"status": []string{"400-599"},
					"service": map[string]interface{}{
						"name": "default-backend",
						"port": 80,
					},
					"query": "/{status}.html",
				},
			},
		},
	}

	client, err := dynamic.NewForConfig(tc.installer.Config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Use dynamic client
	gvr := schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "middlewares"}

	_, err = client.Resource(gvr).Namespace(tc.namespace).Create(ctx, middleware, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			tc.logger.WithField("name", "error-pages").Debug("Error middleware already exists")
		} else {
			return fmt.Errorf("failed to create error middleware: %w", err)
		}
	} else {
		tc.logger.WithFields(logrus.Fields{
			"name":      "error-pages",
			"namespace": tc.namespace,
			"reference": tc.namespace + "-error-pages@kubernetescrd",
		}).Info("✓ Created Traefik error middleware for custom error pages")
	}

	return nil
}

// Uninstall removes Traefik
func (tc *TraefikController) Uninstall(ctx context.Context) error {
	tc.logger.Info("Uninstalling Traefik...")
	return tc.installer.Uninstall(ctx, tc.releaseName, tc.namespace)
}

// CreateIngress creates an ingress resource for a service using Traefik
func (tc *TraefikController) CreateIngress(config IngressConfig) error {
	ctx := context.Background()

	tc.logger.WithFields(logrus.Fields{
		"name":      config.Name,
		"namespace": config.Namespace,
		"host":      config.Host,
		"service":   config.ServiceName,
	}).Debug("Creating Traefik ingress resource")

	pathTypePrefix := networkingv1.PathTypePrefix

	// Merge annotations with Traefik specifics if needed
	// Traefik typically relies on IngressClass, but we can add router middlewares here if needed
	annotations := mergeMaps(map[string]string{
		"kubernetes.io/ingress.class": tc.ingressClass,
	}, config.Annotations)

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        config.Name,
			Namespace:   config.Namespace,
			Annotations: annotations,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "pipeops-agent",
				"app.kubernetes.io/component":  "monitoring",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &tc.ingressClass,
			Rules: []networkingv1.IngressRule{
				{
					Host: config.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: config.ServiceName,
											Port: networkingv1.ServiceBackendPort{
												Number: int32(config.ServicePort),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Add TLS if enabled
	if config.TLSEnabled {
		ingress.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{config.Host},
				SecretName: fmt.Sprintf("%s-tls", config.Name),
			},
		}
	}

	// Create or update ingress
	existingIngress, err := tc.installer.K8sClient.NetworkingV1().Ingresses(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// Create new ingress
		_, err = tc.installer.K8sClient.NetworkingV1().Ingresses(config.Namespace).Create(ctx, ingress, metav1.CreateOptions{})
		if err != nil {
			if isNginxAdmissionWebhookError(err) {
				tc.logger.WithError(err).Warn("Ingress create blocked by stale NGINX admission webhook, attempting cleanup and retry")
				if cleanupErr := tc.cleanupStaleNginxAdmissionWebhooks(ctx); cleanupErr != nil {
					return fmt.Errorf("failed to create ingress: %w (cleanup failed: %v)", err, cleanupErr)
				}
				if _, retryErr := tc.installer.K8sClient.NetworkingV1().Ingresses(config.Namespace).Create(ctx, ingress, metav1.CreateOptions{}); retryErr != nil {
					return fmt.Errorf("failed to create ingress after webhook cleanup: %w", retryErr)
				}
				tc.logger.WithField("name", config.Name).Info("✓ Created Traefik ingress resource after webhook cleanup")
				return nil
			}
			return fmt.Errorf("failed to create ingress: %w", err)
		}
		tc.logger.WithField("name", config.Name).Info("✓ Created Traefik ingress resource")
	} else {
		// Update existing ingress
		ingress.ResourceVersion = existingIngress.ResourceVersion
		_, err = tc.installer.K8sClient.NetworkingV1().Ingresses(config.Namespace).Update(ctx, ingress, metav1.UpdateOptions{})
		if err != nil {
			if isNginxAdmissionWebhookError(err) {
				tc.logger.WithError(err).Warn("Ingress update blocked by stale NGINX admission webhook, attempting cleanup and retry")
				if cleanupErr := tc.cleanupStaleNginxAdmissionWebhooks(ctx); cleanupErr != nil {
					return fmt.Errorf("failed to update ingress: %w (cleanup failed: %v)", err, cleanupErr)
				}
				// Re-fetch latest resource version before retrying update.
				refreshed, getErr := tc.installer.K8sClient.NetworkingV1().Ingresses(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
				if getErr != nil {
					return fmt.Errorf("failed to update ingress after webhook cleanup: failed to refresh ingress: %w", getErr)
				}
				ingress.ResourceVersion = refreshed.ResourceVersion
				if _, retryErr := tc.installer.K8sClient.NetworkingV1().Ingresses(config.Namespace).Update(ctx, ingress, metav1.UpdateOptions{}); retryErr != nil {
					return fmt.Errorf("failed to update ingress after webhook cleanup: %w", retryErr)
				}
				tc.logger.WithField("name", config.Name).Info("✓ Updated Traefik ingress resource after webhook cleanup")
				return nil
			}
			return fmt.Errorf("failed to update ingress: %w", err)
		}
		tc.logger.WithField("name", config.Name).Info("✓ Updated Traefik ingress resource")
	}

	return nil
}

func isNginxAdmissionWebhookError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "validate.nginx.ingress.kubernetes.io") ||
		strings.Contains(errStr, "ingress-nginx-controller-admission")
}

func (tc *TraefikController) cleanupStaleNginxAdmissionWebhooks(ctx context.Context) error {
	// Safety guard: only clean up webhook configurations if the backing admission service is missing.
	_, err := tc.installer.K8sClient.CoreV1().Services("ingress-nginx").Get(ctx, "ingress-nginx-controller-admission", metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("nginx admission service still exists; refusing to delete webhook configurations")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed checking nginx admission service: %w", err)
	}

	deleted := 0

	// Clean validating webhook configurations that point to the missing ingress-nginx admission service.
	vwcList, err := tc.installer.K8sClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list validating webhook configurations: %w", err)
	}
	for _, cfg := range vwcList.Items {
		if validatingWebhookConfigReferencesMissingNginxService(cfg.Webhooks) {
			if delErr := tc.installer.K8sClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(ctx, cfg.Name, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
				return fmt.Errorf("failed deleting validating webhook configuration %s: %w", cfg.Name, delErr)
			}
			deleted++
			tc.logger.WithField("webhook", cfg.Name).Info("Deleted stale NGINX validating webhook configuration")
		}
	}

	// Also clean mutating webhook configurations if any reference the missing service.
	mwcList, err := tc.installer.K8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list mutating webhook configurations: %w", err)
	}
	for _, cfg := range mwcList.Items {
		if mutatingWebhookConfigReferencesMissingNginxService(cfg.Webhooks) {
			if delErr := tc.installer.K8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(ctx, cfg.Name, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
				return fmt.Errorf("failed deleting mutating webhook configuration %s: %w", cfg.Name, delErr)
			}
			deleted++
			tc.logger.WithField("webhook", cfg.Name).Info("Deleted stale NGINX mutating webhook configuration")
		}
	}

	tc.logger.WithField("deleted_webhooks", deleted).Info("Completed stale NGINX admission webhook cleanup")
	return nil
}

func validatingWebhookConfigReferencesMissingNginxService(webhooks []admissionregistrationv1.ValidatingWebhook) bool {
	for _, webhook := range webhooks {
		if webhook.ClientConfig.Service == nil {
			continue
		}
		svc := webhook.ClientConfig.Service
		if svc.Name == "ingress-nginx-controller-admission" && svc.Namespace == "ingress-nginx" {
			return true
		}
		if strings.Contains(webhook.Name, "nginx.ingress.kubernetes.io") {
			return true
		}
	}
	return false
}

func mutatingWebhookConfigReferencesMissingNginxService(webhooks []admissionregistrationv1.MutatingWebhook) bool {
	for _, webhook := range webhooks {
		if webhook.ClientConfig.Service == nil {
			continue
		}
		svc := webhook.ClientConfig.Service
		if svc.Name == "ingress-nginx-controller-admission" && svc.Namespace == "ingress-nginx" {
			return true
		}
		if strings.Contains(webhook.Name, "nginx.ingress.kubernetes.io") {
			return true
		}
	}
	return false
}

// GetIngressURL returns the URL to access an ingress
func (tc *TraefikController) GetIngressURL(namespace, name string) (string, error) {
	ctx := context.Background()

	// Get the ingress controller service
	svc, err := tc.installer.K8sClient.CoreV1().Services(tc.namespace).Get(ctx, tc.releaseName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get ingress service: %w", err)
	}

	// Get ingress
	ingress, err := tc.installer.K8sClient.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get ingress: %w", err)
	}

	if len(ingress.Spec.Rules) == 0 {
		return "", fmt.Errorf("no rules defined in ingress")
	}

	host := ingress.Spec.Rules[0].Host
	if host == "" {
		host = "localhost"
	}

	// For NodePort service
	if svc.Spec.Type == corev1.ServiceTypeNodePort {
		for _, port := range svc.Spec.Ports {
			if port.Port == 80 || port.Name == "web" {
				return fmt.Sprintf("http://%s:%d", host, port.NodePort), nil
			}
		}
	}

	// For LoadBalancer service
	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
		if len(svc.Status.LoadBalancer.Ingress) > 0 {
			lb := svc.Status.LoadBalancer.Ingress[0]
			if lb.IP != "" {
				return fmt.Sprintf("http://%s", lb.IP), nil
			}
			if lb.Hostname != "" {
				return fmt.Sprintf("http://%s", lb.Hostname), nil
			}
		}
	}

	return fmt.Sprintf("http://%s", host), nil
}
