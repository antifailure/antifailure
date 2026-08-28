variable "subscription_id" {
  type        = string
  description = "Supplied by CI or ARM_SUBSCRIPTION_ID. Not committed: no resource identifier belongs in this repository."
}

variable "resource_group_name" {
  type    = string
  default = "af-cp-eastus"
}

variable "location" {
  type        = string
  default     = "eastus"
  description = <<-EOT
    The spec names the control plane's group af-cp-scus, and South Central US
    is not available on this subscription.

    A policy assignment called bonfire-allowed-locations denies every region
    except eastus, centralus and global. It is a deny effect, so a plan in
    southcentralus is perfectly clean and every resource is refused at apply.
    Quota was never the constraint: both regions have 65 cores.

    eastus is also cheaper for what this stack uses. A B1ms flexible server is
    0.017/hour here against 0.0204 in southcentralus, and storage is 0.115 per
    GB against 0.138.
  EOT

  # This validation lives HERE and not only on the module, and that is the
  # whole point of the comment.
  #
  # The module's location comes from module.foundation.location, which is
  # azurerm_resource_group.this.location and therefore unknown until apply.
  # Terraform SKIPS a validation whose value is unknown, silently, so the
  # module-level check never fired and a plan in southcentralus was clean. A
  # guard that quietly does not run is worse than no guard, because somebody
  # trusts it. This variable is a real input, known at plan time, so this one
  # actually runs.
  validation {
    condition     = contains(["eastus", "centralus"], var.location)
    error_message = "This subscription carries a policy assignment named bonfire-allowed-locations which denies every region except eastus, centralus and global. The spec names this group af-cp-scus; South Central US is not available here. A plan elsewhere is clean and every resource is refused at apply."
  }
}

variable "name" {
  type    = string
  default = "afcp"
}

variable "monthly_budget_usd" {
  type    = number
  default = 300
}

variable "budget_contact_emails" {
  type    = list(string)
  default = []
}

variable "log_retention_days" {
  type    = number
  default = 30
}

variable "image_repository" {
  type    = string
  default = "ghcr.io/antifailure/control-plane"
}

variable "image_tag" {
  type    = string
  default = "v0.1.1"
}

variable "image_digest" {
  type    = string
  default = ""
}

variable "database_sku" {
  type    = string
  default = "B_Standard_B1ms"
}

variable "database_storage_mb" {
  type    = number
  default = 32768
}

variable "backup_retention_days" {
  type    = number
  default = 14
}

variable "high_availability" {
  type    = bool
  default = false
}

variable "min_replicas" {
  type    = number
  default = 1
}

variable "max_replicas" {
  type    = number
  default = 3
}

variable "app_base_url" {
  type    = string
  default = ""
}

variable "github_client_id" {
  type      = string
  sensitive = true
}

variable "github_client_secret" {
  type      = string
  sensitive = true
}

variable "github_redirect_uri" {
  type = string
}

variable "event_retention_months" {
  type        = number
  default     = null
  description = "Null keeps every event forever. Staging sets a year, because a partitioned table with no retention is a partition per month and no reason to ever drop one."
}

variable "key_vault_name" {
  type        = string
  default     = ""
  description = "Overrides the computed vault name, for a vault that already exists under a different one."
}

variable "database_extensions" {
  type        = list(string)
  default     = ["PGCRYPTO"]
  description = "Allow-listed in azure.extensions. Azure refuses CREATE EXTENSION for anything absent from it, and it defaults to empty."
}
