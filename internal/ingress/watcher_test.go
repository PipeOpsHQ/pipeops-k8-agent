package ingress

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// mockControllerClient is a mock implementation of ControllerClient for testing
type mockControllerClient struct {
	registerCalls   []RegisterRouteRequest
	unregisterCalls []string
	syncCalls       []SyncIngressesRequest
}

func (m *mockControllerClient) RegisterRoute(ctx context.Context, req RegisterRouteRequest) error {
	m.registerCalls = append(m.registerCalls, req)
	return nil
}

func (m *mockControllerClient) UnregisterRoute(ctx context.Context, hostname string) error {
	m.unregisterCalls = append(m.unregisterCalls, hostname)
	return nil
}

func (m *mockControllerClient) SyncIngresses(ctx context.Context, req SyncIngressesRequest) error {
	m.syncCalls = append(m.syncCalls, req)
	return nil
}

func TestIngressWatcher_ExtractRoutes(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create the service that the ingress will reference
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(svc)
	mockClient := &mockControllerClient{}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	pathTypePrefix := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/rewrite-target": "/",
			},
		},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{
				{
					Hosts: []string{"example.com"},
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/api",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "api-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

	routes := watcher.extractRoutes(ingress)

	assert.Len(t, routes, 1)
	assert.Equal(t, "example.com", routes[0].Host)
	assert.Equal(t, "/api", routes[0].Path)
	assert.Equal(t, "Prefix", routes[0].PathType)
	assert.Equal(t, "api-service", routes[0].Service)
	assert.Equal(t, "default", routes[0].Namespace)
	assert.Equal(t, "test-ingress", routes[0].IngressName)
	assert.Equal(t, int32(8080), routes[0].Port)
	assert.True(t, routes[0].TLS)
}

func TestDetectClusterType_Private(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	fakeClient := fake.NewSimpleClientset()

	// No ingress service - should be detected as private
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	isPrivate, err := DetectClusterType(ctx, fakeClient, logger)

	assert.NoError(t, err)
	assert.True(t, isPrivate)
}

func TestDetectClusterType_PublicWithExternalIP(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create LoadBalancer service with external IP
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-nginx-controller",
			Namespace: "ingress-nginx",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{
					{
						IP: "1.2.3.4",
					},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(svc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	isPrivate, err := DetectClusterType(ctx, fakeClient, logger)

	assert.NoError(t, err)
	assert.False(t, isPrivate)
}

func TestDetectClusterType_PrivateNodePort(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create NodePort service (not LoadBalancer)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-nginx-controller",
			Namespace: "ingress-nginx",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
		},
	}

	fakeClient := fake.NewSimpleClientset(svc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	isPrivate, err := DetectClusterType(ctx, fakeClient, logger)

	assert.NoError(t, err)
	assert.True(t, isPrivate)
}

func TestExtractDeploymentInfo(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	tests := []struct {
		name               string
		ingress            *networkingv1.Ingress
		hostname           string
		expectedID         string
		expectedName       string
		expectedLogMessage string
	}{
		{
			name: "Priority 1: Explicit annotations",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"pipeops.io/deployment-id":   "proj_abc123",
						"pipeops.io/deployment-name": "healthy-spiders",
					},
				},
			},
			hostname:     "healthy-spiders.antqube.io",
			expectedID:   "proj_abc123",
			expectedName: "healthy-spiders",
		},
		{
			name: "Priority 2: Owner + Environment",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"pipeops.io/owner":       "healthy-spiders",
						"pipeops.io/environment": "mana",
					},
				},
			},
			hostname:     "healthy-spiders.antqube.io",
			expectedID:   "mana:healthy-spiders",
			expectedName: "healthy-spiders",
		},
		{
			name: "Priority 2: Owner only (no environment)",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"pipeops.io/owner": "meaty-hope",
					},
				},
			},
			hostname:     "meaty-hope.antqube.io",
			expectedID:   "",
			expectedName: "meaty-hope",
		},
		{
			name: "Priority 3: Hostname fallback",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			hostname:     "acidic-partner.antqube.io",
			expectedID:   "",
			expectedName: "acidic-partner",
		},
		{
			name: "Mixed: Explicit ID + Owner for name",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"pipeops.io/deployment-id": "proj_xyz789",
						"pipeops.io/owner":         "grafana",
						"pipeops.io/environment":   "monitoring",
					},
				},
			},
			hostname:     "grafana.local",
			expectedID:   "proj_xyz789",
			expectedName: "grafana",
		},
		{
			name: "Production example",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"pipeops.io/owner":       "rambunctious-bath",
						"pipeops.io/environment": "production",
					},
				},
			},
			hostname:     "rambunctious-bath.antqube.io",
			expectedID:   "production:rambunctious-bath",
			expectedName: "rambunctious-bath",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, name := watcher.extractDeploymentInfo(tt.ingress, tt.hostname)
			assert.Equal(t, tt.expectedID, id, "deployment_id mismatch")
			assert.Equal(t, tt.expectedName, name, "deployment_name mismatch")
		})
	}
}

func TestIsPipeOpsManaged(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	tests := []struct {
		name     string
		ingress  *networkingv1.Ingress
		expected bool
	}{
		{
			name: "PipeOps managed with label",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Labels: map[string]string{
						"pipeops.io/managed": "true",
					},
				},
			},
			expected: true,
		},
		{
			name: "PipeOps managed with annotation",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Annotations: map[string]string{
						"pipeops.io/managed-by": "pipeops",
					},
				},
			},
			expected: true,
		},
		{
			name: "PipeOps managed with both",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Labels: map[string]string{
						"pipeops.io/managed": "true",
					},
					Annotations: map[string]string{
						"pipeops.io/managed-by": "pipeops",
					},
				},
			},
			expected: true,
		},
		{
			name: "Not PipeOps managed",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Labels: map[string]string{
						"app": "nginx",
					},
				},
			},
			expected: false,
		},
		{
			name: "PipeOps label false",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ingress",
					Namespace: "default",
					Labels: map[string]string{
						"pipeops.io/managed": "false",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := watcher.isPipeOpsManaged(tt.ingress)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsServiceAllowed(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create test services
	allowedService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "allowed-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	kubernetesService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Port: 443},
			},
		},
	}

	loadBalancerService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lb-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Ports: []corev1.ServicePort{
				{Port: 80},
			},
		},
	}

	externalNameService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "external-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "evil.example.com",
		},
	}

	systemService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-dns",
			Namespace: "kube-system",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Port: 53},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(
		allowedService,
		kubernetesService,
		loadBalancerService,
		externalNameService,
		systemService,
	)
	mockClient := &mockControllerClient{}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	tests := []struct {
		name        string
		namespace   string
		serviceName string
		expected    bool
		description string
	}{
		{
			name:        "Allow normal ClusterIP service",
			namespace:   "default",
			serviceName: "allowed-service",
			expected:    true,
			description: "Normal user services should be allowed",
		},
		{
			name:        "Block Kubernetes API service",
			namespace:   "default",
			serviceName: "kubernetes",
			expected:    false,
			description: "Should block access to Kubernetes API",
		},
		{
			name:        "Block LoadBalancer service",
			namespace:   "default",
			serviceName: "lb-service",
			expected:    false,
			description: "LoadBalancer services should not be exposed via ingress",
		},
		{
			name:        "Block ExternalName service",
			namespace:   "default",
			serviceName: "external-service",
			expected:    false,
			description: "ExternalName services are SSRF vectors",
		},
		{
			name:        "Block kube-system namespace",
			namespace:   "kube-system",
			serviceName: "kube-dns",
			expected:    false,
			description: "System namespaces should be blocked",
		},
		{
			name:        "Block non-existent service",
			namespace:   "default",
			serviceName: "non-existent",
			expected:    false,
			description: "Non-existent services should be blocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := watcher.isServiceAllowed(tt.namespace, tt.serviceName)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

// mockControllerClientWithRetries is a mock that can simulate retries
type mockControllerClientWithRetries struct {
	registerCalls   []RegisterRouteRequest
	unregisterCalls []string
	syncCalls       []SyncIngressesRequest
	syncError       error
	syncCallCount   int
	maxFailures     int // Number of times to fail before succeeding
}

func (m *mockControllerClientWithRetries) RegisterRoute(ctx context.Context, req RegisterRouteRequest) error {
	m.registerCalls = append(m.registerCalls, req)
	return nil
}

func (m *mockControllerClientWithRetries) UnregisterRoute(ctx context.Context, hostname string) error {
	m.unregisterCalls = append(m.unregisterCalls, hostname)
	return nil
}

func (m *mockControllerClientWithRetries) SyncIngresses(ctx context.Context, req SyncIngressesRequest) error {
	m.syncCalls = append(m.syncCalls, req)
	m.syncCallCount++

	// Fail for the first maxFailures attempts, then succeed
	if m.syncCallCount <= m.maxFailures {
		return m.syncError
	}

	return nil
}

func TestSyncExistingIngresses_RetryLogic(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create a PipeOps-managed ingress
	pathTypePrefix := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
			Labels: map[string]string{
				"pipeops.io/managed": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "test-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

	// Create the service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	// fakeClient declaration removed as it is created inside the loop

	tests := []struct {
		name           string
		maxFailures    int
		syncError      error
		expectSuccess  bool
		expectAttempts int
	}{
		{
			name:           "Success on first attempt",
			maxFailures:    0,
			syncError:      nil,
			expectSuccess:  true,
			expectAttempts: 1,
		},
		{
			name:           "Success on second attempt after one failure",
			maxFailures:    1,
			syncError:      fmt.Errorf("temporary network error"),
			expectSuccess:  true,
			expectAttempts: 2,
		},
		{
			name:           "Success on third attempt after two failures",
			maxFailures:    2,
			syncError:      fmt.Errorf("503 Service Unavailable"),
			expectSuccess:  true,
			expectAttempts: 3,
		},
		{
			name:           "Fail after all retries exhausted",
			maxFailures:    3,
			syncError:      fmt.Errorf("persistent error"),
			expectSuccess:  false,
			expectAttempts: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockControllerClientWithRetries{
				maxFailures: tt.maxFailures,
				syncError:   tt.syncError,
			}

			// Add Ingress Controller service to fake client
			ingressControllerSvc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ingress-nginx-controller",
					Namespace: "ingress-nginx",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "http",
							Port: 80,
						},
					},
				},
			}

			// Re-create client with ingress controller service
			fakeClient := fake.NewSimpleClientset(ingress, svc, ingressControllerSvc)

			watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

			ctx := context.Background()
			err := watcher.syncExistingIngresses(ctx)

			if tt.expectSuccess {
				assert.NoError(t, err, "Expected sync to succeed")
			} else {
				assert.Error(t, err, "Expected sync to fail")
				assert.Contains(t, err.Error(), "failed to sync routes after")
			}

			assert.Equal(t, tt.expectAttempts, mockClient.syncCallCount, "Expected number of sync attempts")
		})
	}
}

func TestSyncExistingIngresses_ContextCancellation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create a PipeOps-managed ingress
	pathTypePrefix := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
			Labels: map[string]string{
				"pipeops.io/managed": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "test-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

	// Create the service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	// Add Ingress Controller service
	ingressControllerSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-nginx-controller",
			Namespace: "ingress-nginx",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 80,
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(ingress, svc, ingressControllerSvc)
	mockClient := &mockControllerClientWithRetries{
		maxFailures: 3,
		syncError:   fmt.Errorf("always fail"),
	}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	// Create a context that will be cancelled after a short delay
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context after first retry attempt
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := watcher.syncExistingIngresses(ctx)

	// Should fail due to context cancellation
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "route sync cancelled during retry")

	// Should have attempted at least once but not all 3 retries
	assert.True(t, mockClient.syncCallCount >= 1 && mockClient.syncCallCount < 3,
		"Expected sync to be cancelled mid-retry")
}

func TestSyncExistingIngresses_BackoffTiming(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create a PipeOps-managed ingress
	pathTypePrefix := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
			Labels: map[string]string{
				"pipeops.io/managed": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "test-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

	// Create the service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{Port: 8080},
			},
		},
	}

	// Add Ingress Controller service
	ingressControllerSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-nginx-controller",
			Namespace: "ingress-nginx",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 80,
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(ingress, svc, ingressControllerSvc)
	mockClient := &mockControllerClientWithRetries{
		maxFailures: 3,
		syncError:   fmt.Errorf("always fail"),
	}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	ctx := context.Background()
	start := time.Now()

	// We expect this to fail after retries
	err := watcher.syncExistingIngresses(ctx)

	duration := time.Since(start)

	assert.Error(t, err)

	// Base delay is 1s, exponential backoff: 1s, 2s. Total wait should be around 3s + jitter.
	// We just verify it didn't return immediately
	assert.True(t, duration > 1*time.Second, "Expected backoff delay to be applied")
}

func TestOnIngressEvent_RegistersIngressControllerService(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create the service that the ingress will reference
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
				},
			},
		},
	}

	// Create Ingress Controller service
	ingressControllerSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-nginx-controller",
			Namespace: "ingress-nginx",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 80,
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(svc, ingressControllerSvc)
	mockClient := &mockControllerClient{}

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	pathTypePrefix := networkingv1.PathTypePrefix

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
			Labels: map[string]string{
				"pipeops.io/managed": "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/api",
									PathType: &pathTypePrefix,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "api-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
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

	// Trigger add event
	watcher.onIngressEvent(ingress, "add")

	// Verify that RegisterRoute was called with Ingress Controller service details
	assert.Len(t, mockClient.registerCalls, 1)
	req := mockClient.registerCalls[0]

	assert.Equal(t, "example.com", req.Hostname)

	// IMPORTANT: Should match Ingress Controller service, NOT the backend service
	assert.Equal(t, "ingress-nginx-controller", req.ServiceName)
	assert.Equal(t, "ingress-nginx", req.Namespace)
	assert.Equal(t, int32(80), req.ServicePort)
}

func TestIngressWatcher_GetIngressControllerService(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	// Not yet detected
	assert.Nil(t, watcher.GetIngressControllerService())

	// Set ingress controller service and verify copy is returned
	watcher.ingressService = &IngressService{
		Name:      "ingress-nginx-controller",
		Namespace: "ingress-nginx",
		Port:      80,
	}

	svc := watcher.GetIngressControllerService()
	assert.NotNil(t, svc)
	if svc == nil {
		t.Fatal("expected ingress controller service")
	}

	assert.Equal(t, "ingress-nginx-controller", svc.Name)
	assert.Equal(t, "ingress-nginx", svc.Namespace)
	assert.Equal(t, int32(80), svc.Port)

	// Ensure it's a copy
	svc.Name = "changed"
	assert.Equal(t, "ingress-nginx-controller", watcher.ingressService.Name)
}

func TestIngressWatcher_SwitchExistingIngressesToClass(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	nginxClass := "nginx"
	traefikClass := "traefik"

	ingressWithNginx := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "nginx-ingress",
			Namespace:   "default",
			Annotations: map[string]string{"kubernetes.io/ingress.class": "nginx"},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &nginxClass,
		},
	}

	ingressWithoutClass := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-class-ingress",
			Namespace: "beta",
		},
	}

	alreadyTraefik := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "traefik-ingress",
			Namespace:   "pipeops-monitoring",
			Annotations: map[string]string{"kubernetes.io/ingress.class": "traefik"},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &traefikClass,
		},
	}

	fakeClient := fake.NewSimpleClientset(ingressWithNginx, ingressWithoutClass, alreadyTraefik)
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	err := watcher.switchExistingIngressesToClass(context.Background(), "traefik")
	assert.NoError(t, err)

	updatedNginx, err := fakeClient.NetworkingV1().Ingresses("default").Get(context.Background(), "nginx-ingress", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, updatedNginx.Spec.IngressClassName)
	assert.Equal(t, "traefik", *updatedNginx.Spec.IngressClassName)
	assert.Equal(t, "traefik", updatedNginx.Annotations["kubernetes.io/ingress.class"])

	updatedNoClass, err := fakeClient.NetworkingV1().Ingresses("beta").Get(context.Background(), "no-class-ingress", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, updatedNoClass.Spec.IngressClassName)
	assert.Equal(t, "traefik", *updatedNoClass.Spec.IngressClassName)
	assert.Equal(t, "traefik", updatedNoClass.Annotations["kubernetes.io/ingress.class"])

	updatedTraefik, err := fakeClient.NetworkingV1().Ingresses("pipeops-monitoring").Get(context.Background(), "traefik-ingress", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, updatedTraefik.Spec.IngressClassName)
	assert.Equal(t, "traefik", *updatedTraefik.Spec.IngressClassName)
	assert.Equal(t, "traefik", updatedTraefik.Annotations["kubernetes.io/ingress.class"])
}

func TestIngressWatcher_SwitchExistingIngressesToClass_EmptyTarget(t *testing.T) {
	logger := logrus.New()
	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	err := watcher.switchExistingIngressesToClass(context.Background(), " ")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "target ingress class cannot be empty")
}

func TestWaitForIngressControllerService_ImmediateDetection(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// Traefik service is already present — should return immediately
	traefikSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "traefik",
			Namespace: "pipeops-system",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "web", Port: 80},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(traefikSvc)
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	start := time.Now()
	result := watcher.waitForIngressControllerService(context.Background(), 10*time.Second)
	elapsed := time.Since(start)

	assert.NotNil(t, result, "should detect the pre-existing Traefik service")
	assert.Equal(t, "traefik", result.Name)
	assert.Equal(t, "pipeops-system", result.Namespace)
	assert.Equal(t, int32(80), result.Port)
	assert.Less(t, elapsed, 2*time.Second, "should detect immediately without polling")
}

func TestWaitForIngressControllerService_Timeout(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// No ingress controller service — should time out
	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	timeout := 2 * time.Second
	start := time.Now()
	result := watcher.waitForIngressControllerService(context.Background(), timeout)
	elapsed := time.Since(start)

	assert.Nil(t, result, "should return nil when service is not found")
	assert.GreaterOrEqual(t, elapsed, timeout, "should wait for the full timeout")
}

func TestWaitForIngressControllerService_ContextCancellation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	// No ingress controller service + parent context cancelled quickly
	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel the context after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result := watcher.waitForIngressControllerService(ctx, 30*time.Second)
	elapsed := time.Since(start)

	assert.Nil(t, result, "should return nil when context is cancelled")
	assert.Less(t, elapsed, 5*time.Second, "should exit promptly when context is cancelled")
}

// mockRouteClient is a flexible mock that supports configurable failures for
// RegisterRoute and UnregisterRoute, used by the retry tests.
type mockRouteClient struct {
	registerCalls       []RegisterRouteRequest
	registerCallCount   int
	registerMaxFailures int
	registerError       error

	unregisterCalls       []string
	unregisterCallCount   int
	unregisterMaxFailures int
	unregisterError       error

	syncCalls []SyncIngressesRequest
}

func (m *mockRouteClient) RegisterRoute(_ context.Context, req RegisterRouteRequest) error {
	m.registerCalls = append(m.registerCalls, req)
	m.registerCallCount++
	if m.registerCallCount <= m.registerMaxFailures {
		return m.registerError
	}
	return nil
}

func (m *mockRouteClient) UnregisterRoute(_ context.Context, hostname string) error {
	m.unregisterCalls = append(m.unregisterCalls, hostname)
	m.unregisterCallCount++
	if m.unregisterCallCount <= m.unregisterMaxFailures {
		return m.unregisterError
	}
	return nil
}

func (m *mockRouteClient) SyncIngresses(_ context.Context, req SyncIngressesRequest) error {
	m.syncCalls = append(m.syncCalls, req)
	return nil
}

func TestRegisterRouteWithRetry(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	req := RegisterRouteRequest{
		Hostname:    "example.com",
		ServiceName: "web",
		Namespace:   "default",
		ServicePort: 8080,
	}

	tests := []struct {
		name           string
		maxFailures    int
		registerError  error
		expectErr      bool
		errContains    string
		expectAttempts int
	}{
		{
			name:           "success on first attempt",
			maxFailures:    0,
			registerError:  nil,
			expectErr:      false,
			expectAttempts: 1,
		},
		{
			name:           "success after one failure",
			maxFailures:    1,
			registerError:  fmt.Errorf("temporary network error"),
			expectErr:      false,
			expectAttempts: 2,
		},
		{
			name:           "success after two failures",
			maxFailures:    2,
			registerError:  fmt.Errorf("connection reset"),
			expectErr:      false,
			expectAttempts: 3,
		},
		{
			name:           "fail after all retries exhausted",
			maxFailures:    3,
			registerError:  fmt.Errorf("persistent failure"),
			expectErr:      true,
			errContains:    "failed to register route after 3 attempts",
			expectAttempts: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockRouteClient{
				registerMaxFailures: tt.maxFailures,
				registerError:       tt.registerError,
			}

			fakeClient := fake.NewSimpleClientset()
			watcher := NewIngressWatcher(fakeClient, "test-cluster", mock, logger, "", "tunnel", nil)

			err := watcher.registerRouteWithRetry(context.Background(), req)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectAttempts, mock.registerCallCount)
		})
	}
}

func TestRegisterRouteWithRetry_ContextCancellation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	mock := &mockRouteClient{
		registerMaxFailures: 3, // always fail
		registerError:       fmt.Errorf("always fail"),
	}

	fakeClient := fake.NewSimpleClientset()
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mock, logger, "", "tunnel", nil)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay — enough for the first attempt but before all retries
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := watcher.registerRouteWithRetry(ctx, RegisterRouteRequest{
		Hostname:    "example.com",
		ServiceName: "web",
		Namespace:   "default",
		ServicePort: 8080,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "route registration cancelled")
	// Should have attempted at least 1 but not all 3
	assert.GreaterOrEqual(t, mock.registerCallCount, 1)
	assert.Less(t, mock.registerCallCount, 3)
}

func TestUnregisterRouteWithRetry(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name            string
		maxFailures     int
		unregisterError error
		expectErr       bool
		errContains     string
		expectAttempts  int
	}{
		{
			name:            "success on first attempt",
			maxFailures:     0,
			unregisterError: nil,
			expectErr:       false,
			expectAttempts:  1,
		},
		{
			name:            "success after one failure",
			maxFailures:     1,
			unregisterError: fmt.Errorf("temporary network error"),
			expectErr:       false,
			expectAttempts:  2,
		},
		{
			name:            "success after two failures",
			maxFailures:     2,
			unregisterError: fmt.Errorf("connection reset"),
			expectErr:       false,
			expectAttempts:  3,
		},
		{
			name:            "fail after all retries exhausted",
			maxFailures:     3,
			unregisterError: fmt.Errorf("persistent failure"),
			expectErr:       true,
			errContains:     "failed to unregister route after 3 attempts",
			expectAttempts:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockRouteClient{
				unregisterMaxFailures: tt.maxFailures,
				unregisterError:       tt.unregisterError,
			}

			fakeClient := fake.NewSimpleClientset()
			watcher := NewIngressWatcher(fakeClient, "test-cluster", mock, logger, "", "tunnel", nil)

			err := watcher.unregisterRouteWithRetry(context.Background(), "example.com")

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectAttempts, mock.unregisterCallCount)
		})
	}
}

func TestUnregisterRouteWithRetry_ContextCancellation(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	mock := &mockRouteClient{
		unregisterMaxFailures: 3,
		unregisterError:       fmt.Errorf("always fail"),
	}

	fakeClient := fake.NewSimpleClientset()
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mock, logger, "", "tunnel", nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := watcher.unregisterRouteWithRetry(ctx, "example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "route unregistration cancelled")
	assert.GreaterOrEqual(t, mock.unregisterCallCount, 1)
	assert.Less(t, mock.unregisterCallCount, 3)
}

func TestWaitForIngressControllerService_DelayedAppearance(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	fakeClient := fake.NewSimpleClientset()
	mockClient := &mockControllerClient{}
	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel", nil)

	// Simulate the service appearing after a short delay
	go func() {
		time.Sleep(2 * time.Second)
		traefikSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "traefik",
				Namespace: "pipeops-system",
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{Name: "web", Port: 80},
				},
			},
		}
		_, err := fakeClient.CoreV1().Services("pipeops-system").Create(
			context.Background(), traefikSvc, metav1.CreateOptions{},
		)
		if err != nil {
			fmt.Printf("test: failed to create service: %v\n", err)
		}
	}()

	start := time.Now()
	result := watcher.waitForIngressControllerService(context.Background(), 30*time.Second)
	elapsed := time.Since(start)

	assert.NotNil(t, result, "should detect the service after it appears")
	assert.Equal(t, "traefik", result.Name)
	assert.Equal(t, "pipeops-system", result.Namespace)
	// Should have taken at least ~2s (service creation delay) but finished well before the 30s timeout
	assert.GreaterOrEqual(t, elapsed, 2*time.Second, "should wait for the service to appear")
	assert.Less(t, elapsed, 15*time.Second, "should detect promptly after service appears")
}
