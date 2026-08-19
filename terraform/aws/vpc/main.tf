data "aws_availability_zones" "available" {
  region = var.region
}

data "aws_region" "current" {
  region = var.region
}

data "aws_partition" "current" {}

locals {
  name   = var.vpc_name
  region = coalesce(var.region, data.aws_region.current.region)

  vpc_cidr = var.vpc_cidr
  azs      = slice(data.aws_availability_zones.available.names, 0, 3)

  kubernetes_cluster_name = coalesce(var.kubernetes_cluster_name, var.vpc_name)
  kubernetes_cluster_tags = var.manage_kubernetes_cluster_tag ? {
    "kubernetes.io/cluster/${local.kubernetes_cluster_name}" = "shared"
  } : {}
  nat_gateway_tags = var.nat_gateway_name != null ? { Name = var.nat_gateway_name } : {}

  // us-east-1 uses ec2.internal, all other regions use <region>.compute.internal
  dhcp_domain = local.region == "us-east-1" ? "ec2.internal" : "${local.region}.compute.internal"

  tags = merge({ "ditto.live/managed_by" = "dittocloud" }, var.tags)
}


################################################################################
# VPC Module
################################################################################

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "6.6.0"

  region = local.region

  name = local.name
  cidr = local.vpc_cidr

  azs                   = local.azs
  secondary_cidr_blocks = local.secondary_enabled ? [local.secondary_cidr] : []

  private_subnets = [for az in local.azs : local.private_subnet_cidrs[az]]
  public_subnets  = [for az in local.azs : local.public_subnet_cidrs[az]]

  private_subnet_tags = merge(
    {
      "kubernetes.io/role/internal-elb" = "1"
    },
    local.kubernetes_cluster_tags,
  )

  public_subnet_tags = merge(
    {
      "kubernetes.io/role/elb" = "1"
    },
    local.kubernetes_cluster_tags,
  )

  nat_gateway_tags = local.nat_gateway_tags

  # Database capacity lives in the secondary workload block behind its own per-AZ
  # route tables. It must not share the private DMZ tables, which is where peering
  # routes are attached.
  database_subnets                   = var.enable_database_subnets ? [for az in local.azs : local.database_subnet_cidrs[az]] : []
  database_subnet_tags               = local.database_subnet_tags
  create_database_subnet_route_table = var.enable_database_subnets
  create_database_nat_gateway_route  = var.enable_database_subnets

  create_elasticache_subnet_group = false
  create_redshift_subnet_group    = false
  create_database_subnet_group    = var.enable_database_subnets
  manage_default_network_acl      = true
  manage_default_route_table      = true
  manage_default_security_group   = true

  enable_dns_hostnames = true
  enable_dns_support   = true

  enable_nat_gateway = true
  single_nat_gateway = false

  # The NAT addresses are owned by this module (see nat.tf) so egress IPs are
  # stable and can be pre-allocated out of band.
  reuse_nat_ips       = true
  external_nat_ip_ids = local.nat_eip_allocation_ids
  external_nat_ips    = local.nat_eip_public_ips

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

  tags = local.tags
}

################################################################################
# VPC Endpoints
################################################################################

module "vpc_endpoints" {
  source  = "terraform-aws-modules/vpc/aws//modules/vpc-endpoints"
  version = "6.6.0"

  region = local.region

  vpc_id = module.vpc.vpc_id

  create_security_group      = true
  security_group_name_prefix = "${local.name}-vpc-endpoints-"
  security_group_description = "VPC endpoint security group"
  security_group_rules = {
    ingress_https = {
      description = "HTTPS from VPC"
      # Nodes and pods live in the secondary block, so it has to be admitted here
      # too or every interface endpoint becomes unreachable for them.
      cidr_blocks = local.secondary_enabled ? [module.vpc.vpc_cidr_block, local.secondary_cidr] : [module.vpc.vpc_cidr_block]
    }
  }

  endpoints = {
    s3 = {
      service_endpoint = "${data.aws_partition.current.reverse_dns_prefix}.${local.region}.s3"
      # A gateway endpoint is reached through a prefix-list route, so it takes
      # route table IDs; subnet IDs are ignored for this endpoint type.
      route_table_ids = local.s3_endpoint_route_table_ids
      tags            = { Name = "s3-vpc-endpoint" }
      service_type    = "Gateway"
    },
    ecr_api = {
      service_endpoint    = "${data.aws_partition.current.reverse_dns_prefix}.${local.region}.ecr.api"
      private_dns_enabled = true
      subnet_ids          = module.vpc.private_subnets
      policy              = data.aws_iam_policy_document.generic_endpoint_policy.json
      tags                = { Name = "ecr-api-endpoint" }
    },
    ecr_dkr = {
      service_endpoint    = "${data.aws_partition.current.reverse_dns_prefix}.${local.region}.ecr.dkr"
      private_dns_enabled = true
      subnet_ids          = module.vpc.private_subnets
      policy              = data.aws_iam_policy_document.generic_endpoint_policy.json
      tags                = { Name = "ecr-dkr-endpoint" }
    },
  }

  tags = local.tags
}

################################################################################
# Supporting Resources
################################################################################

data "aws_iam_policy_document" "generic_endpoint_policy" {
  # An endpoint policy grants nothing unless it includes an explicit Allow, so the
  # deny-if-outside-VPC guardrail below must be paired with an allow for in-VPC traffic.
  # Without this, ECR pulls (e.g. ecr:GetAuthorizationToken) from inside the VPC are
  # implicitly denied, which blocks EKS nodes from pulling images and joining.
  statement {
    sid       = "AllowAllInVpc"
    effect    = "Allow"
    actions   = ["*"]
    resources = ["*"]

    principals {
      type        = "*"
      identifiers = ["*"]
    }
  }

  statement {
    sid       = "DenyOutsideVpc"
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

locals {
  # Everything that reaches S3 over the gateway endpoint rather than through NAT.
  # database_route_table_ids falls back to the private tables when no dedicated
  # database tables exist, hence the distinct().
  s3_endpoint_route_table_ids = distinct(concat(
    module.vpc.private_route_table_ids,
    module.vpc.database_route_table_ids,
    [for az in local.azs : aws_route_table.workload[az].id if local.secondary_enabled],
  ))
}

output "vpc" {
  value = {
    private_subnets  = module.vpc.private_subnets
    public_subnets   = module.vpc.public_subnets
    database_subnets = module.vpc.database_subnets
    # compact() keeps these list(string) whether or not a secondary CIDR exists,
    # so a caller can collect them across VPCs without a type mismatch.
    pod_subnets    = compact([for az in local.azs : aws_subnet.pod[az].id if local.secondary_enabled])
    node_subnets   = compact([for az in local.azs : aws_subnet.node[az].id if local.secondary_enabled])
    vpc_id         = module.vpc.vpc_id
    secondary_cidr = var.secondary_cidr
  }
}

output "vpc_id" {
  value = module.vpc.vpc_id
}

# One ENIConfig per availability zone points VPC CNI custom networking at that
# zone's pod subnet. Without AWS_VPC_K8S_CNI_CUSTOM_NETWORK_CFG=true, an
# ENIConfig per AZ, and ENI_CONFIG_LABEL_DEF=topology.kubernetes.io/zone on the
# cluster, VPC CNI places pods in the node subnet and this tier goes unused.
output "pod_subnets_by_az" {
  description = "Pod subnet for each availability zone, for generating one VPC CNI ENIConfig per zone."
  value = [
    for az in local.azs : {
      availability_zone = az
      subnet_id         = aws_subnet.pod[az].id
    } if local.secondary_enabled
  ]
}

output "node_subnets_by_az" {
  description = "Node subnet for each availability zone."
  value = [
    for az in local.azs : {
      availability_zone = az
      subnet_id         = aws_subnet.node[az].id
    } if local.secondary_enabled
  ]
}

output "nat_eip_allocation_ids" {
  description = "Elastic IP allocation IDs backing the NAT gateways, in availability zone order."
  value       = local.nat_eip_allocation_ids
}

output "nat_public_ips" {
  description = "Stable public egress addresses of the NAT gateways, in availability zone order."
  value       = local.nat_eip_public_ips
}

output "subnets" {
  description = "Allocated subnet CIDR blocks by tier and availability zone, plus the reserved block held back inside the secondary CIDR."
  value = {
    public   = local.public_subnet_cidrs
    private  = local.private_subnet_cidrs
    pod      = local.pod_subnet_cidrs
    node     = local.node_subnet_cidrs
    database = local.database_subnet_cidrs
    reserved = local.secondary_enabled ? local.reserved_tier_cidr : null
  }
}

output "valet_web_config" {
  value = {
    id = local.region
    vpc = [{
      id             = module.vpc.vpc_id
      privateSubnets = module.vpc.private_subnets
      publicSubnets  = module.vpc.public_subnets
    }]
  }
}
