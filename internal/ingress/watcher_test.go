package ingress

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
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

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel")

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

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel")

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

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel")

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

	watcher := NewIngressWatcher(fakeClient, "test-cluster", mockClient, logger, "", "tunnel")

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
