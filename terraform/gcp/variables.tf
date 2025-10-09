variable "project_id" {
  description = "The project ID to create resources in"
  type        = string
}

variable "region" {
  description = "The region to create resources in"
  type        = string
}

variable "vpc_config" {
  description = "VPC configuration for hosting Kubernetes clusters"
  type = object({
    vpc_name                = optional(string, "ditto-vpc")
    vpc_description         = optional(string, "VPC for Ditto workload clusters")
    auto_create_subnetworks = optional(bool, false)
    routing_mode            = optional(string, "REGIONAL")

    # letting CAPG create subnets and firewall rules
    create_subnets = optional(bool, false)
    # Subnet configuration
    subnet_name        = optional(string, "ditto-subnet")
    subnet_description = optional(string, "Subnet for Ditto workload clusters")
    subnet_cidr        = optional(string, "10.140.0.0/19")

    # Secondary IP ranges for K8s
    pods_secondary_range_name     = optional(string, "pods")
    pods_cidr_range               = optional(string, "100.90.0.0/16")
    services_secondary_range_name = optional(string, "services")
    services_cidr_range           = optional(string, "100.91.0.0/16")

    # Network security
    private_google_access = optional(bool, true)

    # Firewall configuration
    create_default_firewall_rules = optional(bool, false)
  })
  default = {}
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
