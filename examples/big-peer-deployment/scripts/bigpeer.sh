#!/usr/bin/env bash
# Apply the BigPeer CR, then route to it with Gateway API HTTPRoutes.
#
# The quickstart sets `spec.network.ingress.host`, which makes the operator
# create a Kubernetes *Ingress*. Envoy Gateway implements the Gateway API and
# does not serve Ingress resources, so that field is deliberately omitted and
# we author HTTPRoutes instead -- the same approach cloud-infra-apps takes.
#
# Operator 0.17.2 only serves v1alpha1. The `spec.network.mode: GatewayApi`
# field referenced in the CRD docs arrives in v1alpha2 and is not available
# yet; revisit this script when the operator ships it.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

ensure_ns "$NAMESPACE"

# The CR is applied unconditionally -- it is declarative and cheap. What we skip
# is the slow readiness wait, when the instance is already up at this version.
already_running() {
  force && return 1
  [[ "$(kc -n "$NAMESPACE" get bigpeer "$BIGPEER_NAME" \
        -o jsonpath='{.spec.version}' 2>/dev/null)" == "$BIGPEER_VERSION" ]] || return 1
  kc -n "$NAMESPACE" get pods -l "ditto.live/big-peer=${BIGPEER_NAME}" -o json 2>/dev/null \
    | jq -e '.items | length > 0 and (all(.[]; .status.conditions[]? | select(.type=="Ready") | .status == "True"))' \
      >/dev/null 2>&1
}

if already_running; then
  skip "BigPeer/$BIGPEER_NAME v$BIGPEER_VERSION"
  BIGPEER_READY=1
fi

# Big Peer defaults to an `ingress`-type external listener for Kafka, which
# only Data Bridges need. Strimzi then blocks its entire reconcile waiting for
# that Ingress to become addressable -- and with Envoy Gateway there is no
# Ingress controller to give it an address, so the Kafka broker is never
# created and the whole Big Peer stalls. Off unless explicitly requested.
if [[ "${KAFKA_EXTERNAL_LISTENER:-0}" == "1" ]]; then
  kafka_listener="true"
  warn "KAFKA_EXTERNAL_LISTENER=1 requires an Ingress controller (Envoy Gateway"
  warn "does not serve Ingress). Kafka will not start without one."
else
  kafka_listener="false"
fi

log "applying BigPeer/$BIGPEER_NAME (version $BIGPEER_VERSION)"
kc apply -f - <<YAML
apiVersion: ditto.live/v1alpha1
kind: BigPeer
metadata:
  name: ${BIGPEER_NAME}
  namespace: ${NAMESPACE}
spec:
  version: ${BIGPEER_VERSION}
  cdc:
    network:
      kafkaExternalListener:
        enabled: ${kafka_listener}
  auth:
    providers:
      __playgroundProvider:
        anonymous:
          permission:
            read:
              everything: true
              queriesByCollection: {}
            write:
              everything: true
              queriesByCollection: {}
          sessionLength: 630000
          sharedToken: ${SHARED_TOKEN}
YAML

if [[ -n "${BIGPEER_READY:-}" ]]; then
  log "Big Peer already Ready; not waiting again"
else
  log "waiting for Big Peer pods (first run pulls images and provisions Kafka; this is slow)"
  wait_pods "$NAMESPACE" "ditto.live/big-peer=${BIGPEER_NAME}" 900 || {
    warn "Big Peer pods not all Ready. Checking the usual causes:"

    # Kafka gates everything else, so diagnose it first. A PVC stuck on
    # WaitForFirstConsumer with no pod means Strimzi never got that far.
    kafka_msg=$(kc -n "$NAMESPACE" get kafka -o jsonpath='{.items[*].status.conditions[?(@.type=="NotReady")].message}' 2>/dev/null || true)
    if [[ "$kafka_msg" == *"Ingress"* ]]; then
      warn "  Kafka is blocked on an Ingress that nothing serves:"
      warn "    $kafka_msg"
      warn "  Envoy Gateway does not implement the Ingress API. Set"
      warn "  KAFKA_EXTERNAL_LISTENER=0 (the default) and re-run 'make bigpeer',"
      warn "  or install an Ingress controller if you need Kafka Data Bridges."
    elif [[ -n "$kafka_msg" ]]; then
      warn "  Kafka not ready: $kafka_msg"
    fi

    if kc -n "$NAMESPACE" get pods 2>/dev/null | grep -q Pending; then
      warn "  Pods Pending -> check node capacity; there is no autoscaler."
    fi
    warn "  PVCs Pending -> check the gp3 StorageClass exists (make storage)."

    kc -n "$NAMESPACE" get pods,pvc || true
    kc -n "$NAMESPACE" get kafka,kafkanodepool 2>/dev/null || true
    exit 1
  }
fi

# Backend services created by the operator, per cloud-infra-apps:
#   ditto-<name>-api           :8080  HTTP API
#   ditto-<name>-auth-server   :7070  auth
#   ditto-<name>-subscription  :7070  sync / WebSocket
SVC="ditto-${BIGPEER_NAME}"

log "applying HTTPRoutes"
# One route with three rules. Gateway API resolves ties by longest path prefix,
# so /_ditto/auth beats /_ditto, which beats /. Splitting these across separate
# HTTPRoutes would make precedence depend on creation order instead.
for gw in $(kc -n envoy-gateway-system get gateway -o name 2>/dev/null | cut -d/ -f2); do
  log "  -> gateway $gw"
  kc apply -f - <<YAML
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: ${SVC}-${gw}
  namespace: ${NAMESPACE}
spec:
  parentRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: ${gw}
      namespace: envoy-gateway-system
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /_ditto/auth
      backendRefs:
        - group: ""
          kind: Service
          name: ${SVC}-auth-server
          port: 7070
          weight: 1
    - matches:
        - path:
            type: PathPrefix
            value: /_ditto
      backendRefs:
        - group: ""
          kind: Service
          name: ${SVC}-subscription
          port: 7070
          weight: 1
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - group: ""
          kind: Service
          name: ${SVC}-api
          port: 8080
          weight: 1
YAML
done

log "big peer ready"
kc -n "$NAMESPACE" get bigpeer,httproute
