locals {
  # Terraform-created VPCs are confined automatically using the module output.
  # Existing customer VPCs use the selected default VPC ID. In CAPI mode, the
  # VPC may not exist yet and no VPC ID is available for initial confinement.
  create_dittocloud_vpc = local.default_create_vpc && !local.default_customer_managed_vpc
  effective_vpc_id      = local.create_dittocloud_vpc ? module.vpc[0].vpc_id : local.default_vpc_id
  dittocloud_vpc_subnet_ids = local.create_dittocloud_vpc ? concat(
    module.vpc[0].vpc.private_subnets,
    module.vpc[0].vpc.public_subnets,
  ) : []
  customer_vpc_subnet_ids = local.default_customer_managed_vpc && local.default_create_iam ? sort(distinct(concat(
    data.aws_subnets.customer_managed_private[0].ids,
    data.aws_subnets.customer_managed_public[0].ids,
  ))) : []
  effective_vpc_subnet_ids = local.create_dittocloud_vpc ? local.dittocloud_vpc_subnet_ids : local.customer_vpc_subnet_ids
}

# Existing-VPC mode discovers only the Kubernetes load-balancer subnets. This
# avoids granting ELB access to unrelated subnets in a shared VPC.
data "aws_subnets" "customer_managed_private" {
  count = local.default_customer_managed_vpc && local.default_create_iam ? 1 : 0

  filter {
    name   = "vpc-id"
    values = [local.default_vpc_id]
  }

  filter {
    name   = "tag:kubernetes.io/role/internal-elb"
    values = ["1"]
  }
}

data "aws_subnets" "customer_managed_public" {
  count = local.default_customer_managed_vpc && local.default_create_iam ? 1 : 0

  filter {
    name   = "vpc-id"
    values = [local.default_vpc_id]
  }

  filter {
    name   = "tag:kubernetes.io/role/elb"
    values = ["1"]
  }
}

# Non-default existing VPCs discover only their Kubernetes load-balancer
# subnets, using the owning scope's Region explicitly.
data "aws_subnets" "scoped_existing_private" {
  for_each = local.non_default_existing_scopes
  region   = each.value.region

  filter {
    name   = "vpc-id"
    values = [each.value.vpc.id]
  }

  filter {
    name   = "tag:kubernetes.io/role/internal-elb"
    values = ["1"]
  }
}

data "aws_subnets" "scoped_existing_public" {
  for_each = local.non_default_existing_scopes
  region   = each.value.region

  filter {
    name   = "vpc-id"
    values = [each.value.vpc.id]
  }

  filter {
    name   = "tag:kubernetes.io/role/elb"
    values = ["1"]
  }
}

module "vpc" {
  source = "./vpc"
  count  = local.create_dittocloud_vpc ? 1 : 0

  region                        = local.root_region
  vpc_name                      = local.default_vpc_name
  vpc_cidr                      = local.default_vpc_cidr
  manage_kubernetes_cluster_tag = false
  nat_gateway_name              = local.default_nat_gateway_name
  tags                          = local.default_scope_tags
}

module "scoped_vpc" {
  source   = "./vpc"
  for_each = local.non_default_dittocloud_scopes

  region                        = each.value.region
  vpc_name                      = each.value.vpc.name
  vpc_cidr                      = each.value.vpc.cidr
  manage_kubernetes_cluster_tag = false
  nat_gateway_name              = each.value.vpc.nat_gateway_name
  tags = merge(
    var.tags,
    { "ditto.live/scope-ref" = each.key },
  )
}

locals {
  scoped_effective_vpc_ids = {
    for scope_ref, scope in local.non_default_scopes : scope_ref => (
      scope.vpc.mode == "dittocloud" ? module.scoped_vpc[scope_ref].vpc_id :
      scope.vpc.mode == "existing" ? scope.vpc.id :
      null
    )
  }
  scoped_effective_vpc_subnet_ids = {
    for scope_ref, scope in local.non_default_scopes : scope_ref => (
      scope.vpc.mode == "dittocloud" ? sort(distinct(concat(
        module.scoped_vpc[scope_ref].vpc.private_subnets,
        module.scoped_vpc[scope_ref].vpc.public_subnets,
        ))) : scope.vpc.mode == "existing" ? sort(distinct(concat(
        data.aws_subnets.scoped_existing_private[scope_ref].ids,
        data.aws_subnets.scoped_existing_public[scope_ref].ids,
      ))) : []
    )
  }
}

module "cross_account_iam" {
  source = "./cross_account_iam"
  count  = local.default_create_iam ? 1 : 0

  # Variables used by the module
  controller_trusted_role_arns          = var.controller_trusted_role_arns
  iam_trusted_role_arns                 = var.iam_trusted_role_arns
  iam_trusted_operations_principal_arns = var.iam_trusted_operations_principal_arns
  iam_trusted_operations_condition_arns = var.iam_trusted_operations_condition_arns
  enable_eks                            = local.default_enable_eks
  customer_managed_vpc                  = local.default_customer_managed_vpc
  cluster_name                          = local.default_iam_cluster_name
  scope_identity_ref                    = local.scope_mode ? local.default_scope_ref : null
  vpc_id                                = local.effective_vpc_id
  additional_authorized_vpc_ids         = var.additional_authorized_vpc_ids
  vpc_subnet_ids                        = local.effective_vpc_subnet_ids
  tags                                  = local.default_scope_tags
}

module "scoped_cross_account_iam" {
  source   = "./cross_account_iam"
  for_each = local.non_default_scopes

  scope_ref                             = each.key
  region                                = each.value.region
  create_admin_view_role                = false
  controller_trusted_role_arns          = var.controller_trusted_role_arns
  iam_trusted_role_arns                 = var.iam_trusted_role_arns
  iam_trusted_operations_principal_arns = var.iam_trusted_operations_principal_arns
  iam_trusted_operations_condition_arns = var.iam_trusted_operations_condition_arns
  enable_eks                            = each.value.cluster_type == "eks"
  customer_managed_vpc                  = each.value.vpc.mode == "existing"
  cluster_name                          = local.scoped_iam_cluster_names[each.key]
  vpc_id                                = local.scoped_effective_vpc_ids[each.key]
  vpc_subnet_ids                        = local.scoped_effective_vpc_subnet_ids[each.key]
  tags                                  = var.tags
}

resource "terraform_data" "validate_vpc_mode" {
  input = {
    customer_managed_vpc = local.default_customer_managed_vpc
    vpc_id               = local.default_vpc_id
  }

  lifecycle {
    precondition {
      condition     = !local.default_customer_managed_vpc || try(trimspace(local.default_vpc_id) != "", false)
      error_message = "vpc_id must be provided when customer_managed_vpc is true so IAM permissions can be restricted to the existing VPC."
    }
  }
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

output "aws" {
  value = merge(
    {
      account_id = data.aws_caller_identity.current.account_id
      region     = coalesce(local.root_region, data.aws_region.current.region)
      vpc        = module.vpc
    },
    {
      for key, value in { scopes = local.aws_scope_outputs } : key => value
      if local.scope_outputs_enabled
    },
    {
      for key, value in { regionalResources = local.aws_regional_resources_output } : key => value
      if local.scope_outputs_enabled
    },
  )
}
