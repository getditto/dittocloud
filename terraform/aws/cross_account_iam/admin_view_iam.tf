# We create this role to allow the Ditto team to view AWS resources.
#
# The role is linked to the AWS ManagedReadOnlyAccess policy.
/*

R O L E

*/

module "iam_admin_view_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-assumable-role"
  version = "5.48.0"

  create_role                     = true
  role_name                       = "iam-admin-view.ditto.live"
  role_description                = "Ditto Cross Account IAM admin view role"
  role_path                       = "/ditto/"
  role_requires_mfa               = false
  attach_readonly_policy          = true
  readonly_role_policy_arn        = "arn:aws:iam::aws:policy/ReadOnlyAccess"
  create_custom_role_trust_policy = true
  custom_role_trust_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          AWS = var.iam_trusted_operations_principal_arns
        }
        Action = "sts:AssumeRole"
        Condition = {
          ArnLike = {
            "aws:PrincipalArn" = var.iam_trusted_operations_condition_arns
          }
        }
      }
    ]
  })
}
