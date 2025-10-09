module "this" {
  source = "../"

  controller_trusted_role_arns = [
    "arn:aws:iam::851725645787:role/controllers.cluster-api-provider-aws.sigs.k8s.io",
  ]

  iam_trusted_role_arns = [
    "arn:aws:iam::851725645787:role/trust-editor.ditto.live",
  ]

  iam_trusted_operations_principal_arns = "arn:aws:iam::851725645787:root"

  iam_trusted_operations_condition_arns = [
    "arn:aws:iam::851725645787:role/aws-reserved/sso.amazonaws.com/*"
  ]

  unrestricted = var.unrestricted
}
