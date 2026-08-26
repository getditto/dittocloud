variable "vpc_name" {
  description = "The name of the VPC."
  default     = "ditto"
}

variable "vpc_cidr" {
  description = "The primary IPv4 CIDR block for the VPC. This block is a DMZ: it carries load balancers, NAT gateways, and explicitly placed EC2 only, and it is the only surface a peered VPC ever sees."
  default     = "10.210.0.0/16"
}

variable "region" {
  description = "The AWS region to deploy resources in. Overrides the provider region when set."
  type        = string
  default     = null
}

####################################################################################################
# Primary CIDR (DMZ) subnet sizing
####################################################################################################

variable "public_subnet_netmask" {
  description = "Netmask for each per-AZ public DMZ subnet, which carries internet-facing load balancers and NAT gateways."
  type        = number
  default     = 24

  validation {
    condition     = var.public_subnet_netmask <= 24
    error_message = "public_subnet_netmask must be 24 or lower (a /24 or larger block). A load-balancer subnet needs at least 8 free addresses per AZ, an ALB scales its ENI count under load, and AWS fails silently once a subnet is exhausted."
  }

  validation {
    condition     = var.public_subnet_netmask >= tonumber(split("/", var.vpc_cidr)[1]) + 4
    error_message = "public_subnet_netmask must be at least 4 bits longer than the vpc_cidr prefix so three subnets fit inside the first quarter of the primary block."
  }
}

variable "private_subnet_netmask" {
  description = "Netmask for each per-AZ private DMZ subnet, which carries internal (peer-reachable) load balancers and explicitly placed EC2. Nodes and pods live in secondary_cidr, not here."
  type        = number
  default     = 23

  validation {
    condition     = var.private_subnet_netmask <= 24
    error_message = "private_subnet_netmask must be 24 or lower (a /24 or larger block). Internal load balancers scale their ENI count under load, several ingresses are Classic ELBs, and the private DMZ also absorbs ad-hoc EC2."
  }

  validation {
    condition = (
      var.private_subnet_netmask >= tonumber(split("/", var.vpc_cidr)[1]) + 2 &&
      pow(2, var.private_subnet_netmask - tonumber(split("/", var.vpc_cidr)[1]) - 2) + 3 <=
      pow(2, var.private_subnet_netmask - tonumber(split("/", var.vpc_cidr)[1]))
    )
    error_message = "private_subnet_netmask must be at least 2 bits longer than the vpc_cidr prefix and leave room for three subnets after the public tier."
  }
}

####################################################################################################
# Secondary CIDR (workload capacity)
####################################################################################################

variable "secondary_cidr" {
  description = "Optional secondary IPv4 CIDR block carrying every workload tier (pod, node, database). Must be unique per VPC: AWS rejects a peering connection when any associated CIDR overlaps, secondary blocks included, regardless of routing intent."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition = var.secondary_cidr == null ? true : contains(
      [for index in range(64) : cidrsubnet("100.64.0.0/10", 6, index)],
      var.secondary_cidr,
    )
    error_message = "secondary_cidr must be one of the /16 blocks inside 100.64.0.0/10, for example 100.64.0.0/16."
  }

  # Keep this list in sync with the scope validation in ../variables.tf and with
  # awsReservedClusterSecondaryCIDRs in cmd/internal/bootstrap/aws_scopes.go.
  # Variable validation cannot reference a local, so all three spell it out.
  #
  # 100.66.0.0/16 is the kubeadm cluster pod and Service range. 100.80.0.0/16 and
  # 100.81.0.0/16 are the pod and Service CIDRs every self-managed AWS cluster is
  # built with, in cloud-infra-apps apps/valet-cluster-k8s-aws. A VPC secondary on
  # any of them would overlap the in-cluster addressing of its own clusters.
  validation {
    condition = var.secondary_cidr == null ? true : !contains(
      ["100.66.0.0/16", "100.80.0.0/16", "100.81.0.0/16"],
      var.secondary_cidr,
    )
    error_message = "100.66.0.0/16, 100.80.0.0/16 and 100.81.0.0/16 are not allocatable: Valet clusters already use them for in-cluster pod and Service addressing, so a VPC secondary there collides with cluster-internal traffic."
  }
}

variable "pod_subnet_netmask" {
  description = "Netmask for each per-AZ pod subnet inside secondary_cidr. VPC CNI pre-allocates a full ENI worth of addresses per node, so this tier — not the node tier — sets the real node ceiling."
  type        = number
  default     = 18

  validation {
    condition     = var.pod_subnet_netmask >= 18 && var.pod_subnet_netmask <= 24
    error_message = "pod_subnet_netmask must be between 18 and 24. Three subnets larger than a /18 do not fit the pod region of the secondary block."
  }
}

variable "node_subnet_netmask" {
  description = "Netmask for each per-AZ node subnet inside secondary_cidr."
  type        = number
  default     = 22

  validation {
    condition     = var.node_subnet_netmask >= 22 && var.node_subnet_netmask <= 26
    error_message = "node_subnet_netmask must be between 22 and 26 so three subnets plus one spare fit the node tier's /20."
  }
}

variable "database_subnet_netmask" {
  description = "Netmask for each per-AZ database subnet inside secondary_cidr."
  type        = number
  default     = 22

  validation {
    condition     = var.database_subnet_netmask >= 22 && var.database_subnet_netmask <= 26
    error_message = "database_subnet_netmask must be between 22 and 26 so three subnets plus one spare fit the database tier's /20."
  }
}

variable "enable_database_subnets" {
  description = "Whether to create database subnets in the secondary workload block, with their own per-AZ route tables and a database subnet group."
  type        = bool
  default     = false

  validation {
    condition     = !var.enable_database_subnets || var.secondary_cidr != null
    error_message = "enable_database_subnets requires secondary_cidr: database capacity is carved from the secondary workload block, never from the DMZ."
  }
}

####################################################################################################
# Tagging
####################################################################################################

variable "kubernetes_cluster_name" {
  description = "Name used for kubernetes.io/cluster/* subnet tags. Defaults to vpc_name when not set."
  type        = string
  default     = null
}

variable "manage_kubernetes_cluster_tag" {
  description = "Whether Terraform manages the kubernetes.io/cluster/* subnet tag. Scope mode leaves this namespace to Cluster API."
  type        = bool
  default     = true
}

variable "karpenter_discovery_tag_value" {
  description = "Value for the karpenter.sh/discovery tag on the node subnets, which is how Karpenter finds where to launch nodes. Defaults to kubernetes_cluster_name; the tag is omitted when neither is set."
  type        = string
  default     = null
  nullable    = true
}

####################################################################################################
# NAT
####################################################################################################

variable "nat_gateway_name" {
  description = "Optional stable Name tag for every NAT gateway in this VPC, independent of Cluster API membership tags."
  type        = string
  default     = null
  nullable    = true

  validation {
    condition = (
      var.nat_gateway_name == null || (
        length(var.nat_gateway_name) > 0 &&
        length(var.nat_gateway_name) <= 256 &&
        trimspace(var.nat_gateway_name) == var.nat_gateway_name
      )
    )
    error_message = "nat_gateway_name must be null or contain 1 to 256 non-whitespace-bounded characters."
  }
}

variable "nat_gateway_eip_allocation_ids" {
  description = "Optional pre-allocated Elastic IP allocation IDs for the NAT gateways, one per availability zone in order. When empty this module allocates and owns the addresses; supply them to keep egress addresses stable across a VPC rebuild."
  type        = list(string)
  default     = []

  validation {
    condition = length(var.nat_gateway_eip_allocation_ids) == 0 || (
      length(var.nat_gateway_eip_allocation_ids) == 3 &&
      alltrue([
        for allocation_id in var.nat_gateway_eip_allocation_ids :
        can(regex("^eipalloc-(?:[0-9a-f]{8}|[0-9a-f]{17})$", allocation_id))
      ])
    )
    error_message = "nat_gateway_eip_allocation_ids must be empty or contain one eipalloc- identifier for each of the three availability zones."
  }
}

variable "tags" {
  type = map(string)
  default = {
    GithubRepo = "terraform-modules"
    GithubOrg  = "getditto"
  }
}
