# PipeOps Gateway Helm Chart

Environment-aware TCP ingress using either Istio Gateway/VirtualService or Kubernetes Gateway API (TCPRoute).

## What this chart provides

- Istio: Creates an Istio `Gateway` with configurable TCP/TLS servers and a matching `VirtualService` with `tcp` and `tls` routes.
- K8s Gateway API: Creates a `Gateway` with `TCPRoute` and `UDPRoute` objects.
- Env-aware exposure:
  - `environment.mode=managed`: rely on provider LoadBalancer IP assignment.
  - `environment.mode=single-vm`: treat LB IP as the VM/node IP. For Gateway API we set `spec.addresses`. For Istio you can optionally enable a dedicated `Service` that targets `istio-ingressgateway` and pins its IP to your VM via `loadBalancerIP`/`externalIPs`.

## Prerequisites

- Helm 3.2+
- One of:
  - Istio installed (ingress gateway running, usually in `istio-system`)
  - A Gateway API implementation (e.g., Istio, Contour, NGINX GW, etc.)

## Quick start

Istio mode (TCP to redis example):

```bash
helm install pipeops-gw ./helm/pipeops-gateway \
  --set istio.enabled=true \
  --set istio.gateway.servers[0].port.number=6379 \
  --set istio.gateway.servers[0].port.name=tcp-redis \
  --set istio.gateway.servers[0].port.protocol=TCP \
  --set istio.virtualService.tcpRoutes[0].name=redis \
  --set istio.virtualService.tcpRoutes[0].port=6379 \
  --set istio.virtualService.tcpRoutes[0].destination.host=redis.default.svc.cluster.local \
  --set istio.virtualService.tcpRoutes[0].destination.port=6379
```

Enable dedicated Istio LoadBalancer Service (optional):

```bash
helm upgrade --install pipeops-gw ./helm/pipeops-gateway \
  --set istio.enabled=true \
  --set istio.service.create=true \
  --set istio.service.namespace=istio-system
```

Gateway API mode (TCPRoute to redis):

```bash
helm install pipeops-gw ./helm/pipeops-gateway \
  --set gatewayApi.enabled=true \
  --set gatewayApi.gateway.listeners[0].name=tcp-redis \
  --set gatewayApi.gateway.listeners[0].port=6379 \
  --set gatewayApi.gateway.listeners[0].protocol=TCP \
  --set gatewayApi.tcpRoutes[0].name=redis \
  --set gatewayApi.tcpRoutes[0].sectionName=tcp-redis \
  --set gatewayApi.tcpRoutes[0].backendRefs[0].name=redis \
  --set gatewayApi.tcpRoutes[0].backendRefs[0].namespace=default \
  --set gatewayApi.tcpRoutes[0].backendRefs[0].port=6379

Gateway API mode (UDPRoute to CoreDNS on :53):

```bash
helm install pipeops-gw ./helm/pipeops-gateway \
  --set gatewayApi.enabled=true \
  --set gatewayApi.gateway.listeners[0].name=udp-dns \
  --set gatewayApi.gateway.listeners[0].port=53 \
  --set gatewayApi.gateway.listeners[0].protocol=UDP \
  --set gatewayApi.udpRoutes[0].name=dns \
  --set gatewayApi.udpRoutes[0].sectionName=udp-dns \
  --set gatewayApi.udpRoutes[0].backendRefs[0].name=coredns \
  --set gatewayApi.udpRoutes[0].backendRefs[0].namespace=kube-system \
  --set gatewayApi.udpRoutes[0].backendRefs[0].port=53
```
```

## Environment awareness

Set single VM (k3s) with explicit node IP:

```bash
--set environment.mode=single-vm \
--set environment.vmIP=192.0.2.10
```

- Istio mode: optional dedicated Service will use `loadBalancerIP` and `externalIPs` with the given VM IP.
- Gateway API: `Gateway.spec.addresses[0].value` will be set to your VM IP.

Managed cluster:

```bash
--set environment.mode=managed
```

## Notes

- For Istio TLS passthrough, configure a `server` with `protocol: TLS`, `tls.mode: PASSTHROUGH`, and add a `tlsRoute` with `sniHosts` mapping to a backend `destination`.
- If you enable Istio Service creation, ensure the ports you expose match the ports opened in the Istio `Gateway` `servers`. Envoy will begin to listen on those ports once the `Gateway` is applied.
- Gateway API support for `TCPRoute` may vary by implementation and Kubernetes version; adjust the `apiVersion` if your controller requires a specific version.
