# Enforce IMDSv2 as the account+region default for all new EC2 instances.
#
# EKS Bottlerocket managed node groups (and Karpenter-launched nodes) cannot carry a
# per-node-group launch template in CAPA - its launch-template AMI lookup only
# supports AmazonLinux/AmazonLinux2023, not Bottlerocket - so IMDSv2 (http-tokens =
# required) is enforced here at the account level instead of via a launch template.
# This applies to every instance in the account/Region, so the legacy address is
# present whenever at least one EKS scope requires it in the default Region.
resource "aws_ec2_instance_metadata_defaults" "imdsv2" {
  count                       = local.default_region_requires_eks ? 1 : 0
  http_tokens                 = "required"
  http_put_response_hop_limit = 2

  depends_on = [terraform_data.scope_registry]
}

# Additional EKS Regions use one Region-keyed singleton shared by every EKS
# scope in that Region. The default Region stays at the legacy address above.
resource "aws_ec2_instance_metadata_defaults" "scoped_imdsv2" {
  for_each = local.scoped_imds_scope_refs_by_region

  region                      = each.key
  http_tokens                 = "required"
  http_put_response_hop_limit = 2

  depends_on = [terraform_data.scope_registry]
}
