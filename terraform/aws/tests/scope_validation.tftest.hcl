# Root-module coverage for the multi-scope AWS configuration.
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

  mock_data "aws_availability_zones" {
    defaults = {
      names = [
        "ap-southeast-2a",
        "ap-southeast-2b",
        "ap-southeast-2c",
      ]
    }
  }

  mock_data "aws_subnets" {
    defaults = {
      ids = [
        "subnet-00000000000000001",
        "subnet-00000000000000002",
      ]
    }
  }

  mock_data "aws_iam_policy_document" {
    defaults = {
      json          = jsonencode({ Version = "2012-10-17", Statement = [] })
      minified_json = jsonencode({ Version = "2012-10-17", Statement = [] })
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
}

variables {
  profile    = "test"
  region     = "ap-southeast-2"
  create_iam = false
  create_vpc = false
}

run "preserves_legacy_mode_without_registry" {
  command = plan

  assert {
    condition     = length(terraform_data.scope_registry) == 0
    error_message = "Legacy mode must not create scope registry sentinels."
  }

  assert {
    condition     = length(terraform_data.scope_tag_policy) == 0
    error_message = "Legacy mode must not create applied tag-policy markers."
  }

  assert {
    condition     = output.aws.region == "ap-southeast-2"
    error_message = "Legacy mode must continue using the legacy root Region."
  }

  assert {
    condition     = toset(keys(output.aws)) == toset(["account_id", "region", "vpc"])
    error_message = "Legacy mode must retain the exact existing AWS output shape without scope-aware fields."
  }
}

run "creates_registry_for_valid_multi_scope_object" {
  command = plan

  override_module {
    target = module.scoped_vpc["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"]
    outputs = {
      vpc_id = "vpc-00000000000000001"
      vpc = {
        vpc_id           = "vpc-00000000000000001"
        private_subnets  = ["subnet-00000000000000001"]
        public_subnets   = ["subnet-00000000000000002"]
        database_subnets = []
        pod_subnets      = []
        node_subnets     = []
        secondary_cidr   = null
      }
      pod_subnets_by_az = []
      nat_public_ips    = []
    }
  }

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_type = "kubeadm"
        region       = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        cluster_name = "sydney-eks"
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode = "dittocloud"
          name = "ditto-sydney"
          cidr = "10.210.0.0/16"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" = {
        region = "us-west-2"
        vpc = {
          mode = "existing"
          id   = "vpc-09e877f9012f52241"
        }
      }
    }
  }

  assert {
    condition     = length(terraform_data.scope_registry) == 3
    error_message = "Scope mode must create one registry sentinel per configured scope."
  }


  assert {
    condition     = length(terraform_data.scope_tag_policy) == 3
    error_message = "Scope mode must create one applied tag-policy marker per configured scope."
  }

  assert {
    condition     = length(terraform_data.scope_configuration) == 3
    error_message = "Scope mode must persist one normalized applied-configuration snapshot per configured scope."
  }

  assert {
    condition = terraform_data.scope_registry["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input == {
      schema_version = 1
      scope_ref      = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
      default        = true
    }
    error_message = "The default registry sentinel must persist its schema, identity, and default marker."
  }

  assert {
    condition = terraform_data.scope_registry["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input == {
      schema_version = 1
      scope_ref      = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"
      default        = false
    }
    error_message = "A non-default registry sentinel must persist its identity without claiming the default."
  }


  assert {
    condition = terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input == {
      schema_version = 1
      scope_ref      = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
      policy_version = 0
    }
    error_message = "The default scope must persist its applied tag-policy version separately from identity."
  }

  assert {
    condition = terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input == {
      schema_version = 1
      scope_ref      = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"
      policy_version = 0
    }
    error_message = "A non-default scope must persist its applied tag-policy version separately from identity."
  }

  assert {
    condition = (
      local.default_iam_cluster_name == null &&
      local.scoped_iam_cluster_names["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"] == null
    )
    error_message = "Policy version 0 must not enable single-cluster IAM conditions even when cluster_name is recorded."
  }

  assert {
    condition = (
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.schema_version == 1 &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.scope_ref == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      !terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.default &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.cluster_name == "sydney-eks" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.cluster_type == "eks" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.region == "ap-southeast-2" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.scope_tag_policy_version == 0 &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.vpc.mode == "dittocloud" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.vpc.name == "ditto-sydney" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.vpc.cidr == "10.210.0.0/16" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.vpc.id == null &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.configuration.vpc.nat_gateway_name == null
    )
    error_message = "The applied configuration snapshot must retain the complete normalized EKS scope intent for exact recovery."
  }

  assert {
    condition     = output.aws.region == "us-east-1"
    error_message = "Scope mode must use the default scope Region for the root provider and legacy output."
  }

  assert {
    condition = (
      toset(keys(output.aws)) == toset(["account_id", "region", "vpc", "scopes", "regionalResources"]) &&
      output.aws.account_id == "123456789012" &&
      length(output.aws.vpc) == 0 &&
      join(",", keys(output.aws.scopes)) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3,dsc-01k2m8g7n4p6q9r3t5v8x1y2z4,dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"
    )
    error_message = "Scope mode must extend the legacy AWS output with complete, lexically keyed scopes and regionalResources maps."
  }

  assert {
    condition = alltrue([
      for scope_ref, scope_output in output.aws.scopes :
      scope_output.scopeRef == scope_ref &&
      toset(keys(scope_output)) == toset([
        "scopeRef",
        "default",
        "accountId",
        "region",
        "clusterType",
        "scopeTagPolicyVersion",
        "controllerRoleName",
        "controllerRoleArn",
        "trustEditorRoleName",
        "trustEditorRoleArn",
        "managedClusterRolePath",
        "nodesRoleName",
        "nodesRoleArn",
        "nodesInstanceProfileName",
        "nodesInstanceProfileArn",
        "controlPlaneRoleName",
        "controlPlaneRoleArn",
        "controlPlaneInstanceProfileName",
        "controlPlaneInstanceProfileArn",
        "eksControlPlaneRoleName",
        "eksControlPlaneRoleArn",
        "clusterBoundaryName",
        "clusterBoundaryArn",
        "externalBoundaryName",
        "externalBoundaryArn",
        "karpenterInterruptionQueueName",
        "karpenterInterruptionQueueArn",
        "vpcId",
        "privateSubnetIds",
        "publicSubnetIds",
        "databaseSubnetIds",
        "iamSubnetIds",
      ])
    ])
    error_message = "Every scope output must repeat its map key and expose the complete stable binding schema, including explicit nullable fields."
  }

  assert {
    condition = (
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].default &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].accountId == "123456789012" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].region == "us-east-1" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].clusterType == "kubeadm" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].scopeTagPolicyVersion == 0 &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].controllerRoleName == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].managedClusterRolePath == "/dittocluster/" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].eksControlPlaneRoleName == null &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].karpenterInterruptionQueueName == null &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].vpcId == null &&
      length(output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].iamSubnetIds) == 0
    )
    error_message = "The default CAPI kubeadm output must preserve legacy IAM names and report unavailable VPC, EKS, and Karpenter values explicitly as null or empty lists."
  }

  assert {
    condition = (
      !output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].default &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].clusterType == "eks" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].controllerRoleName == "ditto-capa-controller-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].managedClusterRolePath == "/dittocluster/dsc-01k2m8g7n4p6q9r3t5v8x1y2z4/" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].eksControlPlaneRoleName == "ditto-capa-eks-control-plane-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].karpenterInterruptionQueueName == "karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].vpcId == "vpc-00000000000000001" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].privateSubnetIds) == "subnet-00000000000000001" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].publicSubnetIds) == "subnet-00000000000000002" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].iamSubnetIds) == "subnet-00000000000000001,subnet-00000000000000002"
    )
    error_message = "A managed EKS scope output must use exact scoped IAM and Karpenter names and the managed VPC's deterministic subnet sets."
  }

  assert {
    condition = (
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].vpcId == "vpc-09e877f9012f52241" &&
      length(output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].databaseSubnetIds) == 0 &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].karpenterInterruptionQueueName == null
    )
    error_message = "An existing-VPC kubeadm output must expose its configured VPC, an explicit empty database subnet list, and null Karpenter values."
  }

  assert {
    condition = (
      join(",", keys(output.aws.regionalResources)) == "ap-southeast-2,us-east-1,us-west-2" &&
      output.aws.regionalResources["ap-southeast-2"].region == "ap-southeast-2" &&
      join(",", output.aws.regionalResources["ap-southeast-2"].scopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      join(",", output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults.requiredByScopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults.httpTokens == "required" &&
      output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults.httpPutResponseHopLimit == 2 &&
      output.aws.regionalResources["us-east-1"].ec2InstanceMetadataDefaults == null &&
      output.aws.regionalResources["us-west-2"].ec2InstanceMetadataDefaults == null
    )
    error_message = "Regional outputs must be lexically keyed, repeat their Region, and distinguish managed IMDS settings from explicit null values."
  }

  assert {
    condition = (
      length(module.scoped_cross_account_iam) == 2 &&
      length(module.scoped_vpc) == 1 &&
      contains(keys(module.scoped_vpc), "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4")
    )
    error_message = "Every non-default scope must receive keyed IAM, while only Dittocloud-managed scopes receive a keyed VPC module."
  }

  assert {
    condition = (
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.scope_ref == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.controller_role.name == "ditto-capa-controller-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].scope_iam.managed_cluster_role_path == "/dittocluster/dsc-01k2m8g7n4p6q9r3t5v8x1y2z5/"
    )
    error_message = "Keyed IAM instances must repeat their immutable scope identity and exact scoped names and paths."
  }

  assert {
    condition = (
      local.scoped_effective_vpc_ids["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"] == "vpc-09e877f9012f52241" &&
      data.aws_subnets.scoped_existing_private["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].region == "us-west-2"
    )
    error_message = "An existing-VPC scope must keep its own VPC ID and discover subnets in its own Region."
  }

  assert {
    condition = (
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.confinement.vpc_id == "vpc-00000000000000001" &&
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].scope_iam.confinement.vpc_id == "vpc-09e877f9012f52241" &&
      toset(module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.confinement.subnet_ids) == toset([
        "subnet-00000000000000001",
        "subnet-00000000000000002",
      ])
    )
    error_message = "Each keyed IAM module must receive only its own VPC and subnet set, never a cross-scope union."
  }
}

run "retains_existing_vpc_discovery_at_policy_size_limit" {
  command = apply

  # 6 is the real, measured ceiling for a kubeadm (non-EKS) scope: at 7+
  # subnets the generated cluster_resources_boundary_policy exceeds IAM's
  # 6,144-character managed-policy limit (see cross_account_iam/variables.tf).
  override_data {
    target          = data.aws_subnets.scoped_existing_private["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"]
    override_during = plan
    values = {
      ids = [
        "subnet-00000000000000001",
        "subnet-00000000000000002",
        "subnet-00000000000000003",
      ]
    }
  }

  override_data {
    target          = data.aws_subnets.scoped_existing_public["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"]
    override_during = plan
    values = {
      ids = [
        "subnet-00000000000000004",
        "subnet-00000000000000005",
        "subnet-00000000000000006",
      ]
    }
  }

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        region = "us-west-2"
        vpc = {
          mode = "existing"
          id   = "vpc-09e877f9012f52241"
        }
      }
    }
  }

  assert {
    condition = (
      data.aws_subnets.scoped_existing_private["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].region == "us-west-2" &&
      data.aws_subnets.scoped_existing_public["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].region == "us-west-2" &&
      length(module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.confinement.subnet_ids) == 6 &&
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.confinement.vpc_id == "vpc-09e877f9012f52241" &&
      module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].scope_iam.policies.controller_vpc_lifecycle == null
    )
    error_message = "An existing-VPC scope must discover only its own Region's six Kubernetes load-balancer subnets, confine IAM to that VPC, and omit VPC lifecycle permissions."
  }
}

run "rejects_missing_default_scope" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        region = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_non_generated_scope_reference" {
  command = plan

  variables {
    deployment_scopes = {
      Sydney_EKS = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_out_of_range_ulid_scope_reference" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-81k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_forbidden_crockford_character" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01i2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_invalid_existing_vpc" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode = "existing"
          id   = "not-a-vpc-id"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_nat_gateway_name_outside_managed_vpc" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode             = "existing"
          id               = "vpc-09e877f9012f52241"
          nat_gateway_name = "founding-cluster-nat"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_invalid_scope_tag_policy_version" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default                  = true
        region                   = "ap-southeast-2"
        scope_tag_policy_version = 2
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "requires_named_cluster_for_scope_tag_policy_version_one" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default                  = true
        region                   = "ap-southeast-2"
        scope_tag_policy_version = 1
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_direct_terraform_scope_tag_policy_enablement" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default                  = true
        cluster_name             = "secure-cluster"
        region                   = "ap-southeast-2"
        scope_tag_policy_version = 1
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"]]
}

run "accepts_cli_authorized_scope_tag_policy_version_one" {
  command = plan

  variables {
    scope_tag_policy_cli_authorized_refs = ["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"]
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default                  = true
        cluster_name             = "secure-cluster"
        region                   = "ap-southeast-2"
        scope_tag_policy_version = 1
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition = (
      local.default_iam_cluster_name == "secure-cluster" &&
      terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.policy_version == 1
    )
    error_message = "CLI-authorized policy version 1 must pass the exact cluster name into single-cluster IAM and advance the marker."
  }
}

run "applies_version_one_only_to_the_authorized_non_default_scope" {
  command = plan

  variables {
    scope_tag_policy_cli_authorized_refs = ["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"]
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        cluster_name             = "secure-eks"
        cluster_type             = "eks"
        region                   = "ap-southeast-2"
        scope_tag_policy_version = 1
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition = (
      local.default_iam_cluster_name == null &&
      local.scoped_iam_cluster_names["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"] == "secure-eks" &&
      terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.policy_version == 0 &&
      terraform_data.scope_tag_policy["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].input.policy_version == 1
    )
    error_message = "Version-1 IAM tightening and the applied marker must be isolated to the authorized non-default scope."
  }
}

run "accepts_duplicate_cluster_names" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_name = "shared-cluster"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        cluster_name = "shared-cluster"
        region       = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition     = length(terraform_data.scope_registry) == 2
    error_message = "Repeated cluster names must not prevent distinct scope identities from being registered."
  }
}

run "shares_default_region_imds_without_claiming_default_karpenter" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_type = "kubeadm"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition     = length(aws_ec2_instance_metadata_defaults.imdsv2) == 1
    error_message = "An EKS scope in the default Region must require the shared legacy-address IMDS default."
  }

  assert {
    condition     = length(aws_sqs_queue.karpenter_interruption) == 0
    error_message = "A non-default EKS scope must not claim the default scope's unsuffixed Karpenter queue."
  }

  assert {
    condition     = length(aws_ec2_instance_metadata_defaults.scoped_imdsv2) == 0
    error_message = "An EKS scope in the default Region must share the legacy-address IMDS singleton instead of creating a second Region-keyed resource."
  }

  assert {
    condition = (
      length(aws_sqs_queue.scoped_karpenter_interruption) == 1 &&
      aws_sqs_queue.scoped_karpenter_interruption["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].name == "karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      aws_sqs_queue.scoped_karpenter_interruption["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].region == "ap-southeast-2" &&
      aws_sqs_queue.scoped_karpenter_interruption["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"
    )
    error_message = "A non-default EKS scope must receive its exact scoped Karpenter queue in its own Region with the reserved identity tag."
  }

  assert {
    condition = (
      length(aws_cloudwatch_event_rule.scoped_karpenter_interruption) == 4 &&
      toset([for rule in values(aws_cloudwatch_event_rule.scoped_karpenter_interruption) : rule.name]) == toset([
        "KarpenterSpotInterruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "KarpenterRebalance-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "KarpenterInstanceState-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "KarpenterHealth-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
      ]) &&
      alltrue([for rule in values(aws_cloudwatch_event_rule.scoped_karpenter_interruption) : rule.region == "ap-southeast-2"])
    )
    error_message = "A non-default EKS scope must receive all four exact scoped EventBridge rules in its own Region."
  }

  assert {
    condition = (
      length(aws_sqs_queue_policy.scoped_karpenter_interruption) == 1 &&
      length(aws_cloudwatch_event_target.scoped_karpenter_interruption) == 4 &&
      length(terraform_data.scoped_karpenter_name_validation) == 5
    )
    error_message = "A non-default EKS scope must receive one queue policy, four EventBridge targets, and validation for every generated name."
  }
}

run "preserves_default_eks_legacy_resource_addresses" {
  command = plan

  variables {
    tags = {
      "ditto.live/scope-ref" = "cannot-override"
    }
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition     = length(module.cross_account_iam) == 1
    error_message = "The default scope must continue using module.cross_account_iam[0]."
  }

  assert {
    condition = (
      module.cross_account_iam[0].scope_iam.scope_ref == null &&
      module.cross_account_iam[0].scope_iam.scope_identity_ref == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    )
    error_message = "The default scope must preserve legacy IAM naming while carrying its immutable tag identity."
  }

  assert {
    condition     = length(aws_ec2_instance_metadata_defaults.imdsv2) == 1
    error_message = "The default EKS scope must continue using aws_ec2_instance_metadata_defaults.imdsv2[0]."
  }

  assert {
    condition     = length(aws_sqs_queue.karpenter_interruption) == 1
    error_message = "The default EKS scope must continue using aws_sqs_queue.karpenter_interruption[0]."
  }

  assert {
    condition     = length(aws_cloudwatch_event_rule.karpenter_interruption) == 4
    error_message = "The default EKS scope must retain all four unsuffixed EventBridge rules."
  }

  assert {
    condition = (
      aws_sqs_queue.karpenter_interruption[0].tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" &&
      alltrue([
        for rule in values(aws_cloudwatch_event_rule.karpenter_interruption) :
        rule.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
      ])
    )
    error_message = "The default scope's unsuffixed Karpenter resources must carry the reserved identity tag and reject user override."
  }

  assert {
    condition = (
      length(aws_sqs_queue.scoped_karpenter_interruption) == 0 &&
      length(aws_sqs_queue_policy.scoped_karpenter_interruption) == 0 &&
      length(aws_cloudwatch_event_rule.scoped_karpenter_interruption) == 0 &&
      length(aws_cloudwatch_event_target.scoped_karpenter_interruption) == 0 &&
      length(aws_ec2_instance_metadata_defaults.scoped_imdsv2) == 0 &&
      length(terraform_data.scoped_karpenter_name_validation) == 0
    )
    error_message = "A default-only EKS deployment must not create any non-default Karpenter or Region-keyed IMDS resources."
  }

  assert {
    condition = (
      length(output.aws.scopes) == 1 &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].controllerRoleName == "controllers.cluster-api-provider-aws.sigs.k8s.io" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].eksControlPlaneRoleName == "eks-controlplane.cluster-api-provider-aws.sigs.k8s.io" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].karpenterInterruptionQueueName == "karpenter-interruption" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].vpcId == null &&
      join(",", output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults.requiredByScopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    )
    error_message = "The default EKS binding must export legacy IAM and queue names while reporting the default Region's shared IMDS requirement."
  }
}

run "preserves_default_managed_vpc_legacy_output_shape" {
  command = plan

  override_module {
    target = module.vpc[0]
    outputs = {
      vpc_id = "vpc-00000000000000003"
      vpc = {
        vpc_id           = "vpc-00000000000000003"
        private_subnets  = ["subnet-00000000000000003"]
        public_subnets   = ["subnet-00000000000000004"]
        database_subnets = ["subnet-00000000000000005"]
        pod_subnets      = []
        node_subnets     = []
        secondary_cidr   = null
      }
      pod_subnets_by_az = []
      nat_public_ips    = []
    }
  }

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode = "dittocloud"
          name = "default-vpc"
          cidr = "10.220.0.0/16"
        }
      }
    }
  }

  assert {
    condition = (
      length(output.aws.vpc) == 1 &&
      output.aws.vpc[0].vpc_id == "vpc-00000000000000003" &&
      join(",", output.aws.vpc[0].vpc.private_subnets) == "subnet-00000000000000003" &&
      join(",", output.aws.vpc[0].vpc.public_subnets) == "subnet-00000000000000004" &&
      join(",", output.aws.vpc[0].vpc.database_subnets) == "subnet-00000000000000005"
    )
    error_message = "A Dittocloud-managed default scope must preserve the legacy one-element aws.vpc module-output list."
  }

  assert {
    condition = (
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].vpcId == "vpc-00000000000000003" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].privateSubnetIds) == "subnet-00000000000000003" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].publicSubnetIds) == "subnet-00000000000000004" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].databaseSubnetIds) == "subnet-00000000000000005" &&
      join(",", output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].iamSubnetIds) == "subnet-00000000000000003,subnet-00000000000000004" &&
      output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults == null
    )
    error_message = "The scope-aware default binding must expose the same managed VPC plus deterministic IAM subnets without inventing an IMDS requirement for kubeadm."
  }
}

run "isolates_multiple_eks_scopes_while_sharing_region_imds" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_type = "kubeadm"
        region       = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" = {
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition = (
      length(aws_sqs_queue.karpenter_interruption) == 0 &&
      length(aws_cloudwatch_event_rule.karpenter_interruption) == 0
    )
    error_message = "A default kubeadm scope must not create the default Karpenter resources."
  }

  assert {
    condition = (
      keys(aws_sqs_queue.scoped_karpenter_interruption) == [
        "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "dsc-01k2m8g7n4p6q9r3t5v8x1y2z5",
      ] &&
      toset([for queue in values(aws_sqs_queue.scoped_karpenter_interruption) : queue.name]) == toset([
        "karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z5",
      ]) &&
      alltrue([for queue in values(aws_sqs_queue.scoped_karpenter_interruption) : queue.region == "ap-southeast-2"])
    )
    error_message = "Every non-default EKS scope must receive an isolated, exactly named queue in the shared Region."
  }

  assert {
    condition = (
      length(aws_cloudwatch_event_rule.scoped_karpenter_interruption) == 8 &&
      length(aws_sqs_queue_policy.scoped_karpenter_interruption) == 2 &&
      length(aws_cloudwatch_event_target.scoped_karpenter_interruption) == 8 &&
      length(terraform_data.scoped_karpenter_name_validation) == 10 &&
      alltrue([for rule in values(aws_cloudwatch_event_rule.scoped_karpenter_interruption) : rule.region == "ap-southeast-2"]) &&
      alltrue([for target in values(aws_cloudwatch_event_target.scoped_karpenter_interruption) : target.region == "ap-southeast-2"])
    )
    error_message = "Two non-default EKS scopes must receive two policies, eight scoped rules and targets, and ten generated-name guards."
  }

  assert {
    condition = (
      length(aws_ec2_instance_metadata_defaults.imdsv2) == 0 &&
      toset(keys(aws_ec2_instance_metadata_defaults.scoped_imdsv2)) == toset(["ap-southeast-2"]) &&
      aws_ec2_instance_metadata_defaults.scoped_imdsv2["ap-southeast-2"].region == "ap-southeast-2" &&
      aws_ec2_instance_metadata_defaults.scoped_imdsv2["ap-southeast-2"].http_tokens == "required" &&
      aws_ec2_instance_metadata_defaults.scoped_imdsv2["ap-southeast-2"].http_put_response_hop_limit == 2 &&
      toset(local.eks_scope_refs_by_region["ap-southeast-2"]) == toset([
        "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "dsc-01k2m8g7n4p6q9r3t5v8x1y2z5",
      ])
    )
    error_message = "EKS scopes sharing a non-default Region must share one Region-keyed IMDSv2 singleton with both scope references recorded in deterministic order."
  }

  assert {
    condition = (
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].karpenterInterruptionQueueName == "karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      output.aws.scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"].karpenterInterruptionQueueName == "karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" &&
      join(",", output.aws.regionalResources["ap-southeast-2"].scopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4,dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" &&
      join(",", output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults.requiredByScopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4,dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" &&
      output.aws.regionalResources["us-east-1"].ec2InstanceMetadataDefaults == null
    )
    error_message = "Same-Region EKS scope outputs must keep separate queue bindings while sharing one deterministic regional IMDS binding."
  }
}

run "creates_imds_only_for_eks_regions_in_mixed_scope_set" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_type = "kubeadm"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        cluster_type = "eks"
        region       = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" = {
        cluster_type = "kubeadm"
        region       = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z6" = {
        cluster_type = "eks"
        region       = "us-west-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  assert {
    condition = (
      length(aws_ec2_instance_metadata_defaults.imdsv2) == 0 &&
      toset(keys(aws_ec2_instance_metadata_defaults.scoped_imdsv2)) == toset(["us-east-1", "us-west-2"]) &&
      toset(local.eks_scope_refs_by_region["us-east-1"]) == toset(["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"]) &&
      toset(local.eks_scope_refs_by_region["us-west-2"]) == toset(["dsc-01k2m8g7n4p6q9r3t5v8x1y2z6"])
    )
    error_message = "Only Regions containing EKS scopes must receive Region-keyed IMDS defaults; kubeadm scopes must not create or join them."
  }

  assert {
    condition = (
      keys(aws_sqs_queue.scoped_karpenter_interruption) == [
        "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4",
        "dsc-01k2m8g7n4p6q9r3t5v8x1y2z6",
      ] &&
      aws_sqs_queue.scoped_karpenter_interruption["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].region == "us-east-1" &&
      aws_sqs_queue.scoped_karpenter_interruption["dsc-01k2m8g7n4p6q9r3t5v8x1y2z6"].region == "us-west-2" &&
      length(aws_cloudwatch_event_rule.scoped_karpenter_interruption) == 8
    )
    error_message = "Karpenter resources must be created only for EKS scopes and must use each owning scope's Region."
  }

  assert {
    condition = (
      join(",", keys(output.aws.regionalResources)) == "ap-southeast-2,us-east-1,us-west-2" &&
      output.aws.regionalResources["ap-southeast-2"].ec2InstanceMetadataDefaults == null &&
      join(",", output.aws.regionalResources["us-east-1"].scopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4,dsc-01k2m8g7n4p6q9r3t5v8x1y2z5" &&
      join(",", output.aws.regionalResources["us-east-1"].ec2InstanceMetadataDefaults.requiredByScopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" &&
      join(",", output.aws.regionalResources["us-west-2"].ec2InstanceMetadataDefaults.requiredByScopeRefs) == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z6"
    )
    error_message = "Regional outputs must list every scope but only EKS scope references as IMDS contributors across mixed multi-Region deployments."
  }
}

run "publishes_workload_networking_for_a_managed_scope" {
  command = plan

  override_module {
    target = module.vpc[0]
    outputs = {
      vpc_id = "vpc-00000000000000006"
      vpc = {
        vpc_id           = "vpc-00000000000000006"
        private_subnets  = ["subnet-00000000000000010"]
        public_subnets   = ["subnet-00000000000000011"]
        database_subnets = ["subnet-00000000000000012"]
        pod_subnets      = ["subnet-00000000000000013"]
        node_subnets     = ["subnet-00000000000000014"]
        secondary_cidr   = "100.64.0.0/16"
      }
      pod_subnets_by_az = [
        {
          availability_zone = "ap-southeast-2a"
          subnet_id         = "subnet-00000000000000013"
        },
      ]
      nat_public_ips = ["198.51.100.10"]
    }
  }

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default      = true
        cluster_name = "valet-dev"
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode                   = "dittocloud"
          name                   = "valet"
          cidr                   = "10.214.0.0/20"
          secondary_cidr         = "100.64.0.0/16"
          public_subnet_netmask  = 24
          private_subnet_netmask = 23
        }
      }
    }
  }

  assert {
    condition = (
      output.aws_workload_networking["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].secondaryCidr == "100.64.0.0/16" &&
      join(",", output.aws_workload_networking["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].podSubnetIds) == "subnet-00000000000000013" &&
      join(",", output.aws_workload_networking["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].nodeSubnetIds) == "subnet-00000000000000014" &&
      output.aws_workload_networking["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].podSubnetsByAz[0].availabilityZone == "ap-southeast-2a" &&
      output.aws_workload_networking["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].podSubnetsByAz[0].subnetId == "subnet-00000000000000013" &&
      join(",", output.aws_workload_networking["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].natPublicIps) == "198.51.100.10"
    )
    error_message = "A managed scope must publish its secondary CIDR, workload subnets, per-availability-zone pod subnets for ENIConfigs, and NAT egress addresses."
  }

  assert {
    condition = (
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.secondary_cidr == "100.64.0.0/16" &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.public_subnet_netmask == 24 &&
      terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.private_subnet_netmask == 23 &&
      length(terraform_data.scope_configuration["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].input.configuration.vpc.nat_gateway_eip_allocation_ids) == 0
    )
    error_message = "The applied-configuration snapshot must record the workload block and subnet sizing, because recovery rebuilds the scopes file from it."
  }
}

run "rejects_a_secondary_cidr_shared_between_scopes" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode           = "dittocloud"
          name           = "valet-a"
          cidr           = "10.214.0.0/20"
          secondary_cidr = "100.64.0.0/16"
        }
      }
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4" = {
        region = "us-west-2"
        vpc = {
          mode           = "dittocloud"
          name           = "valet-b"
          cidr           = "10.215.0.0/20"
          secondary_cidr = "100.64.0.0/16"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_a_secondary_cidr_on_an_unmanaged_vpc" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode           = "existing"
          id             = "vpc-09e877f9012f52241"
          secondary_cidr = "100.64.0.0/16"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_scope_subnet_netmasks_that_do_not_fit_the_primary_block" {
  command = plan

  variables {
    deployment_scopes = {
      "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3" = {
        default = true
        region  = "ap-southeast-2"
        vpc = {
          mode                  = "dittocloud"
          name                  = "valet"
          cidr                  = "10.214.0.0/22"
          public_subnet_netmask = 24
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}
