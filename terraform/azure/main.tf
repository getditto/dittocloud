# Create resource group
resource "azurerm_resource_group" "this" {
  name     = var.resource_group_name
  location = var.location
}

# Create user-assigned managed identity
resource "azurerm_user_assigned_identity" "this" {
  name                = var.identity_name
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
}

# Assign custom Ditto Management Plane role at resource group level
resource "azurerm_role_assignment" "contributor" {
  scope                = "/subscriptions/${var.subscription_id}/resourceGroups/${var.resource_group_name}"
  role_definition_name = "Contributor"
  principal_id         = azurerm_user_assigned_identity.this.principal_id
}

# Federated identity credential for azureserviceoperator
resource "azurerm_federated_identity_credential" "cluster_identity" {
  name                = "ditto-cluster-identity-secret"
  resource_group_name = azurerm_resource_group.this.name
  parent_id           = azurerm_user_assigned_identity.this.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = var.issuer_url
  subject             = "system:serviceaccount:capz-system:azureserviceoperator-default"
}

# Federated identity credential for capz-manager
resource "azurerm_federated_identity_credential" "capz_manager" {
  name                = "ditto-capz-manager-secret"
  resource_group_name = azurerm_resource_group.this.name
  parent_id           = azurerm_user_assigned_identity.this.id
  audience            = ["api://AzureADTokenExchange"]
  issuer              = var.issuer_url
  subject             = "system:serviceaccount:capz-system:capz-manager"
}