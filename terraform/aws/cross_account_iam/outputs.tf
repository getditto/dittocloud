output "scope_iam" {
  description = "Exact IAM names and ARNs for this module instance. Scoped consumers must use these values rather than reconstructing names."
  value = {
    scope_ref = var.scope_ref
    region    = local.effective_region
    confinement = {
      vpc_id     = var.vpc_id
      subnet_ids = sort(var.vpc_subnet_ids)
    }
    controller_role = {
      name = module.capa_controller_role.name
      arn  = module.capa_controller_role.arn
    }
    trust_editor_role = {
      name = module.iam_trust_editor_role.name
      arn  = module.iam_trust_editor_role.arn
    }
    nodes = {
      role_name             = aws_iam_role.capa_nodes.name
      role_arn              = aws_iam_role.capa_nodes.arn
      instance_profile_name = aws_iam_instance_profile.capa_nodes.name
      instance_profile_arn  = aws_iam_instance_profile.capa_nodes.arn
    }
    control_plane = {
      role_name             = aws_iam_role.capa_control_plane.name
      role_arn              = aws_iam_role.capa_control_plane.arn
      instance_profile_name = aws_iam_instance_profile.capa_control_plane.name
      instance_profile_arn  = aws_iam_instance_profile.capa_control_plane.arn
    }
    eks_control_plane_role = var.enable_eks ? {
      name = aws_iam_role.capa_eks_control_plane[0].name
      arn  = aws_iam_role.capa_eks_control_plane[0].arn
    } : null
    policies = {
      trust_editor               = { name = aws_iam_policy.iam_trust_editor_policy.name, arn = aws_iam_policy.iam_trust_editor_policy.arn }
      cluster_resources_boundary = { name = aws_iam_policy.cluster_resources_boundary_policy.name, arn = aws_iam_policy.cluster_resources_boundary_policy.arn }
      cluster_external_boundary  = { name = aws_iam_policy.cluster_external_resources_boundary_policy.name, arn = aws_iam_policy.cluster_external_resources_boundary_policy.arn }
      nodes                      = { name = aws_iam_policy.capa_nodes.name, arn = aws_iam_policy.capa_nodes.arn }
      control_plane              = { name = aws_iam_policy.capa_control_plane.name, arn = aws_iam_policy.capa_control_plane.arn }
      control_plane_tags         = { name = aws_iam_policy.capa_control_plane_tags.name, arn = aws_iam_policy.capa_control_plane_tags.arn }
      controller_base            = { name = aws_iam_policy.capa_controller_base.name, arn = aws_iam_policy.capa_controller_base.arn }
      controller_network         = { name = aws_iam_policy.capa_controller_network.name, arn = aws_iam_policy.capa_controller_network.arn }
      controller_elb             = { name = aws_iam_policy.capa_controller_elb.name, arn = aws_iam_policy.capa_controller_elb.arn }
      controller_vpc_lifecycle   = var.customer_managed_vpc ? null : { name = aws_iam_policy.capa_controller_vpc_lifecycle[0].name, arn = aws_iam_policy.capa_controller_vpc_lifecycle[0].arn }
      controller_eks             = var.enable_eks ? { name = aws_iam_policy.capa_controller_eks_policy[0].name, arn = aws_iam_policy.capa_controller_eks_policy[0].arn } : null
    }
    managed_cluster_role_path = local.managed_cluster_role_path
    karpenter_queue = var.enable_eks ? {
      name = local.iam_names.karpenter_queue
      arn  = local.karpenter_queue_arn
    } : null
  }
}
