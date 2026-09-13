# The rotation's two variables, set where an operator sets them.
#
# The runbook tells an operator to put provider_key_secrets_name and
# provider_key_version in this stack's tfvars. The module declared both and this
# stack declared neither, and a tfvars key the stack does not declare is a warning
# Terraform prints and then ignores: the module kept its empty defaults, and the
# second step of every rotation changed nothing while appearing to apply. The module
# tests could not see it, because they set the module's variables directly. This
# plans the stack, so a value only counts if it arrives through the stack.

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
      id        = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-rotation-test/providers/Microsoft.KeyVault/vaults/rotation-test"
      vault_uri = "https://rotation-test.vault.azure.net/"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  subscription_id      = "00000000-0000-0000-0000-000000000003"
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  # The module refuses a plan whose replicas could open more connections than the
  # database SKU hands out, and the stack's defaults exceed it. The module's own
  # tests size it down the same way; the connection budget is not what this checks.
  pool_max     = 1
  max_replicas = 1
}

run "a_rotation_set_in_this_stack_reaches_the_app_and_the_job" {
  command = plan

  variables {
    provider_key_secrets_name = "provider-key-secrets"
    provider_key_version      = "v2"
  }

  assert {
    condition     = contains(module.control_plane.sealing_key_environment.app, "AF_PROVIDER_KEY_SECRETS")
    error_message = "provider_key_secrets_name set on the stack did not reach the container app: the stack is not passing it to the module."
  }

  assert {
    condition     = module.control_plane.sealing_key_environment.sealing_version == "v2"
    error_message = "provider_key_version set on the stack did not reach the container app: the stack is not passing it to the module."
  }

  assert {
    condition     = contains(module.control_plane.sealing_key_environment.reseal_job, "AF_PROVIDER_KEY_SECRETS")
    error_message = "provider_key_secrets_name set on the stack did not reach the re-sealing job."
  }
}

run "an_installation_that_never_rotated_sets_neither" {
  command = plan

  assert {
    condition     = !contains(module.control_plane.sealing_key_environment.app, "AF_PROVIDER_KEY_SECRETS")
    error_message = "With nothing set on the stack, the app must not read a key set it was never given."
  }

  assert {
    condition     = module.control_plane.sealing_key_environment.sealing_version == null
    error_message = "With nothing set on the stack, the app must not be told a sealing version."
  }

  assert {
    condition     = contains(module.control_plane.sealing_key_environment.app, "AF_PROVIDER_KEY_SECRET")
    error_message = "An installation that never rotated must still hold its generated v1 key."
  }
}
