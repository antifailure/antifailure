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
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-scale-test/providers/Microsoft.KeyVault/vaults/scale-test"
      vault_uri = "https://scale-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "scale-test"
  resource_group_name  = "af-scale-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
}

# The scaler's concurrency is declared, not inherited. Production's first
# release ran on the platform default of ten per replica because no rule was
# written; this holds the number where the tfvars put it.
run "the_http_concurrency_rule_carries_the_configured_number" {
  command = plan

  variables {
    max_replicas        = 12
    concurrent_requests = 40
  }

  assert {
    condition     = one(azurerm_container_app.this.template[0].http_scale_rule[*].concurrent_requests) == "40"
    error_message = "The serving application must declare the configured HTTP concurrency to the scaler."
  }

  assert {
    condition     = azurerm_container_app.this.template[0].max_replicas == 12
    error_message = "The serving application must carry the configured replica ceiling."
  }
}

run "an_impossible_concurrency_is_refused" {
  command = plan

  variables {
    concurrent_requests = 0
  }

  expect_failures = [var.concurrent_requests]
}
