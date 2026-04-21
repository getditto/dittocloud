output "endpoint_service" {
  description = "VPC Endpoint Service details"
  value = {
    id           = aws_vpc_endpoint_service.big_peer.id
    service_name = aws_vpc_endpoint_service.big_peer.service_name
    state        = aws_vpc_endpoint_service.big_peer.state
  }
}

output "nlb" {
  description = "Network Load Balancer details"
  value = {
    arn  = data.aws_lb.big_peer_nlb.arn
    name = data.aws_lb.big_peer_nlb.name
    dns_name = data.aws_lb.big_peer_nlb.dns_name
  }
}

output "domain_verification" {
  description = "Domain verification details for private DNS name"
  value = {
    name  = try(aws_vpc_endpoint_service.big_peer.private_dns_name_configuration[0].name, null)
    value = try(aws_vpc_endpoint_service.big_peer.private_dns_name_configuration[0].value, null)
    type  = try(aws_vpc_endpoint_service.big_peer.private_dns_name_configuration[0].type, null)
    state = try(aws_vpc_endpoint_service.big_peer.private_dns_name_configuration[0].state, null)
  }
}
