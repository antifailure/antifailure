# The database, which is the thing that takes the whole control plane with it.
#
# Metric names read from the running server rather than remembered:
#
#   az monitor metrics list-definitions --resource <flexible server id>
#
# storage_percent, cpu_percent, active_connections, max_connections and
# is_db_alive are all real and all reported by this SKU.

locals {
  # max_connections is a SERVER PARAMETER derived from the SKU's memory, and
  # Azure does not expose a "percent of connections used" metric. A metric alert
  # cannot divide active_connections by max_connections either, because each
  # criteria block reads exactly one series. So the eighty percent is computed
  # here and the denominator has to be known at plan time.
  #
  # 50 for B1ms is not from a table, it was read from the running staging server:
  #
  #   az postgres flexible-server parameter show -g <group> -s <server> \
  #     -n max_connections
  #   {"allowed": "25-5000", "default": "50", "value": "50"}
  #
  # 429 and 859 come from the same documented series (4 GiB and 8 GiB), and the
  # B1ms figure agreeing exactly with it is the reason to trust them. CONFIRM
  # THE PRODUCTION ONE with that command after the first apply: if the number is
  # wrong, this alert is quietly measuring the wrong fraction and nothing else
  # will ever say so.
  max_connections_by_sku = {
    B_Standard_B1ms     = 50
    B_Standard_B2s      = 429
    GP_Standard_D2ds_v4 = 859
  }

  max_connections    = lookup(local.max_connections_by_sku, var.database_sku, 0)
  connection_ceiling = floor(local.max_connections * var.connection_percent / 100)
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
resource "azurerm_monitor_metric_alert" "database_connections" {
  name                = "${var.name}-database-connections"
  resource_group_name = var.resource_group_name
  scopes              = [var.postgres_server_id]
  severity            = 2

  description = "More than ${local.connection_ceiling} active connections, which is ${var.connection_percent} percent of the ${local.max_connections} this SKU allows. Runbook: ${local.runbooks}/database-connections/"

  window_size = "PT15M"
  frequency   = "PT5M"

  criteria {
    metric_namespace = "Microsoft.DBforPostgreSQL/flexibleServers"
    metric_name      = "active_connections"
    aggregation      = "Average"
    operator         = "GreaterThan"
    threshold        = local.connection_ceiling
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags

  lifecycle {
    precondition {
      condition     = local.max_connections > 0
      error_message = "No max_connections is recorded for ${var.database_sku}, so this alert would compare active connections against a threshold of zero and fire forever. Read the real number with `az postgres flexible-server parameter show -n max_connections` and add it to max_connections_by_sku in modules/alerting/database.tf."
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
