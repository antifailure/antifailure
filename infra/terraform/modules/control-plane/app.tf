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
  # Resolved once, because three resources pull it and three copies of this
  # expression is three chances for the bootstrap job to migrate a database for
  # a build the application is not running.
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

  # Named because Azure assigns it. Left unsaid, every apply plans to unset
  # the profile this is actually running on -- noise in a plan that has to be
  # read in full to be safe, which is how a real change gets skimmed past.
  workload_profile_name = "Consumption"

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
    for_each = toset(concat(
      ["database-url", "migration-database-url"],
      var.operator_portal_enabled ? ["admin-database-url"] : [],
    ))
    content {
      name                = secret.value
      identity            = azurerm_user_assigned_identity.app.id
      key_vault_secret_id = local.secret_by_name[secret.value].versionless_id
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

      # The operator credential reaches this job so the job can CREATE it.
      #
      # A dynamic block rather than always set, and here the two readings are
      # not close: the bootstrap treats unset as "this installation has no
      # operator portal" and an empty string as a URL it cannot parse, which
      # stops the job and therefore the deploy. There is no intent an empty
      # value here would express.
      #
      # `antifailure_admin` is created NOLOGIN with no password by the
      # migrations, exactly as `antifailure_app` is, and nothing else in this
      # repository ever gives it one. Without this the application's
      # AF_ADMIN_DATABASE_URL would name a role no client can authenticate as,
      # and createAdminPool is awaited at start-up, so the whole control plane
      # would refuse to start rather than merely lose its portal.
      dynamic "env" {
        for_each = var.operator_portal_enabled ? [1] : []
        content {
          name        = "AF_ADMIN_DATABASE_URL"
          secret_name = "admin-database-url"
        }
      }
    }
  }

  tags = var.tags

  # The same split of ownership the app has, for the same reason, and it was
  # missing here.
  #
  # Continuous deployment points this job at the image it is about to deploy and
  # runs it, so the image on this job is the deploy pipeline's, not Terraform's.
  # Without this line every apply plans to put it back to var.image_tag: the
  # plan that added the sign-in allowlist also proposed reverting the migration
  # job to v0.1.1, which is a build from before /readyz existed.
  #
  # It would have healed itself on the next deploy, and that is not a defence.
  # An apply that silently rolls back the thing that migrates the database is
  # exactly the shape of the drift that has now bitten this stack three times.
  lifecycle {
    ignore_changes = [template[0].container[0].image]
  }

  # The extension allow-list too, and not only the database. Without it the
  # first migration is refused and this job fails, which is exactly what it did.
  depends_on = [
    azurerm_role_assignment.app_reads_secrets,
    azurerm_postgresql_flexible_server_database.this,
    azurerm_postgresql_flexible_server_configuration.extensions,
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

  # Named because Azure assigns it. Left unsaid, every apply plans to unset
  # the profile this is actually running on -- noise in a plan that has to be
  # read in full to be safe, which is how a real change gets skimmed past.
  workload_profile_name = "Consumption"

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
    key_vault_secret_id = local.secret_by_name["migration-database-url"].versionless_id
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

  # Named because Azure assigns it. Left unsaid, every apply plans to unset
  # the profile this is actually running on -- noise in a plan that has to be
  # read in full to be safe, which is how a real change gets skimmed past.
  workload_profile_name = "Consumption"

  # Multiple, so that a rollback is a traffic shift rather than a redeploy.
  #
  # In Single mode the previous revision is deactivated the moment the new one
  # is provisioned, so recovering from a bad deploy means building and starting
  # the old image again: minutes, during which the bad revision is serving. In
  # Multiple mode the old revision is still running with zero traffic, and
  # rolling back is one API call that takes effect in seconds.
  #
  # This is what makes the post-deploy health gate in .github/workflows/cd.yml
  # able to promise anything. A gate that detects a bad deploy and cannot undo
  # it quickly is a notification, not a gate.
  revision_mode = "Multiple"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.app.id]
  }


  dynamic "secret" {
    for_each = toset(concat(
      ["database-url", "github-client-id", "github-client-secret", "github-redirect-uri"],
      var.provider_key_secret_enabled ? ["provider-key-secret"] : [],
      var.operator_portal_enabled ? ["admin-database-url"] : [],
      var.analytics_enabled ? ["analytics-surrogate-secret"] : [],
    ))
    content {
      name                = secret.value
      identity            = azurerm_user_assigned_identity.app.id
      key_vault_secret_id = local.secret_by_name[secret.value].versionless_id
    }
  }

  # The App's two secrets, which this module reads rather than writes, and the
  # three credentials Stripe and Resend mint, which it reads for the same
  # reason: a resource that manages a value it cannot produce is a resource that
  # will one day set it to the empty string.
  dynamic "secret" {
    for_each = merge(
      var.github_app_id == "" ? {} : {
        "github-app-private-key"    = data.azurerm_key_vault_secret.github_app_private_key[0].versionless_id
        "github-app-webhook-secret" = data.azurerm_key_vault_secret.github_app_webhook_secret[0].versionless_id
      },
      var.stripe_price_team == "" ? {} : {
        "stripe-secret-key"     = data.azurerm_key_vault_secret.stripe_secret_key[0].versionless_id
        "stripe-webhook-secret" = data.azurerm_key_vault_secret.stripe_webhook_secret[0].versionless_id
      },
      var.mail_from == "" ? {} : {
        "resend-api-key" = data.azurerm_key_vault_secret.resend_api_key[0].versionless_id
      },
    )
    content {
      name                = secret.key
      identity            = azurerm_user_assigned_identity.app.id
      key_vault_secret_id = secret.value
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

    # The weights here are the INITIAL state only. Continuous deployment owns
    # them afterwards: it puts a new revision in at zero, checks it, and then
    # shifts. See the lifecycle block below.
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

      # Who may sign in. Set for every value of the variable EXCEPT null.
      #
      # Read the condition carefully, because the obvious simplification of it
      # is the bug this deployment already shipped once. The application reads
      # an unset variable as "open to every GitHub account" and an EMPTY one as
      # "nobody". So a dynamic block keyed on the list being empty would turn
      # the most restrictive intent into the least restrictive deployment.
      #
      # It is keyed on null instead, which is a different value from an empty
      # list in Terraform and is the only way to say "everybody" here. An empty
      # list still renders `AF_SIGNIN_ALLOWLIST=""` and still closes the plane
      # to everyone, exactly as before.
      dynamic "env" {
        for_each = var.signin_allowlist == null ? [] : [1]
        content {
          name  = "AF_SIGNIN_ALLOWLIST"
          value = join(",", var.signin_allowlist)
        }
      }

      # What the ones it turns away see. Unset means the refusal page has
      # nowhere to send anybody, which is correct for an installation whose
      # operator has their own way of being asked, so it is a dynamic block
      # rather than always set: an empty string here would be a link to nothing.
      dynamic "env" {
        for_each = var.signup_url == "" ? [] : [var.signup_url]
        content {
          name  = "AF_SIGNUP_URL"
          value = env.value
        }
      }

      # Whether signing in ends somewhere. Always set, both ways, because the
      # value a reader of the revision needs is the answer rather than the
      # absence of a question, and the application's "1 or 0 or unset" grammar
      # accepts an explicit 0.
      env {
        name  = "AF_SELF_SERVE_SIGNUP"
        value = var.self_serve_signup ? "1" : "0"
      }

      dynamic "env" {
        for_each = var.provider_key_secret_enabled ? [1] : []
        content {
          name        = "AF_PROVIDER_KEY_SECRET"
          secret_name = "provider-key-secret"
        }
      }

      # All three together. The application refuses a half-configured App at
      # start-up, so a block that set two of these would stop the container
      # rather than degrade, which is the behaviour we want and not a state to
      # deploy into on purpose.
      dynamic "env" {
        for_each = var.github_app_id == "" ? [] : [var.github_app_id]
        content {
          name  = "AF_GITHUB_APP_ID"
          value = env.value
        }
      }
      dynamic "env" {
        for_each = var.github_app_id == "" ? [] : [1]
        content {
          name        = "AF_GITHUB_APP_PRIVATE_KEY"
          secret_name = "github-app-private-key"
        }
      }
      dynamic "env" {
        for_each = var.github_app_id == "" ? [] : [1]
        content {
          name        = "AF_GITHUB_APP_WEBHOOK_SECRET"
          secret_name = "github-app-webhook-secret"
        }
      }

      # -------------------------------------------------------------------
      # THE OPERATOR PORTAL.
      #
      # A separate credential rather than a privilege on the application's own
      # role: `antifailure_admin` holds BYPASSRLS, and BYPASSRLS is an
      # ATTRIBUTE, which membership does not carry. That is what makes the wall
      # hold. The application role cannot be granted its way across it.
      #
      # Both blocks are dynamic on the same switch and they are the clearest
      # case in this file where empty is not a quieter version of unset. Unset
      # means "no operator portal", which the application says at startup and
      # every /admin route repeats by name. An empty AF_ADMIN_DATABASE_URL is a
      # connection string with no host, and the pool is awaited at start-up, so
      # it would stop the container. An empty AF_ADMIN_POOL_MAX is worse than
      # either, and it is the reason this pair is called out: the application
      # reads it as `Number(value ?? 4)`, so unset is four and empty is
      # Number('') -- a pool of ZERO, which is a portal that hangs on its first
      # query rather than one that says it is not configured.
      dynamic "env" {
        for_each = var.operator_portal_enabled ? [1] : []
        content {
          name        = "AF_ADMIN_DATABASE_URL"
          secret_name = "admin-database-url"
        }
      }
      dynamic "env" {
        for_each = var.operator_portal_enabled ? [var.admin_pool_max] : []
        content {
          name  = "AF_ADMIN_POOL_MAX"
          value = tostring(env.value)
        }
      }

      # -------------------------------------------------------------------
      # ANALYTICS.
      #
      # The secret is dynamic on its own switch because unset is a real and
      # supported answer -- "recording nothing", said out loud at startup -- and
      # an empty value is not: the application requires exactly 64 hex
      # characters and stops at startup on any other length.
      dynamic "env" {
        for_each = var.analytics_enabled ? [1] : []
        content {
          name        = "AF_ANALYTICS_SURROGATE_SECRET"
          secret_name = "analytics-surrogate-secret"
        }
      }

      # Who may READ the dashboard, which is a separate decision from whether
      # anything is recorded: an installation can record without granting the
      # dashboard to anybody. Dynamic, and safe in the direction that matters,
      # because unset and empty BOTH mean nobody here. There is no value that
      # opens it.
      dynamic "env" {
        for_each = var.analytics_operator_org == "" ? [] : [var.analytics_operator_org]
        content {
          name  = "AF_ANALYTICS_OPERATOR_ORG"
          value = env.value
        }
      }

      # How long raw analytics events are kept. Null keeps them forever, which
      # is the default because retention is an operator's decision, and an empty
      # string is not a number of days -- the rollup refuses it by name.
      dynamic "env" {
        for_each = var.analytics_retention_days == null ? [] : [var.analytics_retention_days]
        content {
          name  = "AF_ANALYTICS_RETENTION_DAYS"
          value = tostring(env.value)
        }
      }

      # The one origin the marketing site's beacon may be called from.
      #
      # Dynamic, and this one is worth checking rather than assuming, because it
      # is a CORS decision and the dangerous default would be permissive. It is
      # not: the application compares the arriving Origin against this value and
      # refuses when it is falsy, so unset and empty both refuse every beacon
      # and neither reflects whatever Origin arrives. Absent is the safe end.
      dynamic "env" {
        for_each = var.site_origin == "" ? [] : [var.site_origin]
        content {
          name  = "AF_SITE_ORIGIN"
          value = env.value
        }
      }

      # -------------------------------------------------------------------
      # The GitHub App's public install address, which is a different variable
      # from the App itself and is why it was missed.
      #
      # Its absence is what hid both actions on the "No organization yet"
      # screen. Dynamic, because the application validates the address and stops
      # at startup on anything that is not an
      # https://github.com/apps/<slug>/installations/new URL, so an empty string
      # would take the container down rather than turn a button off. Unset is
      # the supported "not configured": the person is told so and is still
      # offered "Check my GitHub membership", which never depended on it.
      dynamic "env" {
        for_each = var.github_app_install_url == "" ? [] : [var.github_app_install_url]
        content {
          name  = "AF_GITHUB_APP_INSTALL_URL"
          value = env.value
        }
      }

      # Where the GitHub API lives, for GitHub Enterprise Server. Dynamic
      # because the application passes this straight through as the API base
      # when it is set: an empty string would be a base URL that resolves
      # nowhere, where unset is the documented https://api.github.com.
      dynamic "env" {
        for_each = var.github_api_base == "" ? [] : [var.github_api_base]
        content {
          name  = "AF_GITHUB_API_BASE"
          value = env.value
        }
      }

      # -------------------------------------------------------------------
      # SIGNING IN WITH A LINK, and inviting somebody who is not in your GitHub
      # organization. Both of these are the same three variables.
      #
      # All three on one switch, deliberately, and it is the same shape the
      # GitHub App's three blocks have above and for the same reason: the
      # application exits at startup when it can see one or two of them, because
      # two of three is a link that goes nowhere or mail that cannot be sent.
      # A block that could emit AF_MAIL_FROM without AF_PUBLIC_URL would not
      # degrade, it would stop the container. The precondition below refuses
      # that combination at PLAN time instead, where it is a review comment.
      dynamic "env" {
        for_each = var.mail_from == "" ? [] : [1]
        content {
          name        = "AF_RESEND_API_KEY"
          secret_name = "resend-api-key"
        }
      }
      dynamic "env" {
        for_each = var.mail_from == "" ? [] : [var.mail_from]
        content {
          name  = "AF_MAIL_FROM"
          value = env.value
        }
      }
      dynamic "env" {
        for_each = var.mail_from == "" ? [] : [var.public_url]
        content {
          name  = "AF_PUBLIC_URL"
          value = env.value
        }
      }

      # Where an enterprise lead is announced.
      #
      # Dynamic on its own value rather than on the mail switch, and the
      # difference matters. Gating it on mail_from would silently DROP an
      # address somebody set, leaving them with "nobody is mailed" and no
      # explanation. The precondition below refuses the combination instead, so
      # the address is either delivered with a mailer behind it or the plan says
      # why not.
      dynamic "env" {
        for_each = var.lead_notify_email == "" ? [] : [var.lead_notify_email]
        content {
          name  = "AF_LEAD_NOTIFY_EMAIL"
          value = env.value
        }
      }

      # The name in a sign-in link's subject line, for a white-labelled
      # deployment. Dynamic; the application already treats an empty string as
      # unset, and a subject line addressed from nothing is not an intent.
      dynamic "env" {
        for_each = var.product_name == "" ? [] : [var.product_name]
        content {
          name  = "AF_PRODUCT_NAME"
          value = env.value
        }
      }

      # -------------------------------------------------------------------
      # BILLING, on one switch for the same reason mail is.
      #
      # There is no AF_STRIPE_PRICE_ENTERPRISE block and there is not meant to
      # be one. Enterprise is arranged with a person, so no Stripe price exists
      # behind it; a plan with no price is not a misconfiguration, and checkout
      # refuses that plan by name rather than reaching Stripe with an empty
      # price id.
      dynamic "env" {
        for_each = var.stripe_price_team == "" ? [] : [1]
        content {
          name        = "AF_STRIPE_SECRET_KEY"
          secret_name = "stripe-secret-key"
        }
      }
      dynamic "env" {
        for_each = var.stripe_price_team == "" ? [] : [1]
        content {
          name        = "AF_STRIPE_WEBHOOK_SECRET"
          secret_name = "stripe-webhook-secret"
        }
      }
      dynamic "env" {
        for_each = var.stripe_price_team == "" ? [] : [var.stripe_price_team]
        content {
          name  = "AF_STRIPE_PRICE_TEAM"
          value = env.value
        }
      }

      # The plan gate on a control plane sold only to enterprise organizations.
      # Dynamic because any value other than `enterprise` stops the process, and
      # an empty string is one of those values.
      dynamic "env" {
        for_each = var.hosted_required_plan == "" ? [] : [var.hosted_required_plan]
        content {
          name  = "AF_HOSTED_REQUIRED_PLAN"
          value = env.value
        }
      }

      # Whoever runs this plane also decides each organization's plan.
      #
      # Dynamic, and this is the one place a bool could plausibly have been
      # emitted as "0" instead. It is not, because unset and "0" mean exactly
      # the same thing to the application -- both parse to false -- so an
      # always-set block would buy nothing and would put a variable in the
      # template that the precondition below has to reason about anyway.
      dynamic "env" {
        for_each = var.operator_sets_plan ? [1] : []
        content {
          name  = "AF_OPERATOR_SETS_PLAN"
          value = "1"
        }
      }

      # Model prices, added to the built-in defaults rather than replacing them.
      # Dynamic; the application treats blank as "use the defaults", so an empty
      # block would say the same thing at more length.
      dynamic "env" {
        for_each = var.model_prices == "" ? [] : [var.model_prices]
        content {
          name  = "AF_MODEL_PRICES"
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

  # Terraform owns the shape of this app; the deploy pipeline owns which build
  # is in it and how much traffic that build has.
  #
  # Without this, the two fight. Every `terraform apply` would reset the image
  # to var.image_tag and hand 100 percent of traffic back to whatever that
  # resolves to, silently undoing the last deploy, and the next deploy would
  # show up as drift in the next plan. Splitting ownership at this line is what
  # lets both run on their own schedule.
  #
  # THE COST OF THE SPLIT, which has already caught us once. This app runs in
  # Multiple revision mode, so a change to the template below creates a new
  # revision, and traffic is not ours to move. The new revision comes up at zero
  # percent, terraform reports a successful apply, and production keeps serving
  # the old one. Add an environment variable here and the application does not
  # see it until somebody deploys. Check what is actually serving after an apply
  # that touched the template; the self-hosting guide has the two commands.
  lifecycle {
    ignore_changes = [
      template[0].container[0].image,
      ingress[0].traffic_weight,
      # ingress[0].custom_domain is NOT here, and it was, until `terraform
      # validate` pointed out that the attribute is computed by the provider
      # and cannot be set in configuration, so ignoring it does nothing.
      # azurerm_container_app_custom_domain in domain.tf owns the binding and
      # this resource reads it back. Older provider versions made it
      # configurable, which is why every example on the internet ignores it.
    ]

    # THE CONNECTION ARITHMETIC, CHECKED AT PLAN TIME.
    #
    # Three numbers in this file multiply into a fourth that nothing used to
    # compute: replicas times pool_max is a demand on the database, and
    # database.tf knows the supply. Staging ran for weeks with a shape that
    # only fit because the app never scaled, and the first thing that pushed
    # it over was not traffic at all.
    #
    # This fails the PLAN, which is the point: a number that cannot fit should
    # be a review comment on a pull request, not a 503 at the next peak.
    # deploy/cd/deploy.sh checks the measured version of the same arithmetic
    # after every deploy, because a plan cannot see how many revisions the
    # platform is actually running.
    precondition {
      condition = (
        local.usable_connections == 0 ||
        local.peak_app_connections + local.tool_connections <= local.usable_connections
      )
      error_message = "This app can open ${local.peak_app_connections} connections (max_replicas ${var.max_replicas} plus one rollback revision at min_replicas ${var.min_replicas}, each holding ${local.per_replica_connections}: a pool of ${var.pool_max}${var.operator_portal_enabled ? " and an operator pool of ${var.admin_pool_max}" : ""}) and ${var.database_sku} hands an ordinary role only ${local.usable_connections}, with ${local.tool_connections} of those spoken for by the bootstrap job, the maintenance job, break-glass and backup. Lower pool_max${var.operator_portal_enabled ? " or admin_pool_max" : ""}, lower max_replicas, or move to a SKU with more max_connections."
    }

    # THE CONTRADICTIONS THE APPLICATION EXITS ON, CAUGHT AT PLAN TIME INSTEAD.
    #
    # Each of the three below is already refused by the control plane at
    # start-up, with `process.exit(2)` and a message naming the variables. That
    # is the right behaviour and it is not enough here, because of the cost this
    # file's lifecycle block spells out just above: a template change creates a
    # revision at ZERO percent traffic. So a tfvars file carrying one of these
    # combinations applies cleanly, terraform reports success, the new revision
    # crash-loops where nobody is looking, and production keeps serving the old
    # build until somebody deploys and the deploy gate finally fails.
    #
    # A configuration that cannot start should be a review comment on a pull
    # request, which is exactly the argument the connection arithmetic above
    # makes for itself.

    # Mail is three variables and the application refuses two of them.
    precondition {
      condition     = var.mail_from == "" || var.public_url != ""
      error_message = "mail_from is set and public_url is empty, so this deployment would set AF_MAIL_FROM and AF_RESEND_API_KEY without AF_PUBLIC_URL. The control plane exits at start-up on a half-configured mailer, because two of the three is a link that goes nowhere or mail that cannot be sent. Set public_url to the origin a browser reaches this deployment on, or clear mail_from to turn the path off."
    }

    # A lead notification nobody receives.
    #
    # Not a start-up refusal in the application, which records the lead and
    # prints that the address CANNOT be told. That is the right runtime
    # behaviour and it is a poor deploy-time one: the sentence scrolls past in a
    # log and the deployment goes on believing it announces leads.
    precondition {
      condition     = var.lead_notify_email == "" || var.mail_from != ""
      error_message = "lead_notify_email is set and mail_from is empty, so this deployment would name an address it has no mailer to reach. Enterprise leads are still recorded and readable with `af-control-plane-backup leads`; set mail_from to announce them, or clear lead_notify_email."
    }

    # A plan gate nobody can satisfy.
    precondition {
      condition     = contains(["", "enterprise"], var.hosted_required_plan) && (var.hosted_required_plan == "" || var.stripe_price_team != "")
      error_message = "hosted_required_plan is ${jsonencode(var.hosted_required_plan)}. It must be `enterprise` or empty, and setting it requires billing, which means stripe_price_team and the two Stripe secrets in the vault. The control plane exits at start-up on either mistake: an organization on a plane with a gate it cannot buy its way past is locked out of the product."
    }

    # A plan that can be granted by hand is not a plan anybody has to buy.
    precondition {
      condition     = !var.operator_sets_plan || (var.stripe_price_team == "" && var.hosted_required_plan == "")
      error_message = "operator_sets_plan is true on an installation that takes payment. A plan that can be granted by hand is not a plan anybody has to buy, and the control plane exits at start-up on the combination. Unset it, or clear stripe_price_team and hosted_required_plan."
    }
  }

  # The application must not start before its database is usable. Without this
  # the first revision comes up against a schema that does not exist yet and
  # against a role with no grants, which is a CrashLoop whose cause is an
  # ordering rather than a bug.
  depends_on = [azurerm_container_app_job.bootstrap]
}
