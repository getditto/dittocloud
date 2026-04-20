# Data source to find the NLB by tags
data "aws_lb" "big_peer_nlb" {
  tags = {
    "elbv2.k8s.aws/cluster"       = var.big_peer_name
    "ditto.live/valet-cluster"    = "true"
  }
}

# VPC Endpoint Service Configuration
resource "aws_vpc_endpoint_service" "big_peer" {
  acceptance_required        = false
  network_load_balancer_arns = [data.aws_lb.big_peer_nlb.arn]
  private_dns_name           = var.private_dns_name

  allowed_principals = [var.allowed_principal]

  tags = merge(
    {
      Name       = "${var.big_peer_name}-endpoint-service"
      BigPeer    = var.big_peer_name
      ManagedBy  = "dittocloud"
    },
    var.tags
  )
}
