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
      capa-controller-elb  = aws_iam_policy.capa_controller_elb.arn
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
          "ec2:DescribeTags",
          "ec2:DescribeVpcs",
          "ec2:GetSecurityGroupsForVpc",
          "tag:GetResources",
        ]
        Resource = ["*"]
      },
      # Route mutations — cannot add ResourceTag: route tables may be customer-owned in BYO-VPC
      {
        Effect = "Allow"
        Action = [
          "ec2:CreateRoute",
          "ec2:DeleteRoute",
          "ec2:ModifyNetworkInterfaceAttribute",
          "ec2:ReplaceRoute",
        ]
        Resource = ["*"]
      },
      # Tag mutations — must reach all resource types (VPC, subnets, instances, SGs, etc.),
      # including client-owned resources in BYO-VPC, so resource conditions can't be used.
      # Scoped by tag key namespace instead: only kubernetes.io/*, k8s.io/*,
      # sigs.k8s.io/*, and ditto.live/* keys may be added or removed.
      {
        Effect = "Allow"
        Action = [
          "ec2:CreateTags",
          "ec2:DeleteTags",
        ]
        Resource = ["*"]
        Condition = {
          "ForAllValues:StringLike" = {
            "aws:TagKeys" = [
              "kubernetes.io/*",
              "k8s.io/*",
              "sigs.k8s.io/*",
              "ditto.live/*",
              "Name",
              "MachineName",
            ]
          }
        }
      },
      # Security group rule mutations — cluster scoped in phase-2 and VPC scoped
      # when vpc_id is set. Security groups expose the ec2:Vpc condition key.
      merge(
        {
          Effect = "Allow"
          Action = [
            "ec2:AuthorizeSecurityGroupIngress",
            "ec2:RevokeSecurityGroupEgress",
            "ec2:RevokeSecurityGroupIngress",
          ]
          Resource = ["arn:aws:ec2:*:*:security-group/*"]
        },
        local.ec2_vpc_resource_cond != null ? { Condition = local.ec2_vpc_resource_cond } : {}
      ),
      # Instance attribute mutations — phase-2: scoped to cluster-managed instances
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:ModifyInstanceAttribute"]
          Resource = ["arn:aws:ec2:*:*:instance/*"]
        },
        local.ec2_resource_cond != null ? { Condition = local.ec2_resource_cond } : {}
      ),
      # Launch template creation supports request tags but not ec2:Vpc.
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:CreateLaunchTemplate"]
          Resource = ["arn:aws:ec2:*:*:launch-template/*"]
        },
        local.ec2_create_cond != null ? { Condition = local.ec2_create_cond } : {}
      ),
      # A launch template version mutates an existing template. AWS exposes
      # ResourceTag, not RequestTag, for this action.
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:CreateLaunchTemplateVersion"]
          Resource = ["arn:aws:ec2:*:*:launch-template/*"]
        },
        local.ec2_resource_cond != null ? { Condition = local.ec2_resource_cond } : {}
      ),
      # Security group creation authorizes the new security group and its target
      # VPC separately. Request-tag conditions only apply to the SG resource.
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:CreateSecurityGroup"]
          Resource = ["arn:aws:ec2:*:*:security-group/*"]
        },
        local.ec2_create_cond != null ? { Condition = local.ec2_create_cond } : {}
      ),
      {
        Effect   = "Allow"
        Action   = ["ec2:CreateSecurityGroup"]
        Resource = [local.ec2_vpc_arn != null ? local.ec2_vpc_arn : "arn:aws:ec2:*:*:vpc/*"]
      },
      # RunInstances authorizes several resource types. Require the ownership tag
      # only on the instance created by the request.
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:RunInstances"]
          Resource = ["arn:aws:ec2:*:*:instance/*"]
        },
        local.ec2_create_cond != null ? { Condition = local.ec2_create_cond } : {}
      ),
      # Subnets, security groups, and network interfaces expose ec2:Vpc.
      merge(
        {
          Effect = "Allow"
          Action = ["ec2:RunInstances"]
          Resource = [
            "arn:aws:ec2:*:*:network-interface/*",
            "arn:aws:ec2:*:*:security-group/*",
            "arn:aws:ec2:*:*:subnet/*",
          ]
        },
        local.ec2_vpc_cond != null ? { Condition = local.ec2_vpc_cond } : {}
      ),
      # Permit every other RunInstances resource context (for example AMIs,
      # volumes, key pairs, snapshots, and launch templates) without applying
      # condition keys those resource types do not expose.
      {
        Effect = "Allow"
        Action = ["ec2:RunInstances"]
        NotResource = [
          "arn:aws:ec2:*:*:instance/*",
          "arn:aws:ec2:*:*:network-interface/*",
          "arn:aws:ec2:*:*:security-group/*",
          "arn:aws:ec2:*:*:subnet/*",
        ]
      },
      # Instance termination — phase-2: scoped to cluster-managed instances
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:TerminateInstances"]
          Resource = ["arn:aws:ec2:*:*:instance/*"]
        },
        local.ec2_resource_cond != null ? { Condition = local.ec2_resource_cond } : {}
      ),
      # Security group deletion — phase-2: scoped to cluster-managed SGs
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:DeleteSecurityGroup"]
          Resource = ["arn:aws:ec2:*:*:security-group/*"]
        },
        local.ec2_vpc_resource_cond != null ? { Condition = local.ec2_vpc_resource_cond } : {}
      ),
      # Network interface deletion — phase-2: scoped to cluster-managed NICs
      merge(
        {
          Effect = "Allow"
          Action = [
            "ec2:DeleteNetworkInterface",
            "ec2:DetachNetworkInterface",
          ]
          Resource = ["arn:aws:ec2:*:*:network-interface/*"]
        },
        local.ec2_vpc_resource_cond != null ? { Condition = local.ec2_vpc_resource_cond } : {}
      ),
      # Launch template deletion — phase-2: scoped to cluster-managed templates
      merge(
        {
          Effect = "Allow"
          Action = [
            "ec2:DeleteLaunchTemplate",
            "ec2:DeleteLaunchTemplateVersions",
          ]
          Resource = ["arn:aws:ec2:*:*:launch-template/*"]
        },
        local.ec2_resource_cond != null ? { Condition = local.ec2_resource_cond } : {}
      ),
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

# ELB / Load Balancer Controller permissions — always attached alongside capa_controller_base.
# Split into a separate policy so that base + ELB each stay under the 6144-char AWS limit.
resource "aws_iam_policy" "capa_controller_elb" {
  name = "ditto-capa-controller-elb-policy"
  tags = local.tags
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
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
      # ELB v2 LB + TG creates — no tag condition: CAPA creates the control plane NLB
      # with sigs.k8s.io/* tags (not elbv2.k8s.aws/cluster), so the LBC-specific
      # tag condition would block it. Mutations and deletes are still tag-scoped below.
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:CreateLoadBalancer",
          "elasticloadbalancing:CreateTargetGroup",
        ]
        Resource = ["*"]
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
      # ELB v2 LB + TG mutations — scoped by ARN type only; CAPA-created resources
      # carry sigs.k8s.io/* tags (not elbv2.k8s.aws/cluster), so a resource tag
      # condition would deny CAPA's own NLB/TG mutations.
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
      # ELB target registration — scoped by TG ARN only; freshly created TGs don't
      # carry elbv2.k8s.aws/cluster yet so a resource tag condition would deny CAPA.
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DeregisterTargets",
          "elasticloadbalancing:RegisterTargets",
        ]
        Resource = ["arn:aws:elasticloadbalancing:*:*:targetgroup/*/*"]
      },
      # ELB tag operations on LBs/TGs at creation time — CAPA tags NLBs with
      # sigs.k8s.io/* keys (not elbv2.k8s.aws/cluster), so the LBC-specific
      # request tag condition is dropped; CreateAction scope is still enforced.
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
        Condition = {
          StringEquals = {
            "elasticloadbalancing:CreateAction" = [
              "CreateLoadBalancer",
              "CreateTargetGroup",
            ]
          }
        }
      },
      # ELB tag mutations on existing LBs/TGs — scoped by ARN type only.
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
            "aws:RequestTag/ditto.live/managed_by" = "dittocloud"
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
            "ec2:ResourceTag/ditto.live/managed_by" = "dittocloud"
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
