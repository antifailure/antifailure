# Asking the way a customer asks.
#
# Every other rule in this module reads a metric Azure collects from inside the
# platform. All of them can be green while the service is unreachable, because
# none of them touches DNS, the custom domain binding, the certificate, or the
# ingress. The first deploy of this application answered /health with 200 for
# thirteen minutes while every endpoint that read a table returned 500, and no
# platform metric said anything at all.
#
# HOW FAR OUTSIDE, STATED HONESTLY. These tests run from Microsoft managed
# agents in other regions. That is outside this stack, outside its resource
# group, outside its region and outside its network, and it is not outside
# Azure. An Azure wide control plane failure could take the prober and the
# service together, and this rule would report nothing. A third party prober is
# the only thing that closes that, and it cannot be created from Terraform with
# the credentials this repository holds, so it is written down here rather than
# implied away.

resource "azurerm_application_insights" "probe" {
  name                = "${var.name}-probe"
  location            = var.location
  resource_group_name = var.resource_group_name
  application_type    = "web"

  # Workspace based, into the workspace the foundation module already created.
  # A classic component keeps its own store, on its own retention, that nothing
  # else in this stack queries.
  workspace_id = var.log_analytics_id

  # This component exists to hold two availability tests. Nothing sends it
  # telemetry: the application exports Prometheus counters at /metrics and has
  # no Application Insights SDK in it. Sampling is therefore irrelevant and the
  # ingestion it bills for is the availability results themselves.
  tags = var.tags
}

# The availability test.
#
# TWO FAILURES RATHER THAN ONE, and it is stronger than it looks. retry_enabled
# makes an agent repeat a failed request before it reports a failure at all, so
# every failure this rule counts is already not a single dropped packet. On top
# of that the alert needs TWO locations to be failing inside the same window.
# One region's networking problem cannot reach that on its own.
resource "azurerm_application_insights_standard_web_test" "readyz" {
  name                    = "${var.name}-readyz"
  resource_group_name     = var.resource_group_name
  location                = var.location
  application_insights_id = azurerm_application_insights.probe.id

  geo_locations = var.web_test_locations
  frequency     = var.web_test_frequency_seconds
  timeout       = 30
  retry_enabled = true
  enabled       = true

  description = "Asks ${var.probe_url} the way a customer does, from outside this stack."

  request {
    url       = var.probe_url
    http_verb = "GET"
    # A redirect to a sign-in page is not readiness. /readyz answers 200 or 503
    # and nothing else, so a 3xx here means something is in front of the app
    # that should not be.
    follow_redirects_enabled = false
    # Nothing on the page is fetched. This is an endpoint returning a small JSON
    # body, and asking for its dependent requests would bill for parsing a
    # document that does not exist.
    parse_dependent_requests_enabled = false
  }

  validation_rules {
    # 503 is the honest answer from an application whose database is gone, and
    # it must be a failure here. Without this the test passes on any response at
    # all and the rule becomes "is something listening".
    expected_status_code = 200
    ssl_check_enabled    = false
  }

  tags = var.tags
}

resource "azurerm_monitor_metric_alert" "unreachable" {
  name                = "${var.name}-unreachable"
  resource_group_name = var.resource_group_name
  scopes = [
    azurerm_application_insights_standard_web_test.readyz.id,
    azurerm_application_insights.probe.id,
  ]
  severity = 0

  description = "${var.probe_url} failed from two locations. Runbook: ${local.runbooks}/availability/"

  # Fifteen minutes and not ten. Azure Monitor accepts PT1M, PT5M, PT15M, PT30M,
  # PT1H, PT6H, PT12H and P1D for a window and nothing in between, which
  # `terraform validate` says and an apply would also have said, later.
  window_size = "PT15M"
  frequency   = "PT5M"

  application_insights_web_test_location_availability_criteria {
    web_test_id           = azurerm_application_insights_standard_web_test.readyz.id
    component_id          = azurerm_application_insights.probe.id
    failed_location_count = 2
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}

# ---------------------------------------------------------------------------
# The certificate.
#
# Azure emits no metric for the remaining life of a Container Apps managed
# certificate. What it does have is a web test that can be told to fail when the
# certificate it is presented has fewer than N days left, which is the same
# question asked from the other end.
#
# A SEPARATE TEST FROM THE ONE ABOVE, DELIBERATELY. Putting the SSL check on the
# availability test would make a certificate with nineteen days left page
# somebody at three in the morning as an outage. This one runs every fifteen
# minutes from a single location, is severity 3, and is the cheapest rule here.
#
# A managed certificate renews itself. This fires when the renewal did not
# happen, which is the only interesting case, and three weeks is enough time to
# do something about it during working hours.
# ---------------------------------------------------------------------------
resource "azurerm_application_insights_standard_web_test" "certificate" {
  name                    = "${var.name}-certificate"
  resource_group_name     = var.resource_group_name
  location                = var.location
  application_insights_id = azurerm_application_insights.probe.id

  # One location and the slowest frequency Azure offers. A certificate does not
  # expire in one region.
  geo_locations = [var.web_test_locations[0]]
  frequency     = 900
  timeout       = 30
  retry_enabled = false

  description = "Fails when the certificate on ${var.probe_url} has fewer than ${var.certificate_warning_days} days left."

  request {
    url                              = var.probe_url
    http_verb                        = "GET"
    follow_redirects_enabled         = false
    parse_dependent_requests_enabled = false
  }

  validation_rules {
    expected_status_code        = 200
    ssl_check_enabled           = true
    ssl_cert_remaining_lifetime = var.certificate_warning_days
  }

  tags = var.tags
}

resource "azurerm_monitor_metric_alert" "certificate_expiring" {
  name                = "${var.name}-certificate-expiring"
  resource_group_name = var.resource_group_name
  scopes = [
    azurerm_application_insights_standard_web_test.certificate.id,
    azurerm_application_insights.probe.id,
  ]
  severity = 3

  description = "The certificate on ${var.probe_url} has fewer than ${var.certificate_warning_days} days left, or that test could not complete. Runbook: ${local.runbooks}/certificate/"

  window_size = "PT30M"
  frequency   = "PT15M"

  application_insights_web_test_location_availability_criteria {
    web_test_id           = azurerm_application_insights_standard_web_test.certificate.id
    component_id          = azurerm_application_insights.probe.id
    failed_location_count = 1
  }

  action {
    action_group_id = azurerm_monitor_action_group.pager.id
  }

  tags = var.tags
}
