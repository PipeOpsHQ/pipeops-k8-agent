package tunnel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	chclient "github.com/jpillora/chisel/client"
	"github.com/sirupsen/logrus"
)

// Client wraps the Chisel client for reverse tunneling
type Client struct {
	chiselClient *chclient.Client
	config       *Config
	logger       *logrus.Logger
	tunnelOpen   bool
	mu           sync.Mutex
}

// Config represents tunnel configuration
type Config struct {
	// Tunnel server address (e.g., wss://tunnel.pipeops.io:443)
	ServerAddr string

	// SSH fingerprint for server verification
	ServerFingerprint string

	// List of port forwards (Portainer-style)
	Forwards []Forward

	// SSH credentials for tunnel authentication (username:password format)
	Credentials string
}

// Forward represents a single port forward
type Forward struct {
	Name       string // Friendly name (e.g., "kubernetes-api")
	LocalAddr  string // Local address (e.g., "localhost:6443")
	RemotePort string // Remote port on tunnel server
}

// NewClient creates a new Chisel tunnel client
func NewClient(config *Config, logger *logrus.Logger) *Client {
	return &Client{
		config: config,
		logger: logger,
	}
}

// CreateTunnel establishes a reverse tunnel to the control plane
func (c *Client) CreateTunnel(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tunnelOpen {
		return fmt.Errorf("tunnel already open")
	}

	if len(c.config.Forwards) == 0 {
		return fmt.Errorf("no port forwards configured")
	}

	// Build remote endpoint specifications (Portainer-style multiple forwards)
	// Format: R:remotePort:localAddr
	// Example: R:8000:localhost:6443 means "open port 8000 on server, forward to localhost:6443 on agent"
	var remotes []string
	for _, forward := range c.config.Forwards {
		remote := fmt.Sprintf("R:%s:%s", forward.RemotePort, forward.LocalAddr)
		remotes = append(remotes, remote)

		c.logger.WithFields(logrus.Fields{
			"name":        forward.Name,
			"remote_port": forward.RemotePort,
			"local_addr":  forward.LocalAddr,
		}).Info("Configuring port forward")
	}

	c.logger.WithFields(logrus.Fields{
		"forwards": len(c.config.Forwards),
		"server":   c.config.ServerAddr,
	}).Info("Creating reverse tunnel with multiple port forwards")

	// Ensure server URL uses correct scheme (wss:// or https://)
	serverURL := c.normalizeServerURL(c.config.ServerAddr)

	// Build Chisel client config with all port forwards
	chiselConfig := &chclient.Config{
		Server:      serverURL,
		Remotes:     remotes, // Multiple port forwards
		Fingerprint: c.config.ServerFingerprint,
		Auth:        c.config.Credentials,
		KeepAlive:   30 * time.Second,
	}

	// Create Chisel client
	chiselClient, err := chclient.NewClient(chiselConfig)
	if err != nil {
		return fmt.Errorf("failed to create Chisel client: %w", err)
	}

	c.chiselClient = chiselClient

	// Start the tunnel in background
	go func() {
		if err := chiselClient.Start(ctx); err != nil {
			c.logger.WithError(err).Error("Tunnel connection failed")
		}
	}()

	// Wait briefly for connection to establish
	time.Sleep(2 * time.Second)

	c.tunnelOpen = true
	c.logger.Info("Reverse tunnel established successfully")

	return nil
}

// CloseTunnel closes the active tunnel
func (c *Client) CloseTunnel() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.tunnelOpen {
		return nil // Already closed
	}

	c.logger.Info("Closing reverse tunnel")

	if c.chiselClient != nil {
		if err := c.chiselClient.Close(); err != nil {
			c.logger.WithError(err).Error("Error closing tunnel")
			return err
		}
	}

	c.tunnelOpen = false
	c.logger.Info("Reverse tunnel closed")
	return nil
}

// IsTunnelOpen returns true if the tunnel is currently open
func (c *Client) IsTunnelOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tunnelOpen
}

// normalizeServerURL ensures the server URL has the correct scheme for Chisel
func (c *Client) normalizeServerURL(url string) string {
	url = strings.TrimSpace(url)

	// If already has wss:// or https://, use it
	if strings.HasPrefix(url, "wss://") || strings.HasPrefix(url, "https://") {
		return url
	}

	// Replace http:// with https://
	if strings.HasPrefix(url, "http://") {
		return strings.Replace(url, "http://", "https://", 1)
	}

	// Replace ws:// with wss://
	if strings.HasPrefix(url, "ws://") {
		return strings.Replace(url, "ws://", "wss://", 1)
	}

	// No scheme, add https://
	return "https://" + url
}

// Wait blocks until the tunnel is closed or context is cancelled
func (c *Client) Wait(ctx context.Context) error {
	if c.chiselClient == nil {
		return fmt.Errorf("no active tunnel")
	}
	return c.chiselClient.Wait()
}
