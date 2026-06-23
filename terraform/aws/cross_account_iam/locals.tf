
locals {
  tags = {
    "ditto.live/managed_by" = "terraform"
  }

  # Phase-2 lock-down: when cluster_name is set, IAM conditions switch from generic
  # Ditto tags to cluster-specific tags so the CAPA controller can only affect
  # resources belonging to this one cluster.
  #
  # jsondecode(jsonencode(...)) coerces the ternary branches to a dynamic type,
  # which is required when the two branches have different object shapes
  # (e.g. {Null={...}} vs {StringEquals={...}}).

  # EC2 creates (RunInstances, CreateSecurityGroup, CreateLaunchTemplate)
  ec2_create_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({ StringEquals = { "aws:RequestTag/kubernetes.io/cluster/${var.cluster_name}" = "owned" } })
    : jsonencode({ StringEquals = { "aws:RequestTag/ditto.live/managed_by" = "terraform" } })
  )

  # EC2 resource mutations — terminate, delete, modify on existing resources
  ec2_resource_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({ StringEquals = { "ec2:ResourceTag/kubernetes.io/cluster/${var.cluster_name}" = "owned" } })
    : jsonencode({ StringEquals = { "ec2:ResourceTag/ditto.live/managed_by" = "terraform" } })
  )

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
