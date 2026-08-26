####################################################################################################
# Shared Variables
####################################################################################################
variable "region" {
  description = "The AWS region to deploy resources in. Can be overridden by the embedded CLI."
  type        = string
  default     = null
}

variable "profile" {
  description = "The AWS profile to use for authentication. Can be overridden by the embedded CLI."
  type        = string
  default     = null
}

variable "deployment_scopes" {
  description = "Complete desired AWS deployment scope map. An empty map preserves legacy single-scope behavior; a non-empty map enables scope mode and requires exactly one default scope."
  type = map(object({
    default                  = optional(bool, false)
    cluster_name             = optional(string)
    cluster_type             = optional(string, "kubeadm")
    region                   = string
    scope_tag_policy_version = optional(number, 0)
    vpc = object({
      mode                           = string
      name                           = optional(string)
      cidr                           = optional(string)
      secondary_cidr                 = optional(string)
      public_subnet_netmask          = optional(number, 24)
      private_subnet_netmask         = optional(number, 23)
      id                             = optional(string)
      nat_gateway_name               = optional(string)
      nat_gateway_eip_allocation_ids = optional(list(string), [])
    })
  }))
  default = {}

  validation {
    condition = length(var.deployment_scopes) == 0 || length([
      for scope in values(var.deployment_scopes) : scope if scope.default
    ]) == 1
    error_message = "A non-empty deployment_scopes map must contain exactly one scope with default = true."
  }

  validation {
    condition = alltrue([
      for scope_ref in keys(var.deployment_scopes) :
      length(scope_ref) == 30 &&
      can(regex("^dsc-[0-7][0-9a-hjkmnp-tv-z]{25}$", scope_ref))
    ])
    error_message = "Each deployment scope reference must be exactly 30 characters in generated dsc-<lowercase-crockford-ulid> form."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      contains(["kubeadm", "eks"], scope.cluster_type) &&
      can(regex("^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$", scope.region)) &&
      try(
        scope.cluster_name == null || (
          length(scope.cluster_name) <= 63 &&
          can(regex("^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$", scope.cluster_name))
        ),
        true,
      )
    ])
    error_message = "Each deployment scope must use cluster_type kubeadm or eks, a valid AWS region, and an optional lowercase DNS-label cluster_name with at most 63 characters."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) : contains([0, 1], scope.scope_tag_policy_version)
    ])
    error_message = "Each deployment scope scope_tag_policy_version must be 0 or 1."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) : scope.scope_tag_policy_version == 0 || scope.cluster_name != null
    ])
    error_message = "Each deployment scope with scope_tag_policy_version 1 must identify one exact cluster_name."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) : contains(["dittocloud", "capi", "existing"], scope.vpc.mode)
    ])
    error_message = "Each deployment scope VPC mode must be dittocloud, capi, or existing."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      scope.vpc.mode == "dittocloud" ? (
        try(trimspace(scope.vpc.name) != "", false) &&
        try(can(cidrhost(scope.vpc.cidr, 0)) && !strcontains(scope.vpc.cidr, ":"), false) &&
        scope.vpc.id == null
        ) : scope.vpc.mode == "capi" ? (
        scope.vpc.name == null &&
        scope.vpc.cidr == null &&
        (scope.vpc.id == null ? true : can(regex("^vpc-(?:[0-9a-f]{8}|[0-9a-f]{17})$", scope.vpc.id)))
        ) : scope.vpc.mode == "existing" ? (
        scope.vpc.name == null &&
        scope.vpc.cidr == null &&
        (scope.vpc.id == null ? false : can(regex("^vpc-(?:[0-9a-f]{8}|[0-9a-f]{17})$", scope.vpc.id)))
      ) : true
    ])
    error_message = "Dittocloud VPC scopes require name and IPv4 cidr; CAPI scopes permit only an optional VPC id; existing scopes require a valid VPC id."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      scope.vpc.nat_gateway_name == null || (
        scope.vpc.mode == "dittocloud" &&
        length(scope.vpc.nat_gateway_name) > 0 &&
        length(scope.vpc.nat_gateway_name) <= 256 &&
        trimspace(scope.vpc.nat_gateway_name) == scope.vpc.nat_gateway_name
      )
    ])
    error_message = "nat_gateway_name can only be set for a Dittocloud-managed VPC and must contain 1 to 256 non-whitespace-bounded characters."
  }

  # A scope is one VPC, so each scope owns its own workload block and its own
  # subnet sizing. Two scopes sharing a secondary CIDR could never be peered.
  #
  # Variable validation cannot reference a local, so the reserved list is spelled
  # out here, in vpc/variables.tf, and in awsReservedClusterSecondaryCIDRs
  # (cmd/internal/bootstrap/aws_scopes.go). Reserving a fourth block means
  # editing all three; nothing fails if one is missed.
  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      scope.vpc.secondary_cidr == null ? true : (
        scope.vpc.mode == "dittocloud" &&
        contains([for index in range(64) : cidrsubnet("100.64.0.0/10", 6, index)], scope.vpc.secondary_cidr) &&
        !contains(["100.66.0.0/16", "100.80.0.0/16", "100.81.0.0/16"], scope.vpc.secondary_cidr)
      )
    ])
    error_message = "A scope secondary_cidr can only be set for a Dittocloud-managed VPC and must be one of the 61 allocatable /16 blocks inside 100.64.0.0/10. Of the 64 blocks in that range, 100.66.0.0/16, 100.80.0.0/16 and 100.81.0.0/16 are reserved because Valet clusters use them for in-cluster pod and Service addressing."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      scope.vpc.mode == "dittocloud" || (
        scope.vpc.public_subnet_netmask == 24 &&
        scope.vpc.private_subnet_netmask == 23 &&
        length(scope.vpc.nat_gateway_eip_allocation_ids) == 0
      )
    ])
    error_message = "Only a Dittocloud-managed VPC has subnet sizing or NAT Elastic IP allocations. Leave public_subnet_netmask and private_subnet_netmask at their defaults of 24 and 23, and nat_gateway_eip_allocation_ids empty, for capi and existing scopes."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      scope.vpc.mode != "dittocloud" || scope.vpc.cidr == null || (
        scope.vpc.public_subnet_netmask <= 24 &&
        scope.vpc.private_subnet_netmask <= 24 &&
        scope.vpc.public_subnet_netmask >= tonumber(split("/", scope.vpc.cidr)[1]) + 4 &&
        scope.vpc.private_subnet_netmask >= tonumber(split("/", scope.vpc.cidr)[1]) + 2
      )
    ])
    error_message = "A Dittocloud-managed VPC needs load-balancer subnets of at least a /24 that fit inside its primary CIDR: public_subnet_netmask at least 4 bits and private_subnet_netmask at least 2 bits longer than the VPC prefix."
  }

  validation {
    condition = alltrue([
      for scope in values(var.deployment_scopes) :
      length(scope.vpc.nat_gateway_eip_allocation_ids) == 0 || (
        length(scope.vpc.nat_gateway_eip_allocation_ids) == 3 &&
        alltrue([
          for allocation_id in scope.vpc.nat_gateway_eip_allocation_ids :
          can(regex("^eipalloc-(?:[0-9a-f]{8}|[0-9a-f]{17})$", allocation_id))
        ])
      )
    ])
    error_message = "nat_gateway_eip_allocation_ids must be empty or contain one eipalloc- identifier for each of the three availability zones."
  }
}

variable "scope_tag_policy_cli_authorized_refs" {
  description = "Internal Dittocloud CLI authorization for scopes whose verified policy version is 1. Direct Terraform callers must leave this empty."
  type        = set(string)
  default     = []

  validation {
    condition = alltrue([
      for scope_ref in var.scope_tag_policy_cli_authorized_refs :
      try(var.deployment_scopes[scope_ref].scope_tag_policy_version == 1, false)
    ])
    error_message = "scope_tag_policy_cli_authorized_refs may contain only configured version-1 scopes verified by the Dittocloud CLI."
  }
}

variable "scope_tag_policy_v0_legacy_cluster_refs" {
  description = "Internal migration bridge that preserves an already-applied legacy phase-two cluster policy while its default scope remains at version 0."
  type        = set(string)
  default     = []

  validation {
    condition = alltrue([
      for scope_ref in var.scope_tag_policy_v0_legacy_cluster_refs :
      try(
        var.deployment_scopes[scope_ref].default &&
        var.deployment_scopes[scope_ref].scope_tag_policy_version == 0 &&
        var.deployment_scopes[scope_ref].cluster_name != null,
        false,
      )
    ])
    error_message = "scope_tag_policy_v0_legacy_cluster_refs may contain only the named default version-0 scope detected from legacy phase-two IAM state."
  }
}

####################################################################################################
# IAM Policies
####################################################################################################
variable "create_iam" {
  type        = bool
  description = "Whether to create cross-account IAM roles and policies. IAM is a global service — set to false for additional region deployments to avoid duplicate resource conflicts."
  default     = true
}

variable "create_vpc" {
  type        = bool
  description = "Whether Terraform creates the Ditto VPC. When false without customer_managed_vpc, Terraform skips VPC creation but retains VPC lifecycle permissions so Cluster API can create one."
  default     = true
}

variable "enable_eks" {
  type        = bool
  description = "Whether to provision EKS IAM: the CAPA controller EKS policy (managed control plane, node groups, addons, access entries, OIDC provider) and the Karpenter controller permissions on the cluster-resources boundary. Opt-in per account."
  default     = false
}

variable "controller_trusted_role_arns" {
  type = list(string)

  validation {
    condition = alltrue([
      for value in var.controller_trusted_role_arns : can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", value))
    ])
    # condition     = can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", var.server_role_arn))
    error_message = "Must be a valid AWS IAM role ARN."
  }

  default = [
    # Central Operations
    "arn:aws:iam::851725645787:role/controllers.cluster-api-provider-aws.sigs.k8s.io",
    # Production Operations Valet Control
    "arn:aws:iam::851725645787:role/valet-controllers.cluster-api-provider-aws.sigs.k8s.io",
  ]
}

variable "iam_trusted_role_arns" {
  type = list(string)

  validation {
    condition = alltrue([
      for value in var.iam_trusted_role_arns : can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", value))
    ])
    # condition     = can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", var.server_role_arn))
    error_message = "Must be a valid AWS IAM role ARN."
  }

  default = [
    # Central Operations
    "arn:aws:iam::851725645787:role/trust-editor.ditto.live",
    # Production Operations Valet Control
    "arn:aws:iam::851725645787:role/valet-trust-editor.ditto.live",
  ]
}

variable "iam_trusted_operations_principal_arns" {
  type    = string
  default = "arn:aws:iam::851725645787:root"
}

variable "iam_trusted_operations_condition_arns" {
  type = list(string)

  validation {
    condition = alltrue([
      for value in var.iam_trusted_operations_condition_arns : can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", value))
    ])
    # condition     = can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", var.server_role_arn))
    error_message = "Must be a valid AWS IAM role ARN."
  }

  default = [
    # Allow Ditto SRE UI View Only access
    "arn:aws:iam::851725645787:role/aws-reserved/sso.amazonaws.com/*"
  ]
}


####################################################################################################
# VPC
####################################################################################################

variable "vpc_name" {
  description = "The name of the VPC."
  default     = "ditto"
}

variable "vpc_cidr" {
  description = "The primary IPv4 CIDR block for the VPC. This block is a DMZ carrying load balancers, NAT gateways, and explicitly placed EC2 only."
  default     = "10.210.0.0/16"
}

variable "vpc_secondary_cidr" {
  description = "Optional secondary IPv4 CIDR block carrying every workload tier (pod, node, database). Must be one of the allocatable /16 blocks inside 100.64.0.0/10. The same block is used on every VPC, because it is never routed or advertised outside its own VPC. Two VPCs sharing it cannot be peered, since AWS rejects a peering connection on any overlapping CIDR; cross-VPC connectivity is PrivateLink or VPC Lattice instead."
  type        = string
  default     = null
  nullable    = true
}

variable "public_subnet_netmask" {
  description = "Netmask for each per-AZ public DMZ subnet. Pin this to the value an existing deployment already uses; changing it renumbers live subnets."
  type        = number
  default     = 24
}

variable "private_subnet_netmask" {
  description = "Netmask for each per-AZ private DMZ subnet. Pin this to the value an existing deployment already uses; changing it renumbers live subnets."
  type        = number
  default     = 23
}

variable "karpenter_discovery_tag_value" {
  description = "Value for the karpenter.sh/discovery tag on the node subnets, which is how Karpenter finds where to launch nodes. Defaults to cluster_name when set; the tag is omitted when neither is set. Terraform has to own this tag because the CAPA controller boundary does not permit the karpenter.sh namespace."
  type        = string
  default     = null
  nullable    = true
}

variable "nat_gateway_eip_allocation_ids" {
  description = "Optional pre-allocated Elastic IP allocation IDs for the NAT gateways, one per availability zone in order. When empty the module allocates and owns the addresses itself."
  type        = list(string)
  default     = []
}

variable "tags" {
  type    = map(string)
  default = {}
}

variable "customer_managed_vpc" {
  type        = bool
  description = "Whether the customer provides an existing VPC. When true, vpc_id is required and VPC lifecycle permissions (create/delete VPC, subnets, IGW, NAT gateways, etc.) are not granted to the CAPA controller."
  default     = false
}

variable "cluster_name" {
  type        = string
  description = "When set, tightens CAPA controller IAM conditions to this specific cluster name. Requires an existing state file — use only on re-runs after the initial deployment."
  default     = null
}

variable "vpc_id" {
  type        = string
  description = "ID of an existing customer-managed VPC. Required when customer_managed_vpc is true. Terraform automatically uses the created VPC ID when create_vpc is true."
  default     = null
}
