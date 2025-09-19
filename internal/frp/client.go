package frp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"text/template"
	"time"
)

// Config represents the FRP client configuration
type Config struct {
	ServerAddr        string            `yaml:"server_addr"`
	ServerPort        int               `yaml:"server_port"`
	Token             string            `yaml:"token"`
	LocalK8sPort      int               `yaml:"local_k8s_port"`
	LocalAgentPort    int               `yaml:"local_agent_port"`
	ClusterID         string            `yaml:"cluster_id"`
	Tunnels           map[string]Tunnel `yaml:"tunnels"`
	UseEncryption     bool              `yaml:"use_encryption"`
	UseCompression    bool              `yaml:"use_compression"`
	LogLevel          string            `yaml:"log_level"`
	HeartbeatInterval int               `yaml:"heartbeat_interval"`
	HeartbeatTimeout  int               `yaml:"heartbeat_timeout"`
}

// Tunnel represents a single FRP tunnel configuration
type Tunnel struct {
	Type       string `yaml:"type"` // tcp, http, https
	LocalPort  int    `yaml:"local_port"`
	RemotePort int    `yaml:"remote_port,omitempty"`
	Domain     string `yaml:"domain,omitempty"`
}

// Client manages the FRP client process and configuration
type Client struct {
	config     *Config
	process    *exec.Cmd
	status     string
	logFile    *os.File
	configPath string
	mu         sync.RWMutex
	stopCh     chan struct{}
	log        Logger
}

// NewClient creates a new FRP client instance
func NewClient(config *Config, log Logger) *Client {
	return &Client{
		config: config,
		status: "stopped",
		stopCh: make(chan struct{}),
		log:    log,
	}
}

// Start starts the FRP client process
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status == "running" {
		return fmt.Errorf("FRP client is already running")
	}

	// Generate FRP configuration file
	configPath, err := c.generateConfig()
	if err != nil {
		return fmt.Errorf("failed to generate FRP config: %w", err)
	}
	c.configPath = configPath

	// Set up logging
	logPath := filepath.Join(os.TempDir(), fmt.Sprintf("frpc-%s.log", c.config.ClusterID))
	c.logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	// Start FRP client process
	c.process = exec.CommandContext(ctx, "frpc", "-c", configPath)
	c.process.Stdout = c.logFile
	c.process.Stderr = c.logFile

	if err := c.process.Start(); err != nil {
		return fmt.Errorf("failed to start FRP client: %w", err)
	}

	c.status = "running"
	c.log.Info("FRP client started successfully",
		"cluster_id", c.config.ClusterID,
		"server", fmt.Sprintf("%s:%d", c.config.ServerAddr, c.config.ServerPort),
		"config_path", configPath,
		"log_path", logPath,
	)

	// Start monitoring goroutine
	go c.monitorProcess(ctx)

	return nil
}

// Stop stops the FRP client process
func (c *Client) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status == "stopped" {
		return nil
	}

	close(c.stopCh)

	if c.process != nil {
		if err := c.process.Process.Kill(); err != nil {
			c.log.Error("Failed to kill FRP process", "error", err)
		} else {
			c.log.Info("FRP process killed successfully")
		}
		c.process.Wait()
		c.process = nil
	}

	if c.logFile != nil {
		c.logFile.Close()
		c.logFile = nil
	}

	// Clean up config file
	if c.configPath != "" {
		os.Remove(c.configPath)
		c.configPath = ""
	}

	c.status = "stopped"
	c.log.Info("FRP client stopped")
	return nil
}

// Status returns the current status of the FRP client
func (c *Client) Status() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// IsRunning returns true if the FRP client is running
func (c *Client) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status == "running"
}

// UpdateToken updates the FRP token and restarts the client
func (c *Client) UpdateToken(newToken string) error {
	c.mu.Lock()
	c.config.Token = newToken
	c.mu.Unlock()

	if c.IsRunning() {
		c.log.Info("Updating FRP token, restarting client")
		ctx := context.Background()
		if err := c.Stop(); err != nil {
			return fmt.Errorf("failed to stop FRP client: %w", err)
		}
		time.Sleep(2 * time.Second) // Brief pause before restart
		return c.Start(ctx)
	}

	return nil
}

// GetConfig returns a copy of the current configuration
func (c *Client) GetConfig() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return *c.config
}

// monitorProcess monitors the FRP process and handles restarts
func (c *Client) monitorProcess(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.checkProcess(ctx)
		}
	}
}

// checkProcess checks if the FRP process is still running
func (c *Client) checkProcess(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.process == nil {
		return
	}

	// Check if process has exited
	if c.process.ProcessState != nil && c.process.ProcessState.Exited() {
		c.log.Warn("FRP process has exited, attempting restart",
			"exit_code", c.process.ProcessState.ExitCode())

		c.status = "restarting"

		// Attempt to restart
		go func() {
			time.Sleep(5 * time.Second) // Wait before restart
			if err := c.restart(ctx); err != nil {
				c.log.Error("Failed to restart FRP client", "error", err)
				c.mu.Lock()
				c.status = "failed"
				c.mu.Unlock()
			}
		}()
	}
}

// restart restarts the FRP client
func (c *Client) restart(ctx context.Context) error {
	c.log.Info("Restarting FRP client")

	// Clean up old process
	if c.logFile != nil {
		c.logFile.Close()
		c.logFile = nil
	}

	// Set up new logging
	logPath := filepath.Join(os.TempDir(), fmt.Sprintf("frpc-%s.log", c.config.ClusterID))
	var err error
	c.logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	// Start new process
	c.process = exec.CommandContext(ctx, "frpc", "-c", c.configPath)
	c.process.Stdout = c.logFile
	c.process.Stderr = c.logFile

	if err := c.process.Start(); err != nil {
		return fmt.Errorf("failed to restart FRP client: %w", err)
	}

	c.status = "running"
	c.log.Info("FRP client restarted successfully")
	return nil
}

// generateConfig generates the FRP client configuration file
func (c *Client) generateConfig() (string, error) {
	configTemplate := `[common]
server_addr = {{.ServerAddr}}
server_port = {{.ServerPort}}
token = {{.Token}}
{{if .UseEncryption}}use_encryption = true{{end}}
{{if .UseCompression}}use_compression = true{{end}}
{{if .LogLevel}}log_level = {{.LogLevel}}{{else}}log_level = info{{end}}
{{if .HeartbeatInterval}}heartbeat_interval = {{.HeartbeatInterval}}{{else}}heartbeat_interval = 30{{end}}
{{if .HeartbeatTimeout}}heartbeat_timeout = {{.HeartbeatTimeout}}{{else}}heartbeat_timeout = 90{{end}}

# Kubernetes API tunnel
[k8s-api-{{.ClusterID}}]
type = tcp
local_ip = 127.0.0.1
local_port = {{.LocalK8sPort}}
remote_port = 0
use_encryption = true
use_compression = true

# Agent HTTP API tunnel  
[agent-api-{{.ClusterID}}]
type = http
local_port = {{.LocalAgentPort}}
custom_domains = agent-{{.ClusterID}}.pipeops.io
use_encryption = true

{{range $name, $tunnel := .Tunnels}}
# Custom tunnel: {{$name}}
[{{$name}}-{{$.ClusterID}}]
type = {{$tunnel.Type}}
local_port = {{$tunnel.LocalPort}}
{{if $tunnel.RemotePort}}remote_port = {{$tunnel.RemotePort}}{{end}}
{{if $tunnel.Domain}}custom_domains = {{$tunnel.Domain}}{{end}}
use_encryption = true
{{end}}
`

	tmpl, err := template.New("frpc").Parse(configTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	configPath := filepath.Join(os.TempDir(), fmt.Sprintf("frpc-%s.ini", c.config.ClusterID))
	file, err := os.Create(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, c.config); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return configPath, nil
}
