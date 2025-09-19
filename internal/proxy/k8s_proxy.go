package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/k8s"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
)

// K8sProxy handles proxying Kubernetes API requests over HTTP via FRP tunnels
type K8sProxy struct {
	k8sClient *k8s.Client
	logger    *logrus.Logger
	config    *rest.Config
}

// NewK8sProxy creates a new Kubernetes API proxy for FRP
func NewK8sProxy(k8sClient *k8s.Client, logger *logrus.Logger) *K8sProxy {
	return &K8sProxy{
		k8sClient: k8sClient,
		logger:    logger,
		config:    k8sClient.GetConfig(),
	}
}

// HandleHTTPRequest handles HTTP requests from FRP tunnels
func (p *K8sProxy) HandleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	p.logger.WithFields(logrus.Fields{
		"method": r.Method,
		"path":   r.URL.Path,
		"query":  r.URL.RawQuery,
	}).Debug("Processing K8s API request via FRP")

	// Build the full Kubernetes API URL
	apiURL := strings.TrimSuffix(p.config.Host, "/") + r.URL.Path
	if r.URL.RawQuery != "" {
		apiURL += "?" + r.URL.RawQuery
	}

	// Create HTTP request to Kubernetes API
	var bodyReader io.Reader
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			p.sendError(w, http.StatusBadRequest, fmt.Sprintf("Failed to read request body: %v", err))
			return
		}
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequest(r.Method, apiURL, bodyReader)
	if err != nil {
		p.sendError(w, http.StatusBadRequest, fmt.Sprintf("Failed to create request: %v", err))
		return
	}

	// Copy headers from original request
	for key, values := range r.Header {
		// Skip headers that shouldn't be forwarded
		if strings.ToLower(key) == "host" || strings.ToLower(key) == "authorization" {
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	// Add Kubernetes authentication
	if p.config.BearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.BearerToken)
	}

	// Create HTTP client with proper TLS config
	transport := &http.Transport{}
	if p.config.TLSClientConfig.CAFile != "" || len(p.config.TLSClientConfig.CAData) > 0 {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: p.config.TLSClientConfig.Insecure,
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// Execute request to Kubernetes API
	httpResp, err := client.Do(httpReq)
	if err != nil {
		p.sendError(w, http.StatusServiceUnavailable, fmt.Sprintf("Request failed: %v", err))
		return
	}
	defer httpResp.Body.Close()

	// Copy response headers
	for key, values := range httpResp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set status code
	w.WriteHeader(httpResp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, httpResp.Body); err != nil {
		p.logger.WithError(err).Error("Failed to copy response body")
	}
}

// ProxyRequest handles a single K8s API request (programmatic interface)
func (p *K8sProxy) ProxyRequest(ctx context.Context, req *types.K8sProxyRequest) (*types.K8sProxyResponse, error) {
	// Build the full URL
	apiURL := strings.TrimSuffix(p.config.Host, "/") + req.Path
	if len(req.Query) > 0 {
		queryValues := url.Values(req.Query)
		apiURL += "?" + queryValues.Encode()
	}

	// Create HTTP request
	var bodyReader io.Reader
	if req.Body != nil {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, apiURL, bodyReader)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusBadRequest,
			Error:      fmt.Sprintf("Failed to create request: %v", err),
		}, nil
	}

	// Set headers
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}

	// Add authentication
	if p.config.BearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.BearerToken)
	}

	// Create HTTP client with proper TLS config
	transport := &http.Transport{}
	if p.config.TLSClientConfig.CAFile != "" || len(p.config.TLSClientConfig.CAData) > 0 {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: p.config.TLSClientConfig.Insecure,
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// Execute request
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusServiceUnavailable,
			Error:      fmt.Sprintf("Request failed: %v", err),
		}, nil
	}
	defer httpResp.Body.Close()

	// Read response body
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return &types.K8sProxyResponse{
			ID:         req.ID,
			StatusCode: http.StatusInternalServerError,
			Error:      fmt.Sprintf("Failed to read response: %v", err),
		}, nil
	}

	// Convert headers
	headers := make(map[string]string)
	for key, values := range httpResp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return &types.K8sProxyResponse{
		ID:         req.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    headers,
		Body:       body,
	}, nil
}

// sendError sends an error response
func (p *K8sProxy) sendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, `{"error": "%s"}`, message)
}
