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
  description = "Name of the Big Peer deployment (used to find the NLB and for policy naming)"
  type        = string
}

variable "capa_controller_role_name" {
  description = "Name of the CAPA controller IAM role"
  type        = string
}

variable "capa_controlplane_role_name" {
  description = "Name of the CAPA control plane IAM role"
  type        = string
}

variable "capa_nodes_role_name" {
  description = "Name of the CAPA nodes IAM role"
  type        = string
}
