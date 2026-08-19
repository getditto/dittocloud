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

variable "additional_authorized_vpc_ids" {
  type        = list(string)
  description = "Extra VPC IDs, beyond vpc_id, also authorized on this role's ec2:Vpc-scoped conditions."
  default     = []

  validation {
    condition     = var.vpc_id != null || length(var.additional_authorized_vpc_ids) == 0
    error_message = "additional_authorized_vpc_ids requires vpc_id to be set — there is no VPC-scoped policy to extend it onto otherwise."
  }
}

variable "vpc_subnet_ids" {
  type        = list(string)
  description = "Subnet IDs in vpc_id that ELB load balancers may use. When vpc_id is set, an empty list intentionally denies ELB subnet selection."
  default     = []

  validation {
    # Measured against the actual minified policy (terraform test, not a live
    # AWS call — the old "<=9" bound was never actually exercised against
    # IAM's real quota by any test). Only scoped mode (scope_ref set) embeds
    # the long scope_ref-suffixed PassRole ARNs that eat into this budget; the
    # legacy shared/default boundary uses a short wildcard PassRole ARN and
    # keeps the original 9-subnet headroom.
    #
    # The boundary used to spend the subnet list twice: CreateLoadBalancer and
    # SetSubnets were separate statements with byte-identical Resource and
    # Condition blocks. They are now one statement, which removed a whole
    # duplicate of the list and its condition scaffolding. Current minified
    # sizes in scoped mode: 6 subnets without EKS is 5,769; 6 subnets with
    # enable_eks = true (which adds the EKS control-plane role ARN to the
    # PassRole list) is 5,862, comfortably inside the 6,144 limit where it
    # previously came to 6,196 and blew the quota.
    #
    # 6 is the meaningful bound because a Dittocloud-managed VPC always yields
    # 3 AZs x (1 private + 1 public) = 6 load-balancer subnets. The old
    # enable_eks bound of 4 made a scoped EKS scope with a Dittocloud VPC
    # impossible to build at all.
    condition = length(var.vpc_subnet_ids) <= (
      var.scope_ref == null ? 9 : 6
    )
    error_message = "vpc_subnet_ids may contain at most 9 Kubernetes load-balancer subnets in legacy/default mode, or 6 in scoped mode, so the generated permissions boundary remains within AWS's 6,144-character managed-policy limit."
  }
}

variable "ec2_project_tag" {
  type        = string
  description = "Value for the ec2:ResourceTag/ditto:project condition in the cluster resources boundary policy. Controls which tagged EC2 security groups the boundary policy allows ingress/egress modifications on."
  default     = "ditto"
}
