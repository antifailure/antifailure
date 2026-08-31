# What the application itself can tell you, which is less than it looks.
#
# These three rules read Container Apps platform metrics. The metric names and
# their dimensions were read from the running staging app rather than from
# memory:
#
#   az monitor metrics list-definitions --resource <container app id>
#
# Requests carries revisionName, podName, statusCodeCategory and statusCode.
# RestartCount and Replicas carry revisionName and podName. Nothing else about
# the application is visible from here: the counters at /metrics are the
# process's own and Azure Monitor cannot see them.

resource "azurerm_monitor_metric_alert" "server_errors" {
  name                = "${var.name}-server-errors"
  resource_group_name = var.resource_group_name
  scopes              = [var.container_app_id]
  severity            = 1

  description = "More than ${var.http_5xx_threshold} server errors in five minutes. Runbook: ${local.runbooks}/server-errors/"

  window_size = "PT5M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/containerApps"
    metric_name      = "Requests"
    aggregation      = "Total"
    operator         = "GreaterThan"
    threshold        = var.http_5xx_threshold

    # The dimension is what makes this a 5xx alert rather than a traffic alert.
    # Without it the rule counts every request and fires on a busy afternoon.
    dimension {
      name     = "statusCodeCategory"
      operator = "Include"
      values   = ["5xx"]
    }
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}

# A restart is not a failure. A loop is.
#
# The liveness probe restarts a container that stops answering /health, which is
# the probe working. What is worth waking up for is a container that keeps
# failing to start: a bad image, a missing secret, a database it cannot reach at
# boot. Maximum rather than Total, so one replica restarting three times fires
# and three replicas restarting once each does not.
resource "azurerm_monitor_metric_alert" "restart_loop" {
  name                = "${var.name}-restart-loop"
  resource_group_name = var.resource_group_name
  scopes              = [var.container_app_id]
  severity            = 1

  description = "A replica restarted more than ${var.restart_threshold} times in fifteen minutes. Runbook: ${local.runbooks}/revision-health/"

  window_size = "PT15M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/containerApps"
    metric_name      = "RestartCount"
    aggregation      = "Maximum"
    operator         = "GreaterThan"
    threshold        = var.restart_threshold
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}

# The revision is not running what it was told to run.
#
# THIS ONE ONLY MEANS ANYTHING WITH MORE THAN ONE REPLICA, which is why
# production sets min_replicas to 2 and staging does not run this module at all.
# On a single replica app, "fewer replicas than configured" and "the app is
# down" are the same event and the availability probe already says so, louder.
#
# Minimum rather than Average: a fifteen minute average of 2 and 1 is 1.5 and
# would need a threshold nobody can explain. The minimum is the number of
# replicas that were actually up at the worst moment in the window.
resource "azurerm_monitor_metric_alert" "replicas_below_minimum" {
  name                = "${var.name}-replicas-below-minimum"
  resource_group_name = var.resource_group_name
  scopes              = [var.container_app_id]
  severity            = 2

  description = "Fewer than ${var.min_replicas} replicas were running. Runbook: ${local.runbooks}/revision-health/"

  window_size = "PT15M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/containerApps"
    metric_name      = "Replicas"
    aggregation      = "Minimum"
    operator         = "LessThan"
    threshold        = var.min_replicas
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}
