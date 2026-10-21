#!/usr/bin/env bash
# Envoy Gateway, shaped after cloud-infra-apps but sized for a test cluster.
#
# cloud-infra runs three proxies (ext/int/str) with 3 replicas, an HPA,
# zone topology spread, a dedicated `app.ditto.live/tier: infrastructure`
# node pool and TLS terminated at the NLB via ACM. None of that applies here:
# this cluster has one unlabelled node group and no ACM certificate. We keep
# the CR *shape* (EnvoyProxy -> GatewayClass -> Gateway) and drop the rest.
#
# The internal gateway is always created. The internet-facing one only when
# EXTERNAL=1, because without a certificate it would serve plaintext HTTP.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

EG_NS=envoy-gateway-system
REPLICAS="${ENVOY_REPLICAS:-1}"

# Skip only when the installed state already matches the requested one. A change
# to EXTERNAL must still take effect, so check that the external gateway's
# presence agrees with the flag before bailing out.
ext_present() { kc -n "$EG_NS" get gateway ext-envoy-proxy >/dev/null 2>&1; }
if release_healthy envoy-gateway "$EG_NS" "app.kubernetes.io/name=gateway-helm" \
   && kc -n "$EG_NS" get gateway int-envoy-proxy >/dev/null 2>&1; then
  if external_enabled && ext_present; then
    skip "envoy gateway $ENVOY_GATEWAY_VERSION (internal + external)"; exit 0
  elif ! external_enabled && ! ext_present; then
    skip "envoy gateway $ENVOY_GATEWAY_VERSION (internal)"; exit 0
  fi
  log "EXTERNAL=$EXTERNAL differs from what is installed; reconciling"
fi

helm_install envoy-gateway oci://docker.io/envoyproxy/gateway-helm "$EG_NS" \
  --version "$ENVOY_GATEWAY_VERSION" \
  --set config.envoyGateway.gateway.controllerName=gateway.envoyproxy.io/gatewayclass-controller \
  --set config.envoyGateway.provider.type=Kubernetes \
  --set config.envoyGateway.logging.level.default=info

wait_pods "$EG_NS" "app.kubernetes.io/name=gateway-helm" 300
wait_crd gatewayclasses.gateway.networking.k8s.io
wait_crd envoyproxies.gateway.envoyproxy.io

# Emit one EnvoyProxy + GatewayClass + Gateway triple.
#   $1 = name (int-envoy-proxy | ext-envoy-proxy)
#   $2 = scheme (internal | internet-facing)
render_gateway() {
  local name="$1" scheme="$2" internal_annotation=""

  # Legacy cloud-controller-manager annotations. The AWS Load Balancer
  # Controller is not installed on this cluster, so we use the annotations
  # EKS honours natively.
  if [[ "$scheme" == "internal" ]]; then
    internal_annotation='service.beta.kubernetes.io/aws-load-balancer-internal: "true"'
  fi

  cat <<YAML
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: ${name}
  namespace: ${EG_NS}
spec:
  # Give WebSocket sync sessions time to drain before Envoy exits.
  shutdown:
    drainTimeout: 300s
    minDrainDuration: 10s
  provider:
    type: Kubernetes
    kubernetes:
      envoyDeployment:
        replicas: ${REPLICAS}
        container:
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              memory: 512Mi
      envoyService:
        annotations:
          service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
          service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled: "true"
          ${internal_annotation}
---
apiVersion: gateway.networking.k8s.io/v1beta1
kind: GatewayClass
metadata:
  name: ${name}
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
  parametersRef:
    group: gateway.envoyproxy.io
    kind: EnvoyProxy
    name: ${name}
    namespace: ${EG_NS}
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: ${name}
  namespace: ${EG_NS}
spec:
  gatewayClassName: ${name}
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
        kinds:
          - group: gateway.networking.k8s.io
            kind: HTTPRoute
---
# Ditto's sync protocol holds long-lived WebSocket and streaming-query
# connections. An Envoy-side request deadline would cut them off, so disable
# it at the Gateway level and let the backend own its own timeouts.
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: ${name}-timeout
  namespace: ${EG_NS}
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: ${name}
  timeout:
    http:
      requestTimeout: 0s
YAML
}

log "applying internal gateway"
render_gateway int-envoy-proxy internal | kc apply -f -

if external_enabled; then
  log "applying external gateway (EXTERNAL=1)"
  warn "the external NLB serves plaintext HTTP -- no ACM cert exists in this account"
  render_gateway ext-envoy-proxy internet-facing | kc apply -f -
else
  log "skipping external gateway (set EXTERNAL=1 to enable)"
  # Remove it if it was previously created, so EXTERNAL=0 is a real off switch.
  kc delete gateway ext-envoy-proxy -n "$EG_NS" --ignore-not-found >/dev/null 2>&1 || true
  kc delete gatewayclass ext-envoy-proxy --ignore-not-found >/dev/null 2>&1 || true
  kc delete envoyproxy ext-envoy-proxy -n "$EG_NS" --ignore-not-found >/dev/null 2>&1 || true
fi

log "waiting for gateways to be programmed"
kc -n "$EG_NS" wait --for=condition=Programmed gateway/int-envoy-proxy --timeout=300s || {
  kc -n "$EG_NS" describe gateway int-envoy-proxy || true
  die "internal gateway never became Programmed"
}
external_enabled && kc -n "$EG_NS" wait --for=condition=Programmed gateway/ext-envoy-proxy --timeout=300s || true

log "envoy gateway ready"
kc -n "$EG_NS" get gateway
