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
  non_default_dittocloud_scopes = {
    for scope_ref, scope in local.non_default_scopes : scope_ref => scope
    if scope.vpc.mode == "dittocloud"
  }
  non_default_existing_scopes = {
    for scope_ref, scope in local.non_default_scopes : scope_ref => scope
    if scope.vpc.mode == "existing"
  }
  non_default_eks_scopes = {
    for scope_ref, scope in local.non_default_scopes : scope_ref => scope
    if scope.cluster_type == "eks"
  }

  eks_regions = sort(distinct([
    for scope in values(var.deployment_scopes) : scope.region
    if scope.cluster_type == "eks"
  ]))
  eks_scope_refs_by_region = {
    for region in local.eks_regions : region => sort([
      for scope_ref, scope in var.deployment_scopes : scope_ref
      if scope.cluster_type == "eks" && scope.region == region
    ])
  }
  scoped_imds_scope_refs_by_region = {
    for region, scope_refs in local.eks_scope_refs_by_region : region => scope_refs
    if local.default_scope_ref != null && region != local.root_region
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
  default_nat_gateway_name     = local.default_scope != null ? local.default_scope.vpc.nat_gateway_name : null
  default_enable_eks           = local.scope_mode ? try(local.default_scope.cluster_type == "eks", false) : var.enable_eks
  default_scope_tags = local.scope_mode && local.default_scope_ref != null ? merge(
    var.tags,
    { "ditto.live/scope-ref" = local.default_scope_ref },
  ) : var.tags

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

# Keep the applied tag-policy version separate from the immutable identity
# registry. The IAM modules must finish before this marker advances so a
# partial apply cannot claim that a policy transition completed first.
resource "terraform_data" "scope_tag_policy" {
  for_each = var.deployment_scopes

  input = {
    schema_version = 1
    scope_ref      = each.key
    policy_version = each.value.scope_tag_policy_version
  }

  depends_on = [
    terraform_data.scope_registry,
    terraform_data.scope_configuration,
    module.cross_account_iam,
    module.scoped_cross_account_iam,
  ]
}

# Persist the complete normalized configuration that was last applied for each
# scope. Recovery reads these snapshots directly from state instead of trying
# to infer operator intent from resource outputs. Keep this separate from the
# immutable identity registry and the applied tag-policy marker.
resource "terraform_data" "scope_configuration" {
  for_each = var.deployment_scopes

  input = {
    schema_version = 1
    scope_ref      = each.key
    configuration = {
      default                  = each.value.default
      cluster_name             = each.value.cluster_name
      cluster_type             = each.value.cluster_type
      region                   = each.value.region
      scope_tag_policy_version = each.value.scope_tag_policy_version
      vpc = {
        mode             = each.value.vpc.mode
        name             = each.value.vpc.name
        cidr             = each.value.vpc.cidr
        id               = each.value.vpc.id
        nat_gateway_name = each.value.vpc.nat_gateway_name
      }
    }
  }

  # A snapshot is recovery evidence only after every resource class driven by
  # the scope configuration has completed. Static whole-resource references
  # conservatively cover all for_each/count instances and preserve safe
  # partial-apply behavior.
  depends_on = [
    terraform_data.scope_registry,
    terraform_data.validate_vpc_mode,
    terraform_data.scoped_karpenter_name_validation,
    module.vpc,
    module.scoped_vpc,
    module.cross_account_iam,
    module.scoped_cross_account_iam,
    aws_ec2_instance_metadata_defaults.imdsv2,
    aws_ec2_instance_metadata_defaults.scoped_imdsv2,
    aws_sqs_queue.karpenter_interruption,
    aws_sqs_queue_policy.karpenter_interruption,
    aws_cloudwatch_event_rule.karpenter_interruption,
    aws_cloudwatch_event_target.karpenter_interruption,
    aws_sqs_queue.scoped_karpenter_interruption,
    aws_sqs_queue_policy.scoped_karpenter_interruption,
    aws_cloudwatch_event_rule.scoped_karpenter_interruption,
    aws_cloudwatch_event_target.scoped_karpenter_interruption,
  ]
}
