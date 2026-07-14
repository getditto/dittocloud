
locals {
  tags = {
    "ditto.live/managed_by" = "dittocloud"
  }

  # Phase-2 lock-down: when cluster_name is set, IAM conditions switch from generic
  # Ditto tags to cluster-specific tags so the CAPA controller can only affect
  # resources belonging to this one cluster.
  # VPC confinement must only be added to actions and resource types that expose
  # ec2:Vpc. Launch templates, volumes, and instances do not expose that key, while
  # security groups, subnets, and network interfaces do.
  ec2_vpc_arn = var.vpc_id != null ? "arn:aws:ec2:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:vpc/${var.vpc_id}" : null

  # ec2:Vpc StringEquals entries — empty map when vpc_id is not set.
  ec2_vpc_string_equals = var.vpc_id != null ? {
    "ec2:Vpc" = local.ec2_vpc_arn
  } : {}

  # Creation-time cluster ownership. Only use this with actions that support
  # aws:RequestTag; CreateLaunchTemplateVersion is an existing-resource mutation.
  ec2_create_cond_entries = var.cluster_name != null ? {
    "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
  } : {}
  ec2_create_cond = length(local.ec2_create_cond_entries) > 0 ? {
    StringEquals = local.ec2_create_cond_entries
  } : null

  # Existing-resource cluster ownership. This is intentionally independent of
  # VPC confinement because many EC2 resource types do not expose ec2:Vpc.
  ec2_resource_cond_entries = var.cluster_name != null ? {
    "ec2:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned"
  } : {}
  ec2_resource_cond = length(local.ec2_resource_cond_entries) > 0 ? {
    StringEquals = local.ec2_resource_cond_entries
  } : null

  # Existing resources that support both ownership tags and ec2:Vpc, such as
  # security groups and network interfaces.
  ec2_vpc_resource_cond_entries = merge(
    local.ec2_resource_cond_entries,
    local.ec2_vpc_string_equals,
  )
  ec2_vpc_resource_cond = length(local.ec2_vpc_resource_cond_entries) > 0 ? {
    StringEquals = local.ec2_vpc_resource_cond_entries
  } : null

  # VPC-only condition for RunInstances resource contexts. Cluster request tags
  # are enforced separately on the instance resource because referenced resources
  # such as AMIs and subnets do not expose aws:RequestTag.
  ec2_vpc_cond = length(local.ec2_vpc_string_equals) > 0 ? {
    StringEquals = local.ec2_vpc_string_equals
  } : null

  # ELB LB/TG creates — cluster tag must be present (phase 1) or match exactly (phase 2)
  elb_create_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({ StringEquals = { "aws:RequestTag/elbv2.k8s.aws/cluster" = var.cluster_name } })
    : jsonencode({ Null = { "aws:RequestTag/elbv2.k8s.aws/cluster" = "false" } })
  )

  # ELB LB/TG/target-group mutations — cluster tag on existing resource
  elb_resource_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({ StringEquals = { "aws:ResourceTag/elbv2.k8s.aws/cluster" = var.cluster_name } })
    : jsonencode({ Null = { "aws:ResourceTag/elbv2.k8s.aws/cluster" = "false" } })
  )

  # ELB AddTags at LB/TG creation — combined CreateAction + cluster tag check
  elb_add_tags_create_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({
      StringEquals = {
        "elasticloadbalancing:CreateAction"    = ["CreateLoadBalancer", "CreateTargetGroup"]
        "aws:RequestTag/elbv2.k8s.aws/cluster" = var.cluster_name
      }
    })
    : jsonencode({
      StringEquals = { "elasticloadbalancing:CreateAction" = ["CreateLoadBalancer", "CreateTargetGroup"] }
      Null         = { "aws:RequestTag/elbv2.k8s.aws/cluster" = "false" }
    })
  )

  # ELB AddTags/RemoveTags on existing LBs/TGs
  # Phase 1: cluster tag must not be in request AND must exist on resource (prevents tag hijacking)
  # Phase 2: resource must belong to this cluster
  elb_tag_mutation_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({ StringEquals = { "aws:ResourceTag/elbv2.k8s.aws/cluster" = var.cluster_name } })
    : jsonencode({
      Null = {
        "aws:RequestTag/elbv2.k8s.aws/cluster"  = "true"
        "aws:ResourceTag/elbv2.k8s.aws/cluster" = "false"
      }
    })
  )
}
