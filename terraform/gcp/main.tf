locals {
  # https://cloud.google.com/iam/docs/tags-access-control#control-access
  # The project is tagged with this tag key-value pair. To revoke access for an individual resource, override the tag value at the resource level.
  iam_condition = {
    title       = "Ditto resource tagging requirement"
    description = "Enforce Ditto resource conditions for IAM access"
    expression  = "resource.matchTag('${google_tags_tag_key.cluster_fqdn_key.namespaced_name}', '${google_tags_tag_value.cluster_fqdn_value.short_name}')"
  }
  capg_service_account_email       = "${var.capg_iam.service_account_name}${var.env_suffix}@${var.capg_iam.service_account_project}.iam.gserviceaccount.com"
  crossplane_service_account_email = "${var.crossplane_iam.service_account_name}${var.env_suffix}@${var.crossplane_iam.service_account_project}.iam.gserviceaccount.com"
}

data "google_project" "this" {
  project_id = var.project_id

  depends_on = [time_sleep.wait_for_project_services]
}

data "google_compute_zones" "available" {
  project = var.project_id
  region  = var.region
}

# TODO: enable IAM audit logging for the project
resource "google_tags_tag_key" "cluster_fqdn_key" {
  parent      = data.google_project.this.id
  short_name  = var.iam_condition_tag.short_name
  description = "marks that a resource is managed by the ditto management cluster"
}

resource "google_tags_tag_value" "cluster_fqdn_value" {
  parent      = google_tags_tag_key.cluster_fqdn_key.id
  short_name  = var.iam_condition_tag.value
  description = "Mangement cluster identifier"
}

# Tag the project, so that every resource created in the project will have the tag
resource "google_tags_tag_binding" "project_tag" {
  parent    = "//cloudresourcemanager.googleapis.com/projects/${data.google_project.this.number}"
  tag_value = google_tags_tag_value.cluster_fqdn_value.id
}

resource "google_project_iam_custom_role" "capg" {
  role_id     = var.capg_iam.controller_custom_role_name
  title       = "Ditto Management Service"
  description = "Role for the Ditto Management Service"
  permissions = local.ditto_mangement_plane_role.permissions

  depends_on = [module.project-services]
}

# Setting a IAM policy binding with the tag condition. IAM bindings work if any of them grant access.
# If a condition checks the tags for a resource, it cannot check any other attributes, such as the resource name or the timestamp for the request.
resource "google_project_iam_member" "capg_controller_access" {
  project = data.google_project.this.project_id
  role    = google_project_iam_custom_role.capg.id
  member  = "serviceAccount:${local.capg_service_account_email}"

  condition {
    title       = local.iam_condition.title
    description = local.iam_condition.description
    expression  = local.iam_condition.expression
  }
}

resource "google_service_account" "k8s_control_plane" {
  account_id   = var.capg_iam.control_plane_service_account_name
  display_name = "Ditto Workload Cluster Control Plane Service Account"
}

resource "google_project_iam_member" "k8s_control_plane_access" {
  project = data.google_project.this.project_id
  role    = var.capg_iam.control_plane_role_id
  member  = "serviceAccount:${google_service_account.k8s_control_plane.email}"

  condition {
    title       = local.iam_condition.title
    description = local.iam_condition.description
    expression  = local.iam_condition.expression
  }
}

resource "google_project_iam_member" "k8s_control_plane_token_creator" {
  project = data.google_project.this.project_id
  role    = "roles/iam.serviceAccountTokenCreator"
  member  = "serviceAccount:${google_service_account.k8s_control_plane.email}"

  condition {
    title       = local.iam_condition.title
    description = local.iam_condition.description
    expression  = local.iam_condition.expression
  }
}

resource "google_service_account" "k8s_node" {
  account_id   = var.capg_iam.node_service_account_name
  display_name = "Ditto Workload Cluster Node Service Account"
}

resource "google_project_iam_member" "k8s_node_access" {
  project = data.google_project.this.project_id
  role    = var.capg_iam.node_role_id
  member  = "serviceAccount:${google_service_account.k8s_node.email}"

  condition {
    title       = local.iam_condition.title
    description = local.iam_condition.description
    expression  = local.iam_condition.expression
  }
}

resource "google_project_iam_member" "k8s_node_token_creator" {
  project = data.google_project.this.project_id
  role    = "roles/iam.serviceAccountTokenCreator"
  member  = "serviceAccount:${google_service_account.k8s_node.email}"

  condition {
    title       = local.iam_condition.title
    description = local.iam_condition.description
    expression  = local.iam_condition.expression
  }
}

resource "google_project_iam_custom_role" "crossplane" {
  project     = data.google_project.this.project_id
  role_id     = var.crossplane_iam.custom_role_name
  title       = "Ditto Crossplane"
  description = "Role for the Ditto management service's Crossplane"
  permissions = local.crossplane_role.permissions
}

resource "google_project_iam_custom_role" "crossplane_trust" {
  project     = data.google_project.this.project_id
  role_id     = var.crossplane_iam.crossplane_trust_role
  title       = "Ditto Crossplane IAM Trust"
  description = "Role for the Ditto management service's Crossplane"
  permissions = local.crossplane_trust_role.permissions
}

resource "google_project_iam_member" "crossplane_iam_binding" {
  project = data.google_project.this.project_id
  role    = google_project_iam_custom_role.crossplane.id
  member  = "serviceAccount:${local.crossplane_service_account_email}"

  condition {
    title       = local.iam_condition.title
    description = local.iam_condition.description
    expression  = local.iam_condition.expression
  }
}

resource "google_project_iam_member" "crossplane_iam_binding_limited" {
  project = data.google_project.this.project_id
  role    = google_project_iam_custom_role.crossplane_trust.id
  member  = "serviceAccount:${local.crossplane_service_account_email}"

  condition {
    title       = "Restricted IAM operations"
    description = "Limits Crossplane to only grant specific roles (Workload Identity, Storage, and Velero) to service accounts within this project"
    expression  = <<-EOT
      api.getAttribute('iam.googleapis.com/modifiedGrantsByRole', [])
        .hasOnly([
          'roles/iam.workloadIdentityUser',
          'roles/storage.objectAdmin',
          'roles/storage.admin',
          'roles/secretmanager.admin',
          'roles/secretmanager.secretAccessor',
          '${google_project_iam_custom_role.velero.id}'
        ])
    EOT
  }
}

# Velero custom IAM role for backup/restore operations
# Note: Service account and IAM binding are created per-cluster by Crossplane
resource "google_project_iam_custom_role" "velero" {
  project     = data.google_project.this.project_id
  role_id     = var.velero_iam.custom_role_name
  title       = "Ditto Velero"
  description = "Role for Velero backup and restore operations with restricted bucket access"
  permissions = local.velero_role.permissions
}

module "vpc" {
  source  = "terraform-google-modules/network/google"
  version = "~> 9.0"

  project_id   = var.project_id
  network_name = var.vpc_config.vpc_name
  description  = var.vpc_config.vpc_description
  routing_mode = var.vpc_config.routing_mode

  auto_create_subnetworks = false

  subnets = var.vpc_config.create_subnets ? [
    {
      subnet_name           = var.vpc_config.subnet_name
      subnet_ip             = var.vpc_config.subnet_cidr
      subnet_region         = var.region
      description           = var.vpc_config.subnet_description
      subnet_private_access = var.vpc_config.private_google_access ? "true" : "false"
      subnet_flow_logs      = "false"
    }
  ] : []

  secondary_ranges = var.vpc_config.create_subnets ? {
    (var.vpc_config.subnet_name) = [
      {
        range_name    = var.vpc_config.pods_secondary_range_name
        ip_cidr_range = var.vpc_config.pods_cidr_range
      },
      {
        range_name    = var.vpc_config.services_secondary_range_name
        ip_cidr_range = var.vpc_config.services_cidr_range
      }
    ]
  } : {}

  routes = []

  depends_on = [module.project-services]
}

# Create firewall rules separately when enabled
module "firewall_rules" {
  count = var.vpc_config.create_default_firewall_rules ? 1 : 0
  source       = "terraform-google-modules/network/google//modules/firewall-rules"
  version      = "~> 9.0"
  project_id   = var.project_id
  network_name = module.vpc.network_name

  rules = [
    {
      name        = "${var.vpc_config.vpc_name}-allow-internal"
      direction   = "INGRESS"
      priority    = 1000
      description = "Allow internal communication within VPC and secondary ranges"
      ranges = [
        var.vpc_config.subnet_cidr,
        var.vpc_config.pods_cidr_range,
        var.vpc_config.services_cidr_range
      ]
      allow = [
        {
          protocol = "tcp"
          ports    = ["0-65535"]
        },
        {
          protocol = "udp"
          ports    = ["0-65535"]
        },
        {
          protocol = "icmp"
          ports    = []
        }
      ]
      deny = []
    },
    {
      name        = "${var.vpc_config.vpc_name}-allow-ssh"
      direction   = "INGRESS"
      priority    = 1000
      description = "Allow SSH access to instances with ssh-access tag"
      ranges      = ["0.0.0.0/0"]
      target_tags = ["ssh-access"]
      allow = [
        {
          protocol = "tcp"
          ports    = ["22"]
        }
      ]
      deny = []
    },
    {
      name        = "${var.vpc_config.vpc_name}-allow-https"
      direction   = "INGRESS"
      priority    = 1000
      description = "Allow HTTP/HTTPS access for web services"
      ranges      = ["0.0.0.0/0"]
      target_tags = ["web-access"]
      allow = [
        {
          protocol = "tcp"
          ports    = ["443", "80"]
        }
      ]
      deny = []
    },
    {
      name        = "${var.vpc_config.vpc_name}-allow-k8s-api"
      direction   = "INGRESS"
      priority    = 1000
      description = "Allow access to Kubernetes API server"
      ranges      = ["0.0.0.0/0"]
      target_tags = ["k8s-control-plane"]
      allow = [
        {
          protocol = "tcp"
          ports    = ["6443"]
        }
      ]
      deny = []
    }
  ]
}
