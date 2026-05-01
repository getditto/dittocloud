variable "private_dns_enabled" {
  description = "Whether to associate a private hosted zone with the specified VPC for the endpoint service. Defaults to true to preserve existing behavior."
  type        = bool
  default     = true
}

# VPC Endpoint for accessing the Endpoint Service
resource "aws_vpc_endpoint" "main" {
  vpc_id              = var.vpc_id
  service_name        = var.service_name
  vpc_endpoint_type   = "Interface"
  subnet_ids          = var.subnet_ids
  security_group_ids  = [aws_security_group.vpc_endpoint.id]

  private_dns_enabled = var.private_dns_enabled

  tags = merge(
    {
      Name             = "endpoint-${var.private_dns_name}"
      ServiceName      = var.service_name
      PrivateDNSName   = var.private_dns_name
      ManagedBy        = "dittocloud"
    },
    var.tags
  )
}
