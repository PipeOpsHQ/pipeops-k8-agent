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

	fakeClient := fake.NewSimpleClientset()
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
