# The application rules read the metric they claim to, at the number the
# operator set.
#
# THE FAILURE. This module had ten rules and every one of them watched a
# failure: a 5xx, a restart, a missing replica, a database that stopped
# answering. A control plane that answered every request correctly in twelve
# seconds paged nobody, and on a busy day that is the likeliest degradation and
# the one a customer notices first. The slow-responses rule is the fix, and
# these runs are the proof that it is wired to the right series with the right
# shape, which a plan against a real subscription would also prove but only for
# the person holding the credential.
#
# Nothing here reaches Azure. The provider is mocked, so a computed id is a
# generated string, and the assertions compare references rather than literal
# ids where an id is involved.

mock_provider "azurerm" {
  override_during = plan

  # The provider parses these two ids at plan time, as Azure resource ids
  # with a fixed number of segments, and the mock's generated string is not
  # one. Supplying ids of the right shape is what lets a plan against the
  # mock get as far as the assertions.
  override_resource {
    target = azurerm_monitor_action_group.pager
    values = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-latency-test/providers/Microsoft.Insights/actionGroups/latency-test-pager"
    }
  }

  override_resource {
    target = azurerm_application_insights.probe
    values = {
      id = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-latency-test/providers/Microsoft.Insights/components/latency-test-probe"
    }
  }
}

variables {
  name                = "latency-test"
  resource_group_name = "af-latency-test"
  location            = "centralus"
  log_analytics_id    = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-latency-test/providers/Microsoft.OperationalInsights/workspaces/latency-test-logs"
  container_app_id    = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-latency-test/providers/Microsoft.App/containerapps/latency-test-app"
  postgres_server_id  = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-latency-test/providers/Microsoft.DBforPostgreSQL/flexibleServers/latency-test-pg"
  job_ids = {
    bootstrap = "/subscriptions/00000000-0000-0000-0000-000000000003/resourceGroups/af-latency-test/providers/Microsoft.App/jobs/latency-test-bootstrap"
  }
  probe_url          = "https://latency.example.test/readyz"
  usable_connections = 35
  min_replicas       = 2
  alert_emails       = ["oncall@example.test"]
}

run "slow_responses_reads_the_ingress_response_time" {
  command = plan

  # The metric, the namespace and the aggregation were read from the running
  # production app with `az monitor metrics list-definitions`. ResponseTime is
  # emitted in milliseconds by the ingress and supports Average, and a rule on
  # any other name creates cleanly and evaluates nothing.
  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.criteria[0].metric_name == "ResponseTime"
    error_message = "The slow-responses rule must read ResponseTime, the only latency series a container app emits."
  }

  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.criteria[0].metric_namespace == "Microsoft.App/containerApps"
    error_message = "ResponseTime lives in the Microsoft.App/containerApps namespace."
  }

  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.criteria[0].aggregation == "Average"
    error_message = "Average, not Maximum: one request at the fifteen second statement timeout would fire a Maximum rule every time it happened."
  }

  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.criteria[0].operator == "GreaterThan"
    error_message = "The rule fires when the average is above the threshold, not below it."
  }

  # The default, restated in the stack and in both tfvars files. A rule at a
  # different number than the one written beside the measurement is a rule
  # nobody can explain at three in the morning.
  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.criteria[0].threshold == 2000
    error_message = "The default threshold is 2000 ms, and it must reach the rule unchanged."
  }

  # Fifteen minutes evaluated every five is the nearest a static threshold
  # gets to "two consecutive evaluations", which Azure only offers on a dynamic
  # one. Ten is what was written first, and the provider refused it: PT5M and
  # PT15M are the only sizes between one minute and half an hour.
  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.window_size == "PT15M"
    error_message = "The window is fifteen minutes, so one slow five minutes is diluted by the ten before it."
  }

  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.frequency == "PT5M"
    error_message = "Evaluated every five minutes, the same cadence as the 5xx rule."
  }

  # Severity 1 is what the module gives the 5xx rule: failing and probably
  # visible. A slow service is visible in exactly the same way.
  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.severity == 1
    error_message = "Slow responses page at severity 1, the same rank as server errors."
  }

  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.name == "latency-test-slow-responses"
    error_message = "The rule is named like its neighbours: the stack's prefix, then what it watches."
  }

  # ResponseTime carries only statusCodeCategory and statusCode, so there is no
  # dimension that could exclude a probe, and a dimension on status would hide
  # the timeouts a stall ends in. The rule reads the whole population.
  assert {
    condition     = length(azurerm_monitor_metric_alert.slow_responses.criteria[0].dimension) == 0
    error_message = "The rule must read every request; a status dimension would hide the 5xx a stall turns into."
  }

  # The runbook travels in the description because that is the text in the
  # email and the SMS, and the number travels with it so the reader knows what
  # was crossed without opening a portal.
  assert {
    condition     = strcontains(azurerm_monitor_metric_alert.slow_responses.description, "2000 ms")
    error_message = "The description must carry the threshold, because it is the only text the person paged will read."
  }

  assert {
    condition     = endswith(azurerm_monitor_metric_alert.slow_responses.description, "/runbooks/slow-responses/")
    error_message = "The description must end with the slow-responses runbook URL, like every other rule here."
  }
}

run "slow_responses_pages_through_the_same_group_as_every_other_rule" {
  command = plan

  # The same pager as every other rule. A second action group is a second
  # list of people to keep in step, and the one that is wrong is always the
  # one that was needed.
  assert {
    condition     = length(azurerm_monitor_metric_alert.slow_responses.action) == 1
    error_message = "Exactly one action, the pager."
  }

  assert {
    condition     = one([for a in azurerm_monitor_metric_alert.slow_responses.action : a.action_group_id]) == azurerm_monitor_action_group.pager.id
    error_message = "The rule must page through the module's own action group, not one of its own."
  }
}

run "the_threshold_reaches_the_rule_and_its_description" {
  command = plan

  variables {
    response_time_threshold_ms = 3500
  }

  assert {
    condition     = azurerm_monitor_metric_alert.slow_responses.criteria[0].threshold == 3500
    error_message = "An operator's threshold must reach the rule unchanged."
  }

  assert {
    condition     = strcontains(azurerm_monitor_metric_alert.slow_responses.description, "3500 ms")
    error_message = "The description must carry the operator's number, not the default."
  }
}

run "slow_responses_is_listed_with_every_other_rule" {
  command = plan

  # `terraform output alert_names` is how somebody without a portal answers
  # what is watched. A rule missing from the list is a rule that list lies
  # about.
  assert {
    condition     = contains(output.alert_names, "latency-test-slow-responses")
    error_message = "alert_names must include the slow-responses rule, or the output stops answering what is watched."
  }
}

run "a_zero_threshold_is_refused" {
  command = plan

  variables {
    response_time_threshold_ms = 0
  }

  expect_failures = [
    var.response_time_threshold_ms,
  ]
}
