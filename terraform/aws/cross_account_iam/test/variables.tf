variable "unrestricted" {
  type        = bool
  description = "Flag to determine if the IAM role should be unrestricted. Warning this will allow Ditto to create IAM roles with any permissions with no boundaries."
  default     = false
}
