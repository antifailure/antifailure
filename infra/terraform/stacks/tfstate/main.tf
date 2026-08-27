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

  account_tier = "Standard"
  account_kind = "StorageV2"

  # LRS, and NOT because LRS is the right durability for a file that records
  # everything this project owns. GRS is. bonfire-sku-allowlist denies any
  # storage account whose sku is not exactly Standard_LRS, so a GRS account here
  # is refused at apply and the plan that produced it is clean.
  #
  # I had already read that policy when I wrote GRS. I read it for the SKUs it
  # names for Postgres, satisfied myself the database was fine, and did not
  # carry the storage clause four files across. Reading the one policy you
  # expect to bite is not the same as reading all of them, and a plan cannot
  # tell you the difference because Terraform does not evaluate Azure Policy.
  account_replication_type = "LRS"

  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  allow_nested_items_to_be_public = false

  # bonfire-deny-public-data on this subscription refuses any storage account
  # whose publicNetworkAccess is not "Disabled". That is not negotiable from
  # here, and it has a consequence worth stating rather than discovering:
  #
  # WITH THIS DISABLED, TERRAFORM CANNOT REACH ITS OWN STATE from a laptop or
  # from a hosted CI runner. There is no clever way around it: `Disabled` is not
  # a firewall default that a network rule can carve an exception out of, it
  # turns the data plane off for everything that is not a private endpoint.
  #
  # THAT LEAVES EXACTLY THREE OPTIONS AND THEY ARE NOT TECHNICAL, SO THIS STACK
  # IS STILL NOT APPLIED:
  #
  # 1. An `azurerm_resource_policy_exemption` on THIS resource group only, with
  #    a waiver category and an expiry. It changes no assignment and no
  #    definition and touches nothing bonfire owns; it is the mechanism Azure
  #    provides for precisely this. The account would still be Entra-only
  #    (shared_access_key_enabled = false), private, and RBAC gated, so reaching
  #    it needs a directory identity holding a data role rather than a URL. This
  #    is what most organisations actually run.
  # 2. A private endpoint plus a self hosted runner inside the VNet. No
  #    exemption, about 7.30 USD a month, and a runner to maintain.
  # 3. Local state, which is where the control plane stack sits today.
  #
  # Option 1 weakens a control somebody deliberately turned on, and that is a
  # decision for the person whose subscription this is, not for the person who
  # finds it inconvenient. Applying this file without settling that first
  # produces a state account nobody can read, which is strictly worse than no
  # remote state at all.
  public_network_access_enabled = false

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
