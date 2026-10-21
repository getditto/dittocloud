#!/usr/bin/env bash
# EKS ships a legacy `gp2` StorageClass whose in-tree provisioner no longer
# exists in Kubernetes >= 1.31. Install a gp3 class backed by the EBS CSI
# driver and make it the cluster default.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

if object_exists storageclass gp3; then
  skip "gp3 StorageClass"
  exit 0
fi

log "installing gp3 StorageClass"

# Drop the default annotation from any other class first; two defaults is an
# error state and PVCs without an explicit class will fail to bind.
for sc in $(kc get storageclass -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do
  [[ "$sc" == "gp3" ]] && continue
  if [[ "$(kc get storageclass "$sc" -o jsonpath='{.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}' 2>/dev/null)" == "true" ]]; then
    log "removing default flag from StorageClass/$sc"
    kc patch storageclass "$sc" -p \
      '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}'
  fi
done

kc apply -f - <<'YAML'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: ebs.csi.aws.com
# WaitForFirstConsumer keeps the volume in the same AZ as the pod that claims
# it. With Immediate, a volume can land in an AZ with no schedulable node.
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
reclaimPolicy: Delete
parameters:
  type: gp3
  encrypted: "true"
YAML

log "storage ready"
kc get storageclass
