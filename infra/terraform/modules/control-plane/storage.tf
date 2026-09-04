# Golden dumps and their masking attestations.
#
# A golden is a masked copy of production data. Everything about this account is
# chosen on that basis: it is private, it is versioned, deleting from it is
# reversible for a month, and nothing in it is readable without an Entra
# identity. If this account is world readable then so is production.

resource "azurerm_storage_account" "goldens" {
  count               = var.goldens_enabled ? 1 : 0
  name                = substr(replace("${var.name}goldens", "-", ""), 0, 24)
  resource_group_name = var.resource_group_name
  location            = var.location

  account_tier             = "Standard"
  account_replication_type = var.goldens_replication
  account_kind             = "StorageV2"
  access_tier              = "Hot"

  # TLS 1.2 or nothing, and no unencrypted transport at all.
  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  allow_nested_items_to_be_public = false

  # Disabled, not merely firewalled. The subscription's bonfire-deny-public-data
  # policy refuses any storage account whose publicNetworkAccess is not
  # "Disabled", so a network_rules block with default_action = Deny is NOT
  # enough: the account is denied at create time. Reaching it therefore needs a
  # private endpoint, which is why this whole resource is off by default until
  # something actually reads it.
  public_network_access_enabled = false

  # Keys are a credential that cannot be revoked per person and does not appear
  # in any audit trail as a name. Entra identity only.
  shared_access_key_enabled = false

  blob_properties {
    versioning_enabled = true
    delete_retention_policy { days = var.goldens_soft_delete_days }
    container_delete_retention_policy { days = var.goldens_soft_delete_days }
  }

  tags = var.tags
}

resource "azurerm_storage_container" "goldens" {
  count                 = var.goldens_enabled ? 1 : 0
  name                  = "goldens"
  storage_account_id    = azurerm_storage_account.goldens[0].id
  container_access_type = "private"
}

resource "azurerm_role_assignment" "app_reads_goldens" {
  count                = var.goldens_enabled ? 1 : 0
  scope                = azurerm_storage_account.goldens[0].id
  role_definition_name = "Storage Blob Data Contributor"
  principal_id         = azurerm_user_assigned_identity.app.principal_id
}
