resource "aws_iam_instance_profile" "capa_nodes" {
  name = "nodes.cluster-api-provider-aws.sigs.k8s.io"
  role = aws_iam_role.capa_nodes.name
}

resource "aws_iam_policy" "capa_nodes" {
  description = "Cluster API nodes"
  name        = "nodes.cluster-api-provider-aws.sigs.k8s.io"
  policy = jsonencode({
    Statement = [
      {
        Action = [
          "ec2:AssignIpv6Addresses",
          "ec2:DescribeInstances",
          "ec2:DescribeRegions",
          "ec2:CreateTags",
          "ec2:DescribeTags",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeInstanceTypes",
          "ecr:GetAuthorizationToken",
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:GetRepositoryPolicy",
          "ecr:DescribeRepositories",
          "ecr:ListImages",
          "ecr:BatchGetImage"
        ]
        Effect = "Allow"
        Resource = [
          "*"
        ]
      },
      {
        Action = [
          "secretsmanager:DeleteSecret",
          "secretsmanager:GetSecretValue"
        ]
        Effect = "Allow"
        Resource = [
          "arn:*:secretsmanager:*:*:secret:aws.cluster.x-k8s.io/*"
        ]
      },
      {
        Action = [
          "ssm:UpdateInstanceInformation",
          "ssmmessages:CreateControlChannel",
          "ssmmessages:CreateDataChannel",
          "ssmmessages:OpenControlChannel",
          "ssmmessages:OpenDataChannel",
          "s3:GetEncryptionConfiguration"
        ]
        Effect = "Allow"
        Resource = [
          "*"
        ]
      }
    ]
    Version = "2012-10-17"
  })

}

resource "aws_iam_role" "capa_nodes" {
  assume_role_policy = jsonencode({
    Statement = [
      {
        Action = [
          "sts:AssumeRole"
        ]
        Effect = "Allow"
        Principal = {
          Service = [
            "ec2.amazonaws.com"
          ]
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
