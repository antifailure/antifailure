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

# A service that answers everything slowly, and correctly, pages nobody.
#
# THE FAILURE. The three rules above read failures: a 5xx, a restart, a missing
# replica. A saturated replica set, a slow query on the hot path or a blocked
# connection pool produces none of those. Every request completes, every status
# is 200, and every one of them takes twelve seconds. The availability test has
# a thirty second timeout and stays green. That is the likeliest degradation on
# the first busy day and the one a customer notices first, and until this rule
# nothing here could see it.
#
# ResponseTime is the metric, read from the running production app rather than
# from memory. It is emitted by the Container Apps ingress in milliseconds,
# supports Average, Total, Maximum and Minimum, and carries exactly two
# dimensions, statusCodeCategory and statusCode. There is no path or route
# dimension, so the health probes cannot be filtered out of it. That matters
# less than it sounds: the liveness and readiness probes are called by the
# platform against the container directly and never pass through the ingress,
# so they are not in this series at all. The only probe that is in it is the
# availability test's request to /readyz every five minutes from three
# locations, a fraction of a percent of the traffic, and one that should be
# counted, because a slow /readyz is a slow database.
#
# Average, not Maximum. The application's own statement timeout is fifteen
# seconds, so one request that waits on a lock can legitimately take that long,
# and a Maximum rule would fire on it every time. The average over the window
# is what the whole population of customers experienced.
#
# The window is fifteen minutes, evaluated every five, the shape the restart
# and replica rules use. A static threshold in Azure Monitor cannot be told to
# wait for two consecutive violations; that knob, evaluation_failure_count,
# exists only on a dynamic threshold, and the provider refuses a ten minute
# window outright: PT5M and PT15M are the only sizes between one minute and
# half an hour. Fifteen at the 5xx rule's cadence is the nearest equivalent
# to a second look: one slow five minutes is diluted by the ten before it, and
# a stall that is still going at the next evaluation is not.
#
# The threshold is measured, not guessed, and the measurement is in the tfvars
# next to the value.
resource "azurerm_monitor_metric_alert" "slow_responses" {
  name                = "${var.name}-slow-responses"
  resource_group_name = var.resource_group_name
  scopes              = [var.container_app_id]
  severity            = 1

  description = "The average response time across every request was above ${var.response_time_threshold_ms} ms for fifteen minutes. Runbook: ${local.runbooks}/slow-responses/"

  window_size = "PT15M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.App/containerApps"
    metric_name      = "ResponseTime"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.response_time_threshold_ms
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}
