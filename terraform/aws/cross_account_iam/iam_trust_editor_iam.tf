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
  name            = "iam-trust-editor.ditto.live"
  use_name_prefix = false
  description     = "Ditto Cross Account IAM trust editor role"
  path            = "/ditto/"

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
  name = "ditto-iam-trust-editor-policy"
  policy = templatefile("${path.module}/policies/assume-trust-policy.json.tpl", {
    account_id = data.aws_caller_identity.current.account_id
  })
  tags = local.tags
}

resource "aws_iam_policy" "cluster_resources_boundary_policy" {
  name   = "ditto-cluster-resources-boundary-policy"
  policy = templatefile("${path.module}/policies/cluster-resources-boundary-policy.json.tpl", {
    ec2_project_tag = var.ec2_project_tag
    vpc_arn         = var.vpc_id != null ? "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:vpc/${var.vpc_id}" : null
  })
  tags   = local.tags
}

resource "aws_iam_policy" "cluster_external_resources_boundary_policy" {
  name   = "ditto-cluster-external-resources-boundary-policy"
  policy = file("${path.module}/policies/cluster-external-resources-boundary-policy.json")
  tags   = local.tags
}
