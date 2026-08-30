#!/usr/bin/env bash
# Quickstart steps 11-12: insert a document, then read it back.
#
# Routes through the internal gateway via a port-forward to the Envoy service,
# so this works whether or not EXTERNAL=1. That also exercises the HTTPRoutes
# rather than talking to the Big Peer service directly.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

ENV_FILE="$LOCAL_DIR/app.env"
[[ -f "$ENV_FILE" ]] || die "missing $ENV_FILE -- run 'make app' first"
# shellcheck disable=SC1090
source "$ENV_FILE"

EG_NS=envoy-gateway-system

svc=$(kc -n "$EG_NS" get svc -l gateway.envoyproxy.io/owning-gateway-name=int-envoy-proxy \
        -o name 2>/dev/null | head -1)
[[ -n "$svc" ]] || die "could not find the Envoy service for int-envoy-proxy"

port_forward "$EG_NS" "$svc" 80
BASE="http://127.0.0.1:${PF_PORT}/${DITTO_APP_ID}/api/v4/store/execute"

log "INSERT"
insert=$(curl -s -X POST "$BASE" \
  --header "Authorization: bearer ${DITTO_API_KEY}" \
  --header 'Content-Type: application/json' \
  --data-raw '{"statement":"INSERT INTO cars DOCUMENTS (:doc1)","args":{"doc1":{"_id":{"id":"777","locationId":"123456"},"color":"blue","timestamp":"1732192529"}}}')
echo "$insert" | jq . 2>/dev/null || echo "$insert"

log "SELECT"
select=$(curl -s -X POST "$BASE" \
  --header "Authorization: bearer ${DITTO_API_KEY}" \
  --header 'Content-Type: application/json' \
  --data-raw '{"statement":"SELECT * FROM cars"}')
echo "$select" | jq . 2>/dev/null || echo "$select"

if grep -q '"color"' <<<"$select"; then
  log "smoke test PASSED"
else
  die "smoke test FAILED -- no document returned"
fi
