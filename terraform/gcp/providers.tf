terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.35.0"
    }
  }
  required_version = ">= 1.11.0"
}

provider "google" {
  project = var.project_id
  region  = var.region
}

module "project-services" {
  source                      = "terraform-google-modules/project-factory/google//modules/project_services"
  version                     = "~> 18.0"
  project_id                  = var.project_id
  disable_services_on_destroy = false

  activate_apis = [
    "compute.googleapis.com",
    "iam.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "secretmanager.googleapis.com",
    "storage.googleapis.com",
    "monitoring.googleapis.com",
    "logging.googleapis.com",
  ]
}

resource "time_sleep" "wait_for_project_services" {
  depends_on = [module.project-services]

  create_duration = "30s"
}
