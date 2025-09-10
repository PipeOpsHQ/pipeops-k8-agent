package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
)

// RunnerExample demonstrates how a Runner would connect to the VM Agent
// and use the K8s API proxy functionality
func main() {
	// VM Agent endpoint (normally this would come from Control Plane)
	agentEndpoint := "ws://localhost:8080/ws/k8s"

	// Connect to the VM Agent
	conn, err := connectToAgent(agentEndpoint)
	if err != nil {
		log.Fatalf("Failed to connect to VM Agent: %v", err)
	}
	defer conn.Close()

	fmt.Println("Connected to VM Agent K8s API proxy")

	// Example 1: List all pods in default namespace
	fmt.Println("\n=== Example 1: List Pods ===")
	if err := listPods(conn); err != nil {
		log.Printf("Failed to list pods: %v", err)
	}

	// Example 2: Get deployments
	fmt.Println("\n=== Example 2: List Deployments ===")
	if err := listDeployments(conn); err != nil {
		log.Printf("Failed to list deployments: %v", err)
	}

	// Example 3: Watch pods (streaming example)
	fmt.Println("\n=== Example 3: Watch Pods (5 seconds) ===")
	if err := watchPods(conn, 5*time.Second); err != nil {
		log.Printf("Failed to watch pods: %v", err)
	}

	fmt.Println("\nRunner example completed")
}

// connectToAgent establishes WebSocket connection to the VM Agent
func connectToAgent(endpoint string) (*websocket.Conn, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	// In a real implementation, this would include authentication headers
	header := make(map[string][]string)
	header["Authorization"] = []string{"Bearer <runner-token>"}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	return conn, nil
}

// listPods demonstrates listing pods via the K8s API proxy
func listPods(conn *websocket.Conn) error {
	request := &types.K8sProxyRequest{
		ID:     fmt.Sprintf("list-pods-%d", time.Now().UnixNano()),
		Method: "GET",
		Path:   "/api/v1/namespaces/default/pods",
		Headers: map[string]string{
			"Accept": "application/json",
		},
	}

	// Send request
	if err := conn.WriteJSON(request); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	var response types.K8sProxyResponse
	if err := conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if response.StatusCode != 200 {
		return fmt.Errorf("API request failed with status %d: %s", response.StatusCode, response.Error)
	}

	// Parse and display results
	var podList map[string]interface{}
	if err := json.Unmarshal(response.Body, &podList); err != nil {
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

// listDeployments demonstrates listing deployments via the K8s API proxy
func listDeployments(conn *websocket.Conn) error {
	request := &types.K8sProxyRequest{
		ID:     fmt.Sprintf("list-deployments-%d", time.Now().UnixNano()),
		Method: "GET",
		Path:   "/apis/apps/v1/namespaces/default/deployments",
		Headers: map[string]string{
			"Accept": "application/json",
		},
	}

	// Send request
	if err := conn.WriteJSON(request); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	var response types.K8sProxyResponse
	if err := conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if response.StatusCode != 200 {
		return fmt.Errorf("API request failed with status %d: %s", response.StatusCode, response.Error)
	}

	// Parse and display results
	var deploymentList map[string]interface{}
	if err := json.Unmarshal(response.Body, &deploymentList); err != nil {
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

// watchPods demonstrates watching pods via the K8s API proxy (streaming)
func watchPods(conn *websocket.Conn, duration time.Duration) error {
	// Prepare watch request
	queryParams := map[string][]string{
		"watch": {"true"},
	}

	request := &types.K8sProxyRequest{
		ID:     fmt.Sprintf("watch-pods-%d", time.Now().UnixNano()),
		Method: "GET",
		Path:   "/api/v1/namespaces/default/pods",
		Headers: map[string]string{
			"Accept": "application/json",
		},
		Query:  queryParams,
		Stream: true,
	}

	// Send watch request
	if err := conn.WriteJSON(request); err != nil {
		return fmt.Errorf("failed to send watch request: %w", err)
	}

	// Read initial response (should contain stream ID)
	var initResponse types.K8sProxyResponse
	if err := conn.ReadJSON(&initResponse); err != nil {
		return fmt.Errorf("failed to read initial response: %w", err)
	}

	if initResponse.StatusCode != 200 {
		return fmt.Errorf("watch request failed with status %d: %s", initResponse.StatusCode, initResponse.Error)
	}

	if !initResponse.Stream {
		return fmt.Errorf("expected streaming response")
	}

	streamID := initResponse.StreamID
	fmt.Printf("Started watching pods (Stream ID: %s)\n", streamID)

	// Set up timeout
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Cancel the stream when done
	defer func() {
		cancelRequest := &types.K8sProxyRequest{
			ID:       fmt.Sprintf("cancel-%d", time.Now().UnixNano()),
			Method:   "CANCEL",
			StreamID: streamID,
		}
		conn.WriteJSON(cancelRequest)
	}()

	// Read streaming events
	eventCount := 0
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("Watch completed. Received %d events.\n", eventCount)
			return nil
		default:
			// Set read deadline to avoid blocking forever
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))

			var response types.K8sProxyResponse
			err := conn.ReadJSON(&response)
			if err != nil {
				// Check if it's a timeout (expected during watch)
				if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
					continue
				}
				return fmt.Errorf("failed to read streaming response: %w", err)
			}

			// Reset read deadline
			conn.SetReadDeadline(time.Time{})

			if response.StreamID != streamID {
				continue // Not our stream
			}

			if response.Done {
				fmt.Println("Stream ended by server")
				return nil
			}

			if response.Chunk && len(response.Body) > 0 {
				// Parse watch event
				var event map[string]interface{}
				if err := json.Unmarshal(response.Body, &event); err != nil {
					fmt.Printf("Failed to parse watch event: %v\n", err)
					continue
				}

				eventType, ok := event["type"].(string)
				if !ok {
					continue
				}

				object, ok := event["object"].(map[string]interface{})
				if !ok {
					continue
				}

				metadata, ok := object["metadata"].(map[string]interface{})
				if !ok {
					continue
				}

				name, ok := metadata["name"].(string)
				if !ok {
					continue
				}

				eventCount++
				fmt.Printf("  Event %d: %s - Pod: %s\n", eventCount, eventType, name)
			}
		}
	}
}
