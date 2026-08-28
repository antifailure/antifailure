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

# The name is overridable because a Key Vault cannot be renamed in place.
#
# Changing it is a destroy and a create, and Key Vault carries purge protection,
# so the old name is unusable for seven days afterwards and every secret in it
# goes through soft delete. A plan that renames this vault is therefore an
# outage plus a week, and it is produced by something as small as editing the
# expression below. This deployment's vault predates that expression and is
# called afcp-kv-centralus; staging.tfvars says so, and that one line is the
# difference between an apply and a replacement.
resource "azurerm_key_vault" "this" {
  name                = var.key_vault_name != "" ? var.key_vault_name : substr(replace("${var.name}-kv", "_", "-"), 0, 24)
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

# The sealing secret for provider keys.
#
# Generated here rather than passed in, so that no person and no workflow ever
# holds it: it goes from the random provider into Key Vault and into the
# container's environment, and the only copy outside the vault is in Terraform
# state, which lives in a storage account with key access disabled.
#
# keepers is empty on purpose. Anything in it that changes regenerates the
# secret, and a regenerated sealing secret means every stored provider key
# stops opening -- silently, because a sealed value that will not decrypt looks
# exactly like a tampered one. There is no key rotation story that starts with
# "the infrastructure changed the secret without being asked".
resource "random_bytes" "provider_key_secret" {
  count = var.provider_key_secret_enabled ? 1 : 0
  # 32 bytes, which is what sealingKeyFrom() requires -- exactly, not at least.
  # random_bytes rather than random_password because the application wants
  # BYTES: a password of 32 characters is 32 bytes only by accident of encoding,
  # and base64 of 32 characters chosen from an alphabet is not 32 bytes of key
  # material. This resource's .base64 is the value the application parses.
  length = 32
}

locals {
  secrets = merge({
    "database-url"           = local.app_url
    "migration-database-url" = local.migration_url
    "github-client-id"       = var.github_client_id
    "github-client-secret"   = var.github_client_secret
    "github-redirect-uri"    = var.github_redirect_uri
    }, var.provider_key_secret_enabled ? {
    "provider-key-secret" = random_bytes.provider_key_secret[0].base64
  } : {})
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
