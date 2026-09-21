#!/usr/bin/env bash
# Show what is installed and where to reach it.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

EG_NS=envoy-gateway-system

endpoints() {
  log "gateway endpoints"
  for gw in int-envoy-proxy ext-envoy-proxy; do
    kc -n "$EG_NS" get gateway "$gw" >/dev/null 2>&1 || continue
    addr=$(kc -n "$EG_NS" get gateway "$gw" -o jsonpath='{.status.addresses[0].value}' 2>/dev/null)
    printf '  %-18s %s\n' "$gw" "${addr:-<pending>}"
  done
  echo
  echo "  Nothing is published in DNS. Reach the internal gateway from inside the"
  echo "  VPC, or locally with:"
  echo "    kubectl --context $KUBE_CONTEXT -n $EG_NS port-forward \\"
  echo "      svc/<envoy-service> 18080:80"
}

if [[ "${1:-}" == "endpoints" ]]; then
  endpoints
  exit 0
fi

log "nodes"; kc get nodes -o wide 2>/dev/null || true
echo
log "storage"; kc get storageclass 2>/dev/null || true
echo
log "helm releases"
helmc list --all-namespaces 2>/dev/null || true
echo
log "gateways"; kc -n "$EG_NS" get gateway 2>/dev/null || true
echo
log "ditto namespace"
kc -n "$NAMESPACE" get bigpeer,pods,pvc,httproute 2>/dev/null || true
echo
endpoints
