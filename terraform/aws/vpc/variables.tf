variable "vpc_name" {
  description = "The name of the VPC."
  default     = "ditto"
}

variable "vpc_cidr" {
  description = "The IPv4 CIDR block for the VPC."
  default     = "10.210.0.0/16"
}

variable "tags" {
  type = map(string)
  default = {
    GithubRepo = "terraform-modules"
    GithubOrg  = "getditto"
  }
}

variable "region" {
  description = "The AWS region to deploy the resources in"
  type        = string
  default     = "us-east-1"
}
