output "endpoint" {
  description = "VPC Endpoint details"
  value = {
    id               = aws_vpc_endpoint.main.id
    state            = aws_vpc_endpoint.main.state
    service_name     = aws_vpc_endpoint.main.service_name
    vpc_id           = aws_vpc_endpoint.main.vpc_id
    subnet_ids       = aws_vpc_endpoint.main.subnet_ids
    network_interface_ids = aws_vpc_endpoint.main.network_interface_ids
  }
}

output "security_group" {
  description = "Security group created for the VPC endpoint"
  value = {
    id   = aws_security_group.vpc_endpoint.id
    name = aws_security_group.vpc_endpoint.name
  }
}

output "dns" {
  description = "DNS configuration for the VPC endpoint"
  value = {
    private_dns_enabled = aws_vpc_endpoint.main.private_dns_enabled
    dns_entries         = aws_vpc_endpoint.main.dns_entry
  }
}

output "vpc_cidr" {
  description = "VPC CIDR block used for security group rules"
  value       = data.aws_vpc.selected.cidr_block
}
