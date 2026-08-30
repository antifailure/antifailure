# The thing that wakes somebody up.
#
# Before this module there was no azurerm_monitor_metric_alert and no
# azurerm_monitor_action_group anywhere in infra/. Diagnostics flowed into Log
# Analytics and a consumption budget watched the bill, so the record of an
# outage was complete and nobody was told about it. For a paid service that is
# the gap that matters most, because every other gap is discovered by a customer
# telling you.
#
# WHAT THIS IS NOT. It is not sophisticated and does not try to be: there is no
# error budget, no burn rate, no dependency graph, no on call rotation. Eight
# rules, one action group, one runbook each. The gap was existence.
#
# EVERY RULE CARRIES ITS RUNBOOK IN ITS DESCRIPTION, not in a comment here.
# The description is the text Azure puts in the email and the SMS, so it is the
# only place the person who has just been woken up will actually read. A rule
# whose runbook lives only in a repository is a rule that sends somebody to read
# code at three in the morning.

terraform {
  required_version = ">= 1.9.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.16"
    }
  }
}

locals {
  runbooks = "https://antifailure.dev/docs/self-hosting/runbooks"

  has_email = length(var.alert_emails) > 0
  has_sms   = var.alert_sms_number != "" && var.alert_sms_country_code != ""

  # Azure caps this at 12 characters and puts it at the front of every SMS, so
  # it is the first thing read on a phone at three in the morning. Alphanumerics
  # only, because the documented constraint is a length and the accepted
  # character set is not written down anywhere authoritative.
  short_name = substr(replace("${var.name}alerts", "-", ""), 0, 12)
}

# The action group, and the guard that makes it mean something.
#
# Azure accepts an action group with no receivers at all. It creates cleanly,
# every alert attaches to it, every rule reports healthy, and nothing is ever
# delivered to anybody. That is a worse state than having no alerting, because
# it looks like alerting. The precondition refuses it at plan time.
resource "azurerm_monitor_action_group" "pager" {
  name                = "${var.name}-pager"
  resource_group_name = var.resource_group_name
  short_name          = local.short_name
  # Action groups are a global resource. Naming the region here would be a
  # promise Azure does not keep.
  location = "global"

  dynamic "email_receiver" {
    for_each = var.alert_emails
    content {
      name          = "email-${email_receiver.key}"
      email_address = email_receiver.value
      # The common schema, because the alternative is a different email body per
      # alert type and a person learning to read four layouts under pressure.
      use_common_alert_schema = true
    }
  }

  dynamic "sms_receiver" {
    for_each = local.has_sms ? [1] : []
    content {
      name         = "sms"
      country_code = var.alert_sms_country_code
      phone_number = var.alert_sms_number
    }
  }

  tags = var.tags

  lifecycle {
    precondition {
      condition     = length(var.alert_emails) > 0 || (var.alert_sms_number != "" && var.alert_sms_country_code != "")
      error_message = "An action group with no receivers creates cleanly, attaches to every rule, reports healthy and delivers nothing. Set alert_emails, or both alert_sms_country_code and alert_sms_number, or do not enable alerting at all."
    }
  }
}
