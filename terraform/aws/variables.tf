####################################################################################################
# Shared Variables
####################################################################################################
variable "region" {
  description = "The AWS region to deploy the resources in"
  type        = string
  default     = "us-east-1"
}

variable "profile" {
  description = "The AWS profile to use for authentication"
  type        = string
  default     = "default"
}

####################################################################################################
# IAM Policies
####################################################################################################
variable "controller_trusted_role_arns" {
  type = list(string)

  validation {
    condition = alltrue([
      for value in var.controller_trusted_role_arns : can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", value))
    ])
    # condition     = can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", var.server_role_arn))
    error_message = "Must be a valid AWS IAM role ARN."
  }

  default = [
    # Central Operations
    "arn:aws:iam::851725645787:role/controllers.cluster-api-provider-aws.sigs.k8s.io",
    # Production Operations Valet Control
    "arn:aws:iam::851725645787:role/valet-controllers.cluster-api-provider-aws.sigs.k8s.io",
  ]
}

variable "iam_trusted_role_arns" {
  type = list(string)

  validation {
    condition = alltrue([
      for value in var.iam_trusted_role_arns : can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", value))
    ])
    # condition     = can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", var.server_role_arn))
    error_message = "Must be a valid AWS IAM role ARN."
  }

  default = [
    # Central Operations
    "arn:aws:iam::851725645787:role/trust-editor.ditto.live",
    # Production Operations Valet Control
    "arn:aws:iam::851725645787:role/valet-trust-editor.ditto.live",
  ]
}

variable "iam_trusted_operations_principal_arns" {
  type    = string
  default = "arn:aws:iam::851725645787:root"
}

variable "iam_trusted_operations_condition_arns" {
  type = list(string)

  validation {
    condition = alltrue([
      for value in var.iam_trusted_operations_condition_arns : can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", value))
    ])
    # condition     = can(regex("^arn:aws:iam::[[:digit:]]{12}:role/.+", var.server_role_arn))
    error_message = "Must be a valid AWS IAM role ARN."
  }

  default = [
    # Allow Ditto SRE UI View Only access
    "arn:aws:iam::851725645787:role/aws-reserved/sso.amazonaws.com/*"
  ]
}

variable "unrestricted" {
  type        = bool
  description = "Flag to determine if the IAM role should be unrestricted. Warning this will allow Ditto to create IAM roles with any permissions with no boundaries."
  default     = false
}


####################################################################################################
# VPC
####################################################################################################

variable "vpc_name" {
  description = "The name of the VPC."
  default     = "ditto"
}

variable "vpc_cidr" {
  description = "The IPv4 CIDR block for the VPC."
  default     = "10.210.0.0/16"
}

variable "tags" {
  type    = map(string)
  default = {}
}
