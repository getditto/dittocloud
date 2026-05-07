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

  unrestricted = var.unrestricted
}

# S3 bucket for storing Terraform state
resource "aws_s3_bucket" "terraform_state" {
  bucket = "ditto-terraform-state-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_versioning" "terraform_state_versioning" {
  bucket = aws_s3_bucket.terraform_state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "terraform_state_encryption" {
  bucket = aws_s3_bucket.terraform_state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "terraform_state_pab" {
  bucket = aws_s3_bucket.terraform_state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

output "aws" {
  value = {
    account_id = data.aws_caller_identity.current.account_id
    region     = coalesce(var.region, data.aws_region.current.id)
    vpc        = module.vpc
  }
}
