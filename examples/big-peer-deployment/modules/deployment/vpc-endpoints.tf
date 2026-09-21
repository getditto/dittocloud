# VPC endpoints so private subnets can reach AWS services without an internet path.
# Only created for managed VPCs; existing VPCs are expected to provide their own.
module "vpc_endpoints" {
  source  = "terraform-aws-modules/vpc/aws//modules/vpc-endpoints"
  version = "6.6.0"
  count   = var.deployment.vpc.create ? 1 : 0

  vpc_id = local.vpc_id

  create_security_group      = true
  security_group_name_prefix = "${var.deployment.cluster.name}-vpc-endpoints-"
  security_group_description = "VPC endpoint security group"
  security_group_rules = {
    ingress_https = {
      description = "HTTPS from VPC"
      cidr_blocks = [module.vpc[0].vpc_cidr_block]
    }
  }

  endpoints = {
    s3 = {
      service             = "s3"
      service_type        = "Gateway"
      route_table_ids     = module.vpc[0].private_route_table_ids
      private_dns_enabled = false
      tags                = { Name = "s3-vpc-endpoint" }
    }
    ecr_api = {
      service             = "ecr.api"
      private_dns_enabled = true
      subnet_ids          = local.private_subnet_ids
      tags                = { Name = "ecr-api-endpoint" }
    }
    ecr_dkr = {
      service             = "ecr.dkr"
      private_dns_enabled = true
      subnet_ids          = local.private_subnet_ids
      tags                = { Name = "ecr-dkr-endpoint" }
    }
    # SSM endpoints required for AL2023 node bootstrap and EKS node registration.
    # Without these, nodes cannot register with the control plane (NodeCreationFailure).
    ssm = {
      service             = "ssm"
      private_dns_enabled = true
      subnet_ids          = local.private_subnet_ids
      tags                = { Name = "ssm-endpoint" }
    }
    ssmmessages = {
      service             = "ssmmessages"
      private_dns_enabled = true
      subnet_ids          = local.private_subnet_ids
      tags                = { Name = "ssmmessages-endpoint" }
    }
    # STS endpoint required for AL2023 node bootstrap. Nodes use sts:GetCallerIdentity
    # during kubelet startup to authenticate with EKS. Without this, nodes in private
    # subnets cannot complete bootstrap (NodeCreationFailure).
    sts = {
      service             = "sts"
      private_dns_enabled = true
      subnet_ids          = local.private_subnet_ids
      tags                = { Name = "sts-endpoint" }
    }
  }

  tags = var.tags
}
