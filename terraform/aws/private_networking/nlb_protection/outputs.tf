output "nlb" {
  description = "Protected NLB details"
  value = {
    arn         = data.aws_lb.big_peer_nlb.arn
    name        = data.aws_lb.big_peer_nlb.name
    dns_name    = data.aws_lb.big_peer_nlb.dns_name
    eni_ids     = data.aws_network_interfaces.nlb_enis.ids
  }
}

output "protected_roles" {
  description = "IAM roles with deny policies attached"
  value = {
    controller_role   = var.capa_controller_role_name
    controlplane_role = var.capa_controlplane_role_name
    nodes_role        = var.capa_nodes_role_name
  }
}

output "deny_policies" {
  description = "Names of the deny policies created"
  value = {
    controller_policy   = aws_iam_role_policy.deny_nlb_modification_controller.name
    controlplane_policy = aws_iam_role_policy.deny_nlb_modification_controlplane.name
    nodes_policy        = aws_iam_role_policy.deny_nlb_modification_nodes.name
  }
}

output "protection_summary" {
  description = "Summary of NLB protection"
  value = {
    big_peer_name   = var.big_peer_name
    nlb_arn         = data.aws_lb.big_peer_nlb.arn
    protected_enis  = length(data.aws_network_interfaces.nlb_enis.ids)
    status          = "LOCKED"
  }
}
