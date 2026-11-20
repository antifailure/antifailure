mock_provider "azurerm" {
  override_during = plan

  mock_data "azurerm_client_config" {
    defaults = {
      tenant_id       = "00000000-0000-0000-0000-000000000001"
      object_id       = "00000000-0000-0000-0000-000000000002"
      subscription_id = "00000000-0000-0000-0000-000000000003"
    }
  }

  mock_resource "azurerm_key_vault" {
    defaults = {
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-agentrail-test/providers/Microsoft.KeyVault/vaults/agentrail-test"
      vault_uri = "https://agentrail-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "agentrail-test"
  resource_group_name  = "af-agentrail-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "enabled_sends_both_vault_references_as_a_pair" {
  command = plan

  variables {
    agentrail_enabled = true
  }

  assert {
    condition = one([
      for secret in azurerm_container_app.this.secret : secret.key_vault_secret_id
      if secret.name == "agentrail-secret"
    ]) == "https://agentrail-test.vault.azure.net/secrets/agentrail-secret"
    error_message = "The Agentrail source secret must be an external Key Vault reference."
  }

  assert {
    condition = one([
      for secret in azurerm_container_app.this.secret : secret.key_vault_secret_id
      if secret.name == "agentrail-hash-key"
    ]) == "https://agentrail-test.vault.azure.net/secrets/agentrail-hash-key"
    error_message = "The Agentrail hash key must be an external Key Vault reference."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_AGENTRAIL_SECRET"
    ]) == "agentrail-secret"
    error_message = "AF_AGENTRAIL_SECRET must come from the agentrail-secret reference."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_AGENTRAIL_HASH_KEY"
    ]) == "agentrail-hash-key"
    error_message = "AF_AGENTRAIL_HASH_KEY must come from the agentrail-hash-key reference. The app refuses to start on one of the pair."
  }
}

run "off_by_default_sends_neither" {
  command = plan

  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env
      if startswith(env.name, "AF_AGENTRAIL_")
    ]) == 0
    error_message = "A deployment that did not turn Agentrail on must not report to it."
  }

  assert {
    condition = length([
      for secret in azurerm_container_app.this.secret : secret
      if startswith(secret.name, "agentrail-")
    ]) == 0
    error_message = "A deployment that did not turn Agentrail on must not reference its vault secrets."
  }
}
