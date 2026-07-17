terraform {
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
  # Scope mode must not enumerate or remove membership for other clusters.
  ignore_tags {
    key_prefixes = local.scope_mode ? [
      "kubernetes.io/cluster/",
      "sigs.k8s.io/cluster-api-provider-aws/cluster/",
    ] : []
    keys = local.scope_mode ? [
      "sigs.k8s.io/cluster-api-provider-aws/role",
    ] : []
  }
}
