module "vpc" {
  source = "./vpc"

  region   = var.region
  vpc_name = var.vpc_name
  vpc_cidr = var.vpc_cidr
  tags     = var.tags
}

module "cross_account_iam" {
  source = "./cross_account_iam"

  # Variables used by the module
  controller_trusted_role_arns          = var.controller_trusted_role_arns
  iam_trusted_role_arns                 = var.iam_trusted_role_arns
  iam_trusted_operations_principal_arns = var.iam_trusted_operations_principal_arns
  iam_trusted_operations_condition_arns = var.iam_trusted_operations_condition_arns
}

data "aws_caller_identity" "current" {
  # This data source retrieves the current AWS account ID
  # and is used to set the account_id variable in the output
}

output "aws" {
  value = {
    account_id = data.aws_caller_identity.current.account_id
    region = var.region
    vpc = module.vpc
  }
}
