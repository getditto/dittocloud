/*

R O L E

*/

module "capa_controller_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role"
  version = "6.4.0"

  create          = true
  name            = "controllers.cluster-api-provider-aws.sigs.k8s.io"
  use_name_prefix = false
  description     = "Ditto Cross Account Infrastructure Controller"

  trust_policy_permissions = {
    TrustedRoles = {
      actions = ["sts:AssumeRole", "sts:TagSession"]
      principals = [
        {
          type        = "AWS"
          identifiers = var.controller_trusted_role_arns
        }
      ]
    }
  }

  policies = {
    capa-controller = aws_iam_policy.capa_controller_policy.arn
  }
}


/*

P O L I C I E S

*/

resource "aws_iam_policy" "capa_controller_policy" {
  name   = "ditto-capa-controller-policy"
  policy = file("${path.module}/policies/capa-controller-policy.json")
  tags   = local.tags
}
