variable "enable_eks" {
  type        = bool
  description = "Provision EKS IAM: the CAPA controller EKS policy and the Karpenter controller permissions on the cluster-resources boundary."
  default     = false
}

variable "scope_ref" {
  type        = string
  description = "Immutable generated deployment-scope reference. Null preserves all legacy IAM names and paths."
  default     = null
  nullable    = true

  validation {
    condition = (
      var.scope_ref == null ||
      (length(var.scope_ref) == 30 && can(regex("^dsc-[0-7][0-9a-hjkmnp-tv-z]{25}$", var.scope_ref)))
    )
    error_message = "scope_ref must be null or exactly 30 characters in generated dsc-<lowercase-crockford-ulid> form."
  }
}

variable "scope_identity_ref" {
  type        = string
  description = "Immutable deployment-scope identity used for tags and tag-policy enforcement. It may be set while scope_ref stays null so the default scope preserves legacy names."
  default     = null
  nullable    = true

  validation {
    condition = (
      var.scope_identity_ref == null ||
      (length(var.scope_identity_ref) == 30 && can(regex("^dsc-[0-7][0-9a-hjkmnp-tv-z]{25}$", var.scope_identity_ref)))
    )
    error_message = "scope_identity_ref must be null or exactly 30 characters in generated dsc-<lowercase-crockford-ulid> form."
  }
}

variable "region" {
  type        = string
  description = "AWS Region owned by this deployment scope. Null preserves the provider Region used by legacy callers."
  default     = null
  nullable    = true

  validation {
    condition     = var.region == null || can(regex("^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$", var.region))
    error_message = "region must be null or a valid AWS Region name."
  }
}

variable "create_admin_view_role" {
  type        = bool
  description = "Whether to create the shared account-wide IAM admin view role. Scoped module instances must disable it."
  default     = true
}

variable "tags" {
  type        = map(string)
  description = "Additional tags for scope-owned IAM resources. The reserved scope identity tag cannot be overridden."
  default     = {}
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

variable "customer_managed_vpc" {
  type        = bool
  description = "Whether the customer provides their own VPC. When true, the VPC lifecycle policy is not attached to the CAPA controller role."
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

variable "vpc_subnet_ids" {
  type        = list(string)
  description = "Subnet IDs in vpc_id that ELB load balancers may use. When vpc_id is set, an empty list intentionally denies ELB subnet selection."
  default     = []

  validation {
    # ELB actions live in their own boundary policy now; measured safe well
    # past 60 subnets, so 40 leaves a comfortable margin.
    condition     = length(var.vpc_subnet_ids) <= 40
    error_message = "vpc_subnet_ids may contain at most 40 Kubernetes load-balancer subnets so the generated ELB boundary policy stays within AWS's 6,144-character managed-policy limit."
  }
}

variable "ec2_project_tag" {
  type        = string
  description = "Value for the ec2:ResourceTag/ditto:project condition in the cluster resources boundary policy. Controls which tagged EC2 security groups the boundary policy allows ingress/egress modifications on."
  default     = "ditto"
}
