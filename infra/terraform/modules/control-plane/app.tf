# The control plane itself, and the job that makes its database usable.
#
# Container Apps rather than AKS, deliberately. The control plane is one web
# process and a Postgres. The cheapest always-on AKS control plane is about 75
# USD a month before a single node runs; this whole stack is roughly 30 to 45.
# A cluster buys nothing here. The Helm chart in deploy/helm covers anyone who
# wants it on Kubernetes anyway, and it installs on any conformant cluster.

resource "azurerm_container_app_environment" "this" {
  name                       = "${var.name}-env"
  location                   = var.location
  resource_group_name        = var.resource_group_name
  infrastructure_subnet_id   = azurerm_subnet.apps.id
  log_analytics_workspace_id = var.log_analytics_id

  # The environment keeps a public ingress for the app; the database stays
  # private because it is on its own delegated subnet with no public endpoint.
  internal_load_balancer_enabled = false

  # DECLARED BECAUSE AZURE CREATES IT, NOT BECAUSE THIS MODULE ASKED FOR IT.
  #
  # Azure attaches a default Consumption workload profile to every managed
  # environment. Terraform did not create it, so the next plan proposes to
  # REMOVE it, quietly, as a `- workload_profile` block inside an otherwise
  # uninteresting in-place update. Left undeclared, every apply forever tries to
  # delete a profile the platform immediately puts back, so the stack never
  # converges and every plan carries a change that is not a change. The same
  # thing happens on the database subnet's Microsoft.Storage service endpoint,
  # and it is declared there for the same reason.
  #
  # A plan that always shows a diff is a plan people stop reading, which is how
  # a real destroy goes past a reviewer.
  workload_profile {
    name                  = "Consumption"
    workload_profile_type = "Consumption"
    maximum_count         = 0
    minimum_count         = 0
  }

  tags = var.tags
}

locals {
  secret_names = ["database-url", "migration-database-url", "github-client-id", "github-client-secret", "github-redirect-uri"]

  image = var.image_digest != "" ? "${var.image_repository}@${var.image_digest}" : "${var.image_repository}:${var.image_tag}"
}

# ---------------------------------------------------------------------------
# The bootstrap job.
#
# Applies the schema and grants the application role its membership in
# antifailure_app. Nothing else does this: the migrations create that group role
# NOLOGIN and cannot know what the operator named their login role, and the
# application connects as the unprivileged role and could not grant itself
# anything.
#
# Without it a fresh install migrates cleanly, starts, answers /health with 200,
# and cannot read a single table, because a role with no USAGE on the schema is
# told the relation does not exist rather than that it lacks permission.
#
# It runs INSIDE the VNet, which is the other reason it is a job here rather
# than a postgresql provider block: the server has no public endpoint and
# Terraform, running on a laptop or in CI, cannot reach it.
# ---------------------------------------------------------------------------
resource "azurerm_container_app_job" "bootstrap" {
  name                         = "${var.name}-bootstrap"
  location                     = var.location
  resource_group_name          = var.resource_group_name
  container_app_environment_id = azurerm_container_app_environment.this.id

  replica_timeout_in_seconds = 600
  replica_retry_limit        = 2
  manual_trigger_config {
    parallelism              = 1
    replica_completion_count = 1
  }

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.app.id]
  }

  dynamic "secret" {
    for_each = toset(["database-url", "migration-database-url"])
    content {
      name                = secret.value
      identity            = azurerm_user_assigned_identity.app.id
      key_vault_secret_id = azurerm_key_vault_secret.this[secret.value].versionless_id
    }
  }

  template {
    container {
      name    = "bootstrap"
      image   = local.image
      cpu     = 0.5
      memory  = "1Gi"
      command = ["node", "bootstrap.mjs"]

      env {
        name        = "AF_DATABASE_URL"
        secret_name = "database-url"
      }
      env {
        name        = "AF_MIGRATION_DATABASE_URL"
        secret_name = "migration-database-url"
      }
    }
  }

  tags = var.tags

  depends_on = [
    azurerm_role_assignment.app_reads_secrets,
    azurerm_postgresql_flexible_server_database.this,
  ]
}

# ---------------------------------------------------------------------------
# Partition maintenance.
#
# The events table is partitioned by month and a range-partitioned table with no
# partition for an incoming row does not slow down, it fails. Creating them
# ahead is DDL, so it needs the privileged role, and that is exactly why it is
# here and not in the app: otherwise every replica serving public traffic would
# be holding a credential that can ALTER TABLE.
# ---------------------------------------------------------------------------
resource "azurerm_container_app_job" "maintenance" {
  name                         = "${var.name}-maintenance"
  location                     = var.location
  resource_group_name          = var.resource_group_name
  container_app_environment_id = azurerm_container_app_environment.this.id

  replica_timeout_in_seconds = 900
  replica_retry_limit        = 2
  schedule_trigger_config {
    cron_expression          = var.maintenance_cron
    parallelism              = 1
    replica_completion_count = 1
  }

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.app.id]
  }

  secret {
    name                = "migration-database-url"
    identity            = azurerm_user_assigned_identity.app.id
    key_vault_secret_id = azurerm_key_vault_secret.this["migration-database-url"].versionless_id
  }

  template {
    container {
      name    = "maintenance"
      image   = local.image
      cpu     = 0.5
      memory  = "1Gi"
      command = ["node", "maintenance.mjs"]

      env {
        name        = "AF_MAINTENANCE_DATABASE_URL"
        secret_name = "migration-database-url"
      }
      dynamic "env" {
        for_each = var.event_retention_months == null ? [] : [var.event_retention_months]
        content {
          name  = "AF_EVENT_RETENTION_MONTHS"
          value = tostring(env.value)
        }
      }
    }
  }

  tags = var.tags

  depends_on = [azurerm_role_assignment.app_reads_secrets]
}

# ---------------------------------------------------------------------------
# The application.
#
# Note what is NOT in its environment: AF_MIGRATION_DATABASE_URL and
# AF_MAINTENANCE_DATABASE_URL. The serving process holds no credential that can
# run DDL. AF_MIGRATE is absent for the same reason, and because migrations
# racing across replicas at startup is a worse way to apply a schema than a job
# that runs once.
# ---------------------------------------------------------------------------
resource "azurerm_container_app" "this" {
  name                         = "${var.name}-app"
  resource_group_name          = var.resource_group_name
  container_app_environment_id = azurerm_container_app_environment.this.id
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.app.id]
  }

  dynamic "secret" {
    for_each = toset(["database-url", "github-client-id", "github-client-secret", "github-redirect-uri"])
    content {
      name                = secret.value
      identity            = azurerm_user_assigned_identity.app.id
      key_vault_secret_id = azurerm_key_vault_secret.this[secret.value].versionless_id
    }
  }

  ingress {
    external_enabled = true
    target_port      = 8080
    transport        = "auto"
    # TLS terminates at the ingress and Azure will not serve this without it.
    # An installation that set AF_INSECURE_COOKIES here would be sending session
    # cookies without the Secure attribute over a public route, so it is not
    # configurable from this module at all.
    allow_insecure_connections = false

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    min_replicas = var.min_replicas
    max_replicas = var.max_replicas

    container {
      name   = "control-plane"
      image  = local.image
      cpu    = var.app_cpu
      memory = var.app_memory

      env {
        name        = "AF_DATABASE_URL"
        secret_name = "database-url"
      }
      env {
        name        = "AF_GITHUB_CLIENT_ID"
        secret_name = "github-client-id"
      }
      env {
        name        = "AF_GITHUB_CLIENT_SECRET"
        secret_name = "github-client-secret"
      }
      env {
        name        = "AF_GITHUB_REDIRECT_URI"
        secret_name = "github-redirect-uri"
      }
      env {
        name  = "AF_PORT"
        value = "8080"
      }
      env {
        name  = "AF_POOL_MAX"
        value = tostring(var.pool_max)
      }
      dynamic "env" {
        for_each = var.app_base_url == "" ? [] : [var.app_base_url]
        content {
          name  = "AF_APP_BASE_URL"
          value = env.value
        }
      }

      # Liveness only, and only because /health is a static literal in the
      # application that never touches the database. It answers "is the process
      # up" and is not allowed to imply more, so readiness is a TCP check.
      liveness_probe {
        transport               = "HTTP"
        port                    = 8080
        path                    = "/health"
        initial_delay           = 10
        interval_seconds        = 30
        failure_count_threshold = 3
      }

      readiness_probe {
        transport               = "TCP"
        port                    = 8080
        interval_seconds        = 10
        failure_count_threshold = 3
      }
    }
  }

  tags = var.tags

  # The application must not start before its database is usable. Without this
  # the first revision comes up against a schema that does not exist yet and
  # against a role with no grants, which is a CrashLoop whose cause is an
  # ordering rather than a bug.
  depends_on = [azurerm_container_app_job.bootstrap]
}
