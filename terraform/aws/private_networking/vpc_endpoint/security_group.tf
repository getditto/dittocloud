# Data source to get VPC CIDR block
data "aws_vpc" "selected" {
  id = var.vpc_id
}

# Security group for VPC endpoint
resource "aws_security_group" "vpc_endpoint" {
  name_prefix = "vpc-endpoint-${var.private_dns_name}-"
  description = "Security group for VPC endpoint to ${var.private_dns_name}"
  vpc_id      = var.vpc_id

  # Allow all inbound traffic from VPC CIDR
  ingress {
    description = "Allow all traffic from VPC"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [data.aws_vpc.selected.cidr_block]
  }

  # Allow all outbound traffic
  egress {
    description = "Allow all outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(
    {
      Name      = "vpc-endpoint-${var.private_dns_name}"
      ManagedBy = "dittocloud"
    },
    var.tags
  )

  lifecycle {
    create_before_destroy = true
  }
}
