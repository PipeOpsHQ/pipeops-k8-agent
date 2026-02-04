package ingress

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/cloud"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Metrics for route sync operations
var (
	routeSyncRetries = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_route_sync_retries_total",
		Help: "Total number of route sync retry attempts",
	})
	routeSyncFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_route_sync_failures_total",
		Help: "Total number of route sync failures after all retries",
	})
	routeSyncSuccess = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_route_sync_success_total",
		Help: "Total number of successful route syncs",
	})
)

// IngressWatcher watches ingress resources and reports routes to controller
type IngressWatcher struct {
	k8sClient        kubernetes.Interface
	clusterUUID      string
	logger           *logrus.Logger
	controllerClient RouteClient
	publicEndpoint   string // LoadBalancer IP:port or empty for tunnel
	routingMode      string // "direct" or "tunnel"
	informer         cache.SharedIndexInformer
	stopCh           chan struct{}
	mu               sync.RWMutex
	isRunning        bool
	routeCache       map[string]*Route                 // hostname -> route mapping for quick lookup
	ingressService   *IngressService                   // The Ingress Controller service to route traffic to
	compatConfig     *types.IngressCompatibilityConfig // Compatibility settings for existing clusters
}

// IngressService represents the ingress controller service details
type IngressService struct {
	Name      string
	Namespace string
	Port      int32
}

// Route represents an ingress route (internal representation)
type Route struct {
	Host        string            `json:"host"`
	Path        string            `json:"path"`
	PathType    string            `json:"path_type"`
	Service     string            `json:"service"`
	Namespace   string            `json:"namespace"`
	IngressName string            `json:"ingress_name"`
	Port        int32             `json:"port"`
	TLS         bool              `json:"tls"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// NewIngressWatcher creates a new ingress watcher
func NewIngressWatcher(k8sClient kubernetes.Interface, clusterUUID string, controllerClient RouteClient, logger *logrus.Logger, publicEndpoint, routingMode string, compatConfig *types.IngressCompatibilityConfig) *IngressWatcher {
	return &IngressWatcher{
		k8sClient:        k8sClient,
		clusterUUID:      clusterUUID,
		logger:           logger,
		controllerClient: controllerClient,
		publicEndpoint:   publicEndpoint,
		routingMode:      routingMode,
		stopCh:           make(chan struct{}),
		routeCache:       make(map[string]*Route),
		compatConfig:     compatConfig,
	}
}

// detectIngressControllerService attempts to find the Ingress Controller service
func (w *IngressWatcher) detectIngressControllerService(ctx context.Context) (*IngressService, error) {
	// Common ingress controller service names/namespaces to check
	// Priority:
	// 1. ingress-nginx/ingress-nginx-controller (Standard Helm chart)
	// 2. kube-system/rke2-ingress-nginx-controller (RKE2)
	// 3. kube-system/traefik (K3s default)

	candidates := []struct {
		Namespace string
		Name      string
	}{
		{"ingress-nginx", "ingress-nginx-controller"},
		{"kube-system", "rke2-ingress-nginx-controller"},
		{"kube-system", "traefik"},
		{"traefik", "traefik"},
	}

	for _, c := range candidates {
		svc, err := w.k8sClient.CoreV1().Services(c.Namespace).Get(ctx, c.Name, metav1.GetOptions{})
		if err == nil {
			if s := w.validateControllerService(svc); s != nil {
				return s, nil
			}
		}
	}

	// Fallback: Search by label in common namespaces
	searchNamespaces := []string{"kube-system", "traefik", "ingress-nginx", "default"}
	labelsToCheck := []string{"app.kubernetes.io/name=traefik", "app.kubernetes.io/name=ingress-nginx", "k8s-app=traefik-ingress-lb"}

	for _, ns := range searchNamespaces {
		for _, label := range labelsToCheck {
			services, err := w.k8sClient.CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: label})
			if err == nil {
				for _, svc := range services.Items {
					if s := w.validateControllerService(&svc); s != nil {
						return s, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("ingress controller service not found")
}

// validateControllerService checks if a service is a valid ingress controller and returns details
func (w *IngressWatcher) validateControllerService(svc *corev1.Service) *IngressService {
	// Find the HTTP/HTTPS port
	var port int32
	for _, p := range svc.Spec.Ports {
		if p.Name == "http" || p.Name == "web" || p.Port == 80 {
			port = p.Port
			break
		}
		if p.Name == "https" || p.Name == "websecure" || p.Port == 443 {
			if port == 0 {
				port = p.Port
			}
		}
	}

	if port != 0 {
		return &IngressService{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Port:      port,
		}
	}
	return nil
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

	// Detect Ingress Controller Service
	// We need to know which service to route traffic to (the ingress controller itself)
	// This ensures all tunnel traffic goes through the ingress controller
	controllerSvc, err := w.detectIngressControllerService(context.Background())
	if err != nil {
		w.logger.WithError(err).Warn("Failed to detect ingress controller service - will retry during sync")
		// We don't fail here, but route registration might fail or fallback if not found
	} else {
		w.ingressService = controllerSvc
		w.logger.WithFields(logrus.Fields{
			"service":   controllerSvc.Name,
			"namespace": controllerSvc.Namespace,
			"port":      controllerSvc.Port,
		}).Info("Detected Ingress Controller service for tunneling")

		// Ensure NGINX Ingress Controller ConfigMap has correct settings to prevent SSL redirect loops
		// Only run this if we detect NGINX (Traefik uses different configuration mechanisms)
		if strings.Contains(controllerSvc.Name, "nginx") {
			if err := EnsureNGINXConfigMapSettings(w.k8sClient, controllerSvc.Namespace, w.logger, w.compatConfig); err != nil {
				w.logger.WithError(err).Warn("Failed to ensure NGINX ConfigMap settings - SSL redirects may not work correctly")
				// Don't fail the watcher - this is a best-effort attempt to fix configuration
			}
		}
	}

	// Create shared informer for all ingresses in all namespaces
	// Use context.Background() to ensure watcher survives agent context cancellation
	// The watcher manages its own lifecycle via stopCh
	watchCtx := context.Background()
	w.informer = cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return w.k8sClient.NetworkingV1().Ingresses("").List(watchCtx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return w.k8sClient.NetworkingV1().Ingresses("").Watch(watchCtx, options)
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
	// Use watchCtx to ensure sync completes even if caller's context is cancelled
	if err := w.syncExistingIngresses(watchCtx); err != nil {
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

// UpdateClusterUUID updates the cluster UUID (e.g., after re-registration)
func (w *IngressWatcher) UpdateClusterUUID(clusterUUID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clusterUUID = clusterUUID
	w.logger.WithField("cluster_uuid", clusterUUID).Info("Updated gateway proxy cluster UUID")
}

// TriggerResync triggers a re-sync of all ingresses to the controller
func (w *IngressWatcher) TriggerResync() error {
	w.mu.RLock()
	if !w.isRunning {
		w.mu.RUnlock()
		return fmt.Errorf("ingress watcher not running")
	}
	w.mu.RUnlock()

	w.logger.Info("Triggering ingress re-sync after reconnection")
	ctx := context.Background()
	if err := w.syncExistingIngresses(ctx); err != nil {
		return fmt.Errorf("failed to re-sync ingresses: %w", err)
	}
	w.logger.Info("Ingress re-sync completed successfully")
	return nil
}

// onIngressEvent handles ingress add/update/delete events
func (w *IngressWatcher) onIngressEvent(ingress *networkingv1.Ingress, action string) {
	// Only process ingresses managed by PipeOps
	if !w.isPipeOpsManaged(ingress) {
		w.logger.WithFields(logrus.Fields{
			"ingress":   ingress.Name,
			"namespace": ingress.Namespace,
			"action":    action,
		}).Debug("Skipping non-PipeOps managed ingress")
		return
	}

	ctx := context.Background()
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
		"ingress":      ingress.Name,
		"namespace":    ingress.Namespace,
		"action":       action,
		"routes":       len(routes),
		"routing_mode": w.routingMode,
	}).Info("Ingress event detected")

	switch action {
	case "add", "update":
		// Ensure we have the ingress controller service info
		if w.ingressService == nil {
			if svc, err := w.detectIngressControllerService(ctx); err == nil {
				w.ingressService = svc
			} else {
				w.logger.WithError(err).Warn("Cannot register routes: Ingress Controller service not found")
				return
			}
		}

		// Register each route individually
		for _, route := range routes {
			// Update route cache for local lookup
			// IMPORTANT: For local lookup, we still want to know the REAL backend service
			// so that we can verify permissions or debug, but for the actual proxying
			// via tunnel, we might want to point to the ingress controller.
			// However, the `handleProxyRequest` logic uses this cache to resolve the service.
			// If we want ALL traffic to go through ingress controller, we should update the cache
			// to point to the ingress controller service, OR update `handleProxyRequest` to use
			// the ingress controller if a route is found.
			//
			// Given the requirement "control plane should only expose 2 thing via the tunnel the k8 api and ingress nginx service",
			// we should register the route with the control plane pointing to the Ingress Controller.

			// We'll store the ORIGINAL route in the cache for debugging/reference,
			// but the Control Plane will receive the Ingress Controller as the target.
			w.mu.Lock()
			w.routeCache[route.Host] = &route
			w.mu.Unlock()

			// Extract deployment information from ingress annotations
			deploymentID, deploymentName := w.extractDeploymentInfo(ingress, route.Host)

			// USE INGRESS CONTROLLER SERVICE FOR TUNNEL TARGET
			// This ensures the tunnel connects to the Ingress Controller, which then routes to the app
			req := RegisterRouteRequest{
				Hostname:       route.Host,
				ClusterUUID:    w.clusterUUID,
				Namespace:      w.ingressService.Namespace, // Target: Ingress Controller Namespace
				ServiceName:    w.ingressService.Name,      // Target: Ingress Controller Service
				ServicePort:    w.ingressService.Port,      // Target: Ingress Controller Port
				IngressName:    route.IngressName,
				Path:           route.Path,
				PathType:       route.PathType,
				TLS:            route.TLS,
				Annotations:    route.Annotations,
				PublicEndpoint: w.publicEndpoint,
				RoutingMode:    w.routingMode,
				DeploymentID:   deploymentID,
				DeploymentName: deploymentName,
			}

			if err := w.controllerClient.RegisterRoute(ctx, req); err != nil {
				w.logger.WithError(err).WithFields(logrus.Fields{
					"hostname":  route.Host,
					"ingress":   ingress.Name,
					"namespace": ingress.Namespace,
				}).Error("Failed to register route with controller")
			}
		}

	case "delete":
		// Unregister each route by hostname
		for _, route := range routes {
			// Remove from route cache
			w.mu.Lock()
			delete(w.routeCache, route.Host)
			w.mu.Unlock()

			if err := w.controllerClient.UnregisterRoute(ctx, route.Host); err != nil {
				w.logger.WithError(err).WithFields(logrus.Fields{
					"hostname":  route.Host,
					"ingress":   ingress.Name,
					"namespace": ingress.Namespace,
				}).Error("Failed to unregister route from controller")
			}
		}
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
			} else if path.Backend.Service.Port.Name != "" {
				// Port specified by name - need to resolve it from the service
				resolvedPort, err := w.resolveServicePort(ingress.Namespace, path.Backend.Service.Name, path.Backend.Service.Port.Name)
				if err != nil {
					w.logger.WithError(err).WithFields(logrus.Fields{
						"service":   path.Backend.Service.Name,
						"namespace": ingress.Namespace,
						"portName":  path.Backend.Service.Port.Name,
					}).Warn("Failed to resolve service port by name, skipping route")
					continue
				}
				port = resolvedPort
			}

			// Skip routes with invalid port (security: prevents exposing K8s API)
			if port == 0 {
				w.logger.WithFields(logrus.Fields{
					"ingress":   ingress.Name,
					"namespace": ingress.Namespace,
					"host":      rule.Host,
					"service":   path.Backend.Service.Name,
				}).Warn("Skipping route with port 0 - invalid configuration")
				continue
			}

			// Validate that the service is safe to expose
			if !w.isServiceAllowed(ingress.Namespace, path.Backend.Service.Name) {
				w.logger.WithFields(logrus.Fields{
					"ingress":   ingress.Name,
					"namespace": ingress.Namespace,
					"host":      rule.Host,
					"service":   path.Backend.Service.Name,
				}).Warn("Skipping route to disallowed service - potential security risk")
				continue
			}

			route := Route{
				Host:        rule.Host,
				Path:        path.Path,
				PathType:    pathType,
				Service:     path.Backend.Service.Name,
				Namespace:   ingress.Namespace,
				IngressName: ingress.Name,
				Port:        port,
				TLS:         hasTLS,
				Annotations: ingress.Annotations,
			}

			routes = append(routes, route)
		}
	}

	return routes
}

// resolveServicePort resolves a service port name to its port number
func (w *IngressWatcher) resolveServicePort(namespace, serviceName, portName string) (int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	service, err := w.k8sClient.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get service %s/%s: %w", namespace, serviceName, err)
	}

	for _, port := range service.Spec.Ports {
		if port.Name == portName {
			return port.Port, nil
		}
	}

	return 0, fmt.Errorf("port name %s not found in service %s/%s", portName, namespace, serviceName)
}

// syncExistingIngresses syncs all existing ingresses on startup using bulk API
func (w *IngressWatcher) syncExistingIngresses(ctx context.Context) error {
	w.logger.Info("Syncing existing ingresses to controller")

	// Ensure we have the ingress controller service info
	if w.ingressService == nil {
		if svc, err := w.detectIngressControllerService(ctx); err == nil {
			w.ingressService = svc
		} else {
			return fmt.Errorf("cannot sync routes: Ingress Controller service not found")
		}
	}

	ingressList, err := w.k8sClient.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ingresses: %w", err)
	}

	// Convert ingresses to bulk sync format
	ingressData := make([]IngressData, 0, len(ingressList.Items))
	totalRoutes := 0

	for _, ingress := range ingressList.Items {
		// Only sync PipeOps-managed ingresses
		if !w.isPipeOpsManaged(&ingress) {
			w.logger.WithFields(logrus.Fields{
				"ingress":   ingress.Name,
				"namespace": ingress.Namespace,
			}).Debug("Skipping non-PipeOps managed ingress during sync")
			continue
		}

		hasTLS := len(ingress.Spec.TLS) > 0

		// Group rules by host
		rulesByHost := make(map[string]*IngressRule)

		for _, rule := range ingress.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}

			// Create or get rule for this host
			ingressRule, exists := rulesByHost[rule.Host]
			if !exists {
				ingressRule = &IngressRule{
					Host:  rule.Host,
					TLS:   hasTLS,
					Paths: []IngressPath{},
				}
				rulesByHost[rule.Host] = ingressRule
			}

			// Add paths for this rule
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
				} else if path.Backend.Service.Port.Name != "" {
					// Port specified by name - need to resolve it from the service
					resolvedPort, err := w.resolveServicePort(ingress.Namespace, path.Backend.Service.Name, path.Backend.Service.Port.Name)
					if err != nil {
						w.logger.WithError(err).WithFields(logrus.Fields{
							"service":   path.Backend.Service.Name,
							"namespace": ingress.Namespace,
							"portName":  path.Backend.Service.Port.Name,
						}).Warn("Failed to resolve service port by name during bulk sync, skipping path")
						continue
					}
					port = resolvedPort
				}

				// Skip paths with invalid port (security: prevents exposing K8s API)
				if port == 0 {
					w.logger.WithFields(logrus.Fields{
						"ingress":   ingress.Name,
						"namespace": ingress.Namespace,
						"host":      rule.Host,
						"service":   path.Backend.Service.Name,
					}).Warn("Skipping path with port 0 during bulk sync - invalid configuration")
					continue
				}

				// Validate that the service is safe to expose
				if !w.isServiceAllowed(ingress.Namespace, path.Backend.Service.Name) {
					w.logger.WithFields(logrus.Fields{
						"ingress":   ingress.Name,
						"namespace": ingress.Namespace,
						"host":      rule.Host,
						"service":   path.Backend.Service.Name,
					}).Warn("Skipping path to disallowed service during bulk sync - potential security risk")
					continue
				}

				// USE INGRESS CONTROLLER SERVICE FOR TUNNEL TARGET
				// We override the actual service with the Ingress Controller service
				ingressRule.Paths = append(ingressRule.Paths, IngressPath{
					Path:        path.Path,
					PathType:    pathType,
					ServiceName: w.ingressService.Name, // Target: Ingress Controller Service
					ServicePort: w.ingressService.Port, // Target: Ingress Controller Port
				})

				totalRoutes++
			}
		}

		// Only add ingress if it has rules
		if len(rulesByHost) > 0 {
			rules := make([]IngressRule, 0, len(rulesByHost))
			for _, rule := range rulesByHost {
				rules = append(rules, *rule)

				// Populate route cache for local lookups
				for _, path := range rule.Paths {
					w.mu.Lock()
					w.routeCache[rule.Host] = &Route{
						Host:        rule.Host,
						Path:        path.Path,
						PathType:    path.PathType,
						Service:     path.ServiceName,           // Ingress Controller Service
						Namespace:   w.ingressService.Namespace, // Ingress Controller Namespace
						IngressName: ingress.Name,
						Port:        path.ServicePort, // Ingress Controller Port
						TLS:         rule.TLS,
						Annotations: ingress.Annotations,
					}
					w.mu.Unlock()
				}
			}

			ingressData = append(ingressData, IngressData{
				Namespace:   ingress.Namespace,
				IngressName: ingress.Name,
				Annotations: ingress.Annotations,
				Rules:       rules,
			})
		}
	}

	// Send bulk sync request to controller with retry logic
	if len(ingressData) > 0 {
		syncReq := SyncIngressesRequest{
			ClusterUUID:    w.clusterUUID,
			PublicEndpoint: w.publicEndpoint,
			RoutingMode:    w.routingMode,
			Ingresses:      ingressData,
		}

		// Retry configuration
		const (
			maxRetries = 3
			baseDelay  = 1 * time.Second
			maxDelay   = 10 * time.Second
		)

		for attempt := 0; attempt < maxRetries; attempt++ {
			if err := w.controllerClient.SyncIngresses(ctx, syncReq); err != nil {
				if attempt < maxRetries-1 {
					// Record retry metric
					routeSyncRetries.Inc()

					// Exponential backoff: 1s, 2s, 4s (with jitter)
					delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
					if delay > maxDelay {
						delay = maxDelay
					}

					// Add jitter (±25%)
					jitterRange := int64(delay) / 2
					jitter := time.Duration(rand.Int63n(jitterRange))
					delay = delay - delay/4 + jitter

					w.logger.WithFields(logrus.Fields{
						"attempt":     attempt + 1,
						"max_retries": maxRetries,
						"retry_in":    delay,
						"error":       err,
					}).Warn("Route sync failed, retrying with backoff")

					// Wait with context cancellation support
					select {
					case <-ctx.Done():
						return fmt.Errorf("route sync cancelled during retry: %w", ctx.Err())
					case <-time.After(delay):
						// Continue to next retry
					}
					continue
				}

				// All retries exhausted - record failure metric
				routeSyncFailures.Inc()
				w.logger.WithError(err).WithField("attempts", maxRetries).Error("Route sync failed after all retries")
				return fmt.Errorf("failed to sync routes after %d attempts: %w", maxRetries, err)
			}

			// Success - record success metric
			routeSyncSuccess.Inc()
			if attempt > 0 {
				w.logger.WithField("attempts", attempt+1).Info("Route sync succeeded after retry")
			}
			break
		}
	}

	w.logger.WithFields(logrus.Fields{
		"ingresses":    len(ingressData),
		"routes":       totalRoutes,
		"routing_mode": w.routingMode,
	}).Info("Successfully synced all ingresses")

	return nil
}

// syncBatchWithRetry syncs a batch of ingresses with exponential backoff retry logic
func (w *IngressWatcher) syncBatchWithRetry(ctx context.Context, req SyncIngressesRequest, batchNum int, logger *logrus.Entry) error {
	const (
		maxRetries = 3
		baseDelay  = 1 * time.Second
		maxDelay   = 10 * time.Second
	)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := w.controllerClient.SyncIngresses(ctx, req); err != nil {
			lastErr = err

			// Check if error is retryable
			if isNonRetryableError(err) {
				logger.WithError(err).Error("Non-retryable error, skipping batch")
				return err
			}

			if attempt < maxRetries-1 {
				delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
				if delay > maxDelay {
					delay = maxDelay
				}

				jitter := time.Duration(rand.Int63n(int64(delay) / 2))
				delay = delay - delay/4 + jitter

				logger.WithFields(logrus.Fields{
					"attempt":     attempt + 1,
					"max_retries": maxRetries,
					"retry_in":    delay,
					"error":       err,
				}).Warn("Batch sync failed, retrying")

				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}

			return fmt.Errorf("batch %d failed after %d attempts: %w", batchNum, maxRetries, lastErr)
		}

		return nil
	}

	return lastErr
}

// isNonRetryableError checks if an error is non-retryable (4xx client errors)
func isNonRetryableError(err error) bool {
	errStr := err.Error()
	// Check for HTTP status codes in error message
	return strings.Contains(errStr, "400 Bad Request") ||
		strings.Contains(errStr, "401 Unauthorized") ||
		strings.Contains(errStr, "403 Forbidden") ||
		strings.Contains(errStr, "404 Not Found")
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

// isPipeOpsManaged checks if an ingress is managed by PipeOps
func (w *IngressWatcher) isPipeOpsManaged(ingress *networkingv1.Ingress) bool {
	// Check for PipeOps management label
	if managed, exists := ingress.Labels["pipeops.io/managed"]; exists && managed == "true" {
		return true
	}

	// Also check annotation as fallback
	if managedBy, exists := ingress.Annotations["pipeops.io/managed-by"]; exists && managedBy == "pipeops" {
		return true
	}

	// If neither label nor annotation is present, it's not managed by PipeOps
	return false
}

// extractDeploymentInfo extracts deployment ID and name from ingress annotations
// Priority order:
//  1. Explicit annotations: pipeops.io/deployment-id, pipeops.io/deployment-name
//  2. Derived from: pipeops.io/owner + pipeops.io/environment
//  3. Fallback: Parse from hostname
func (w *IngressWatcher) extractDeploymentInfo(ingress *networkingv1.Ingress, hostname string) (deploymentID, deploymentName string) {
	annotations := ingress.Annotations

	// Priority 1: Check for explicit deployment annotations (future-proof)
	if id, exists := annotations["pipeops.io/deployment-id"]; exists && id != "" {
		deploymentID = id
	}
	if name, exists := annotations["pipeops.io/deployment-name"]; exists && name != "" {
		deploymentName = name
	}

	// Priority 2: Derive from existing annotations
	owner := annotations["pipeops.io/owner"]
	environment := annotations["pipeops.io/environment"]

	if deploymentName == "" && owner != "" {
		deploymentName = owner
	}

	if deploymentID == "" && owner != "" && environment != "" {
		// Construct deployment ID as "environment:owner"
		deploymentID = fmt.Sprintf("%s:%s", environment, owner)
	}

	// Priority 3: Fallback to hostname parsing (last resort)
	if deploymentName == "" && hostname != "" {
		// Extract first part of hostname (e.g., "healthy-spiders" from "healthy-spiders.antqube.io")
		parts := strings.Split(hostname, ".")
		if len(parts) > 0 && parts[0] != "" {
			deploymentName = parts[0]
			w.logger.WithFields(logrus.Fields{
				"hostname":        hostname,
				"deployment_name": deploymentName,
			}).Debug("Extracted deployment name from hostname (fallback)")
		}
	}

	return deploymentID, deploymentName
}

// DetectClusterType checks if the cluster is public or private
func DetectClusterType(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) (isPrivate bool, err error) {
	logger.Info("Detecting cluster connectivity type...")

	// List of potential ingress controller services to check
	candidates := []struct {
		Namespace string
		Name      string
	}{
		{"ingress-nginx", "ingress-nginx-controller"},
		{"kube-system", "traefik"},
		{"traefik", "traefik"},
		{"kube-system", "rke2-ingress-nginx-controller"},
	}

	var svc *corev1.Service
	found := false

	for _, c := range candidates {
		s, err := k8sClient.CoreV1().Services(c.Namespace).Get(ctx, c.Name, metav1.GetOptions{})
		if err == nil {
			svc = s
			found = true
			logger.WithFields(logrus.Fields{
				"service":   c.Name,
				"namespace": c.Namespace,
			}).Info("Found Ingress Controller service")
			break
		}
	}

	if !found {
		logger.Debug("No known ingress controller service found, assuming private cluster")
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
			if cloud.IsPrivateIP(externalIP) {
				logger.WithField("external_ip", externalIP).Info("LoadBalancer IP is private, treating as private cluster (tunnel mode)")
				return true, nil
			}
			logger.WithField("external_ip", externalIP).Info("Cluster has public LoadBalancer, using direct routing mode")
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
			updatedSvc, err := k8sClient.CoreV1().Services(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
			if err != nil {
				logger.WithError(err).Debug("Failed to check LoadBalancer status")
				continue
			}

			if len(updatedSvc.Status.LoadBalancer.Ingress) > 0 {
				externalIP := ""
				if updatedSvc.Status.LoadBalancer.Ingress[0].IP != "" {
					externalIP = updatedSvc.Status.LoadBalancer.Ingress[0].IP
				} else if updatedSvc.Status.LoadBalancer.Ingress[0].Hostname != "" {
					externalIP = updatedSvc.Status.LoadBalancer.Ingress[0].Hostname
				}

				if externalIP != "" {
					if cloud.IsPrivateIP(externalIP) {
						logger.WithField("external_ip", externalIP).Info("LoadBalancer IP is private, treating as private cluster (tunnel mode)")
						return true, nil
					}
					logger.WithField("external_ip", externalIP).Info("Cluster has public LoadBalancer, using direct routing mode")
					return false, nil
				}
			}
		}
	}
}

// DetectLoadBalancerEndpoint returns the LoadBalancer endpoint (IP:port) if available
func DetectLoadBalancerEndpoint(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) string {
	// List of potential ingress controller services to check
	candidates := []struct {
		Namespace string
		Name      string
	}{
		{"ingress-nginx", "ingress-nginx-controller"},
		{"kube-system", "traefik"},
		{"traefik", "traefik"},
		{"kube-system", "rke2-ingress-nginx-controller"},
	}

	var svc *corev1.Service
	for _, c := range candidates {
		s, err := k8sClient.CoreV1().Services(c.Namespace).Get(ctx, c.Name, metav1.GetOptions{})
		if err == nil {
			svc = s
			break
		}
	}

	if svc == nil || svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return ""
	}

	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		ingress := svc.Status.LoadBalancer.Ingress[0]
		endpoint := ""

		if ingress.IP != "" {
			endpoint = ingress.IP
		} else if ingress.Hostname != "" {
			endpoint = ingress.Hostname
		}

		if endpoint != "" {
			// Prefer HTTPS port if available, fallback to HTTP
			for _, port := range svc.Spec.Ports {
				if port.Name == "https" || port.Name == "websecure" || port.Port == 443 {
					endpoint = fmt.Sprintf("%s:443", endpoint)
					logger.WithFields(logrus.Fields{
						"endpoint": endpoint,
						"port":     "https",
					}).Info("Detected LoadBalancer endpoint for direct routing")
					return endpoint
				}
			}

			// Fallback to HTTP port
			for _, port := range svc.Spec.Ports {
				if port.Name == "http" || port.Name == "web" || port.Port == 80 {
					endpoint = fmt.Sprintf("%s:80", endpoint)
					logger.WithFields(logrus.Fields{
						"endpoint": endpoint,
						"port":     "http",
					}).Info("Detected LoadBalancer endpoint for direct routing")
					return endpoint
				}
			}

			// If no standard ports found, use first port
			if len(svc.Spec.Ports) > 0 {
				endpoint = fmt.Sprintf("%s:%d", endpoint, svc.Spec.Ports[0].Port)
				logger.WithFields(logrus.Fields{
					"endpoint": endpoint,
					"port":     svc.Spec.Ports[0].Port,
				}).Warn("No standard HTTP/HTTPS port found, using first available port")
				return endpoint
			}
		}
	}

	return ""
}

// isServiceAllowed validates that a service is safe to expose via ingress
// This prevents malicious ingresses from exposing cluster infrastructure services
func (w *IngressWatcher) isServiceAllowed(namespace, serviceName string) bool {
	// Block access to Kubernetes API server service
	if namespace == "default" && serviceName == "kubernetes" {
		w.logger.WithFields(logrus.Fields{
			"namespace": namespace,
			"service":   serviceName,
		}).Warn("Blocked attempt to expose Kubernetes API service")
		return false
	}

	// Block access to system namespaces that shouldn't be exposed
	systemNamespaces := map[string]bool{
		"kube-system":     true,
		"kube-public":     true,
		"kube-node-lease": true,
	}

	if systemNamespaces[namespace] {
		w.logger.WithFields(logrus.Fields{
			"namespace": namespace,
			"service":   serviceName,
		}).Warn("Blocked attempt to expose service in system namespace")
		return false
	}

	// Verify the service actually exists in the specified namespace
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	service, err := w.k8sClient.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		w.logger.WithError(err).WithFields(logrus.Fields{
			"namespace": namespace,
			"service":   serviceName,
		}).Warn("Service not found, blocking route")
		return false
	}

	// Additional validation: check if service type is appropriate
	// Block direct exposure of LoadBalancer services (they should already be exposed)
	if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
		w.logger.WithFields(logrus.Fields{
			"namespace": namespace,
			"service":   serviceName,
			"type":      service.Spec.Type,
		}).Warn("Blocked attempt to expose LoadBalancer service via ingress")
		return false
	}

	// Block exposure of ExternalName services (potential SSRF vector)
	if service.Spec.Type == corev1.ServiceTypeExternalName {
		w.logger.WithFields(logrus.Fields{
			"namespace":    namespace,
			"service":      serviceName,
			"type":         service.Spec.Type,
			"externalName": service.Spec.ExternalName,
		}).Warn("Blocked attempt to expose ExternalName service via ingress")
		return false
	}

	// Service passed all validation checks
	return true
}

// LookupRoute finds a route by hostname in the local cache
// This is used as a fallback when the gateway doesn't send service routing info
func (w *IngressWatcher) LookupRoute(hostname string) *Route {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Exact match first
	if route, exists := w.routeCache[hostname]; exists {
		return route
	}

	// Try to match without port (e.g., "example.com:80" -> "example.com")
	hostWithoutPort := hostname
	if idx := strings.Index(hostname, ":"); idx > 0 {
		hostWithoutPort = hostname[:idx]
		if route, exists := w.routeCache[hostWithoutPort]; exists {
			return route
		}
	}

	// No match found
	return nil
}

// GetIngressControllerService returns the detected ingress controller service, if any.
// This allows the agent to route fallback proxy traffic through the ingress controller
// rather than directly to backend services.
func (w *IngressWatcher) GetIngressControllerService() *IngressService {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.ingressService == nil {
		return nil
	}

	svc := *w.ingressService
	return &svc
}
