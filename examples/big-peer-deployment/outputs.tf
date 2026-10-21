output "deployments" {
  description = "Summary of each deployment's cluster and VPC."
  value = {
    for name, dep in module.deployment : name => {
      cluster_name       = dep.cluster_name
      cluster_endpoint   = dep.cluster_endpoint
      cluster_oidc_url   = dep.cluster_oidc_issuer_url
      vpc_id             = dep.vpc_id
      private_subnet_ids = dep.private_subnet_ids
      public_subnet_ids  = dep.public_subnet_ids
      node_group_names   = dep.node_group_names
    }
  }
}
