package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client wraps the Kubernetes client for convenient in-cluster access
type Client struct {
	clientset *kubernetes.Clientset
}

// NewInClusterClient creates a new Kubernetes client using in-cluster configuration
// This automatically uses the ServiceAccount token and CA cert mounted by Kubernetes
func NewInClusterClient() (*Client, error) {
	// Create in-cluster config (uses ServiceAccount token and CA cert automatically)
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	return &Client{
		clientset: clientset,
	}, nil
}

// GetVersion returns the Kubernetes cluster version
func (c *Client) GetVersion() (*version.Info, error) {
	versionInfo, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get server version: %w", err)
	}
	return versionInfo, nil
}

// GetNodeCount returns the number of nodes in the cluster
func (c *Client) GetNodeCount(ctx context.Context) (int, error) {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list nodes: %w", err)
	}
	return len(nodes.Items), nil
}

// GetPodCount returns the number of pods across all namespaces
func (c *Client) GetPodCount(ctx context.Context) (int, error) {
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list pods: %w", err)
	}
	return len(pods.Items), nil
}

// GetClusterMetrics returns node count and pod count
func (c *Client) GetClusterMetrics(ctx context.Context) (nodeCount int, podCount int, err error) {
	nodeCount, err = c.GetNodeCount(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get node count: %w", err)
	}

	podCount, err = c.GetPodCount(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get pod count: %w", err)
	}

	return nodeCount, podCount, nil
}
