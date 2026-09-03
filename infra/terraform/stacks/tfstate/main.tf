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

  # ENTRA FOR THE DATA PLANE, NOT A STORAGE KEY.
  #
  # This is not a preference, it is required by the account below. After it
  # creates a storage account the provider polls the BLOB SERVICE to see whether
  # the data plane is up, and by default it authenticates that poll with a
  # shared key. `shared_access_key_enabled = false` makes that poll fail:
  #
  #   403 Key based authentication is not permitted on this storage account
  #
  # The account is created and healthy at that point, so the failure looks like
  # a transient Azure problem rather than a provider configuration one, and
  # running apply again reproduces it exactly. `storage_use_azuread` switches
  # every data plane call, including that poll, to the caller's Entra token.
  storage_use_azuread = true

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

  resource_group_name = var.resource_group_name
  location            = var.location
  environment         = "tfstate"

  # No budget: this group holds one storage account with a few megabytes in it.
  # A budget here would be noise, and noise is how a real budget alert gets
  # ignored.
  monthly_budget_usd    = 0
  log_analytics_enabled = false
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

  # Reachable, and ONLY because exemption.tf exempts this one resource group
  # from bonfire-deny-public-data. Read that file before changing this line:
  # every reason it is safe to be reachable lives there, and four of the five
  # reasons are settings in this very block. Delete the exemption and the next
  # write to this account is denied, which is the correct behaviour.
  #
  # Reachable is not readable. There is no storage key, nothing here can be made
  # anonymously public, and a read needs an Entra identity holding a data role.
  public_network_access_enabled = true

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

  # The exemption must exist BEFORE the account is written, and Terraform has no
  # way to know that from the arguments alone: nothing in this resource refers
  # to the exemption. Without this the two are created in parallel and the
  # account loses the race about half the time, with an error that names the
  # policy and gives no hint that the fix is ordering.
  #
  # AZURE POLICY IS EVENTUALLY CONSISTENT, so depends_on is necessary and not
  # sufficient. A first apply can still be denied while the exemption
  # propagates, which takes up to about fifteen minutes in the worst case. If
  # that happens the answer is to run apply again, not to change anything here.
  depends_on = [azurerm_resource_group_policy_exemption.state_is_reachable]

  lifecycle {
    # Losing this account loses the record of everything else that exists.
    #
    # ONE SHARP EDGE, BECAUSE IT COST TIME HERE. If a create fails AFTER Azure
    # has made the resource but before Terraform finishes with it, Terraform
    # marks the instance TAINTED, and the next plan proposes to replace it,
    # which prevent_destroy then refuses. The result is a stack that will not
    # move in either direction and an error naming prevent_destroy rather than
    # the taint. The account is fine; the fix is `terraform untaint
    # azurerm_storage_account.state`, not removing this guard.
    prevent_destroy = true
  }
}

data "azurerm_client_config" "current" {}

# Owner on the subscription does NOT let you read a blob.
#
# Azure splits storage into a control plane (create the account, read its
# settings) and a data plane (read and write the bytes). Owner covers the first
# and grants nothing on the second, and with shared keys disabled there is no
# back door. So the identity running Terraform needs an explicit data role or
# the container below cannot be created and no state can ever be written.
#
# Scoped to this one storage account, not the resource group and certainly not
# the subscription.
resource "azurerm_role_assignment" "deployer_writes_state" {
  scope                = azurerm_storage_account.state.id
  role_definition_name = "Storage Blob Data Owner"
  principal_id         = data.azurerm_client_config.current.object_id
}

resource "azurerm_storage_container" "state" {
  name                  = "tfstate"
  storage_account_id    = azurerm_storage_account.state.id
  container_access_type = "private"

  # Nothing in the container's arguments refers to the role assignment, so
  # without this Terraform creates them in parallel and the container loses.
  # Azure RBAC is also eventually consistent: a role assignment takes up to a
  # couple of minutes to be honoured on the data plane, so a first apply can
  # still fail with 403 AuthorizationPermissionMismatch. Run apply again.
  depends_on = [azurerm_role_assignment.deployer_writes_state]
}

# The CI identity's access to the state, and why it is READ ONLY.
#
# The plan job has to read the state or it cannot see a destroy, which is the
# whole reason it runs. It does NOT have to write it. The azurerm backend takes
# a blob lease for a lock even on a plan, and a lease is a write, so the natural
# grant here is Storage Blob Data Contributor. That would give every pull request
# in this repository the ability to corrupt or delete the record of everything
# the project owns, and a pull request can edit the workflow file that uses the
# credential in the same commit that runs it.
#
# So: Reader here, and `terraform plan -lock=false` in .github/workflows/infra.yml.
# The pair only makes sense together. A plan that writes nothing does not need a
# lock, and the cost of skipping it is that two plans running at once might read
# a state mid write, which produces a wrong plan and never a wrong state.
resource "azurerm_role_assignment" "ci_reads_state" {
  count                = var.ci_principal_id == "" ? 0 : 1
  scope                = azurerm_storage_account.state.id
  role_definition_name = "Storage Blob Data Reader"
  principal_id         = var.ci_principal_id
}

# AND A CONTROL PLANE READ, WHICH IS THE EXACT MIRROR OF AN EARLIER SURPRISE.
#
# Further up this file there is a note that Owner on the subscription grants
# nothing on the storage DATA plane. The reverse is just as true and cost just
# as much: a data role grants nothing on the CONTROL plane. Before the azurerm
# backend reads a single byte of state it does a control plane GET on the
# account, to resolve its blob endpoint, and Storage Blob Data Reader does not
# permit that:
#
#   AuthorizationFailed: ... does not have authorization to perform action
#   'Microsoft.Storage/storageAccounts/read'
#
# The message names a read action, the identity is called a Reader, and it
# still fails, which is why this is worth a comment rather than a line.
#
# Reader here is the CONTROL plane only: it can see that the account exists and
# what its settings are, and it can read nothing inside it. The pair is what
# makes the plan job work, and both halves are read-only.
resource "azurerm_role_assignment" "ci_sees_the_account" {
  count                = var.ci_principal_id == "" ? 0 : 1
  scope                = azurerm_storage_account.state.id
  role_definition_name = "Reader"
  principal_id         = var.ci_principal_id
}
