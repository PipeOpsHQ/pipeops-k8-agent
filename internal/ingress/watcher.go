package ingress

import (
	"context"
	"fmt"
	"strings"
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
func NewIngressWatcher(k8sClient kubernetes.Interface, clusterUUID string, controllerClient RouteClient, logger *logrus.Logger, publicEndpoint, routingMode string) *IngressWatcher {
	return &IngressWatcher{
		k8sClient:        k8sClient,
		clusterUUID:      clusterUUID,
		logger:           logger,
		controllerClient: controllerClient,
		publicEndpoint:   publicEndpoint,
		routingMode:      routingMode,
		stopCh:           make(chan struct{}),
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
		// Register each route individually
		for _, route := range routes {
			// Extract deployment information from ingress annotations
			deploymentID, deploymentName := w.extractDeploymentInfo(ingress, route.Host)

			req := RegisterRouteRequest{
				Hostname:       route.Host,
				ClusterUUID:    w.clusterUUID,
				Namespace:      route.Namespace,
				ServiceName:    route.Service,
				ServicePort:    route.Port,
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

				ingressRule.Paths = append(ingressRule.Paths, IngressPath{
					Path:        path.Path,
					PathType:    pathType,
					ServiceName: path.Backend.Service.Name,
					ServicePort: port,
				})

				totalRoutes++
			}
		}

		// Only add ingress if it has rules
		if len(rulesByHost) > 0 {
			rules := make([]IngressRule, 0, len(rulesByHost))
			for _, rule := range rulesByHost {
				rules = append(rules, *rule)
			}

			ingressData = append(ingressData, IngressData{
				Namespace:   ingress.Namespace,
				IngressName: ingress.Name,
				Annotations: ingress.Annotations,
				Rules:       rules,
			})
		}
	}

	// Send bulk sync request to controller
	if len(ingressData) > 0 {
		syncReq := SyncIngressesRequest{
			ClusterUUID:    w.clusterUUID,
			PublicEndpoint: w.publicEndpoint,
			RoutingMode:    w.routingMode,
			Ingresses:      ingressData,
		}

		if err := w.controllerClient.SyncIngresses(ctx, syncReq); err != nil {
			return fmt.Errorf("failed to bulk sync ingresses: %w", err)
		}
	}

	w.logger.WithFields(logrus.Fields{
		"ingresses":    len(ingressData),
		"routes":       totalRoutes,
		"routing_mode": w.routingMode,
	}).Info("Finished syncing existing ingresses to controller")

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
					logger.WithField("external_ip", externalIP).Info("Cluster has public LoadBalancer, using direct routing mode")
					return false, nil
				}
			}
		}
	}
}

// DetectLoadBalancerEndpoint returns the LoadBalancer endpoint (IP:port) if available
func DetectLoadBalancerEndpoint(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) string {
	svc, err := k8sClient.CoreV1().Services("ingress-nginx").
		Get(ctx, "ingress-nginx-controller", metav1.GetOptions{})

	if err != nil || svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
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
				if port.Name == "https" || port.Port == 443 {
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
				if port.Name == "http" || port.Port == 80 {
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
