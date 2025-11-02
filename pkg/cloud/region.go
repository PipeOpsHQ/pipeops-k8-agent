package cloud

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Provider represents a cloud provider
type Provider string

const (
	ProviderAWS          Provider = "aws"
	ProviderGCP          Provider = "gcp"
	ProviderAzure        Provider = "azure"
	ProviderDigitalOcean Provider = "digitalocean"
	ProviderLinode       Provider = "linode"
	ProviderHetzner      Provider = "hetzner"
	ProviderBareMetal    Provider = "bare-metal"
	ProviderOnPremises   Provider = "on-premises"
	ProviderUnknown      Provider = "unknown"
)

// RegionInfo contains detected cloud provider and region
type RegionInfo struct {
	Provider       Provider
	Region         string
	ProviderName   string     // Full provider name (e.g., "AWS", "Google Cloud")
	GeoIP          *GeoIPInfo // Geographic location from IP (for bare-metal/on-premises)
	RegistryRegion string     // Recommended registry region (eu/us)
}

// DetectRegion attempts to detect the cloud provider and region
func DetectRegion(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) RegionInfo {
	logger.Info("Detecting cloud provider and region...")

	// Try detection methods in order of reliability
	detectors := []func(context.Context, kubernetes.Interface, *logrus.Logger) (RegionInfo, bool){
		detectFromNodes,
		detectFromMetadataService,
		detectFromEnvironment,
	}

	for _, detector := range detectors {
		if info, detected := detector(ctx, k8sClient, logger); detected {
			// Populate RegistryRegion if not already set
			if info.RegistryRegion == "" {
				info.RegistryRegion = info.GetPreferredRegistryRegion()
			}
			logger.WithFields(logrus.Fields{
				"provider":        info.ProviderName,
				"region":          info.Region,
				"registry_region": info.RegistryRegion,
			}).Info("Cloud region detected successfully")
			return info
		}
	}

	logger.Info("Could not detect cloud provider, detecting via GeoIP...")

	// Detect geographic location for bare-metal/on-premises
	geoIP := DetectGeoIP(ctx, logger)
	registryRegion := "us"
	if geoIP != nil {
		registryRegion = geoIP.GetRegistryRegion()
	}

	return RegionInfo{
		Provider:       ProviderBareMetal,
		Region:         "on-premises",
		ProviderName:   "Bare Metal",
		GeoIP:          geoIP,
		RegistryRegion: registryRegion,
	}
}

// detectFromNodes detects provider and region from node labels
func detectFromNodes(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) (RegionInfo, bool) {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil || len(nodes.Items) == 0 {
		logger.WithError(err).Debug("Could not list nodes for region detection")
		return RegionInfo{}, false
	}

	node := nodes.Items[0]

	// Check provider ID first for accurate detection
	providerID := node.Spec.ProviderID

	// Detect by provider ID prefix (most reliable)
	if strings.HasPrefix(providerID, "aws://") {
		if region, ok := detectAWSFromNode(node); ok {
			return RegionInfo{
				Provider:     ProviderAWS,
				Region:       region,
				ProviderName: "AWS",
			}, true
		}
	}

	if strings.HasPrefix(providerID, "gce://") {
		if region, ok := detectGCPFromNode(node); ok {
			return RegionInfo{
				Provider:     ProviderGCP,
				Region:       region,
				ProviderName: "Google Cloud",
			}, true
		}
	}

	if strings.HasPrefix(providerID, "azure://") {
		if region, ok := detectAzureFromNode(node); ok {
			return RegionInfo{
				Provider:     ProviderAzure,
				Region:       region,
				ProviderName: "Azure",
			}, true
		}
	}

	if strings.HasPrefix(providerID, "digitalocean://") {
		if region, ok := detectDigitalOceanFromNode(node); ok {
			return RegionInfo{
				Provider:     ProviderDigitalOcean,
				Region:       region,
				ProviderName: "DigitalOcean",
			}, true
		}
	}

	if strings.HasPrefix(providerID, "hcloud://") {
		if region, ok := detectHetznerFromNode(node); ok {
			return RegionInfo{
				Provider:     ProviderHetzner,
				Region:       region,
				ProviderName: "Hetzner Cloud",
			}, true
		}
	}

	// Fallback to label-based detection if no provider ID
	// Check for provider-specific labels
	if region, ok := detectAWSFromNode(node); ok {
		return RegionInfo{
			Provider:     ProviderAWS,
			Region:       region,
			ProviderName: "AWS",
		}, true
	}

	if region, ok := detectGCPFromNode(node); ok {
		return RegionInfo{
			Provider:     ProviderGCP,
			Region:       region,
			ProviderName: "Google Cloud",
		}, true
	}

	if region, ok := detectAzureFromNode(node); ok {
		return RegionInfo{
			Provider:     ProviderAzure,
			Region:       region,
			ProviderName: "Azure",
		}, true
	}

	if region, ok := detectDigitalOceanFromNode(node); ok {
		return RegionInfo{
			Provider:     ProviderDigitalOcean,
			Region:       region,
			ProviderName: "DigitalOcean",
		}, true
	}

	if region, ok := detectLinodeFromNode(node); ok {
		return RegionInfo{
			Provider:     ProviderLinode,
			Region:       region,
			ProviderName: "Linode",
		}, true
	}

	if region, ok := detectHetznerFromNode(node); ok {
		return RegionInfo{
			Provider:     ProviderHetzner,
			Region:       region,
			ProviderName: "Hetzner Cloud",
		}, true
	}

	return RegionInfo{}, false
}

func detectAWSFromNode(node corev1.Node) (string, bool) {
	// AWS labels: topology.kubernetes.io/region or failure-domain.beta.kubernetes.io/region
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region, true
	}
	if region, ok := node.Labels["failure-domain.beta.kubernetes.io/region"]; ok {
		return region, true
	}
	// Check provider ID: aws:///us-east-1a/i-0123456789
	if strings.HasPrefix(node.Spec.ProviderID, "aws://") {
		parts := strings.Split(node.Spec.ProviderID, "/")
		if len(parts) >= 4 {
			// Extract region from zone (e.g., us-east-1a -> us-east-1)
			zone := parts[3]
			if len(zone) > 1 {
				region := zone[:len(zone)-1] // Remove last character (zone letter)
				return region, true
			}
		}
	}
	return "", false
}

func detectGCPFromNode(node corev1.Node) (string, bool) {
	// GCP labels: topology.kubernetes.io/region
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region, true
	}
	if region, ok := node.Labels["failure-domain.beta.kubernetes.io/region"]; ok {
		return region, true
	}
	// Check provider ID: gce://project-id/us-central1-a/instance-name
	if strings.HasPrefix(node.Spec.ProviderID, "gce://") {
		parts := strings.Split(node.Spec.ProviderID, "/")
		if len(parts) >= 4 {
			zone := parts[3]
			// Extract region from zone (e.g., us-central1-a -> us-central1)
			if idx := strings.LastIndex(zone, "-"); idx > 0 {
				return zone[:idx], true
			}
		}
	}
	return "", false
}

func detectAzureFromNode(node corev1.Node) (string, bool) {
	// Azure labels: topology.kubernetes.io/region
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region, true
	}
	if region, ok := node.Labels["failure-domain.beta.kubernetes.io/region"]; ok {
		return region, true
	}
	// Check provider ID: azure:///subscriptions/.../resourceGroups/.../providers/Microsoft.Compute/virtualMachines/...
	if strings.HasPrefix(node.Spec.ProviderID, "azure://") {
		// Azure region typically in location label
		if location, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
			// Zone format: eastus-1, westus2-2, etc.
			if idx := strings.LastIndex(location, "-"); idx > 0 {
				return location[:idx], true
			}
			return location, true
		}
	}
	return "", false
}

func detectDigitalOceanFromNode(node corev1.Node) (string, bool) {
	// DigitalOcean labels: topology.kubernetes.io/region or region
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region, true
	}
	if region, ok := node.Labels["region"]; ok {
		return region, true
	}
	// Check provider ID: digitalocean://12345678
	if strings.HasPrefix(node.Spec.ProviderID, "digitalocean://") {
		// Check zone label for region
		if zone, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
			return zone, true
		}
	}
	return "", false
}

func detectLinodeFromNode(node corev1.Node) (string, bool) {
	// Linode labels: topology.kubernetes.io/region
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region, true
	}
	// Linode-specific labels
	if region, ok := node.Labels["linode.com/region"]; ok {
		return region, true
	}
	return "", false
}

func detectHetznerFromNode(node corev1.Node) (string, bool) {
	// Hetzner labels: topology.kubernetes.io/region
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region, true
	}
	// Check provider ID: hcloud://123456
	if strings.HasPrefix(node.Spec.ProviderID, "hcloud://") {
		// Check zone/region labels
		if zone, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
			return zone, true
		}
	}
	return "", false
}

// detectFromMetadataService tries to detect region from cloud metadata services
func detectFromMetadataService(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) (RegionInfo, bool) {
	// Try AWS
	if region, ok := detectAWSMetadata(ctx); ok {
		return RegionInfo{
			Provider:     ProviderAWS,
			Region:       region,
			ProviderName: "AWS",
		}, true
	}

	// Try GCP
	if region, ok := detectGCPMetadata(ctx); ok {
		return RegionInfo{
			Provider:     ProviderGCP,
			Region:       region,
			ProviderName: "Google Cloud",
		}, true
	}

	// Try Azure
	if region, ok := detectAzureMetadata(ctx); ok {
		return RegionInfo{
			Provider:     ProviderAzure,
			Region:       region,
			ProviderName: "Azure",
		}, true
	}

	return RegionInfo{}, false
}

func detectAWSMetadata(ctx context.Context) (string, bool) {
	// AWS EC2 metadata service
	client := &http.Client{Timeout: 2 * time.Second}

	// Get token for IMDSv2
	tokenReq, _ := http.NewRequestWithContext(ctx, "PUT", "http://169.254.169.254/latest/api/token", nil)
	tokenReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		return "", false
	}
	defer tokenResp.Body.Close()

	token, _ := io.ReadAll(tokenResp.Body)

	// Get region with token
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/latest/meta-data/placement/region", nil)
	req.Header.Set("X-aws-ec2-metadata-token", string(token))
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		region, _ := io.ReadAll(resp.Body)
		return strings.TrimSpace(string(region)), true
	}

	return "", false
}

func detectGCPMetadata(ctx context.Context) (string, bool) {
	// GCP metadata service
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://metadata.google.internal/computeMetadata/v1/instance/zone", nil)
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		zone, _ := io.ReadAll(resp.Body)
		zoneStr := strings.TrimSpace(string(zone))
		// Zone format: projects/123/zones/us-central1-a
		parts := strings.Split(zoneStr, "/")
		if len(parts) > 0 {
			zone := parts[len(parts)-1]
			// Extract region from zone (e.g., us-central1-a -> us-central1)
			if idx := strings.LastIndex(zone, "-"); idx > 0 {
				return zone[:idx], true
			}
		}
	}

	return "", false
}

func detectAzureMetadata(ctx context.Context) (string, bool) {
	// Azure metadata service
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/instance/compute/location?api-version=2021-02-01&format=text", nil)
	req.Header.Set("Metadata", "true")

	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		region, _ := io.ReadAll(resp.Body)
		return strings.TrimSpace(string(region)), true
	}

	return "", false
}

// detectFromEnvironment tries to detect local/dev environments
func detectFromEnvironment(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) (RegionInfo, bool) {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5})
	if err != nil {
		return RegionInfo{}, false
	}

	if len(nodes.Items) == 0 {
		return RegionInfo{}, false
	}

	// Check for common local/dev Kubernetes distributions
	for _, node := range nodes.Items {
		// K3s detection
		if strings.Contains(node.Status.NodeInfo.OSImage, "k3s") ||
			strings.Contains(node.Status.NodeInfo.KubeletVersion, "k3s") ||
			node.Labels["node.kubernetes.io/instance-type"] == "k3s" {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       detectLocalRegion(node),
				ProviderName: "K3s",
			}, true
		}

		// kind (Kubernetes in Docker) detection
		if strings.Contains(node.Name, "kind-") ||
			node.Labels["kubernetes.io/hostname"] != "" && strings.Contains(node.Labels["kubernetes.io/hostname"], "kind") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "kind",
			}, true
		}

		// minikube detection
		if strings.Contains(node.Name, "minikube") ||
			node.Labels["minikube.k8s.io/name"] != "" {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "Minikube",
			}, true
		}

		// Docker Desktop Kubernetes
		if strings.Contains(node.Name, "docker-desktop") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "Docker Desktop",
			}, true
		}

		// Rancher detection
		if node.Labels["node-role.kubernetes.io/rancher"] != "" ||
			strings.Contains(node.Status.NodeInfo.OSImage, "RancherOS") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       detectLocalRegion(node),
				ProviderName: "Rancher",
			}, true
		}

		// OpenShift detection
		if node.Labels["node-role.kubernetes.io/master"] != "" &&
			strings.Contains(node.Status.NodeInfo.OSImage, "Red Hat") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       detectLocalRegion(node),
				ProviderName: "OpenShift",
			}, true
		}
	}

	// Check if all nodes are local/private IPs (bare-metal indicator)
	allLocalIPs := true
	for _, node := range nodes.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == "InternalIP" || addr.Type == "ExternalIP" {
				ip := addr.Address
				// Check if IP is private (10.x, 172.16-31.x, 192.168.x)
				if !isPrivateIP(ip) {
					allLocalIPs = false
					break
				}
			}
		}
		if !allLocalIPs {
			break
		}
	}

	if allLocalIPs && len(nodes.Items) > 0 {
		// Bare-metal or on-premises cluster with private IPs only
		return RegionInfo{
			Provider:     ProviderBareMetal,
			Region:       detectLocalRegion(nodes.Items[0]),
			ProviderName: "Bare Metal",
		}, true
	}

	return RegionInfo{}, false
}

// detectLocalRegion tries to determine a region name from hostname or labels
func detectLocalRegion(node corev1.Node) string {
	// Try to get a meaningful region from labels
	if datacenter, ok := node.Labels["topology.kubernetes.io/datacenter"]; ok {
		return datacenter
	}
	if zone, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
		return zone
	}
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		return region
	}

	// Try hostname from node addresses
	for _, addr := range node.Status.Addresses {
		if addr.Type == "Hostname" && addr.Address != "" && addr.Address != "localhost" {
			// Extract first part of hostname as location hint
			parts := strings.Split(addr.Address, "-")
			if len(parts) > 1 {
				return parts[0] // e.g., "dc1" from "dc1-node-01"
			}
		}
	}

	// Try node name as fallback (but avoid generic names)
	if node.Name != "" && node.Name != "localhost" {
		parts := strings.Split(node.Name, "-")
		if len(parts) > 1 {
			firstPart := parts[0]
			// Avoid generic names like "node", "worker", "master"
			if firstPart != "node" && firstPart != "worker" && firstPart != "master" &&
				firstPart != "control" && firstPart != "server" {
				return firstPart
			}
		}
	}

	// Default for on-premises
	return "on-premises"
}

// isPrivateIP checks if an IP is in private range
func isPrivateIP(ip string) bool {
	// Parse IP
	parsedIP := strings.Split(ip, ".")
	if len(parsedIP) != 4 {
		return false
	}

	// 10.0.0.0/8
	if parsedIP[0] == "10" {
		return true
	}

	// 172.16.0.0/12
	if parsedIP[0] == "172" {
		second := parsedIP[1]
		if len(second) > 0 {
			val := 0
			for _, c := range second {
				val = val*10 + int(c-'0')
			}
			if val >= 16 && val <= 31 {
				return true
			}
		}
	}

	// 192.168.0.0/16
	if parsedIP[0] == "192" && parsedIP[1] == "168" {
		return true
	}

	// 127.0.0.0/8 (localhost)
	if parsedIP[0] == "127" {
		return true
	}

	return false
}

// GetRegionCode returns a region code suitable for registration
func (r RegionInfo) GetRegionCode() string {
	if r.Region == "" {
		// Default based on provider type
		if r.Provider == ProviderBareMetal || r.Provider == ProviderOnPremises {
			return "on-premises"
		}
		return "agent-managed"
	}
	return r.Region
}

// GetCloudProvider returns the cloud provider name for registration
func (r RegionInfo) GetCloudProvider() string {
	if r.Provider == ProviderUnknown {
		return "agent"
	}
	return string(r.Provider)
}

// GetPreferredRegistryRegion returns the preferred registry region (eu/us)
// Uses GeoIP for bare-metal/on-premises, cloud region for cloud providers
func (r RegionInfo) GetPreferredRegistryRegion() string {
	// If already computed, return it
	if r.RegistryRegion != "" {
		return r.RegistryRegion
	}

	// For cloud providers, use region prefix
	if r.Provider != ProviderBareMetal && r.Provider != ProviderOnPremises && r.Provider != ProviderUnknown {
		if r.Region != "" && len(r.Region) >= 2 {
			regionPrefix := r.Region[0:2]
			// EU regions
			if regionPrefix == "eu" {
				return "eu"
			}
		}
		return "us"
	}

	// For bare-metal/on-premises, use GeoIP
	if r.GeoIP != nil {
		return r.GeoIP.GetRegistryRegion()
	}

	// Default
	return "us"
}
