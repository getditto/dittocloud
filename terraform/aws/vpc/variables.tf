variable "vpc_name" {
  description = "The name of the VPC."
  default     = "ditto"
}

variable "vpc_cidr" {
  description = "The IPv4 CIDR block for the VPC."
  default     = "10.210.0.0/16"
}

variable "region" {
  description = "The AWS region to deploy resources in. Overrides the provider region when set."
  type        = string
  default     = null
}

variable "enable_database_subnets" {
  description = "Whether to create database subnets (/24 per AZ) and a database subnet group."
  type        = bool
  default     = false
}

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

variable "tags" {
  type = map(string)
  default = {
    GithubRepo = "terraform-modules"
    GithubOrg  = "getditto"
  }
}
