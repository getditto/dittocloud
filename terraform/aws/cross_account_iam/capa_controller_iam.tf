/*

R O L E

*/

module "capa_controller_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-assumable-role"
  version = "5.48.0"

  create_role      = true
  role_name        = "controllers.cluster-api-provider-aws.sigs.k8s.io"
  role_description = "Ditto Cross Account Infrastructure Controller"
  # role_path               = "/ditto/"
  role_requires_mfa       = false
  custom_role_policy_arns = [aws_iam_policy.capa_controller_policy.arn]
  trusted_role_arns       = var.controller_trusted_role_arns
}


/*

P O L I C I E S

*/

resource "aws_iam_policy" "capa_controller_policy" {
  name   = "ditto-capa-controller-policy"
  policy = file("${path.module}/policies/capa-controller-policy.json")
  tags   = local.tags
}
