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

  # THE PRICE IS THE THIRD REQUIRED VARIABLE AND NOTHING HERE ASSERTED IT.
  #
  # The two assertions above prove the credentials arrive. src/billing/plans.ts
  # requires three settings and treats a partial configuration as a REFUSAL:
  # with the key and the webhook secret present and the price missing, billing
  # is off entirely and the process says "billing is OFF and partially
  # configured". So a module that delivered both secrets and dropped the price
  # would satisfy every assertion this file had, deploy cleanly, and take no
  # money, which is indistinguishable at the infrastructure layer from the state
  # this control plane is in today.
  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env
      if env.name == "AF_STRIPE_PRICE_TEAM" && env.value == "price_test"
    ]) == 1
    error_message = "The Team price must reach the process environment, or billing is off however many credentials arrived."
  }

  # AND IT MUST ARRIVE AS A VALUE, NOT AS A VAULT REFERENCE.
  #
  # A price identifier is not a credential: it is in the checkout URL of every
  # person who buys, and it is written in plain text in production.tfvars. If
  # somebody "tidied" it into the secret block beside the two real credentials,
  # the container would reference a vault entry nobody has created, and the
  # revision would fail to start on a value that was never secret.
  assert {
    condition = alltrue([
      for env in azurerm_container_app.this.template[0].container[0].env :
      env.secret_name == null || env.secret_name == ""
      if env.name == "AF_STRIPE_PRICE_TEAM"
    ])
    error_message = "The Team price is a public identifier and must be set as a value, not resolved from the vault."
  }

  # NO ENTERPRISE PRICE, AND ITS ABSENCE IS THE DESIGN.
  #
  # Enterprise is arranged with a person, so this module deliberately has no
  # input for it and plans.ts deliberately does not require it. A future edit
  # that added one with a default would make checkout offer a plan that reaches
  # Stripe with a price nobody configured. Asserted here because the module is
  # where such an addition would be made.
  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env.name
      if env.name == "AF_STRIPE_PRICE_ENTERPRISE"
    ]) == 0
    error_message = "Enterprise has no price and is arranged with a person; the module must not emit one."
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

  # And no payment SETTING either, which the run above did not cover. A control
  # plane that takes no money must not carry a half configuration: plans.ts
  # reports one variable of the three as "billing is OFF and partially
  # configured" and refuses to take payment, which is the right answer and a
  # confusing one to arrive at from a deployment nobody meant to change.
  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env.name
      if startswith(env.name, "AF_STRIPE_")
    ]) == 0
    error_message = "Billing disabled must leave no AF_STRIPE_ setting on the container."
  }
}
