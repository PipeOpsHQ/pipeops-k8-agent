package cloud

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
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

func getMetadataTimeout() time.Duration {
	// Allow override via environment variable for tests
	if timeoutStr := os.Getenv("METADATA_TIMEOUT_MS"); timeoutStr != "" {
		if ms, err := strconv.Atoi(timeoutStr); err == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	// Use shorter timeout in test/CI environments
	if os.Getenv("CI") != "" || os.Getenv("SKIP_GEOIP") != "" {
		return 100 * time.Millisecond
	}
	return 2 * time.Second
}

// RegionInfo contains detected cloud provider and region
type RegionInfo struct {
	Provider       Provider
	Region         string
	Country        string     // Country name (e.g., "United Kingdom")
	ProviderName   string     // Full provider name (e.g., "AWS", "Google Cloud")
	GeoIP          *GeoIPInfo // Geographic location from IP (for bare-metal/on-premises)
	RegistryRegion string     // Recommended registry region (eu/us)
}

// DetectRegion attempts to detect the cloud provider and region
func DetectRegion(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger) RegionInfo {
	logger.Info("Detecting cloud provider and region...")

	// Detect GeoIP early - we'll use it for bare-metal/on-premises
	geoIP := DetectGeoIP(ctx, logger)

	// Try detection methods in order of reliability
	// Check nodes first (most reliable), then metadata service, then system DMI, then local/dev environments
	// Metadata services must run BEFORE environment detection to properly detect K3s on cloud VMs
	detectors := []func(context.Context, kubernetes.Interface, *logrus.Logger, *GeoIPInfo) (RegionInfo, bool){
		detectFromNodes,
		detectFromMetadataService, // Moved BEFORE detectFromEnvironment
		detectFromSystem,          // Check system DMI/Vendor info
		detectFromEnvironment,
	}

	for _, detector := range detectors {
		if info, detected := detector(ctx, k8sClient, logger, geoIP); detected {
			// Populate RegistryRegion if not already set
			if info.RegistryRegion == "" {
				info.RegistryRegion = info.GetPreferredRegistryRegion()
			}
			// Populate Country from GeoIP if available and not set
			if info.Country == "" && geoIP != nil {
				info.Country = geoIP.Country
			}

			logger.WithFields(logrus.Fields{
				"provider":        info.ProviderName,
				"region":          info.Region,
				"country":         info.Country,
				"registry_region": info.RegistryRegion,
			}).Info("Cloud region detected successfully")
			return info
		}
	}

	logger.Info("Could not detect cloud provider, using GeoIP fallback...")

	// Fallback to GeoIP-based region
	registryRegion := "us"
	region := "on-premises"
	country := ""

	if geoIP != nil {
		registryRegion = geoIP.GetRegistryRegion()
		country = geoIP.Country
		// Use country or city as region if available
		if geoIP.Country != "" {
			region = strings.ToLower(geoIP.Country)
		}
	}

	return RegionInfo{
		Provider:       ProviderBareMetal,
		Region:         region,
		Country:        country,
		ProviderName:   "Bare Metal",
		GeoIP:          geoIP,
		RegistryRegion: registryRegion,
	}
}

// detectFromNodes detects provider and region from node labels
func detectFromNodes(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger, geoIP *GeoIPInfo) (RegionInfo, bool) {
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
func detectFromMetadataService(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger, geoIP *GeoIPInfo) (RegionInfo, bool) {
	// Skip metadata service detection in test/CI environments to avoid false positives
	if os.Getenv("SKIP_GEOIP") != "" || os.Getenv("SKIP_METADATA") != "" {
		logger.Debug("Skipping metadata service detection (SKIP_GEOIP or SKIP_METADATA set)")
		return RegionInfo{}, false
	}

	logger.Debug("Trying cloud metadata services...")

	// Try AWS
	if region, ok := detectAWSMetadata(ctx); ok {
		logger.WithFields(logrus.Fields{
			"provider": "AWS",
			"region":   region,
			"method":   "metadata-service",
		}).Info("Detected cloud provider from metadata service")
		return RegionInfo{
			Provider:     ProviderAWS,
			Region:       region,
			ProviderName: "AWS",
		}, true
	}

	// Try GCP
	if region, ok := detectGCPMetadata(ctx); ok {
		logger.WithFields(logrus.Fields{
			"provider": "GCP",
			"region":   region,
			"method":   "metadata-service",
		}).Info("Detected cloud provider from metadata service")
		return RegionInfo{
			Provider:     ProviderGCP,
			Region:       region,
			ProviderName: "Google Cloud",
		}, true
	}

	// Try Azure
	if region, ok := detectAzureMetadata(ctx); ok {
		logger.WithFields(logrus.Fields{
			"provider": "Azure",
			"region":   region,
			"method":   "metadata-service",
		}).Info("Detected cloud provider from metadata service")
		return RegionInfo{
			Provider:     ProviderAzure,
			Region:       region,
			ProviderName: "Azure",
		}, true
	}

	// Try DigitalOcean
	if region, ok := detectDigitalOceanMetadata(ctx); ok {
		logger.WithFields(logrus.Fields{
			"provider": "DigitalOcean",
			"region":   region,
			"method":   "metadata-service",
		}).Info("Detected cloud provider from metadata service")
		return RegionInfo{
			Provider:     ProviderDigitalOcean,
			Region:       region,
			ProviderName: "DigitalOcean",
		}, true
	}

	logger.Debug("No cloud provider detected from metadata services")
	return RegionInfo{}, false
}

// detectFromSystem tries to detect cloud provider from system DMI information
func detectFromSystem(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger, geoIP *GeoIPInfo) (RegionInfo, bool) {
	// Read sys_vendor and product_name
	vendorBytes, vendorErr := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	productBytes, productErr := os.ReadFile("/sys/class/dmi/id/product_name")

	// If both files are missing, DMI info is not available (common in containers/CI)
	if vendorErr != nil && productErr != nil {
		logger.Debug("DMI information not available (not a VM or running in container)")
		return RegionInfo{}, false
	}

	// Only use the data if the read was successful
	vendor := ""
	if vendorErr == nil {
		vendor = strings.TrimSpace(string(vendorBytes))
	}

	product := ""
	if productErr == nil {
		product = strings.TrimSpace(string(productBytes))
	}

	// If both are empty, no useful DMI info
	if vendor == "" && product == "" {
		logger.Debug("DMI information empty")
		return RegionInfo{}, false
	}

	logger.WithFields(logrus.Fields{
		"vendor":  vendor,
		"product": product,
	}).Debug("Checking system DMI information")

	// AWS
	if strings.Contains(vendor, "Amazon EC2") || strings.Contains(product, "Amazon EC2") {
		return RegionInfo{
			Provider:     ProviderAWS,
			Region:       "aws-unknown", // We know it's AWS but not the region from DMI
			ProviderName: "AWS",
		}, true
	}

	// GCP
	if strings.Contains(vendor, "Google") || strings.Contains(product, "Google") {
		return RegionInfo{
			Provider:     ProviderGCP,
			Region:       "gcp-unknown",
			ProviderName: "Google Cloud",
		}, true
	}

	// Azure
	if strings.Contains(vendor, "Microsoft Corporation") && (strings.Contains(product, "Virtual Machine") || strings.Contains(product, "Hyper-V")) {
		return RegionInfo{
			Provider:     ProviderAzure,
			Region:       "azure-unknown",
			ProviderName: "Azure",
		}, true
	}

	// DigitalOcean
	if strings.Contains(vendor, "DigitalOcean") {
		return RegionInfo{
			Provider:     ProviderDigitalOcean,
			Region:       "do-unknown",
			ProviderName: "DigitalOcean",
		}, true
	}

	// Linode
	if strings.Contains(vendor, "Linode") {
		return RegionInfo{
			Provider:     ProviderLinode,
			Region:       "linode-unknown",
			ProviderName: "Linode",
		}, true
	}

	// Hetzner
	if strings.Contains(vendor, "Hetzner") {
		return RegionInfo{
			Provider:     ProviderHetzner,
			Region:       "hetzner-unknown",
			ProviderName: "Hetzner Cloud",
		}, true
	}

	return RegionInfo{}, false
}

func detectAWSMetadata(ctx context.Context) (string, bool) {
	// AWS EC2 metadata service
	client := &http.Client{Timeout: getMetadataTimeout()}

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
	client := &http.Client{Timeout: getMetadataTimeout()}
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
	client := &http.Client{Timeout: getMetadataTimeout()}
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

func detectDigitalOceanMetadata(ctx context.Context) (string, bool) {
	// DigitalOcean metadata service
	client := &http.Client{Timeout: getMetadataTimeout()}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/v1/region", nil)

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
func detectFromEnvironment(ctx context.Context, k8sClient kubernetes.Interface, logger *logrus.Logger, geoIP *GeoIPInfo) (RegionInfo, bool) {
	nodes, err := k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5})
	if err != nil {
		return RegionInfo{}, false
	}

	if len(nodes.Items) == 0 {
		return RegionInfo{}, false
	}

	// Check for common local/dev Kubernetes distributions
	for _, node := range nodes.Items {
		// K3s detection - but check if it's on a cloud provider first
		if strings.Contains(node.Status.NodeInfo.OSImage, "k3s") ||
			strings.Contains(node.Status.NodeInfo.KubeletVersion, "k3s") ||
			node.Labels["node.kubernetes.io/instance-type"] == "k3s" {

			// Don't return immediately - K3s might be on cloud
			// Only return on-premises if no cloud provider detected
			// This should only be reached if detectFromMetadataService already ran and failed
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       detectLocalRegion(node, geoIP),
				ProviderName: "K3s",
				GeoIP:        geoIP,
			}, true
		}

		// kind (Kubernetes in Docker) detection
		if strings.Contains(node.Name, "kind-") ||
			node.Labels["kubernetes.io/hostname"] != "" && strings.Contains(node.Labels["kubernetes.io/hostname"], "kind") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "kind",
				GeoIP:        geoIP,
			}, true
		}

		// minikube detection
		if strings.Contains(node.Name, "minikube") ||
			node.Labels["minikube.k8s.io/name"] != "" {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "Minikube",
				GeoIP:        geoIP,
			}, true
		}

		// Docker Desktop Kubernetes
		if strings.Contains(node.Name, "docker-desktop") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       "local-dev",
				ProviderName: "Docker Desktop",
				GeoIP:        geoIP,
			}, true
		}

		// Rancher detection
		if node.Labels["node-role.kubernetes.io/rancher"] != "" ||
			strings.Contains(node.Status.NodeInfo.OSImage, "RancherOS") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       detectLocalRegion(node, geoIP),
				ProviderName: "Rancher",
				GeoIP:        geoIP,
			}, true
		}

		// OpenShift detection
		if node.Labels["node-role.kubernetes.io/master"] != "" &&
			strings.Contains(node.Status.NodeInfo.OSImage, "Red Hat") {
			return RegionInfo{
				Provider:     ProviderOnPremises,
				Region:       detectLocalRegion(node, geoIP),
				ProviderName: "OpenShift",
				GeoIP:        geoIP,
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
			Region:       detectLocalRegion(nodes.Items[0], geoIP),
			ProviderName: "Bare Metal",
			GeoIP:        geoIP,
		}, true
	}

	return RegionInfo{}, false
}

// detectLocalRegion tries to determine a region name, prioritizing GeoIP over node labels
func detectLocalRegion(node corev1.Node, geoIP *GeoIPInfo) string {
	// Prioritize GeoIP detection for accurate geographic location
	if geoIP != nil && geoIP.Country != "" {
		// Use country code as region for on-premises/bare-metal
		return strings.ToLower(geoIP.Country)
	}

	// Try datacenter-specific labels (these are usually explicitly set by admins)
	if datacenter, ok := node.Labels["topology.kubernetes.io/datacenter"]; ok {
		// Validate it's not a generic/OS value
		if isValidRegionLabel(datacenter) {
			return datacenter
		}
	}

	// Try zone label only if it looks like a valid datacenter zone
	if zone, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
		if isValidRegionLabel(zone) {
			return zone
		}
	}

	// Try region label only if it looks valid
	if region, ok := node.Labels["topology.kubernetes.io/region"]; ok {
		if isValidRegionLabel(region) {
			return region
		}
	}

	// Try hostname from node addresses only if it looks like a datacenter identifier
	for _, addr := range node.Status.Addresses {
		if addr.Type == "Hostname" && addr.Address != "" && addr.Address != "localhost" {
			// Extract first part of hostname as location hint
			parts := strings.Split(addr.Address, "-")
			if len(parts) > 1 && isValidRegionLabel(parts[0]) {
				return parts[0] // e.g., "dc1" from "dc1-node-01"
			}
		}
	}

	// Default for on-premises
	return "on-premises"
}

// isValidRegionLabel checks if a label value looks like a valid region/datacenter identifier
// and not an OS name or other generic value
func isValidRegionLabel(label string) bool {
	label = strings.ToLower(label)

	// Reject OS names and generic values
	invalidLabels := []string{
		"ubuntu", "debian", "centos", "rhel", "fedora", "alpine", "rocky", "alma",
		"linux", "windows", "node", "worker", "master", "control", "server",
		"localhost", "default", "unknown", "none", "null", "n/a",
	}

	for _, invalid := range invalidLabels {
		if label == invalid || strings.Contains(label, invalid) {
			return false
		}
	}

	// Accept labels that look like datacenter/region identifiers
	// Examples: dc1, us-east, eu-west, asia-1, etc.
	if strings.HasPrefix(label, "dc") ||
		strings.HasPrefix(label, "az") ||
		strings.Contains(label, "east") ||
		strings.Contains(label, "west") ||
		strings.Contains(label, "north") ||
		strings.Contains(label, "south") ||
		strings.Contains(label, "central") ||
		strings.Contains(label, "asia") ||
		strings.Contains(label, "europe") ||
		strings.Contains(label, "america") {
		return true
	}

	// If it's a short code (2-3 chars), it might be valid (e.g., us, eu, sg)
	if len(label) >= 2 && len(label) <= 3 {
		return true
	}

	return false
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
	// Priority: Country Name > Region Code
	if r.Country != "" {
		return r.Country
	}

	if r.Region == "" {
		// Default based on provider type
		if r.Provider == ProviderBareMetal || r.Provider == ProviderOnPremises {
			return "on-premises"
		}
		// For known cloud providers without region, use provider name as fallback
		if r.Provider != ProviderUnknown && r.Provider != "" {
			return string(r.Provider)
		}
		return "unknown"
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
