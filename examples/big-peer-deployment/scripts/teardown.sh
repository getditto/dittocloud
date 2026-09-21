#!/usr/bin/env bash
# Remove everything this Makefile installed. Terraform-managed resources (the
# cluster, node groups, EKS add-ons) are left alone -- use `terraform destroy`
# for those.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

EG_NS=envoy-gateway-system

cat <<EOF

This will DELETE from cluster '$CLUSTER' (context: $KUBE_CONTEXT):

  - BigPeer/$BIGPEER_NAME in namespace $NAMESPACE, and its PersistentVolumeClaims
  - the ditto-operator, strimzi, envoy-gateway and cert-manager Helm releases
  - the Gateway / GatewayClass / EnvoyProxy resources and their NLBs
  - the gp3 StorageClass

Persistent volumes are reclaimPolicy: Delete, so Big Peer and Kafka data
will be destroyed permanently.

EOF

read -r -p "Type the cluster name to confirm: " confirm
[[ "$confirm" == "$CLUSTER" ]] || die "aborted"

# Big Peer first: the operator must be alive to finalize its children.
log "deleting BigPeer"
kc -n "$NAMESPACE" delete bigpeer "$BIGPEER_NAME" --ignore-not-found --timeout=300s || true

log "deleting HTTPRoutes"
kc -n "$NAMESPACE" delete httproute --all --ignore-not-found || true

log "uninstalling helm releases"
helmc uninstall ditto-operator -n "$NAMESPACE"  2>/dev/null || true
helmc uninstall strimzi        -n kafka         2>/dev/null || true

log "deleting gateway resources"
for n in int-envoy-proxy ext-envoy-proxy; do
  kc -n "$EG_NS" delete gateway "$n"     --ignore-not-found || true
  kc delete gatewayclass "$n"            --ignore-not-found || true
  kc -n "$EG_NS" delete envoyproxy "$n"  --ignore-not-found || true
done
helmc uninstall envoy-gateway -n "$EG_NS" 2>/dev/null || true

log "uninstalling cert-manager"
kc delete clusterissuer ditto-ca selfsigned-bootstrap --ignore-not-found || true
kc -n cert-manager delete certificate ditto-root-ca --ignore-not-found || true
helmc uninstall cert-manager -n cert-manager 2>/dev/null || true

log "deleting gp3 StorageClass"
kc delete storageclass gp3 --ignore-not-found || true

log "checking for leftover PVCs"
kc -n "$NAMESPACE" get pvc 2>/dev/null || true

log "teardown complete. Local credentials remain at $LOCAL_DIR/app.env"
