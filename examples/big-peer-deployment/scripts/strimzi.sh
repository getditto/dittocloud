#!/usr/bin/env bash
# Strimzi Kafka operator. Big Peer's CDC pipeline runs on Kafka, and the Ditto
# Operator expects Strimzi's CRDs to already exist.
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

if release_healthy strimzi kafka "name=strimzi-cluster-operator"; then
  skip "strimzi $STRIMZI_VERSION"
  exit 0
fi

helm_install strimzi strimzi-kafka-operator kafka \
  --repo https://strimzi.io/charts \
  --version "$STRIMZI_VERSION" \
  --set watchAnyNamespace=true

wait_pods kafka "name=strimzi-cluster-operator" 300

# The Ditto Operator creates Kafka CRs; without these registered it fails to
# reconcile a BigPeer.
wait_crd kafkas.kafka.strimzi.io
wait_crd kafkatopics.kafka.strimzi.io

log "strimzi ready"
