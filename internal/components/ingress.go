package components

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// IngressController manages the ingress controller installation
type IngressController struct {
	installer    *HelmInstaller
	k8sClient    *kubernetes.Clientset
	logger       *logrus.Logger
	namespace    string
	chartRepo    string
	chartName    string
	ingressClass string
}

// IngressConfig holds configuration for an ingress resource
type IngressConfig struct {
	Name        string
	Namespace   string
	Host        string
	ServiceName string
	ServicePort int
	TLSEnabled  bool
	Annotations map[string]string
}

// NewIngressController creates a new ingress controller manager
func NewIngressController(installer *HelmInstaller, k8sClient *kubernetes.Clientset, logger *logrus.Logger) *IngressController {
	return &IngressController{
		installer:    installer,
		k8sClient:    k8sClient,
		logger:       logger,
		namespace:    "ingress-nginx",
		chartRepo:    "https://kubernetes.github.io/ingress-nginx",
		chartName:    "ingress-nginx/ingress-nginx",
		ingressClass: "nginx",
	}
}

// Install installs the NGINX ingress controller
func (ic *IngressController) Install() error {
	ic.logger.WithFields(logrus.Fields{
		"namespace": ic.namespace,
		"chart":     ic.chartName,
	}).Info("Installing NGINX Ingress Controller")

	// Create namespace if it doesn't exist
	if err := ic.createNamespace(); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}

	// Add Helm repository
	ctx := context.Background()
	if err := ic.installer.addRepo(ctx, ic.chartName, ic.chartRepo); err != nil {
		return fmt.Errorf("failed to add helm repo: %w", err)
	}

	// Prepare values for NGINX ingress controller
	values := map[string]interface{}{
		"controller": map[string]interface{}{
			"ingressClass": ic.ingressClass,
			"service": map[string]interface{}{
				"type": "NodePort", // Use NodePort for minikube/development
				"nodePorts": map[string]interface{}{
					"http":  30080,
					"https": 30443,
				},
			},
			"hostNetwork": false, // Set to true for bare-metal if needed
			"metrics": map[string]interface{}{
				"enabled": true,
				"serviceMonitor": map[string]interface{}{
					"enabled": false, // Can enable if Prometheus Operator is installed
				},
			},
			// Resource limits
			"resources": map[string]interface{}{
				"requests": map[string]interface{}{
					"cpu":    "100m",
					"memory": "90Mi",
				},
			},
		},
		"defaultBackend": map[string]interface{}{
			"enabled": true,
		},
	}

	// Install the chart
	release := &HelmRelease{
		Name:      "ingress-nginx",
		Namespace: ic.namespace,
		Chart:     ic.chartName,
		Repo:      ic.chartRepo,
		Version:   "", // latest
		Values:    values,
	}

	if err := ic.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install ingress controller: %w", err)
	}

	// Wait for ingress controller to be ready
	if err := ic.waitForReady(); err != nil {
		return fmt.Errorf("ingress controller did not become ready: %w", err)
	}

	ic.logger.Info("✓ NGINX Ingress Controller installed successfully")
	return nil
}

// createNamespace creates the ingress namespace
func (ic *IngressController) createNamespace() error {
	ctx := context.Background()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ic.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "ingress-nginx",
				"app.kubernetes.io/instance":  "ingress-nginx",
				"app.kubernetes.io/component": "controller",
			},
		},
	}

	_, err := ic.k8sClient.CoreV1().Namespaces().Get(ctx, ic.namespace, metav1.GetOptions{})
	if err != nil {
		// Namespace doesn't exist, create it
		_, err = ic.k8sClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
		ic.logger.WithField("namespace", ic.namespace).Debug("Created namespace")
	}

	return nil
}

// waitForReady waits for the ingress controller to be ready
func (ic *IngressController) waitForReady() error {
	ic.logger.Info("Waiting for ingress controller to be ready...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for ingress controller")
		case <-ticker.C:
			// Check if deployment is ready
			deployment, err := ic.k8sClient.AppsV1().Deployments(ic.namespace).Get(
				context.Background(),
				"ingress-nginx-controller",
				metav1.GetOptions{},
			)
			if err != nil {
				ic.logger.WithError(err).Debug("Ingress controller deployment not found yet")
				continue
			}

			if deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == deployment.Status.Replicas {
				ic.logger.Info("✓ Ingress controller is ready")
				return nil
			}

			ic.logger.WithFields(logrus.Fields{
				"ready":    deployment.Status.ReadyReplicas,
				"expected": deployment.Status.Replicas,
			}).Debug("Waiting for ingress controller replicas to be ready")
		}
	}
}

// CreateIngress creates an ingress resource for a service
func (ic *IngressController) CreateIngress(config IngressConfig) error {
	ctx := context.Background()

	ic.logger.WithFields(logrus.Fields{
		"name":      config.Name,
		"namespace": config.Namespace,
		"host":      config.Host,
		"service":   config.ServiceName,
	}).Debug("Creating ingress resource")

	pathTypePrefix := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.Name,
			Namespace: config.Namespace,
			Annotations: mergeMaps(map[string]string{
				"kubernetes.io/ingress.class":                    ic.ingressClass,
				"nginx.ingress.kubernetes.io/rewrite-target":     "/",
				"nginx.ingress.kubernetes.io/ssl-redirect":       "false",
				"nginx.ingress.kubernetes.io/force-ssl-redirect": "false",
			}, config.Annotations),
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "pipeops-agent",
				"app.kubernetes.io/component":  "monitoring",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ic.ingressClass,
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
	existingIngress, err := ic.k8sClient.NetworkingV1().Ingresses(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
	if err != nil {
		// Create new ingress
		_, err = ic.k8sClient.NetworkingV1().Ingresses(config.Namespace).Create(ctx, ingress, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create ingress: %w", err)
		}
		ic.logger.WithField("name", config.Name).Info("✓ Created ingress resource")
	} else {
		// Update existing ingress
		ingress.ResourceVersion = existingIngress.ResourceVersion
		_, err = ic.k8sClient.NetworkingV1().Ingresses(config.Namespace).Update(ctx, ingress, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update ingress: %w", err)
		}
		ic.logger.WithField("name", config.Name).Info("✓ Updated ingress resource")
	}

	return nil
}

// GetIngressURL returns the URL to access an ingress
func (ic *IngressController) GetIngressURL(namespace, name string) (string, error) {
	ctx := context.Background()

	// Get the ingress controller service
	svc, err := ic.k8sClient.CoreV1().Services(ic.namespace).Get(ctx, "ingress-nginx-controller", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get ingress service: %w", err)
	}

	// Get ingress
	ingress, err := ic.k8sClient.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
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
			if port.Port == 80 {
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

// mergeMaps merges multiple maps, with later maps overriding earlier ones
func mergeMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// IsInstalled checks if the ingress controller is installed
func (ic *IngressController) IsInstalled() bool {
	ctx := context.Background()
	_, err := ic.k8sClient.AppsV1().Deployments(ic.namespace).Get(
		ctx,
		"ingress-nginx-controller",
		metav1.GetOptions{},
	)
	return err == nil
}
