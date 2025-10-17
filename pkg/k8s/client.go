package k8s

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client wraps the Kubernetes client for convenient in-cluster access
type Client struct {
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
	httpClient *http.Client
}

// NewInClusterClient creates a new Kubernetes client using in-cluster configuration
// This automatically uses the ServiceAccount token and CA cert mounted by Kubernetes
func NewInClusterClient() (*Client, error) {
	// Create in-cluster config (uses ServiceAccount token and CA cert automatically)
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	transport, err := rest.TransportFor(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport for Kubernetes client: %w", err)
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
	}

	return &Client{
		clientset:  clientset,
		restConfig: config,
		httpClient: httpClient,
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

// ProxyRequest executes a raw HTTP request against the Kubernetes API using the in-cluster credentials.
func (c *Client) ProxyRequest(ctx context.Context, method, path, rawQuery string, headers map[string][]string, body []byte) (int, http.Header, []byte, error) {
	if c.httpClient == nil || c.restConfig == nil {
		return 0, nil, nil, fmt.Errorf("kubernetes HTTP client not initialized")
	}

	method = strings.ToUpper(method)
	if method == "" {
		method = http.MethodGet
	}

	baseURL, err := url.Parse(c.restConfig.Host)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("invalid Kubernetes host URL: %w", err)
	}

	// Ensure path is absolute
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	baseURL.Path = path
	baseURL.RawQuery = rawQuery

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL.String(), bodyReader)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to create Kubernetes request: %w", err)
	}

	// Apply provided headers (skip hop-by-hop headers and Authorization, which is handled by transport)
	for key, values := range headers {
		if strings.EqualFold(key, "authorization") {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("kubernetes API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to read Kubernetes response body: %w", err)
	}

	return resp.StatusCode, resp.Header.Clone(), respBody, nil
}
