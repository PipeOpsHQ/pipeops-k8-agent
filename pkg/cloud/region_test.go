package cloud

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDetectAWSFromNode(t *testing.T) {
	tests := []struct {
		name     string
		node     corev1.Node
		expected string
		detected bool
	}{
		{
			name: "AWS with topology label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "us-east-1",
					},
				},
			},
			expected: "us-east-1",
			detected: true,
		},
		{
			name: "AWS with legacy label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"failure-domain.beta.kubernetes.io/region": "us-west-2",
					},
				},
			},
			expected: "us-west-2",
			detected: true,
		},
		{
			name: "AWS with provider ID",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					ProviderID: "aws:///us-east-1a/i-0123456789abcdef0",
				},
			},
			expected: "us-east-1",
			detected: true,
		},
		{
			name:     "No AWS labels",
			node:     corev1.Node{},
			expected: "",
			detected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region, detected := detectAWSFromNode(tt.node)
			if detected != tt.detected {
				t.Errorf("detectAWSFromNode() detected = %v, want %v", detected, tt.detected)
			}
			if region != tt.expected {
				t.Errorf("detectAWSFromNode() region = %v, want %v", region, tt.expected)
			}
		})
	}
}

func TestDetectGCPFromNode(t *testing.T) {
	tests := []struct {
		name     string
		node     corev1.Node
		expected string
		detected bool
	}{
		{
			name: "GCP with topology label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "us-central1",
					},
				},
			},
			expected: "us-central1",
			detected: true,
		},
		{
			name: "GCP with provider ID",
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					ProviderID: "gce://project-123/us-central1-a/instance-name",
				},
			},
			expected: "us-central1",
			detected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region, detected := detectGCPFromNode(tt.node)
			if detected != tt.detected {
				t.Errorf("detectGCPFromNode() detected = %v, want %v", detected, tt.detected)
			}
			if region != tt.expected {
				t.Errorf("detectGCPFromNode() region = %v, want %v", region, tt.expected)
			}
		})
	}
}

func TestDetectDigitalOceanFromNode(t *testing.T) {
	tests := []struct {
		name     string
		node     corev1.Node
		expected string
		detected bool
	}{
		{
			name: "DigitalOcean with topology label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "nyc3",
					},
				},
			},
			expected: "nyc3",
			detected: true,
		},
		{
			name: "DigitalOcean with region label",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"region": "sfo2",
					},
				},
			},
			expected: "sfo2",
			detected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region, detected := detectDigitalOceanFromNode(tt.node)
			if detected != tt.detected {
				t.Errorf("detectDigitalOceanFromNode() detected = %v, want %v", detected, tt.detected)
			}
			if region != tt.expected {
				t.Errorf("detectDigitalOceanFromNode() region = %v, want %v", region, tt.expected)
			}
		})
	}
}

func TestDetectRegionInfo(t *testing.T) {
	tests := []struct {
		name             string
		node             corev1.Node
		expectedProvider Provider
		expectedRegion   string
	}{
		{
			name: "AWS cluster",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "us-east-1",
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "aws:///us-east-1a/i-0123456789",
				},
			},
			expectedProvider: ProviderAWS,
			expectedRegion:   "us-east-1",
		},
		{
			name: "GCP cluster",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"topology.kubernetes.io/region": "us-central1",
						"cloud.google.com/gke-nodepool": "default-pool", // GKE-specific label
					},
				},
				Spec: corev1.NodeSpec{
					ProviderID: "gce://project/us-central1-a/instance",
				},
			},
			expectedProvider: ProviderGCP,
			expectedRegion:   "us-central1",
		},
		{
			name:             "Unknown cluster",
			node:             corev1.Node{},
			expectedProvider: ProviderUnknown,
			expectedRegion:   "agent-managed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(&tt.node)
			logger := logrus.New()
			logger.SetLevel(logrus.FatalLevel) // Suppress logs during tests

			info := DetectRegion(context.Background(), clientset, logger)

			if info.Provider != tt.expectedProvider {
				t.Errorf("DetectRegion() provider = %v, want %v", info.Provider, tt.expectedProvider)
			}
			if info.Region != tt.expectedRegion {
				t.Errorf("DetectRegion() region = %v, want %v", info.Region, tt.expectedRegion)
			}
		})
	}
}

func TestRegionInfoMethods(t *testing.T) {
	tests := []struct {
		name                 string
		info                 RegionInfo
		expectedRegionCode   string
		expectedCloudProvider string
	}{
		{
			name: "AWS region",
			info: RegionInfo{
				Provider:     ProviderAWS,
				Region:       "us-east-1",
				ProviderName: "AWS",
			},
			expectedRegionCode:    "us-east-1",
			expectedCloudProvider: "aws",
		},
		{
			name: "GCP region",
			info: RegionInfo{
				Provider:     ProviderGCP,
				Region:       "us-central1",
				ProviderName: "Google Cloud",
			},
			expectedRegionCode:    "us-central1",
			expectedCloudProvider: "gcp",
		},
		{
			name: "Unknown region",
			info: RegionInfo{
				Provider:     ProviderUnknown,
				Region:       "agent-managed",
				ProviderName: "Self-Managed",
			},
			expectedRegionCode:    "agent-managed",
			expectedCloudProvider: "agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.GetRegionCode(); got != tt.expectedRegionCode {
				t.Errorf("GetRegionCode() = %v, want %v", got, tt.expectedRegionCode)
			}
			if got := tt.info.GetCloudProvider(); got != tt.expectedCloudProvider {
				t.Errorf("GetCloudProvider() = %v, want %v", got, tt.expectedCloudProvider)
			}
		})
	}
}
