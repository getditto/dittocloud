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
      mode             = string
      name             = optional(string)
      cidr             = optional(string)
      id               = optional(string)
      nat_gateway_name = optional(string)
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
  description = "The IPv4 CIDR block for the VPC."
  default     = "10.210.0.0/16"
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

variable "additional_authorized_vpc_arns" {
  type        = list(string)
  description = "Extra VPC ARNs also authorized on the default scope's controller role. Ignored for non-default scopes."
  default     = []
}
