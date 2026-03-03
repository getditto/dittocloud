variable "location" {
  description = "The Azure region where resources will be created"
  type        = string
  default     = "eastus"
}

variable "resource_group_name" {
  description = "Name of the resource group"
  type        = string
  default     = "az-managed-identity"
}

variable "identity_name" {
  description = "Name of the user-assigned managed identity"
  type        = string
  default     = "capz-identity"
}

variable "subscription_id" {
  description = "Azure subscription ID"
  type        = string
  default     = "2aeaecef-0f47-4e9c-ba4d-20f8f04e1f9e"
}

variable "issuer_url" {
  description = "OIDC issuer URL for federated credentials"
  type        = string
  default     = "https://127.0.0.1"
}

variable "controlplane_cidr" {
  description = "Controlplane CIDR range"
  type        = string
  default     = "10.0.1.0/24"
}

variable "node_cidr" {
  description = "Node CIDR range"
  type        = string
  default     = "10.0.2.0/24"
}

variable "vpc_cidr" {
  description = "VPC CIDR range"
  type        = string
  default     = "10.0.0.0/8"
}