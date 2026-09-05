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
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-billing-test/providers/Microsoft.KeyVault/vaults/billing-test"
      vault_uri = "https://billing-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "billing-test"
  resource_group_name  = "af-billing-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "billing_references_the_configured_secret_names" {
  command = plan

  variables {
    stripe_price_team                 = "price_test"
    stripe_secret_key_secret_name     = "payments-api"
    stripe_webhook_secret_secret_name = "payments-webhook"
  }

  assert {
    condition = one([
      for secret in azurerm_container_app.this.secret : secret.key_vault_secret_id
      if secret.name == "stripe-secret-key"
    ]) == "https://billing-test.vault.azure.net/secrets/payments-api"
    error_message = "The serving application must reference the configured Stripe API secret."
  }

  assert {
    condition = one([
      for secret in azurerm_container_app.this.secret : secret.key_vault_secret_id
      if secret.name == "stripe-webhook-secret"
    ]) == "https://billing-test.vault.azure.net/secrets/payments-webhook"
    error_message = "The serving application must reference the configured Stripe webhook secret."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_STRIPE_SECRET_KEY"
    ]) == "stripe-secret-key"
    error_message = "The API credential reference must reach the process environment."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_STRIPE_WEBHOOK_SECRET"
    ]) == "stripe-webhook-secret"
    error_message = "The webhook credential reference must reach the process environment."
  }
}

run "billing_off_has_no_payment_secret_references" {
  command = plan

  assert {
    condition = length([
      for secret in azurerm_container_app.this.secret : secret.name
      if startswith(secret.name, "stripe-")
    ]) == 0
    error_message = "Billing disabled must not require payment secrets."
  }
}
