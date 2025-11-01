package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// IngressWatcher watches ingress resources and reports routes to control plane
type IngressWatcher struct {
	k8sClient   kubernetes.Interface
	clusterUUID string
	logger      *logrus.Logger
	routeSync   RouteSync
	informer    cache.SharedIndexInformer
	stopCh      chan struct{}
	mu          sync.RWMutex
	isRunning   bool
}

// RouteSync defines the interface for syncing routes to control plane
type RouteSync interface {
	SendRoutes(action string, routes []Route) error
}

// Route represents an ingress route
type Route struct {
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	PathType    string            `json:"path_type"`
	Service     string            `json:"service"`
	Namespace   string            `json:"namespace"`
	Port        int32             `json:"port"`
	TLS         bool              `json:"tls"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// NewIngressWatcher creates a new ingress watcher
func NewIngressWatcher(k8sClient kubernetes.Interface, clusterUUID string, routeSync RouteSync, logger *logrus.Logger) *IngressWatcher {
	return &IngressWatcher{
		k8sClient:   k8sClient,
		clusterUUID: clusterUUID,
		logger:      logger,
		routeSync:   routeSync,
		stopCh:      make(chan struct{}),
	}
}

// Start begins watching ingress resources
func (w *IngressWatcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.isRunning {
		w.mu.Unlock()
		return fmt.Errorf("ingress watcher already running")
	}
	w.isRunning = true
	w.mu.Unlock()

	w.logger.Info("Starting ingress watcher for gateway proxy")

	// Create shared informer for all ingresses in all namespaces
	w.informer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return w.k8sClient.NetworkingV1().Ingresses("").List(ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return w.k8sClient.NetworkingV1().Ingresses("").Watch(ctx, options)
			},
		},
		&networkingv1.Ingress{},
		0, // No resync period - we rely on events
		cache.Indexers{},
	)

	// Add event handlers
	w.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if ingress, ok := obj.(*networkingv1.Ingress); ok {
				w.onIngressEvent(ingress, "add")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if ingress, ok := newObj.(*networkingv1.Ingress); ok {
				w.onIngressEvent(ingress, "update")
			}
		},
		DeleteFunc: func(obj interface{}) {
			if ingress, ok := obj.(*networkingv1.Ingress); ok {
				w.onIngressEvent(ingress, "delete")
			}
		},
	})

	// Start informer in background
	go w.informer.Run(w.stopCh)

	// Wait for cache sync
	if !cache.WaitForCacheSync(w.stopCh, w.informer.HasSynced) {
		return fmt.Errorf("failed to sync ingress cache")
	}

	w.logger.Info("Ingress watcher started and synced")

	// Perform initial sync of existing ingresses
	if err := w.syncExistingIngresses(ctx); err != nil {
		w.logger.WithError(err).Warn("Failed to sync existing ingresses (non-fatal)")
	}

	return nil
}

// Stop stops the ingress watcher
func (w *IngressWatcher) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.isRunning {
		return nil
	}

	w.logger.Info("Stopping ingress watcher")
	close(w.stopCh)
	w.isRunning = false

	return nil
}

// onIngressEvent handles ingress add/update/delete events
func (w *IngressWatcher) onIngressEvent(ingress *networkingv1.Ingress, action string) {
	routes := w.extractRoutes(ingress)
	if len(routes) == 0 {
		w.logger.WithFields(logrus.Fields{
			"ingress":   ingress.Name,
			"namespace": ingress.Namespace,
			"action":    action,
		}).Debug("Ingress has no routes, skipping")
		return
	}

	w.logger.WithFields(logrus.Fields{
		"ingress":   ingress.Name,
		"namespace": ingress.Namespace,
		"action":    action,
		"routes":    len(routes),
	}).Info("Ingress event detected")

	if err := w.routeSync.SendRoutes(action, routes); err != nil {
		w.logger.WithError(err).WithFields(logrus.Fields{
			"ingress":   ingress.Name,
			"namespace": ingress.Namespace,
			"action":    action,
		}).Error("Failed to sync routes to control plane")
	}
}

// extractRoutes extracts route information from an ingress resource
func (w *IngressWatcher) extractRoutes(ingress *networkingv1.Ingress) []Route {
	var routes []Route

	hasTLS := len(ingress.Spec.TLS) > 0

	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}

		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil {
				continue
			}

			pathType := "Prefix"
			if path.PathType != nil {
				pathType = string(*path.PathType)
			}

			port := int32(0)
			if path.Backend.Service.Port.Number != 0 {
				port = path.Backend.Service.Port.Number
			}

			route := Route{
				Host:        rule.Host,
				Path:        path.Path,
				PathType:    pathType,
				Service:     path.Backend.Service.Name,
				Namespace:   ingress.Namespace,
				Port:        port,
				TLS:         hasTLS,
				Annotations: ingress.Annotations,
			}

			routes = append(routes, route)
		}
	}

	return routes
}

// syncExistingIngresses syncs all existing ingresses on startup
func (w *IngressWatcher) syncExistingIngresses(ctx context.Context) error {
	w.logger.Info("Syncing existing ingresses to control plane")

	ingresses, err := w.k8sClient.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ingresses: %w", err)
	}

	totalRoutes := 0
	for _, ingress := range ingresses.Items {
		routes := w.extractRoutes(&ingress)
		if len(routes) > 0 {
			if err := w.routeSync.SendRoutes("add", routes); err != nil {
				w.logger.WithError(err).WithFields(logrus.Fields{
					"ingress":   ingress.Name,
					"namespace": ingress.Namespace,
				}).Warn("Failed to sync ingress routes")
				continue
			}
			totalRoutes += len(routes)
		}
	}

	w.logger.WithFields(logrus.Fields{
		"ingresses": len(ingresses.Items),
		"routes":    totalRoutes,
	}).Info("Finished syncing existing ingresses")

	return nil
}

// GetRouteCount returns the current number of routes being watched
func (w *IngressWatcher) GetRouteCount() int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ingresses, err := w.k8sClient.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		w.logger.WithError(err).Debug("Failed to get route count")
		return 0
	}

	count := 0
	for _, ingress := range ingresses.Items {
		routes := w.extractRoutes(&ingress)
		count += len(routes)
	}

	return count
}

// DetectClusterType checks if the cluster is public or private
func DetectClusterType(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) (isPrivate bool, err error) {
	logger.Info("Detecting cluster connectivity type...")

	// Check if ingress-nginx service exists and has LoadBalancer type
	svc, err := k8sClient.CoreV1().Services("ingress-nginx").Get(ctx, "ingress-nginx-controller", metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Debug("Ingress-nginx service not found, assuming private cluster")
		return true, nil
	}

	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		logger.WithField("type", svc.Spec.Type).Info("Ingress service is not LoadBalancer type, cluster is private")
		return true, nil
	}

	// Check if LoadBalancer already has external IP
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		externalIP := ""
		if svc.Status.LoadBalancer.Ingress[0].IP != "" {
			externalIP = svc.Status.LoadBalancer.Ingress[0].IP
		} else if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
			externalIP = svc.Status.LoadBalancer.Ingress[0].Hostname
		}

		if externalIP != "" {
			logger.WithField("external_ip", externalIP).Info("Cluster has public LoadBalancer, gateway proxy not needed")
			return false, nil
		}
	}

	// Wait up to 2 minutes for LoadBalancer to get external IP
	logger.Info("LoadBalancer service found, waiting for external IP assignment...")
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return true, ctx.Err()

		case <-timeout:
			logger.Info("LoadBalancer did not receive external IP after 2 minutes, treating as private cluster")
			return true, nil

		case <-ticker.C:
			svc, err = k8sClient.CoreV1().Services("ingress-nginx").Get(ctx, "ingress-nginx-controller", metav1.GetOptions{})
			if err != nil {
				logger.WithError(err).Debug("Failed to check LoadBalancer status")
				continue
			}

			if len(svc.Status.LoadBalancer.Ingress) > 0 {
				externalIP := ""
				if svc.Status.LoadBalancer.Ingress[0].IP != "" {
					externalIP = svc.Status.LoadBalancer.Ingress[0].IP
				} else if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
					externalIP = svc.Status.LoadBalancer.Ingress[0].Hostname
				}

				if externalIP != "" {
					logger.WithField("external_ip", externalIP).Info("Cluster has public LoadBalancer, gateway proxy not needed")
					return false, nil
				}
			}
		}
	}
}
