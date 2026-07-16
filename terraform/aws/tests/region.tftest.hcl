mock_provider "aws" {
  mock_data "aws_availability_zones" {
    defaults = {
      names = [
        "ap-southeast-2a",
        "ap-southeast-2b",
        "ap-southeast-2c",
      ]
    }
  }

  mock_data "aws_region" {
    defaults = {
      region = "us-east-1"
    }
  }

  mock_data "aws_iam_policy_document" {
    defaults = {
      json          = jsonencode({ Version = "2012-10-17", Statement = [] })
      minified_json = jsonencode({ Version = "2012-10-17", Statement = [] })
    }
  }

  mock_data "aws_vpc_endpoint_service" {
    defaults = {
      service_name = "com.amazonaws.ap-southeast-2.mock"
    }
  }
}

run "uses_the_explicit_scope_region" {
  command = plan

  module {
    source = "./vpc"
  }

  override_module {
    target  = module.vpc_endpoints
    outputs = {}
  }

  variables {
    region                  = "ap-southeast-2"
    vpc_name                = "scope-vpc"
    vpc_cidr                = "10.210.0.0/16"
    kubernetes_cluster_name = "scope-cluster"
    tags = {
      "ditto.live/scope-ref" = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    }
  }

  assert {
    condition = (
      local.region == "ap-southeast-2" &&
      data.aws_availability_zones.available.region == "ap-southeast-2" &&
      output.valet_web_config.id == "ap-southeast-2" &&
      local.tags["ditto.live/scope-ref"] == "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
    )
    error_message = "A scoped VPC must resolve availability zones, resources, and outputs in its explicit Region."
  }
}
