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
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-cms-test/providers/Microsoft.KeyVault/vaults/cms-test"
      vault_uri = "https://cms-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "cms-test"
  resource_group_name  = "af-cms-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "existing_vault_secret_reaches_only_the_server" {
  command = plan

  variables {
    cms_anthropic_secret_name = "cms-anthropic-api-key"
  }

  assert {
    condition = one([
      for secret in azurerm_container_app.this.secret : secret.key_vault_secret_id
      if secret.name == "cms-anthropic-api-key"
    ]) == "https://cms-test.vault.azure.net/secrets/cms-anthropic-api-key"
    error_message = "The CMS key must be an external Key Vault reference, not a Terraform-managed value."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_CMS_ANTHROPIC_API_KEY"
    ]) == "cms-anthropic-api-key"
    error_message = "Only the server container may receive the CMS key reference."
  }
}

run "self_hosted_without_a_key_has_no_ai_env" {
  command = plan

  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env
      if env.name == "AF_CMS_ANTHROPIC_API_KEY"
    ]) == 0
    error_message = "The optional AI key must not be set to an empty value."
  }
}
