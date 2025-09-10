package communication

import (
	"context"
	"testing"
	"time"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

func TestClient_DualConnections(t *testing.T) {
	config := &types.PipeOpsConfig{
		APIURL:  "ws://localhost:8081/ws",
		Token:   "test-token",
		Timeout: 30 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise in tests

	client := NewClient(config, logger)

	// Test initial state
	if client.IsConnected() {
		t.Error("Client should not be connected initially")
	}

	if client.IsControlPlaneConnected() {
		t.Error("Control Plane should not be connected initially")
	}

	if client.IsRunnerConnected() {
		t.Error("Runner should not be connected initially")
	}

	// Test dual connections setup (without actual connection)
	err := client.EnableDualConnections("ws://localhost:9090/ws", "runner-token")
	if err != nil {
		t.Errorf("EnableDualConnections should succeed even without Control Plane connection: %v", err)
	}

	// The runner connection will be attempted in background, but won't succeed in tests
	// This is expected behavior - the method enables dual mode regardless of current connection state
}

func TestClient_MessageHandlers(t *testing.T) {
	config := &types.PipeOpsConfig{
		APIURL:  "ws://localhost:8081/ws",
		Token:   "test-token",
		Timeout: 30 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	client := NewClient(config, logger)

	// Test handler registration
	handlerCalled := false
	testHandler := MessageHandlerFunc(func(ctx context.Context, msg *types.Message) (*types.Message, error) {
		handlerCalled = true
		return nil, nil
	})

	client.RegisterHandler(types.MessageTypeDeploy, testHandler)

	// Test handler retrieval
	handler := client.handlers[types.MessageTypeDeploy]
	if handler == nil {
		t.Error("Handler should be registered")
	}

	// Test handler execution (simulate)
	msg := &types.Message{
		Type: types.MessageTypeDeploy,
		Data: []byte(`{"test": true}`),
	}

	_, err := handler.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("Handler execution failed: %v", err)
	}

	if !handlerCalled {
		t.Error("Handler should have been called")
	}
}

func TestMessageEndpointRouting(t *testing.T) {
	// Test endpoint constants
	if types.EndpointControlPlane != "control_plane" {
		t.Errorf("Expected EndpointControlPlane to be 'control_plane', got '%s'", types.EndpointControlPlane)
	}

	if types.EndpointRunner != "runner" {
		t.Errorf("Expected EndpointRunner to be 'runner', got '%s'", types.EndpointRunner)
	}
}

func TestMessageTypes(t *testing.T) {
	expectedTypes := map[types.MessageType]string{
		types.MessageTypeRegister:       "register",
		types.MessageTypeHeartbeat:      "heartbeat",
		types.MessageTypeRunnerAssigned: "runner_assigned",
		types.MessageTypeDeploy:         "deploy",
		types.MessageTypeScale:          "scale",
		types.MessageTypeResponse:       "response",
	}

	for msgType, expected := range expectedTypes {
		if string(msgType) != expected {
			t.Errorf("Expected %s to be '%s', got '%s'", msgType, expected, string(msgType))
		}
	}
}
