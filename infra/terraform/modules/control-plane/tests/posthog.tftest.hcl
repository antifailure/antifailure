# The PostHog proxy and the process's own PostHog reporting reach the container.
#
# THE FAILURE. docs/reference/control-plane.md documented AF_POSTHOG_REGION and
# AF_POSTHOG_PROJECT_KEY, web/apps/api read both, the marketing site was built
# to send its product analytics to /ph on this container, and no apply of this
# module could set either variable. tools/wirecheck saw it, after the docs test
# and the config test had both stayed green, because those two ask whether a
# variable is documented and read, which a variable nothing can deliver
# satisfies exactly. These runs are the module half of the proof; the
# application half is web/apps/api/test/posthog-proxy.test.ts, which never
# reads Terraform and would stay green against a module that set nothing.

mock_provider "azurerm" {
  override_during = plan

  mock_data "azurerm_client_config" {
    defaults = {
      tenant_id       = "00000000-0000-0000-0000-000000000001"
      object_id       = "00000000-0000-0000-0000-000000000002"
      subscription_id = "00000000-0000-0000-0000-000000000003"
    }
  }
}

mock_provider "random" {
  override_during = plan
}

variables {
  name                 = "posthog-test"
  resource_group_name  = "af-posthog-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "region_and_key_reach_the_process_as_plain_values" {
  command = plan

  variables {
    posthog_region      = "eu"
    posthog_project_key = "phc_test_only_not_a_live_project"
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.value
      if env.name == "AF_POSTHOG_REGION"
    ]) == "eu"
    error_message = "posthog_region must reach the container unchanged in AF_POSTHOG_REGION."
  }

  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.value
      if env.name == "AF_POSTHOG_PROJECT_KEY"
    ]) == "phc_test_only_not_a_live_project"
    error_message = "posthog_project_key must reach the container unchanged in AF_POSTHOG_PROJECT_KEY."
  }

  # A PostHog project key is public by design and ships inside the site's
  # JavaScript. Resolving it from the vault would fail the revision on an entry
  # nobody created, for a value that was never a secret.
  assert {
    condition = alltrue([
      for env in azurerm_container_app.this.template[0].container[0].env :
      env.secret_name == null || env.secret_name == ""
      if env.name == "AF_POSTHOG_REGION" || env.name == "AF_POSTHOG_PROJECT_KEY"
    ])
    error_message = "The PostHog region and project key are public values and must be set as values, not resolved from the vault."
  }
}

run "nothing_configured_sets_nothing_rather_than_an_empty_string" {
  command = plan

  # ABSENT RATHER THAN EMPTY. The application mounts no proxy when the region
  # is unset, and answers a site pointed at it 404 rather than forwarding to a
  # cloud the operator did not choose. An empty string would be refused at
  # startup as neither us nor eu, which is a failed revision for a feature that
  # was never asked for.
  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env
      if env.name == "AF_POSTHOG_REGION" || env.name == "AF_POSTHOG_PROJECT_KEY"
    ]) == 0
    error_message = "With no PostHog configured both variables must be absent, not empty strings."
  }
}

run "a_region_outside_the_two_clouds_is_refused_at_plan_time" {
  command = plan

  variables {
    posthog_region = "ap"
  }

  expect_failures = [var.posthog_region]
}

run "a_key_with_no_region_is_refused_at_plan_time" {
  command = plan

  variables {
    posthog_project_key = "phc_test_only_not_a_live_project"
  }

  expect_failures = [var.posthog_project_key]
}
