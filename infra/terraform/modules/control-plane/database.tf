# The control plane's Postgres.
#
# TWO ROLES, which is the point of this file.
#
# The administrator login created here is the OWNER: it runs migrations and owns
# the tables. The application never uses it. The application connects as a
# separate login role that is a member of `antifailure_app`, the NOLOGIN group
# role the migrations create, and which owns nothing.
#
# The reason is not tidiness. A role that can ALTER TABLE can drop the policies
# that isolate tenants, and the process exposed to the internet should not be
# able to do that. Proved rather than asserted: with the application role,
# ALTER TABLE ... DISABLE ROW LEVEL SECURITY and DROP POLICY are both refused
# with "must be owner", CREATE TABLE is refused, UPDATE and DELETE on
# audit_entries are refused, and a query with no tenant set returns nothing.
#
# The second role is NOT created here. Terraform cannot reach this server: it
# has no public endpoint, deliberately, and a plan running in CI is not inside
# the VNet. It is created by the bootstrap job in jobs.tf, which runs inside the
# network, from the same image as the application, in the only order that works.

resource "random_password" "admin" {
  length  = 32
  special = true
  # Azure rejects several of these in an administrator password and the failure
  # arrives as an unhelpful 400 at create time.
  override_special = "-_"
}

resource "random_password" "app" {
  length           = 32
  special          = true
  override_special = "-_"
}

locals {
  # Burstable SKUs cannot do zone-redundant high availability. Caught here as a
  # precondition rather than discovered as a failed apply twenty minutes in.
  sku_is_burstable = startswith(var.database_sku, "B_")
}

resource "azurerm_postgresql_flexible_server" "this" {
  name                = "${var.name}-pg"
  resource_group_name = var.resource_group_name
  location            = var.location
  version             = var.postgres_version

  administrator_login    = var.database_admin_user
  administrator_password = random_password.admin.result

  sku_name   = var.database_sku
  storage_mb = var.database_storage_mb

  # Point in time recovery. The window is the only thing standing between a bad
  # migration and a permanent loss, so it is not zero and it is not a default
  # nobody chose.
  backup_retention_days        = var.backup_retention_days
  geo_redundant_backup_enabled = var.geo_redundant_backup

  # Private only. There is no public endpoint and no firewall rule.
  delegated_subnet_id           = azurerm_subnet.database.id
  private_dns_zone_id           = azurerm_private_dns_zone.postgres.id
  public_network_access_enabled = false

  dynamic "high_availability" {
    for_each = var.high_availability ? [1] : []
    content {
      mode = "ZoneRedundant"
    }
  }

  tags = var.tags

  lifecycle {
    precondition {
      condition     = !(var.high_availability && local.sku_is_burstable)
      error_message = "high_availability requires a General Purpose or Memory Optimized SKU; ${var.database_sku} is burstable and Azure will refuse zone-redundant HA on it. Either set high_availability = false or choose a GP_ SKU, and note that a GP SKU costs several times a burstable one."
    }
    # The password is generated and stored in Key Vault. Changing it here would
    # silently break the running application, which reads the old one.
    #
    # The two zones are ignored for the same reason as each other: Azure picks
    # them and this configuration deliberately does not. `zone` was already here
    # for the primary. standby_availability_zone is the same value for the HA
    # replica, and without it every plan after the first apply reads
    #
    #   high_availability[0].standby_availability_zone: "2" -> null
    #
    # forever, because Azure assigned 2 and nothing here ever will. It is one
    # in-place change on a plan that should be empty, and a plan that is never
    # empty is one people stop reading, which is how a real change gets waved
    # through. Pinning a zone instead would be worse: it would tie the standby
    # to a zone that may not be the one Azure can actually place it in.
    ignore_changes = [administrator_password, zone, high_availability[0].standby_availability_zone]
  }

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]
}

# The extensions the schema needs, allow-listed.
#
# THIS IS WHY A FRESH INSTALL ON AZURE COULD NOT MIGRATE. Azure Database for
# PostgreSQL refuses CREATE EXTENSION for anything not named in the
# azure.extensions server parameter, and that parameter defaults to EMPTY.
# Migration 0001 opens with `CREATE EXTENSION IF NOT EXISTS pgcrypto`, so the
# very first statement of the very first migration was refused, the whole file
# rolled back, and the control plane came up serving /health with a database it
# had no schema in.
#
# It was invisible until it ran here because every other place the schema is
# proved uses a stock postgres image, where CREATE EXTENSION simply works: the
# kind cluster in control-plane-image.yml, the docker-compose in development,
# and every test in web/packages/db. A managed Postgres is not a Postgres with
# a different hostname, and this is the difference that bites first.
#
# Changing azure.extensions is dynamic and needs no restart.
resource "azurerm_postgresql_flexible_server_configuration" "extensions" {
  name      = "azure.extensions"
  server_id = azurerm_postgresql_flexible_server.this.id
  value     = join(",", var.database_extensions)
}

resource "azurerm_postgresql_flexible_server_database" "this" {
  name      = var.database_name
  server_id = azurerm_postgresql_flexible_server.this.id
  collation = "en_US.utf8"
  charset   = "UTF8"

  lifecycle {
    # Dropping the control plane's database because a name changed is not
    # something a plan should be allowed to do quietly.
    prevent_destroy = false
  }
}

# Sent to Log Analytics so that "what was the database doing at 03:00" has an
# answer that does not depend on somebody having been logged in at the time.
# count keys off a plain boolean and NOT off log_analytics_id. The workspace id
# is a resource attribute that is unknown until apply, and a count that depends
# on an unknown value makes the whole plan fail with "cannot be determined until
# apply". A plan in CI caught this; it would otherwise have been found by the
# first person to run an apply.
resource "azurerm_monitor_diagnostic_setting" "postgres" {
  count                      = var.diagnostics_enabled ? 1 : 0
  name                       = "${var.name}-pg-diag"
  target_resource_id         = azurerm_postgresql_flexible_server.this.id
  log_analytics_workspace_id = var.log_analytics_id

  enabled_log { category = "PostgreSQLLogs" }
  enabled_metric { category = "AllMetrics" }
}

locals {
  pg_host = azurerm_postgresql_flexible_server.this.fqdn

  # sslmode=require, always. Azure enforces TLS server side, but a client that
  # does not ask for it will happily be downgraded by anything in between.
  migration_url = format(
    "postgres://%s:%s@%s:5432/%s?sslmode=require",
    var.database_admin_user,
    urlencode(random_password.admin.result),
    local.pg_host,
    var.database_name,
  )

  app_url = format(
    "postgres://%s:%s@%s:5432/%s?sslmode=require",
    var.database_app_user,
    urlencode(random_password.app.result),
    local.pg_host,
    var.database_name,
  )
}
