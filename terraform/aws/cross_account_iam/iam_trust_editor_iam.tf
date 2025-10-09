# We create this role and boundary to allow the Ditto team to manage IAM roles for cluster resources.
#
# The roles can only be created within a specific path and should have the boundary policy attached to them.
# The boundary policy restricts the resources that can be managed by the role to only the resources within the cluster.
/*

R O L E

*/

module "iam_trust_editor_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-assumable-role"
  version = "5.48.0"

  create_role             = true
  role_name               = "iam-trust-editor.ditto.live"
  role_description        = "Ditto Cross Account IAM trust editor role"
  role_path               = "/ditto/"
  role_requires_mfa       = false
  custom_role_policy_arns = var.unrestricted ? [aws_iam_policy.unrestricted_iam_trust_editor_policy[0].arn] : [aws_iam_policy.iam_trust_editor_policy[0].arn]
  trusted_role_arns       = var.iam_trusted_role_arns
}

/*

P O L I C I E S

*/
# This policy is a restricted set and the default policy for the trust editor role.
# It includes restrictions on the resources that can be managed by the role. Including locking
# Roles with the boundary policy.
resource "aws_iam_policy" "iam_trust_editor_policy" {
  count = var.unrestricted ? 0 : 1

  name = "ditto-iam-trust-editor-policy"
  policy = templatefile("${path.module}/policies/assume-trust-policy.json.tpl", {
    account_id = data.aws_caller_identity.current.account_id
  })
  tags = local.tags
}

# This policy is an unrestricted set and should not be used in production.
# It allows the trust editor role to manage any IAM resource with no restrictions!
resource "aws_iam_policy" "unrestricted_iam_trust_editor_policy" {
  count  = var.unrestricted ? 1 : 0
  name   = "ditto-iam-trust-editor-policy"
  policy = file("${path.module}/policies/unrestricted.json")
  tags   = local.tags
}

# @todo: The boundary policy should restrict Ec2 resources to appropriate tagged resources.
resource "aws_iam_policy" "cluster_resources_boundary_policy" {
  name   = "ditto-cluster-resources-boundary-policy"
  policy = file("${path.module}/policies/cluster-resources-boundary-policy.json")
  tags   = local.tags
}

resource "aws_iam_policy" "cluster_external_resources_boundary_policy" {
  name   = "ditto-cluster-external-resources-boundary-policy"
  policy = file("${path.module}/policies/cluster-external-resources-boundary-policy.json")
  tags   = local.tags
}
