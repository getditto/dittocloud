locals {
  azs = coalesce(var.deployment.vpc.availability_zones, [
    data.aws_availability_zones.available.names[0],
    data.aws_availability_zones.available.names[1],
    data.aws_availability_zones.available.names[2],
  ])
}

data "aws_availability_zones" "available" {
  state = "available"
}

# Managed VPC: created only when vpc.create is true.
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "6.6.0"
  count   = var.deployment.vpc.create ? 1 : 0

  name = "${var.deployment.cluster.name}-vpc"
  cidr = var.deployment.vpc.cidr

  azs             = local.azs
  private_subnets = var.deployment.vpc.private_subnets
  public_subnets  = var.deployment.vpc.public_subnets

  enable_nat_gateway = true
  single_nat_gateway = false

  enable_dns_hostnames = true
  enable_dns_support   = true

  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = "1"
  }

  public_subnet_tags = {
    "kubernetes.io/role/elb" = "1"
  }

  tags = var.tags
}

# Existing VPC: subnet IDs are supplied directly.
data "aws_vpc" "existing" {
  count = var.deployment.vpc.create ? 0 : 1
  id    = var.deployment.vpc.vpc_id
}

locals {
  vpc_id = var.deployment.vpc.create ? module.vpc[0].vpc_id : data.aws_vpc.existing[0].id

  private_subnet_ids = var.deployment.vpc.create ? module.vpc[0].private_subnets : var.deployment.vpc.private_subnet_ids
  public_subnet_ids  = var.deployment.vpc.create ? module.vpc[0].public_subnets : var.deployment.vpc.public_subnet_ids
}
