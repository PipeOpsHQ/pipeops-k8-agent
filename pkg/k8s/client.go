package k8s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// DefaultK8sTimeout is the default timeout for Kubernetes API requests
	DefaultK8sTimeout = 30 * time.Second
)

// Client wraps the Kubernetes client for convenient in-cluster access.
// Client is safe for concurrent use after initialization. Do not modify
// client fields after creation.
type Client struct {
	clientset     *kubernetes.Clientset
	restConfig    *rest.Config
	httpClient    *http.Client
	baseTransport http.RoundTripper // Transport with TLS config but NO default token
	mu            sync.RWMutex      // protects concurrent access during initialization checks
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
		config.Timeout = DefaultK8sTimeout
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Transport with the default in-cluster token (for agent's internal calls)
	transport, err := rest.TransportFor(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport for Kubernetes client: %w", err)
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Preserve upstream redirect responses so the control plane/user agent can follow them.
			return http.ErrUseLastResponse
		},
	}

	// Create a base transport with TLS config but WITHOUT the bearer token
	// This is used for ProxyRequest to allow per-request tokens from the control plane
	baseConfig := rest.CopyConfig(config)
	baseConfig.BearerToken = ""
	baseConfig.BearerTokenFile = ""
	baseTransport, err := rest.TransportFor(baseConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create base transport: %w", err)
	}

	return &Client{
		clientset:     clientset,
		restConfig:    config,
		httpClient:    httpClient,
		baseTransport: baseTransport,
	}, nil
}

// GetClientset returns the underlying Kubernetes clientset
func (c *Client) GetClientset() kubernetes.Interface {
	if c == nil {
		return nil
	}
	return c.clientset
}

// GetVersion returns the Kubernetes cluster version
func (c *Client) GetVersion(ctx context.Context) (*version.Info, error) {
	if c == nil || c.clientset == nil {
		return nil, fmt.Errorf("kubernetes client not initialized")
	}
	versionInfo, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get server version: %w", err)
	}
	return versionInfo, nil
}

// GetNodeCount returns the number of nodes in the cluster
func (c *Client) GetNodeCount(ctx context.Context) (int, error) {
	if c == nil || c.clientset == nil {
		return 0, fmt.Errorf("kubernetes client not initialized")
	}
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list nodes: %w", err)
	}
	return len(nodes.Items), nil
}

// GetPodCount returns the number of pods across all namespaces
func (c *Client) GetPodCount(ctx context.Context) (int, error) {
	if c == nil || c.clientset == nil {
		return 0, fmt.Errorf("kubernetes client not initialized")
	}
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list pods: %w", err)
	}
	return len(pods.Items), nil
}

// GetClusterMetrics returns node count and pod count concurrently
func (c *Client) GetClusterMetrics(ctx context.Context) (nodeCount int, podCount int, err error) {
	var wg sync.WaitGroup
	var nodeErr, podErr error

	wg.Add(2)

	go func() {
		defer wg.Done()
		nodeCount, nodeErr = c.GetNodeCount(ctx)
	}()

	go func() {
		defer wg.Done()
		podCount, podErr = c.GetPodCount(ctx)
	}()

	wg.Wait()

	if nodeErr != nil {
		return 0, 0, fmt.Errorf("failed to get node count: %w", nodeErr)
	}
	if podErr != nil {
		return 0, 0, fmt.Errorf("failed to get pod count: %w", podErr)
	}

	return nodeCount, podCount, nil
}

// ProxyRequest executes a raw HTTP request against the Kubernetes API.
// It uses a base transport with the cluster's CA bundle but does NOT inject a default token,
// allowing the 'Authorization' header provided in the 'headers' map to be used instead.
// The caller is responsible for closing the returned response body when finished.
func (c *Client) ProxyRequest(ctx context.Context, method, path, rawQuery string, headers map[string][]string, body io.ReadCloser) (int, http.Header, io.ReadCloser, error) {
	if c == nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("kubernetes HTTP client not initialized")
	}

	c.mu.RLock()
	baseTransport := c.baseTransport
	restConfig := c.restConfig
	c.mu.RUnlock()

	if baseTransport == nil || restConfig == nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("kubernetes HTTP client not initialized")
	}

	method = strings.ToUpper(method)
	if method == "" {
		method = http.MethodGet
	}

	baseURL, err := url.Parse(restConfig.Host)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("invalid Kubernetes host URL: %w", err)
	}

	// Ensure path is absolute
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	baseURL.Path = path
	baseURL.RawQuery = rawQuery

	var reqBody io.Reader
	if body != nil {
		reqBody = body
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL.String(), reqBody)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("failed to create Kubernetes request: %w", err)
	}

	// Apply ALL provided headers, including Authorization.
	// Since we use baseTransport (which has no default token), the provided
	// Authorization header will be the one used for the request.
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Use the baseTransport directly to avoid the default in-cluster token injection
	// We create a temporary client for this request to inherit timeout settings
	proxyClient := &http.Client{
		Transport: baseTransport,
		Timeout:   restConfig.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := proxyClient.Do(req)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return 0, nil, nil, fmt.Errorf("kubernetes API request failed: %w", err)
	}

	return resp.StatusCode, resp.Header, resp.Body, nil
}

// TokenHasNamespaceWriteAccess checks whether the provided bearer token is allowed to create namespaces.
// This uses a SelfSubjectAccessReview, so the evaluation is performed by impersonating the token directly.
// Note: This is a minimal permission check. A token may pass this check but still fail other operations
// if it lacks additional required permissions.
func (c *Client) TokenHasNamespaceWriteAccess(ctx context.Context, token string) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("kubernetes client not initialized")
	}

	c.mu.RLock()
	restConfig := c.restConfig
	c.mu.RUnlock()

	if restConfig == nil {
		return false, fmt.Errorf("kubernetes client not initialized")
	}
	if token == "" {
		return false, fmt.Errorf("token is empty")
	}

	tokenConfig := rest.CopyConfig(restConfig)
	tokenConfig.BearerToken = token
	tokenConfig.BearerTokenFile = ""
	tokenConfig.Username = ""
	tokenConfig.Password = ""
	tokenConfig.CertFile = ""
	tokenConfig.KeyFile = ""
	tokenConfig.CertData = nil
	tokenConfig.KeyData = nil

	clientset, err := kubernetes.NewForConfig(tokenConfig)
	if err != nil {
		return false, fmt.Errorf("failed to create Kubernetes client for token validation: %w", err)
	}

	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:    "",
				Resource: "namespaces",
				Verb:     "create",
			},
		},
	}

	resp, err := clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("self subject access review failed: %w", err)
	}

	return resp.Status.Allowed, nil
}

// GetRestConfig returns the REST config for creating custom clients
func (c *Client) GetRestConfig() *rest.Config {
	if c == nil {
		return nil
	}
	return c.restConfig
}

// GetGatewayClient returns a Gateway API clientset for managing Gateway resources
// This is a placeholder - the actual Gateway client is created in the tunnel package
// using the rest config from GetRestConfig()
func (c *Client) GetGatewayClient() (interface{}, error) {
	if c == nil || c.restConfig == nil {
		return nil, fmt.Errorf("kubernetes client not initialized")
	}
	
	// Return the rest config so caller can create the gateway client
	// Use gatewayclient.NewForConfig(c.GetRestConfig()) in the caller
	return nil, fmt.Errorf("use GetRestConfig() and gatewayclient.NewForConfig() instead")
}
