################################################################################
# Subnet Layout
################################################################################
#
# The primary CIDR is a DMZ. It carries internet-facing load balancers and NAT
# gateways in the public tier, and internal (peer-reachable) load balancers plus
# explicitly placed EC2 in the private tier. It is the only surface a peered VPC
# ever sees.
#
# All workload capacity — pods, nodes, databases — is carved from the optional
# secondary CIDR. Every tier is allocated from its own aligned block, so resizing
# one tier never renumbers another, and each tier leaves a spare per-AZ block for
# a fourth availability zone or in-place growth.
#
# Primary /20 example (10.214.0.0/20, public /24, private /23):
#
#   Public   10.214.0.0/24  10.214.1.0/24  10.214.2.0/24  spare 10.214.3.0/24
#   Private  10.214.4.0/23  10.214.6.0/23  10.214.8.0/23  spare 10.214.10.0/23
#   Free     10.214.12.0/22
#
# Secondary /16 example (100.64.0.0/16):
#
#   Pod       100.64.0.0/18    100.64.64.0/18   100.64.128.0/18
#   Node      100.64.192.0/22  100.64.196.0/22  100.64.200.0/22  spare .204.0/22
#   Database  100.64.208.0/22  100.64.212.0/22  100.64.216.0/22  spare .220.0/22
#   Reserved  100.64.224.0/19
#
# Pinning public_subnet_netmask = 22 and private_subnet_netmask = 18 against a /16
# primary reproduces the pre-DMZ allocation exactly, which is how an existing
# deployment adopts this module without renumbering its subnets.
#
locals {
  primary_prefix = tonumber(split("/", local.vpc_cidr)[1])

  # The public tier owns the first quarter of the primary block. The private tier
  # is indexed against the whole primary block instead, starting immediately after
  # that quarter, so its addresses do not move when the public netmask changes.
  public_tier_cidr   = cidrsubnet(local.vpc_cidr, 2, 0)
  public_tier_prefix = local.primary_prefix + 2
  private_tier_start = pow(2, var.private_subnet_netmask - local.public_tier_prefix)

  public_subnet_cidrs = {
    for index, az in local.azs : az => cidrsubnet(
      local.public_tier_cidr,
      var.public_subnet_netmask - local.public_tier_prefix,
      index,
    )
  }

  private_subnet_cidrs = {
    for index, az in local.azs : az => cidrsubnet(
      local.vpc_cidr,
      var.private_subnet_netmask - local.primary_prefix,
      local.private_tier_start + index,
    )
  }

  ##############################################################################
  # Secondary CIDR
  ##############################################################################

  secondary_enabled = var.secondary_cidr != null
  secondary_cidr    = coalesce(var.secondary_cidr, "100.64.0.0/16")
  secondary_prefix  = tonumber(split("/", local.secondary_cidr)[1])

  # Pods take the low three quarters of the secondary block: at the default /18
  # they are the binding constraint on cluster size, so they get the room. The
  # node and database tiers own the two /20s that follow, and the closing /19 is
  # held back for a fourth AZ or a future tier.
  node_tier_cidr     = cidrsubnet(local.secondary_cidr, 4, 12)
  database_tier_cidr = cidrsubnet(local.secondary_cidr, 4, 13)
  reserved_tier_cidr = cidrsubnet(local.secondary_cidr, 3, 7)

  pod_subnet_cidrs = local.secondary_enabled ? {
    for index, az in local.azs : az => cidrsubnet(
      local.secondary_cidr,
      var.pod_subnet_netmask - local.secondary_prefix,
      index,
    )
  } : {}

  node_subnet_cidrs = local.secondary_enabled ? {
    for index, az in local.azs : az => cidrsubnet(
      local.node_tier_cidr,
      var.node_subnet_netmask - (local.secondary_prefix + 4),
      index,
    )
  } : {}

  database_subnet_cidrs = local.secondary_enabled && var.enable_database_subnets ? {
    for index, az in local.azs : az => cidrsubnet(
      local.database_tier_cidr,
      var.database_subnet_netmask - (local.secondary_prefix + 4),
      index,
    )
  } : {}

  az_indexes = { for index, az in local.azs : az => index }
}
