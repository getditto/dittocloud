# Output the important values
output "client_id" {
  value = azurerm_user_assigned_identity.this.client_id
}

output "principal_id" {
  value = azurerm_user_assigned_identity.this.principal_id
}

output "tenant_id" {
  value = azurerm_user_assigned_identity.this.tenant_id
}

output "identity_id" {
  value = azurerm_user_assigned_identity.this.id
}

output "env_vars" {
  value = <<EOF
    export AZURE_CLIENT_ID_USER_ASSIGNED_IDENTITY=${azurerm_user_assigned_identity.this.client_id}
    export AZURE_TENANT_ID=${azurerm_user_assigned_identity.this.tenant_id}
    export AZURE_CLIENT_ID=${azurerm_user_assigned_identity.this.client_id}
EOF
}