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

locals {
  # A KEY VAULT NAME IS GLOBAL AND A DELETED ONE KEEPS IT FOR A WEEK.
  #
  # This was `${var.name}-kv`, and that is a trap the first region move walked
  # straight into. Soft delete holds the name for the retention period, and
  # purge protection means it cannot be released early even by the person who
  # owns it: that is the whole point of purge protection and it is working as
  # designed. So a name with nothing region-specific in it makes
  # destroy-and-recreate-elsewhere fail on the vault, seven days after the
  # decision to move, with an error about a name conflict rather than about
  # soft delete.
  #
  # Including the location fixes the move. It does NOT make a
  # destroy-and-recreate in the SAME region within seven days work, and nothing
  # can, because that is exactly the attack purge protection exists to stop:
  # somebody with delete replacing the vault's contents by replacing the vault.
  # Set `key_vault_name` yourself if you need to sidestep it deliberately.
  key_vault_name = coalesce(
    var.key_vault_name,
    substr(replace("${var.name}-kv-${var.location}", "_", "-"), 0, 24),
  )
}

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
  name                = local.key_vault_name
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

# Whoever is running Terraform needs to be able to WRITE the secrets below, and
# this grants it. It is OFF BY DEFAULT, which is the opposite of what it was,
# and the reason is worth more than the resource.
#
# A ROLE ASSIGNMENT WHOSE PRINCIPAL IS "WHOEVER IS RUNNING TERRAFORM" CHURNS ON
# EVERY PLAN BY A DIFFERENT CALLER. principal_id is ForceNew, so a plan run by
# the CI identity against a state applied from a laptop does not report a small
# difference: it reports that a role assignment MUST BE REPLACED. The pull
# request plan job exists so that a change which would destroy something is
# visible in review, and this made every single run carry a destroy that meant
# nothing. A plan that always shows a destroy is a plan people stop reading,
# which is exactly how a real one gets waved through.
#
# So the caller-dependent resource is out of the stack by default, and the
# grant is one command run once by the operator, documented in
# self-hosting/azure.md:
#
#   az role assignment create --role "Key Vault Secrets Officer" \
#     --assignee-object-id "$(az ad signed-in-user show --query id -o tsv)" \
#     --assignee-principal-type User --scope <vault id>
#
# Set assign_deployer_secret_officer = true only where exactly one identity
# ever runs Terraform, and understand that any OTHER identity planning the
# stack will then propose to replace this.
resource "azurerm_role_assignment" "deployer_writes_secrets" {
  count                = var.assign_deployer_secret_officer ? 1 : 0
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = coalesce(var.deployer_principal_id, data.azurerm_client_config.current.object_id)
}

# TWO KINDS OF SECRET, AND TERRAFORM MUST NOT TREAT THEM THE SAME.
#
# The database URLs are OWNED here: Terraform generated the passwords inside
# them, nothing else may change them, and a difference between config and vault
# is drift that should be corrected.
#
# The GitHub OAuth values are SEEDED here and owned elsewhere. Terraform cannot
# know them (creating an OAuth application is a human act on another service),
# so it writes a placeholder once and the operator replaces it. If Terraform
# kept managing the VALUE, three things would follow, and all three did:
#
#   every plan run without the real values proposes to overwrite the real ones
#   with placeholders, which is a plan that would BREAK sign-in if applied;
#   the pull request plan job, which cannot have the real values and must not,
#   shows three changes on every run forever;
#   and self-hosting/control-plane.md's instruction to rotate them with `az
#   keyvault secret set` is quietly untrue, because the next apply reverts it.
#
# ignore_changes on the value makes the rotation instruction true.
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
  # provider-key-secret is owned rather than seeded: Terraform generates it, so
  # a difference between configuration and vault is drift to correct, which is
  # the definition of owned above.
  owned_secrets = merge({
    "database-url"           = local.app_url
    "migration-database-url" = local.migration_url
    }, var.provider_key_secret_enabled ? {
    "provider-key-secret" = random_bytes.provider_key_secret[0].base64
  } : {})
  seeded_secrets = {
    "github-client-id"     = var.github_client_id
    "github-client-secret" = var.github_client_secret
    "github-redirect-uri"  = var.github_redirect_uri
  }
}

resource "azurerm_key_vault_secret" "owned" {
  for_each     = local.owned_secrets
  name         = each.key
  value        = each.value
  key_vault_id = azurerm_key_vault.this.id
  content_type = "text/plain"
  tags         = var.tags

  depends_on = [azurerm_role_assignment.deployer_writes_secrets]
}

# The GitHub App's two secrets, read rather than written.
#
# See the variable comments: GitHub mints the private key and shows it once, so
# Terraform cannot create it and must not manage it. A person puts both in the
# vault; this reads them so the container app can reference them by id.
#
# count on github_app_id rather than on the secrets existing, so that a stack
# with no App plans clean instead of failing on a data source for a secret that
# is deliberately absent.
data "azurerm_key_vault_secret" "github_app_private_key" {
  count        = var.github_app_id == "" ? 0 : 1
  name         = var.github_app_private_key_secret_name
  key_vault_id = azurerm_key_vault.this.id
}

data "azurerm_key_vault_secret" "github_app_webhook_secret" {
  count        = var.github_app_id == "" ? 0 : 1
  name         = var.github_app_webhook_secret_name
  key_vault_id = azurerm_key_vault.this.id
}

resource "azurerm_key_vault_secret" "seeded" {
  for_each     = local.seeded_secrets
  name         = each.key
  value        = each.value
  key_vault_id = azurerm_key_vault.this.id
  content_type = "text/plain"
  tags         = var.tags

  lifecycle {
    # Seeded once, then owned by the operator. See the block comment above.
    ignore_changes = [value]
  }

  depends_on = [azurerm_role_assignment.deployer_writes_secrets]
}

locals {
  # One map so callers do not have to know which half a name lives in.
  secret_by_name = merge(azurerm_key_vault_secret.owned, azurerm_key_vault_secret.seeded)
}
