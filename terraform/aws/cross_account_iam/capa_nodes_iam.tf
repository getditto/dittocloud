resource "aws_iam_instance_profile" "capa_nodes" {
  name = "nodes.cluster-api-provider-aws.sigs.k8s.io"
  role = aws_iam_role.capa_nodes.name
}

resource "aws_iam_policy" "capa_nodes" {
  description = "Cluster API nodes"
  name        = "nodes.cluster-api-provider-aws.sigs.k8s.io"
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
      # EC2 tag and IPv6 mutations — cannot add resource conditions;
      # CreateTags is needed by VPC CNI to tag network interfaces
      {
        Effect = "Allow"
        Action = [
          "ec2:AssignIpv6Addresses",
          "ec2:CreateTags",
        ]
        Resource = ["*"]
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
  name = "nodes.cluster-api-provider-aws.sigs.k8s.io"
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
