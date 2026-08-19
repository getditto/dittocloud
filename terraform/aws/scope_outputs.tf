locals {
  scope_outputs_enabled = local.scope_mode && local.default_scope_ref != null

  scope_vpc_outputs = local.scope_outputs_enabled ? {
    for scope_ref, scope in var.deployment_scopes : scope_ref => {
      vpc_id = scope.vpc.mode == "dittocloud" ? (
        scope.default ? module.vpc[0].vpc_id : module.scoped_vpc[scope_ref].vpc_id
      ) : scope.vpc.id
      private_subnet_ids = scope.vpc.mode == "dittocloud" ? sort(
        scope.default ? module.vpc[0].vpc.private_subnets : module.scoped_vpc[scope_ref].vpc.private_subnets
        ) : scope.vpc.mode == "existing" ? sort(
        scope.default ? data.aws_subnets.customer_managed_private[0].ids : data.aws_subnets.scoped_existing_private[scope_ref].ids
      ) : []
      public_subnet_ids = scope.vpc.mode == "dittocloud" ? sort(
        scope.default ? module.vpc[0].vpc.public_subnets : module.scoped_vpc[scope_ref].vpc.public_subnets
        ) : scope.vpc.mode == "existing" ? sort(
        scope.default ? data.aws_subnets.customer_managed_public[0].ids : data.aws_subnets.scoped_existing_public[scope_ref].ids
      ) : []
      database_subnet_ids = scope.vpc.mode == "dittocloud" ? sort(
        scope.default ? module.vpc[0].vpc.database_subnets : module.scoped_vpc[scope_ref].vpc.database_subnets
      ) : []
      iam_subnet_ids = sort(
        scope.default ? local.effective_vpc_subnet_ids : local.scoped_effective_vpc_subnet_ids[scope_ref]
      )
    }
  } : {}

  scope_iam_outputs = local.scope_outputs_enabled ? {
    for scope_ref, scope in var.deployment_scopes : scope_ref => (
      scope.default ? module.cross_account_iam[0].scope_iam : module.scoped_cross_account_iam[scope_ref].scope_iam
    )
  } : {}

  aws_scope_outputs = local.scope_outputs_enabled ? {
    for scope_ref, scope in var.deployment_scopes : scope_ref => {
      scopeRef              = scope_ref
      default               = scope.default
      accountId             = data.aws_caller_identity.current.account_id
      region                = scope.region
      clusterType           = scope.cluster_type
      scopeTagPolicyVersion = scope.scope_tag_policy_version

      controllerRoleName = local.scope_iam_outputs[scope_ref].controller_role.name
      controllerRoleArn  = local.scope_iam_outputs[scope_ref].controller_role.arn

      trustEditorRoleName = local.scope_iam_outputs[scope_ref].trust_editor_role.name
      trustEditorRoleArn  = local.scope_iam_outputs[scope_ref].trust_editor_role.arn

      managedClusterRolePath = local.scope_iam_outputs[scope_ref].managed_cluster_role_path

      nodesRoleName            = local.scope_iam_outputs[scope_ref].nodes.role_name
      nodesRoleArn             = local.scope_iam_outputs[scope_ref].nodes.role_arn
      nodesInstanceProfileName = local.scope_iam_outputs[scope_ref].nodes.instance_profile_name
      nodesInstanceProfileArn  = local.scope_iam_outputs[scope_ref].nodes.instance_profile_arn

      controlPlaneRoleName            = local.scope_iam_outputs[scope_ref].control_plane.role_name
      controlPlaneRoleArn             = local.scope_iam_outputs[scope_ref].control_plane.role_arn
      controlPlaneInstanceProfileName = local.scope_iam_outputs[scope_ref].control_plane.instance_profile_name
      controlPlaneInstanceProfileArn  = local.scope_iam_outputs[scope_ref].control_plane.instance_profile_arn

      eksControlPlaneRoleName = try(local.scope_iam_outputs[scope_ref].eks_control_plane_role.name, null)
      eksControlPlaneRoleArn  = try(local.scope_iam_outputs[scope_ref].eks_control_plane_role.arn, null)

      clusterBoundaryName  = local.scope_iam_outputs[scope_ref].policies.cluster_resources_boundary.name
      clusterBoundaryArn   = local.scope_iam_outputs[scope_ref].policies.cluster_resources_boundary.arn
      externalBoundaryName = local.scope_iam_outputs[scope_ref].policies.cluster_external_boundary.name
      externalBoundaryArn  = local.scope_iam_outputs[scope_ref].policies.cluster_external_boundary.arn

      karpenterInterruptionQueueName = scope.cluster_type == "eks" ? (
        scope.default ? aws_sqs_queue.karpenter_interruption[0].name : aws_sqs_queue.scoped_karpenter_interruption[scope_ref].name
      ) : null
      karpenterInterruptionQueueArn = scope.cluster_type == "eks" ? (
        scope.default ? aws_sqs_queue.karpenter_interruption[0].arn : aws_sqs_queue.scoped_karpenter_interruption[scope_ref].arn
      ) : null

      vpcId             = local.scope_vpc_outputs[scope_ref].vpc_id
      privateSubnetIds  = local.scope_vpc_outputs[scope_ref].private_subnet_ids
      publicSubnetIds   = local.scope_vpc_outputs[scope_ref].public_subnet_ids
      databaseSubnetIds = local.scope_vpc_outputs[scope_ref].database_subnet_ids
      iamSubnetIds      = local.scope_vpc_outputs[scope_ref].iam_subnet_ids
    }
  } : {}

  scope_regions = sort(distinct([
    for scope in values(var.deployment_scopes) : scope.region
  ]))
  scope_refs_by_region = {
    for region in local.scope_regions : region => sort([
      for scope_ref, scope in var.deployment_scopes : scope_ref
      if scope.region == region
    ])
  }

  # The workload tiers are published separately from aws_scope_outputs, which
  # carries the IAM and load-balancer surface. Only a Dittocloud-managed VPC has
  # workload subnets to report, and every value here comes from the same module,
  # so each element of this map keeps one consistent type.
  aws_workload_networking_output = {
    for scope_ref, scope in var.deployment_scopes : scope_ref => {
      scopeRef      = scope_ref
      secondaryCidr = scope.vpc.secondary_cidr
      podSubnetIds = sort(
        scope.default ? module.vpc[0].vpc.pod_subnets : module.scoped_vpc[scope_ref].vpc.pod_subnets
      )
      nodeSubnetIds = sort(
        scope.default ? module.vpc[0].vpc.node_subnets : module.scoped_vpc[scope_ref].vpc.node_subnets
      )
      # One ENIConfig per availability zone is what VPC CNI custom networking
      # needs to place pods in the pod subnet instead of the node subnet.
      podSubnetsByAz = [
        for entry in(scope.default ? module.vpc[0].pod_subnets_by_az : module.scoped_vpc[scope_ref].pod_subnets_by_az) : {
          availabilityZone = entry.availability_zone
          subnetId         = entry.subnet_id
        }
      ]
      natPublicIps = scope.default ? module.vpc[0].nat_public_ips : module.scoped_vpc[scope_ref].nat_public_ips
    }
    if local.scope_outputs_enabled && scope.vpc.mode == "dittocloud"
  }

  aws_regional_resources_output = {
    for region, scope_refs in local.scope_refs_by_region : region => {
      region    = region
      scopeRefs = scope_refs
      ec2InstanceMetadataDefaults = contains(keys(local.eks_scope_refs_by_region), region) ? {
        requiredByScopeRefs     = local.eks_scope_refs_by_region[region]
        httpTokens              = region == local.root_region ? aws_ec2_instance_metadata_defaults.imdsv2[0].http_tokens : aws_ec2_instance_metadata_defaults.scoped_imdsv2[region].http_tokens
        httpPutResponseHopLimit = region == local.root_region ? aws_ec2_instance_metadata_defaults.imdsv2[0].http_put_response_hop_limit : aws_ec2_instance_metadata_defaults.scoped_imdsv2[region].http_put_response_hop_limit
      } : null
    }
    if local.scope_outputs_enabled
  }
}
