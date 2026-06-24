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

variable "tags" {
  type = map(string)
  default = {
    GithubRepo = "terraform-modules"
    GithubOrg  = "getditto"
  }
}
