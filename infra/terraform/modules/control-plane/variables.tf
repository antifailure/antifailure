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

variable "goldens_replication" {
  type    = string
  default = "LRS"
}
variable "goldens_soft_delete_days" {
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
  default = "v1.2.1"
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
# THREE ANSWERS, NOT TWO, because the application has three states and this used
# to be able to express two of them. A list names those accounts. An EMPTY list
# means nobody, which is what to set on an instance nobody should be signing in
# to yet. And NULL means everybody, which is the state a product with self serve
# signup is in and which had no representation here at all: `join(",", [])` is
# the empty string, and the application reads an empty string as "set, and names
# nobody", so the most open intent and the most closed one produced the same
# deployment. See app.tf for the block, which is careful about exactly this.
variable "signin_allowlist" {
  type        = list(string)
  nullable    = true
  description = "GitHub logins that may sign in. Empty means nobody, null means anybody. There is no default."
}

# Where a refused person is sent instead.
#
# The allowlist decides who gets in. This decides what the ones it turns away
# see, and it is the difference between a closed door and a closed door with a
# note on it. Empty is the right answer for an installation whose operator has
# their own way of being asked: the refusal page then names who to ask rather
# than inventing a link. Our own instances point at the contact page on the
# marketing site, which reaches a person.
#
# On a plane where signin_allowlist is null this is never rendered, because
# nobody is refused. It is set anyway, so that closing signups later is one
# decision rather than two.
variable "signup_url" {
  type        = string
  default     = ""
  description = "Where somebody the allowlist refuses is sent to ask for access. Empty means the refusal page offers no link."
}

# Whether somebody who signs in with no organization is given one.
#
# The other half of the sentence the allowlist starts. That decides who may sign
# in; this decides whether there is anything on the other side of the door.
# Without it a new person authenticates, belongs to nothing, and reaches the
# console's empty state, which is a working sign-in and a product that appears
# broken.
#
# Defaults to false, which is what the application defaults to and for the
# reason auth/provision.ts gives: what it grants is a tenant with real quotas
# and real compute against them, so forgetting the variable has to close the
# door rather than open it. Antifailure's own planes set it to true.
variable "self_serve_signup" {
  type        = bool
  default     = false
  description = "Whether a sign-in that lands in no organization creates one, on the free plan, owned by that person."
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

# --- the public name ------------------------------------------------------
#
# Empty means the app serves only its generated
# <name>-app.<region>.azurecontainerapps.io address, which is a supported state
# and is what every installation starts as. Set all three together: a custom
# domain with no zone is a name Terraform cannot create a record for.
variable "custom_domain" {
  type        = string
  default     = ""
  description = "The public name to bind, for example app.antifailure.dev. Empty binds nothing."
}

variable "dns_zone_name" {
  type        = string
  default     = ""
  description = "The Azure DNS zone that holds custom_domain, for example antifailure.dev."
}

variable "dns_zone_resource_group" {
  type        = string
  default     = ""
  description = "The resource group holding the zone, which is NOT this stack's group: antifailure.dev lives in af-web with the marketing site. The identity applying this stack needs DNS Zone Contributor there, and this module deliberately does not create that grant."
}

# ---------------------------------------------------------------------------
# THE CONFIGURATION THE APPLICATION READS AND THIS MODULE COULD NOT SET.
#
# docs/reference/control-plane.md documents 45 variables the control plane
# process reads. Before this block the module could set 16 of them, and nothing
# anywhere said so: the reference is complete, the application is complete, and
# the wire between them had 29 strands missing. Every symptom of that reads as a
# broken feature rather than as an unset variable -- the operator portal
# answering PRECONDITION_FAILED on all 23 of its routes, no sign-in link and no
# invitation able to leave the building, billing off with a real Stripe price
# sitting behind Team, the marketing site's beacon refused cross origin with a
# bare network error, the analytics stream recording nothing, and the two
# buttons that went missing from the "No organization yet" screen.
#
# tools/wirecheck is the gate that will not let it happen again, and
# tools/docs/wiring-exemptions.tsv is where a variable that genuinely cannot be
# set here says why. Adding a variable to the reference now fails a build until
# one of those two things is true.
#
# EVERY VARIABLE BELOW IS OPTIONAL WITH A DEFAULT, and that is a promise rather
# than a preference: tools/inputcheck refuses a new input arriving without one,
# because a required input refuses a tfvars file that was complete the day
# before.
#
# UNSET VERSUS EMPTY IS DECIDED PER VARIABLE AND EACH DECISION IS WRITTEN DOWN
# AT THE env BLOCK IN app.tf that carries it. The rule is not "dynamic block
# unless awkward". It is: read what the application does with unset and what it
# does with empty, and pick the block that cannot turn one into the other.
# AF_SIGNIN_ALLOWLIST above is the precedent and the reason -- unset means
# everybody there and empty means nobody, so a dynamic block that vanished on
# empty would deploy the least restrictive reading of the most restrictive
# intent. Nothing added below inverts that way; the one that comes closest is
# AF_ADMIN_POOL_MAX, where unset means four and empty parses as a pool of zero,
# and it is never emitted empty.
# ---------------------------------------------------------------------------

# The operator portal, off by default.
#
# ON means this module generates a password for `antifailure_admin`, keeps it in
# Key Vault, and hands it to the application as AF_ADMIN_DATABASE_URL and to the
# bootstrap job, which is the thing that gives that role a login at all.
#
# The role itself is NOT created here and could not be: it is created by
# migration 0023 with BYPASSRLS and granted its privileges by 0029, 0030 and
# 0031, and Terraform cannot reach this server -- there is no public endpoint
# and a plan running in CI is not inside the VNet. That is the same reason the
# application's own role is created by the bootstrap job rather than by a
# postgresql provider block.
#
# OFF is a real answer and the right default for a single team running this for
# themselves: the application says so at startup and every /admin route answers
# PRECONDITION_FAILED naming the variable, rather than rendering an empty portal
# that reads like a platform with no customers on it.
variable "operator_portal_enabled" {
  type        = bool
  default     = false
  description = "Generate an operator credential and wire AF_ADMIN_DATABASE_URL. Off means this installation has no operator portal."
}

# Connections in the operator pool.
#
# Counted against the database the same way the application's pool is: see the
# precondition in app.tf, which now includes this number. A pool that fits on
# its own and does not fit beside the application is a 503 at the next peak.
variable "admin_pool_max" {
  type        = number
  default     = 4
  description = "Connections in the operator pool. Small on purpose: it serves a handful of operators, not customer traffic."
}

# Analytics, off by default.
#
# The surrogate secret is GENERATED here rather than typed, like the provider
# sealing secret and for the same reason: nobody and no workflow should ever
# hold it. Regenerating it re-keys every organization surrogate, which silently
# breaks every funnel that crosses the change, so it is created once and kept.
variable "analytics_enabled" {
  type        = bool
  default     = false
  description = "Generate a surrogate secret so the analytics stream records. Off records nothing and the dashboard says so."
}

variable "analytics_operator_org" {
  type        = string
  default     = ""
  description = "Slug of the organization whose owners and admins may read the analytics dashboard. Empty means nobody."
}

variable "analytics_retention_days" {
  type        = number
  default     = null
  description = "Delete raw analytics events older than this many days. Null keeps them forever, because retention is an operator's decision."
}

# Every origin the marketing site is served from, for the three things a
# browser on it calls cross origin: the analytics beacon, the enterprise lead
# form and the careers application form.
#
# Empty refuses all of them, which is what unset does in the application too, so
# this cannot be set wrong in the dangerous direction: there is no value here
# that reflects whatever Origin arrives.
#
# MORE THAN ONE, COMMA SEPARATED, BECAUSE THE SITE IS SERVED ON MORE THAN ONE
# HOSTNAME. This held one value, the apex, and www.antifailure.dev is a second
# custom domain on the same Azure Static Web App answering 200 for every page.
# Static Web Apps cannot redirect one hostname to the other: a route rule matches
# on PATH and the schema has no hostname condition at all. So a visitor on www
# had every call the site makes refused 403 and there was nothing in the tree
# that could have said so.
#
# STILL ONE STRING, and that is deliberate rather than lazy. It is the value of
# AF_SITE_ORIGIN with nothing done to it, so there is no join between what an
# operator writes and what the process parses, and no shape for that join to get
# wrong. It is also the input docs/reference/stability.md promised: a list would
# be a retyped variable, which costs a major version, and every tfvars already
# holding one origin keeps working untouched.
variable "site_origin" {
  type        = string
  default     = ""
  description = "Every origin the marketing site is served from, as whole origins separated by commas, such as https://example.com,https://www.example.com. Empty refuses every beacon, lead and application."

  validation {
    # A path, a query or anything beyond scheme, host and port can never equal
    # an Origin header, so such a value would allow nobody while looking
    # configured. The application refuses it at startup; refusing it at plan
    # time means finding out before the revision fails rather than after.
    condition = var.site_origin == "" || alltrue([
      for o in split(",", var.site_origin) : can(regex("^https?://[^/?#,]+$", trimspace(o)))
    ])
    error_message = "Each site origin must be scheme, host and optional port and nothing more, such as https://example.com. A browser sends only that in the Origin header. Separate several with commas."
  }

  validation {
    # There is no value here meaning "any origin". The routes this configures
    # write to the database from an unauthenticated caller.
    condition     = !strcontains(var.site_origin, "*")
    error_message = "There is no wildcard site origin. Name each origin the site is served on."
  }
}

# Where a person with no organization installs the GitHub App.
#
# The variable whose absence hid both buttons on the "No organization yet"
# screen. The application validates it and stops at startup on anything that is
# not an https://github.com/apps/<slug>/installations/new address, so empty is
# the only safe way to say "not configured".
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

# Signing in with a link, and inviting somebody who is not in your GitHub
# organization. Off by default.
#
# mail_from is the switch, the way github_app_id is the switch for the App, and
# for the same reason: the application refuses a half-configured mailer at
# startup, so a block that could emit two of the three would stop the container
# rather than degrade. Set it and public_url becomes required by a precondition
# in app.tf, and the Resend key is READ from the vault rather than passed
# through a tfvars file.
variable "mail_from" {
  type        = string
  default     = ""
  description = "The From address on sign-in links and invitations. Empty turns mail off entirely."
}

variable "public_url" {
  type        = string
  default     = ""
  description = "The origin a browser reaches this deployment on, which is where a sign-in link points. Required when mail_from is set."
}

# Where an enterprise lead is announced.
#
# Needs a mailer, and the application says so at startup rather than failing:
# set with no mailer it prints that this deployment believes it is announcing
# leads and CANNOT, which is its own state and a worse one than either end. The
# precondition in app.tf refuses that combination at plan time so the module
# cannot produce it. Leads are recorded either way and read with
# `af-control-plane-backup leads`.
variable "lead_notify_email" {
  type        = string
  default     = ""
  description = "Where an enterprise lead is announced. Empty records leads and mails nobody. Requires mail_from."
}

variable "resend_api_key_secret_name" {
  type        = string
  default     = "resend-api-key"
  description = "The Key Vault secret holding the Resend API key. Put it there yourself; Terraform never sees the value."
}

# Billing. Off by default, and the Team price is the switch.
#
# THERE IS NO enterprise PRICE AND THERE IS NOT MEANT TO BE. Enterprise is
# arranged with a person, so AF_STRIPE_PRICE_ENTERPRISE is deliberately unset
# and this module has no input for it. A plan with no price is a plan that is
# not sold here; checkout refuses it by name and points at the contact route.
# Adding a default that made an absent price look present would sell something
# nobody can buy.
variable "stripe_price_team" {
  type        = string
  default     = ""
  description = "The Stripe price the team plan is sold at. Empty turns billing off entirely."
}

variable "stripe_secret_key_secret_name" {
  type        = string
  default     = "stripe-secret-key"
  description = "The Key Vault secret holding the Stripe API key. Put it there yourself; Terraform never sees the value."
}

variable "stripe_webhook_secret_secret_name" {
  type        = string
  default     = "stripe-webhook-secret"
  description = "The Key Vault secret holding the Stripe webhook signing secret. Put it there yourself; Terraform never sees the value."
}

# The plan gate on a control plane sold only to enterprise organizations.
#
# Any value other than `enterprise` stops the process, and so does setting it
# while billing is off, because no customer could then satisfy the gate. Both
# are caught by a precondition in app.tf so they fail a plan in review rather
# than a container at 3am.
variable "hosted_required_plan" {
  type        = string
  default     = ""
  description = "Set to enterprise on a plane sold only to enterprise organizations. Empty serves every plan."
}

# Whoever runs this plane also decides each organization's plan.
#
# Refused by the application at startup on an installation that takes payment,
# because a plan that can be granted by hand is not a plan anybody has to buy.
# The precondition in app.tf refuses the same combination at plan time.
variable "operator_sets_plan" {
  type        = bool
  default     = false
  description = "Allow billing.set on an installation that takes no payment. Refused together with any Stripe configuration."
}

variable "model_prices" {
  type        = string
  default     = ""
  description = "Model prices as model=input/output in US dollars per million tokens, comma separated. Adds to the built-in defaults."
}

variable "product_name" {
  type        = string
  default     = ""
  description = "The product name in a sign-in link's subject line, for a white-labelled deployment. Empty uses Antifailure."
}
