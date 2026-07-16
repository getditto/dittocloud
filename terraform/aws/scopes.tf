locals {
  scope_mode = length(var.deployment_scopes) > 0
  default_scope_refs = sort([
    for scope_ref, scope in var.deployment_scopes : scope_ref
    if scope.default
  ])

  # Keep all downstream locals safe while variable validation reports a missing
  # or duplicate default. A usable default exists only after that contract is
  # satisfied.
  default_scope_ref = length(local.default_scope_refs) == 1 ? one(local.default_scope_refs) : null
  default_scope     = local.default_scope_ref != null ? var.deployment_scopes[local.default_scope_ref] : null
  non_default_scopes = {
    for scope_ref, scope in var.deployment_scopes : scope_ref => scope
    if !scope.default
  }

  root_region = local.default_scope != null ? local.default_scope.region : var.region

  default_create_iam           = local.scope_mode ? local.default_scope != null : var.create_iam
  default_vpc_mode             = local.default_scope != null ? local.default_scope.vpc.mode : null
  default_create_vpc           = local.scope_mode ? local.default_vpc_mode == "dittocloud" : var.create_vpc
  default_customer_managed_vpc = local.scope_mode ? local.default_vpc_mode == "existing" : var.customer_managed_vpc
  default_vpc_id               = local.default_scope != null ? local.default_scope.vpc.id : var.vpc_id
  default_vpc_name             = local.default_scope != null ? local.default_scope.vpc.name : var.vpc_name
  default_vpc_cidr             = local.default_scope != null ? local.default_scope.vpc.cidr : var.vpc_cidr
  default_cluster_name         = local.default_scope != null ? local.default_scope.cluster_name : var.cluster_name
  default_enable_eks           = local.scope_mode ? try(local.default_scope.cluster_type == "eks", false) : var.enable_eks

  # The legacy IMDS address belongs to the default Region, but a non-default EKS
  # scope in that same Region also requires the shared account+Region default.
  default_region_requires_eks = local.scope_mode ? anytrue([
    for scope in values(var.deployment_scopes) :
    scope.cluster_type == "eks" && scope.region == local.root_region
  ]) : var.enable_eks
}

resource "terraform_data" "scope_registry" {
  for_each = var.deployment_scopes

  input = {
    schema_version = 1
    scope_ref      = each.key
    default        = each.value.default
  }
}
