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
    condition     = output.aws.region == "ap-southeast-2"
    error_message = "Legacy mode must continue using the legacy root Region."
  }
}

run "creates_registry_for_valid_multi_scope_object" {
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
    condition     = output.aws.region == "us-east-1"
    error_message = "Scope mode must use the default scope Region for the root provider and legacy output."
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
}

run "preserves_default_eks_legacy_resource_addresses" {
  command = plan

  variables {
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
}
