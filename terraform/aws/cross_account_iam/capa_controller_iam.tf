module "capa_controller_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role"
  version = "6.4.0"

  create          = true
  name            = "controllers.cluster-api-provider-aws.sigs.k8s.io"
  use_name_prefix = false
  description     = "Ditto Cross Account Infrastructure Controller"

  trust_policy_permissions = {
    TrustedRoles = {
      actions = ["sts:AssumeRole", "sts:TagSession"]
      principals = [
        {
          type        = "AWS"
          identifiers = var.controller_trusted_role_arns
        }
      ]
    }
  }

  policies = merge(
    {
      capa-controller-base = aws_iam_policy.capa_controller_base.arn
    },
    !var.customer_managed_vpc ? {
      capa-controller-vpc-lifecycle = aws_iam_policy.capa_controller_vpc_lifecycle[0].arn
    } : {}
  )
}

# Core CAPA controller permissions — always attached regardless of VPC ownership.
# Destructive operations are gated by the ditto.live/managed_by=terraform resource tag
# so the controller can only affect resources it created.
resource "aws_iam_policy" "capa_controller_base" {
  name = "ditto-capa-controller-policy"
  tags = local.tags
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # Read-only describes — EC2 Describe* does not support resource-level permissions
      {
        Effect = "Allow"
        Action = [
          "ec2:DescribeAccountAttributes",
          "ec2:DescribeAddresses",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeDhcpOptions",
          "ec2:DescribeImages",
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeInternetGateways",
          "ec2:DescribeLaunchTemplates",
          "ec2:DescribeLaunchTemplateVersions",
          "ec2:DescribeNatGateways",
          "ec2:DescribeNetworkInterfaceAttribute",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeRouteTables",
          "ec2:DescribeSecurityGroupRules",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSubnets",
          "ec2:DescribeVolumes",
          "ec2:DescribeVpcAttribute",
          "ec2:DescribeVpcEndpoints",
          "ec2:DescribeVpcs",
          "tag:GetResources",
        ]
        Resource = ["*"]
      },
      # Route and tag mutations — cannot add ResourceTag: route tables may be customer-owned
      # in BYO-VPC, and CreateTags/DeleteTags must reach resources of any type
      {
        Effect = "Allow"
        Action = [
          "ec2:CreateRoute",
          "ec2:CreateTags",
          "ec2:DeleteRoute",
          "ec2:DeleteTags",
          "ec2:ModifyNetworkInterfaceAttribute",
          "ec2:ReplaceRoute",
        ]
        Resource = ["*"]
      },
      # ELB reads — Describe* does not support resource-level permissions
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DescribeListenerAttributes",
          "elasticloadbalancing:DescribeListenerCertificates",
          "elasticloadbalancing:DescribeListeners",
          "elasticloadbalancing:DescribeLoadBalancerAttributes",
          "elasticloadbalancing:DescribeLoadBalancers",
          "elasticloadbalancing:DescribeRules",
          "elasticloadbalancing:DescribeSSLPolicies",
          "elasticloadbalancing:DescribeTags",
          "elasticloadbalancing:DescribeTargetGroupAttributes",
          "elasticloadbalancing:DescribeTargetGroups",
          "elasticloadbalancing:DescribeTargetHealth",
        ]
        Resource = ["*"]
      },
      # ELB v2 LB + TG creates — require elbv2.k8s.aws/cluster tag at request time
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:CreateLoadBalancer",
          "elasticloadbalancing:CreateTargetGroup",
        ]
        Resource  = ["*"]
        Condition = local.elb_create_cond
      },
      # ELB listener + rule creates — listeners don't carry the cluster tag so
      # tag conditions cannot be applied; scoped by association with tagged LBs above
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:CreateListener",
          "elasticloadbalancing:CreateRule",
        ]
        Resource = ["*"]
      },
      # ELB v2 LB + TG mutations — scoped by ARN type + cluster tag on resource
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:SetIpAddressType",
          "elasticloadbalancing:SetSecurityGroups",
          "elasticloadbalancing:SetSubnets",
          "elasticloadbalancing:DeleteTargetGroup",
          "elasticloadbalancing:ModifyTargetGroup",
          "elasticloadbalancing:ModifyTargetGroupAttributes",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
          "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
        ]
        Condition = local.elb_resource_cond
      },
      # ELB listener + rule mutations — scoped by ARN type only; listeners do not
      # carry the elbv2.k8s.aws/cluster tag so resource tag conditions cannot be applied
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:AddListenerCertificates",
          "elasticloadbalancing:DeleteListener",
          "elasticloadbalancing:DeleteRule",
          "elasticloadbalancing:ModifyListener",
          "elasticloadbalancing:ModifyListenerAttributes",
          "elasticloadbalancing:ModifyRule",
          "elasticloadbalancing:RemoveListenerCertificates",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:listener/app/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener/net/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener-rule/app/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener-rule/net/*/*/*",
        ]
      },
      # ELB target registration — scoped by TG ARN + cluster tag on resource
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DeregisterTargets",
          "elasticloadbalancing:RegisterTargets",
        ]
        Resource  = ["arn:aws:elasticloadbalancing:*:*:targetgroup/*/*"]
        Condition = local.elb_resource_cond
      },
      # ELB tag operations on LBs/TGs at creation time
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:AddTags",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
          "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
        ]
        Condition = local.elb_add_tags_create_cond
      },
      # ELB tag mutations on existing LBs/TGs — require cluster tag on resource
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:AddTags",
          "elasticloadbalancing:RemoveTags",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
          "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
        ]
        Condition = local.elb_tag_mutation_cond
      },
      # ELB tag mutations on listeners/rules — scoped by ARN type only
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:AddTags",
          "elasticloadbalancing:RemoveTags",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:listener/app/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener/net/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener-rule/app/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener-rule/net/*/*/*",
        ]
      },
      # Security group rule mutations — scoped to cluster-managed SGs
      {
        Effect = "Allow"
        Action = [
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:RevokeSecurityGroupIngress",
        ]
        Resource  = ["arn:aws:ec2:*:*:security-group/*"]
        Condition = local.ec2_resource_cond
      },
      # Instance attribute mutations — scoped to cluster-managed instances
      {
        Effect    = "Allow"
        Action    = ["ec2:ModifyInstanceAttribute"]
        Resource  = ["arn:aws:ec2:*:*:instance/*"]
        Condition = local.ec2_resource_cond
      },
      # Create operations — require cluster ownership tag at request time
      {
        Effect = "Allow"
        Action = [
          "ec2:CreateLaunchTemplate",
          "ec2:CreateLaunchTemplateVersion",
          "ec2:CreateSecurityGroup",
          "ec2:RunInstances",
        ]
        Resource  = ["*"]
        Condition = local.ec2_create_cond
      },
      # Instance termination — scoped to cluster-managed instances by ARN + resource tag
      {
        Effect    = "Allow"
        Action    = ["ec2:TerminateInstances"]
        Resource  = ["arn:aws:ec2:*:*:instance/*"]
        Condition = local.ec2_resource_cond
      },
      # Security group deletion — scoped to cluster-managed SGs by ARN + resource tag
      {
        Effect    = "Allow"
        Action    = ["ec2:DeleteSecurityGroup"]
        Resource  = ["arn:aws:ec2:*:*:security-group/*"]
        Condition = local.ec2_resource_cond
      },
      # Network interface deletion — scoped to cluster-managed NICs by ARN + resource tag
      {
        Effect = "Allow"
        Action = [
          "ec2:DeleteNetworkInterface",
          "ec2:DetachNetworkInterface",
        ]
        Resource  = ["arn:aws:ec2:*:*:network-interface/*"]
        Condition = local.ec2_resource_cond
      },
      # Launch template deletion — scoped to cluster-managed templates by ARN + resource tag
      {
        Effect = "Allow"
        Action = [
          "ec2:DeleteLaunchTemplate",
          "ec2:DeleteLaunchTemplateVersions",
        ]
        Resource  = ["arn:aws:ec2:*:*:launch-template/*"]
        Condition = local.ec2_resource_cond
      },
      # IAM service-linked roles — each scoped to its specific service ARN
      {
        Effect   = "Allow"
        Action   = ["iam:CreateServiceLinkedRole"]
        Resource = ["arn:*:iam::*:role/aws-service-role/elasticloadbalancing.amazonaws.com/AWSServiceRoleForElasticLoadBalancing"]
        Condition = {
          StringLike = {
            "iam:AWSServiceName" = "elasticloadbalancing.amazonaws.com"
          }
        }
      },
      {
        Effect   = "Allow"
        Action   = ["iam:CreateServiceLinkedRole"]
        Resource = ["arn:*:iam::*:role/aws-service-role/spot.amazonaws.com/AWSServiceRoleForEC2Spot"]
        Condition = {
          StringLike = {
            "iam:AWSServiceName" = "spot.amazonaws.com"
          }
        }
      },
      # IAM PassRole — scoped to CAPA roles only
      {
        Effect   = "Allow"
        Action   = ["iam:PassRole"]
        Resource = ["arn:*:iam::*:role/*.cluster-api-provider-aws.sigs.k8s.io"]
      },
      # Secrets Manager — scoped to CAPI cluster secrets
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:CreateSecret",
          "secretsmanager:DeleteSecret",
          "secretsmanager:TagResource",
        ]
        Resource = ["arn:*:secretsmanager:*:*:secret:aws.cluster.x-k8s.io/*"]
      },
      # OIDC provider management for IRSA — scoped to OIDC provider resource type
      {
        Effect = "Allow"
        Action = [
          "iam:AddClientIDToOpenIDConnectProvider",
          "iam:CreateOpenIDConnectProvider",
          "iam:DeleteOpenIDConnectProvider",
          "iam:GetOpenIDConnectProvider",
          "iam:ListOpenIDConnectProviders",
          "iam:TagOpenIDConnectProvider",
          "iam:UpdateOpenIDConnectProviderThumbprint",
        ]
        Resource = ["arn:aws:iam::*:oidc-provider/*"]
      },
      # S3 — scoped to ditto-prefixed buckets (OIDC issuer + cluster state)
      {
        Effect = "Allow"
        Action = [
          "s3:CreateBucket",
          "s3:DeleteBucket",
          "s3:DeleteObject",
          "s3:GetObject",
          "s3:ListBucket",
          "s3:PutBucketAcl",
          "s3:PutBucketOwnershipControls",
          "s3:PutBucketPolicy",
          "s3:PutBucketPublicAccessBlock",
          "s3:PutBucketTagging",
          "s3:PutLifecycleConfiguration",
          "s3:PutObject",
        ]
        Resource = [
          "arn:aws:s3:::ditto-*",
          "arn:aws:s3:::ditto-*/*",
        ]
      },
      # OIDC discovery documents — scoped to issuer bucket paths
      {
        Effect = "Allow"
        Action = ["s3:PutObjectAcl"]
        Resource = [
          "arn:aws:s3:::ditto-issuer-*/.well-known/openid-configuration",
          "arn:aws:s3:::ditto-issuer-*/openid/v1/jwks",
        ]
      },
    ]
  })
}

# VPC lifecycle permissions — only attached when Ditto manages the VPC.
# Omitted when customer_managed_vpc = true; the base policy above covers
# everything the controller needs inside an existing VPC.
resource "aws_iam_policy" "capa_controller_vpc_lifecycle" {
  count = var.customer_managed_vpc ? 0 : 1
  name  = "ditto-capa-controller-vpc-lifecycle-policy"
  tags  = local.tags
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # VPC infrastructure create — require ditto.live/managed_by tag at request time
      {
        Effect = "Allow"
        Action = [
          "ec2:AllocateAddress",
          "ec2:CreateInternetGateway",
          "ec2:CreateNatGateway",
          "ec2:CreateRouteTable",
          "ec2:CreateSubnet",
          "ec2:CreateVpc",
          "ec2:CreateVpcEndpoint",
        ]
        Resource = ["*"]
        Condition = {
          StringEquals = {
            "aws:RequestTag/ditto.live/managed_by" = "terraform"
          }
        }
      },
      # VPC infrastructure associations/modifications — non-destructive, no tag condition required
      {
        Effect = "Allow"
        Action = [
          "ec2:AssociateRouteTable",
          "ec2:AssociateVpcCidrBlock",
          "ec2:AttachInternetGateway",
          "ec2:DisassociateVpcCidrBlock",
          "ec2:ModifySubnetAttribute",
          "ec2:ModifyVpcAttribute",
          "ec2:ModifyVpcEndpoint",
        ]
        Resource = ["*"]
      },
      # VPC infrastructure delete — require ditto.live/managed_by tag on the resource
      {
        Effect = "Allow"
        Action = [
          "ec2:DeleteInternetGateway",
          "ec2:DeleteNatGateway",
          "ec2:DeleteRouteTable",
          "ec2:DeleteSubnet",
          "ec2:DeleteVpc",
          "ec2:DeleteVpcEndpoints",
          "ec2:DetachInternetGateway",
          "ec2:DisassociateAddress",
          "ec2:DisassociateRouteTable",
          "ec2:ReleaseAddress",
        ]
        Resource = ["*"]
        Condition = {
          StringEquals = {
            "ec2:ResourceTag/ditto.live/managed_by" = "terraform"
          }
        }
      },
    ]
  })
}

moved {
  from = aws_iam_policy.capa_controller_policy
  to   = aws_iam_policy.capa_controller_base
}

# EKS permissions for the CAPA controller (managed control plane, node groups,
# addons, access entries, and the IRSA OIDC provider). Kept as a separate managed
# policy so the base kubeadm policy stays under the IAM size limit and EKS perms
# are independently versioned. Based on `clusterawsadm bootstrap iam print-policy
# --document AWSIAMManagedPolicyControllersEKS`, plus the access-entry and
# OpenIDConnectProvider statements CAPA >= v2.10 needs that clusterawsadm omits.
resource "aws_iam_policy" "capa_controller_eks_policy" {
  count  = var.enable_eks ? 1 : 0
  name   = "ditto-capa-controller-eks-policy"
  policy = file("${path.module}/policies/capa-controller-eks-policy.json")
  tags   = local.tags
}
