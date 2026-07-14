resource "aws_iam_instance_profile" "capa_control_plane" {
  name = "control-plane.cluster-api-provider-aws.sigs.k8s.io"
  role = aws_iam_role.capa_control_plane.name
}

# Control plane instance policy — phase-1 allows broad EC2 mutations; phase-2
# (cluster_name set) scopes destructive operations to cluster-owned resources via
# kubernetes.io/cluster/<name> tag conditions.
resource "aws_iam_policy" "capa_control_plane" {
  description = "Cluster API Control Plane instances"
  name        = "control-plane.cluster-api-provider-aws.sigs.k8s.io"
  tags        = local.tags
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # Read-only describes — Describe* does not support resource-level permissions
      {
        Effect = "Allow"
        Action = [
          "autoscaling:DescribeAutoScalingGroups",
          "autoscaling:DescribeLaunchConfigurations",
          "autoscaling:DescribeTags",
          "ec2:DescribeImages",
          "ec2:DescribeInstances",
          "ec2:DescribeRegions",
          "ec2:DescribeRouteTables",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSubnets",
          "ec2:DescribeVolumes",
          "ec2:DescribeVolumesModifications",
          "ec2:DescribeVpcs",
          "kms:DescribeKey",
        ]
        Resource = ["*"]
      },
      # Route, tag, and IPv6 mutations — cannot add resource conditions;
      # route tables may be customer-owned in BYO-VPC, and CreateTags must
      # reach resources of any type
      {
        Effect = "Allow"
        Action = [
          "ec2:AssignIpv6Addresses",
          "ec2:CreateRoute",
          "ec2:CreateTags",
          "ec2:DeleteRoute",
        ]
        Resource = ["*"]
      },
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
      # EBS volumes support request tags but have no VPC context.
      merge(
        {
          Effect   = "Allow"
          Action   = ["ec2:CreateVolume"]
          Resource = ["arn:aws:ec2:*:*:volume/*"]
        },
        local.ec2_create_cond != null ? { Condition = local.ec2_create_cond } : {}
      ),
      # Security group mutations — cluster scoped in phase-2 and VPC scoped
      # when vpc_id is set.
      merge(
        {
          Effect = "Allow"
          Action = [
            "ec2:AuthorizeSecurityGroupIngress",
            "ec2:DeleteSecurityGroup",
            "ec2:RevokeSecurityGroupIngress",
          ]
          Resource = ["arn:aws:ec2:*:*:security-group/*"]
        },
        local.ec2_vpc_resource_cond != null ? { Condition = local.ec2_vpc_resource_cond } : {}
      ),
      # Volume mutations — phase-2: scoped to cluster-managed volumes.
      # AttachVolume/DetachVolume are intentionally omitted: AWS requires both the
      # volume and the instance resource to be authorized, and AmazonEBSCSIDriverPolicy
      # (also attached to this role) covers them on Resource = ["*"].
      merge(
        {
          Effect = "Allow"
          Action = [
            "ec2:DeleteVolume",
            "ec2:ModifyVolume",
          ]
          Resource = ["arn:aws:ec2:*:*:volume/*"]
        },
        local.ec2_resource_cond != null ? { Condition = local.ec2_resource_cond } : {}
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
      # ELB reads — Describe* does not support resource-level permissions
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DescribeListeners",
          "elasticloadbalancing:DescribeLoadBalancerAttributes",
          "elasticloadbalancing:DescribeLoadBalancers",
          "elasticloadbalancing:DescribeTargetGroups",
          "elasticloadbalancing:DescribeTargetHealth",
        ]
        Resource = ["*"]
      },
      # ELBv2 LB + TG creates — require cluster tag at request time
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:CreateLoadBalancer",
          "elasticloadbalancing:CreateTargetGroup",
        ]
        Resource  = ["*"]
        Condition = local.elb_create_cond
      },
      # ELBv2 listener creates — listeners don't carry the cluster tag so
      # tag conditions cannot be applied; scoped by association with tagged LBs above
      {
        Effect   = "Allow"
        Action   = ["elasticloadbalancing:CreateListener"]
        Resource = ["*"]
      },
      # ELBv2 LB + TG mutations — scoped by ARN type + cluster tag on resource
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:DeleteTargetGroup",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:ModifyTargetGroup",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
          "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
        ]
        Condition = local.elb_resource_cond
      },
      # ELBv2 listener mutations — scoped by ARN type only; listeners do not
      # carry the elbv2.k8s.aws/cluster tag so resource tag conditions cannot be applied
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DeleteListener",
          "elasticloadbalancing:ModifyListener",
        ]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:listener/app/*/*/*",
          "arn:aws:elasticloadbalancing:*:*:listener/net/*/*/*",
        ]
      },
      # ELBv2 target registration — scoped by TG ARN + cluster tag on resource
      {
        Effect = "Allow"
        Action = [
          "elasticloadbalancing:DeregisterTargets",
          "elasticloadbalancing:RegisterTargets",
        ]
        Resource  = ["arn:aws:elasticloadbalancing:*:*:targetgroup/*/*"]
        Condition = local.elb_resource_cond
      },
      # ELBv2 tag operations on LBs/TGs at creation time
      {
        Effect = "Allow"
        Action = ["elasticloadbalancing:AddTags"]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
          "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
        ]
        Condition = local.elb_add_tags_create_cond
      },
      # ELBv2 tag mutations on existing LBs/TGs — require cluster tag on resource
      {
        Effect = "Allow"
        Action = ["elasticloadbalancing:AddTags"]
        Resource = [
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/app/*/*",
          "arn:aws:elasticloadbalancing:*:*:loadbalancer/net/*/*",
          "arn:aws:elasticloadbalancing:*:*:targetgroup/*/*",
        ]
        Condition = local.elb_tag_mutation_cond
      },
      # IAM service-linked role for ELB
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
    ]
  })
}

# Configure the AWS EBS CSI Permissions to enable backups and updates to snapshots
data "aws_iam_policy" "aws_ebs_csi_policy" {
  arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

resource "aws_iam_role" "capa_control_plane" {
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
  name = "control-plane.cluster-api-provider-aws.sigs.k8s.io"
}

resource "aws_iam_role_policy_attachment" "capa_control_plane" {
  role       = aws_iam_role.capa_control_plane.name
  policy_arn = aws_iam_policy.capa_control_plane.arn
}

// ControlPlane nodes also need the nodes policy.
resource "aws_iam_role_policy_attachment" "capa_control_plane_nodes_policy" {
  role       = aws_iam_role.capa_control_plane.name
  policy_arn = aws_iam_policy.capa_nodes.arn
}

// ControlPlane nodes also need the controllers policy.
resource "aws_iam_role_policy_attachment" "capa_control_plane_controllers_policy" {
  role       = aws_iam_role.capa_control_plane.name
  policy_arn = aws_iam_policy.capa_controller_base.arn
}

// ControlPlane AWS EBS Controller needs the ability to take snapshots
resource "aws_iam_role_policy_attachment" "aws_ebs_csi_policy" {
  role       = aws_iam_role.capa_control_plane.name
  policy_arn = data.aws_iam_policy.aws_ebs_csi_policy.arn
}
