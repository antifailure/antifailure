output "action_group_id" {
  value       = azurerm_monitor_action_group.pager.id
  description = "For anything else that needs to page the same people. Attach to it rather than creating a second group: two groups is two lists to keep in step, and the one that is wrong is always the one that was needed."
}

output "alert_names" {
  value = sort(concat(
    [
      azurerm_monitor_metric_alert.unreachable.name,
      azurerm_monitor_metric_alert.certificate_expiring.name,
      azurerm_monitor_metric_alert.server_errors.name,
      azurerm_monitor_metric_alert.restart_loop.name,
      azurerm_monitor_metric_alert.replicas_below_minimum.name,
      azurerm_monitor_metric_alert.database_storage.name,
      azurerm_monitor_metric_alert.database_connections.name,
      azurerm_monitor_metric_alert.database_cpu.name,
      azurerm_monitor_metric_alert.database_unreachable.name,
    ],
    [for a in azurerm_monitor_metric_alert.job_failed : a.name],
  ))
  description = "Every rule this module created, so `terraform output` answers what is watched without a portal."
}

# Deliberately not output: the addresses and the phone number. They are inputs
# marked sensitive for a reason, and an output would print them in a plan
# summary that is world readable on a public repository.
