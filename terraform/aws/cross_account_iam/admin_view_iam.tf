# We create this role to allow the Ditto team to view AWS resources.
#
# The role is linked to the AWS ManagedReadOnlyAccess policy.
/*

R O L E

*/

module "iam_admin_view_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role"
  version = "6.4.0"

  create          = true
  name            = "iam-admin-view.ditto.live"
  use_name_prefix = false
  description     = "Ditto Cross Account IAM admin view role"
  path            = "/ditto/"

  trust_policy_permissions = {
    TrustedOperations = {
      actions = ["sts:AssumeRole", "sts:TagSession"]
      principals = [
        {
          type        = "AWS"
          identifiers = [var.iam_trusted_operations_principal_arns]
        }
      ]
      condition = [
        {
          test     = "ArnLike"
          variable = "aws:PrincipalArn"
          values   = var.iam_trusted_operations_condition_arns
        }
      ]
    }
  }

  policies = {
    readonly = "arn:aws:iam::aws:policy/ReadOnlyAccess"
  }
}
