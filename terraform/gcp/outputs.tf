output "project" {
  description = "Project information"
  value = {
    id     = data.google_project.this.project_id
    number = data.google_project.this.number
    tag = {
      key = {
        id              = google_tags_tag_key.cluster_fqdn_key.id
        namespaced_name = google_tags_tag_key.cluster_fqdn_key.namespaced_name
        short_name      = google_tags_tag_key.cluster_fqdn_key.short_name
        description     = "Used to identify resources managed by Ditto for IAM policies"
      }
      value = {
        id          = google_tags_tag_value.cluster_fqdn_value.id
        short_name  = google_tags_tag_value.cluster_fqdn_value.short_name
        description = "Used to identify resources managed by Ditto for IAM policies"
      }
    }
  }
}

output "networking" {
  description = "Networking configuration and resources"
  value = {
    vpc = {
      name      = module.vpc.network_name
      id        = module.vpc.network_id
      self_link = module.vpc.network_self_link
    }
    // TODO: make output more friendly for CAPG resources/helm charts to refer
    subnets = {
      primary = length(module.vpc.subnets_names) > 0 ? {
        name             = module.vpc.subnets_names[0]
        id               = module.vpc.subnets_ids[0]
        self_link        = module.vpc.subnets_self_links[0]
        cidr             = module.vpc.subnets_ips[0]
        secondary_ranges = length(module.vpc.subnets_names) > 0 && length(module.vpc.subnets_secondary_ranges) > 0 ? module.vpc.subnets_secondary_ranges[0] : null
      } : null
    }
  }
}

output "service_accounts" {
  description = "Service accounts created for workload clusters"
  value = {
    control_plane = {
      email = google_service_account.k8s_control_plane.email
      name  = google_service_account.k8s_control_plane.name
    }
    node = {
      email = google_service_account.k8s_node.email
      name  = google_service_account.k8s_node.name
    }
  }
}

output "iam_roles" {
  description = "Custom IAM roles created in the project"
  value = {
    capg = {
      id   = google_project_iam_custom_role.capg.id
      name = google_project_iam_custom_role.capg.name
    }
    crossplane = {
      id   = google_project_iam_custom_role.crossplane.id
      name = google_project_iam_custom_role.crossplane.name
    }
    velero = {
      id   = google_project_iam_custom_role.velero.id
      name = google_project_iam_custom_role.velero.name
    }
  }
}

output "zones" {
  description = "Available compute zones in the region"
  value       = sort(data.google_compute_zones.available.names)
}
