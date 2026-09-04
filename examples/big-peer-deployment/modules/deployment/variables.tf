variable "deployment" {
  description = "Single deployment definition from the YAML config."
  type = object({
    region = string
    vpc = object({
      create             = bool
      cidr               = optional(string)
      availability_zones = optional(list(string))
      private_subnets    = optional(list(string))
      public_subnets     = optional(list(string))
      vpc_id             = optional(string)
      private_subnet_ids = optional(list(string))
      public_subnet_ids  = optional(list(string))
    })
    cluster = object({
      name                 = string
      version              = string
      managed_node_groups  = optional(map(any), {})
      admin_principal_arns = optional(list(string), [])
    })
  })

  # The upstream EKS module defaults enable_cluster_creator_admin_permissions
  # to false, so without at least one admin ARN the access_entries map renders
  # empty and no identity can reach the cluster API. Fail fast at plan.
  validation {
    condition     = length(var.deployment.cluster.admin_principal_arns) > 0
    error_message = "cluster.admin_principal_arns must list at least one IAM role ARN (e.g. your SSO admin role). Without it, no identity is granted cluster access."
  }
}

variable "tags" {
  description = "Tags applied to all resources."
  type        = map(string)
  default     = {}
}
