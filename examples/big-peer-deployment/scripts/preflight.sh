#!/usr/bin/env bash
# Verify the cluster is actually ready to receive the stack.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

need kubectl; need helm; need aws; need jq

log "checking cluster $CLUSTER in $REGION"
kc cluster-info >/dev/null 2>&1 \
  || die "cannot reach cluster via context '$KUBE_CONTEXT'. Run: make kubeconfig"

# Nodes must be Ready. A NotReady node here almost always means the CNI add-on
# never landed -- the exact failure this example's Terraform now guards against.
ready=$(kc get nodes --no-headers 2>/dev/null | awk '$2=="Ready"' | wc -l | tr -d ' ')
total=$(kc get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
log "nodes Ready: $ready/$total"
(( ready > 0 )) || {
  kc get nodes -o wide || true
  die "no Ready nodes. If nodes are NotReady, check that the vpc-cni add-on is installed."
}

# The full stack does not fit on a single m8i.large. Warn rather than fail so
# a deliberately small run can proceed.
if (( ready < 3 )); then
  warn "only $ready Ready node(s). cert-manager + Envoy + Kafka + Big Peer will not"
  warn "fit comfortably below 3 nodes. Consider desired_size: 3 in config.yaml."
fi

# The EBS CSI driver is a hard dependency: Strimzi and Big Peer both need PVCs.
if ! kc get csidriver ebs.csi.aws.com >/dev/null 2>&1; then
  die "EBS CSI driver not found. Run 'terraform apply' first -- aws-ebs-csi-driver
  is declared in modules/deployment/eks.tf. Without it every PVC stays Pending."
fi
log "EBS CSI driver present"

log "preflight OK"
