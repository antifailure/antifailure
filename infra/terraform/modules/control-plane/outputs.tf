output "app_url" {
  value       = "https://${azurerm_container_app.this.ingress[0].fqdn}"
  description = "The public origin. The GitHub OAuth App's callback URL must match github_redirect_uri exactly or sign in fails with a redirect_uri mismatch."
}

output "database_fqdn" {
  value       = azurerm_postgresql_flexible_server.this.fqdn
  description = "Resolvable from inside the VNet only. There is no public endpoint."
}

output "key_vault_name" { value = azurerm_key_vault.this.name }

# The scope for the one-time `az role assignment create` that gives an operator
# write access to the vault. self-hosting/azure.md names this command, and an
# output is what makes that instruction followable instead of an exercise in
# assembling a resource id by hand.
output "key_vault_id" { value = azurerm_key_vault.this.id }
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


# The ids the alerting module scopes its rules to.
#
# Ids rather than names, because azurerm_monitor_metric_alert takes a resource
# id in `scopes` and assembling one from a subscription, a group and a name in
# the caller is four chances to get a string wrong and no error until apply.
output "container_app_id" { value = azurerm_container_app.this.id }
output "postgres_server_id" { value = azurerm_postgresql_flexible_server.this.id }
output "job_ids" {
  value = {
    bootstrap   = azurerm_container_app_job.bootstrap.id
    maintenance = azurerm_container_app_job.maintenance.id
  }
  description = "Keyed by the short name that ends up in the alert rule's name, so a page says which job failed."
}

output "custom_domain_verification_id" {
  value       = azurerm_container_app.this.custom_domain_verification_id
  description = "The value the asuid TXT record carries. Output so that binding a name in a zone this stack does not own is a copy rather than a portal visit."
}

# What the application actually gets from the database, for the alerting module.
#
# An output rather than a second copy of the SKU table in modules/alerting: the
# alert's threshold and the app's own ceiling have to come from the SAME number.
# When they did not, the connection alert on a B1ms was set at eighty percent of
# max_connections, which is 40, and the server had already started refusing the
# application at 35. The rule could not fire before the outage it exists to
# predict. See the derivation and the measurements in database.tf.
output "usable_connections" {
  value       = local.usable_connections
  description = "max_connections for this SKU less reserved_connections and superuser_reserved_connections: what a role without pg_use_reserved_connections may actually open. Zero means the SKU is not in the table in database.tf."
}
