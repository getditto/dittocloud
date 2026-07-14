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
  description = "Whether to create VPC resources for Valet default is true for best configurations."
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
  description = "Whether the customer provides their own VPC. When true, VPC lifecycle permissions (create/delete VPC, subnets, IGW, NAT gateways, etc.) are not granted to the CAPA controller."
  default     = false
}

variable "cluster_name" {
  type        = string
  description = "When set, tightens CAPA controller IAM conditions to this specific cluster name. Requires an existing state file — use only on re-runs after the initial deployment."
  default     = null
}

variable "vpc_id" {
  type        = string
  description = "When set, scopes security-group creation to this VPC and applies ec2:Vpc only to EC2 action/resource combinations that support it."
  default     = null
}
