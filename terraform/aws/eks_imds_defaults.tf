# Enforce IMDSv2 as the account+region default for all new EC2 instances.
#
# EKS Bottlerocket managed node groups (and Karpenter-launched nodes) cannot carry a
# per-node-group launch template in CAPA - its launch-template AMI lookup only
# supports AmazonLinux/AmazonLinux2023, not Bottlerocket - so IMDSv2 (http-tokens =
# required) is enforced here at the account level instead of via a launch template.
# This applies to every instance in the account/region, so it is gated on enable_eks.
resource "aws_ec2_instance_metadata_defaults" "imdsv2" {
  count                       = var.enable_eks ? 1 : 0
  http_tokens                 = "required"
  http_put_response_hop_limit = 2
}
