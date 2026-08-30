#!/usr/bin/env bash
# cert-manager via the Jetstack chart, matching the Ditto quickstart.
#
# There is no Route53 hosted zone or ACM certificate in this account, so ACME
# (Let's Encrypt) cannot complete a challenge. We install a self-signed root CA
# and a ClusterIssuer that signs from it -- enough for in-cluster TLS and for
# any webhook that needs a cert. Clients will not trust it; that is expected on
# a test cluster.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

if release_healthy cert-manager cert-manager "app.kubernetes.io/instance=cert-manager" \
   && object_exists clusterissuer ditto-ca; then
  skip "cert-manager $CERT_MANAGER_VERSION"
  exit 0
fi

helm_install cert-manager cert-manager cert-manager \
  --repo https://charts.jetstack.io \
  --version "$CERT_MANAGER_VERSION" \
  --set crds.enabled=true \
  --set startupapicheck.enabled=false \
  --set prometheus.enabled=false

wait_pods cert-manager "app.kubernetes.io/instance=cert-manager" 300

wait_crd clusterissuers.cert-manager.io

log "creating self-signed root CA and ClusterIssuer"
kc apply -f - <<'YAML'
---
# Bootstrap issuer: signs nothing but the root CA below.
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-bootstrap
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: ditto-root-ca
  namespace: cert-manager
spec:
  isCA: true
  commonName: ditto-root-ca
  secretName: ditto-root-ca
  duration: 87600h    # 10y
  renewBefore: 720h   # 30d
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: selfsigned-bootstrap
    kind: ClusterIssuer
    group: cert-manager.io
---
# The issuer everything else should reference.
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: ditto-ca
spec:
  ca:
    secretName: ditto-root-ca
YAML

log "waiting for the root CA to be issued"
kc -n cert-manager wait --for=condition=Ready certificate/ditto-root-ca --timeout=120s

log "cert-manager ready (ClusterIssuer: ditto-ca)"
