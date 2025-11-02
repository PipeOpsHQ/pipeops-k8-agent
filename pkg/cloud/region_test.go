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
			name: "Bare metal cluster",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-01",
				},
				Status: corev1.NodeStatus{
					Addresses: []corev1.NodeAddress{
						{Type: "InternalIP", Address: "192.168.1.10"},
					},
				},
			},
			expectedProvider: ProviderBareMetal,
			expectedRegion:   "on-premises", // May be GeoIP country if available
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

			// For bare-metal, accept GeoIP-based region or the expected default
			if tt.expectedProvider == ProviderBareMetal && info.GeoIP != nil && info.GeoIP.Country != "" {
				// GeoIP detection is valid, log it
				t.Logf("Bare metal using GeoIP region: %s (Country: %s)", info.Region, info.GeoIP.Country)
			} else if info.Region != tt.expectedRegion {
				t.Errorf("DetectRegion() region = %v, want %v", info.Region, tt.expectedRegion)
			}
		})
	}
}

func TestRegionInfoMethods(t *testing.T) {
	tests := []struct {
		name                  string
		info                  RegionInfo
		expectedRegionCode    string
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
			name: "Bare metal",
			info: RegionInfo{
				Provider:     ProviderBareMetal,
				Region:       "on-premises",
				ProviderName: "Bare Metal",
			},
			expectedRegionCode:    "on-premises",
			expectedCloudProvider: "bare-metal",
		},
		{
			name: "K3s cluster",
			info: RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "dc1",
				ProviderName: "K3s",
			},
			expectedRegionCode:    "dc1",
			expectedCloudProvider: "on-premises",
		},
		{
			name: "Local dev",
			info: RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "kind",
			},
			expectedRegionCode:    "local-dev",
			expectedCloudProvider: "on-premises",
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

func TestDetectLocalEnvironments(t *testing.T) {
	tests := []struct {
		name             string
		node             corev1.Node
		expectedProvider Provider
		acceptRegions    []string // Accept any of these regions (for GeoIP variability)
	}{
		{
			name: "K3s cluster",
			node: corev1.Node{
				Status: corev1.NodeStatus{
					NodeInfo: corev1.NodeSystemInfo{
						KubeletVersion: "v1.27.3+k3s1",
						OSImage:        "Ubuntu 22.04 with k3s",
					},
				},
			},
			expectedProvider: ProviderOnPremises,
			acceptRegions:    []string{"on-premises"}, // Will use GeoIP country if available
		},
		{
			name: "kind cluster",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "kind-control-plane",
					Labels: map[string]string{
						"kubernetes.io/hostname": "kind-control-plane",
					},
				},
			},
			expectedProvider: ProviderOnPremises,
			acceptRegions:    []string{"local-dev"},
		},
		{
			name: "minikube cluster",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "minikube",
					Labels: map[string]string{
						"minikube.k8s.io/name": "minikube",
					},
				},
			},
			expectedProvider: ProviderOnPremises,
			acceptRegions:    []string{"local-dev"},
		},
		{
			name: "Docker Desktop",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "docker-desktop",
				},
			},
			expectedProvider: ProviderOnPremises,
			acceptRegions:    []string{"local-dev"},
		},
		{
			name: "Bare metal with private IPs",
			node: corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dc1-node-01",
				},
				Status: corev1.NodeStatus{
					Addresses: []corev1.NodeAddress{
						{Type: "InternalIP", Address: "192.168.1.10"},
						{Type: "Hostname", Address: "dc1-node-01"},
					},
				},
			},
			expectedProvider: ProviderBareMetal,
			acceptRegions:    []string{"dc1"}, // Will use GeoIP country if available, or hostname-derived dc1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(&tt.node)
			logger := logrus.New()
			logger.SetLevel(logrus.FatalLevel)

			info := DetectRegion(context.Background(), clientset, logger)

			if info.Provider != tt.expectedProvider {
				t.Errorf("DetectRegion() provider = %v, want %v", info.Provider, tt.expectedProvider)
			}

			// Check if region is one of the accepted values OR is a GeoIP-detected country
			regionMatched := false
			for _, acceptedRegion := range tt.acceptRegions {
				if info.Region == acceptedRegion {
					regionMatched = true
					break
				}
			}

			// Also accept GeoIP-based country codes (2-3 letter codes or country names)
			if !regionMatched && info.GeoIP != nil && info.GeoIP.Country != "" {
				// GeoIP detection is valid for on-premises/bare-metal
				if info.Provider == ProviderOnPremises || info.Provider == ProviderBareMetal {
					regionMatched = true
					t.Logf("Using GeoIP-detected region: %s (Country: %s)", info.Region, info.GeoIP.Country)
				}
			}

			if !regionMatched {
				t.Errorf("DetectRegion() region = %v, want one of %v (or GeoIP country)", info.Region, tt.acceptRegions)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.255.255", true},
		{"127.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := isPrivateIP(tt.ip); got != tt.expected {
				t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.expected)
			}
		})
	}
}
