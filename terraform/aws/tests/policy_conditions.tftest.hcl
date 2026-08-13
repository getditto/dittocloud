mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "520778242457"
    }
  }

  mock_data "aws_region" {
    defaults = {
      region = "us-west-2"
    }
  }

  mock_data "aws_iam_policy_document" {
    defaults = {
      json          = jsonencode({ Version = "2012-10-17", Statement = [] })
      minified_json = jsonencode({ Version = "2012-10-17", Statement = [] })
    }
  }
}

run "vpc_and_cluster_conditions_match_supported_resources" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    cluster_name = "test-cluster"
    vpc_id       = "vpc-09e877f9012f52241"
    vpc_subnet_ids = [
      "subnet-00000000000000001",
      "subnet-00000000000000002",
      "subnet-00000000000000003",
      "subnet-00000000000000004",
      "subnet-00000000000000005",
      "subnet-00000000000000006",
    ]
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateRoute") &&
      try(statement.Resource, null) == ["arn:aws:ec2:*:*:route-table/*"] &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "CAPA route mutations must be scoped to route-table ARNs in the selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:ModifyNetworkInterfaceAttribute") &&
      try(statement.Resource, null) == ["arn:aws:ec2:*:*:network-interface/*"] &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "CAPA network-interface mutations must be scoped to network-interface ARNs in the selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_control_plane.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateRoute") &&
      try(statement.Resource, null) == ["arn:aws:ec2:*:*:route-table/*"] &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "Control-plane route mutations must be scoped to route-table ARNs in the selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_nodes.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:AssignIpv6Addresses") &&
      try(statement.Resource, null) == ["arn:aws:ec2:*:*:network-interface/*"] &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "Node IPv6 assignment must be confined to network interfaces in the selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateTags") &&
      contains(try(statement.Resource, []), "arn:aws:ec2:*:*:subnet/*") &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "CAPA tagging of VPC-aware EC2 resources must be confined to the selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_elb.policy).Statement : statement
      if contains(try(statement.Action, []), "elasticloadbalancing:CreateLoadBalancer") &&
      toset(try(statement.Condition["ForAllValues:StringEquals"]["elasticloadbalancing:Subnet"], [])) == toset([
        "subnet-00000000000000001",
        "subnet-00000000000000002",
        "subnet-00000000000000003",
        "subnet-00000000000000004",
        "subnet-00000000000000005",
        "subnet-00000000000000006",
      ]) &&
      try(statement.Condition.Null["elasticloadbalancing:Subnet"], null) == "false"
    ]) == 1
    error_message = "CAPA load-balancer creation must require every selected subnet to belong to the configured VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_elb.policy).Statement : statement
      if contains(try(statement.Action, []), "elasticloadbalancing:SetSubnets") &&
      toset(try(statement.Condition["ForAllValues:StringEquals"]["elasticloadbalancing:Subnet"], [])) == toset([
        "subnet-00000000000000001",
        "subnet-00000000000000002",
        "subnet-00000000000000003",
        "subnet-00000000000000004",
        "subnet-00000000000000005",
        "subnet-00000000000000006",
      ])
    ]) == 1
    error_message = "CAPA SetSubnets must keep every load-balancer subnet inside the configured VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "elasticloadbalancing:CreateLoadBalancer") &&
      toset(try(statement.Condition["ForAllValues:StringEquals"]["elasticloadbalancing:Subnet"], [])) == toset([
        "subnet-00000000000000001",
        "subnet-00000000000000002",
        "subnet-00000000000000003",
        "subnet-00000000000000004",
        "subnet-00000000000000005",
        "subnet-00000000000000006",
      ])
    ]) == 1
    error_message = "The cluster boundary must confine load-balancer creation to subnets in the configured VPC."
  }

  assert {
    condition = alltrue([
      for action in [
        "elasticloadbalancing:CreateTargetGroup",
        "elasticloadbalancing:CreateListener",
        "elasticloadbalancing:CreateRule",
        "elasticloadbalancing:RegisterTargets",
        "elasticloadbalancing:SetSecurityGroups",
        "elasticloadbalancing:SetSubnets",
        ] : contains(flatten([
          for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : try(statement.Action, [])
      ]), action)
    ])
    error_message = "VPC confinement must preserve the AWS Load Balancer Controller operations needed to reconcile Kubernetes Services and Ingresses."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.iam_trust_editor_policy.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "iam:CreateRole") &&
      try(statement.Resource, null) == "arn:aws:iam::520778242457:role/dittocluster/*" &&
      toset(try(statement.Condition.StringEquals["iam:PermissionsBoundary"], [])) == toset([
        "arn:aws:iam::520778242457:policy/ditto-cluster-resources-boundary-policy",
        "arn:aws:iam::520778242457:policy/ditto-cluster-external-resources-boundary-policy",
      ])
    ]) == 1
    error_message = "The trust editor must require an approved Ditto permissions boundary when creating a role."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.iam_trust_editor_policy.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "iam:PutRolePermissionsBoundary")
    ]) == 0
    error_message = "The trust editor must not allow replacing a role permissions boundary after creation."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.iam_trust_editor_policy.policy).Statement : statement
      if statement.Effect == "Deny" &&
      try(statement.Resource, null) == "arn:aws:iam::520778242457:role/dittocluster/*" &&
      toset(try(statement.Action, [])) == toset([
        "iam:DeleteRolePermissionsBoundary",
        "iam:PutRolePermissionsBoundary",
      ])
    ]) == 1
    error_message = "The trust editor must explicitly deny permissions-boundary replacement and removal for managed roles."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      try(statement.Resource, null) == "arn:aws:iam::*:role/*.cluster-api-provider-aws.sigs.k8s.io"
    ]) == 1
    error_message = "The legacy cluster boundary must retain its existing aws-partition PassRole ARN during default-scope migration."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      try(statement.Resource[0], null) == "arn:*:iam::*:role/*.cluster-api-provider-aws.sigs.k8s.io"
    ]) == 1
    error_message = "The legacy controller policy must retain its existing partition-wildcard PassRole ARN during default-scope migration."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if try(statement.Condition.StringEquals["ec2:Vpc"], null) != null && length(setintersection(
        toset(try(statement.Action, [])),
        toset([
          "ec2:CreateLaunchTemplate",
          "ec2:CreateLaunchTemplateVersion",
          "ec2:ModifyInstanceAttribute",
          "ec2:TerminateInstances",
        ])
      )) > 0
    ]) == 0
    error_message = "ec2:Vpc must not be attached to EC2 action/resource combinations that do not expose it."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_control_plane.policy).Statement : statement
      if try(statement.Condition.StringEquals["ec2:Vpc"], null) != null && length(setintersection(
        toset(try(statement.Action, [])),
        toset([
          "ec2:CreateVolume",
          "ec2:DeleteVolume",
          "ec2:ModifyInstanceAttribute",
          "ec2:ModifyVolume",
        ])
      )) > 0
    ]) == 0
    error_message = "The control-plane policy must not apply ec2:Vpc to volumes or instances."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateLaunchTemplateVersion") &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/kubernetes.io/cluster/test-cluster"], null) == "owned"
    ]) == 1
    error_message = "CreateLaunchTemplateVersion must be scoped with the existing launch template's resource tag."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateSecurityGroup") &&
      contains(try(statement.Resource, []), "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241")
    ]) == 1
    error_message = "CreateSecurityGroup must separately authorize the selected VPC resource."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_control_plane.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateSecurityGroup") &&
      contains(try(statement.Resource, []), "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241")
    ]) == 1
    error_message = "The control-plane CreateSecurityGroup permission must separately authorize the selected VPC resource."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:RunInstances") &&
      contains(try(statement.Resource, []), "arn:aws:ec2:*:*:instance/*") &&
      try(statement.Condition.StringEquals["aws:RequestTag/kubernetes.io/cluster/test-cluster"], null) == "owned"
    ]) == 1
    error_message = "RunInstances must require the cluster ownership request tag on the new instance."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:RunInstances") &&
      contains(try(statement.Resource, []), "arn:aws:ec2:*:*:subnet/*") &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "RunInstances must apply ec2:Vpc to its VPC-aware resource contexts."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      try(statement.Resource, null) == ["*"] &&
      contains(try(statement.Condition.StringLike["ec2:CreateAction"], []), "RunInstances")
    ]) == 1
    error_message = "CAPA must authorize initial EC2 tags only through the creation-time ec2:CreateAction path."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      try(statement.Condition.StringLike["ec2:CreateAction"], null) == null &&
      (
        try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) != "false" ||
        try(statement.Condition.StringEquals["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/cluster/test-cluster"], null) != "owned"
      )
    ]) == 0
    error_message = "Every CAPA direct CreateTags path must require the immutable role marker and the phase-2 cluster owner tag."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if statement.Effect == "Deny" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      contains(try(statement.Condition["ForAnyValue:StringLike"]["aws:TagKeys"], []), "sigs.k8s.io/cluster-api-provider-aws/role") &&
      contains(try(statement.Condition["ForAnyValue:StringLike"]["aws:TagKeys"], []), "kubernetes.io/cluster/*") &&
      try(statement.Condition.Null["ec2:CreateAction"], null) == "true" &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) == "true"
    ]) == 1
    error_message = "CAPA must deny direct ownership-tag assignment when the existing resource lacks its bootstrap role tag."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_control_plane_tags.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      try(statement.Condition.StringLike["ec2:CreateAction"], null) == null &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) != "false"
    ]) == 0
    error_message = "Control-plane direct CreateTags paths must require CAPA's existing bootstrap role tag."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_nodes.policy).Statement : statement
      if statement.Effect == "Deny" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      contains(try(statement.Condition["ForAnyValue:StringLike"]["aws:TagKeys"], []), "ditto.live/managed_by") &&
      contains(try(statement.Condition["ForAnyValue:StringLike"]["aws:TagKeys"], []), "ditto:project") &&
      try(statement.Condition.Null["ec2:CreateAction"], null) == "true" &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) == "true"
    ]) == 1
    error_message = "The shared node policy must override AWS managed tag grants that could self-assign ownership to existing resources."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      try(statement.Resource, null) == "*" &&
      contains(try(statement.Condition.StringLike["ec2:CreateAction"], []), "RunInstances")
    ]) == 1
    error_message = "The cluster boundary must allow initial EC2 tags only during resource creation."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      try(statement.Condition.StringLike["ec2:CreateAction"], null) == null &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/ditto:project"], null) != "ditto"
    ]) == 0
    error_message = "The cluster boundary must not permit direct tagging until the existing resource has the Ditto project marker."
  }

  assert {
    condition     = length(aws_iam_policy.capa_controller_base.policy) <= 6144
    error_message = "The CAPA controller base managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition     = length(aws_iam_policy.capa_controller_network.policy) <= 6144
    error_message = "The CAPA controller network managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition     = length(aws_iam_policy.capa_controller_elb.policy) <= 6144
    error_message = "The CAPA controller ELB managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition     = length(aws_iam_policy.capa_nodes.policy) <= 6144
    error_message = "The CAPA node managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition     = length(aws_iam_policy.capa_control_plane.policy) <= 6144
    error_message = "The CAPA control-plane managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition     = length(aws_iam_policy.capa_control_plane_tags.policy) <= 6144
    error_message = "The CAPA control-plane tag managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:CreateLaunchTemplate") &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) != null
    ]) == 0
    error_message = "The cluster boundary must not apply ec2:Vpc to CreateLaunchTemplate."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "ec2:RunInstances") &&
      try(statement.Condition.StringEqualsIfExists["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "The cluster boundary must restrict VPC-aware RunInstances contexts without denying its other resource contexts."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if length(setintersection(
        toset(try(statement.Action, [])),
        toset([
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:RevokeSecurityGroupIngress",
          "ec2:DeleteSecurityGroup",
        ])
      )) > 0 &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/ditto:project"], null) == null &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == null
    ]) == 0
    error_message = "Every boundary statement that mutates security groups must be restricted by the Ditto project tag or selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if try(statement.Resource, null) == "arn:aws:ec2:*:*:security-group/*" &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/ditto:project"], null) == "ditto" &&
      toset(try(statement.Action, [])) == toset([
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:RevokeSecurityGroupIngress",
        "ec2:DeleteSecurityGroup",
      ])
    ]) == 1
    error_message = "The project-tagged security-group mutation path must cover ingress changes and deletion."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if try(statement.Resource, null) == "arn:aws:ec2:*:*:security-group/*" &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241"
    ]) == 1
    error_message = "The untagged load-balancer-controller security-group path must be confined to the selected VPC."
  }

  assert {
    condition     = length(jsonencode(jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy))) <= 6144
    error_message = "The cluster resources boundary managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition = (
      length(aws_iam_policy.capa_controller_eks_policy) == 0 &&
      length(aws_iam_role.capa_eks_control_plane) == 0 &&
      !contains(keys(local.capa_controller_policies), "capa-controller-eks")
    )
    error_message = "EKS IAM resources and controller attachment must remain absent when enable_eks is false."
  }
}

run "security_group_mutations_without_vpc_require_project_tag" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if length(setintersection(
        toset(try(statement.Action, [])),
        toset([
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:RevokeSecurityGroupIngress",
          "ec2:DeleteSecurityGroup",
        ])
      )) > 0 &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/ditto:project"], null) != "ditto"
    ]) == 0
    error_message = "Without a selected VPC, every security-group mutation must require the Ditto project tag."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if toset(try(statement.Action, [])) == toset([
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:RevokeSecurityGroupIngress",
        "ec2:DeleteSecurityGroup",
      ]) &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) != null
    ]) == 0
    error_message = "The VPC-scoped security-group exception must not exist when vpc_id is unset."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "elasticloadbalancing:CreateLoadBalancer") &&
      try(statement.Condition["ForAllValues:StringEquals"]["elasticloadbalancing:Subnet"], null) != null
    ]) == 0
    error_message = "The subnet condition must be omitted in Cluster API-managed VPC mode because no VPC or subnet IDs exist yet."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_network.policy).Statement : statement
      if statement.Effect == "Allow" &&
      contains(try(statement.Action, []), "ec2:CreateTags") &&
      try(statement.Condition.StringLike["ec2:CreateAction"], null) == null &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) != "false"
    ]) == 0
    error_message = "Phase 1 direct CAPA tagging must require the existing bootstrap role tag even before the cluster name is known."
  }
}

run "eks_permissions_are_attached_when_enabled" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    enable_eks = true
  }

  assert {
    condition = (
      length(aws_iam_policy.capa_controller_eks_policy) == 1 &&
      contains(keys(local.capa_controller_policies), "capa-controller-eks")
    )
    error_message = "enable_eks must create and attach the CAPA controller EKS policy."
  }

  assert {
    condition = (
      length(aws_iam_role.capa_eks_control_plane) == 1 &&
      length(aws_iam_role_policy_attachment.capa_eks_control_plane) == 1
    )
    error_message = "enable_eks must create the EKS control-plane service role and attach AmazonEKSClusterPolicy."
  }
}

run "legacy_names_and_policy_paths_remain_unchanged" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    enable_eks = true
  }

  assert {
    condition = (
      module.capa_controller_role.name == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      module.iam_trust_editor_role.name == "iam-trust-editor.ditto.live" &&
      module.iam_admin_view_role.name == "iam-admin-view.ditto.live" &&
      aws_iam_role.capa_nodes.name == "nodes.cluster-api-provider-aws.sigs.k8s.io" &&
      aws_iam_role.capa_control_plane.name == "control-plane.cluster-api-provider-aws.sigs.k8s.io" &&
      aws_iam_role.capa_eks_control_plane[0].name == "eks-controlplane.cluster-api-provider-aws.sigs.k8s.io"
    )
    error_message = "A null scope_ref must preserve every legacy IAM role name."
  }

  assert {
    condition = (
      aws_iam_policy.iam_trust_editor_policy.name == "ditto-iam-trust-editor-policy" &&
      aws_iam_policy.cluster_resources_boundary_policy.name == "ditto-cluster-resources-boundary-policy" &&
      aws_iam_policy.cluster_external_resources_boundary_policy.name == "ditto-cluster-external-resources-boundary-policy" &&
      aws_iam_policy.capa_controller_base.name == "ditto-capa-controller-policy" &&
      aws_iam_policy.capa_controller_network.name == "ditto-capa-controller-network-policy" &&
      aws_iam_policy.capa_controller_elb.name == "ditto-capa-controller-elb-policy" &&
      aws_iam_policy.capa_controller_vpc_lifecycle[0].name == "ditto-capa-controller-vpc-lifecycle-policy" &&
      aws_iam_policy.capa_controller_eks_policy[0].name == "ditto-capa-controller-eks-policy"
    )
    error_message = "A null scope_ref must preserve every legacy IAM policy and boundary name."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.iam_trust_editor_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:CreateRole") &&
      try(statement.Resource, null) == "arn:aws:iam::520778242457:role/dittocluster/*" &&
      toset(try(statement.Condition.StringEquals["iam:PermissionsBoundary"], [])) == toset([
        "arn:aws:iam::520778242457:policy/ditto-cluster-resources-boundary-policy",
        "arn:aws:iam::520778242457:policy/ditto-cluster-external-resources-boundary-policy",
      ])
    ]) == 1
    error_message = "A null scope_ref must preserve the legacy trust-editor path and boundary ARNs."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "sqs:ReceiveMessage") &&
      try(statement.Resource, null) == "arn:aws:sqs:*:*:karpenter-*"
    ]) == 1
    error_message = "A null scope_ref must preserve the legacy Karpenter queue wildcard."
  }
}

run "rejects_more_than_six_existing_vpc_subnets" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    customer_managed_vpc = true
    vpc_id               = "vpc-09e877f9012f52241"
    vpc_subnet_ids = [
      "subnet-00000000000000001",
      "subnet-00000000000000002",
      "subnet-00000000000000003",
      "subnet-00000000000000004",
      "subnet-00000000000000005",
      "subnet-00000000000000006",
      "subnet-00000000000000007",
      "subnet-00000000000000008",
      "subnet-00000000000000009",
      "subnet-00000000000000010",
    ]
  }

  expect_failures = [var.vpc_subnet_ids]
}

run "scoped_module_requires_an_explicit_region" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    scope_ref              = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    create_admin_view_role = false
  }

  expect_failures = [terraform_data.scope_contract[0]]
}

run "scoped_module_cannot_duplicate_the_shared_admin_role" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    scope_ref = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    region    = "ap-southeast-2"
  }

  expect_failures = [terraform_data.scope_contract[0]]
}

run "default_scope_identity_tags_preserve_legacy_names" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    scope_identity_ref = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    tags = {
      Owner                  = "platform"
      "ditto.live/scope-ref" = "cannot-override"
    }
  }

  assert {
    condition = (
      module.capa_controller_role.name == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      module.iam_trust_editor_role.name == "iam-trust-editor.ditto.live" &&
      aws_iam_role.capa_nodes.name == "nodes.cluster-api-provider-aws.sigs.k8s.io" &&
      aws_iam_role.capa_control_plane.name == "control-plane.cluster-api-provider-aws.sigs.k8s.io"
    )
    error_message = "A default scope identity must not suffix any legacy IAM names."
  }

  assert {
    condition = alltrue([
      local.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      local.tags["Owner"] == "platform",
      aws_iam_role.capa_nodes.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      aws_iam_instance_profile.capa_nodes.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      aws_iam_role.capa_control_plane.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      aws_iam_instance_profile.capa_control_plane.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      aws_iam_policy.capa_nodes.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
    ])
    error_message = "Default-scope IAM resources must receive the exact reserved identity tag as an in-place tag-only migration."
  }
}

run "scoped_names_paths_and_policy_arns_are_exact" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    scope_ref              = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    region                 = "ap-southeast-2"
    create_admin_view_role = false
    enable_eks             = true
    vpc_id                 = "vpc-09e877f9012f52241"
    vpc_subnet_ids = [
      "subnet-00000000000000001",
      "subnet-00000000000000002",
    ]
    tags = {
      Owner                  = "platform"
      "ditto.live/scope-ref" = "cannot-override"
    }
  }

  assert {
    condition = (
      module.capa_controller_role.name == "ditto-capa-controller-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      module.iam_trust_editor_role.name == "ditto-iam-trust-editor-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_role.capa_nodes.name == "ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_role.capa_control_plane.name == "ditto-capa-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_role.capa_eks_control_plane[0].name == "ditto-capa-eks-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    )
    error_message = "Scoped IAM roles must use the exact accepted scopeRef-suffixed names."
  }

  assert {
    condition = (
      aws_iam_policy.iam_trust_editor_policy.name == "ditto-iam-trust-editor-policy-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_policy.cluster_resources_boundary_policy.name == "ditto-cluster-resources-boundary-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_policy.cluster_external_resources_boundary_policy.name == "ditto-cluster-external-boundary-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_policy.capa_controller_base.name == "ditto-capa-controller-base-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_policy.capa_controller_vpc_lifecycle[0].name == "ditto-capa-controller-vpc-lifecycle-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    )
    error_message = "Scoped IAM policies and boundaries must use the exact accepted scopeRef-suffixed names."
  }

  assert {
    condition     = module.iam_admin_view_role.name == null
    error_message = "A scoped IAM module must not duplicate the shared account-wide admin view role."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.iam_trust_editor_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:CreateRole") &&
      try(statement.Resource, null) == "arn:aws:iam::520778242457:role/dittocluster/dsc-01k2m8g7n4p6q9r3t5v8x1y2z3/*" &&
      toset(try(statement.Condition.StringEquals["iam:PermissionsBoundary"], [])) == toset([
        "arn:aws:iam::520778242457:policy/ditto-cluster-resources-boundary-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:policy/ditto-cluster-external-boundary-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      ])
    ]) == 1
    error_message = "The scoped trust editor must manage only its scope path and require only its exact boundaries."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      toset(try(statement.Resource, [])) == toset([
        "arn:aws:iam::520778242457:role/ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-eks-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      ])
    ]) == 1
    error_message = "The scoped CAPA controller must pass only its own exact roles."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      toset(try(statement.Resource, [])) == toset([
        "arn:aws:iam::520778242457:role/ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-eks-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      ])
    ]) == 1
    error_message = "The scoped cluster boundary must pass only its scope's exact CAPA roles."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_eks_policy[0].policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      toset(try(statement.Resource, [])) == toset([
        "arn:aws:iam::520778242457:role/ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-eks-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/dittocluster/dsc-01k2m8g7n4p6q9r3t5v8x1y2z3/ebs-csi-driver-*",
      ])
    ]) == 1
    error_message = "The scoped EKS controller policy must pass its scope's CAPA roles plus the EBS CSI addon role."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "sqs:ReceiveMessage") &&
      try(statement.Resource, null) == "arn:aws:sqs:ap-southeast-2:520778242457:karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    ]) == 1
    error_message = "The scoped boundary must consume only its exact Karpenter interruption queue."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_external_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "secretsmanager:CreateSecret") &&
      try(statement.Resource, null) == ["arn:aws:secretsmanager:*:*:secret:dittocluster/dsc-01k2m8g7n4p6q9r3t5v8x1y2z3/*"]
    ]) == 1
    error_message = "The scoped external boundary must be limited to its own secrets path."
  }

  assert {
    condition = (
      aws_iam_policy.capa_nodes.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      aws_iam_policy.capa_nodes.tags["Owner"] == "platform" &&
      local.ec2_vpc_arn == "arn:aws:ec2:ap-southeast-2:520778242457:vpc/vpc-09e877f9012f52241" &&
      length(terraform_data.scoped_name_validation) == length(local.scoped_generated_names)
    )
    error_message = "Scoped IAM resources must carry the reserved identity tag and validate every generated name."
  }
}

run "scoped_kubeadm_pass_role_excludes_nonexistent_eks_role" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    scope_ref              = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    region                 = "ap-southeast-2"
    create_admin_view_role = false
    enable_eks             = false
    vpc_id                 = "vpc-09e877f9012f52241"
    vpc_subnet_ids = [
      "subnet-00000000000000001",
      "subnet-00000000000000002",
    ]
  }

  assert {
    condition     = length(aws_iam_role.capa_eks_control_plane) == 0
    error_message = "A kubeadm scope must not create the EKS control-plane role."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      toset(try(statement.Resource, [])) == toset([
        "arn:aws:iam::520778242457:role/ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      ])
    ]) == 1
    error_message = "A kubeadm scope's CAPA controller must not reference the nonexistent EKS control-plane role."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy).Statement : statement
      if contains(try(statement.Action, []), "iam:PassRole") &&
      toset(try(statement.Resource, [])) == toset([
        "arn:aws:iam::520778242457:role/ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
        "arn:aws:iam::520778242457:role/ditto-capa-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
      ])
    ]) == 1
    error_message = "A kubeadm scope's cluster boundary must not carry the nonexistent EKS control-plane role, keeping the generated policy well clear of IAM's 6,144-character limit."
  }
}

run "phase_two_security_group_mutations_use_capa_ownership_tag" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  variables {
    cluster_name = "test-cluster"
    vpc_id       = "vpc-09e877f9012f52241"
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if toset(try(statement.Action, [])) == toset([
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:RevokeSecurityGroupIngress",
      ]) &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/cluster/test-cluster"], null) == "owned" &&
      try(statement.Condition.StringEquals["ec2:Vpc"], null) == "arn:aws:ec2:us-west-2:520778242457:vpc/vpc-09e877f9012f52241" &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) == "false"
    ]) == 1
    error_message = "Controller security group rule mutations must require CAPA's ownership tag, its bootstrap role marker, and the selected VPC."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if try(statement.Action, []) == ["ec2:DeleteSecurityGroup"] &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/cluster/test-cluster"], null) == "owned" &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) == "false"
    ]) == 1
    error_message = "Controller security group deletion must require CAPA's ownership tag and bootstrap role marker."
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_control_plane.policy).Statement : statement
      if toset(try(statement.Action, [])) == toset([
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:DeleteSecurityGroup",
        "ec2:RevokeSecurityGroupIngress",
      ]) &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/cluster/test-cluster"], null) == "owned" &&
      try(statement.Condition.Null["ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/role"], null) == "false"
    ]) == 1
    error_message = "Control-plane security group mutations must require CAPA's ownership tag and bootstrap role marker."
  }

  assert {
    condition = length([
      for statement in concat(
        jsondecode(aws_iam_policy.capa_controller_base.policy).Statement,
        jsondecode(aws_iam_policy.capa_control_plane.policy).Statement,
      ) : statement
      if contains(try(statement.Resource, []), "arn:aws:ec2:*:*:security-group/*") &&
      length(setintersection(
        toset(try(statement.Action, [])),
        toset([
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:RevokeSecurityGroupIngress",
          "ec2:DeleteSecurityGroup",
        ])
      )) > 0 &&
      try(statement.Condition.StringEquals["ec2:ResourceTag/kubernetes.io/cluster/test-cluster"], null) != null
    ]) == 0
    error_message = "No security-group mutation may be gated on kubernetes.io/cluster/<name>; CAPA does not write that tag to its own groups."
  }

  assert {
    condition = (
      length(aws_iam_policy.capa_controller_base.policy) <= 6144 &&
      length(aws_iam_policy.capa_control_plane.policy) <= 6144
    )
    error_message = "Security group condition changes must keep both policies under AWS's 6,144-character limit."
  }
}

run "phase_one_security_group_mutations_stay_unconditional" {
  command = plan

  module {
    source = "./cross_account_iam"
  }

  assert {
    condition = length([
      for statement in jsondecode(aws_iam_policy.capa_controller_base.policy).Statement : statement
      if toset(try(statement.Action, [])) == toset([
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:RevokeSecurityGroupIngress",
      ]) &&
      try(statement.Condition, null) == null
    ]) == 1
    error_message = "Without a cluster name or VPC, security group rule mutations must remain unconditional."
  }
}
