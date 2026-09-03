variable "subscription_id" {
  type        = string
  description = "Supplied by CI or ARM_SUBSCRIPTION_ID. Not committed: no resource identifier belongs in this repository."
}

variable "resource_group_name" {
  type    = string
  default = "af-cp-centralus"
}

variable "location" {
  type        = string
  default     = "centralus"
  description = <<-EOT
    THE THIRD REGION THIS STACK HAS TRIED, AND EACH MOVE WAS FORCED BY A
    DIFFERENT SYSTEM THAT A PLAN CANNOT SEE.

    southcentralus, which the spec names, is denied by a policy assignment
    called bonfire-allowed-locations. It is a deny EFFECT, so a plan in
    southcentralus is perfectly clean and every resource is refused at apply.

    eastus is allowed by that policy and is cheaper, so this defaulted there.
    An apply then failed on the database with

      ParameterOutOfRange: The value of 'Version' should be in: []

    and the empty list is literal. `az postgres flexible-server list-skus -l
    eastus` returns supportedServerVersions: [] with the reason "Provisioning
    is restricted in this region." PostgreSQL flexible server cannot be created
    in eastus on this subscription at all, at any version, in any SKU. Every
    other resource in this stack creates there quite happily, which is why the
    apply got 26 resources deep before finding out.

    centralus offers versions 11 through 18 and every burstable SKU, and is
    allowed by the policy. It is the only region that satisfies all three
    gates. It costs about 2 USD a month more than eastus would have.

    QUOTA WAS NEVER THE CONSTRAINT. Both regions have 65 cores. Quota is the
    thing everybody checks and it was the one thing that was never in the way.
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
  #
  # It cannot check the third gate. Regional service restriction is not
  # expressible in a variable validation because it is a property of the
  # subscription that has to be asked for over the network. `azguard region`
  # asks, and self-hosting/azure.md says to run it first.
  validation {
    condition     = contains(["centralus"], var.location)
    error_message = "bonfire-allowed-locations denies every region except eastus, centralus and global, and PostgreSQL flexible server provisioning is RESTRICTED in eastus on this subscription (az postgres flexible-server list-skus -l eastus returns supportedServerVersions: []). centralus is the only region that satisfies both. Run `go run ./tools/azguard region centralus` before changing this."
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
  type        = list(string)
  default     = []
  sensitive   = true
  description = "Marked sensitive so it cannot reach a public pull request plan summary. See the foundation module's copy for the full reason."
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
  default = "v1.0.0"
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

# Connections PER REPLICA, and the stack has to set it because the number that
# matters is this one multiplied by the replicas and compared against what the
# database SKU allows. The module defaulted it to 10 and the stack never passed
# it at all, so staging and production ran the same pool against servers whose
# connection budgets differ by a factor of twenty four.
#
# modules/control-plane/database.tf does the multiplication and fails the plan
# if it does not fit. Both tfvars files show their own arithmetic.
variable "pool_max" {
  type        = number
  default     = 10
  description = "Postgres connections each replica may hold. (max_replicas + min_replicas) times this, plus four for the jobs and break-glass, has to fit in what the database SKU hands a role without pg_use_reserved_connections."
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

# No default. See the module's variable of the same name: an unset allowlist
# means the control plane accepts any GitHub account, and a default is a value
# somebody gets by forgetting.
variable "signin_allowlist" {
  type        = list(string)
  description = "GitHub logins that may sign in. Empty means nobody."
}

# Where the people that list turns away are sent. See the module's variable of
# the same name. Empty means the refusal page offers no link at all.
variable "signup_url" {
  type        = string
  default     = ""
  description = "Where somebody the allowlist refuses is sent to ask for access."
}

variable "provider_key_secret_enabled" {
  type        = bool
  default     = true
  description = "Generate a sealing secret so customers' provider keys can be stored."
}

# Empty means no GitHub App, which is a supported state: sign-in works and the
# webhook endpoint refuses deliveries rather than accepting unsigned ones. Set
# it and the two secrets must already be in the vault, because GitHub mints the
# private key and Terraform cannot.
variable "assign_deployer_secret_officer" {
  type        = bool
  default     = false
  description = "Whether this stack manages the Key Vault Secrets Officer grant on the human or identity that runs Terraform. See deployer_principal_id: leaving that null makes the grant caller-dependent, which is why the module defaults this off."
}

variable "deployer_principal_id" {
  type        = string
  default     = null
  description = "Pins the principal that gets Key Vault Secrets Officer instead of taking whoever is calling, so two identities planning the same stack do not each propose to replace the other's grant."
}

variable "github_app_id" {
  type        = string
  default     = ""
  description = "The numeric GitHub App ID, or empty for no App."
}

variable "ci_principal_id" {
  type        = string
  default     = ""
  description = "Object id of the service principal GitHub Actions federates into. Gets Reader on this resource group and nothing else. Empty disables the grant."
}

variable "geo_redundant_backup" {
  type        = bool
  default     = false
  description = <<-EOT
    Copy backups to the paired region. False is right for staging and wrong for
    production: without it, a region losing its storage loses the backups with
    the database, and the only remaining copy of the control plane is whatever
    somebody has on a laptop.

    It CANNOT BE CHANGED IN PLACE. Azure sets geo-redundancy when the server is
    created and refuses to turn it on afterwards, so this is decided once per
    server or paid for with a dump and a restore into a new one.
  EOT
}

# --- the public name ------------------------------------------------------
variable "custom_domain" {
  type        = string
  default     = ""
  description = "The name to bind on the container app, for example app.antifailure.dev. Empty serves only the generated address. Must agree with app_base_url and with the OAuth App's callback."
}

variable "dns_zone_name" {
  type    = string
  default = ""
}

variable "dns_zone_resource_group" {
  type        = string
  default     = ""
  description = "The group holding the zone. antifailure.dev is in af-web, not in this stack's group, so the identity applying this needs DNS Zone Contributor there."
}

# --- alerting ---------------------------------------------------------------
variable "alerting_enabled" {
  type        = bool
  default     = false
  description = "Create the action group, the availability tests and the metric alerts. Off for staging on purpose: a page for an environment that is supposed to break is a page people learn to ignore."

  # Cross-variable validation, which Terraform allows from 1.9 and which this
  # stack already requires. The alternative is a precondition inside the
  # alerting module, and this one has to fail at plan time on the STACK, before
  # anybody reads a diff that would create nine rules pointed at nothing.
  validation {
    condition     = !var.alerting_enabled || var.app_base_url != ""
    error_message = "alerting_enabled needs app_base_url set, because the availability test has to ask for the name a customer uses. A probe against the generated azurecontainerapps.io address stays green while DNS, the domain binding or the certificate is broken, which is most of the ways this service becomes unreachable without the application doing anything wrong."
  }

  validation {
    condition     = !var.alerting_enabled || length(var.alert_emails) > 0 || (var.alert_sms_number != "" && var.alert_sms_country_code != "")
    error_message = "alerting_enabled with no receiver produces an action group that creates cleanly, attaches to every rule, reports healthy and delivers nothing to anybody. Set alert_emails, or both alert_sms_country_code and alert_sms_number."
  }
}

# Sensitive for the same reason budget_contact_emails is: this repository plans
# on every pull request into a step summary that is world readable, and an
# address supplied as a variable would leave through a diff. See the foundation
# module's copy for why the mark is necessary and not sufficient.
variable "alert_emails" {
  type      = list(string)
  default   = []
  sensitive = true
}

variable "alert_sms_country_code" {
  type        = string
  default     = ""
  description = "Country calling code without the plus. Empty means no SMS receiver."
}

variable "alert_sms_number" {
  type      = string
  default   = ""
  sensitive = true
}

# ---------------------------------------------------------------------------
# The application configuration this stack could not reach.
#
# One pass-through per module input, so that a tfvars file is still the whole
# configuration of an installation. See the block comment in
# modules/control-plane/variables.tf for what was missing and why nothing
# caught it; tools/wirecheck is what catches it now.
#
# The DEFAULTS here are the module's, restated rather than omitted, because
# tools/inputcheck refuses an input that arrives without one: a required input
# refuses a tfvars file that was complete the day before.
#
# NO SECRET VALUE IS AN INPUT ON THIS LIST. The Stripe key, the Stripe webhook
# secret and the Resend key are read out of Key Vault by the module; the
# operator credential and the analytics surrogate secret are generated there.
# What a tfvars file carries is the NAME of a vault secret and the switch that
# turns a feature on, never the credential itself. This repository plans on
# every pull request into a step summary that is world readable.
# ---------------------------------------------------------------------------

variable "operator_portal_enabled" {
  type        = bool
  default     = false
  description = "Generate an operator credential and wire AF_ADMIN_DATABASE_URL. Off means this installation has no operator portal."
}

variable "admin_pool_max" {
  type        = number
  default     = 4
  description = "Connections in the operator pool, counted against the database beside the application's."
}

variable "analytics_enabled" {
  type        = bool
  default     = false
  description = "Generate a surrogate secret so the analytics stream records."
}

variable "analytics_operator_org" {
  type        = string
  default     = ""
  description = "Slug of the organization whose owners and admins may read the analytics dashboard. Empty means nobody."
}

variable "analytics_retention_days" {
  type        = number
  default     = null
  description = "Delete raw analytics events older than this many days. Null keeps them forever."
}

variable "site_origin" {
  type        = string
  default     = ""
  description = "The origin the marketing site is served from. Empty refuses every beacon."
}

variable "github_app_install_url" {
  type        = string
  default     = ""
  description = "The public https://github.com/apps/<slug>/installations/new address. Empty offers no install action."
}

variable "github_api_base" {
  type        = string
  default     = ""
  description = "Where the GitHub API lives, for GitHub Enterprise Server. Empty uses https://api.github.com."
}

variable "mail_from" {
  type        = string
  default     = ""
  description = "The From address on sign-in links and invitations. Empty turns mail off entirely."
}

variable "public_url" {
  type        = string
  default     = ""
  description = "The origin a browser reaches this deployment on. Required when mail_from is set."
}

variable "resend_api_key_secret_name" {
  type        = string
  default     = "resend-api-key"
  description = "The Key Vault secret holding the Resend API key."
}

variable "stripe_price_team" {
  type        = string
  default     = ""
  description = "The Stripe price the team plan is sold at. Empty turns billing off entirely."
}

variable "stripe_secret_key_secret_name" {
  type        = string
  default     = "stripe-secret-key"
  description = "The Key Vault secret holding the Stripe API key."
}

variable "stripe_webhook_secret_secret_name" {
  type        = string
  default     = "stripe-webhook-secret"
  description = "The Key Vault secret holding the Stripe webhook signing secret."
}

variable "hosted_required_plan" {
  type        = string
  default     = ""
  description = "Set to enterprise on a plane sold only to enterprise organizations. Empty serves every plan."
}

variable "operator_sets_plan" {
  type        = bool
  default     = false
  description = "Allow billing.set on an installation that takes no payment."
}

variable "model_prices" {
  type        = string
  default     = ""
  description = "Model prices as model=input/output in US dollars per million tokens, comma separated."
}

variable "product_name" {
  type        = string
  default     = ""
  description = "The product name in a sign-in link's subject line. Empty uses Antifailure."
}
