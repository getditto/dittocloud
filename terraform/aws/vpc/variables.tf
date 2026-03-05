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

variable "tags" {
  type = map(string)
  default = {
    GithubRepo = "terraform-modules"
    GithubOrg  = "getditto"
  }
}
