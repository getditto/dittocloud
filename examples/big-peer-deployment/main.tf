# Parse the deployment definitions from YAML. Each top-level key under
# "deployments" is a complete VPC + EKS stack.
locals {
  deployments = yamldecode(file(var.config_path)).deployments

  # The root provider serves all deployments, so every deployment must target
  # the same region as the provider. Multi-region support can be added later by
  # passing per-region provider configurations into each deployment module.
  deployment_regions = distinct([for d in local.deployments : d.region])

  precondition_regions_match = length(local.deployment_regions) == 1 && local.deployment_regions[0] == var.region
}

resource "terraform_data" "validate_single_region" {
  input = local.deployment_regions

  lifecycle {
    precondition {
      condition     = local.precondition_regions_match
      error_message = "All deployments must use the root provider region (${var.region}). Found regions: ${join(", ", local.deployment_regions)}."
    }
  }
}

module "deployment" {
  source   = "./modules/deployment"
  for_each = local.deployments

  deployment = each.value
  tags       = var.tags

  providers = {
    aws = aws
  }
}
