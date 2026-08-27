# The storage account that holds every other stack's state.
#
# A chicken and egg: this stack cannot keep its state in the account it creates,
# so its own state is local and is committed nowhere. That is acceptable for
# exactly one stack whose contents are a storage account and nothing secret, and
# it is why this is separate rather than part of the control plane.
#
# Every other stack's state DOES contain secrets: Terraform generates the
# database passwords, so they are in the state file whether or not they are in
# an output. Everything below follows from that.

terraform {
  required_version = ">= 1.9.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.16"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
}

module "foundation" {
  source = "../../modules/foundation"

  name        = var.resource_group_name
  location    = var.location
  environment = "tfstate"

  # No budget: this group holds one storage account with a few megabytes in it.
  # A budget here would be noise, and noise is how a real budget alert gets
  # ignored.
  monthly_budget_usd = 0
  log_analytics      = false
}

resource "azurerm_storage_account" "state" {
  name                = var.storage_account_name
  resource_group_name = module.foundation.resource_group_name
  location            = module.foundation.location

  account_tier             = "Standard"
  account_replication_type = "GRS"
  account_kind             = "StorageV2"

  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  allow_nested_items_to_be_public = false

  # Entra identity only. A storage key is a credential that cannot be revoked
  # per person and appears in no audit trail as a name, and this account holds
  # every database password the project has.
  shared_access_key_enabled = false

  blob_properties {
    versioning_enabled = true
    # A state file destroyed by a bad apply is recoverable for a month. This is
    # the single most valuable setting in this file.
    delete_retention_policy {
      days = 30
    }
    container_delete_retention_policy {
      days = 30
    }
  }

  tags = module.foundation.tags

  lifecycle {
    # Losing this account loses the record of everything else that exists.
    prevent_destroy = true
  }
}

resource "azurerm_storage_container" "state" {
  name                  = "tfstate"
  storage_account_id    = azurerm_storage_account.state.id
  container_access_type = "private"
}
