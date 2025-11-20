variable "project_id" {
  description = "The project ID to create resources in"
  type        = string
}

variable "region" {
  description = "The region to create resources in"
  type        = string
}

variable "vpc_name" {
  description = "The name of the VPC to create"
  type        = string
  default     = "ditto-vpc"
}

variable "create_default_firewall_rules" {
  description = "Create default firewall rules for internal VPC traffic"
  type        = bool
  default     = false
}

variable "capg_iam" {
  description = "Configuration variables for CAPG"
  type = object({
    # Resources that already exist are refered to by their generated values
    # lives in a Ditto owned org
    # This service account lives in a Ditto owned org
    service_account_name = optional(string, "valet-controllers")
    # This service account lives in a Ditto owned org
    service_account_project = optional(string, "ditto-valet-ops")
    # GCP managed role with appropriate permissions
    control_plane_role_id = optional(string, "roles/container.serviceAgent")
    node_role_id          = optional(string, "roles/compute.viewer")

    # These are created for the project
    controller_custom_role_name        = optional(string, "DittoCapg")
    control_plane_service_account_name = optional(string, "ditto-control-plane")
    node_service_account_name          = optional(string, "ditto-node")
  })
  default = {}
}

variable "iam_condition_tag" {
  description = <<EOT
    The namespaced name and value of the tag to use for conditional IAM policies.
    If the tag name matches the tag value, resource permissions will be granted.
    EOT
  type = object({
    short_name = optional(string, "managed-by-ditto")
    value      = optional(string, "true")
  })
  default = {}
}

variable "crossplane_iam" {
  description = "IAM configuration for Crossplane"
  type = object({
    # This service account lives in a Ditto owned org
    service_account_name = optional(string, "valet-crossplane")
    # This service account lives in a Ditto owned org
    service_account_project = optional(string, "ditto-valet-ops")
    # This custom role is created in customer project
    custom_role_name = optional(string, "DittoCrossplane")
    # This custom role is created in customer project
    crossplane_trust_role = optional(string, "DittoCrossplaneIAMTrust")
  })
  default = {}
}

variable "velero_iam" {
  description = "IAM configuration for Velero backup and restore operations"
  type = object({
    custom_role_name = optional(string, "DittoVelero")
  })
  default = {}
}

variable "ditto_management_project" {
  description = "The project ID for Ditto management operations"
  type        = string
  default     = "ditto-valet-ops"
}

variable "env_suffix" {
  description = "For Ditto development purposes only."
  default     = ""
}
