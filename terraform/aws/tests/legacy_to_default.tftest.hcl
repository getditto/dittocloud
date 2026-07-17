mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "123456789012"
    }
  }

  mock_data "aws_region" {
    defaults = {
      region = "ap-southeast-2"
    }
  }

  mock_data "aws_partition" {
    defaults = {
      reverse_dns_prefix = "com.amazonaws"
    }
  }

  mock_data "aws_availability_zones" {
    defaults = {
      names = [
        "ap-southeast-2a",
        "ap-southeast-2b",
        "ap-southeast-2c",
      ]
    }
  }

  mock_data "aws_iam_policy_document" {
    defaults = {
      json          = jsonencode({ Version = "2012-10-17", Statement = [] })
      minified_json = jsonencode({ Version = "2012-10-17", Statement = [] })
    }
  }

  mock_data "aws_iam_policy" {
    defaults = {
      arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
    }
  }

  mock_resource "aws_iam_policy" {
    defaults = {
      arn = "arn:aws:iam::123456789012:policy/mock-policy"
    }
  }

  mock_resource "aws_iam_role" {
    defaults = {
      arn = "arn:aws:iam::123456789012:role/mock-role"
    }
  }

  mock_resource "aws_iam_instance_profile" {
    defaults = {
      arn = "arn:aws:iam::123456789012:instance-profile/mock-profile"
    }
  }

  mock_resource "aws_sqs_queue" {
    defaults = {
      arn = "arn:aws:sqs:ap-southeast-2:123456789012:karpenter-interruption"
    }
  }

  mock_resource "aws_vpc" {
    defaults = {
      id = "vpc-00000000000000001"
    }
  }

  mock_resource "aws_subnet" {
    defaults = {
      id = "subnet-00000000000000001"
    }
  }

  mock_resource "aws_route_table" {
    defaults = {
      id = "rtb-00000000000000001"
    }
  }

  mock_resource "aws_security_group" {
    defaults = {
      id = "sg-00000000000000001"
    }
  }
}

override_data {
  target = module.vpc[0].data.aws_availability_zones.available
  values = {
    names = [
      "ap-southeast-2a",
      "ap-southeast-2b",
      "ap-southeast-2c",
    ]
  }
}

variables {
  profile              = "test"
  region               = "ap-southeast-2"
  create_iam           = true
  create_vpc           = true
  enable_eks           = true
  customer_managed_vpc = false
  vpc_name             = "migration-vpc"
  vpc_cidr             = "10.220.0.0/16"
  cluster_name         = "migration-eks"
  deployment_scopes = {
    "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
      default      = true
      cluster_name = "migration-eks"
      cluster_type = "eks"
      region       = "ap-southeast-2"
      vpc = {
        mode = "dittocloud"
        name = "migration-vpc"
        cidr = "10.220.0.0/16"
      }
    }
  }
}

run "apply_legacy_configuration" {
  command = apply

  variables {
    deployment_scopes = {}
  }

  assert {
    condition = (
      length(terraform_data.scope_registry) == 0 &&
      length(terraform_data.scope_configuration) == 0 &&
      length(module.vpc) == 1 &&
      length(module.cross_account_iam) == 1 &&
      length(aws_sqs_queue.karpenter_interruption) == 1 &&
      length(aws_cloudwatch_event_rule.karpenter_interruption) == 4 &&
      length(aws_ec2_instance_metadata_defaults.imdsv2) == 1
    )
    error_message = "The golden legacy fixture must apply the current IAM, VPC, Karpenter, and IMDS resource set without a scope registry."
  }

  assert {
    condition = (
      module.cross_account_iam[0].scope_iam.controller_role.name == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      aws_sqs_queue.karpenter_interruption[0].name == "karpenter-interruption" &&
      toset(keys(output.aws)) == toset(["account_id", "region", "vpc"])
    )
    error_message = "The golden legacy fixture must retain the current default names and exact legacy output shape."
  }
}

run "plan_default_scope_registry_seed" {
  command = plan

  plan_options {
    target = [terraform_data.scope_registry["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"]]
  }

  assert {
    condition = (
      length(terraform_data.scope_registry) == 1 &&
      terraform_data.scope_registry["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.default
    )
    error_message = "The migration seed must create only the immutable default-scope registry sentinel."
  }
}

run "seed_default_scope_registry" {
  command = apply

  plan_options {
    target = [terraform_data.scope_registry["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"]]
  }

  assert {
    condition = (
      length(terraform_data.scope_registry) == 1 &&
      terraform_data.scope_registry["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].output.default
    )
    error_message = "The migration seed must create only the immutable default-scope registry sentinel."
  }
}

run "plan_equivalent_default_scope" {
  command = plan

  assert {
    condition = (
      length(terraform_data.scope_registry) == 1 &&
      length(terraform_data.scope_tag_policy) == 1 &&
      length(terraform_data.scope_configuration) == 1 &&
      terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.policy_version == 0 &&
      length(module.vpc) == 1 &&
      length(module.cross_account_iam) == 1 &&
      length(module.scoped_vpc) == 0 &&
      length(module.scoped_cross_account_iam) == 0 &&
      length(aws_sqs_queue.karpenter_interruption) == 1 &&
      length(aws_sqs_queue.scoped_karpenter_interruption) == 0 &&
      length(aws_cloudwatch_event_rule.karpenter_interruption) == 4 &&
      length(aws_cloudwatch_event_rule.scoped_karpenter_interruption) == 0 &&
      length(aws_ec2_instance_metadata_defaults.imdsv2) == 1 &&
      length(aws_ec2_instance_metadata_defaults.scoped_imdsv2) == 0
    )
    error_message = "An equivalent default scope must retain every legacy resource address and add no scoped replacement instances."
  }

  assert {
    condition = (
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.schema_version == 1 &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.scope_ref == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.default &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.cluster_name == "migration-eks" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.cluster_type == "eks" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.region == "ap-southeast-2" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.scope_tag_policy_version == 0 &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.mode == "dittocloud" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.name == "migration-vpc" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.cidr == "10.220.0.0/16" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.id == null &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.nat_gateway_name == null
    )
    error_message = "The initial full scope-mode plan must add an exact normalized configuration snapshot for future recovery."
  }

  assert {
    condition = (
      module.cross_account_iam[0].scope_iam.controller_role.name == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      aws_sqs_queue.karpenter_interruption[0].name == "karpenter-interruption" &&
      output.aws.region == run.apply_legacy_configuration.aws.region &&
      output.aws.vpc[0].vpc_id == run.apply_legacy_configuration.aws.vpc[0].vpc_id &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].controllerRoleName == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].karpenterInterruptionQueueName == "karpenter-interruption"
    )
    error_message = "The default-scope plan must preserve the applied legacy Region, VPC identity, IAM name, and Karpenter queue name."
  }
}
