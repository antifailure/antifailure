# A job that failed and told nobody.
#
# The bootstrap job applies migrations. Today a failed migration fails the
# deploy loudly, because continuous deployment starts the job and waits for it.
# Nothing else in this system does: an operator running it by hand after an
# image upgrade, or the maintenance job at 03:17 that keeps the event partitions
# ahead, fails into silence. A range partitioned table with no partition for an
# incoming row does not slow down, it refuses the insert.
#
# The dimension was read from the running maintenance job rather than assumed:
#
#   az monitor metrics list --metric Executions --filter "state eq '*'"
#   [('state', 'Succeeded')] total 12
#   [('state', 'Running')]   total 2
#
# so the dimension is named `state`, its values are capitalised words, and
# Failed is the third of them.
resource "azurerm_monitor_metric_alert" "job_failed" {
  for_each = var.job_ids

  name                = "${var.name}-${each.key}-job-failed"
  resource_group_name = var.resource_group_name
  scopes              = [each.value]
  severity            = 1

  description = "The ${each.key} job reported a failed execution. Runbook: ${local.runbooks}/job-failed/"

  # One failure is the whole signal. These jobs run once and either work or do
  # not, so there is no blip to filter out and no reason to wait for a second.
  window_size = "PT5M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/jobs"
    metric_name      = "Executions"
    aggregation      = "Total"
    operator         = "GreaterThan"
    threshold        = 0

    dimension {
      name     = "state"
      operator = "Include"
      values   = ["Failed"]
    }
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}
