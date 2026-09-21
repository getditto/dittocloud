#!/usr/bin/env bash
# Route the self-managed portal through the existing internal gateway.
#
# The portal itself is part of the ditto-operator chart (portal.enabled), so
# ditto-operator.sh turns it on. This script only handles the networking.
#
# The chart's own portal.ingress / portal.operatorApiIngress render `Ingress`
# objects with class nginx, which Envoy Gateway does not serve -- both are left
# disabled and we author an HTTPRoute instead.
#
# Everything is served from ONE hostname so the browser sees a single origin:
# the chart's default topology puts the portal and the operator API on separate
# hosts, which would need a CORS policy on the API. Same-origin avoids that.
#
#   http://$PORTAL_HOST/               -> portal UI
#   http://$PORTAL_HOST/operator-api/  -> operator admin API (prefix stripped)
#   http://$PORTAL_HOST/big-peer/      -> Big Peer HTTP API   (prefix stripped)
#
# No new load balancer: this attaches to int-envoy-proxy, which is an internal
# NLB. There is no public exposure.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

EG_NS=envoy-gateway-system
ROUTE="ditto-portal"
GW=int-envoy-proxy

if [[ "${PORTAL:-0}" != "1" ]]; then
  log "PORTAL=0; removing portal route if present"
  kc -n "$NAMESPACE" delete httproute "$ROUTE" --ignore-not-found >/dev/null 2>&1 || true
  exit 0
fi

kc -n "$EG_NS" get gateway "$GW" >/dev/null 2>&1 \
  || die "gateway $GW not found -- run 'make envoy' first"

kc -n "$NAMESPACE" get deploy ditto-operator-portal >/dev/null 2>&1 \
  || die "portal deployment not found -- run 'PORTAL=1 make operator' first"

log "applying portal HTTPRoute for host $PORTAL_HOST"
# Longest path prefix wins, so /operator-api and /big-peer beat the portal's
# catch-all. The portal is an SPA and needs to keep serving / itself.
kc apply -f - <<YAML
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: ${ROUTE}
  namespace: ${NAMESPACE}
spec:
  parentRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: ${GW}
      namespace: ${EG_NS}
  hostnames:
    - ${PORTAL_HOST}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /operator-api
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - group: ""
          kind: Service
          name: ditto-operator
          port: 80
          weight: 1
    - matches:
        - path:
            type: PathPrefix
            value: /big-peer
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - group: ""
          kind: Service
          name: ditto-${BIGPEER_NAME}-api
          port: 8080
          weight: 1
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - group: ""
          kind: Service
          name: ditto-operator-portal
          port: 80
          weight: 1
YAML

wait_pods "$NAMESPACE" "app.kubernetes.io/name=ditto-operator-portal" 300 \
  || warn "portal pods not Ready; check 'kubectl -n $NAMESPACE get pods'"

log "portal routed"
kc -n "$NAMESPACE" get httproute "$ROUTE"

cat <<EOF

  The portal is only reachable inside the VPC, on an internal NLB. There is no
  DNS record for $PORTAL_HOST, so from a workstation use a port-forward and
  send the Host header yourself:

    kubectl --context $KUBE_CONTEXT -n $EG_NS port-forward \\
      svc/\$(kc -n $EG_NS get svc -l gateway.envoyproxy.io/owning-gateway-name=$GW -o jsonpath='{.items[0].metadata.name}') 18080:80

    curl -H 'Host: $PORTAL_HOST' http://127.0.0.1:18080/

  For a browser, add to /etc/hosts:  127.0.0.1  $PORTAL_HOST
  then open http://$PORTAL_HOST:18080/
EOF
