# The database, which is the thing that takes the whole control plane with it.
#
# Metric names read from the running server rather than remembered:
#
#   az monitor metrics list-definitions --resource <flexible server id>
#
# storage_percent, cpu_percent, active_connections, max_connections and
# is_db_alive are all real and all reported by this SKU.

locals {
  # Azure does not expose a "percent of connections used" metric, and a metric
  # alert cannot divide active_connections by anything, because each criteria
  # block reads exactly one series. So the eighty percent is computed here and
  # the denominator has to be known at plan time.
  #
  # THE DENOMINATOR IS NOT max_connections, and it used to be. Postgres refuses
  # an ordinary role once the free slots fall to reserved_connections plus
  # superuser_reserved_connections. On a B1ms that is 5 and 10 against a
  # max_connections of 50, so the application gets 35, and this rule's old
  # ceiling of eighty percent of 50 was 40. Forty is ABOVE thirty five: the
  # alert could not fire until after the server had already started answering
  # /readyz with "remaining connection slots are reserved for roles with
  # privileges of the pg_use_reserved_connections role". A guard whose
  # threshold sits past the failure it guards is not a guard.
  #
  # The number comes from the control-plane module now rather than a second
  # table here, so the alert's threshold and the application's own ceiling are
  # derived from the same value. See modules/control-plane/database.tf for the
  # measurements and the az command that produced them.
  connection_ceiling = floor(var.usable_connections * var.connection_percent / 100)
}

resource "azurerm_monitor_metric_alert" "database_storage" {
  name                = "${var.name}-database-storage"
  resource_group_name = var.resource_group_name
  scopes              = [var.postgres_server_id]
  severity            = 2

  description = "Database storage is above ${var.database_storage_percent} percent. Runbook: ${local.runbooks}/database-storage/"

  window_size = "PT15M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "storage_percent"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.database_storage_percent
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}

# Connections, against a ceiling this module had to compute.
#
# Severity 2 rather than 1 because a pool at eighty percent is a warning with
# hours in it, not an outage. What it usually means is a replica count that grew
# without pool_max shrinking, and the arithmetic is in the runbook.
#
# MAXIMUM, NOT AVERAGE, and that is the second half of the fix.
#
# Connection exhaustion here is a burst, not a plateau. Staging's own numbers
# over the hour it was refusing connections:
#
#   active_connections, PT1M, Maximum:  6..11 for four minutes, then 33..39
#   active_connections, PT5M, Average:  12
#
# Every replica runs the same five minute housekeeping sweep, so they all reach
# for the pool at once and let go again. An Average aggregation reports 12
# against a ceiling of 28 and stays green through every one of those spikes,
# which is to say it reports the four minutes when nothing was wrong. The
# number that takes the service down is the peak, so the peak is what this
# reads. A fifteen minute window still keeps a single unlucky minute from
# paging anybody, because the criterion is the maximum WITHIN the window.
resource "azurerm_monitor_metric_alert" "database_connections" {
  name                = "${var.name}-database-connections"
  resource_group_name = var.resource_group_name
  scopes              = [var.postgres_server_id]
  severity            = 2

  description = "Peaked above ${local.connection_ceiling} active connections, which is ${var.connection_percent} percent of the ${var.usable_connections} this server will hand an ordinary role. Runbook: ${local.runbooks}/database-connections/"

  window_size = "PT15M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "active_connections"
    aggregation      = "Maximum"
    operator         = "GreaterThan"
    threshold        = local.connection_ceiling
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags

  lifecycle {
    precondition {
      condition     = var.usable_connections > 0
      error_message = "usable_connections is zero, so this alert would compare active connections against a threshold of zero and fire forever. That means the database SKU is not in max_connections_by_sku in modules/control-plane/database.tf. Read the real number with `az postgres flexible-server parameter show -n max_connections` and add it there."
    }
  }
}

# SUSTAINED, which is what the window is for.
#
# A thirty minute window rather than five. Postgres pegs a core for half a
# minute during a vacuum or a large query and recovers, and a five minute window
# turns every one of those into a page. What matters is CPU that does not come
# back down.
resource "azurerm_monitor_metric_alert" "database_cpu" {
  name                = "${var.name}-database-cpu"
  resource_group_name = var.resource_group_name
  scopes              = [var.postgres_server_id]
  severity            = 3

  description = "Database CPU has averaged above ${var.database_cpu_percent} percent for thirty minutes. Runbook: ${local.runbooks}/database-cpu/"

  window_size = "PT30M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "cpu_percent"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = var.database_cpu_percent
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}

# The server is not answering at all.
#
# Not in the assessment's list and added anyway, because it is the one database
# signal that is unambiguous. Everything else here is a number crossing a line
# somebody chose; is_db_alive is Azure saying the server did not answer. Without
# it the first news of a dead database arrives as a wave of 5xx, and whoever
# reads that page starts by looking at the application.
resource "azurerm_monitor_metric_alert" "database_unreachable" {
  name                = "${var.name}-database-unreachable"
  resource_group_name = var.resource_group_name
  scopes              = [var.postgres_server_id]
  severity            = 0

  description = "The database did not answer. Runbook: ${local.runbooks}/database-unreachable/"

  window_size = "PT5M"
  frequency   = "PT1M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "is_db_alive"
    aggregation      = "Minimum"
    operator         = "LessThan"
    threshold        = 1
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}
