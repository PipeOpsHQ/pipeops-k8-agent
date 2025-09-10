package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for testing
		return true
	},
}

type MockRunner struct {
	clients map[string]*websocket.Conn
}

func NewMockRunner() *MockRunner {
	return &MockRunner{
		clients: make(map[string]*websocket.Conn),
	}
}

func (r *MockRunner) handleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("New agent connected from %s", req.RemoteAddr)

	// Send a test deployment command after connection
	go func() {
		time.Sleep(2 * time.Second)
		r.sendTestDeployment(conn)
	}()

	// Handle messages from agent
	for {
		var msg types.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("Failed to read message: %v", err)
			break
		}

		log.Printf("Received message: Type=%s, ID=%s", msg.Type, msg.ID)

		// Handle responses from agent
		switch msg.Type {
		case types.MessageTypeResponse:
			r.handleResponse(conn, &msg)
		default:
			log.Printf("Received message type: %s", msg.Type)
		}
	}

	log.Printf("Agent disconnected")
}

func (r *MockRunner) sendTestDeployment(conn *websocket.Conn) {
	log.Printf("Sending test deployment command")

	deployment := types.DeploymentRequest{
		Name:      "test-nginx",
		Namespace: "default",
		Image:     "nginx:latest",
		Replicas:  1,
		Ports: []types.Port{
			{
				Name:          "http",
				ContainerPort: 80,
				Protocol:      "TCP",
			},
		},
		Environment: map[string]string{
			"ENV": "test",
		},
		Labels: map[string]string{
			"app":     "test-nginx",
			"version": "1.0.0",
		},
	}

	deploymentData, _ := json.Marshal(deployment)

	msg := &types.Message{
		ID:        fmt.Sprintf("deploy-%d", time.Now().UnixNano()),
		Type:      types.MessageTypeDeploy,
		Timestamp: time.Now(),
		Data:      deploymentData,
	}

	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("Failed to send deployment: %v", err)
		return
	}

	log.Printf("Test deployment sent")
}

func (r *MockRunner) handleResponse(conn *websocket.Conn, msg *types.Message) {
	// Parse response data
	data, _ := json.Marshal(msg.Data)
	log.Printf("Response received: %s", string(data))
}

func (r *MockRunner) sendTestOperations(conn *websocket.Conn) {
	operations := []struct {
		msgType types.MessageType
		data    interface{}
	}{
		{
			msgType: types.MessageTypeGetResources,
			data: map[string]interface{}{
				"namespace":     "default",
				"resource_type": "pods",
			},
		},
		{
			msgType: types.MessageTypeScale,
			data: map[string]interface{}{
				"name":      "test-nginx",
				"namespace": "default",
				"replicas":  3,
			},
		},
	}

	for i, op := range operations {
		time.Sleep(time.Duration(i+1) * 5 * time.Second)

		opData, _ := json.Marshal(op.data)
		msg := &types.Message{
			ID:        fmt.Sprintf("op-%d-%d", i, time.Now().UnixNano()),
			Type:      op.msgType,
			Timestamp: time.Now(),
			Data:      opData,
		}

		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("Failed to send operation %d: %v", i, err)
			continue
		}

		log.Printf("Operation %d sent: %s", i, op.msgType)
	}
}

func main() {
	runner := NewMockRunner()

	http.HandleFunc("/ws", runner.handleWebSocket)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Mock Runner OK"))
	})

	fmt.Println("🚀 Mock Runner starting on :9090")
	fmt.Println("WebSocket endpoint: ws://localhost:9090/ws")
	fmt.Println("Health endpoint: http://localhost:9090/health")
	fmt.Println()
	fmt.Println("Test sequence:")
	fmt.Println("1. Agent connects (after Control Plane assignment)")
	fmt.Println("2. Runner sends test deployment (after 2s)")
	fmt.Println("3. Runner sends additional operations")
	fmt.Println()

	log.Fatal(http.ListenAndServe(":9090", nil))
}
