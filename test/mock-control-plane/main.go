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

type MockControlPlane struct {
	clients map[string]*websocket.Conn
}

func NewMockControlPlane() *MockControlPlane {
	return &MockControlPlane{
		clients: make(map[string]*websocket.Conn),
	}
}

func (cp *MockControlPlane) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("New agent connected from %s", r.RemoteAddr)

	// Handle messages from agent
	for {
		var msg types.Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Printf("Failed to read message: %v", err)
			break
		}

		log.Printf("Received message: Type=%s, ID=%s", msg.Type, msg.ID)

		// Handle different message types
		switch msg.Type {
		case types.MessageTypeRegister:
			cp.handleRegistration(conn, &msg)
		case types.MessageTypeHeartbeat:
			cp.handleHeartbeat(conn, &msg)
		case types.MessageTypeStatus:
			cp.handleStatus(conn, &msg)
		default:
			log.Printf("Unknown message type: %s", msg.Type)
		}
	}

	log.Printf("Agent disconnected")
}

func (cp *MockControlPlane) handleRegistration(conn *websocket.Conn, msg *types.Message) {
	log.Printf("Agent registration received")

	// Send registration acknowledgment
	response := &types.Message{
		ID:        fmt.Sprintf("reg-ack-%d", time.Now().UnixNano()),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success":    true,
			"message":    "Registration successful",
			"request_id": msg.ID,
		},
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("Failed to send registration response: %v", err)
		return
	}

	log.Printf("Registration acknowledged")

	// Simulate Runner assignment after 3 seconds
	go func() {
		time.Sleep(3 * time.Second)
		cp.sendRunnerAssignment(conn)
	}()
}

func (cp *MockControlPlane) sendRunnerAssignment(conn *websocket.Conn) {
	log.Printf("Sending Runner assignment")

	assignment := types.RunnerAssignment{
		RunnerID:       "mock-runner-001",
		RunnerEndpoint: "ws://localhost:9090/ws",
		RunnerToken:    "mock-runner-token-123",
		AssignedAt:     time.Now(),
	}

	assignmentData, _ := json.Marshal(assignment)

	msg := &types.Message{
		ID:        fmt.Sprintf("runner-assign-%d", time.Now().UnixNano()),
		Type:      types.MessageTypeRunnerAssigned,
		Timestamp: time.Now(),
		Data:      assignmentData,
	}

	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("Failed to send runner assignment: %v", err)
		return
	}

	log.Printf("Runner assignment sent")
}

func (cp *MockControlPlane) handleHeartbeat(conn *websocket.Conn, msg *types.Message) {
	// Parse heartbeat data
	data, _ := json.Marshal(msg.Data)
	log.Printf("Heartbeat received: %s", string(data))

	// Send heartbeat acknowledgment
	response := &types.Message{
		ID:        fmt.Sprintf("hb-ack-%d", time.Now().UnixNano()),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success":    true,
			"request_id": msg.ID,
		},
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("Failed to send heartbeat response: %v", err)
	}
}

func (cp *MockControlPlane) handleStatus(conn *websocket.Conn, msg *types.Message) {
	// Parse status data
	data, _ := json.Marshal(msg.Data)
	log.Printf("Status received: %s", string(data))

	// Send status acknowledgment
	response := &types.Message{
		ID:        fmt.Sprintf("status-ack-%d", time.Now().UnixNano()),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success":    true,
			"request_id": msg.ID,
		},
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("Failed to send status response: %v", err)
	}
}

func main() {
	cp := NewMockControlPlane()

	http.HandleFunc("/ws", cp.handleWebSocket)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Mock Control Plane OK"))
	})

	fmt.Println("🚀 Mock Control Plane starting on :8081")
	fmt.Println("WebSocket endpoint: ws://localhost:8081/ws")
	fmt.Println("Health endpoint: http://localhost:8081/health")
	fmt.Println()
	fmt.Println("Test sequence:")
	fmt.Println("1. Agent connects")
	fmt.Println("2. Agent registers")
	fmt.Println("3. Control Plane acknowledges")
	fmt.Println("4. Control Plane assigns Runner (after 3s)")
	fmt.Println("5. Agent establishes dual connections")
	fmt.Println()

	log.Fatal(http.ListenAndServe(":8081", nil))
}
