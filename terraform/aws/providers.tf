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
