locals {
  ditto_mangement_plane_role = {
    name        = "dittoMangementPlane"
    description = "Role for the Ditto Management Plane"
    permissions = [
      "artifactregistry.repositories.downloadArtifacts",
      "artifactregistry.repositories.uploadArtifacts",
      "compute.addresses.list",
      "compute.backendServices.create",
      "compute.backendServices.delete",
      "compute.backendServices.get",
      "compute.backendServices.use",
      "compute.backendServices.getEffectiveSecurityPolicies",
      "compute.backendServices.list",
      "compute.backendServices.update",
      "compute.disks.create",
      "compute.firewalls.create",
      "compute.firewalls.delete",
      "compute.firewalls.get",
      "compute.globalAddresses.create",
      "compute.globalAddresses.delete",
      "compute.globalAddresses.get",
      "compute.globalAddresses.use",
      "compute.globalForwardingRules.create",
      "compute.globalForwardingRules.delete",
      "compute.globalForwardingRules.get",
      "compute.globalOperations.get",
      "compute.healthChecks.create",
      "compute.healthChecks.delete",
      "compute.healthChecks.get",
      "compute.healthChecks.useReadOnly",
      "compute.images.useReadOnly",
      "compute.instanceGroups.create",
      "compute.instanceGroups.delete",
      "compute.instanceGroups.get",
      "compute.instanceGroups.update",
      "compute.instanceGroups.use",
      "compute.instances.create",
      "compute.instances.delete",
      "compute.instances.get",
      "compute.instances.list",
      "compute.instances.setLabels",
      "compute.instances.setMetadata",
      "compute.instances.setServiceAccount",
      "compute.instances.setTags",
      "compute.instances.use",
      "compute.networks.create",
      "compute.networks.delete",
      "compute.networks.get",
      "compute.networks.updatePolicy",
      "compute.projects.get",
      "compute.regionOperations.get",
      "compute.regions.get",
      "compute.routers.create",
      "compute.routers.delete",
      "compute.routers.get",
      "compute.subnetworks.create",
      "compute.subnetworks.delete",
      "compute.subnetworks.get",
      "compute.subnetworks.use",
      "compute.targetTcpProxies.create",
      "compute.targetTcpProxies.delete",
      "compute.targetTcpProxies.get",
      "compute.targetTcpProxies.use",
      "compute.targetPools.setSecurityPolicy",
      "compute.zoneOperations.get",
      "compute.zones.list",
      "iam.serviceAccounts.actAs",
      "resourcemanager.projects.get",
      "storage.buckets.create",
      "storage.buckets.delete",
      "storage.buckets.get",
      "storage.objects.create",
      "storage.objects.delete",
      "storage.objects.get",
      "storage.objects.list",
      "storage.objects.update",
      "iam.serviceAccounts.get",
      "iam.serviceAccounts.getAccessToken",
      "iam.serviceAccounts.getOpenIdToken",
      "iam.serviceAccounts.list",
      "iam.serviceAccounts.getIamPolicy",
      "iam.workloadIdentityPoolProviders.create",
      "iam.workloadIdentityPoolProviders.delete",
      "iam.workloadIdentityPoolProviders.get",
      "iam.workloadIdentityPoolProviders.list",
      "iam.workloadIdentityPoolProviders.undelete",
      "iam.workloadIdentityPoolProviders.update",
      "iam.workloadIdentityPools.create",
      "iam.workloadIdentityPools.delete",
      "iam.workloadIdentityPools.get",
      "iam.workloadIdentityPools.list",
      "iam.workloadIdentityPools.undelete",
      "iam.workloadIdentityPools.update",

      # https://cloud.google.com/armor/docs/configure-security-policies#iam-custom-perms
      "compute.securityPolicies.create",
      "compute.securityPolicies.delete",
      "compute.securityPolicies.get",
      "compute.securityPolicies.list",
      "compute.securityPolicies.use",
      "compute.securityPolicies.update",
      "compute.backendServices.setSecurityPolicy",
    ]
  }

  # The role doesn't provide permissions to manage IAM policies.
  # That is provided by the crossplane_trust_role, and constrained by conditions.
  crossplane_role = {
    name        = "crossplane"
    description = "Role for Crossplane"
    permissions = [
      # Service Account management
      "iam.serviceAccounts.create",
      "iam.serviceAccounts.delete",
      "iam.serviceAccounts.get",
      "iam.serviceAccounts.list",
      "iam.serviceAccounts.update",
      "iam.serviceAccounts.getAccessToken",
      "iam.serviceAccounts.getIamPolicy",

      # Required for IAM operations
      "iam.roles.get",
      "iam.roles.list",
      "iam.policybindings.get",
      "iam.policybindings.list",
      "resourcemanager.projects.createPolicyBinding",
      "resourcemanager.projects.deletePolicyBinding",
      "resourcemanager.projects.get",
      "resourcemanager.projects.getIamPolicy",
      "resourcemanager.projects.searchPolicyBindings",
      "resourcemanager.projects.updatePolicyBinding",

      # Required for GCS Bucket management
      "storage.buckets.create",
      "storage.buckets.delete",
      "storage.buckets.get",
      "storage.buckets.getIamPolicy",
      "storage.buckets.update",
      "storage.objects.create",
      "storage.objects.delete",
      "storage.objects.get",

      # GCS HMAC Key management for Ditto Attachments
      "storage.hmacKeys.create",
      "storage.hmacKeys.delete",
      "storage.hmacKeys.get",
      "storage.hmacKeys.list",
      "storage.hmacKeys.update",
    ]
  }

  # The role provides permissions to manage IAM policies
  # but is limited to the crossplane service account and constrained by conditions.
  # See `google_project_iam_member.crossplane_iam_binding_limited`
  crossplane_trust_role = {
    name        = "crossplane-trust"
    description = "Role for Crossplane"
    permissions = [
      # Service Account management
      "iam.serviceAccounts.setIamPolicy",

      # Google Cloud Storage IAM management
      "storage.buckets.setIamPolicy",

      # IAM policy management (limited by conditions)
      "resourcemanager.projects.setIamPolicy",
    ]
  }

  # Velero role for backup and restore operations with least privilege access
  velero_role = {
    name        = "velero"
    description = "Role for Velero backup and restore operations with restricted bucket access"
    permissions = [
      # Core compute permissions for disk snapshots
      "compute.disks.get",
      "compute.disks.create",
      "compute.disks.createSnapshot",
      "compute.projects.get",
      "compute.snapshots.get",
      "compute.snapshots.create",
      "compute.snapshots.useReadOnly",
      "compute.snapshots.delete",
      "compute.zones.get",

      # Storage permissions are also assigned via Bucket IAM Member policy
      # and limited by condition to the ditto-cluster bucket & velero prefix
      # "roles/storage.objectAdmin",

      # IAM permissions for signed URLs (required for velero backup logs/download)
      "iam.serviceAccounts.signBlob",
    ]
  }
}
