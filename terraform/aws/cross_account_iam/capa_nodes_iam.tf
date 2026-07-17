resource "aws_iam_instance_profile" "capa_nodes" {
  name = local.iam_names.nodes_instance_profile
  role = aws_iam_role.capa_nodes.name
  tags = local.scope_identity_enabled ? local.tags : null
}

resource "aws_iam_policy" "capa_nodes" {
  description = "Cluster API nodes"
  name        = local.iam_names.nodes_policy
  tags        = local.tags
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # EC2 reads — Describe* does not support resource-level permissions
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeRegions",
          "ec2:DescribeTags",
        ]
        Resource = ["*"]
      },
      # VPC CNI only needs IPv6 assignment here; AmazonEKS_CNI_Policy supplies
      # its network-interface CreateTags permission.
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:AssignIpv6Addresses"]
          Resource = ["arn:aws:ec2:*:*:network-interface/*"]
        },
        local.ec2_vpc_cond != null ? { Condition = local.ec2_vpc_cond } : {}
      ),
      # This policy is attached to worker and control-plane roles. Deny direct
      # assignment of CAPA ownership/bootstrap tags to an existing resource
      # unless CAPA established its role marker during resource creation.
      {
        Effect    = "Deny"
        Action    = ["ec2:CreateTags"]
        Resource  = ["*"]
        Condition = local.ec2_protected_tag_assignment_deny_cond
      },
      # ECR reads — GetAuthorizationToken is account-level; remaining operations
      # require Resource = ["*"] so nodes can pull from any ECR repository
      {
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:DescribeRepositories",
          "ecr:GetAuthorizationToken",
          "ecr:GetDownloadUrlForLayer",
          "ecr:GetRepositoryPolicy",
          "ecr:ListImages",
        ]
        Resource = ["*"]
      },
      # Secrets Manager — scoped to CAPI cluster secrets
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:DeleteSecret",
          "secretsmanager:GetSecretValue",
        ]
        Resource = ["arn:*:secretsmanager:*:*:secret:aws.cluster.x-k8s.io/*"]
      },
      # SSM Session Manager — s3:GetEncryptionConfiguration required by SSM agent
      {
        Effect = "Allow"
        Action = [
          "ssm:UpdateInstanceInformation",
          "ssmmessages:CreateControlChannel",
          "ssmmessages:CreateDataChannel",
          "ssmmessages:OpenControlChannel",
          "ssmmessages:OpenDataChannel",
          "s3:GetEncryptionConfiguration",
        ]
        Resource = ["*"]
      },
    ]
  })
}

resource "aws_iam_role" "capa_nodes" {
  assume_role_policy = jsonencode({
    Statement = [
      {
        Action = ["sts:AssumeRole"]
        Effect = "Allow"
        Principal = {
          Service = ["ec2.amazonaws.com"]
        }
      }
    ]
    Version = "2012-10-17"
  })
  name = local.iam_names.nodes_role
  tags = local.tags
}

resource "aws_iam_role_policy_attachments_exclusive" "capa_nodes" {
  role_name = aws_iam_role.capa_nodes.name
  policy_arns = [
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
    aws_iam_policy.capa_nodes.arn
  ]
}
