output "resource_group_name" { value = azurerm_resource_group.this.name }
output "resource_group_id" { value = azurerm_resource_group.this.id }
output "location" { value = azurerm_resource_group.this.location }
output "tags" { value = local.tags }
output "log_analytics_id" {
  value       = var.log_analytics_enabled ? azurerm_log_analytics_workspace.this[0].id : null
  description = "Null when log_analytics_enabled is false, so a caller wiring diagnostics gets an error rather than an empty string."
}
