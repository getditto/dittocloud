#
# Part 2 of this example: deploy Big Peer and its dependencies onto the EKS
# cluster that Part 1 (Terraform) built. See README.md.
#
#   make            # list targets
#   make all        # kubeconfig -> bootstrap -> bigpeer -> app -> smoke
#   make FORCE=1 …  # reinstall components that are already healthy
#

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

# These steps are strictly ordered (CNI before nodes, gateways before routes,
# Big Peer before its app). -j would interleave them.
.NOTPARALLEL:

S       := ./scripts
KUBEDIR := $(CURDIR)/.kube
KUBECFG := $(KUBEDIR)/config

# ---------------------------------------------------------------------------
# Configuration, in precedence order (lowest first):
#
#   1. the defaults below
#   2. .env        - written by `terraform apply`, or derived from config.yaml
#   3. .env.local  - yours, never touched by Terraform (see .env.example)
#   4. the command line, e.g. `make EXTERNAL=1 envoy`
#
# `-include` tolerates the files being absent. The `.env:` rule below creates
# it from config.yaml when Terraform has not run yet.
# ---------------------------------------------------------------------------

# Written atomically: a partial .env would be silently swallowed by -include
# and leave `make` pointed at the placeholder defaults below.
.env:
	@echo "==> .env not found; deriving it from config.yaml"
	@$(S)/config.sh > $@.tmp && mv $@.tmp $@ \
	  || { rm -f $@.tmp; echo "ERROR: could not derive .env from config.yaml" >&2; exit 1; }

-include .env
-include .env.local

# Nothing above is required to be set: every value has a working default here.
CLUSTER      ?= big-peer-dev
REGION       ?= us-east-1
DEPLOYMENT   ?= $(CLUSTER)
KUBE_CONTEXT ?= $(CLUSTER)

NAMESPACE       ?= ditto
BIGPEER_NAME    ?= example
BIGPEER_VERSION ?= 1.59.0
SHARED_TOKEN    ?= abc123

EXTERNAL       ?= 0
ENVOY_REPLICAS ?= 1

# Kafka's ingress-type external listener. Needs an Ingress controller, which
# this example does not install (Envoy Gateway is Gateway API only). Only
# required for Kafka Data Bridges.
KAFKA_EXTERNAL_LISTENER ?= 0

# Self-managed portal UI. Ships in the ditto-operator chart, off by default.
# Served from the internal gateway only -- no public load balancer.
PORTAL      ?= 0
PORTAL_HOST ?= portal.ditto.local
PORTAL_VERSION ?= 0.3.0

CERT_MANAGER_VERSION   ?= v1.21.1
ENVOY_GATEWAY_VERSION  ?= 1.6.3
STRIMZI_VERSION        ?= 0.49.0
DITTO_OPERATOR_VERSION ?= 0.17.2

# Scripts read all of this from the environment.
export CLUSTER REGION DEPLOYMENT KUBE_CONTEXT NAMESPACE BIGPEER_NAME
export BIGPEER_VERSION SHARED_TOKEN EXTERNAL ENVOY_REPLICAS FORCE
export KAFKA_EXTERNAL_LISTENER PORTAL PORTAL_HOST PORTAL_VERSION
export CERT_MANAGER_VERSION ENVOY_GATEWAY_VERSION STRIMZI_VERSION DITTO_OPERATOR_VERSION

# Self-contained kubeconfig: this example never touches ~/.kube/config, so it
# cannot disturb whatever cluster you have selected globally.
export KUBECONFIG := $(KUBECFG)

ifdef AWS_PROFILE
export AWS_PROFILE
endif

# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@echo "Big Peer on EKS - Part 2 (deploy)"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Cluster : $(CLUSTER) ($(REGION))   profile: $(or $(AWS_PROFILE),<ambient>)"
	@echo "Big Peer: $(BIGPEER_NAME) v$(BIGPEER_VERSION) in ns/$(NAMESPACE)   EXTERNAL=$(EXTERNAL)"
	@echo "Config  : $(if $(wildcard .env),.env,<none>) $(if $(wildcard .env.local),+ .env.local,)"
	@echo "Kubecfg : $(KUBECFG)"

.PHONY: config
config: ## Print the resolved configuration
	@printf '  %-24s %s\n' \
	  CLUSTER "$(CLUSTER)" REGION "$(REGION)" DEPLOYMENT "$(DEPLOYMENT)" \
	  AWS_PROFILE "$(or $(AWS_PROFILE),<ambient>)" KUBECONFIG "$(KUBECFG)" \
	  NAMESPACE "$(NAMESPACE)" BIGPEER_NAME "$(BIGPEER_NAME)" \
	  BIGPEER_VERSION "$(BIGPEER_VERSION)" EXTERNAL "$(EXTERNAL)" \
	  ENVOY_REPLICAS "$(ENVOY_REPLICAS)" \
	  KAFKA_EXTERNAL_LISTENER "$(KAFKA_EXTERNAL_LISTENER)" \
	  PORTAL "$(PORTAL)" PORTAL_HOST "$(PORTAL_HOST)" \
	  PORTAL_VERSION "$(PORTAL_VERSION)" \
	  CERT_MANAGER_VERSION "$(CERT_MANAGER_VERSION)" \
	  ENVOY_GATEWAY_VERSION "$(ENVOY_GATEWAY_VERSION)" \
	  STRIMZI_VERSION "$(STRIMZI_VERSION)" \
	  DITTO_OPERATOR_VERSION "$(DITTO_OPERATOR_VERSION)"

# File target: written once, then reused. `make kubeconfig` forces a refresh.
$(KUBECFG):
	@mkdir -p $(KUBEDIR)
	@echo "==> writing kubeconfig for $(CLUSTER) -> $(KUBECFG)"
	@aws eks update-kubeconfig --region "$(REGION)" --name "$(CLUSTER)" \
	   --alias "$(KUBE_CONTEXT)" --kubeconfig "$(KUBECFG)"

.PHONY: kubeconfig
kubeconfig: ## (Re)write the local kubeconfig
	@rm -f $(KUBECFG)
	@$(MAKE) --no-print-directory $(KUBECFG)

.PHONY: preflight
preflight: $(KUBECFG) ## Verify tooling, cluster reachability and capacity
	@$(S)/preflight.sh

.PHONY: storage
storage: $(KUBECFG) ## Default gp3 StorageClass (EBS CSI driver comes from Terraform)
	@$(S)/storage.sh

.PHONY: cert-manager
cert-manager: $(KUBECFG) ## cert-manager + a self-signed ClusterIssuer
	@$(S)/cert-manager.sh

.PHONY: envoy
envoy: $(KUBECFG) ## Envoy Gateway + internal (and optional external) gateways
	@$(S)/envoy-gateway.sh

.PHONY: strimzi
strimzi: $(KUBECFG) ## Strimzi Kafka operator
	@$(S)/strimzi.sh

.PHONY: operator
operator: $(KUBECFG) ## Ditto Operator
	@$(S)/ditto-operator.sh

.PHONY: portal
portal: $(KUBECFG) ## Portal UI via the internal gateway (needs PORTAL=1)
	@PORTAL=1 $(S)/ditto-operator.sh
	@PORTAL=1 $(S)/portal.sh

.PHONY: bootstrap
bootstrap: preflight storage cert-manager envoy strimzi operator ## All cluster dependencies
	@$(S)/portal.sh
	@echo "==> bootstrap complete"

.PHONY: bigpeer
bigpeer: $(KUBECFG) ## BigPeer CR + its HTTPRoutes
	@$(S)/bigpeer.sh

.PHONY: app
app: $(KUBECFG) ## Create an app + API key (writes .local/app.env)
	@$(S)/app.sh

.PHONY: smoke
smoke: $(KUBECFG) ## Insert and query a document through the gateway
	@$(S)/smoke.sh

.PHONY: all
all: kubeconfig bootstrap bigpeer app smoke ## Everything, end to end
	@echo "==> all done"

.PHONY: status
status: $(KUBECFG) ## Show the state of every component
	@$(S)/status.sh

.PHONY: endpoint
endpoint: $(KUBECFG) ## Print the gateway endpoints
	@$(S)/status.sh endpoints

.PHONY: teardown
teardown: $(KUBECFG) ## Remove everything the Makefile installed (leaves Terraform alone)
	@$(S)/teardown.sh

.PHONY: clean
clean: ## Remove generated local files (.env, .kube/, .local/)
	rm -rf $(KUBEDIR) .local .env
