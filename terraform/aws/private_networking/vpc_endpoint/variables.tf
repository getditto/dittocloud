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

variable "service_name" {
  description = "VPC Endpoint Service name (e.g., com.amazonaws.vpce.us-east-2.vpce-svc-xxx)"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID to deploy the endpoint into"
  type        = string
}

variable "subnet_ids" {
  description = "List of subnet IDs to deploy the endpoint into"
  type        = list(string)
}

variable "private_dns_name" {
  description = "Private DNS name for the endpoint (must match the endpoint service DNS name)"
  type        = string
}

variable "tags" {
  description = "Additional tags to apply to resources"
  type        = map(string)
  default     = {}
}
