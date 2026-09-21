################################################################################
# Workload Subnets (secondary CIDR)
################################################################################
#
# Pod and node subnets are created here rather than through the upstream VPC
# module so that their route tables, tags, and lifecycle stay explicit. The
# secondary CIDR association itself is owned by the upstream module through
# secondary_cidr_blocks, so every resource below waits on that module.
#
# Routing policy: these subnets reach the internet through their availability
# zone's NAT gateway and reach the rest of the VPC over the local route. They
# deliberately carry no peering routes and no transit gateway attachment —
# secondary addresses never leave the VPC. Cross-VPC access is published through
# an internal load balancer in the private DMZ instead. A workload that initiates
# a connection to a peered VPC from a secondary address will fail on the return
# path even when the far side has a load balancer, because the far VPC has no
# route back; that case needs a terminating proxy in the DMZ and is unsupported.
#
locals {
  pod_subnet_tags = merge(
    { "ditto.live/subnet-tier" = "pod" },
    local.kubernetes_cluster_tags,
  )

  # Defaults to the VPC ID, so the tag is always present and always derivable.
  # Karpenter selects node subnets by this tag, and the selector on the cluster
  # side reads the ditto.live/vpc_id annotation that valet-cluster-operator already
  # publishes on every cluster secret. Both ends therefore agree on a fact neither
  # has to be told, with no metadata to set and no naming convention to remember.
  #
  # The VPC ID is also the right shape for what the tag means: node subnets belong
  # to a VPC, and several clusters can share one, so keying on a cluster name would
  # be wrong wherever more than one cluster lives here.
  karpenter_discovery_value = coalesce(
    var.karpenter_discovery_tag_value,
    var.kubernetes_cluster_name,
    module.vpc.vpc_id,
  )
  karpenter_discovery_tags = {
    "karpenter.sh/discovery" = local.karpenter_discovery_value
  }

  node_subnet_tags = merge(
    { "ditto.live/subnet-tier" = "node" },
    local.karpenter_discovery_tags,
    local.kubernetes_cluster_tags,
  )

  database_subnet_tags = merge(
    { "ditto.live/subnet-tier" = "database" },
    local.kubernetes_cluster_tags,
  )
}

resource "aws_subnet" "pod" {
  for_each = local.pod_subnet_cidrs
  region   = local.region

  vpc_id            = module.vpc.vpc_id
  availability_zone = each.key
  cidr_block        = each.value

  tags = merge(
    { Name = "${local.name}-pod-${each.key}" },
    local.tags,
    local.pod_subnet_tags,
  )

  depends_on = [module.vpc]
}

resource "aws_subnet" "node" {
  for_each = local.node_subnet_cidrs
  region   = local.region

  vpc_id            = module.vpc.vpc_id
  availability_zone = each.key
  cidr_block        = each.value

  tags = merge(
    { Name = "${local.name}-node-${each.key}" },
    local.tags,
    local.node_subnet_tags,
  )

  depends_on = [module.vpc]
}

################################################################################
# Workload Route Tables
################################################################################

resource "aws_route_table" "workload" {
  for_each = local.secondary_enabled ? toset(local.azs) : toset([])
  region   = local.region

  vpc_id = module.vpc.vpc_id

  tags = merge(
    { Name = "${local.name}-workload-${each.key}" },
    local.tags,
  )
}

resource "aws_route" "workload_nat_gateway" {
  for_each = aws_route_table.workload
  region   = local.region

  route_table_id         = each.value.id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = element(module.vpc.natgw_ids, local.az_indexes[each.key])

  timeouts {
    create = "5m"
  }
}

resource "aws_route_table_association" "pod" {
  for_each = aws_subnet.pod
  region   = local.region

  subnet_id      = each.value.id
  route_table_id = aws_route_table.workload[each.key].id
}

resource "aws_route_table_association" "node" {
  for_each = aws_subnet.node
  region   = local.region

  subnet_id      = each.value.id
  route_table_id = aws_route_table.workload[each.key].id
}
