package ingress

import (
	"context"
	"fmt"

	"github.com/pipeops/pipeops-vm-agent/internal/helm"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TraefikController manages the Traefik ingress controller installation
type TraefikController struct {
	installer    *helm.HelmInstaller
	logger       *logrus.Logger
	namespace    string
	chartRepo    string
	chartName    string
	releaseName  string
}

// NewTraefikController creates a new Traefik controller manager
func NewTraefikController(installer *helm.HelmInstaller, logger *logrus.Logger) *TraefikController {
	return &TraefikController{
		installer:   installer,
		logger:      logger,
		namespace:   "kube-system", // Traefik is often in kube-system (e.g. k3s default), but we can configure this
		chartRepo:   "https://traefik.github.io/charts",
		chartName:   "traefik/traefik",
		releaseName: "traefik",
	}
}

// IsInstalled checks if Traefik is already installed
func (tc *TraefikController) IsInstalled(ctx context.Context) bool {
	// Check for deployment in kube-system (common) or default namespace
	namespaces := []string{"kube-system", "traefik", "default", "ingress-nginx"} // Check common namespaces
	
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

// Install installs or upgrades Traefik
func (tc *TraefikController) Install(ctx context.Context, profile types.ResourceProfile) error {
	tc.logger.Info("Installing/Upgrading Traefik Ingress Controller...")

	// Add Helm repository
	if err := tc.installer.AddRepo(ctx, "traefik", tc.chartRepo); err != nil {
		return fmt.Errorf("failed to add traefik helm repo: %w", err)
	}

	// Base values
	values := map[string]interface{}{
		"deployment": map[string]interface{}{
			"enabled": true,
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
		"ports": map[string]interface{}{
			"web": map[string]interface{}{
				"redirectTo": "websecure",
			},
			"websecure": map[string]interface{}{
				"tls": map[string]interface{}{
					"enabled": true,
				},
			},
		},
		"service": map[string]interface{}{
			"enabled": true,
			"type": "LoadBalancer", // Default for most cloud/local clusters
		},
		// Ensure prometheus metrics are enabled if we want to monitor it later
		"metrics": map[string]interface{}{
			"prometheus": map[string]interface{}{
				"enabled": true,
			},
		},
	}

	// Tune based on resource profile
	if profile == types.ProfileLow {
		tc.logger.Info("Applying Low Profile tuning for Traefik")
		values["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]interface{}{"cpu": "300m", "memory": "128Mi"},
		}
	} else {
		// Medium/High profile defaults
		values["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
			"limits":   map[string]interface{}{"cpu": "500m", "memory": "256Mi"},
		}
	}

	// Install the chart
	release := &helm.HelmRelease{
		Name:      tc.releaseName,
		Namespace: tc.namespace,
		Chart:     tc.chartName,
		Repo:      tc.chartRepo,
		Version:   "", // Use latest stable
		Values:    values,
	}

	if err := tc.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install Traefik: %w", err)
	}

	tc.logger.Info("✓ Traefik Ingress Controller installed/upgraded successfully")
	return nil
}

// Uninstall removes Traefik
func (tc *TraefikController) Uninstall(ctx context.Context) error {
	tc.logger.Info("Uninstalling Traefik...")
	return tc.installer.Uninstall(ctx, tc.releaseName, tc.namespace)
}
