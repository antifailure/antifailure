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
  # The subscription's bonfire-sku-allowlist policy permits exactly three
  # server SKUs. Terraform writes the tier as a prefix (B_, GP_); the policy
  # reads sku.name, which is the part after it.
  validation {
    condition = contains(
      ["B_Standard_B1ms", "B_Standard_B2s", "GP_Standard_D2ds_v4"],
      var.database_sku,
    )
    error_message = "bonfire-sku-allowlist on this subscription permits only Standard_B1ms, Standard_B2s and Standard_D2ds_v4 for a flexible server. Anything else plans cleanly and is denied at apply."
  }
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
  default     = false
  description = <<-EOT
    Grant the caller Key Vault Secrets Officer on this vault.

    OFF by default, and that default is the point. A role assignment whose
    principal is "whoever is running Terraform" churns on every plan by a
    different caller, and principal_id is ForceNew, so the pull request plan job
    reports that it MUST BE REPLACED on every single run. A plan that always
    carries a destroy is a plan people stop reading.

    Turn it on only where exactly one identity ever runs this stack, or pin
    deployer_principal_id instead.
  EOT
}

variable "deployer_principal_id" {
  type        = string
  default     = null
  description = "Pins the principal that gets Key Vault Secrets Officer, instead of taking whoever is calling. Null falls back to the caller, which is what makes assign_deployer_secret_officer caller-dependent and therefore off by default."
}
variable "goldens_enabled" {
  type        = bool
  default     = false
  description = <<-EOT
    Create the goldens storage account.

    OFF by default, deliberately. Nothing in the control plane reads blob
    storage: there is no @azure/storage dependency anywhere in web/, and no
    code path that opens a container. Creating an account nothing reads is a
    resource that looks like a feature, which is the shape this repository
    keeps having to remove.

    It is also not merely idle. bonfire-deny-public-data refuses any storage
    account whose publicNetworkAccess is not Disabled, so the account can only
    be reached through a private endpoint, which is a real monthly cost for a
    consumer that does not exist yet.

    Turn it on when the golden storage backend lands, and add the private
    endpoint in the same change.
  EOT
}

variable "golden_replication" {
  type    = string
  default = "LRS"
}
variable "golden_soft_delete_days" {
  type    = number
  default = 30
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

# Who may sign in at all.
#
# REQUIRED, with no default, and that is the point. The application reads
# AF_SIGNIN_ALLOWLIST and treats an UNSET variable as "open: any GitHub account
# may sign in". It said so in its own start-up log on the day this deployment
# went up, and nobody read the log, so a control plane on a public address
# accepted any GitHub account in the world for a week.
#
# A variable with a default would have the same failure mode: whoever forgets it
# gets the default, and the default that is convenient is the one that is wrong.
# So there is no default. A plan cannot be produced without somebody deciding
# who may sign in.
#
# An EMPTY list is a real answer and means nobody, which is what to set on an
# instance nobody should be signing in to yet. It is not the same as unset.
variable "signin_allowlist" {
  type        = list(string)
  description = "GitHub logins that may sign in. Empty means nobody. There is no value that means everybody."
}

# The secret that seals customers' provider keys, 32 bytes.
#
# Not a variable anybody types. Terraform generates it, Key Vault holds it, and
# it is never in a tfvars file, a workflow, or a person's terminal. Rotating it
# means every stored key stops opening, so it is created once and kept.
#
# Empty is a valid state: the application serves normally, says in its start-up
# log and in the console that keys cannot be stored, and refuses a save rather
# than accepting one it cannot seal. That is the right behaviour for an
# installation that does not want the feature -- but it is a decision, and this
# module makes it by generating the secret, because our own instance wants it.
# The GitHub App.
#
# Set the id and this module wires all three variables the application needs:
# the id, the private key, and the webhook secret. Leave it empty and none of
# them are set, which is a supported state -- sign-in works, and the webhook
# endpoint answers 503 rather than accepting unsigned deliveries.
#
# All three or none, because the application refuses a half-configured App at
# start-up. A webhook secret with no private key produces an endpoint that
# verifies deliveries perfectly and can do nothing with them.
#
# The two secrets are NOT managed here. GitHub generates the private key and
# shows it once; Terraform can neither create it nor recreate it, and a resource
# that manages a value it cannot produce is a resource that will one day set it
# to the empty string. They are put in the vault by a person and read back here,
# which is why these are data sources rather than resources.
variable "github_app_id" {
  type        = string
  default     = ""
  description = "The numeric GitHub App ID. Empty means no App is configured."
}

variable "github_app_private_key_secret_name" {
  type        = string
  default     = "github-app-private-key"
  description = "The Key Vault secret holding the App's PEM private key."
}

variable "github_app_webhook_secret_name" {
  type        = string
  default     = "github-app-webhook-secret"
  description = "The Key Vault secret holding the App's webhook secret."
}

variable "provider_key_secret_enabled" {
  type        = bool
  default     = true
  description = "Generate and store a sealing secret so provider keys can be saved."
}


variable "database_extensions" {
  type        = list(string)
  default     = ["PGCRYPTO"]
  description = <<-EOT
    Extensions to allow-list in azure.extensions. Azure refuses CREATE EXTENSION
    for anything absent from this parameter, and it defaults to empty, so a
    schema that needs one cannot apply until it is named here.

    pgcrypto is required: migration 0001 creates it for gen_random_uuid().
  EOT
}

variable "key_vault_name" {
  type        = string
  default     = null
  description = <<-EOT
    Overrides the derived name, which is `<name>-kv-<location>` truncated to the
    24 characters Azure allows.

    You need this exactly when purge protection is in your way: a deleted vault
    holds its GLOBAL name for the soft delete retention period and purge
    protection means nobody can release it early. Recreating in the same region
    inside that window is impossible by design, and a different name is the only
    way through. Reach for it knowingly rather than as a reflex.
  EOT
  validation {
    condition     = var.key_vault_name == null || var.key_vault_name == "" || can(regex("^[a-zA-Z][a-zA-Z0-9-]{1,22}[a-zA-Z0-9]$", var.key_vault_name))
    error_message = "A Key Vault name is 3 to 24 characters, alphanumerics and hyphens, starts with a letter and does not end with a hyphen."
  }
}
