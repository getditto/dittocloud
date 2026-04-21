# Data source to find the NLB by tags
data "aws_lb" "big_peer_nlb" {
  tags = {
    "elbv2.k8s.aws/cluster"    = var.big_peer_name
    "ditto.live/valet-cluster" = "true"
    "service.k8s.aws/stack"    = "ingress/ingress-nginx-controller"
  }
}

# Get current AWS account and region
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# Data source to get NLB's network interfaces
data "aws_network_interfaces" "nlb_enis" {
  filter {
    name   = "description"
    values = ["ELB ${data.aws_lb.big_peer_nlb.arn_suffix}"]
  }
}

# Deny policy for CAPA controller role
resource "aws_iam_role_policy" "deny_nlb_modification_controller" {
  name = "DenyNLBModification-${var.big_peer_name}"
  role = var.capa_controller_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Deny"
        Action = [
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:SetSubnets",
        ]
        Resource = data.aws_lb.big_peer_nlb.arn
      },
      {
        Effect = "Deny"
        Action = [
          "ec2:DeleteNetworkInterface"
        ]
        Resource = [
          for eni_id in data.aws_network_interfaces.nlb_enis.ids :
          "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:network-interface/${eni_id}"
        ]
      }
    ]
  })
}

# Deny policy for CAPA control plane role
resource "aws_iam_role_policy" "deny_nlb_modification_controlplane" {
  name = "DenyNLBModification-${var.big_peer_name}"
  role = var.capa_controlplane_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Deny"
        Action = [
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:SetSubnets",
        ]
        Resource = data.aws_lb.big_peer_nlb.arn
      },
      {
        Effect = "Deny"
        Action = [
          "ec2:DeleteNetworkInterface"
        ]
        Resource = [
          for eni_id in data.aws_network_interfaces.nlb_enis.ids :
          "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:network-interface/${eni_id}"
        ]
      }
    ]
  })
}

# Deny policy for CAPA nodes role
resource "aws_iam_role_policy" "deny_nlb_modification_nodes" {
  name = "DenyNLBModification-${var.big_peer_name}"
  role = var.capa_nodes_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Deny"
        Action = [
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:SetSubnets",
        ]
        Resource = data.aws_lb.big_peer_nlb.arn
      },
      {
        Effect = "Deny"
        Action = [
          "ec2:DeleteNetworkInterface"
        ]
        Resource = [
          for eni_id in data.aws_network_interfaces.nlb_enis.ids :
          "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:network-interface/${eni_id}"
        ]
      }
    ]
  })
}
