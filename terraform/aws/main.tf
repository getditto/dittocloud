locals {
  # Terraform-created VPCs are confined automatically using the module output.
  # Existing customer VPCs use var.vpc_id. When both controls are false, Cluster
  # API may create the VPC and no VPC ID exists yet for initial confinement.
  create_dittocloud_vpc = var.create_vpc && !var.customer_managed_vpc
  effective_vpc_id      = local.create_dittocloud_vpc ? module.vpc[0].vpc_id : var.vpc_id
  dittocloud_vpc_subnet_ids = local.create_dittocloud_vpc ? concat(
    module.vpc[0].vpc.private_subnets,
    module.vpc[0].vpc.public_subnets,
  ) : []
  customer_vpc_subnet_ids = var.customer_managed_vpc && var.create_iam ? sort(distinct(concat(
    data.aws_subnets.customer_managed_private[0].ids,
    data.aws_subnets.customer_managed_public[0].ids,
  ))) : []
  effective_vpc_subnet_ids = local.create_dittocloud_vpc ? local.dittocloud_vpc_subnet_ids : local.customer_vpc_subnet_ids
}

# Existing-VPC mode discovers only the Kubernetes load-balancer subnets. This
# avoids granting ELB access to unrelated subnets in a shared VPC.
data "aws_subnets" "customer_managed_private" {
  count = var.customer_managed_vpc && var.create_iam ? 1 : 0

  filter {
    name   = "vpc-id"
    values = [var.vpc_id]
  }

  filter {
    name   = "tag:kubernetes.io/role/internal-elb"
    values = ["1"]
  }
}

data "aws_subnets" "customer_managed_public" {
  count = var.customer_managed_vpc && var.create_iam ? 1 : 0

  filter {
    name   = "vpc-id"
    values = [var.vpc_id]
  }

  filter {
    name   = "tag:kubernetes.io/role/elb"
    values = ["1"]
  }
}

module "vpc" {
  source = "./vpc"
  count  = local.create_dittocloud_vpc ? 1 : 0

  region   = var.region
  vpc_name = var.vpc_name
  vpc_cidr = var.vpc_cidr
  tags     = var.tags
}

module "cross_account_iam" {
  source = "./cross_account_iam"
  count  = var.create_iam ? 1 : 0

  # Variables used by the module
  controller_trusted_role_arns          = var.controller_trusted_role_arns
  iam_trusted_role_arns                 = var.iam_trusted_role_arns
  iam_trusted_operations_principal_arns = var.iam_trusted_operations_principal_arns
  iam_trusted_operations_condition_arns = var.iam_trusted_operations_condition_arns
  customer_managed_vpc                  = var.customer_managed_vpc
  cluster_name                          = var.cluster_name
  vpc_id                                = local.effective_vpc_id
  vpc_subnet_ids                        = local.effective_vpc_subnet_ids
}

resource "terraform_data" "validate_vpc_mode" {
  input = {
    customer_managed_vpc = var.customer_managed_vpc
    vpc_id               = var.vpc_id
  }

  lifecycle {
    precondition {
      condition     = !var.customer_managed_vpc || try(trimspace(var.vpc_id) != "", false)
      error_message = "vpc_id must be provided when customer_managed_vpc is true so IAM permissions can be restricted to the existing VPC."
    }
  }
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

output "aws" {
  value = {
    account_id = data.aws_caller_identity.current.account_id
    region     = coalesce(var.region, data.aws_region.current.region)
    vpc        = module.vpc
  }
}
