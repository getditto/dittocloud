terraform {
  # Verified by running terraform validate against 1.11.4, 1.12.2, 1.13.4 and
  # 1.15.2: 1.11 rejects an existing variable validation that calls length() on a
  # null optional input, and 1.12 is the first version that accepts the whole
  # configuration. Consumers apply this module directly, so the floor has to be
  # declared rather than assumed from the version CI happens to pin.
  required_version = ">= 1.12"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  profile = var.profile
  region  = local.root_region

  # Cluster API owns these open-ended tag namespaces on shared VPC resources.
  # Dittocloud never enumerates or removes another cluster's membership tags —
  # in scope mode or default mode — so a VPC shared with other kubeadm/EKS
  # clusters does not churn their Cluster API ownership tags on every apply.
  ignore_tags {
    key_prefixes = [
      "kubernetes.io/cluster/",
      "sigs.k8s.io/cluster-api-provider-aws/cluster/",
    ]
    keys = [
      "sigs.k8s.io/cluster-api-provider-aws/role",
    ]
  }
}
