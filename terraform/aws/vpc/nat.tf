################################################################################
# NAT Gateway Addresses
################################################################################
#
# The NAT Elastic IPs are owned here instead of by the upstream VPC module so the
# egress addresses are stable, individually named, and readable as an output
# before anything downstream needs them. An operator who needs addresses that
# survive a VPC rebuild allocates them out of band and passes them in through
# nat_gateway_eip_allocation_ids.
#
# The moved blocks adopt the addresses the upstream module created previously, so
# an existing deployment keeps its current egress IPs.
#
locals {
  external_nat_eips = length(var.nat_gateway_eip_allocation_ids) > 0

  nat_eip_allocation_ids = local.external_nat_eips ? var.nat_gateway_eip_allocation_ids : aws_eip.nat[*].id
  nat_eip_public_ips     = local.external_nat_eips ? data.aws_eip.nat[*].public_ip : aws_eip.nat[*].public_ip
}

resource "aws_eip" "nat" {
  count  = local.external_nat_eips ? 0 : length(local.azs)
  region = local.region

  domain = "vpc"

  tags = merge(
    { Name = "${local.name}-nat-${local.azs[count.index]}" },
    local.tags,
  )
}

data "aws_eip" "nat" {
  count  = local.external_nat_eips ? length(var.nat_gateway_eip_allocation_ids) : 0
  region = local.region

  id = var.nat_gateway_eip_allocation_ids[count.index]
}

moved {
  from = module.vpc.aws_eip.nat[0]
  to   = aws_eip.nat[0]
}

moved {
  from = module.vpc.aws_eip.nat[1]
  to   = aws_eip.nat[1]
}

moved {
  from = module.vpc.aws_eip.nat[2]
  to   = aws_eip.nat[2]
}

# Private NAT gateways translate workload addresses to the primary VPC CIDR
# before traffic crosses a peering connection. They need no Elastic IPs.
# Destination routes through these gateways and the peering are managed by the
# caller; existing internet egress continues to use the public NAT gateways.
resource "aws_nat_gateway" "private" {
  for_each = var.enable_private_nat_gateways ? toset(local.azs) : toset([])
  region   = local.region

  connectivity_type = "private"
  subnet_id         = module.vpc.private_subnets[local.az_indexes[each.key]]

  tags = merge(
    local.tags,
    { Name = "${local.name}-private-nat-${each.key}" },
  )
}
