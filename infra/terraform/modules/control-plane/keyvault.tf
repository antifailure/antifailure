# Where the credentials live.
#
# The Container App reads them through its managed identity at start, so no
# secret is ever written into an app setting, a revision, or a Terraform output
# that somebody can `terraform output` in plain text.
#
# They ARE in the state file, because Terraform generated them. That is why the
# state account in stacks/tfstate has public network access disabled, blob
# versioning on, soft delete on, and no key-based access.

data "azurerm_client_config" "current" {}

resource "azurerm_key_vault" "this" {
  name                = substr(replace("${var.name}-kv", "_", "-"), 0, 24)
  location            = var.location
  resource_group_name = var.resource_group_name
  tenant_id           = data.azurerm_client_config.current.tenant_id
  sku_name            = "standard"

  # Access is by Azure RBAC, not vault access policies. Policies are per-vault
  # state that nothing else audits; RBAC assignments show up in the same place
  # as every other permission in the subscription.
  rbac_authorization_enabled = true

  # A vault that can be deleted and immediately recreated is a vault whose
  # secrets can be replaced by somebody who only has delete. Purge protection
  # makes that a seven day wait that somebody will notice.
  soft_delete_retention_days = 7
  purge_protection_enabled   = var.key_vault_purge_protection

  public_network_access_enabled = true
  network_acls {
    bypass         = "AzureServices"
    default_action = "Allow"
  }

  tags = var.tags
}

# The identity the Container App runs as. User-assigned rather than
# system-assigned so that the role assignment can exist before the app does:
# a system-assigned identity is created with the app, which means the app's
# first revision starts before it can read anything and fails.
resource "azurerm_user_assigned_identity" "app" {
  name                = "${var.name}-id"
  location            = var.location
  resource_group_name = var.resource_group_name
  tags                = var.tags
}

resource "azurerm_role_assignment" "app_reads_secrets" {
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.app.principal_id
}

# Whoever is running Terraform needs to be able to WRITE the secrets below.
# Scoped to this vault, never to the subscription.
resource "azurerm_role_assignment" "deployer_writes_secrets" {
  count                = var.assign_deployer_secret_officer ? 1 : 0
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}

locals {
  secrets = {
    "database-url"           = local.app_url
    "migration-database-url" = local.migration_url
    "github-client-id"       = var.github_client_id
    "github-client-secret"   = var.github_client_secret
    "github-redirect-uri"    = var.github_redirect_uri
  }
}

resource "azurerm_key_vault_secret" "this" {
  for_each     = local.secrets
  name         = each.key
  value        = each.value
  key_vault_id = azurerm_key_vault.this.id
  content_type = "text/plain"
  tags         = var.tags

  depends_on = [azurerm_role_assignment.deployer_writes_secrets]
}
