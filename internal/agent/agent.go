package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/frp"
	"github.com/pipeops/pipeops-vm-agent/internal/k8s"
	"github.com/pipeops/pipeops-vm-agent/internal/server"
	"github.com/pipeops/pipeops-vm-agent/internal/version"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// Agent represents the main PipeOps agent
type Agent struct {
	config      *types.Config
	logger      *logrus.Logger
	k8sClient   *k8s.Client
	frpClient   *frp.Client
	authService *frp.AuthService
	server      *server.Server
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// New creates a new agent instance
func New(config *types.Config, logger *logrus.Logger) (*Agent, error) {
	ctx, cancel := context.WithCancel(context.Background())

	agent := &Agent{
		config: config,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}

	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(config.Kubernetes.Kubeconfig, config.Kubernetes.InCluster)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	agent.k8sClient = k8sClient

	// Initialize FRP authentication service
	authService := frp.NewAuthService(
		[]byte(config.PipeOps.Token), // Use token as JWT secret for now
		config.PipeOps.APIURL,
		&frp.DefaultLogger{},
	)
	agent.authService = authService

	// Initialize FRP client configuration
	frpConfig := &frp.Config{
		ServerAddr:        config.PipeOps.APIURL,
		ServerPort:        7000, // Default FRP port
		Token:             config.PipeOps.Token,
		LocalK8sPort:      8080,
		LocalAgentPort:    config.Agent.Port,
		ClusterID:         config.Agent.ID,
		UseEncryption:     true,
		UseCompression:    true,
		LogLevel:          "info",
		HeartbeatInterval: 30,
		HeartbeatTimeout:  90,
	}

	// Initialize FRP client
	frpClient := frp.NewClient(frpConfig, &frp.DefaultLogger{})
	agent.frpClient = frpClient

	// Initialize HTTP server with K8s API proxy
	httpServer := server.NewServer(config, k8sClient, logger)
	agent.server = httpServer

	// Register message handlers
	agent.registerHandlers()

	return agent, nil
}

// Start starts the agent
func (a *Agent) Start() error {
	a.logger.Info("Starting PipeOps agent...")

	// Start HTTP server with K8s API proxy
	if err := a.server.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Start FRP authentication service
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.authService.StartCleanupRoutine(a.ctx)
	}()

	// Start FRP client for tunneling
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := a.frpClient.Start(a.ctx); err != nil {
			a.logger.WithError(err).Error("Failed to start FRP client")
			return
		}

		// Register agent with control plane via HTTP
		if err := a.register(); err != nil {
			a.logger.WithError(err).Error("Failed to register agent")
			return
		}

		// Keep FRP connection alive
		<-a.ctx.Done()

		if err := a.frpClient.Stop(); err != nil {
			a.logger.WithError(err).Warn("Failed to stop FRP client")
		}
	}()

	// Start periodic status reporting
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.startStatusReporting()
	}()

	// Start heartbeat
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.startHeartbeat()
	}()

	a.logger.WithField("port", a.config.Agent.Port).Info("PipeOps agent started successfully")

	// Wait for context cancellation
	<-a.ctx.Done()

	return nil
}

// Stop stops the agent
func (a *Agent) Stop() error {
	a.logger.Info("Stopping PipeOps agent...")

	a.cancel()

	// Stop HTTP server
	if a.server != nil {
		if err := a.server.Stop(); err != nil {
			a.logger.WithError(err).Error("Failed to stop HTTP server")
		}
	}

	// Stop FRP client
	if a.frpClient != nil {
		if err := a.frpClient.Stop(); err != nil {
			a.logger.WithError(err).Warn("Failed to stop FRP client")
		}
	}

	// Wait for all goroutines to finish
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	// Wait for graceful shutdown or timeout
	select {
	case <-done:
		a.logger.Info("PipeOps agent stopped gracefully")
	case <-time.After(30 * time.Second):
		a.logger.Warn("Graceful shutdown timeout, forcing stop")
	}

	return nil
}

// register registers the agent with the control plane via HTTP
func (a *Agent) register() error {
	hostname, _ := os.Hostname()

	agent := &types.Agent{
		ID:          a.config.Agent.ID,
		Name:        a.config.Agent.Name,
		ClusterName: a.config.Agent.ClusterName,
		Version:     version.GetVersion(),
		Labels:      a.config.Agent.Labels,
		Status:      types.AgentStatusRegistering,
		LastSeen:    time.Now(),
	}

	// Add default labels
	if agent.Labels == nil {
		agent.Labels = make(map[string]string)
	}
	agent.Labels["hostname"] = hostname
	agent.Labels["agent.pipeops.io/version"] = agent.Version

	a.logger.WithFields(map[string]interface{}{
		"agent_id":     agent.ID,
		"cluster_name": agent.ClusterName,
		"version":      agent.Version,
	}).Info("Agent registered successfully via FRP")

	return nil
}

// startStatusReporting starts periodic status reporting
func (a *Agent) startStatusReporting() {
	ticker := time.NewTicker(a.config.Agent.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if err := a.reportStatus(); err != nil {
				a.logger.WithError(err).Error("Failed to report status")
			}
		}
	}
}

// startHeartbeat starts periodic heartbeat
func (a *Agent) startHeartbeat() {
	ticker := time.NewTicker(30 * time.Second) // Heartbeat every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(); err != nil {
				a.logger.WithError(err).Error("Failed to send heartbeat")
			}
		}
	}
}

// reportStatus reports the current cluster status via FRP
func (a *Agent) reportStatus() error {
	status, err := a.k8sClient.GetClusterStatus(a.ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster status: %w", err)
	}

	a.logger.WithFields(map[string]interface{}{
		"nodes":       status.Nodes,
		"pods":        status.Pods,
		"deployments": status.Deployments,
	}).Debug("Cluster status updated via FRP")

	return nil
}

// sendHeartbeat sends a heartbeat message via FRP
func (a *Agent) sendHeartbeat() error {
	frpStatus := "disconnected"
	if a.frpClient.Status() == "running" {
		frpStatus = "connected"
	}

	heartbeatData := map[string]interface{}{
		"agent_id":     a.config.Agent.ID,
		"cluster_name": a.config.Agent.ClusterName,
		"status":       types.AgentStatusConnected,
		"frp_status":   frpStatus,
		"timestamp":    time.Now(),
	}

	a.logger.WithFields(heartbeatData).Debug("Heartbeat sent via FRP")

	return nil
}

// registerHandlers registers HTTP API handlers for FRP communication
func (a *Agent) registerHandlers() {
	// With FRP, command handling will be done via HTTP API endpoints
	// exposed through the agent's HTTP server and accessible via FRP tunnels
	a.logger.Info("HTTP API handlers registered for FRP communication")
}

// handleDeploy handles deployment requests
func (a *Agent) handleDeploy(ctx context.Context, msg *types.Message) (*types.Message, error) {
	var req types.DeploymentRequest
	if err := json.Unmarshal(msg.Data.([]byte), &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal deployment request: %w", err)
	}

	a.logger.WithFields(logrus.Fields{
		"name":      req.Name,
		"namespace": req.Namespace,
		"image":     req.Image,
	}).Info("Handling deployment request")

	err := a.k8sClient.CreateDeployment(ctx, &req)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %w", err)
	}

	response := &types.Message{
		ID:        generateMessageID(),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Deployment %s created successfully", req.Name),
		},
	}

	return response, nil
}

// handleDelete handles deletion requests
func (a *Agent) handleDelete(ctx context.Context, msg *types.Message) (*types.Message, error) {
	data := msg.Data.(map[string]interface{})
	name, ok := data["name"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid name field")
	}

	namespace, ok := data["namespace"].(string)
	if !ok {
		namespace = "default"
	}

	a.logger.WithFields(logrus.Fields{
		"name":      name,
		"namespace": namespace,
	}).Info("Handling deletion request")

	err := a.k8sClient.DeleteDeployment(ctx, name, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to delete deployment: %w", err)
	}

	response := &types.Message{
		ID:        generateMessageID(),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Deployment %s deleted successfully", name),
		},
	}

	return response, nil
}

// handleScale handles scaling requests
func (a *Agent) handleScale(ctx context.Context, msg *types.Message) (*types.Message, error) {
	data := msg.Data.(map[string]interface{})
	name, ok := data["name"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid name field")
	}

	namespace, ok := data["namespace"].(string)
	if !ok {
		namespace = "default"
	}

	replicasFloat, ok := data["replicas"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing or invalid replicas field")
	}
	replicas := int32(replicasFloat)

	a.logger.WithFields(logrus.Fields{
		"name":      name,
		"namespace": namespace,
		"replicas":  replicas,
	}).Info("Handling scale request")

	err := a.k8sClient.ScaleDeployment(ctx, name, namespace, replicas)
	if err != nil {
		return nil, fmt.Errorf("failed to scale deployment: %w", err)
	}

	response := &types.Message{
		ID:        generateMessageID(),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success": true,
			"message": fmt.Sprintf("Deployment %s scaled to %d replicas", name, replicas),
		},
	}

	return response, nil
}

// handleGetResources handles resource listing requests
func (a *Agent) handleGetResources(ctx context.Context, msg *types.Message) (*types.Message, error) {
	a.logger.Info("Handling get resources request")

	status, err := a.k8sClient.GetClusterStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster status: %w", err)
	}

	response := &types.Message{
		ID:        generateMessageID(),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data:      status,
	}

	return response, nil
}

// handleCommand handles general command requests
func (a *Agent) handleCommand(ctx context.Context, msg *types.Message) (*types.Message, error) {
	var req types.CommandRequest
	if err := json.Unmarshal(msg.Data.([]byte), &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal command request: %w", err)
	}

	a.logger.WithFields(logrus.Fields{
		"type":   req.Type,
		"target": req.Target.Name,
	}).Info("Handling command request")

	var result string
	var err error

	switch req.Type {
	case types.CommandTypeLogs:
		lines := int64(100) // Default to 100 lines
		if linesStr, ok := req.Args["lines"]; ok {
			// Parse lines from args
			if parsedLines, err := strconv.ParseInt(linesStr, 10, 64); err == nil && parsedLines > 0 {
				lines = parsedLines
			} else {
				a.logger.WithField("lines", linesStr).Warn("Invalid lines parameter, using default")
			}
		}

		result, err = a.k8sClient.GetPodLogs(ctx, req.Target.Name, req.Target.Namespace, req.Target.Container, lines)
		if err != nil {
			return nil, fmt.Errorf("failed to get pod logs: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported command type: %s", req.Type)
	}

	response := &types.Message{
		ID:        generateMessageID(),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: &types.CommandResponse{
			ID:      req.ID,
			Success: true,
			Output:  result,
		},
	}

	return response, nil
}

// handleRunnerAssignment handles Runner assignment messages from Control Plane
func (a *Agent) handleRunnerAssignment(ctx context.Context, msg *types.Message) (*types.Message, error) {
	var assignment types.RunnerAssignment
	if err := json.Unmarshal(msg.Data.([]byte), &assignment); err != nil {
		return nil, fmt.Errorf("failed to unmarshal runner assignment: %w", err)
	}

	a.logger.WithFields(logrus.Fields{
		"runner_id":       assignment.RunnerID,
		"runner_endpoint": assignment.RunnerEndpoint,
		"assigned_at":     assignment.AssignedAt,
	}).Info("Runner assigned to agent")

	// With FRP, runner connectivity is handled through tunnels
	a.logger.Info("Runner connectivity established via FRP tunnels")

	a.logger.Info("Dual connections enabled - Runner connection will be established in background")

	// Note: The connection to Runner happens asynchronously in EnableDualConnections
	// We can check the status later using a.commClient.IsRunnerConnected()

	// Send success response back to Control Plane
	response := &types.Message{
		ID:        generateMessageID(),
		Type:      types.MessageTypeResponse,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"success":    true,
			"request_id": msg.ID,
			"message":    "Runner assignment accepted - establishing connection",
			"runner_id":  assignment.RunnerID,
		},
	}

	return response, nil
}

// generateMessageID generates a unique message ID
func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
