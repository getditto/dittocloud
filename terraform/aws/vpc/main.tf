data "aws_availability_zones" "available" {}
data "aws_region" "current" {}

locals {
  name   = var.vpc_name
  region = data.aws_region.current.name

  vpc_cidr = var.vpc_cidr
  azs      = slice(data.aws_availability_zones.available.names, 0, 3)

  private_subnets = {
    for az in local.azs :
    "private-${az}" => {
      name    = "private-${az}"
      netmask = 18
      type    = "Private"
      zone    = az
    }
  }

  public_subnets = {
    for az in local.azs :
    "public-${az}" => {
      name    = "public-${az}"
      netmask = 22
      type    = "Public"
      zone    = az
    }
  }

  # db_subnets = {
  #   for az in local.azs :
  #   "db-${az}" => {
  #     name    = "db-${az}"
  #     netmask = 24
  #     type    = "Database"
  #     zone    = az
  #   }
  # }

  // us-east-1 uses ec2.internal, all other regions use <region>.compute.internal
  dhcp_domain = var.region == "us-east-1" ? "ec2.internal" : "${var.region}.compute.internal"
}


################################################################################
# Subnet Calculator
################################################################################

module "subnets" {
  source  = "drewmullen/subnets/cidr"
  version = "1.0.2"

  base_cidr_block = local.vpc_cidr

  networks = flatten([
    # [for sub in local.db_subnets :
    #   {
    #     name    = sub.name,
    #     netmask = sub.netmask
    # }],
    [for sub in local.public_subnets :
      {
        name    = sub.name,
        netmask = sub.netmask
    }],
    [for sub in local.private_subnets :
      {
        name    = sub.name,
        netmask = sub.netmask
    }],

  ])
}


################################################################################
# VPC Module
################################################################################

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "6.6.0"

  name = local.name
  cidr = local.vpc_cidr

  azs             = local.azs
  private_subnets = [for subnet in local.private_subnets : module.subnets.network_cidr_blocks[subnet.name]]
  public_subnets  = [for subnet in local.public_subnets : module.subnets.network_cidr_blocks[subnet.name]]
  # database_subnets = [for subnet in local.db_subnets : module.subnets.network_cidr_blocks[subnet.name]]

  private_subnet_tags = {
    "kubernetes.io/cluster/ditto"     = "shared"
    "kubernetes.io/role/internal-elb" = "1"
  }

  public_subnet_tags = {
    "kubernetes.io/cluster/ditto" = "shared"
    "kubernetes.io/role/elb"      = "1"
  }

  # Capi might delete those ^

  create_elasticache_subnet_group = false
  create_redshift_subnet_group    = false
  create_database_subnet_group    = true
  manage_default_network_acl      = true
  manage_default_route_table      = true
  manage_default_security_group   = true

  enable_dns_hostnames = true
  enable_dns_support   = true

  enable_nat_gateway = true
  single_nat_gateway = false

  enable_vpn_gateway = false

  enable_dhcp_options      = true
  dhcp_options_domain_name = local.dhcp_domain
  # dhcp_options_domain_name_servers = ["127.0.0.1", "10.10.0.2"]

  # VPC Flow Logs (Cloudwatch log group and IAM role will be created)
  # vpc_flow_log_iam_role_name            = "vpc-complete-example-role"
  # vpc_flow_log_iam_role_use_name_prefix = false
  # enable_flow_log                       = false
  # create_flow_log_cloudwatch_log_group  = false
  # create_flow_log_cloudwatch_iam_role   = false
  # flow_log_max_aggregation_interval     = 60

  tags = var.tags
}

################################################################################
# VPC Endpoints
################################################################################

module "vpc_endpoints" {
  source  = "terraform-aws-modules/vpc/aws//modules/vpc-endpoints"
  version = "6.6.0"

  vpc_id = module.vpc.vpc_id

  create_security_group      = true
  security_group_name_prefix = "${local.name}-vpc-endpoints-"
  security_group_description = "VPC endpoint security group"
  security_group_rules = {
    ingress_https = {
      description = "HTTPS from VPC"
      cidr_blocks = [module.vpc.vpc_cidr_block]
    }
  }

  endpoints = {
    s3 = {
      service             = "s3"
      private_dns_enabled = true
      subnet_ids          = module.vpc.private_subnets
      tags                = { Name = "s3-vpc-endpoint" }
      service_type        = "Gateway"
    },
    ecr_api = {
      service             = "ecr.api"
      private_dns_enabled = true
      subnet_ids          = module.vpc.private_subnets
      policy              = data.aws_iam_policy_document.generic_endpoint_policy.json
      tags                = { Name = "ecr-api-endpoint" }
    },
    ecr_dkr = {
      service             = "ecr.dkr"
      private_dns_enabled = true
      subnet_ids          = module.vpc.private_subnets
      policy              = data.aws_iam_policy_document.generic_endpoint_policy.json
      tags                = { Name = "ecr-dkr-endpoint" }
    },
  }

  tags = var.tags
}

################################################################################
# Supporting Resources
################################################################################

data "aws_iam_policy_document" "generic_endpoint_policy" {
  statement {
    effect    = "Deny"
    actions   = ["*"]
    resources = ["*"]

    principals {
      type        = "*"
      identifiers = ["*"]
    }

    condition {
      test     = "StringNotEquals"
      variable = "aws:SourceVpc"

      values = [module.vpc.vpc_id]
    }
  }
}

output "vpc" {
  value = {
    private_subnets = module.vpc.private_subnets,
    public_subnets  = module.vpc.public_subnets,
    vpc_id          = module.vpc.vpc_id,
  }
}
