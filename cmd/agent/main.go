package main

import (
	"encoding/json"
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

	// Gateway flags (top-level toggles and basic settings)
	rootCmd.Flags().Bool("gateway-enabled", false, "Enable env-aware TCP/UDP gateway")
	rootCmd.Flags().String("gateway-namespace", "pipeops-system", "Namespace for gateway Helm release")
	rootCmd.Flags().String("gateway-release-name", "pipeops-gateway", "Helm release name for gateway")
	rootCmd.Flags().String("gateway-env-mode", "", "Environment mode: managed|single-vm (auto if empty)")
	rootCmd.Flags().String("gateway-env-vm-ip", "", "VM IP to use when mode=single-vm (optional)")

	// Gateway API controller settings
	rootCmd.Flags().Bool("gateway-gwapi-enabled", true, "Enable Kubernetes Gateway API for gateway")
	rootCmd.Flags().String("gateway-gwapi-gateway-class", "istio", "GatewayClass name (e.g., istio)")

	// Istio controller settings (optional)
	rootCmd.Flags().Bool("gateway-istio-enabled", false, "Enable Istio Gateway/VirtualService for gateway")
	rootCmd.Flags().Bool("gateway-istio-service-create", false, "Create LoadBalancer Service targeting istio ingress")
	rootCmd.Flags().String("gateway-istio-service-namespace", "istio-system", "Namespace for istio ingress service")

	// JSON flags for inline route/listener definitions (advanced)
	rootCmd.Flags().String("gateway-gwapi-listeners-json", "", "JSON array of Gateway API listeners [{name,port,protocol}]")
	rootCmd.Flags().String("gateway-gwapi-tcp-routes-json", "", "JSON array of Gateway API TCP routes [{name,sectionName,backendRefs:[{name,namespace,port}]}]")
	rootCmd.Flags().String("gateway-gwapi-udp-routes-json", "", "JSON array of Gateway API UDP routes [{name,sectionName,backendRefs:[{name,namespace,port}]}]")
	rootCmd.Flags().String("gateway-istio-servers-json", "", "JSON array of Istio servers [{port:{number,name,protocol},hosts:[...],tls:{...}}]")
	rootCmd.Flags().String("gateway-istio-tcp-routes-json", "", "JSON array of Istio TCP routes [{name,port,destination:{host,port}}]")
	rootCmd.Flags().String("gateway-istio-tls-routes-json", "", "JSON array of Istio TLS routes [{name,port,sniHosts:[...],destination:{host,port}}]")

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

	// Bind gateway flags
	_ = viper.BindPFlag("gateway.enabled", rootCmd.Flags().Lookup("gateway-enabled"))
	_ = viper.BindPFlag("gateway.namespace", rootCmd.Flags().Lookup("gateway-namespace"))
	_ = viper.BindPFlag("gateway.release_name", rootCmd.Flags().Lookup("gateway-release-name"))
	_ = viper.BindPFlag("gateway.environment.mode", rootCmd.Flags().Lookup("gateway-env-mode"))
	_ = viper.BindPFlag("gateway.environment.vm_ip", rootCmd.Flags().Lookup("gateway-env-vm-ip"))
	_ = viper.BindPFlag("gateway.gateway_api.enabled", rootCmd.Flags().Lookup("gateway-gwapi-enabled"))
	_ = viper.BindPFlag("gateway.gateway_api.gateway_class", rootCmd.Flags().Lookup("gateway-gwapi-gateway-class"))
	_ = viper.BindPFlag("gateway.istio.enabled", rootCmd.Flags().Lookup("gateway-istio-enabled"))
	_ = viper.BindPFlag("gateway.istio.service.create", rootCmd.Flags().Lookup("gateway-istio-service-create"))
	_ = viper.BindPFlag("gateway.istio.service.namespace", rootCmd.Flags().Lookup("gateway-istio-service-namespace"))

	// Bind JSON flags to viper keys
	_ = viper.BindPFlag("gateway.gateway_api._json.listeners", rootCmd.Flags().Lookup("gateway-gwapi-listeners-json"))
	_ = viper.BindPFlag("gateway.gateway_api._json.tcp_routes", rootCmd.Flags().Lookup("gateway-gwapi-tcp-routes-json"))
	_ = viper.BindPFlag("gateway.gateway_api._json.udp_routes", rootCmd.Flags().Lookup("gateway-gwapi-udp-routes-json"))
	_ = viper.BindPFlag("gateway.istio._json.servers", rootCmd.Flags().Lookup("gateway-istio-servers-json"))
	_ = viper.BindPFlag("gateway.istio._json.tcp_routes", rootCmd.Flags().Lookup("gateway-istio-tcp-routes-json"))
	_ = viper.BindPFlag("gateway.istio._json.tls_routes", rootCmd.Flags().Lookup("gateway-istio-tls-routes-json"))
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
	viper.BindEnv("agent.grafana_sub_path", "PIPEOPS_AGENT_GRAFANA_SUB_PATH")
	viper.BindEnv("agent.enable_ingress_sync", "ENABLE_INGRESS_SYNC")
	viper.BindEnv("kubernetes.service_token", "PIPEOPS_K8S_SERVICE_TOKEN")
	viper.BindEnv("kubernetes.ca_cert_data", "PIPEOPS_K8S_CERT_DATA")

	// Gateway env overrides (no prefix, simple names)
	_ = viper.BindEnv("gateway.enabled", "GATEWAY_ENABLED")
	_ = viper.BindEnv("gateway.release_name", "GATEWAY_RELEASE_NAME")
	_ = viper.BindEnv("gateway.namespace", "GATEWAY_NAMESPACE")
	_ = viper.BindEnv("gateway.environment.mode", "GATEWAY_ENVIRONMENT_MODE")
	_ = viper.BindEnv("gateway.environment.vm_ip", "GATEWAY_ENVIRONMENT_VM_IP")
	_ = viper.BindEnv("gateway.istio.enabled", "GATEWAY_ISTIO_ENABLED")
	_ = viper.BindEnv("gateway.istio.service.create", "GATEWAY_ISTIO_SERVICE_CREATE")
	_ = viper.BindEnv("gateway.istio.service.namespace", "GATEWAY_ISTIO_SERVICE_NAMESPACE")
	_ = viper.BindEnv("gateway.gateway_api.enabled", "GATEWAY_GATEWAY_API_ENABLED")
	_ = viper.BindEnv("gateway.gateway_api.gateway_class", "GATEWAY_GATEWAY_API_GATEWAY_CLASS")

	// JSON env overrides
	_ = viper.BindEnv("gateway.gateway_api._json.listeners", "GATEWAY_GWAPI_LISTENERS_JSON")
	_ = viper.BindEnv("gateway.gateway_api._json.tcp_routes", "GATEWAY_GWAPI_TCP_ROUTES_JSON")
	_ = viper.BindEnv("gateway.gateway_api._json.udp_routes", "GATEWAY_GWAPI_UDP_ROUTES_JSON")
	_ = viper.BindEnv("gateway.istio._json.servers", "GATEWAY_ISTIO_SERVERS_JSON")
	_ = viper.BindEnv("gateway.istio._json.tcp_routes", "GATEWAY_ISTIO_TCP_ROUTES_JSON")
	_ = viper.BindEnv("gateway.istio._json.tls_routes", "GATEWAY_ISTIO_TLS_ROUTES_JSON")

	// Read config file
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}

	// Parse JSON overrides, if provided
	type jsonSetter func() error
	setters := []jsonSetter{
		func() error {
			raw := viper.GetString("gateway.gateway_api._json.listeners")
			if raw == "" {
				return nil
			}
			var v []types.GatewayListenerCfg
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("invalid GATEWAY GWAPI listeners JSON: %w", err)
			}
			viper.Set("gateway.gateway_api.listeners", v)
			return nil
		},
		func() error {
			raw := viper.GetString("gateway.gateway_api._json.tcp_routes")
			if raw == "" {
				return nil
			}
			var v []types.GatewayTCPRouteCfg
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("invalid GATEWAY GWAPI TCP routes JSON: %w", err)
			}
			viper.Set("gateway.gateway_api.tcp_routes", v)
			return nil
		},
		func() error {
			raw := viper.GetString("gateway.gateway_api._json.udp_routes")
			if raw == "" {
				return nil
			}
			var v []types.GatewayUDPRouteCfg
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("invalid GATEWAY GWAPI UDP routes JSON: %w", err)
			}
			viper.Set("gateway.gateway_api.udp_routes", v)
			return nil
		},
		func() error {
			raw := viper.GetString("gateway.istio._json.servers")
			if raw == "" {
				return nil
			}
			var v []types.IstioServerSpec
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("invalid GATEWAY ISTIO servers JSON: %w", err)
			}
			viper.Set("gateway.istio.gateway.servers", v)
			return nil
		},
		func() error {
			raw := viper.GetString("gateway.istio._json.tcp_routes")
			if raw == "" {
				return nil
			}
			var v []types.TCPRouteVSConfig
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("invalid GATEWAY ISTIO TCP routes JSON: %w", err)
			}
			viper.Set("gateway.istio.virtual_service.tcp_routes", v)
			return nil
		},
		func() error {
			raw := viper.GetString("gateway.istio._json.tls_routes")
			if raw == "" {
				return nil
			}
			var v []types.TLSRouteVSConfig
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("invalid GATEWAY ISTIO TLS routes JSON: %w", err)
			}
			viper.Set("gateway.istio.virtual_service.tls_routes", v)
			return nil
		},
	}
	for _, s := range setters {
		if err := s(); err != nil {
			// Print to stderr; logger not initialized yet
			fmt.Fprintf(os.Stderr, "Gateway JSON override error: %v\n", err)
		}
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
	viper.SetDefault("agent.grafana_sub_path", true)
	viper.SetDefault("agent.enable_ingress_sync", true) // Enable ingress sync by default
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

	// Gateway defaults
	viper.SetDefault("gateway.enabled", false)
	viper.SetDefault("gateway.release_name", "pipeops-gateway")
	viper.SetDefault("gateway.namespace", "pipeops-system")
	viper.SetDefault("gateway.environment.mode", "")  // auto-detect
	viper.SetDefault("gateway.environment.vm_ip", "") // auto-detect if empty
	viper.SetDefault("gateway.istio.enabled", false)
	viper.SetDefault("gateway.istio.service.create", false)
	viper.SetDefault("gateway.istio.service.namespace", "istio-system")
	viper.SetDefault("gateway.istio.gateway.selector", map[string]string{"istio": "ingressgateway"})
	viper.SetDefault("gateway.gateway_api.enabled", false)
	viper.SetDefault("gateway.gateway_api.gateway_class", "istio")
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
