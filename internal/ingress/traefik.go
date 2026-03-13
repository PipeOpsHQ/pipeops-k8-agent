package ingress

import (
	"context"
	"errors"
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
	"k8s.io/client-go/kubernetes"
)

var errTraefikMiddlewareCRDNotReady = errors.New("traefik middleware CRD not ready")

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
		Timeout:   traefikHelmTimeout(profile),
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

	// Migrate existing nginx-class ingresses to traefik so routing works after controller switch.
	if err := tc.migrateExistingNginxIngresses(ctx); err != nil {
		// Non-fatal: Traefik install succeeded, and ingresses may be migrated on subsequent retries/restarts.
		tc.logger.WithError(err).Warn("Failed to migrate existing nginx ingresses to traefik class")
	}

	return nil
}

// traefikHelmTimeout returns a profile-aware timeout for Traefik Helm operations.
func traefikHelmTimeout(profile types.ResourceProfile) time.Duration {
	if profile == types.ProfileLow {
		return 10 * time.Minute
	}
	return 15 * time.Minute
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
				"enabled":             true,
				"allowCrossNamespace": true,
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
		if errors.Is(err, errTraefikMiddlewareCRDNotReady) {
			tc.logger.WithError(err).Info("Skipping custom error pages setup because Traefik middleware CRD is not ready")
			return nil
		}
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

	// Create a low-priority catch-all route so unmatched hosts/paths also return
	// the custom 404 page instead of Traefik's built-in default.
	if err := tc.ensureCatchAllFallbackRoute(ctx); err != nil {
		return fmt.Errorf("failed to create catch-all fallback route: %w", err)
	}

	// Now upgrade Traefik to reference the middleware in entrypoints
	values := tc.buildBaseValues(profile)
	values["additionalArguments"] = []interface{}{
		"--entrypoints.web.http.middlewares=" + tc.namespace + "-errors-middleware@kubernetescrd",
		"--entrypoints.websecure.http.middlewares=" + tc.namespace + "-errors-middleware@kubernetescrd",
	}

	release := &helm.HelmRelease{
		Name:      tc.releaseName,
		Namespace: tc.namespace,
		Chart:     tc.chartName,
		Repo:      tc.chartRepo,
		Version:   "v33.1.0",
		Values:    values,
		Timeout:   traefikHelmTimeout(profile),
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

	waitTimeout := 90 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < waitTimeout {
			waitTimeout = remaining
		}
	}
	deadline := time.After(waitTimeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("%w: timed out waiting for CRD %s to become established", errTraefikMiddlewareCRDNotReady, crdName)
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

// ensureDefaultBackend creates ConfigMaps, deployment and service for PipeOps custom error pages
func (tc *TraefikController) ensureDefaultBackend(ctx context.Context) error {
	tc.logger.WithField("namespace", tc.namespace).Info("Ensuring error pages deployment and service for custom error pages...")

	// Create ConfigMaps first
	if err := tc.ensureErrorPagesConfigMaps(ctx); err != nil {
		return fmt.Errorf("failed to create error pages configmaps: %w", err)
	}

	// Deployment with nginx serving custom HTML pages
	replicas := int32(2)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "error-pages",
			Namespace: tc.namespace,
			Labels: map[string]string{
				"app":                          "error-pages",
				"app.kubernetes.io/name":       "error-pages",
				"app.kubernetes.io/managed-by": "pipeops-agent",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "error-pages"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "error-pages"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:alpine",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
							Name:          "http",
						}},
						Env: []corev1.EnvVar{
							// Filter for nonexistent variable to avoid nginx envsubst processing $uri in config
							{Name: "NGINX_ENVSUBST_FILTER", Value: "NONEXISTENT_VAR"},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "custom-error-pages",
								MountPath: "/usr/share/nginx/html",
							},
							{
								Name:      "nginx-config",
								MountPath: "/etc/nginx/conf.d/default.conf",
								SubPath:   "default.conf",
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "custom-error-pages",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "custom-error-pages",
									},
								},
							},
						},
						{
							Name: "nginx-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "error-pages-nginx-config",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "error-pages",
			Namespace: tc.namespace,
			Labels:    map[string]string{"app": "error-pages"},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": "error-pages"},
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt(80),
				Protocol:   corev1.ProtocolTCP,
				Name:       "http",
			}},
		},
	}

	// Create or update Deployment
	_, err := tc.installer.K8sClient.AppsV1().Deployments(tc.namespace).Get(ctx, "error-pages", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, createErr := tc.installer.K8sClient.AppsV1().Deployments(tc.namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create error-pages deployment: %w", createErr)
			}
			tc.logger.Info("✓ Created error-pages deployment")
		} else {
			return fmt.Errorf("failed to get error-pages deployment: %w", err)
		}
	} else {
		_, updateErr := tc.installer.K8sClient.AppsV1().Deployments(tc.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("failed to update error-pages deployment: %w", updateErr)
		}
		tc.logger.Debug("Updated error-pages deployment")
	}

	// Create or update Service
	_, err = tc.installer.K8sClient.CoreV1().Services(tc.namespace).Get(ctx, "error-pages", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, createErr := tc.installer.K8sClient.CoreV1().Services(tc.namespace).Create(ctx, service, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create error-pages service: %w", createErr)
			}
			tc.logger.Info("✓ Created error-pages service")
		} else {
			return fmt.Errorf("failed to get error-pages service: %w", err)
		}
	} else {
		tc.logger.Debug("error-pages service already exists")
	}

	tc.logger.Info("✓ Error pages setup completed successfully")
	return nil
}

// ensureErrorPagesConfigMaps creates the ConfigMaps for custom HTML error pages and nginx config
func (tc *TraefikController) ensureErrorPagesConfigMaps(ctx context.Context) error {
	// ConfigMap with custom HTML error pages
	errorPagesConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-error-pages",
			Namespace: tc.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "pipeops-agent",
			},
		},
		Data: map[string]string{
			"404.html": errorPage404HTML,
			"502.html": errorPage502HTML,
			"503.html": errorPage503HTML,
		},
	}

	// ConfigMap with nginx server configuration
	nginxConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "error-pages-nginx-config",
			Namespace: tc.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "pipeops-agent",
			},
		},
		Data: map[string]string{
			"default.conf": nginxDefaultConf,
		},
	}

	// Create or update error pages ConfigMap
	if err := tc.createOrUpdateConfigMap(ctx, errorPagesConfigMap); err != nil {
		return fmt.Errorf("failed to ensure custom-error-pages configmap: %w", err)
	}

	// Create or update nginx config ConfigMap
	if err := tc.createOrUpdateConfigMap(ctx, nginxConfigMap); err != nil {
		return fmt.Errorf("failed to ensure error-pages-nginx-config configmap: %w", err)
	}

	return nil
}

// createOrUpdateConfigMap creates a ConfigMap if it doesn't exist, or updates it if it does
func (tc *TraefikController) createOrUpdateConfigMap(ctx context.Context, cm *corev1.ConfigMap) error {
	existing, err := tc.installer.K8sClient.CoreV1().ConfigMaps(cm.Namespace).Get(ctx, cm.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, createErr := tc.installer.K8sClient.CoreV1().ConfigMaps(cm.Namespace).Create(ctx, cm, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create configmap %s: %w", cm.Name, createErr)
			}
			tc.logger.WithField("name", cm.Name).Info("✓ Created ConfigMap")
			return nil
		}
		return fmt.Errorf("failed to get configmap %s: %w", cm.Name, err)
	}

	// Update existing ConfigMap
	existing.Data = cm.Data
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	for k, v := range cm.Labels {
		existing.Labels[k] = v
	}
	_, updateErr := tc.installer.K8sClient.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if updateErr != nil {
		return fmt.Errorf("failed to update configmap %s: %w", cm.Name, updateErr)
	}
	tc.logger.WithField("name", cm.Name).Debug("Updated ConfigMap")
	return nil
}

// ensureErrorMiddleware creates the Traefik Middleware CRD for error pages
func (tc *TraefikController) ensureErrorMiddleware(ctx context.Context) error {
	tc.logger.WithField("namespace", tc.namespace).Debug("Creating Traefik errors middleware")

	// We use Unstructured because we don't have generated clients for Traefik CRDs
	middleware := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]interface{}{
				// Name should match the reference format (namespace-name)
				// Since we reference it as "namespace-errors-middleware@kubernetescrd" in entrypoint config
				"name":      "errors-middleware",
				"namespace": tc.namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "pipeops-agent",
				},
			},
			"spec": map[string]interface{}{
				"errors": map[string]interface{}{
					// Handle specific error codes: 403, 404, 502, 503
					"status": []string{"403", "404", "502", "503"},
					"service": map[string]interface{}{
						"name": "error-pages",
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
			// Update existing middleware to ensure it has the correct configuration
			existing, getErr := client.Resource(gvr).Namespace(tc.namespace).Get(ctx, "errors-middleware", metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("failed to get existing errors middleware: %w", getErr)
			}
			existing.Object["spec"] = middleware.Object["spec"]
			if _, updateErr := client.Resource(gvr).Namespace(tc.namespace).Update(ctx, existing, metav1.UpdateOptions{}); updateErr != nil {
				return fmt.Errorf("failed to update errors middleware: %w", updateErr)
			}
			tc.logger.WithField("name", "errors-middleware").Debug("Updated errors middleware")
		} else {
			return fmt.Errorf("failed to create errors middleware: %w", err)
		}
	} else {
		tc.logger.WithFields(logrus.Fields{
			"name":      "errors-middleware",
			"namespace": tc.namespace,
			"reference": tc.namespace + "-errors-middleware@kubernetescrd",
		}).Info("✓ Created Traefik errors middleware for custom error pages")
	}

	return nil
}

// ensureCatchAllFallbackRoute creates a low-priority IngressRoute that matches
// any host/path and serves the error-pages service for 404 responses.
func (tc *TraefikController) ensureCatchAllFallbackRoute(ctx context.Context) error {
	tc.logger.WithField("namespace", tc.namespace).Debug("Ensuring Traefik catch-all fallback route")

	route := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "IngressRoute",
			"metadata": map[string]interface{}{
				"name":      "catch-all",
				"namespace": tc.namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "pipeops-agent",
				},
			},
			"spec": map[string]interface{}{
				"entryPoints": []interface{}{"web", "websecure"},
				"routes": []interface{}{
					map[string]interface{}{
						"kind": "Rule",
						// Traefik v3 syntax: HostRegexp(`^.+$`) matches any host
						"match":    "HostRegexp(`^.+$`) && PathPrefix(`/`)",
						"priority": int64(1), // Low priority - only matches unmatched requests
						"services": []interface{}{
							map[string]interface{}{
								"name": "error-pages",
								"port": int64(80),
							},
						},
					},
				},
			},
		},
	}

	client, err := dynamic.NewForConfig(tc.installer.Config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "ingressroutes"}

	_, err = client.Resource(gvr).Namespace(tc.namespace).Create(ctx, route, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create catch-all ingressroute: %w", err)
		}

		existing, getErr := client.Resource(gvr).Namespace(tc.namespace).Get(ctx, "catch-all", metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("failed to fetch existing catch-all ingressroute: %w", getErr)
		}
		existing.Object["spec"] = route.Object["spec"]
		if _, updateErr := client.Resource(gvr).Namespace(tc.namespace).Update(ctx, existing, metav1.UpdateOptions{}); updateErr != nil {
			return fmt.Errorf("failed to update catch-all ingressroute: %w", updateErr)
		}

		tc.logger.WithFields(logrus.Fields{
			"name":      "catch-all",
			"namespace": tc.namespace,
		}).Debug("Updated Traefik catch-all fallback route")
		return nil
	}

	tc.logger.WithFields(logrus.Fields{
		"name":      "catch-all",
		"namespace": tc.namespace,
	}).Info("✓ Created Traefik catch-all fallback route for custom 404 pages")

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
	return cleanupStaleNginxAdmissionWebhooksForClient(ctx, tc.installer.K8sClient, tc.logger)
}

func cleanupStaleNginxAdmissionWebhooksForClient(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) error {
	// Safety guard: only clean up webhook configurations if the backing admission service is missing.
	_, err := k8sClient.CoreV1().Services("ingress-nginx").Get(ctx, "ingress-nginx-controller-admission", metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("nginx admission service still exists; refusing to delete webhook configurations")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed checking nginx admission service: %w", err)
	}

	cleaned := 0

	// Clean validating webhook configurations that point to the missing ingress-nginx admission service.
	vwcList, err := k8sClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list validating webhook configurations: %w", err)
	}
	for _, cfg := range vwcList.Items {
		if validatingWebhookConfigReferencesMissingNginxService(cfg.Webhooks) {
			delErr := k8sClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(ctx, cfg.Name, metav1.DeleteOptions{})
			if delErr != nil && !apierrors.IsNotFound(delErr) {
				if apierrors.IsForbidden(delErr) || apierrors.IsUnauthorized(delErr) {
					patchedCfg := cfg.DeepCopy()
					if !setValidatingWebhookFailurePolicyIgnoreForNginx(patchedCfg.Webhooks) {
						return fmt.Errorf("failed deleting validating webhook configuration %s: %w", cfg.Name, delErr)
					}
					if _, updateErr := k8sClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Update(ctx, patchedCfg, metav1.UpdateOptions{}); updateErr != nil {
						return fmt.Errorf("failed deleting validating webhook configuration %s: %w (fallback update failed: %v)", cfg.Name, delErr, updateErr)
					}
					cleaned++
					logger.WithField("webhook", cfg.Name).Warn("Delete forbidden; patched stale NGINX validating webhook failurePolicy to Ignore")
					continue
				}
				return fmt.Errorf("failed deleting validating webhook configuration %s: %w", cfg.Name, delErr)
			}
			cleaned++
			logger.WithField("webhook", cfg.Name).Info("Deleted stale NGINX validating webhook configuration")
		}
	}

	// Also clean mutating webhook configurations if any reference the missing service.
	mwcList, err := k8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list mutating webhook configurations: %w", err)
	}
	for _, cfg := range mwcList.Items {
		if mutatingWebhookConfigReferencesMissingNginxService(cfg.Webhooks) {
			delErr := k8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(ctx, cfg.Name, metav1.DeleteOptions{})
			if delErr != nil && !apierrors.IsNotFound(delErr) {
				if apierrors.IsForbidden(delErr) || apierrors.IsUnauthorized(delErr) {
					patchedCfg := cfg.DeepCopy()
					if !setMutatingWebhookFailurePolicyIgnoreForNginx(patchedCfg.Webhooks) {
						return fmt.Errorf("failed deleting mutating webhook configuration %s: %w", cfg.Name, delErr)
					}
					if _, updateErr := k8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(ctx, patchedCfg, metav1.UpdateOptions{}); updateErr != nil {
						return fmt.Errorf("failed deleting mutating webhook configuration %s: %w (fallback update failed: %v)", cfg.Name, delErr, updateErr)
					}
					cleaned++
					logger.WithField("webhook", cfg.Name).Warn("Delete forbidden; patched stale NGINX mutating webhook failurePolicy to Ignore")
					continue
				}
				return fmt.Errorf("failed deleting mutating webhook configuration %s: %w", cfg.Name, delErr)
			}
			cleaned++
			logger.WithField("webhook", cfg.Name).Info("Deleted stale NGINX mutating webhook configuration")
		}
	}

	logger.WithField("cleaned_webhooks", cleaned).Info("Completed stale NGINX admission webhook cleanup")
	return nil
}

func (tc *TraefikController) migrateExistingNginxIngresses(ctx context.Context) error {
	ingressList, err := tc.installer.K8sClient.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ingresses for migration: %w", err)
	}

	migrated := 0
	skipped := 0
	failed := 0
	webhookCleanupAttempted := false

	for i := range ingressList.Items {
		ing := ingressList.Items[i].DeepCopy()
		if !ingressUsesClass(ing, "nginx") {
			skipped++
			continue
		}

		if err := tc.updateIngressClass(ctx, ing, tc.ingressClass); err != nil {
			if isNginxAdmissionWebhookError(err) {
				tc.logger.WithError(err).WithFields(logrus.Fields{
					"ingress":   ing.Name,
					"namespace": ing.Namespace,
				}).Warn("Ingress class migration blocked by stale NGINX webhook")

				if !webhookCleanupAttempted {
					webhookCleanupAttempted = true
					if cleanupErr := tc.cleanupStaleNginxAdmissionWebhooks(ctx); cleanupErr != nil {
						failed++
						tc.logger.WithError(cleanupErr).Warn("Failed to cleanup stale NGINX webhooks during ingress migration")
						continue
					}
				}

				// Retry once after cleanup with fresh resource version.
				refreshed, getErr := tc.installer.K8sClient.NetworkingV1().Ingresses(ing.Namespace).Get(ctx, ing.Name, metav1.GetOptions{})
				if getErr != nil {
					failed++
					tc.logger.WithError(getErr).WithFields(logrus.Fields{
						"ingress":   ing.Name,
						"namespace": ing.Namespace,
					}).Warn("Failed to refresh ingress for migration retry")
					continue
				}
				if retryErr := tc.updateIngressClass(ctx, refreshed.DeepCopy(), tc.ingressClass); retryErr != nil {
					failed++
					tc.logger.WithError(retryErr).WithFields(logrus.Fields{
						"ingress":   ing.Name,
						"namespace": ing.Namespace,
					}).Warn("Failed to migrate ingress class after webhook cleanup retry")
					continue
				}
				migrated++
				continue
			}

			failed++
			tc.logger.WithError(err).WithFields(logrus.Fields{
				"ingress":   ing.Name,
				"namespace": ing.Namespace,
			}).Warn("Failed to migrate ingress class")
			continue
		}

		migrated++
	}

	tc.logger.WithFields(logrus.Fields{
		"migrated": migrated,
		"skipped":  skipped,
		"failed":   failed,
		"target":   tc.ingressClass,
	}).Info("Completed ingress class migration from nginx to traefik")

	if failed > 0 {
		return fmt.Errorf("%d ingresses failed to migrate", failed)
	}
	return nil
}

func updateIngressClassFields(ing *networkingv1.Ingress, className string) {
	normalizedClass := strings.ToLower(strings.TrimSpace(className))
	ing.Spec.IngressClassName = &normalizedClass
	if ing.Annotations == nil {
		ing.Annotations = map[string]string{}
	}
	ing.Annotations["kubernetes.io/ingress.class"] = normalizedClass
}

func (tc *TraefikController) updateIngressClass(ctx context.Context, ing *networkingv1.Ingress, className string) error {
	updateIngressClassFields(ing, className)
	if _, err := tc.installer.K8sClient.NetworkingV1().Ingresses(ing.Namespace).Update(ctx, ing, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

func ingressUsesClass(ing *networkingv1.Ingress, className string) bool {
	target := strings.ToLower(strings.TrimSpace(className))

	if ing.Spec.IngressClassName != nil && strings.ToLower(strings.TrimSpace(*ing.Spec.IngressClassName)) == target {
		return true
	}
	if ing.Annotations != nil && strings.ToLower(strings.TrimSpace(ing.Annotations["kubernetes.io/ingress.class"])) == target {
		return true
	}
	return false
}

func validatingWebhookConfigReferencesMissingNginxService(webhooks []admissionregistrationv1.ValidatingWebhook) bool {
	for _, webhook := range webhooks {
		if webhookReferencesMissingNginxService(webhook.ClientConfig.Service, webhook.Name) {
			return true
		}
	}
	return false
}

func mutatingWebhookConfigReferencesMissingNginxService(webhooks []admissionregistrationv1.MutatingWebhook) bool {
	for _, webhook := range webhooks {
		if webhookReferencesMissingNginxService(webhook.ClientConfig.Service, webhook.Name) {
			return true
		}
	}
	return false
}

func webhookReferencesMissingNginxService(service *admissionregistrationv1.ServiceReference, webhookName string) bool {
	if service != nil &&
		service.Name == "ingress-nginx-controller-admission" &&
		service.Namespace == "ingress-nginx" {
		return true
	}
	return strings.Contains(webhookName, "nginx.ingress.kubernetes.io")
}

func setValidatingWebhookFailurePolicyIgnoreForNginx(webhooks []admissionregistrationv1.ValidatingWebhook) bool {
	changed := false
	ignore := admissionregistrationv1.Ignore
	for i := range webhooks {
		if !webhookReferencesMissingNginxService(webhooks[i].ClientConfig.Service, webhooks[i].Name) {
			continue
		}
		if webhooks[i].FailurePolicy == nil || *webhooks[i].FailurePolicy != admissionregistrationv1.Ignore {
			webhooks[i].FailurePolicy = &ignore
			changed = true
		}
	}
	return changed
}

func setMutatingWebhookFailurePolicyIgnoreForNginx(webhooks []admissionregistrationv1.MutatingWebhook) bool {
	changed := false
	ignore := admissionregistrationv1.Ignore
	for i := range webhooks {
		if !webhookReferencesMissingNginxService(webhooks[i].ClientConfig.Service, webhooks[i].Name) {
			continue
		}
		if webhooks[i].FailurePolicy == nil || *webhooks[i].FailurePolicy != admissionregistrationv1.Ignore {
			webhooks[i].FailurePolicy = &ignore
			changed = true
		}
	}
	return changed
}

// nginxDefaultConf is the nginx server configuration for routing error pages
const nginxDefaultConf = `server {
    listen       80;
    listen  [::]:80;
    server_name  localhost;

    location / {
        root   /usr/share/nginx/html;
        try_files $uri /404.html;
    }

    error_page  403 404          /404.html;
    error_page  500 502          /502.html;
    error_page  503 504          /503.html;

    location = /404.html {
        root   /usr/share/nginx/html;
    }

    location = /502.html {
        root   /usr/share/nginx/html;
    }

    location = /503.html {
        root   /usr/share/nginx/html;
    }
}
`

// errorPage404HTML is the PipeOps custom 404 error page
const errorPage404HTML = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link href="https://fonts.cdnfonts.com/css/niveau-grotesk-regular" rel="stylesheet" />
    <title>404 | Page Not Found</title>
    <style>
      * {
        margin: 0;
        padding: 0;
        box-sizing: border-box;
        font-family: "Niveau Grotesk", sans-serif;
      }
      body {
        background-color: #18181a;
      }
      img {
        width: 160px;
      }
      .container {
        max-width: 95%;
        margin: 0 auto;
      }
      header {
        padding: 2rem 0;
        position: absolute;
        top: 0;
        left: 0;
        width: 100%;
      }
      main {
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        height: 100vh;
        text-align: center;
      }
      section {
        display: flex;
        justify-content: space-between;
        gap: 3em;
        flex-wrap: wrap;
        align-items: center;
        max-width: 1300px;
        width: 90%;
        margin: 0 auto;
      }
      h1 {
        font-size: clamp(2.3rem, 6vw, 3rem);
        font-weight: 500;
        text-align: left;
        line-height: 111%;
        color: #fff;
        background: linear-gradient(
            94deg,
            rgba(255, 255, 255, 0.72) 42.56%,
            rgba(255, 255, 255, 0.24) 63.66%,
            rgba(255, 255, 255, 0.48) 83.86%
          )
          text;
        -webkit-text-fill-color: transparent;
        margin-bottom: 0.3em;
      }
      p {
        color: rgba(255, 255, 255, 0.46);
        max-width: 562px;
        text-align: left;
        font-size: clamp(0.8rem, 5vw, 1.3rem);
        font-weight: 500;
        line-height: 150%;
      }
      section img {
        width: 30vw;
        min-width: 302px;
      }
      a {
        text-decoration: none;
        color: #d8d8d8;
        text-decoration: underline;
      }
      section a.home-link {
        border-width: 1px;
        border-style: solid;
        opacity: 0.7;
        border-radius: 4rem;
        color: #fff;
        padding: 1em 2em;
        text-decoration: none;
        border-image: linear-gradient(
          90deg,
          rgb(79, 33, 234) 0%,
          rgb(190, 200, 255) 33%,
          rgb(255, 213, 211) 66%,
          rgb(255, 106, 106) 100%
        );
        background: linear-gradient(#0e0e0f, #0e0e0f) padding-box,
          linear-gradient(
              90deg,
              rgb(79, 33, 234) 0%,
              rgb(190, 200, 255) 33%,
              rgb(255, 213, 211) 66%,
              rgb(255, 106, 106) 100%
            )
            border-box;
        font-size: 1rem;
        display: block;
        width: fit-content;
        margin: 2.5em auto 0 0;
        transition: all 0.3s ease-in-out;
      }
      section a:hover,
      section a:focus {
        opacity: 1;
      }
      @media (max-width: 1020px) {
        section {
          justify-content: center;
        }
        h1 {
          padding-top: 2em;
        }
      }
    </style>
  </head>
  <body>
    <header>
      <div class="container">
        <a href="https://console.pipeops.io">
          <img
            src="https://res.cloudinary.com/djhh4kkml/image/upload/v1665733809/Pipeops/Pipeops_bcnyeo.png"
            alt="pipeOps logo"
          />
        </a>
      </div>
    </header>
    <main>
      <section>
        <div>
          <h1>Page Not Found</h1>
          <p>
            Looks like the page you requested for doesn't exist. Let's get you back on track!
          </p>
          <a class="home-link" href="https://console.pipeops.io">Back to Home</a>
        </div>
        <div>
          <img
            src="https://pub-30c11acc143348fcae20835653c5514d.r2.dev//56/404_f3db939d90.svg"
            alt="404 error"
          />
        </div>
      </section>
    </main>
  </body>
</html>
`

// errorPage502HTML is the PipeOps custom 502 error page
const errorPage502HTML = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link href="https://fonts.cdnfonts.com/css/niveau-grotesk-regular" rel="stylesheet" />
    <title>502 | Bad Gateway</title>
    <style>
      * {
        margin: 0;
        padding: 0;
        box-sizing: border-box;
        font-family: "Niveau Grotesk", sans-serif;
      }
      body {
        background-color: #18181a;
      }
      img {
        width: 160px;
      }
      .container {
        max-width: 1200px;
        width: 90%;
        margin: 0 auto;
      }
      header {
        padding: 1.5rem 0;
        position: absolute;
        top: 0;
        left: 0;
        width: 100%;
      }
      main {
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        height: 100vh;
        text-align: center;
      }
      h1 {
        font-family: "Niveau Grotesk Bold", sans-serif;
        font-size: clamp(5rem, 30vw, 10rem);
        font-weight: 800;
        color: #fff;
        text-shadow: 2px 2px #655b86;
      }
      h2 {
        font-family: "Niveau Grotesk Bold", sans-serif;
        color: #fff;
        font-size: clamp(1.5rem, 5vw, 2.5rem);
        margin-bottom: 1rem;
      }
      p {
        color: #d8d8d899;
        font-size: clamp(0.8rem, 5vw, 1.5rem);
        font-weight: 500;
        max-width: 80%;
        margin: 0 auto;
      }
      @media (max-width: 425px) {
        img {
          display: block;
          margin: 0 auto;
        }
      }
    </style>
  </head>
  <body>
    <header>
      <div class="container">
        <a href="https://pipeops.io">
          <img
            src="https://res.cloudinary.com/djhh4kkml/image/upload/v1665733809/Pipeops/Pipeops_bcnyeo.png"
            alt="pipeOps logo"
          />
        </a>
      </div>
    </header>
    <main>
      <div class="container">
        <h1>502</h1>
        <h2>Oops Error!!!</h2>
        <p>
          502 Bad Gateway. The server encountered a temporary error and could not complete your
          request. Please try again later.
        </p>
      </div>
    </main>
  </body>
</html>
`

// errorPage503HTML is the PipeOps custom 503 error page
const errorPage503HTML = `<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link href="https://fonts.cdnfonts.com/css/niveau-grotesk-regular" rel="stylesheet" />
    <title>503 | Project Unavailable</title>
    <style>
      * {
        margin: 0;
        padding: 0;
        box-sizing: border-box;
        font-family: "Niveau Grotesk", sans-serif;
      }
      body {
        background-color: #18181a;
      }
      img {
        width: 160px;
      }
      .container {
        max-width: 95%;
        margin: 0 auto;
      }
      header {
        padding: 2rem 0;
        position: absolute;
        top: 0;
        left: 0;
        width: 100%;
      }
      main {
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        height: 100vh;
        text-align: center;
      }
      section {
        display: flex;
        justify-content: space-between;
        gap: 3em;
        flex-wrap: wrap;
        align-items: center;
        max-width: 1300px;
        width: 90%;
        margin: 0 auto;
      }
      h1 {
        font-size: clamp(2.3rem, 6vw, 3rem);
        font-weight: 500;
        text-align: left;
        line-height: 111%;
        color: #fff;
        background: linear-gradient(
            94deg,
            rgba(255, 255, 255, 0.72) 42.56%,
            rgba(255, 255, 255, 0.24) 63.66%,
            rgba(255, 255, 255, 0.48) 83.86%
          )
          text;
        -webkit-text-fill-color: transparent;
        margin-bottom: 0.3em;
      }
      p {
        color: rgba(255, 255, 255, 0.46);
        max-width: 562px;
        text-align: left;
        font-size: clamp(0.8rem, 5vw, 1.3rem);
        font-weight: 500;
        line-height: 150%;
      }
      section img {
        width: 30vw;
        min-width: 302px;
      }
      a {
        text-decoration: none;
        color: #d8d8d8;
        text-decoration: underline;
      }
      section a.home-link {
        border-width: 1px;
        border-style: solid;
        opacity: 0.7;
        border-radius: 4rem;
        color: #fff;
        padding: 1em 2em;
        text-decoration: none;
        border-image: linear-gradient(
          90deg,
          rgb(79, 33, 234) 0%,
          rgb(190, 200, 255) 33%,
          rgb(255, 213, 211) 66%,
          rgb(255, 106, 106) 100%
        );
        background: linear-gradient(#0e0e0f, #0e0e0f) padding-box,
          linear-gradient(
              90deg,
              rgb(79, 33, 234) 0%,
              rgb(190, 200, 255) 33%,
              rgb(255, 213, 211) 66%,
              rgb(255, 106, 106) 100%
            )
            border-box;
        font-size: 1rem;
        display: block;
        width: fit-content;
        margin: 2.5em auto 0 0;
        transition: all 0.3s ease-in-out;
      }
      section a:hover,
      section a:focus {
        opacity: 1;
      }
      @media (max-width: 1020px) {
        section {
          justify-content: center;
        }
        h1 {
          padding-top: 2em;
        }
      }
    </style>
  </head>
  <body>
    <header>
      <div class="container">
        <a href="https://console.pipeops.io">
          <img
            src="https://res.cloudinary.com/djhh4kkml/image/upload/v1665733809/Pipeops/Pipeops_bcnyeo.png"
            alt="pipeOps logo"
          />
        </a>
      </div>
    </header>
    <main>
      <section>
        <div>
          <h1>Project Unavailable</h1>
          <p>
            Oops! Your project is currently unavailable, possibly due to a paused, expired
            subscription, and due to misconfiguration. Please verify your server, check your
            subscription status, and review your project settings. Visit our
            <a target="_blank" href="https://docs.pipeops.io/docs/category/troubleshooting"
              >troubleshooting page</a
            >
            for assistance.
          </p>
          <a class="home-link" href="https://console.pipeops.io">Back to Home</a>
        </div>
        <div>
          <img
            src="https://pub-30c11acc143348fcae20835653c5514d.r2.dev//56/503_4ecd842aa2.svg"
            alt="503 error"
          />
        </div>
      </section>
    </main>
  </body>
</html>
`

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
