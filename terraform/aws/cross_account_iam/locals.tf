
locals {
  scope_enabled            = var.scope_ref != null
  effective_scope_identity = var.scope_identity_ref != null ? var.scope_identity_ref : var.scope_ref
  scope_identity_enabled   = local.effective_scope_identity != null
  effective_region         = coalesce(var.region, data.aws_region.current.region)

  iam_names = {
    controller_role                 = local.scope_enabled ? "ditto-capa-controller-${var.scope_ref}" : "controllers.cluster-api-provider-aws.sigs.k8s.io"
    trust_editor_role               = local.scope_enabled ? "ditto-iam-trust-editor-${var.scope_ref}" : "iam-trust-editor.ditto.live"
    nodes_role                      = local.scope_enabled ? "ditto-capa-nodes-${var.scope_ref}" : "nodes.cluster-api-provider-aws.sigs.k8s.io"
    nodes_instance_profile          = local.scope_enabled ? "ditto-capa-nodes-${var.scope_ref}" : "nodes.cluster-api-provider-aws.sigs.k8s.io"
    control_plane_role              = local.scope_enabled ? "ditto-capa-control-plane-${var.scope_ref}" : "control-plane.cluster-api-provider-aws.sigs.k8s.io"
    control_plane_instance_profile  = local.scope_enabled ? "ditto-capa-control-plane-${var.scope_ref}" : "control-plane.cluster-api-provider-aws.sigs.k8s.io"
    eks_control_plane_role          = local.scope_enabled ? "ditto-capa-eks-control-plane-${var.scope_ref}" : "eks-controlplane.cluster-api-provider-aws.sigs.k8s.io"
    trust_editor_policy             = local.scope_enabled ? "ditto-iam-trust-editor-policy-${var.scope_ref}" : "ditto-iam-trust-editor-policy"
    cluster_resources_boundary      = local.scope_enabled ? "ditto-cluster-resources-boundary-${var.scope_ref}" : "ditto-cluster-resources-boundary-policy"
    cluster_external_boundary       = local.scope_enabled ? "ditto-cluster-external-boundary-${var.scope_ref}" : "ditto-cluster-external-resources-boundary-policy"
    nodes_policy                    = local.scope_enabled ? "ditto-capa-nodes-policy-${var.scope_ref}" : "nodes.cluster-api-provider-aws.sigs.k8s.io"
    control_plane_policy            = local.scope_enabled ? "ditto-capa-control-plane-policy-${var.scope_ref}" : "control-plane.cluster-api-provider-aws.sigs.k8s.io"
    control_plane_tags_policy       = local.scope_enabled ? "ditto-capa-control-plane-tags-${var.scope_ref}" : "control-plane-tags.cluster-api-provider-aws.sigs.k8s.io"
    controller_base_policy          = local.scope_enabled ? "ditto-capa-controller-base-${var.scope_ref}" : "ditto-capa-controller-policy"
    controller_network_policy       = local.scope_enabled ? "ditto-capa-controller-network-${var.scope_ref}" : "ditto-capa-controller-network-policy"
    controller_elb_policy           = local.scope_enabled ? "ditto-capa-controller-elb-${var.scope_ref}" : "ditto-capa-controller-elb-policy"
    controller_vpc_lifecycle_policy = local.scope_enabled ? "ditto-capa-controller-vpc-lifecycle-${var.scope_ref}" : "ditto-capa-controller-vpc-lifecycle-policy"
    controller_eks_policy           = local.scope_enabled ? "ditto-capa-controller-eks-${var.scope_ref}" : "ditto-capa-controller-eks-policy"
    karpenter_queue                 = local.scope_enabled ? "karpenter-interruption-${var.scope_ref}" : "karpenter-interruption"
  }

  scoped_generated_names = local.scope_enabled ? {
    controller_role                = { kind = "IAM role", name = local.iam_names.controller_role, limit = 64, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    trust_editor_role              = { kind = "IAM role", name = local.iam_names.trust_editor_role, limit = 64, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    nodes_role                     = { kind = "IAM role", name = local.iam_names.nodes_role, limit = 64, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    nodes_instance_profile         = { kind = "IAM instance profile", name = local.iam_names.nodes_instance_profile, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    control_plane_role             = { kind = "IAM role", name = local.iam_names.control_plane_role, limit = 64, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    control_plane_instance_profile = { kind = "IAM instance profile", name = local.iam_names.control_plane_instance_profile, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    eks_control_plane_role         = { kind = "IAM role", name = local.iam_names.eks_control_plane_role, limit = 64, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    trust_editor_policy            = { kind = "IAM managed policy", name = local.iam_names.trust_editor_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    cluster_resources_boundary     = { kind = "IAM managed policy", name = local.iam_names.cluster_resources_boundary, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    cluster_external_boundary      = { kind = "IAM managed policy", name = local.iam_names.cluster_external_boundary, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    nodes_policy                   = { kind = "IAM managed policy", name = local.iam_names.nodes_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    control_plane_policy           = { kind = "IAM managed policy", name = local.iam_names.control_plane_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    control_plane_tags_policy      = { kind = "IAM managed policy", name = local.iam_names.control_plane_tags_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    controller_base_policy         = { kind = "IAM managed policy", name = local.iam_names.controller_base_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    controller_network_policy      = { kind = "IAM managed policy", name = local.iam_names.controller_network_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    controller_elb_policy          = { kind = "IAM managed policy", name = local.iam_names.controller_elb_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    controller_vpc_lifecycle       = { kind = "IAM managed policy", name = local.iam_names.controller_vpc_lifecycle_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    controller_eks_policy          = { kind = "IAM managed policy", name = local.iam_names.controller_eks_policy, limit = 128, pattern = "^[A-Za-z0-9_+=,.@-]+$" }
    karpenter_queue                = { kind = "SQS queue", name = local.iam_names.karpenter_queue, limit = 80, pattern = "^[A-Za-z0-9_-]+$" }
  } : {}

  managed_cluster_role_path = local.scope_enabled ? "/dittocluster/${var.scope_ref}/" : "/dittocluster/"
  managed_cluster_role_arn  = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role${local.managed_cluster_role_path}*"
  boundary_policy_arns = [
    "arn:aws:iam::${data.aws_caller_identity.current.account_id}:policy/${local.iam_names.cluster_resources_boundary}",
    "arn:aws:iam::${data.aws_caller_identity.current.account_id}:policy/${local.iam_names.cluster_external_boundary}",
  ]
  capa_pass_role_arns = local.scope_enabled ? [
    for role_name in [
      local.iam_names.nodes_role,
      local.iam_names.control_plane_role,
      local.iam_names.eks_control_plane_role,
    ] : "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${role_name}"
  ] : ["arn:*:iam::*:role/*.cluster-api-provider-aws.sigs.k8s.io"]
  capa_boundary_pass_role_arns = local.scope_enabled ? local.capa_pass_role_arns : [
    "arn:aws:iam::*:role/*.cluster-api-provider-aws.sigs.k8s.io",
  ]
  karpenter_queue_arn = local.scope_enabled ? "arn:aws:sqs:${local.effective_region}:${data.aws_caller_identity.current.account_id}:${local.iam_names.karpenter_queue}" : "arn:aws:sqs:*:*:karpenter-*"
  cluster_secret_arn  = local.scope_enabled ? "arn:aws:secretsmanager:*:*:secret:dittocluster/${var.scope_ref}/*" : "arn:aws:secretsmanager:*:*:secret:dittocluster/*"

  tags = merge(
    { "ditto.live/managed_by" = "dittocloud" },
    var.tags,
    local.scope_identity_enabled ? { "ditto.live/scope-ref" = local.effective_scope_identity } : {},
  )

  # Phase-2 lock-down: when cluster_name is set, IAM conditions switch from generic
  # Ditto tags to cluster-specific tags so the CAPA controller can only affect
  # resources belonging to this one cluster.
  # VPC confinement must only be added to actions and resource types that expose
  # ec2:Vpc. Launch templates, volumes, and instances do not expose that key, while
  # security groups, subnets, and network interfaces do.
  ec2_vpc_arn = var.vpc_id != null ? "arn:aws:ec2:${local.effective_region}:${data.aws_caller_identity.current.account_id}:vpc/${var.vpc_id}" : null

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

  ec2_tag_keys_cond = {
    "ForAllValues:StringLike" = {
      "aws:TagKeys" = [
        "kubernetes.io/*",
        "k8s.io/*",
        "sigs.k8s.io/*",
        "ditto.live/*",
        "Name",
        "MachineName",
      ]
    }
  }

  # CAPA writes this namespaced role tag in each managed resource's create
  # request. Treat its presence as an immutable bootstrap marker: creation-time
  # tagging may establish it, but a later CreateTags call cannot add the marker
  # to an arbitrary existing resource and then self-assign ownership.
  capa_role_tag_key = "sigs.k8s.io/cluster-api-provider-aws/role"

  ec2_existing_tag_string_equals = var.cluster_name != null ? {
    "ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/cluster/${var.cluster_name}" = "owned"
  } : {}

  ec2_existing_tag_cond = merge(
    local.ec2_tag_keys_cond,
    {
      Null = {
        "ec2:ResourceTag/${local.capa_role_tag_key}" = "false"
      }
    },
    length(local.ec2_existing_tag_string_equals) > 0 ? {
      StringEquals = local.ec2_existing_tag_string_equals
    } : {},
  )

  ec2_existing_vpc_tag_cond = merge(
    local.ec2_existing_tag_cond,
    length(merge(local.ec2_existing_tag_string_equals, local.ec2_vpc_string_equals)) > 0 ? {
      StringEquals = merge(local.ec2_existing_tag_string_equals, local.ec2_vpc_string_equals)
    } : {},
  )

  # EC2 performs a separate CreateTags authorization when tags are supplied to
  # a resource-creating API. ec2:CreateAction makes this path unusable for a
  # direct CreateTags call against an existing resource.
  ec2_create_tag_cond = merge(local.ec2_tag_keys_cond, {
    StringLike = {
      "ec2:CreateAction" = ["Create*", "RunInstances", "AllocateAddress"]
    }
  })

  # The node policy is also attached to control-plane instances, alongside AWS
  # managed policies that independently grant CreateTags. An explicit deny is
  # therefore required to stop those grants from establishing an ownership or
  # permission-gating tag on an existing resource without CAPA's bootstrap tag.
  ec2_protected_tag_assignment_deny_cond = {
    "ForAnyValue:StringLike" = {
      "aws:TagKeys" = [
        local.capa_role_tag_key,
        "sigs.k8s.io/cluster-api-provider-aws/cluster/*",
        "kubernetes.io/cluster/*",
        "ditto.live/managed_by",
        "ditto:project",
      ]
    }
    Null = {
      "ec2:CreateAction"                           = "true"
      "ec2:ResourceTag/${local.capa_role_tag_key}" = "true"
    }
  }

  # ELB does not expose a VPC condition key for load balancers. It does expose
  # the selected subnet IDs on CreateLoadBalancer and SetSubnets, so require
  # every requested subnet to belong to the configured VPC. An empty subnet
  # list fails closed when a VPC ID is configured.
  elb_vpc_subnet_cond = var.vpc_id != null ? {
    "ForAllValues:StringEquals" = {
      "elasticloadbalancing:Subnet" = var.vpc_subnet_ids
    }
    Null = {
      "elasticloadbalancing:Subnet" = "false"
    }
  } : null

  # ELB LB/TG creates — cluster tag must be present (phase 1) or match exactly (phase 2)
  elb_create_cond = jsondecode(
    var.cluster_name != null
    ? jsonencode({ StringEquals = { "aws:RequestTag/elbv2.k8s.aws/cluster" = var.cluster_name } })
    : jsonencode({ Null = { "aws:RequestTag/elbv2.k8s.aws/cluster" = "false" } })
  )

  elb_create_load_balancer_cond = merge(
    local.elb_create_cond,
    jsondecode(
      local.elb_vpc_subnet_cond != null
      ? jsonencode({
        "ForAllValues:StringEquals" = local.elb_vpc_subnet_cond["ForAllValues:StringEquals"]
        Null = merge(
          try(local.elb_create_cond.Null, {}),
          local.elb_vpc_subnet_cond.Null,
        )
      })
      : jsonencode({})
    ),
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
