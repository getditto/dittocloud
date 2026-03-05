module "vpc" {
  source = "./vpc"
  count  = var.create_vpc ? 1 : 0

  region   = var.region
  vpc_name = var.vpc_name
  vpc_cidr = var.vpc_cidr
  tags     = var.tags
}

module "cross_account_iam" {
  source = "./cross_account_iam"
  count  = var.create_iam ? 1 : 0

  # Variables used by the module
  controller_trusted_role_arns          = var.controller_trusted_role_arns
  iam_trusted_role_arns                 = var.iam_trusted_role_arns
  iam_trusted_operations_principal_arns = var.iam_trusted_operations_principal_arns
  iam_trusted_operations_condition_arns = var.iam_trusted_operations_condition_arns
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

output "aws" {
  value = {
    account_id = data.aws_caller_identity.current.account_id
    region     = coalesce(var.region, data.aws_region.current.name)
    vpc        = module.vpc
  }
}
