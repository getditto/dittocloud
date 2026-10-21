#!/usr/bin/env bash
# Ditto Operator. Public OCI chart -- no pull secret required.
#
# PORTAL=1 additionally enables the self-managed portal UI, which ships in this
# same chart (portal.enabled). Its own Ingress templates stay disabled: Envoy
# Gateway does not serve Ingress, so scripts/portal.sh authors an HTTPRoute
# instead. Nothing here creates a load balancer.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

portal_wanted() { [[ "${PORTAL:-0}" == "1" ]]; }

# Skip only when the installed state matches the requested one, so toggling
# PORTAL always takes effect (same approach as EXTERNAL in envoy-gateway.sh).
portal_installed() { kc -n "$NAMESPACE" get deploy ditto-operator-portal >/dev/null 2>&1; }
if release_healthy ditto-operator "$NAMESPACE" "app.kubernetes.io/name=ditto-operator"; then
  if portal_wanted && portal_installed; then
    skip "ditto-operator $DITTO_OPERATOR_VERSION (with portal)"; exit 0
  elif ! portal_wanted && ! portal_installed; then
    skip "ditto-operator $DITTO_OPERATOR_VERSION"; exit 0
  fi
  log "PORTAL=${PORTAL:-0} differs from what is installed; reconciling"
fi

args=(--version "$DITTO_OPERATOR_VERSION")

if portal_wanted; then
  log "portal enabled (host $PORTAL_HOST, image tag $PORTAL_VERSION)"
  # Same-origin URLs: both go through the one HTTPRoute portal.sh creates, so
  # the browser never makes a cross-origin request and no CORS policy is needed.
  args+=(
    --set portal.enabled=true
    --set portal.image.tag="$PORTAL_VERSION"
    --set portal.config.bigPeerName="$BIGPEER_NAME"
    --set portal.config.operatorApiUrl="http://${PORTAL_HOST}/operator-api"
    --set portal.config.bigPeerBaseUrl="http://${PORTAL_HOST}/big-peer"
    --set portal.ingress.enabled=false
    --set portal.operatorApiIngress.enabled=false
  )
fi

helm_install ditto-operator oci://quay.io/ditto-external/ditto-operator "$NAMESPACE" "${args[@]}"

wait_pods "$NAMESPACE" "app.kubernetes.io/name=ditto-operator" 300 \
  || wait_pods "$NAMESPACE" "app=ditto-operator" 300

wait_crd bigpeers.ditto.live

log "ditto operator ready"
kc api-resources --api-group=ditto.live 2>/dev/null || true
