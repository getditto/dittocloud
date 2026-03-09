resource "aws_iam_instance_profile" "capa_control_plane" {
  name = "control-plane.cluster-api-provider-aws.sigs.k8s.io"
  role = aws_iam_role.capa_control_plane.name
}

resource "aws_iam_policy" "capa_control_plane" {
  description = "Cluster API Control Plane instances"
  name        = "control-plane.cluster-api-provider-aws.sigs.k8s.io"
  policy = jsonencode({
    Statement = [
      {
        Action = [
          "autoscaling:DescribeAutoScalingGroups",
          "autoscaling:DescribeLaunchConfigurations",
          "autoscaling:DescribeTags",
          "ec2:AssignIpv6Addresses",
          "ec2:DescribeInstances",
          "ec2:DescribeImages",
          "ec2:DescribeVolumesModifications",
          "ec2:DescribeRegions",
          "ec2:DescribeRouteTables",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSubnets",
          "ec2:DescribeVolumes",
          "ec2:CreateSecurityGroup",
          "ec2:CreateTags",
          "ec2:CreateVolume",
          "ec2:ModifyInstanceAttribute",
          "ec2:ModifyVolume",
          "ec2:AttachVolume",
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:CreateRoute",
          "ec2:DeleteRoute",
          "ec2:DeleteSecurityGroup",
          "ec2:DeleteVolume",
          "ec2:DetachVolume",
          "ec2:RevokeSecurityGroupIngress",
          "ec2:DescribeVpcs",
          "elasticloadbalancing:AddTags",
          "elasticloadbalancing:AttachLoadBalancerToSubnets",
          "elasticloadbalancing:ApplySecurityGroupsToLoadBalancer",
          "elasticloadbalancing:CreateLoadBalancer",
          "elasticloadbalancing:CreateLoadBalancerPolicy",
          "elasticloadbalancing:CreateLoadBalancerListeners",
          "elasticloadbalancing:ConfigureHealthCheck",
          "elasticloadbalancing:DeleteLoadBalancer",
          "elasticloadbalancing:DeleteLoadBalancerListeners",
          "elasticloadbalancing:DescribeLoadBalancers",
          "elasticloadbalancing:DescribeLoadBalancerAttributes",
          "elasticloadbalancing:DetachLoadBalancerFromSubnets",
          "elasticloadbalancing:DeregisterInstancesFromLoadBalancer",
          "elasticloadbalancing:ModifyLoadBalancerAttributes",
          "elasticloadbalancing:RegisterInstancesWithLoadBalancer",
          "elasticloadbalancing:SetLoadBalancerPoliciesForBackendServer",
          "elasticloadbalancing:CreateListener",
          "elasticloadbalancing:CreateTargetGroup",
          "elasticloadbalancing:DeleteListener",
          "elasticloadbalancing:DeleteTargetGroup",
          "elasticloadbalancing:DeregisterTargets",
          "elasticloadbalancing:DescribeListeners",
          "elasticloadbalancing:DescribeLoadBalancerPolicies",
          "elasticloadbalancing:DescribeTargetGroups",
          "elasticloadbalancing:DescribeTargetHealth",
          "elasticloadbalancing:ModifyListener",
          "elasticloadbalancing:ModifyTargetGroup",
          "elasticloadbalancing:RegisterTargets",
          "elasticloadbalancing:SetLoadBalancerPoliciesOfListener",
          "iam:CreateServiceLinkedRole",
          "kms:DescribeKey"
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

# Configure the AWS EBS CSI Permissions to enable backups and updates to snapshots
data "aws_iam_policy" "aws_ebs_csi_policy" {
  arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

resource "aws_iam_role" "capa_control_plane" {
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
  policy_arn = aws_iam_policy.capa_controller_policy.arn
}

// ControlPlane AWS EBS Controller needs the ability to take snapshots
resource "aws_iam_role_policy_attachment" "aws_ebs_csi_policy" {
  role       = aws_iam_role.capa_control_plane.name
  policy_arn = data.aws_iam_policy.aws_ebs_csi_policy.arn
}