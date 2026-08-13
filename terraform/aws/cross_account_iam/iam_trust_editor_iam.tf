# We create this role and boundary to allow the Ditto team to manage IAM roles for cluster resources.
#
# The roles can only be created within a specific path and should have the boundary policy attached to them.
# The boundary policy restricts the resources that can be managed by the role to only the resources within the cluster.
/*

R O L E

*/

module "iam_trust_editor_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role"
  version = "6.4.0"

  create          = true
  name            = local.iam_names.trust_editor_role
  use_name_prefix = false
  description     = "Ditto Cross Account IAM trust editor role"
  path            = "/ditto/"
  tags            = local.scope_identity_enabled ? local.tags : {}

  trust_policy_permissions = {
    TrustedRoles = {
      actions = ["sts:AssumeRole", "sts:TagSession"]
      principals = [
        {
          type        = "AWS"
          identifiers = var.iam_trusted_role_arns
        }
      ]
    }
  }

  policies = {
    iam-trust-editor = aws_iam_policy.iam_trust_editor_policy.arn
  }
}

/*

P O L I C I E S

*/
# This policy is a restricted set and the default policy for the trust editor role.
# It includes restrictions on the resources that can be managed by the role. Including locking
# Roles with the boundary policy.
resource "aws_iam_policy" "iam_trust_editor_policy" {
  name = local.iam_names.trust_editor_policy
  policy = templatefile("${path.module}/policies/assume-trust-policy.json.tpl", {
    managed_role_arn     = local.managed_cluster_role_arn
    boundary_policy_arns = local.boundary_policy_arns
  })
  tags = local.tags
}

resource "aws_iam_policy" "cluster_resources_boundary_policy" {
  name = local.iam_names.cluster_resources_boundary
  policy = templatefile("${path.module}/policies/cluster-resources-boundary-policy.json.tpl", {
    ec2_project_tag         = var.ec2_project_tag
    vpc_arn                 = local.ec2_vpc_arn
    karpenter_queue_arn     = local.karpenter_queue_arn
    capa_pass_role_resource = local.scope_enabled ? jsonencode(local.capa_boundary_pass_role_arns) : jsonencode(one(local.capa_boundary_pass_role_arns))
    cluster_secret_arn      = local.cluster_secret_arn
  })
  tags = local.tags
}

# Split out from cluster_resources_boundary_policy: ELB/ACM/Shield/WAF
# actions scale with vpc_subnet_ids, not scope_ref, so keeping them separate
# gives each policy far more headroom under IAM's 6,144-char limit.
resource "aws_iam_policy" "cluster_resources_elb_boundary_policy" {
  name = local.iam_names.cluster_resources_elb_boundary
  policy = templatefile("${path.module}/policies/cluster-resources-elb-boundary-policy.json.tpl", {
    vpc_arn        = local.ec2_vpc_arn
    vpc_subnet_ids = var.vpc_subnet_ids
  })
  tags = local.tags
}

resource "aws_iam_policy" "cluster_external_resources_boundary_policy" {
  name = local.iam_names.cluster_external_boundary
  policy = templatefile("${path.module}/policies/cluster-external-resources-boundary-policy.json.tpl", {
    cluster_secret_arn = local.cluster_secret_arn
  })
  tags = local.tags
}
