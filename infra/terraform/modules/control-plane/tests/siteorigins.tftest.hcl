# Every hostname the marketing site is served on reaches the container as an
# allowed origin.
#
# THE FAILURE. antifailure.dev and www.antifailure.dev are two custom domains on
# one Azure Static Web App, both Ready, both serving every page, and neither
# redirects to the other, because a Static Web Apps route rule matches on a PATH
# and its configuration schema has no hostname condition at all. site_origin was
# a single string holding the apex, so the control plane answered 403 to every
# cross origin call a page on www made: the analytics beacon silently, the
# enterprise contact form as "Could not reach the server", the careers
# application form on something somebody had just filled in.
#
# WHY THE MODULE IS WORTH A TEST OF ITS OWN. The application half is proved by
# web/apps/api/test/siteorigins.test.ts, which drives real requests at a real
# server. That suite would stay green forever against a module that delivered
# only the first origin, or rewrote the separator, or dropped the variable
# entirely, because it never reads Terraform. The three runs below are the three
# states an operator can put this module in.

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
  name                 = "origins-test"
  resource_group_name  = "af-origins-test"
  location             = "centralus"
  diagnostics_enabled  = false
  github_client_id     = "test-client"
  github_client_secret = "test-only-not-a-credential"
  github_redirect_uri  = "https://example.test/auth/github/callback"
  signin_allowlist     = []
  pool_max             = 1
  max_replicas         = 1
}

run "every_configured_origin_reaches_the_process" {
  command = plan

  variables {
    site_origin = "https://example.test,https://www.example.test"
  }

  # ONE VARIABLE CARRYING BOTH, comma separated, which is exactly what
  # siteOriginsFrom parses. The module does nothing to the string, so this
  # asserts the absence of a translation rather than the correctness of one. A
  # second variable would be a second chance to configure the beacon and the
  # forms differently, and that presents as one form failing with a network
  # error on a site whose analytics work.
  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.value
      if env.name == "AF_SITE_ORIGIN"
    ]) == "https://example.test,https://www.example.test"
    error_message = "Every origin the site is served on must reach the container, comma separated, in AF_SITE_ORIGIN."
  }

  # A VALUE, NOT A VAULT REFERENCE. An origin is a public hostname, it is
  # written in plain text in the tfvars, and referencing it as a secret would
  # make the revision fail to start on a vault entry nobody created.
  assert {
    condition = alltrue([
      for env in azurerm_container_app.this.template[0].container[0].env :
      env.secret_name == null || env.secret_name == ""
      if env.name == "AF_SITE_ORIGIN"
    ])
    error_message = "The site origins are public hostnames and must be set as a value, not resolved from the vault."
  }
}

run "no_origins_configured_sets_nothing_rather_than_an_empty_string" {
  command = plan

  variables {
    site_origin = ""
  }

  # ABSENT RATHER THAN EMPTY, and this is a CORS decision so the direction
  # matters. The application refuses every cross origin caller when the variable
  # is unset and when it is empty, so both are safe; what is not safe is a
  # future edit that made an empty value mean something else. An absent variable
  # cannot be misread by anything.
  assert {
    condition = length([
      for env in azurerm_container_app.this.template[0].container[0].env : env
      if env.name == "AF_SITE_ORIGIN"
    ]) == 0
    error_message = "With no origin configured the variable must be absent, not an empty string."
  }
}

run "one_origin_is_still_one_origin" {
  command = plan

  variables {
    site_origin = "https://example.test"
  }

  # The shape every installation already has. A single origin must arrive
  # unchanged, or upgrading would break every deployment that already sets this
  # correctly, and this variable is one docs/reference/stability.md promised.
  assert {
    condition = one([
      for env in azurerm_container_app.this.template[0].container[0].env : env.value
      if env.name == "AF_SITE_ORIGIN"
    ]) == "https://example.test"
    error_message = "A single origin must arrive unchanged, byte for byte."
  }
}
