package ingress

import (
	"context"
	"fmt"
	"strings"

	"github.com/pipeops/pipeops-vm-agent/internal/helm"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GatewayInstaller installs the PipeOps env-aware TCP gateway chart via HelmInstaller
type GatewayInstaller struct {
	installer *helm.HelmInstaller
	logger    *logrus.Logger
}

// GatewayOptions captures install-time options for the gateway
type GatewayOptions struct {
	// Environment mode
	// "managed" or "single-vm". If empty, best-effort auto-detect.
	EnvironmentMode string
	// If single-vm and this is set, will be used as LB IP; otherwise best-effort detect from node
	VMIP string

	// Which implementation(s) to enable
	IstioEnabled      bool
	GatewayAPIEnabled bool

	ReleaseName string
	Namespace   string

	// Istio-specific
	// If true, create a dedicated LB Service in istio ingress namespace and pin IP accordingly
	IstioCreateLBService  bool
	IstioServiceNamespace string            // typically "istio-system"
	IstioGatewaySelector  map[string]string // defaults to {"istio":"ingressgateway"}
	// Istio servers and routes (minimal fields for TCP)
	IstioServers   []IstioServer
	IstiotcpRoutes []TCPRouteVS
	IstioTLSRoutes []TLSRouteVS

	// Gateway API-specific
	GatewayClassName string // defaults to "istio"
	GatewayListeners []GatewayListener
	GatewayTcpRoutes []GatewayAPITCPRoute
	GatewayUdpRoutes []GatewayAPIUDPRoute
}

type IstioServer struct {
	PortNumber   int
	PortName     string
	PortProtocol string // TCP or TLS
	Hosts        []string
	TLS          map[string]interface{} // e.g., {mode: PASSTHROUGH}
}

type TCPRouteVS struct {
	Name     string
	Port     int
	DestHost string
	DestPort int
}

type TLSRouteVS struct {
	Name     string
	Port     int // optional in match; 0 to omit
	SNIHosts []string
	DestHost string
	DestPort int
}

type GatewayListener struct {
	Name     string
	Port     int
	Protocol string // TCP
}

type GatewayAPITCPRoute struct {
	Name        string
	SectionName string // matches listener name
	BackendRefs []BackendRef
}

type BackendRef struct {
	Name      string
	Namespace string
	Port      int
}

type GatewayAPIUDPRoute struct {
	Name        string
	SectionName string
	BackendRefs []BackendRef
}

func NewGatewayInstaller(installer *helm.HelmInstaller, logger *logrus.Logger) *GatewayInstaller {
	return &GatewayInstaller{installer: installer, logger: logger}
}

// Install installs or upgrades the pipeops-gateway chart with environment-aware values.
func (gi *GatewayInstaller) Install(ctx context.Context, opts GatewayOptions) error {
	if opts.ReleaseName == "" {
		opts.ReleaseName = "pipeops-gateway"
	}
	if opts.Namespace == "" {
		opts.Namespace = "pipeops-system"
	}

	mode, ip := gi.resolveEnvironment(ctx, opts.EnvironmentMode, opts.VMIP)

	// Normalize options (fill defaults, generate names)
	gi.normalize(&opts)

	// Validate configuration before proceeding
	if err := gi.validate(opts); err != nil {
		return fmt.Errorf("gateway config validation failed: %w", err)
	}

	values := map[string]interface{}{
		"environment": map[string]interface{}{
			"mode": mode,
			"vmIP": ip,
		},
		"istio": map[string]interface{}{
			"enabled": opts.IstioEnabled,
		},
		"gatewayApi": map[string]interface{}{
			"enabled": opts.GatewayAPIEnabled,
		},
	}

	if opts.IstioEnabled {
		selector := opts.IstioGatewaySelector
		if len(selector) == 0 {
			selector = map[string]string{"istio": "ingressgateway"}
		}
		servers := make([]map[string]interface{}, 0, len(opts.IstioServers))
		for _, s := range opts.IstioServers {
			entry := map[string]interface{}{
				"port": map[string]interface{}{
					"number":   s.PortNumber,
					"name":     s.PortName,
					"protocol": s.PortProtocol,
				},
			}
			if len(s.Hosts) > 0 {
				entry["hosts"] = s.Hosts
			}
			if s.TLS != nil {
				entry["tls"] = s.TLS
			}
			servers = append(servers, entry)
		}
		tcpRoutes := make([]map[string]interface{}, 0, len(opts.IstiotcpRoutes))
		for _, r := range opts.IstiotcpRoutes {
			tcpRoutes = append(tcpRoutes, map[string]interface{}{
				"name": r.Name,
				"port": r.Port,
				"destination": map[string]interface{}{
					"host": r.DestHost,
					"port": r.DestPort,
				},
			})
		}
		tlsRoutes := make([]map[string]interface{}, 0, len(opts.IstioTLSRoutes))
		for _, r := range opts.IstioTLSRoutes {
			match := map[string]interface{}{
				"sniHosts": r.SNIHosts,
			}
			if r.Port > 0 {
				match["port"] = r.Port
			}
			tlsRoutes = append(tlsRoutes, map[string]interface{}{
				"name":     r.Name,
				"port":     r.Port,
				"sniHosts": r.SNIHosts,
				"destination": map[string]interface{}{
					"host": r.DestHost,
					"port": r.DestPort,
				},
			})
			_ = match // values.yaml shape includes port inside match; we flatten for simplicity here
		}

		istioVals := values["istio"].(map[string]interface{})
		istioVals["gateway"] = map[string]interface{}{
			"selector": selector,
			"servers":  servers,
		}
		istioVals["virtualService"] = map[string]interface{}{
			"tcpRoutes": tcpRoutes,
			"tlsRoutes": tlsRoutes,
		}
		istioVals["service"] = map[string]interface{}{
			"create":                opts.IstioCreateLBService,
			"namespace":             firstNonEmpty(opts.IstioServiceNamespace, "istio-system"),
			"externalTrafficPolicy": "Local",
		}
	}

	if opts.GatewayAPIEnabled {
		listeners := make([]map[string]interface{}, 0, len(opts.GatewayListeners))
		for _, l := range opts.GatewayListeners {
			listeners = append(listeners, map[string]interface{}{
				"name":     l.Name,
				"port":     l.Port,
				"protocol": firstNonEmpty(l.Protocol, "TCP"),
			})
		}
		routes := make([]map[string]interface{}, 0, len(opts.GatewayTcpRoutes))
		for _, r := range opts.GatewayTcpRoutes {
			backs := make([]map[string]interface{}, 0, len(r.BackendRefs))
			for _, b := range r.BackendRefs {
				backs = append(backs, map[string]interface{}{
					"name":      b.Name,
					"namespace": b.Namespace,
					"port":      b.Port,
				})
			}
			routes = append(routes, map[string]interface{}{
				"name":        r.Name,
				"sectionName": r.SectionName,
				"backendRefs": backs,
			})
		}
		gwapiVals := values["gatewayApi"].(map[string]interface{})
		gwapiVals["gateway"] = map[string]interface{}{
			"gatewayClassName": firstNonEmpty(opts.GatewayClassName, "istio"),
			"listeners":        listeners,
		}
		gwapiVals["tcpRoutes"] = routes

		// UDP routes
		if len(opts.GatewayUdpRoutes) > 0 {
			uroutes := make([]map[string]interface{}, 0, len(opts.GatewayUdpRoutes))
			for _, r := range opts.GatewayUdpRoutes {
				backs := make([]map[string]interface{}, 0, len(r.BackendRefs))
				for _, b := range r.BackendRefs {
					backs = append(backs, map[string]interface{}{
						"name":      b.Name,
						"namespace": b.Namespace,
						"port":      b.Port,
					})
				}
				uroutes = append(uroutes, map[string]interface{}{
					"name":        r.Name,
					"sectionName": r.SectionName,
					"backendRefs": backs,
				})
			}
			gwapiVals["udpRoutes"] = uroutes
		}
	}

	// Install local chart from filesystem (copied into image under /helm)
	release := &helm.HelmRelease{
		Name:      opts.ReleaseName,
		Namespace: opts.Namespace,
		Chart:     "/helm/pipeops-gateway",
		Repo:      "", // local path
		Version:   "", // chart version inside Chart.yaml
		Values:    values,
	}

	gi.logger.WithFields(logrus.Fields{
		"release":    release.Name,
		"namespace":  release.Namespace,
		"env_mode":   mode,
		"vm_ip":      ip,
		"istio":      opts.IstioEnabled,
		"gatewayAPI": opts.GatewayAPIEnabled,
	}).Info("Installing PipeOps Gateway via Helm")

	if err := gi.installer.Install(ctx, release); err != nil {
		return fmt.Errorf("failed to install pipeops-gateway: %w", err)
	}

	gi.logger.Info("✓ PipeOps Gateway installed/updated")
	return nil
}

// normalize fills in sensible defaults where safe (e.g., listener protocol/name)
func (gi *GatewayInstaller) normalize(opts *GatewayOptions) {
	if opts == nil {
		return
	}
	if opts.GatewayAPIEnabled {
		for i := range opts.GatewayListeners {
			// Default protocol to TCP if empty
			p := strings.TrimSpace(opts.GatewayListeners[i].Protocol)
			if p == "" {
				p = "TCP"
				opts.GatewayListeners[i].Protocol = p
			}
			// Auto-name listeners if missing: <proto>-<port>
			n := strings.TrimSpace(opts.GatewayListeners[i].Name)
			if n == "" {
				protoLower := strings.ToLower(p)
				if opts.GatewayListeners[i].Port > 0 {
					n = fmt.Sprintf("%s-%d", protoLower, opts.GatewayListeners[i].Port)
				} else {
					n = fmt.Sprintf("%s-listener", protoLower)
				}
				opts.GatewayListeners[i].Name = n
			}
		}
	}
	if opts.IstioEnabled {
		for i := range opts.IstioServers {
			// Default Istio server protocol to TCP if empty
			if strings.TrimSpace(opts.IstioServers[i].PortProtocol) == "" {
				opts.IstioServers[i].PortProtocol = "TCP"
			}
		}
	}
}

// validate performs preflight checks on the requested gateway options
func (gi *GatewayInstaller) validate(opts GatewayOptions) error {
	var problems []string

	if !opts.IstioEnabled && !opts.GatewayAPIEnabled {
		problems = append(problems, "enable at least one controller: gatewayApi.enabled or istio.enabled")
	}

	// Gateway API validation
	if opts.GatewayAPIEnabled {
		// Build listener maps
		lProto := make(map[string]string)
		if len(opts.GatewayListeners) == 0 {
			problems = append(problems, "gatewayApi: at least one listener is required")
		}
		nameSeen := make(map[string]struct{})
		for _, l := range opts.GatewayListeners {
			name := strings.TrimSpace(l.Name)
			if name == "" {
				problems = append(problems, "gatewayApi: listener.name must not be empty")
				continue
			}
			if _, dup := nameSeen[name]; dup {
				problems = append(problems, fmt.Sprintf("gatewayApi: duplicate listener name %q", name))
			}
			nameSeen[name] = struct{}{}
			proto := strings.ToUpper(strings.TrimSpace(l.Protocol))
			if proto == "" {
				proto = "TCP"
			}
			if proto != "TCP" && proto != "UDP" {
				problems = append(problems, fmt.Sprintf("gatewayApi: listener %q has invalid protocol %q (must be TCP or UDP)", name, l.Protocol))
			}
			if l.Port <= 0 {
				problems = append(problems, fmt.Sprintf("gatewayApi: listener %q port must be > 0", name))
			}
			lProto[name] = proto
		}

		// TCP routes -> must point to existing TCP listener
		for _, r := range opts.GatewayTcpRoutes {
			if strings.TrimSpace(r.Name) == "" {
				problems = append(problems, "gatewayApi: tcpRoute.name is required")
			}
			sec := strings.TrimSpace(r.SectionName)
			if sec == "" {
				problems = append(problems, fmt.Sprintf("gatewayApi: tcpRoute %q missing sectionName", r.Name))
			} else if proto, ok := lProto[sec]; !ok {
				problems = append(problems, fmt.Sprintf("gatewayApi: tcpRoute %q references unknown listener %q", r.Name, sec))
			} else if proto != "TCP" {
				problems = append(problems, fmt.Sprintf("gatewayApi: tcpRoute %q sectionName %q must reference a TCP listener (got %s)", r.Name, sec, proto))
			}
			if len(r.BackendRefs) == 0 {
				problems = append(problems, fmt.Sprintf("gatewayApi: tcpRoute %q requires at least one backendRef", r.Name))
			}
			for i, b := range r.BackendRefs {
				if strings.TrimSpace(b.Name) == "" {
					problems = append(problems, fmt.Sprintf("gatewayApi: tcpRoute %q backendRefs[%d].name is required", r.Name, i))
				}
				if b.Port <= 0 {
					problems = append(problems, fmt.Sprintf("gatewayApi: tcpRoute %q backendRefs[%d].port must be > 0", r.Name, i))
				}
			}
		}

		// UDP routes -> must point to existing UDP listener
		for _, r := range opts.GatewayUdpRoutes {
			if strings.TrimSpace(r.Name) == "" {
				problems = append(problems, "gatewayApi: udpRoute.name is required")
			}
			sec := strings.TrimSpace(r.SectionName)
			if sec == "" {
				problems = append(problems, fmt.Sprintf("gatewayApi: udpRoute %q missing sectionName", r.Name))
			} else if proto, ok := lProto[sec]; !ok {
				problems = append(problems, fmt.Sprintf("gatewayApi: udpRoute %q references unknown listener %q", r.Name, sec))
			} else if proto != "UDP" {
				problems = append(problems, fmt.Sprintf("gatewayApi: udpRoute %q sectionName %q must reference a UDP listener (got %s)", r.Name, sec, proto))
			}
			if len(r.BackendRefs) == 0 {
				problems = append(problems, fmt.Sprintf("gatewayApi: udpRoute %q requires at least one backendRef", r.Name))
			}
			for i, b := range r.BackendRefs {
				if strings.TrimSpace(b.Name) == "" {
					problems = append(problems, fmt.Sprintf("gatewayApi: udpRoute %q backendRefs[%d].name is required", r.Name, i))
				}
				if b.Port <= 0 {
					problems = append(problems, fmt.Sprintf("gatewayApi: udpRoute %q backendRefs[%d].port must be > 0", r.Name, i))
				}
			}
		}
	}

	// Istio validation (minimal)
	if opts.IstioEnabled {
		for _, s := range opts.IstioServers {
			if s.PortNumber <= 0 || strings.TrimSpace(s.PortName) == "" {
				problems = append(problems, "istio: server.port.number and port.name are required")
			}
			proto := strings.ToUpper(strings.TrimSpace(s.PortProtocol))
			if proto != "TCP" && proto != "TLS" {
				problems = append(problems, fmt.Sprintf("istio: server %q invalid protocol %q (must be TCP or TLS)", s.PortName, s.PortProtocol))
			}
		}
		for _, r := range opts.IstiotcpRoutes {
			if r.Port <= 0 {
				problems = append(problems, fmt.Sprintf("istio: tcpRoute %q port must be > 0", r.Name))
			}
			if strings.TrimSpace(r.DestHost) == "" || r.DestPort <= 0 {
				problems = append(problems, fmt.Sprintf("istio: tcpRoute %q destination.host and destination.port are required", r.Name))
			}
		}
		for _, r := range opts.IstioTLSRoutes {
			if len(r.SNIHosts) == 0 {
				problems = append(problems, fmt.Sprintf("istio: tlsRoute %q requires at least one sniHost", r.Name))
			}
			if strings.TrimSpace(r.DestHost) == "" || r.DestPort <= 0 {
				problems = append(problems, fmt.Sprintf("istio: tlsRoute %q destination.host and destination.port are required", r.Name))
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func (gi *GatewayInstaller) resolveEnvironment(ctx context.Context, mode, explicitIP string) (string, string) {
	// If mode explicitly provided, honor it and use explicitIP if set
	if mode != "" {
		if mode == "single-vm" {
			return mode, gi.chooseNodeIP(ctx, explicitIP)
		}
		return mode, ""
	}

	// Auto-detect: treat k3s or single-node clusters as single-vm
	detectedMode := "managed"
	var ip string

	if gi.installer != nil && gi.installer.K8sClient != nil {
		if sv, err := gi.installer.K8sClient.Discovery().ServerVersion(); err == nil {
			if strings.Contains(strings.ToLower(sv.GitVersion), "k3s") || strings.Contains(strings.ToLower(sv.String()), "k3s") {
				detectedMode = "single-vm"
			}
		}

		if detectedMode != "single-vm" {
			if nodes, err := gi.installer.K8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
				if len(nodes.Items) == 1 {
					detectedMode = "single-vm"
				}
			}
		}

		if detectedMode == "single-vm" {
			ip = gi.chooseNodeIP(ctx, explicitIP)
		}
	}

	return detectedMode, ip
}

func (gi *GatewayInstaller) chooseNodeIP(ctx context.Context, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if gi.installer == nil || gi.installer.K8sClient == nil {
		return ""
	}
	nodes, err := gi.installer.K8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		return ""
	}
	// Prefer ExternalIP, then InternalIP
	pickIP := func(n corev1.Node) string {
		var internal string
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeExternalIP && addr.Address != "" {
				return addr.Address
			}
			if addr.Type == corev1.NodeInternalIP && internal == "" {
				internal = addr.Address
			}
		}
		return internal
	}
	// Try Ready node first
	for _, n := range nodes.Items {
		if isNodeReady(&n) {
			if ip := pickIP(n); ip != "" {
				return ip
			}
		}
	}
	// Fallback to first node
	return pickIP(nodes.Items[0])
}

func isNodeReady(n *corev1.Node) bool {
	if n == nil {
		return false
	}
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func firstNonEmpty(val string, def string) string {
	if val != "" {
		return val
	}
	return def
}
