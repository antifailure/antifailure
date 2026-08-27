variable "name" {
  type        = string
  description = "Resource group name. Must start with af-: see ISOLATION.md."
  validation {
    condition     = startswith(var.name, "af-")
    error_message = "Antifailure only creates resource groups prefixed af-."
  }
}

variable "location" {
  type        = string
  description = "Azure region."
}

variable "environment" {
  type        = string
  description = "dev, corpus, cp or tfstate."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Extra tags. project=antifailure is always added and cannot be overridden away."
}

variable "log_analytics" {
  type    = bool
  default = true
}

variable "log_retention_days" {
  type    = number
  default = 30
  validation {
    condition     = var.log_retention_days >= 30 && var.log_retention_days <= 730
    error_message = "Log Analytics retention is between 30 and 730 days."
  }
}

variable "monthly_budget_usd" {
  type        = number
  description = "Monthly budget in USD. Zero disables the budget, which should only be true for the state group."
  default     = 0
}

variable "budget_start_date" {
  type        = string
  description = "First of a month, RFC3339. Fixed rather than computed: a start date derived from timestamp() shows as drift on every plan."
  default     = "2026-08-01T00:00:00Z"
}

variable "budget_alert_thresholds" {
  type    = list(number)
  default = [50, 80, 100]
}

variable "budget_contact_emails" {
  type    = list(string)
  default = []
}
