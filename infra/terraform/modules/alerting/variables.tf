variable "name" {
  type        = string
  description = "Prefix for every resource here, the same prefix the control plane uses, so an alert and the thing it watches sort next to each other in a portal list."
}

variable "resource_group_name" { type = string }
variable "location" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}

# --- who gets woken up ------------------------------------------------------

# MARKED SENSITIVE FOR THE SAME REASON budget_contact_emails IS, AND THE REASON
# IS NOT THAT AN ADDRESS IS A CREDENTIAL.
#
# This repository plans on every pull request and writes the result into a step
# summary. On a public repository that summary is world readable, so an address
# that arrives through a variable leaves through a diff without anybody typing
# it into a document.
#
# The mark protects the forward direction only. A REMOVAL diff prints the value
# from prior state rather than from the variable, so it is not sufficient on its
# own, and that is why nothing here has a default.
variable "alert_emails" {
  type        = list(string)
  default     = []
  sensitive   = true
  description = "Addresses that receive every alert. Empty is allowed only when an SMS number is set."
}

variable "alert_sms_country_code" {
  type        = string
  default     = ""
  description = "Country calling code without the plus, for example 1. Empty means no SMS receiver."
}

variable "alert_sms_number" {
  type        = string
  default     = ""
  sensitive   = true
  description = "Phone number without the country code. Empty means no SMS receiver."
}

# --- what is being watched --------------------------------------------------

variable "container_app_id" {
  type        = string
  description = "The application. Scope for the request and restart alerts."
}

variable "postgres_server_id" {
  type        = string
  description = "The flexible server. Scope for the storage, connection and CPU alerts."
}

variable "job_ids" {
  type        = map(string)
  description = "Container app jobs to watch for a failed execution, keyed by a short name that ends up in the alert's name. The migration job is the one that matters: today a failed migration fails the deploy loudly in CI and would fail silently at three in the morning if anything else ran it."
}

variable "probe_url" {
  type        = string
  description = <<-EOT
    The URL the availability test asks for, from outside this stack.

    /readyz rather than /health, and the difference is the whole point. /health
    is a static literal that touches nothing, so a probe against it proves that
    a socket is open and nothing else: the first deploy of this application to
    Azure answered /health with 200 for thirteen minutes while every endpoint
    that read a table returned 500. /readyz takes a connection out of the pool
    the application serves with and answers 503 when the database does not.

    Part 4.3 of the production assessment says /healthz. There is no such route
    in this application, and a probe against one would have been permanently red
    from the day it was applied.
  EOT
  validation {
    condition     = startswith(var.probe_url, "https://")
    error_message = "The availability test has to exercise the path a customer takes, and that includes TLS. A plain http probe stays green while the certificate is expired or the binding is gone."
  }
}

variable "database_sku" {
  type        = string
  description = "The flexible server's SKU, used only to look up max_connections for the connection alert's threshold. A metric alert cannot divide one series by another, so the eighty percent has to be computed here."
}

variable "min_replicas" {
  type        = number
  description = "What the app is configured to keep running. The replica alert fires below it, so the number has to match the application's own configuration rather than be guessed."
}

# --- thresholds -------------------------------------------------------------

variable "http_5xx_threshold" {
  type        = number
  default     = 10
  description = "Server errors in a five minute window before this pages. An absolute count and not a rate: a metric alert cannot divide 5xx by total requests, and a ratio computed in a log alert costs 1.50 USD a month per rule and arrives five minutes later."
}

variable "restart_threshold" {
  type        = number
  default     = 3
  description = "Container restarts on one replica in fifteen minutes. One restart is a liveness probe doing its job; three is a loop."
}

variable "database_storage_percent" {
  type    = number
  default = 80
}

variable "database_cpu_percent" {
  type    = number
  default = 80
}

variable "connection_percent" {
  type        = number
  default     = 80
  description = "Percent of the server's max_connections at which to alert."
}

variable "web_test_locations" {
  type = list(string)
  default = [
    "us-va-ash-azr",
    "us-ca-sjc-azr",
    "emea-nl-ams-azr",
  ]
  description = <<-EOT
    Where the availability test runs from. Microsoft's opaque location ids, which
    is exactly why this is a variable: they are not derivable and Azure refuses
    an unknown one at apply.

    Three, on two continents, because the alert fires on two failed locations
    and a single region's networking problem must not be able to reach that on
    its own. Each location is a separate billed execution, so this list is also
    the whole cost of the test.
  EOT
  validation {
    condition     = length(var.web_test_locations) >= 3
    error_message = "The availability alert fires on two failed locations, so fewer than three makes one region's network problem enough to page somebody."
  }
}

variable "web_test_frequency_seconds" {
  type        = number
  default     = 300
  description = "Azure allows 300, 600 or 900."
  validation {
    condition     = contains([300, 600, 900], var.web_test_frequency_seconds)
    error_message = "Azure allows a standard web test to run every 300, 600 or 900 seconds and nothing else."
  }
}

variable "certificate_warning_days" {
  type        = number
  default     = 21
  description = "Days of certificate life left before the certificate test starts failing. A Container Apps managed certificate renews itself, so this fires only when the renewal did not happen, and three weeks is enough time to do something about that by hand."
}

variable "log_analytics_id" {
  type        = string
  description = "The workspace the availability results land in. Workspace based Application Insights rather than a classic component, so there is one store to query during an incident instead of two."
}
