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

run "accepts_valid_multi_scope_object" {
  command = plan

  variables {
    deployment_scopes = {
      legacy = {
        default      = true
        cluster_type = "kubeadm"
        region       = "ap-southeast-2"
        vpc = {
          mode = "existing"
          id   = "vpc-09e877f9012f52241"
        }
      }
      "sydney-eks" = {
        cluster_name = "sydney-eks"
        cluster_type = "eks"
        region       = "ap-southeast-2"
        vpc = {
          mode = "dittocloud"
          name = "ditto-sydney"
          cidr = "10.210.0.0/16"
        }
      }
      "capi-virginia" = {
        region = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
    }
  }
}

run "rejects_missing_default_scope" {
  command = plan

  variables {
    deployment_scopes = {
      sydney = {
        region = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}

run "rejects_invalid_scope_reference" {
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

run "rejects_invalid_existing_vpc" {
  command = plan

  variables {
    deployment_scopes = {
      legacy = {
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

run "rejects_duplicate_cluster_names" {
  command = plan

  variables {
    deployment_scopes = {
      first = {
        default      = true
        cluster_name = "shared-cluster"
        region       = "ap-southeast-2"
        vpc = {
          mode = "capi"
        }
      }
      second = {
        cluster_name = "shared-cluster"
        region       = "us-east-1"
        vpc = {
          mode = "capi"
        }
      }
    }
  }

  expect_failures = [var.deployment_scopes]
}
