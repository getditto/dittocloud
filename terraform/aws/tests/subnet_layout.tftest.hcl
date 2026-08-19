# The DMZ split is pure address arithmetic, so it is asserted directly against
# the allocation plan rather than against anything AWS returns.
mock_provider "aws" {
  mock_data "aws_availability_zones" {
    defaults = {
      names = [
        "us-east-1a",
        "us-east-1b",
        "us-east-1c",
      ]
    }
  }

  mock_data "aws_region" {
    defaults = {
      region = "us-east-1"
    }
  }

  mock_data "aws_partition" {
    defaults = {
      reverse_dns_prefix = "com.amazonaws"
    }
  }

  mock_data "aws_iam_policy_document" {
    defaults = {
      json          = jsonencode({ Version = "2012-10-17", Statement = [] })
      minified_json = jsonencode({ Version = "2012-10-17", Statement = [] })
    }
  }
}

run "dmz_split_matches_the_agreed_allocation" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    region                  = "us-east-1"
    vpc_name                = "valet"
    vpc_cidr                = "10.214.0.0/20"
    secondary_cidr          = "100.64.0.0/16"
    enable_database_subnets = true
    kubernetes_cluster_name = "valet-dev"
  }

  assert {
    condition = (
      local.public_subnet_cidrs["us-east-1a"] == "10.214.0.0/24" &&
      local.public_subnet_cidrs["us-east-1b"] == "10.214.1.0/24" &&
      local.public_subnet_cidrs["us-east-1c"] == "10.214.2.0/24" &&
      local.private_subnet_cidrs["us-east-1a"] == "10.214.4.0/23" &&
      local.private_subnet_cidrs["us-east-1b"] == "10.214.6.0/23" &&
      local.private_subnet_cidrs["us-east-1c"] == "10.214.8.0/23"
    )
    error_message = "The primary CIDR must carve a /24 public tier out of its first quarter and a /23 private tier immediately after it, leaving one spare block per tier."
  }

  assert {
    condition = (
      local.pod_subnet_cidrs["us-east-1a"] == "100.64.0.0/18" &&
      local.pod_subnet_cidrs["us-east-1b"] == "100.64.64.0/18" &&
      local.pod_subnet_cidrs["us-east-1c"] == "100.64.128.0/18" &&
      local.node_subnet_cidrs["us-east-1a"] == "100.64.192.0/22" &&
      local.node_subnet_cidrs["us-east-1b"] == "100.64.196.0/22" &&
      local.node_subnet_cidrs["us-east-1c"] == "100.64.200.0/22" &&
      local.database_subnet_cidrs["us-east-1a"] == "100.64.208.0/22" &&
      local.database_subnet_cidrs["us-east-1b"] == "100.64.212.0/22" &&
      local.database_subnet_cidrs["us-east-1c"] == "100.64.216.0/22" &&
      local.reserved_tier_cidr == "100.64.224.0/19"
    )
    error_message = "The secondary CIDR must carve pod /18s from its low three quarters, node and database /22s from the two /20s that follow, and hold back the closing /19."
  }

  assert {
    condition = (
      aws_subnet.pod["us-east-1a"].tags["ditto.live/subnet-tier"] == "pod" &&
      !can(aws_subnet.pod["us-east-1a"].tags["kubernetes.io/role/elb"]) &&
      !can(aws_subnet.pod["us-east-1a"].tags["kubernetes.io/role/internal-elb"]) &&
      aws_subnet.node["us-east-1b"].tags["karpenter.sh/discovery"] == "valet-dev" &&
      !can(aws_subnet.node["us-east-1b"].tags["kubernetes.io/role/elb"]) &&
      !can(aws_subnet.node["us-east-1b"].tags["kubernetes.io/role/internal-elb"]) &&
      local.database_subnet_tags["ditto.live/subnet-tier"] == "database"
    )
    error_message = "Subnet tags are the placement mechanism: only the node tier may carry karpenter.sh/discovery, and no workload tier may carry a load-balancer role tag."
  }

  assert {
    condition = (
      length(aws_route_table.workload) == 3 &&
      length(aws_route.workload_nat_gateway) == 3 &&
      aws_route.workload_nat_gateway["us-east-1a"].destination_cidr_block == "0.0.0.0/0" &&
      length(aws_route_table_association.pod) == 3 &&
      length(aws_route_table_association.node) == 3
    )
    error_message = "Each availability zone must get one workload route table carrying exactly the default route to its NAT gateway, with the pod and node subnets attached to it."
  }

  assert {
    condition = (
      length(aws_eip.nat) == 3 &&
      aws_eip.nat[0].tags["Name"] == "valet-nat-us-east-1a" &&
      length(local.nat_eip_allocation_ids) == 3
    )
    error_message = "This module must own one named NAT address per availability zone so egress IPs are stable and readable."
  }
}

run "pinned_netmasks_reproduce_the_pre_dmz_allocation" {
  command = plan

  module {
    source = "./vpc"
  }

  # An existing deployment adopts this module by pinning the netmasks it already
  # has. Any drift here renumbers live subnets, which recreates NAT gateways,
  # nodes, and load balancers with them.
  variables {
    region                 = "us-east-1"
    vpc_name               = "valet"
    vpc_cidr               = "10.214.0.0/16"
    public_subnet_netmask  = 22
    private_subnet_netmask = 18
  }

  assert {
    condition = (
      local.public_subnet_cidrs["us-east-1a"] == "10.214.0.0/22" &&
      local.public_subnet_cidrs["us-east-1b"] == "10.214.4.0/22" &&
      local.public_subnet_cidrs["us-east-1c"] == "10.214.8.0/22" &&
      local.private_subnet_cidrs["us-east-1a"] == "10.214.64.0/18" &&
      local.private_subnet_cidrs["us-east-1b"] == "10.214.128.0/18" &&
      local.private_subnet_cidrs["us-east-1c"] == "10.214.192.0/18"
    )
    error_message = "Pinning the pre-DMZ netmasks must reproduce the exact subnet CIDRs the sequential calculator produced."
  }

  assert {
    condition = (
      length(local.pod_subnet_cidrs) == 0 &&
      length(local.node_subnet_cidrs) == 0 &&
      length(aws_route_table.workload) == 0 &&
      length(aws_subnet.pod) == 0 &&
      length(aws_subnet.node) == 0
    )
    error_message = "Without a secondary CIDR there is nowhere to put workload capacity, so no workload subnet or route table may be planned."
  }
}

run "adopts_pre_allocated_nat_addresses" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    region         = "us-east-1"
    vpc_name       = "valet"
    vpc_cidr       = "10.214.0.0/20"
    secondary_cidr = "100.64.0.0/16"
    nat_gateway_eip_allocation_ids = [
      "eipalloc-0123456789abcdef0",
      "eipalloc-0123456789abcdef1",
      "eipalloc-0123456789abcdef2",
    ]
  }

  assert {
    condition = (
      length(aws_eip.nat) == 0 &&
      local.nat_eip_allocation_ids == var.nat_gateway_eip_allocation_ids
    )
    error_message = "Supplied Elastic IP allocations must be used as-is so egress addresses survive a VPC rebuild."
  }
}

run "rejects_a_secondary_cidr_outside_the_shared_pool" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    secondary_cidr = "10.128.0.0/16"
  }

  expect_failures = [var.secondary_cidr]
}

run "rejects_the_reserved_cluster_block" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    secondary_cidr = "100.66.0.0/16"
  }

  expect_failures = [var.secondary_cidr]
}

run "rejects_load_balancer_subnets_smaller_than_a_24" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    vpc_cidr              = "10.214.0.0/20"
    public_subnet_netmask = 25
  }

  expect_failures = [var.public_subnet_netmask]
}

run "rejects_a_private_tier_that_would_overlap_the_public_tier" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    vpc_cidr               = "10.214.0.0/20"
    private_subnet_netmask = 21
  }

  expect_failures = [var.private_subnet_netmask]
}

run "rejects_database_subnets_without_workload_capacity" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    vpc_cidr                = "10.214.0.0/20"
    enable_database_subnets = true
  }

  expect_failures = [var.enable_database_subnets]
}

run "rejects_an_incomplete_set_of_nat_addresses" {
  command = plan

  module {
    source = "./vpc"
  }

  variables {
    nat_gateway_eip_allocation_ids = ["eipalloc-0123456789abcdef0"]
  }

  expect_failures = [var.nat_gateway_eip_allocation_ids]
}
