# The hosted control plane, in af-cp-scus.
#
# One `terraform apply` from nothing produces: a resource group with a budget, a
# private Postgres with two roles, a Key Vault holding every credential, a
# storage account for goldens, a bootstrap job that makes the database usable,
# a maintenance job that keeps the event partitions ahead, and the application
# on a public HTTPS endpoint.
#
# Read infra/ISOLATION.md before running this. Everything it creates is inside a
# resource group prefixed af- and tagged project=antifailure, which is what
# makes a cleanup scoped to that tag unable to reach anything else in a
# subscription that also holds other people's work.

terraform {
  required_version = ">= 1.9.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.16"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  # Remote state, configured at init time rather than committed, because the
  # storage account name is an identifier this repository does not carry:
  #
  #   terraform init -backend-config=backend.hcl
  #
  # stacks/tfstate creates the account. Until it exists, `terraform init` with
  # no backend config keeps state locally, which is fine for a plan and is not
  # fine for an apply anybody else will ever need to repeat.
  backend "azurerm" {}
}

provider "azurerm" {
  features {
    key_vault {
      # A vault removed by accident is recoverable rather than gone. Purging on
      # destroy would make `terraform destroy` permanent, and the whole point of
      # soft delete is that it is not.
      purge_soft_delete_on_destroy    = false
      recover_soft_deleted_key_vaults = true
    }
    resource_group {
      # Refuse to delete a resource group that still contains something
      # Terraform does not know about. That is exactly the case where a delete
      # is destroying somebody else's work.
      prevent_deletion_if_contains_resources = true
    }
  }

  subscription_id = var.subscription_id

  # The CI identity must never need permission at SUBSCRIPTION scope, and this
  # is the line that decides it.
  #
  # azurerm 4.x defaults to registering a core set of resource providers when it
  # starts, and registration is a write at subscription scope. That single
  # default would force the plan job's identity to hold a subscription level
  # role, which is exactly what infra/ISOLATION.md refuses. Every provider this
  # stack touches is already registered on this subscription and registration is
  # a one time act, so nothing is lost by never asking.
  #
  # THE TRADE, STATED: on a subscription where a provider is NOT yet registered,
  # apply fails with a message naming the namespace. The fix is one command,
  # `az provider register --namespace <name>`, run by somebody who is allowed
  # to, and self-hosting/azure.md says so.
  resource_provider_registrations = "none"
}

module "foundation" {
  source = "../../modules/foundation"

  name        = var.resource_group_name
  location    = var.location
  environment = "cp"

  monthly_budget_usd    = var.monthly_budget_usd
  budget_contact_emails = var.budget_contact_emails
  log_retention_days    = var.log_retention_days
}

module "control_plane" {
  source = "../../modules/control-plane"

  name                = var.name
  resource_group_name = module.foundation.resource_group_name
  location            = module.foundation.location
  tags                = module.foundation.tags
  log_analytics_id    = module.foundation.log_analytics_id

  key_vault_name = var.key_vault_name

  image_repository = var.image_repository
  image_tag        = var.image_tag
  image_digest     = var.image_digest

  database_extensions   = var.database_extensions
  database_sku          = var.database_sku
  database_storage_mb   = var.database_storage_mb
  backup_retention_days = var.backup_retention_days
  geo_redundant_backup  = var.geo_redundant_backup
  high_availability     = var.high_availability

  min_replicas = var.min_replicas
  max_replicas = var.max_replicas
  pool_max     = var.pool_max
  app_base_url = var.app_base_url

  event_retention_months = var.event_retention_months

  github_client_id     = var.github_client_id
  github_client_secret = var.github_client_secret
  github_redirect_uri  = var.github_redirect_uri

  signin_allowlist            = var.signin_allowlist
  provider_key_secret_enabled = var.provider_key_secret_enabled
  github_app_id               = var.github_app_id

  assign_deployer_secret_officer = var.assign_deployer_secret_officer
  deployer_principal_id          = var.deployer_principal_id

  custom_domain           = var.custom_domain
  dns_zone_name           = var.dns_zone_name
  dns_zone_resource_group = var.dns_zone_resource_group
}

# Alerting, which staging does not run and production does.
#
# OFF BY DEFAULT, and that is a decision rather than an oversight. Staging goes
# down on purpose several times a week: it is where a bad deploy is supposed to
# be caught. Paging a person for that trains them to ignore the page, and the
# page they learn to ignore is the same one production sends. Staging keeps the
# post-deploy health gate in cd.yml, which fails the deploy and stops there.
#
# It also costs money. The availability test is billed per execution per
# location and is the largest line this module adds.
module "alerting" {
  count  = var.alerting_enabled ? 1 : 0
  source = "../../modules/alerting"

  name                = var.name
  resource_group_name = module.foundation.resource_group_name
  location            = module.foundation.location
  tags                = module.foundation.tags
  log_analytics_id    = module.foundation.log_analytics_id

  alert_emails           = var.alert_emails
  alert_sms_country_code = var.alert_sms_country_code
  alert_sms_number       = var.alert_sms_number

  container_app_id   = module.control_plane.container_app_id
  postgres_server_id = module.control_plane.postgres_server_id
  job_ids            = module.control_plane.job_ids

  # The name a customer uses, not the generated one. A probe against the
  # generated address is green while DNS, the binding or the certificate is
  # broken, which is most of the ways this becomes unreachable without the
  # application doing anything wrong.
  probe_url = "${var.app_base_url}/readyz"

  # Not the SKU. The number the SKU implies, computed once in the module that
  # owns the server, so this alert's threshold and the application's own
  # connection ceiling cannot drift apart.
  usable_connections = module.control_plane.usable_connections
  min_replicas       = var.min_replicas
}
