# The enterprise edition reaches the container as four variables and two vault
# references, or not at all.
#
# MEASURED BEFORE IT WAS WRITTEN, which is why the assertions are the shape they
# are. The enterprise entry point was run against every combination of these
# and it exits before listening without AF_EE_SSO_KEY, whatever the licence
# says, and refuses at start-up a licence with no AF_ORG or no trusted key. A
# template that carried three of the four would apply cleanly and produce a
# revision that never listens, at zero traffic, where nobody is looking.

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
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-edition-test/providers/Microsoft.KeyVault/vaults/edition-test"
      vault_uri = "https://edition-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "edition-test"
  resource_group_name  = "af-edition-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "the_enterprise_edition_carries_all_four" {
  command = plan

  variables {
    enterprise_edition      = true
    license_org             = "antifailure"
    license_public_keys     = "hosted-test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
    license_key_secret_name = "hosted-licence"
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_EE_SSO_KEY"
    ]) == "ee-sso-key"
    error_message = "The single sign-on sealing key must reach the process: the enterprise entry point exits before listening without it."
  }

  assert {
    condition     = contains([for s in azurerm_container_app.this.secret : s.name], "ee-sso-key")
    error_message = "AF_EE_SSO_KEY names the ee-sso-key secret, so the app must reference it or Azure refuses the revision."
  }

  assert {
    condition     = contains(keys(azurerm_key_vault_secret.owned), "ee-sso-key")
    error_message = "The sealing key is owned: Terraform generates it into the vault, so no person ever holds it."
  }

  assert {
    condition = one([
      for secret in azurerm_container_app.this.secret : secret.key_vault_secret_id
      if secret.name == "license-key"
    ]) == "https://edition-test.vault.azure.net/secrets/hosted-licence"
    error_message = "The licence must be referenced by the configured name and never read by the plan."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.secret_name
      if env.name == "AF_LICENSE_KEY"
    ]) == "license-key"
    error_message = "The licence reference must reach the process environment."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.value
      if env.name == "AF_ORG"
    ]) == "antifailure"
    error_message = "AF_ORG must reach the process: a licence with no organization to compare against is refused at start-up."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.value
      if env.name == "AF_LICENSE_PUBLIC_KEYS"
    ]) == "hosted-test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
    error_message = "AF_LICENSE_PUBLIC_KEYS must reach the process: no build carries a stamped key, so without it no licence verifies."
  }
}

run "the_community_edition_carries_none_of_them" {
  command = plan

  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env.name
      if contains(["AF_EE_SSO_KEY", "AF_LICENSE_KEY", "AF_ORG", "AF_LICENSE_PUBLIC_KEYS"], env.name)
    ]) == 0
    error_message = "A deployment running the community image reads none of the enterprise variables, and a licence reference it cannot use is a vault secret it must not need."
  }

  assert {
    condition     = !contains(keys(azurerm_key_vault_secret.owned), "ee-sso-key")
    error_message = "The community edition must not generate a sealing key nothing reads."
  }
}

run "an_edition_with_no_organization_is_refused_at_plan" {
  command = plan

  variables {
    enterprise_edition  = true
    license_org         = ""
    license_public_keys = "hosted-test=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
  }

  expect_failures = [azurerm_container_app.this]
}

run "an_edition_with_no_trusted_key_is_refused_at_plan" {
  command = plan

  variables {
    enterprise_edition  = true
    license_org         = "antifailure"
    license_public_keys = "  "
  }

  expect_failures = [azurerm_container_app.this]
}
