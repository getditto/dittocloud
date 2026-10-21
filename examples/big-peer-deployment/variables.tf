variable "config_path" {
  description = "Path to the YAML file describing deployments."
  type        = string
  default     = "config.yaml"
}

variable "region" {
  description = "Default AWS region used by the root provider (overridden per deployment)."
  type        = string
  default     = "us-east-1"
}

variable "tags" {
  description = "Tags applied to all resources."
  type        = map(string)
  default = {
    GithubRepo = "dittocloud"
    GithubOrg  = "getditto"
  }
}

variable "primary_deployment" {
  description = "Which deployment the generated .env describes. Optional when only one is defined."
  type        = string
  default     = null
}
