# Gateway API and Istio Setup

This guide covers the installation and configuration of Kubernetes Gateway API (experimental) and Istio with alpha gateway API support for the PipeOps agent.

## Overview

The PipeOps agent supports TCP/UDP port exposure via:
- **Istio Gateway/VirtualService** - Traditional Istio networking
- **Kubernetes Gateway API** - Modern, standardized approach

When using Gateway API with Istio, you must enable experimental Gateway API features and configure Istio with alpha gateway API support.

## Prerequisites

- Kubernetes cluster (1.19+)
- kubectl configured
- Helm 3.2.0+
- istioctl (for Istio installation)

## Gateway API Installation

### Install Gateway API Experimental CRDs

The Gateway API experimental installation includes support for TCPRoute and UDPRoute resources which are required for Layer 4 (TCP/UDP) traffic:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.3.0/experimental-install.yaml
```

This installs:
- Gateway API Core CRDs (v1)
- Experimental CRDs including:
  - `TCPRoute` (v1alpha2)
  - `UDPRoute` (v1alpha2)
  - `TLSRoute` (v1alpha2)
  - `GRPCRoute` (v1alpha2)

**Why Experimental?**

The experimental channel is required because TCPRoute and UDPRoute are still in alpha/beta stage and not yet part of the standard Gateway API release. The PipeOps agent uses these resources to route TCP/UDP traffic for services.

### Verify Installation

```bash
# Check Gateway API CRDs are installed
kubectl get crd | grep gateway

# Expected output includes:
# gateways.gateway.networking.k8s.io
# gatewayclasses.gateway.networking.k8s.io
# httproutes.gateway.networking.k8s.io
# tcproutes.gateway.networking.k8s.io
# udproutes.gateway.networking.k8s.io
```

## Istio Installation with Alpha Gateway API

### Install Istio with Gateway API Support

Istio must be configured with the `PILOT_ENABLE_ALPHA_GATEWAY_API` feature flag to support Gateway API resources:

```bash
istioctl install -y \
  --set "components.ingressGateways[0].name=istio-ingressgateway" \
  --set "components.ingressGateways[0].enabled=false" \
  --set "components.ingressGateways[0].k8s.service.type=ClusterIP" \
  --set "components.ingressGateways[0].k8s.service.externalTrafficPolicy=Local" \
  --set "values.pilot.env.PILOT_ENABLE_ALPHA_GATEWAY_API=true"
```

**Configuration Breakdown:**

- `PILOT_ENABLE_ALPHA_GATEWAY_API=true` - Enables Gateway API support in Istio control plane
- `ingressGateways[0].enabled=false` - Disables default Istio IngressGateway (we'll use Gateway API instead)
- `k8s.service.type=ClusterIP` - Sets service type for the gateway
- `k8s.service.externalTrafficPolicy=Local` - Preserves client source IP

### Alternative: IstioOperator Configuration

For declarative installation, create an IstioOperator resource:

```yaml
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
metadata:
  namespace: istio-system
  name: pipeops-istio
spec:
  profile: default
  components:
    ingressGateways:
      - name: istio-ingressgateway
        enabled: false
        k8s:
          service:
            type: ClusterIP
            externalTrafficPolicy: Local
  values:
    pilot:
      env:
        PILOT_ENABLE_ALPHA_GATEWAY_API: true
```

Apply with:

```bash
kubectl create namespace istio-system
istioctl install -f istio-operator.yaml -y
```

### Verify Istio Installation

```bash
# Check Istio is running
kubectl get pods -n istio-system

# Verify Gateway API support is enabled
kubectl get configmap istio -n istio-system -o yaml | grep PILOT_ENABLE_ALPHA_GATEWAY_API
```

## PipeOps Agent Configuration

### Using Gateway API

Configure the PipeOps agent to use Gateway API in your `values.yaml`:

```yaml
agent:
  gateway:
    enabled: true
    environment:
      mode: managed  # or single-vm for K3s/single node
      # vmIP: "192.168.1.100"  # Required for single-vm mode
    
    gatewayApi:
      enabled: true
      gateway:
        name: pipeops-gateway
        gatewayClassName: istio  # Must match installed GatewayClass
        listeners:
          - name: tcp-ssh
            port: 2222
            protocol: TCP
          - name: tcp-custom
            port: 5000
            protocol: TCP
      
      tcpRoutes:
        - name: ssh-route
          sectionName: tcp-ssh
          backendRefs:
            - name: ssh-service
              namespace: default
              port: 22
        - name: app-route
          sectionName: tcp-custom
          backendRefs:
            - name: app-service
              namespace: default
              port: 5000
```

### Using Traditional Istio Gateway

If you prefer traditional Istio Gateway/VirtualService:

```yaml
agent:
  gateway:
    enabled: true
    
    istio:
      enabled: true
      gateway:
        name: pipeops-istio-gateway
        selector:
          istio: ingressgateway
        servers:
          - port:
              number: 2222
              name: tcp-ssh
              protocol: TCP
            hosts:
              - "*"
          - port:
              number: 5000
              name: tcp-app
              protocol: TCP
      
      virtualService:
        tcpRoutes:
          - port: 2222
            destination:
              host: ssh-service.default.svc.cluster.local
              port: 22
          - port: 5000
            destination:
              host: app-service.default.svc.cluster.local
              port: 5000
```

## Deploy the Agent

Install with Helm:

```bash
helm install pipeops-agent ./helm/pipeops-agent \
  --set agent.pipeops.token="your-token" \
  --set agent.cluster.name="your-cluster" \
  -f gateway-values.yaml
```

Or upgrade existing installation:

```bash
helm upgrade pipeops-agent ./helm/pipeops-agent \
  -f gateway-values.yaml
```

## Verification

### Check Gateway Resources

```bash
# List Gateways
kubectl get gateway -A

# Check Gateway status
kubectl describe gateway pipeops-gateway -n pipeops-system

# List TCPRoutes
kubectl get tcproute -A

# Check route status
kubectl describe tcproute ssh-route -n pipeops-system
```

### Test Connectivity

```bash
# Get the Gateway external IP/hostname
kubectl get gateway pipeops-gateway -n pipeops-system -o jsonpath='{.status.addresses[0].value}'

# Test TCP connection (replace with actual IP)
nc -zv <gateway-ip> 2222
nc -zv <gateway-ip> 5000
```

## Troubleshooting

### Gateway API CRDs Not Found

If you see errors about TCPRoute or UDPRoute not being found:

```bash
# Verify experimental CRDs are installed
kubectl get crd tcproutes.gateway.networking.k8s.io
kubectl get crd udproutes.gateway.networking.k8s.io

# Reinstall if missing
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.3.0/experimental-install.yaml
```

### Istio Not Recognizing Gateway API

If Istio doesn't create resources for Gateway API objects:

```bash
# Check if alpha feature is enabled
kubectl logs -n istio-system deployment/istiod | grep PILOT_ENABLE_ALPHA_GATEWAY_API

# If not found, reinstall Istio with the flag
istioctl install -y --set "values.pilot.env.PILOT_ENABLE_ALPHA_GATEWAY_API=true"
```

### Gateway Not Getting IP Address

```bash
# Check Gateway events
kubectl describe gateway pipeops-gateway -n pipeops-system

# Check if GatewayClass exists and is supported
kubectl get gatewayclass

# Verify Istio GatewayClass
kubectl get gatewayclass istio -o yaml
```

### Routes Not Working

```bash
# Check TCPRoute status
kubectl get tcproute -A -o wide

# Check route events
kubectl describe tcproute <route-name> -n pipeops-system

# Verify backend services exist
kubectl get svc -A

# Check Istio proxy logs
kubectl logs -n istio-system deployment/istiod
```

## Production Considerations

### Gateway Class Selection

The `gatewayClassName` must match an installed GatewayClass:

```bash
# List available GatewayClasses
kubectl get gatewayclass

# Common values:
# - istio (when using Istio)
# - nginx (when using NGINX Gateway)
# - kong (when using Kong)
```

### Resource Limits

Gateway resources consume cluster resources. For production:

```yaml
agent:
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 250m
      memory: 256Mi
```

### High Availability

For production workloads, consider:
- Multiple Gateway replicas
- LoadBalancer service type
- Health checks on backend services
- Resource quotas and limits

### Security

- Use TLS for encrypted traffic
- Implement network policies
- Restrict Gateway to specific namespaces
- Use RBAC for Gateway resources

## References

- [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/)
- [Gateway API Experimental Features](https://gateway-api.sigs.k8s.io/guides/migrating-from-ingress/#experimental-channel)
- [Istio Gateway API Documentation](https://istio.io/latest/docs/tasks/traffic-management/ingress/gateway-api/)
- [Istio Installation Guide](https://istio.io/latest/docs/setup/install/istioctl/)
- [TCPRoute Specification](https://gateway-api.sigs.k8s.io/references/spec/#gateway.networking.k8s.io/v1alpha2.TCPRoute)

## Next Steps

- [Configure monitoring for gateways](monitoring.md)
- [Set up TLS certificates](../getting-started/configuration.md)
- [Configure custom domains](../getting-started/management.md)
