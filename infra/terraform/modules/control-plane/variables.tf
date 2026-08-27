variable "name" {
  type        = string
  description = "Prefix for every resource in this module. Short: storage account names are capped at 24 characters and Key Vault names at 24."
  validation {
    condition     = length(var.name) <= 16 && can(regex("^[a-z][a-z0-9-]*$", var.name))
    error_message = "name must be lower case, start with a letter, and be at most 16 characters, because it is a prefix for storage account and Key Vault names that Azure caps at 24."
  }
}

variable "resource_group_name" { type = string }
variable "location" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}
variable "log_analytics_id" {
  type    = string
  default = null
}

variable "diagnostics_enabled" {
  type        = bool
  default     = true
  description = "Send diagnostics to log_analytics_id. A separate boolean rather than a null check on the id, because the id is unknown at plan time and a count that depends on an unknown value fails the whole plan."
}

variable "vnet_cidr" {
  type        = string
  default     = "10.60.0.0/16"
  description = "Container Apps needs a large subnet; the platform runs its own infrastructure inside it."
}

# --- database -------------------------------------------------------------
variable "postgres_version" {
  type    = string
  default = "17"
}
variable "database_name" {
  type    = string
  default = "antifailure"
}
variable "database_admin_user" {
  type        = string
  default     = "af_migrator"
  description = "The OWNER. Runs migrations, owns the tables, and is never used by the application."
}
variable "database_app_user" {
  type        = string
  default     = "af_app"
  description = "The role the application connects as: a member of antifailure_app, owning nothing, unable to run DDL."
  validation {
    condition     = can(regex("^[a-z_][a-z0-9_]{0,62}$", var.database_app_user))
    error_message = "database_app_user has to be a plain lower case Postgres identifier; the bootstrap job validates the same pattern and refuses anything else rather than quoting it."
  }
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
  validation {
    condition     = var.backup_retention_days >= 7 && var.backup_retention_days <= 35
    error_message = "Azure allows 7 to 35 days of point in time recovery."
  }
}
variable "geo_redundant_backup" {
  type    = bool
  default = false
}
variable "high_availability" {
  type        = bool
  default     = false
  description = "Zone redundant HA. Needs a General Purpose or Memory Optimized SKU and multiplies the bill; the module refuses the burstable combination rather than failing at apply."
}

# --- key vault, storage ---------------------------------------------------
variable "key_vault_purge_protection" {
  type    = bool
  default = true
}
variable "assign_deployer_secret_officer" {
  type        = bool
  default     = true
  description = "Give whoever runs Terraform the ability to write the secrets below, scoped to this vault only. Turn off when the role is granted out of band."
}
variable "golden_replication" {
  type    = string
  default = "LRS"
}
variable "golden_soft_delete_days" {
  type    = number
  default = 30
}
variable "golden_allowed_ips" {
  type        = list(string)
  default     = []
  description = "Public IPs allowed to reach the goldens account. Empty means only the apps subnet can."
}

# --- application ----------------------------------------------------------
variable "image_repository" {
  type    = string
  default = "ghcr.io/antifailure/control-plane"
}
variable "image_tag" {
  type    = string
  default = "v0.1.1"
}
variable "image_digest" {
  type        = string
  default     = ""
  description = "Pin this in production. A tag can be moved and a digest cannot."
}
variable "app_cpu" {
  type    = number
  default = 0.5
}
variable "app_memory" {
  type    = string
  default = "1Gi"
}
variable "min_replicas" {
  type        = number
  default     = 1
  description = "Zero scales to nothing between requests and costs almost nothing, at the price of a cold start on the first request after idle."
}
variable "max_replicas" {
  type    = number
  default = 3
}
variable "pool_max" {
  type    = number
  default = 10
}
variable "app_base_url" {
  type    = string
  default = ""
}
variable "maintenance_cron" {
  type    = string
  default = "17 3 * * *"
}
variable "event_retention_months" {
  type        = number
  default     = null
  description = "Null keeps every event forever, which is the default because retention is an operator's decision."
}

variable "github_client_id" {
  type      = string
  sensitive = true
}
variable "github_client_secret" {
  type      = string
  sensitive = true
}
variable "github_redirect_uri" { type = string }
