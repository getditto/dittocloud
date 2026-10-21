#!/usr/bin/env bash
# Shared helpers. Sourced by every script in this directory.

set -euo pipefail

: "${CLUSTER:?}" "${REGION:?}" "${NAMESPACE:?}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$HERE")"
LOCAL_DIR="$ROOT/.local"

# Self-contained kubeconfig in the example directory. Nothing here touches
# ~/.kube/config, so running this example cannot disturb whatever cluster the
# operator happens to have selected globally.
export KUBECONFIG="${KUBECONFIG:-$ROOT/.kube/config}"
KUBE_CONTEXT="${KUBE_CONTEXT:-$CLUSTER}"

kc()    { kubectl --context "$KUBE_CONTEXT" "$@"; }
helmc() { helm --kube-context "$KUBE_CONTEXT" "$@"; }

log()  { printf '\033[36m==>\033[0m %s\n' "$*"; }
skip() { printf '\033[32m -->\033[0m %s \033[2m(already installed, skipping)\033[0m\n' "$*"; }
warn() { printf '\033[33mWARN\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31mERROR\033[0m %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

# --- idempotency -----------------------------------------------------------
# Every target is safe to re-run, but re-running a healthy Helm release still
# costs a slow --wait. These let a target bail out early. FORCE=1 overrides.

force() { [[ "${FORCE:-0}" == "1" ]]; }

# True when a Helm release is deployed AND its pods are Ready.
release_healthy() {
  local name="$1" ns="$2" selector="${3:-}"
  force && return 1
  [[ "$(helmc status "$name" -n "$ns" -o json 2>/dev/null | jq -r '.info.status' 2>/dev/null)" == "deployed" ]] || return 1
  [[ -z "$selector" ]] && return 0
  kc -n "$ns" get pods -l "$selector" -o json 2>/dev/null \
    | jq -e '.items | length > 0 and (all(.[]; .status.conditions[]? | select(.type=="Ready") | .status == "True"))' \
      >/dev/null 2>&1
}

# True when a cluster-scoped object already exists.
object_exists() {
  force && return 1
  kc get "$@" >/dev/null 2>&1
}

# --- waits -----------------------------------------------------------------

wait_crd() {
  local crd="$1" timeout="${2:-120s}"
  log "waiting for CRD $crd"
  kc wait --for condition=established --timeout="$timeout" "crd/$crd"
}

# Wait for pods matching a selector to be Ready, tolerating the window before
# they exist at all -- which `kubectl wait` alone does not.
wait_pods() {
  local ns="$1" selector="$2" timeout="${3:-300}"
  local deadline=$(( SECONDS + timeout ))
  log "waiting for pods in $ns matching $selector"
  while (( SECONDS < deadline )); do
    if kc -n "$ns" get pods -l "$selector" -o name 2>/dev/null | grep -q .; then
      if kc -n "$ns" wait --for=condition=Ready pod -l "$selector" --timeout=30s >/dev/null 2>&1; then
        return 0
      fi
    fi
    sleep 5
  done
  warn "timed out waiting for $selector in $ns; current state:"
  kc -n "$ns" get pods -l "$selector" || true
  return 1
}

ensure_ns() {
  kc get namespace "$1" >/dev/null 2>&1 || kc create namespace "$1"
}

helm_install() {
  local name="$1" chart="$2" ns="$3"; shift 3
  log "helm upgrade --install $name ($chart) -> $ns"
  helmc upgrade --install "$name" "$chart" \
    --namespace "$ns" --create-namespace \
    --wait --timeout 10m "$@"
}

external_enabled() { [[ "${EXTERNAL:-0}" == "1" ]]; }

mkdir -p "$LOCAL_DIR"

# --- port-forward ----------------------------------------------------------
# Hardcoding a port collides with anything already listening -- including a
# port-forward leaked by an earlier failed run. Pick a free one, and make
# cleanup reliable so we do not leak one ourselves.

PF_PID=""
PF_PORT=""

free_port() {
  local p
  for p in $(seq "${1:-18080}" "${2:-18180}"); do
    if ! (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null; then echo "$p"; return 0; fi
    exec 3<&- 2>/dev/null || true
  done
  die "no free local port in ${1:-18080}-${2:-18180}"
}

pf_stop() {
  [[ -z "$PF_PID" ]] && return 0
  kill "$PF_PID" 2>/dev/null || true
  # kubectl can spawn children; take those too.
  pkill -P "$PF_PID" 2>/dev/null || true
  PF_PID=""
}

# port_forward <namespace> <target> <remote-port>  -> sets PF_PORT
port_forward() {
  local ns="$1" target="$2" remote="$3"
  PF_PORT="$(free_port)"
  log "port-forwarding $target on :$PF_PORT"
  kc -n "$ns" port-forward "$target" "${PF_PORT}:${remote}" >/dev/null 2>&1 &
  PF_PID=$!
  # Detach from job control so killing it does not print "Terminated: 15".
  disown "$PF_PID" 2>/dev/null || true
  trap pf_stop EXIT

  local deadline=$(( SECONDS + 30 ))
  while (( SECONDS < deadline )); do
    kill -0 "$PF_PID" 2>/dev/null || die "port-forward to $target exited immediately"
    # No -f: the API root legitimately returns 404, which still proves the
    # tunnel is up. We only care that *something* answers.
    if curl -s -o /dev/null --max-time 2 "http://127.0.0.1:${PF_PORT}/" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  die "port-forward to $target never became reachable"
}
