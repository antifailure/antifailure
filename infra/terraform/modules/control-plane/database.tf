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

# The operator portal's password, on the SAME footing as the two above.
#
# Generated here rather than typed, so no person and no workflow ever holds the
# credential that reads across every tenant. It goes from the random provider
# into Key Vault, into the bootstrap job which gives `antifailure_admin` a login
# with it, and into the application's environment. The only copy outside the
# vault is in Terraform state, which lives in a storage account with key access
# disabled.
#
# THE ROLE IS NOT CREATED HERE, for the reason stated at the top of this file:
# Terraform cannot reach this server. Migration 0023 creates `antifailure_admin`
# NOLOGIN with BYPASSRLS and 0029, 0030 and 0031 grant it what the portal reads;
# the bootstrap job, running inside the VNet from the same image, is what turns
# it into an account that can log in.
resource "random_password" "operator" {
  count            = var.operator_portal_enabled ? 1 : 0
  length           = 32
  special          = true
  override_special = "-_"
}

locals {
  # Burstable SKUs cannot do zone-redundant high availability. Caught here as a
  # precondition rather than discovered as a failed apply twenty minutes in.
  sku_is_burstable = startswith(var.database_sku, "B_")

  # HOW MANY CONNECTIONS THE APPLICATION ACTUALLY GETS.
  #
  # This lives here, next to the server, because this module is the only one
  # that owns database_sku. The alerting module reads it through an output
  # rather than keeping a second copy: two tables of the same numbers is how
  # one of them silently goes stale, and the alert's threshold and the app's
  # ceiling have to be derived from the SAME number or the alert is measuring
  # a fraction of something the application is not limited by.
  #
  # max_connections is a server parameter Azure derives from the SKU's memory.
  # Read from the running servers rather than remembered:
  #
  #   az postgres flexible-server parameter show -g <group> -s <server> \
  #     -n max_connections
  #
  # B1ms answered 50 on afcp-pg and D2ds_v4 answered 859 on afcpprod-pg; B2s is
  # 429 from the same documented series, which the two measured values agree
  # with. CONFIRM A NEW ONE with that command before adding it here.
  max_connections_by_sku = {
    B_Standard_B1ms     = 50
    B_Standard_B2s      = 429
    GP_Standard_D2ds_v4 = 859
  }

  # THE SUBTRACTION THAT WAS MISSING, and it is the whole reason staging fell
  # over rather than a rounding detail.
  #
  # Postgres refuses an ordinary role once the free slots drop to
  # reserved_connections + superuser_reserved_connections, not at
  # max_connections. On a B1ms those are 5 and 10, so the application gets 35 of
  # the 50, and a ceiling computed as a fraction of 50 can sit ABOVE the level
  # at which the server has already started refusing it. That is exactly what
  # the connection alert did: eighty percent of 50 is 40, and 40 > 35, so the
  # rule could not fire before the outage it exists to predict.
  #
  # Both defaults are readOnly on a flexible server and Azure reports them as 5
  # and 10 on every SKU in the allowlist, which is why they are constants here
  # rather than variables somebody has to set.
  reserved_connections           = 5
  superuser_reserved_connections = 10

  # Zero for a SKU that is not in the table above, and zero is load-bearing: it
  # is what the alerting module's precondition tests, so an unknown SKU fails a
  # plan with a message naming the fix rather than quietly setting a threshold
  # of nothing.
  sku_max_connections = lookup(local.max_connections_by_sku, var.database_sku, 0)
  usable_connections = (
    local.sku_max_connections == 0
    ? 0
    : local.sku_max_connections - (local.reserved_connections + local.superuser_reserved_connections)
  )

  # What is not the application: the bootstrap job's migration connection, the
  # maintenance job's, a break-glass session and a backup or restore run. Each
  # opens with max: 1 and all four can overlap during a release. The same
  # allowance is spelled out in deploy/cd/deploy.sh, which checks the measured
  # version of this arithmetic after every deploy.
  tool_connections = 4

  # THE ARITHMETIC, WRITTEN DOWN.
  #
  # The serving revision can scale to max_replicas. One superseded revision is
  # kept active as the rollback target and sits at min_replicas. Every one of
  # those replicas is a process holding a pool of pool_max. Nothing anywhere
  # else in this stack multiplies these three numbers together, which is how
  # forty six of them accumulated on staging without anybody noticing.
  #
  # THE OPERATOR POOL IS PART OF THE SAME SUM. createAdminPool runs inside the
  # serving process, so every replica holding a pool of pool_max also holds one
  # of admin_pool_max the moment the portal is switched on. Leaving it out would
  # make this precondition pass on a shape the database cannot actually serve,
  # which is the exact failure the whole block exists to catch: staging ran for
  # weeks on a shape that only fit because the app never scaled.
  per_replica_connections = var.pool_max + (var.operator_portal_enabled ? var.admin_pool_max : 0)
  peak_app_connections    = (var.max_replicas + var.min_replicas) * local.per_replica_connections
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

  # The operator role's name is fixed by the migrations rather than by a
  # variable, and that is deliberate. A GRANT cannot name a role that does not
  # exist, so 0029, 0030 and 0031 hand their privileges to `antifailure_admin`
  # by name; an installation that pointed this at some other role would get a
  # credential that connects, holds no privileges, and reads nothing. The
  # bootstrap job refuses that case by name rather than letting it deploy.
  operator_role = "antifailure_admin"

  operator_url = var.operator_portal_enabled ? format(
    "postgres://%s:%s@%s:5432/%s?sslmode=require",
    local.operator_role,
    urlencode(random_password.operator[0].result),
    local.pg_host,
    var.database_name,
  ) : ""
}
