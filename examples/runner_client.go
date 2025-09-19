package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// HTTPClient represents a client that communicates via HTTP through FRP tunnels
type HTTPClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// RunnerExample demonstrates how a Runner would connect to the VM Agent
// via FRP tunnels and use the K8s API proxy functionality over HTTP
func main() {
	// FRP tunnel endpoint (normally this would come from Control Plane)
	agentEndpoint := "http://localhost:8080/api/v1/k8s"
	token := "runner-bearer-token"

	// Create HTTP client for FRP communication
	client := newHTTPClient(agentEndpoint, token)

	fmt.Println("Connected to VM Agent K8s API proxy via FRP")

	// Example 1: List all pods in default namespace
	fmt.Println("\n=== Example 1: List Pods ===")
	if err := listPods(client); err != nil {
		log.Printf("Failed to list pods: %v", err)
	}

	// Example 2: Get deployments
	fmt.Println("\n=== Example 2: List Deployments ===")
	if err := listDeployments(client); err != nil {
		log.Printf("Failed to list deployments: %v", err)
	}

	// Example 3: Get cluster info
	fmt.Println("\n=== Example 3: Get Cluster Info ===")
	if err := getClusterInfo(client); err != nil {
		log.Printf("Failed to get cluster info: %v", err)
	}

	fmt.Println("\nRunner example completed")
}

// newHTTPClient creates a new HTTP client for FRP communication
func newHTTPClient(baseURL, token string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// makeRequest makes an HTTP request to the K8s API via FRP tunnel
func (c *HTTPClient) makeRequest(method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication header
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	return resp, nil
}

// listPods demonstrates listing pods via the K8s API proxy over HTTP
func listPods(client *HTTPClient) error {
	// Make HTTP request to K8s API via FRP tunnel
	resp, err := client.makeRequest("GET", "/api/v1/namespaces/default/pods", nil)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var podList map[string]interface{}
	if err := json.Unmarshal(body, &podList); err != nil {
		return fmt.Errorf("failed to parse pod list: %w", err)
	}

	items, ok := podList["items"].([]interface{})
	if !ok {
		fmt.Println("No pods found or invalid response format")
		return nil
	}

	fmt.Printf("Found %d pods:\n", len(items))
	for i, item := range items {
		pod := item.(map[string]interface{})
		metadata := pod["metadata"].(map[string]interface{})
		name := metadata["name"].(string)
		namespace := metadata["namespace"].(string)

		status := pod["status"].(map[string]interface{})
		phase := status["phase"].(string)

		fmt.Printf("  %d. %s/%s (Phase: %s)\n", i+1, namespace, name, phase)
	}

	return nil
}

// listDeployments demonstrates listing deployments via the K8s API proxy over HTTP
func listDeployments(client *HTTPClient) error {
	// Make HTTP request to K8s API via FRP tunnel
	resp, err := client.makeRequest("GET", "/apis/apps/v1/namespaces/default/deployments", nil)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse and display results
	var deploymentList map[string]interface{}
	if err := json.Unmarshal(body, &deploymentList); err != nil {
		return fmt.Errorf("failed to parse deployment list: %w", err)
	}

	items, ok := deploymentList["items"].([]interface{})
	if !ok {
		fmt.Println("No deployments found or invalid response format")
		return nil
	}

	fmt.Printf("Found %d deployments:\n", len(items))
	for i, item := range items {
		deployment := item.(map[string]interface{})
		metadata := deployment["metadata"].(map[string]interface{})
		name := metadata["name"].(string)
		namespace := metadata["namespace"].(string)

		spec := deployment["spec"].(map[string]interface{})
		replicas := spec["replicas"].(float64)

		fmt.Printf("  %d. %s/%s (Replicas: %.0f)\n", i+1, namespace, name, replicas)
	}

	return nil
}

// getClusterInfo demonstrates getting cluster information via the K8s API proxy over HTTP
func getClusterInfo(client *HTTPClient) error {
	// Make HTTP request to K8s API via FRP tunnel
	resp, err := client.makeRequest("GET", "/api/v1", nil)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var apiInfo map[string]interface{}
	if err := json.Unmarshal(body, &apiInfo); err != nil {
		return fmt.Errorf("failed to parse API info: %w", err)
	}

	// Display cluster version and supported APIs
	if kind, ok := apiInfo["kind"].(string); ok {
		fmt.Printf("API Kind: %s\n", kind)
	}

	if resources, ok := apiInfo["resources"].([]interface{}); ok {
		fmt.Printf("Available API resources: %d\n", len(resources))

		// Show first few resources as examples
		for i, resource := range resources {
			if i >= 5 { // Show only first 5
				fmt.Printf("... and %d more resources\n", len(resources)-5)
				break
			}

			if res, ok := resource.(map[string]interface{}); ok {
				if name, exists := res["name"].(string); exists {
					fmt.Printf("  - %s\n", name)
				}
			}
		}
	}

	return nil
}
