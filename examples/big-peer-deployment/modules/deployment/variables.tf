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
}

variable "tags" {
  description = "Tags applied to all resources."
  type        = map(string)
  default     = {}
}
