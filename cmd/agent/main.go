package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pipeops/pipeops-vm-agent/internal/agent"
	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	configFile string
	logLevel   string
	version    = "dev" // Set during build
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "pipeops-agent",
	Short: "PipeOps Kubernetes Agent",
	Long: `PipeOps Kubernetes Agent connects your k3s cluster to the PipeOps control plane,
enabling secure management and deployment without exposing the Kubernetes API.`,
	RunE: runAgent,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Persistent flags
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "config file (default is $HOME/.pipeops-agent.yaml)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")

	// Local flags
	rootCmd.Flags().String("pipeops-url", "", "PipeOps API URL")
	rootCmd.Flags().String("token", "", "PipeOps authentication token")
	rootCmd.Flags().String("cluster-name", "", "Name of this cluster")
	rootCmd.Flags().String("agent-id", "", "Unique identifier for this agent")
	rootCmd.Flags().String("kubeconfig", "", "Path to kubeconfig file")
	rootCmd.Flags().Bool("in-cluster", false, "Run in cluster mode")

	// Bind flags to viper
	if err := viper.BindPFlag("pipeops.api_url", rootCmd.Flags().Lookup("pipeops-url")); err != nil {
		logrus.Fatalf("Failed to bind pipeops-url flag: %v", err)
	}
	if err := viper.BindPFlag("pipeops.token", rootCmd.Flags().Lookup("token")); err != nil {
		logrus.Fatalf("Failed to bind token flag: %v", err)
	}
	if err := viper.BindPFlag("agent.cluster_name", rootCmd.Flags().Lookup("cluster-name")); err != nil {
		logrus.Fatalf("Failed to bind cluster-name flag: %v", err)
	}
	if err := viper.BindPFlag("agent.id", rootCmd.Flags().Lookup("agent-id")); err != nil {
		logrus.Fatalf("Failed to bind agent-id flag: %v", err)
	}
	if err := viper.BindPFlag("kubernetes.kubeconfig", rootCmd.Flags().Lookup("kubeconfig")); err != nil {
		logrus.Fatalf("Failed to bind kubeconfig flag: %v", err)
	}
	if err := viper.BindPFlag("kubernetes.in_cluster", rootCmd.Flags().Lookup("in-cluster")); err != nil {
		logrus.Fatalf("Failed to bind in-cluster flag: %v", err)
	}
}

// initConfig reads in config file and ENV variables
func initConfig() {
	if configFile != "" {
		viper.SetConfigFile(configFile)
	} else {
		// Find config in home directory
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
			os.Exit(1)
		}

		viper.AddConfigPath(home)
		viper.AddConfigPath("/etc/pipeops")
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".pipeops-agent")
	}

	// Environment variables
	// Note: Not using SetEnvPrefix since env vars already have PIPEOPS_ prefix
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Explicitly bind environment variables to config keys
	viper.BindEnv("pipeops.api_url", "PIPEOPS_API_URL")
	viper.BindEnv("pipeops.token", "PIPEOPS_TOKEN")
	viper.BindEnv("agent.cluster_name", "PIPEOPS_CLUSTER_NAME")
	viper.BindEnv("agent.id", "PIPEOPS_AGENT_ID")

	// Read config file
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}

// runAgent is the main function that runs the agent
func runAgent(cmd *cobra.Command, args []string) error {
	// Setup logger
	logger := setupLogger()

	// Load configuration
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate configuration
	if err := validateConfig(config); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"version":      version,
		"cluster_name": config.Agent.ClusterName,
		"agent_id":     config.Agent.ID,
	}).Info("Starting PipeOps Agent")

	// Create and start agent
	agentInstance, err := agent.New(config, logger)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start agent in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- agentInstance.Start()
	}()

	// Wait for signal or error
	select {
	case sig := <-sigChan:
		logger.WithField("signal", sig).Info("Received shutdown signal")
		return agentInstance.Stop()
	case err := <-errChan:
		if err != nil {
			logger.WithError(err).Error("Agent stopped with error")
			return err
		}
		logger.Info("Agent stopped")
		return nil
	}
}

// setupLogger configures the logger
func setupLogger() *logrus.Logger {
	logger := logrus.New()

	// Set log level
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)

	// Set formatter
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
	})

	return logger
}

// loadConfig loads the configuration from various sources
func loadConfig() (*types.Config, error) {
	config := &types.Config{}

	// Set defaults
	setDefaults()

	// Unmarshal into config struct
	if err := viper.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Note: Agent ID generation moved to agent.New() for persistence support
	// Don't generate agent ID here - let the agent package handle it

	return config, nil
}

// setDefaults sets default configuration values
func setDefaults() {
	// Agent defaults
	viper.SetDefault("agent.labels", map[string]string{})
	viper.SetDefault("agent.port", 8080)
	viper.SetDefault("agent.debug", false)
	viper.SetDefault("agent.version", version)

	// PipeOps defaults
	viper.SetDefault("pipeops.timeout", "30s")
	viper.SetDefault("pipeops.reconnect.enabled", true)
	viper.SetDefault("pipeops.reconnect.max_attempts", 10)
	viper.SetDefault("pipeops.reconnect.interval", "5s")
	viper.SetDefault("pipeops.reconnect.backoff", "5s")
	viper.SetDefault("pipeops.tls.enabled", true)
	viper.SetDefault("pipeops.tls.insecure_skip_verify", false)

	// Kubernetes defaults
	viper.SetDefault("kubernetes.in_cluster", false)
	viper.SetDefault("kubernetes.namespace", "pipeops-system")

	// Logging defaults
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")
	viper.SetDefault("logging.output", "stdout")
}

// validateConfig validates the configuration
func validateConfig(config *types.Config) error {
	if config.PipeOps.APIURL == "" {
		return fmt.Errorf("PipeOps API URL is required")
	}

	if config.PipeOps.Token == "" {
		return fmt.Errorf("PipeOps token is required")
	}

	if config.Agent.ClusterName == "" {
		return fmt.Errorf("cluster name is required")
	}

	// Validate durations
	if config.PipeOps.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return nil
}
