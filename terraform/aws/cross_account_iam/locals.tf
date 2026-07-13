
locals {
  tags = {
    "ditto.live/managed_by" = "dittocloud"
  }

  # Phase-2 lock-down: when cluster_name is set, IAM conditions switch from generic
  # Ditto tags to cluster-specific tags so the CAPA controller can only affect
  # resources belonging to this one cluster.
  # VPC confinement: when vpc_id is set, EC2 conditions also include an ec2:Vpc
  # constraint so the controller cannot create or modify resources outside that VPC.
  # CreateSecurityGroup is special: AWS authorizes it against the target VPC ARN,
  # so that statement uses vpc_arn as its Resource instead of an ec2:Vpc condition.
  #
  # Both constraints use StringEquals and are merged into a single Condition block —
  # IAM does not allow duplicate Condition operator keys in one statement.

  vpc_arn = var.vpc_id != null ? "arn:aws:ec2:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:vpc/${var.vpc_id}" : null

  # ec2:Vpc StringEquals entries — empty map when vpc_id is not set
  ec2_vpc_string_equals = var.vpc_id != null ? {
    "ec2:Vpc" = local.vpc_arn
  } : {}

  # EC2 creates (RunInstances, CreateLaunchTemplate)
  # Cluster scope added in phase-2; VPC scope added when vpc_id is set.
  # Nil when neither is set (phase-1, no vpc_id) → no condition applied.
  ec2_create_cond_entries = merge(
    var.cluster_name != null ? { "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}" = "owned" } : {},
    local.ec2_vpc_string_equals
  )
  ec2_create_cond = length(local.ec2_create_cond_entries) > 0 ? {
    StringEquals = local.ec2_create_cond_entries
  } : null

  # Security group creates are authorized on the VPC resource and carry CAPA tags
  # using the sigs.k8s.io namespace, not the kubernetes.io node ownership tag.
  ec2_security_group_create_cond = var.cluster_name != null ? {
    StringEquals = {
      "aws:RequestTag/sigs.k8s.io/cluster-api-provider-aws/cluster/${var.cluster_name}" = "owned"
    }
  } : null

  # EC2 resource mutations — terminate, delete, modify on existing resources
  # Cluster scope added in phase-2; VPC scope added when vpc_id is set.
  ec2_resource_cond_entries = merge(
    var.cluster_name != null ? { "ec2:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned" } : {},
    local.ec2_vpc_string_equals
  )
  ec2_resource_cond = length(local.ec2_resource_cond_entries) > 0 ? {
    StringEquals = local.ec2_resource_cond_entries
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
