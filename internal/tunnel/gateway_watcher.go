package tunnel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
	gatewayinformers "sigs.k8s.io/gateway-api/pkg/client/informers/externalversions"
)

// GatewayWatcher watches Gateway API resources and registers discovered TCP/UDP tunnels
type GatewayWatcher struct {
	// Kubernetes clients
	k8sClient     kubernetes.Interface
	gatewayClient gatewayclient.Interface

	// Configuration
	clusterUUID     string
	config          *WatcherConfig
	logger          *logrus.Logger
	cpClient        ControlPlaneClient
	forceTunnelMode bool
	dualModeEnabled bool

	// Informers and queue
	gatewayInformer cache.SharedIndexInformer
	workqueue       workqueue.RateLimitingInterface

	// Tunnel cache (tunnelID -> TunnelService)
	tunnels   map[string]*TunnelService
	tunnelsMu sync.RWMutex

	// Cluster type cache
	clusterType   ClusterType
	clusterTypeMu sync.RWMutex

	// Shutdown
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// WatcherConfig contains configuration for the gateway watcher
type WatcherConfig struct {
	// Gateway label selector (e.g., "pipeops.io/managed=true")
	GatewayLabel string

	// Sync interval for periodic reconciliation
	SyncInterval time.Duration

	// Cluster type detection timeout
	ClusterTypeDetectionTimeout time.Duration
}

// DefaultWatcherConfig returns the default watcher configuration
func DefaultWatcherConfig() *WatcherConfig {
	return &WatcherConfig{
		GatewayLabel:                "pipeops.io/managed",
		SyncInterval:                5 * time.Minute,
		ClusterTypeDetectionTimeout: 2 * time.Minute,
	}
}

// NewGatewayWatcher creates a new gateway watcher instance
func NewGatewayWatcher(
	k8sClient kubernetes.Interface,
	gatewayClient gatewayclient.Interface,
	clusterUUID string,
	config *WatcherConfig,
	logger *logrus.Logger,
	cpClient ControlPlaneClient,
	forceTunnelMode bool,
	dualModeEnabled bool,
) *GatewayWatcher {
	if config == nil {
		config = DefaultWatcherConfig()
	}

	return &GatewayWatcher{
		k8sClient:       k8sClient,
		gatewayClient:   gatewayClient,
		clusterUUID:     clusterUUID,
		config:          config,
		logger:          logger,
		cpClient:        cpClient,
		forceTunnelMode: forceTunnelMode,
		dualModeEnabled: dualModeEnabled,
		tunnels:         make(map[string]*TunnelService),
		clusterType:     ClusterTypeUnknown,
		workqueue:       workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		stopCh:          make(chan struct{}),
	}
}

// Start begins watching Gateway API resources
func (w *GatewayWatcher) Start(ctx context.Context) error {
	w.logger.Info("Starting Gateway API watcher for TCP/UDP tunnels")

	// Detect cluster type (public vs private)
	go w.detectClusterType(ctx)

	// Create Gateway informer with label selector
	gatewayInformerFactory := gatewayinformers.NewSharedInformerFactoryWithOptions(
		w.gatewayClient,
		w.config.SyncInterval,
		gatewayinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = w.config.GatewayLabel
		}),
	)

	w.gatewayInformer = gatewayInformerFactory.Gateway().V1().Gateways().Informer()

	// Register event handlers
	w.gatewayInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				w.logger.WithField("gateway", key).Debug("Gateway added")
				w.workqueue.Add(key)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err == nil {
				w.logger.WithField("gateway", key).Debug("Gateway updated")
				w.workqueue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				w.logger.WithField("gateway", key).Debug("Gateway deleted")
				w.workqueue.Add(key)
			}
		},
	})

	// Start informer
	go w.gatewayInformer.Run(w.stopCh)

	// Wait for cache sync
	w.logger.Info("Waiting for Gateway informer cache to sync")
	if !cache.WaitForCacheSync(w.stopCh, w.gatewayInformer.HasSynced) {
		return fmt.Errorf("failed to sync Gateway informer cache")
	}

	w.logger.Info("Gateway informer cache synced successfully")

	// Start worker to process queue
	w.wg.Add(1)
	go w.runWorker()

	// Start periodic sync
	w.wg.Add(1)
	go w.periodicSync(ctx)

	return nil
}

// Stop gracefully shuts down the watcher
func (w *GatewayWatcher) Stop() {
	w.logger.Info("Stopping Gateway watcher")
	close(w.stopCh)
	w.workqueue.ShutDown()
	w.wg.Wait()
	w.logger.Info("Gateway watcher stopped")
}

// runWorker processes items from the work queue
func (w *GatewayWatcher) runWorker() {
	defer w.wg.Done()

	for w.processNextWorkItem() {
	}
}

// processNextWorkItem processes a single item from the queue
func (w *GatewayWatcher) processNextWorkItem() bool {
	obj, shutdown := w.workqueue.Get()
	if shutdown {
		return false
	}

	defer w.workqueue.Done(obj)

	key, ok := obj.(string)
	if !ok {
		w.workqueue.Forget(obj)
		return true
	}

	if err := w.syncGateway(key); err != nil {
		w.logger.WithError(err).WithField("gateway", key).Error("Failed to sync gateway")
		w.workqueue.AddRateLimited(key)
		return true
	}

	w.workqueue.Forget(obj)
	return true
}

// syncGateway processes a single Gateway resource
func (w *GatewayWatcher) syncGateway(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return fmt.Errorf("invalid key: %s", key)
	}

	// Get Gateway from cache
	obj, exists, err := w.gatewayInformer.GetStore().GetByKey(key)
	if err != nil {
		return fmt.Errorf("error fetching Gateway %s: %w", key, err)
	}

	if !exists {
		// Gateway deleted - deregister tunnels
		w.logger.WithFields(logrus.Fields{
			"namespace": namespace,
			"name":      name,
		}).Info("Gateway deleted, deregistering tunnels")

		return w.deregisterGatewayTunnels(namespace, name)
	}

	gateway, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return fmt.Errorf("unexpected object type: %T", obj)
	}

	// Process Gateway and register tunnels
	return w.processGateway(gateway)
}

// processGateway discovers TCP/UDP listeners and registers tunnels
func (w *GatewayWatcher) processGateway(gateway *gatewayv1.Gateway) error {
	w.logger.WithFields(logrus.Fields{
		"namespace": gateway.Namespace,
		"name":      gateway.Name,
	}).Debug("Processing Gateway")

	// Extract public IP (if available)
	publicIP := w.getGatewayPublicIP(gateway)

	// Determine routing mode for this gateway
	routingMode := w.determineRoutingMode(gateway, publicIP)

	w.logger.WithFields(logrus.Fields{
		"gateway":      fmt.Sprintf("%s/%s", gateway.Namespace, gateway.Name),
		"routing_mode": routingMode,
		"public_ip":    publicIP,
	}).Info("Determined routing mode for gateway")

	// Process each listener
	for _, listener := range gateway.Spec.Listeners {
		protocol := w.getProtocolFromListener(listener)
		if protocol == "" {
			// Not TCP or UDP, skip
			continue
		}

		// Build tunnel service
		tunnelService := &TunnelService{
			Protocol:         protocol,
			GatewayName:      gateway.Name,
			GatewayNamespace: gateway.Namespace,
			GatewayPort:      int(listener.Port),
			RoutingMode:      routingMode,
			Labels:           gateway.Labels,
			Annotations:      gateway.Annotations,
		}

		// Build tunnel ID
		tunnelService.TunnelID = fmt.Sprintf("%s-%s-%s-%d",
			protocol,
			w.clusterUUID,
			gateway.Name,
			listener.Port,
		)

		// Set public endpoint if available
		if publicIP != "" {
			tunnelService.PublicEndpoint = fmt.Sprintf("%s:%d", publicIP, listener.Port)
		}

		// Register tunnel with control plane
		if err := w.registerTunnel(tunnelService); err != nil {
			w.logger.WithError(err).WithFields(logrus.Fields{
				"tunnel_id": tunnelService.TunnelID,
				"gateway":   fmt.Sprintf("%s/%s", gateway.Namespace, gateway.Name),
			}).Error("Failed to register tunnel")
			continue
		}

		// Cache tunnel
		w.tunnelsMu.Lock()
		w.tunnels[tunnelService.TunnelID] = tunnelService
		w.tunnelsMu.Unlock()
	}

	return nil
}

// getProtocolFromListener extracts the protocol from a Gateway listener
func (w *GatewayWatcher) getProtocolFromListener(listener gatewayv1.Listener) Protocol {
	switch listener.Protocol {
	case gatewayv1.TCPProtocolType:
		return ProtocolTCP
	case gatewayv1.UDPProtocolType:
		return ProtocolUDP
	default:
		return ""
	}
}

// getGatewayPublicIP extracts the public IP from Gateway status
func (w *GatewayWatcher) getGatewayPublicIP(gateway *gatewayv1.Gateway) string {
	for _, addr := range gateway.Status.Addresses {
		if addr.Type != nil && *addr.Type == gatewayv1.IPAddressType {
			return addr.Value
		}
	}
	return ""
}

// determineRoutingMode determines the routing mode for a gateway
func (w *GatewayWatcher) determineRoutingMode(gateway *gatewayv1.Gateway, publicIP string) RoutingMode {
	// Check for gateway-specific override annotation
	if mode, exists := gateway.Annotations["pipeops.io/routing-mode"]; exists {
		switch mode {
		case "direct":
			return RoutingModeDirect
		case "tunnel":
			return RoutingModeTunnel
		case "dual":
			return RoutingModeDual
		}
	}

	// Check force tunnel mode (global config)
	if w.forceTunnelMode {
		return RoutingModeTunnel
	}

	// Auto-detect based on cluster type
	w.clusterTypeMu.RLock()
	clusterType := w.clusterType
	w.clusterTypeMu.RUnlock()

	if clusterType == ClusterTypePrivate || publicIP == "" {
		// Private cluster or no public IP - tunnel only
		return RoutingModeTunnel
	}

	// Public cluster with dual mode enabled
	if w.dualModeEnabled {
		return RoutingModeDual
	}

	// Public cluster, direct access only
	return RoutingModeDirect
}

// registerTunnel registers a tunnel with the control plane
func (w *GatewayWatcher) registerTunnel(tunnel *TunnelService) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &TunnelRegistration{
		ClusterUUID:      w.clusterUUID,
		Protocol:         tunnel.Protocol,
		TunnelID:         tunnel.TunnelID,
		GatewayName:      tunnel.GatewayName,
		GatewayNamespace: tunnel.GatewayNamespace,
		GatewayPort:      tunnel.GatewayPort,
		ServiceName:      tunnel.ServiceName,
		ServiceNamespace: tunnel.ServiceNamespace,
		ServicePort:      tunnel.ServicePort,
		RoutingMode:      tunnel.RoutingMode,
		PublicEndpoint:   tunnel.PublicEndpoint,
		Labels:           tunnel.Labels,
		Annotations:      tunnel.Annotations,
	}

	resp, err := w.cpClient.RegisterTunnel(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to register tunnel: %w", err)
	}

	// Update tunnel with response data
	tunnel.TunnelPort = resp.TunnelPort
	tunnel.TunnelEndpoint = resp.TunnelEndpoint
	tunnel.RegisteredAt = time.Now()
	tunnel.Status = TunnelStatusActive

	w.logger.WithFields(logrus.Fields{
		"tunnel_id":       tunnel.TunnelID,
		"protocol":        tunnel.Protocol,
		"gateway_port":    tunnel.GatewayPort,
		"tunnel_port":     tunnel.TunnelPort,
		"tunnel_endpoint": tunnel.TunnelEndpoint,
		"routing_mode":    tunnel.RoutingMode,
		"public_endpoint": tunnel.PublicEndpoint,
	}).Info("Tunnel registered successfully")

	return nil
}

// deregisterGatewayTunnels removes all tunnels for a deleted Gateway
func (w *GatewayWatcher) deregisterGatewayTunnels(namespace, name string) error {
	w.tunnelsMu.Lock()
	defer w.tunnelsMu.Unlock()

	for tunnelID, tunnel := range w.tunnels {
		if tunnel.GatewayNamespace == namespace && tunnel.GatewayName == name {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := w.cpClient.DeregisterTunnel(ctx, w.clusterUUID, tunnelID)
			cancel()

			if err != nil {
				w.logger.WithError(err).WithField("tunnel_id", tunnelID).Error("Failed to deregister tunnel")
			} else {
				w.logger.WithField("tunnel_id", tunnelID).Info("Tunnel deregistered")
			}

			delete(w.tunnels, tunnelID)
		}
	}

	return nil
}

// detectClusterType determines if the cluster is public or private
func (w *GatewayWatcher) detectClusterType(ctx context.Context) {
	w.logger.Info("Detecting cluster type (public vs private)")

	// Wait for initial sync
	time.Sleep(5 * time.Second)

	// Check for any LoadBalancer services with external IPs
	timeout := time.After(w.config.ClusterTypeDetectionTimeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			w.logger.Info("Cluster type detection timeout - assuming private cluster")
			w.clusterTypeMu.Lock()
			w.clusterType = ClusterTypePrivate
			w.clusterTypeMu.Unlock()
			return

		case <-ticker.C:
			services, err := w.k8sClient.CoreV1().Services("").List(ctx, metav1.ListOptions{})
			if err != nil {
				w.logger.WithError(err).Warn("Failed to list services for cluster type detection")
				continue
			}

			for _, svc := range services.Items {
				if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
					if len(svc.Status.LoadBalancer.Ingress) > 0 {
						ingress := svc.Status.LoadBalancer.Ingress[0]
						if ingress.IP != "" || ingress.Hostname != "" {
							w.logger.WithField("service", fmt.Sprintf("%s/%s", svc.Namespace, svc.Name)).Info("Found LoadBalancer with external IP - cluster is public")
							w.clusterTypeMu.Lock()
							w.clusterType = ClusterTypePublic
							w.clusterTypeMu.Unlock()
							return
						}
					}
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

// periodicSync performs periodic reconciliation of all tunnels
func (w *GatewayWatcher) periodicSync(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.config.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.logger.Debug("Running periodic tunnel sync")
			w.syncAllTunnels()

		case <-ctx.Done():
			return

		case <-w.stopCh:
			return
		}
	}
}

// syncAllTunnels performs bulk sync of all registered tunnels
func (w *GatewayWatcher) syncAllTunnels() {
	w.tunnelsMu.RLock()
	tunnels := make([]TunnelRegistration, 0, len(w.tunnels))
	for _, tunnel := range w.tunnels {
		tunnels = append(tunnels, TunnelRegistration{
			ClusterUUID:      w.clusterUUID,
			Protocol:         tunnel.Protocol,
			TunnelID:         tunnel.TunnelID,
			GatewayName:      tunnel.GatewayName,
			GatewayNamespace: tunnel.GatewayNamespace,
			GatewayPort:      tunnel.GatewayPort,
			RoutingMode:      tunnel.RoutingMode,
			PublicEndpoint:   tunnel.PublicEndpoint,
		})
	}
	w.tunnelsMu.RUnlock()

	if len(tunnels) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := w.cpClient.SyncTunnels(ctx, &TunnelSyncRequest{
		ClusterUUID: w.clusterUUID,
		Tunnels:     tunnels,
	})

	if err != nil {
		w.logger.WithError(err).Error("Failed to sync tunnels")
		return
	}

	w.logger.WithFields(logrus.Fields{
		"accepted": len(resp.Accepted),
		"rejected": len(resp.Rejected),
	}).Info("Tunnel sync completed")
}

// GetTunnels returns all currently registered tunnels
func (w *GatewayWatcher) GetTunnels() map[string]*TunnelService {
	w.tunnelsMu.RLock()
	defer w.tunnelsMu.RUnlock()

	result := make(map[string]*TunnelService, len(w.tunnels))
	for k, v := range w.tunnels {
		result[k] = v
	}

	return result
}
