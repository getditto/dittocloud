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

  variables {
    cluster_name = "test-cluster"
    vpc_id       = "vpc-09e877f9012f52241"
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
    condition     = length(aws_iam_policy.capa_controller_base.policy) <= 6144
    error_message = "The CAPA controller base managed policy exceeds AWS's 6,144-character limit."
  }

  assert {
    condition     = length(aws_iam_policy.capa_control_plane.policy) <= 6144
    error_message = "The CAPA control-plane managed policy exceeds AWS's 6,144-character limit."
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
    condition     = length(jsonencode(jsondecode(aws_iam_policy.cluster_resources_boundary_policy.policy))) <= 6144
    error_message = "The cluster resources boundary managed policy exceeds AWS's 6,144-character limit."
  }
}
