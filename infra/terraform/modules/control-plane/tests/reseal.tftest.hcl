# The re-sealing job across the whole life of a rotation.
#
# The job used to exist only while provider_key_secret_enabled was true, and the
# last step of a rotation sets it to false. So the step that finished the first
# rotation destroyed the job, and every later rotation had nothing to run. It
# also referenced the v1 secret unconditionally, which a plan cannot resolve once
# that secret is gone. Each run below is one state a deployment passes through.

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
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-reseal-test/providers/Microsoft.KeyVault/vaults/reseal-test"
      vault_uri = "https://reseal-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "reseal-test"
  resource_group_name  = "af-reseal-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "before_any_rotation_the_job_holds_the_generated_key" {
  command = plan

  assert {
    condition     = length(azurerm_container_app_job.reseal) == 1
    error_message = "An installation with the generated sealing key must have a re-sealing job."
  }

  assert {
    condition     = contains([for e in azurerm_container_app_job.reseal[0].template[0].container[0].env : e.name], "AF_PROVIDER_KEY_SECRET")
    error_message = "Before a rotation the job must read the generated v1 key."
  }

  assert {
    condition     = azurerm_container_app_job.reseal[0].template[0].container[0].command == tolist(["node", "backup-cli.mjs", "reseal"])
    error_message = "The job must run the image's launcher, which is what loads every table its edition seals."
  }
}

run "after_v1_is_retired_the_job_survives_without_it" {
  command = plan

  variables {
    provider_key_secret_enabled = false
    provider_key_secrets_name   = "provider-key-secrets"
    provider_key_version        = "v2"
  }

  assert {
    condition     = length(azurerm_container_app_job.reseal) == 1
    error_message = "Retiring v1 destroyed the re-sealing job, so the next rotation has nothing to run."
  }

  assert {
    condition     = !contains([for s in azurerm_container_app_job.reseal[0].secret : s.name], "provider-key-secret")
    error_message = "After v1 is retired the job still references provider-key-secret, which no longer exists in the vault."
  }

  assert {
    condition     = !contains([for e in azurerm_container_app_job.reseal[0].template[0].container[0].env : e.name], "AF_PROVIDER_KEY_SECRET")
    error_message = "After v1 is retired the job still reads AF_PROVIDER_KEY_SECRET."
  }

  assert {
    condition     = contains([for e in azurerm_container_app_job.reseal[0].template[0].container[0].env : e.name], "AF_PROVIDER_KEY_SECRETS")
    error_message = "After v1 is retired the job must read the key set that replaced it."
  }
}

run "with_no_sealing_key_there_is_no_job" {
  command = plan

  variables {
    provider_key_secret_enabled = false
    provider_key_secrets_name   = ""
  }

  assert {
    condition     = length(azurerm_container_app_job.reseal) == 0
    error_message = "An installation with no sealing key at all has nothing to re-seal and must have no job."
  }
}
