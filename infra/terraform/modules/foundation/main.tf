# A resource group Antifailure owns, and the two things that make owning it
# safe: a tag that scopes every cleanup, and a budget that shouts before the
# bill does.
#
# The name prefix and the tag are not cosmetic. `tools/azguard` refuses any
# operation whose target group is not named `af-*` and tagged
# `project=antifailure`, and the leak detector inventories exactly that set.
# A group created outside this module is a group nothing will ever clean up.

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
  # Enforced here as well as in azguard, because a module is easier to reach
  # than a wrapper script and both should refuse.
  name_is_owned = startswith(var.name, "af-")

  tags = merge(var.tags, {
    project     = "antifailure"
    environment = var.environment
    managed_by  = "terraform"
  })
}

resource "terraform_data" "name_guard" {
  lifecycle {
    precondition {
      condition     = local.name_is_owned
      error_message = "Resource group ${var.name} does not start with af-. Antifailure creates resource groups it owns and nothing else; this subscription holds groups belonging to other projects."
    }
  }
}

resource "azurerm_resource_group" "this" {
  name     = var.name
  location = var.location
  tags     = local.tags

  depends_on = [terraform_data.name_guard]

  lifecycle {
    # A group holding a Key Vault and a database is not something a plan should
    # be able to remove as a side effect of a rename.
    prevent_destroy = false
  }
}

# Diagnostics land here rather than nowhere. Thirty days, because the questions
# this answers ("what happened during that incident") are asked within days and
# a longer retention is a bill rather than a benefit.
resource "azurerm_log_analytics_workspace" "this" {
  count               = var.log_analytics ? 1 : 0
  name                = "${var.name}-logs"
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name
  sku                 = "PerGB2018"
  retention_in_days   = var.log_retention_days
  tags                = local.tags
}

# The budget is the reason an idle preview cluster is noticed in a week instead
# of at the end of the month. Alerts fire at each threshold on FORECAST as well
# as actual, because a forecast crossing 100 percent on the fourth of the month
# is the one worth waking up for.
resource "azurerm_consumption_budget_resource_group" "this" {
  count             = var.monthly_budget_usd > 0 ? 1 : 0
  name              = "${var.name}-budget"
  resource_group_id = azurerm_resource_group.this.id

  amount     = var.monthly_budget_usd
  time_grain = "Monthly"

  time_period {
    # Budgets need a start date on the first of a month. Supplied as a variable
    # rather than computed from timestamp(), because a computed start date
    # changes on every plan and shows as perpetual drift.
    start_date = var.budget_start_date
  }

  # contact_roles rather than an address when no address is configured.
  #
  # Azure refuses a notification with all three of contact_emails,
  # contact_roles and contact_groups empty: "Notification cannot have all of
  # Contact Emails, Contact Roles and Contact Groups empty", 400. So an empty
  # budget_contact_emails is not "alert nobody", it is a budget that cannot be
  # created or updated at all, and every apply after the address was removed
  # failed on this resource.
  #
  # Owner is the right fallback and is better than an address: it notifies
  # whoever holds Owner on the subscription today rather than whoever held it
  # when this was written, and it puts no personal address in a public
  # repository, which is why the address was removed in the first place.
  dynamic "notification" {
    for_each = var.budget_alert_thresholds
    content {
      enabled        = true
      threshold      = notification.value
      operator       = "GreaterThan"
      threshold_type = "Actual"
      contact_emails = var.budget_contact_emails
      contact_roles  = length(var.budget_contact_emails) == 0 ? ["Owner"] : []
    }
  }

  dynamic "notification" {
    for_each = var.budget_alert_thresholds
    content {
      enabled        = true
      threshold      = notification.value
      operator       = "GreaterThan"
      threshold_type = "Forecasted"
      contact_emails = var.budget_contact_emails
      contact_roles  = length(var.budget_contact_emails) == 0 ? ["Owner"] : []
    }
  }
}
