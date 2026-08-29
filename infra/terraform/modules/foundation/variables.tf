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
  type        = list(string)
  default     = []
  sensitive   = true
  description = <<-EOT
    Who gets the budget alert.

    MARKED SENSITIVE BECAUSE A PLAN IS PUBLISHED, NOT BECAUSE AN EMAIL IS A
    CREDENTIAL. Terraform prints attribute values in plan output, and this
    repository runs `terraform plan` on every pull request with the result in a
    step summary. On a public repository that summary is world readable, so an
    address supplied here appeared in a public log the first time a plan
    proposed to change the notification blocks. Nobody typed it into a document;
    it arrived through a variable and left through a diff.

    `sensitive` makes Terraform redact it everywhere it flows, including into
    the resource's own plan diff.

    Supply the SAME value to the plan job as to the apply. Left empty in CI, a
    plan proposes to delete every notification block, which reads as a small
    in-place update and would silently switch off the budget alerts this module
    exists to create.
  EOT
}

variable "budget_contact_roles" {
  type        = list(string)
  default     = ["Owner"]
  description = <<-EOT
    Subscription roles that receive the budget alert. The DEFAULT way this
    module notifies anybody, in preference to contact_emails, because a role is
    resolved by Azure at alert time and no personal address ends up in the
    configuration, the state, or a public plan summary.

    Set to [] if you genuinely want addresses only.
  EOT
}
