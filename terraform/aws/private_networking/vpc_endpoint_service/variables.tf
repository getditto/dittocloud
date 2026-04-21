variable "profile" {
  description = "AWS profile to use"
  type        = string
  default     = null
}

variable "region" {
  description = "AWS region where resources are located"
  type        = string
  default     = null
}

variable "big_peer_name" {
  description = "Name of the Big Peer deployment (used to find the NLB via tags)"
  type        = string
}

variable "private_dns_name" {
  description = "Fully qualified domain name for the VPC Endpoint Service private DNS"
  type        = string
}

variable "allowed_principal" {
  description = "AWS principal allowed to create endpoint connections (e.g., AWS account ID, IAM role ARN, or principal ARN)"
  type        = string
}

variable "tags" {
  description = "Additional tags to apply to resources"
  type        = map(string)
  default     = {}
}
