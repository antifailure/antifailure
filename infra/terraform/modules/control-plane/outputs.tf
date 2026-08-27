output "app_url" {
  value       = "https://${azurerm_container_app.this.ingress[0].fqdn}"
  description = "The public origin. The GitHub OAuth App's callback URL must match github_redirect_uri exactly or sign in fails with a redirect_uri mismatch."
}

output "database_fqdn" {
  value       = azurerm_postgresql_flexible_server.this.fqdn
  description = "Resolvable from inside the VNet only. There is no public endpoint."
}

output "key_vault_name" { value = azurerm_key_vault.this.name }
output "goldens_account" {
  value       = var.goldens_enabled ? azurerm_storage_account.goldens[0].name : null
  description = "Null unless goldens_enabled. Nothing in the control plane reads blob storage yet."
}
output "identity_client_id" { value = azurerm_user_assigned_identity.app.client_id }

output "bootstrap_job_name" {
  value       = azurerm_container_app_job.bootstrap.name
  description = "Run it after an image upgrade that carries new migrations: az containerapp job start -n <this> -g <group>"
}

# Deliberately not output: any connection string. They are in Key Vault, and a
# `terraform output` that prints a database password to a terminal is a
# password in a shell history and in a CI log.
