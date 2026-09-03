# Where the credentials live.
#
# The Container App reads them through its managed identity at start, so no
# secret is ever written into an app setting, a revision, or a Terraform output
# that somebody can `terraform output` in plain text.
#
# They ARE in the state file, because Terraform generated them. That is why the
# state account in stacks/tfstate has public network access disabled, blob
# versioning on, soft delete on, and no key-based access.

data "azurerm_client_config" "current" {}

locals {
  # A KEY VAULT NAME IS GLOBAL AND A DELETED ONE KEEPS IT FOR A WEEK.
  #
  # This was `${var.name}-kv`, and that is a trap the first region move walked
  # straight into. Soft delete holds the name for the retention period, and
  # purge protection means it cannot be released early even by the person who
  # owns it: that is the whole point of purge protection and it is working as
  # designed. So a name with nothing region-specific in it makes
  # destroy-and-recreate-elsewhere fail on the vault, seven days after the
  # decision to move, with an error about a name conflict rather than about
  # soft delete.
  #
  # Including the location fixes the move. It does NOT make a
  # destroy-and-recreate in the SAME region within seven days work, and nothing
  # can, because that is exactly the attack purge protection exists to stop:
  # somebody with delete replacing the vault's contents by replacing the vault.
  # Set `key_vault_name` yourself if you need to sidestep it deliberately.
  key_vault_name = coalesce(
    var.key_vault_name,
    substr(replace("${var.name}-kv-${var.location}", "_", "-"), 0, 24),
  )
}

# The name is overridable because a Key Vault cannot be renamed in place.
#
# Changing it is a destroy and a create, and Key Vault carries purge protection,
# so the old name is unusable for seven days afterwards and every secret in it
# goes through soft delete. A plan that renames this vault is therefore an
# outage plus a week, and it is produced by something as small as editing the
# expression below. This deployment's vault predates that expression and is
# called afcp-kv-centralus; staging.tfvars says so, and that one line is the
# difference between an apply and a replacement.
resource "azurerm_key_vault" "this" {
  name                = local.key_vault_name
  location            = var.location
  resource_group_name = var.resource_group_name
  tenant_id           = data.azurerm_client_config.current.tenant_id
  sku_name            = "standard"

  # Access is by Azure RBAC, not vault access policies. Policies are per-vault
  # state that nothing else audits; RBAC assignments show up in the same place
  # as every other permission in the subscription.
  rbac_authorization_enabled = true

  # A vault that can be deleted and immediately recreated is a vault whose
  # secrets can be replaced by somebody who only has delete. Purge protection
  # makes that a seven day wait that somebody will notice.
  soft_delete_retention_days = 7
  purge_protection_enabled   = var.key_vault_purge_protection

  # REACHABLE, AND NOT READABLE, AND THE SECOND HALF DOES ALL THE WORK.
  #
  # tfsec reports these four lines as CRITICAL under
  # azure-keyvault-specify-network-acl: "Vault network ACL does not block access
  # by default." It is right about the configuration. A scanner severity is a
  # prior rather than a verdict, so this block says what reaching this endpoint
  # actually gets somebody, and what closing it would cost.
  #
  # WHAT REACHING IT GETS YOU. Nothing without a Microsoft Entra token, and
  # nothing with one unless its principal holds a Key Vault DATA role here.
  # rbac_authorization_enabled above means vault access policies do not exist on
  # this vault, so the complete list of principals that can read a secret is the
  # list of role assignments in this file:
  #
  #   app_reads_secrets        Key Vault Secrets User, on the user-assigned
  #                            identity the Container App and both jobs run as.
  #   deployer_writes_secrets  Key Vault Secrets Officer, on the one operator
  #                            object id both stacks pin in their tfvars.
  #
  # And one identity is deliberately absent: the pull request plan job.
  # stacks/control-plane/ci.tf grants it Reader on the resource group and says
  # in prose why it holds no Key Vault data role, and infra.yml plans with
  # -refresh=false so it never reads a secret value. Subscription Owner does not
  # help an attacker either. Under RBAC authorization Owner is a control plane
  # role and grants nothing on the data plane, which is the same split
  # stacks/tfstate/main.tf documents for storage: reachable is not readable.
  #
  # So this is a missing layer, not an open door. Saying so plainly is the
  # point. A pull request that inflates its own finding is one nobody trusts the
  # next time.
  #
  # WHY THE ONE LINE FIX IS AN OUTAGE. Setting default_action = "Deny" here
  # stops the control plane at its next revision, and the failure would read as
  # an application defect rather than as a firewall.
  #
  # The application never calls Key Vault. app.tf declares Key Vault secret
  # REFERENCES, and the Container Apps platform resolves those with the app's
  # user-assigned identity before the container starts. The bypass on the line
  # below does not cover that fetch, because Azure Container Apps is not on Key
  # Vault's trusted services list, and a service that is not on that list is
  # blocked by the firewall whether or not the bypass is enabled:
  # learn.microsoft.com/azure/key-vault/general/overview-vnet-service-endpoints
  #
  # A virtual network rule does not rescue it. The environment is integrated
  # with the apps subnet in network.tf, so a Microsoft.KeyVault service endpoint
  # there looks like the answer and is not: the platform's fetch does not
  # present as coming from that subnet, and the only reported workaround is to
  # allow-list the environment's egress addresses, which are not stable and are
  # not known until after the app exists.
  # github.com/microsoft/azure-container-apps/issues/1287
  #
  # WHAT WOULD ACTUALLY CLOSE IT, so the next person does not rediscover this. A
  # private endpoint, which is four resources rather than one word: a third
  # subnet that is NOT delegated, because both subnets in network.tf are
  # delegated and a private endpoint cannot sit in a delegated subnet; a
  # privatelink.vaultcore.azure.net private DNS zone; a link from that zone to
  # this network; and the endpoint itself. Deny is then safe for the app and is
  # still in front of the operator, because `az keyvault secret set`, which the
  # seeded secret comment below tells people to use for rotation, runs from
  # wherever that person happens to be. A vault they cannot reach makes that
  # instruction quietly untrue, which is the failure this file already refuses
  # once.
  #
  # No plan can prove any of that, and getting it wrong is a production outage,
  # so it belongs with somebody who can apply it and watch what happens.
  # Recorded rather than silently accepted, and the expiry below is the whole
  # point of recording it that way. tfsec stops honouring that line on
  # 2027-03-03 and the CRITICAL comes back, into a job that now fails on it.
  public_network_access_enabled = true
  network_acls {
    bypass = "AzureServices"

    # The directive sits on the line it excuses rather than above the block, so
    # that changing this value means reading the reason. See the block above it.
    #tfsec:ignore:azure-keyvault-specify-network-acl:exp:2027-03-03
    default_action = "Allow"
  }

  tags = var.tags
}

# The identity the Container App runs as. User-assigned rather than
# system-assigned so that the role assignment can exist before the app does:
# a system-assigned identity is created with the app, which means the app's
# first revision starts before it can read anything and fails.
resource "azurerm_user_assigned_identity" "app" {
  name                = "${var.name}-id"
  location            = var.location
  resource_group_name = var.resource_group_name
  tags                = var.tags
}

resource "azurerm_role_assignment" "app_reads_secrets" {
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_user_assigned_identity.app.principal_id
}

# Whoever is running Terraform needs to be able to WRITE the secrets below, and
# this grants it. It is OFF BY DEFAULT, which is the opposite of what it was,
# and the reason is worth more than the resource.
#
# A ROLE ASSIGNMENT WHOSE PRINCIPAL IS "WHOEVER IS RUNNING TERRAFORM" CHURNS ON
# EVERY PLAN BY A DIFFERENT CALLER. principal_id is ForceNew, so a plan run by
# the CI identity against a state applied from a laptop does not report a small
# difference: it reports that a role assignment MUST BE REPLACED. The pull
# request plan job exists so that a change which would destroy something is
# visible in review, and this made every single run carry a destroy that meant
# nothing. A plan that always shows a destroy is a plan people stop reading,
# which is exactly how a real one gets waved through.
#
# So the caller-dependent resource is out of the stack by default, and the
# grant is one command run once by the operator, documented in
# self-hosting/azure.md:
#
#   az role assignment create --role "Key Vault Secrets Officer" \
#     --assignee-object-id "$(az ad signed-in-user show --query id -o tsv)" \
#     --assignee-principal-type User --scope <vault id>
#
# Set assign_deployer_secret_officer = true only where exactly one identity
# ever runs Terraform, and understand that any OTHER identity planning the
# stack will then propose to replace this.
resource "azurerm_role_assignment" "deployer_writes_secrets" {
  count                = var.assign_deployer_secret_officer ? 1 : 0
  scope                = azurerm_key_vault.this.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = coalesce(var.deployer_principal_id, data.azurerm_client_config.current.object_id)
}

# TWO KINDS OF SECRET, AND TERRAFORM MUST NOT TREAT THEM THE SAME.
#
# The database URLs are OWNED here: Terraform generated the passwords inside
# them, nothing else may change them, and a difference between config and vault
# is drift that should be corrected.
#
# The GitHub OAuth values are SEEDED here and owned elsewhere. Terraform cannot
# know them (creating an OAuth application is a human act on another service),
# so it writes a placeholder once and the operator replaces it. If Terraform
# kept managing the VALUE, three things would follow, and all three did:
#
#   every plan run without the real values proposes to overwrite the real ones
#   with placeholders, which is a plan that would BREAK sign-in if applied;
#   the pull request plan job, which cannot have the real values and must not,
#   shows three changes on every run forever;
#   and self-hosting/control-plane.md's instruction to rotate them with `az
#   keyvault secret set` is quietly untrue, because the next apply reverts it.
#
# ignore_changes on the value makes the rotation instruction true.
# The sealing secret for provider keys.
#
# Generated here rather than passed in, so that no person and no workflow ever
# holds it: it goes from the random provider into Key Vault and into the
# container's environment, and the only copy outside the vault is in Terraform
# state, which lives in a storage account with key access disabled.
#
# keepers is empty on purpose. Anything in it that changes regenerates the
# secret, and a regenerated sealing secret means every stored provider key
# stops opening -- silently, because a sealed value that will not decrypt looks
# exactly like a tampered one. There is no key rotation story that starts with
# "the infrastructure changed the secret without being asked".
resource "random_bytes" "provider_key_secret" {
  count = var.provider_key_secret_enabled ? 1 : 0
  # 32 bytes, which is what sealingKeyFrom() requires -- exactly, not at least.
  # random_bytes rather than random_password because the application wants
  # BYTES: a password of 32 characters is 32 bytes only by accident of encoding,
  # and base64 of 32 characters chosen from an alphabet is not 32 bytes of key
  # material. This resource's .base64 is the value the application parses.
  length = 32
}

# The key organization surrogates are computed under, 32 bytes as 64 hex
# characters.
#
# Generated here for the same reason the sealing secret is: a surrogate anybody
# can recompute is an organization identifier with extra steps, so the value
# must not pass through a person, a workflow, or a tfvars file.
#
# keepers is empty on purpose, and the consequence is different from the sealing
# secret's but just as permanent. Regenerating this does not stop anything from
# working -- it re-keys every surrogate, so the same organization becomes a
# different one, and every funnel that crosses the change silently splits in
# two. There is no analytics story that starts with "the infrastructure changed
# the key without being asked".
resource "random_bytes" "analytics_surrogate_secret" {
  count = var.analytics_enabled ? 1 : 0
  # 32 bytes, which is what surrogateSecretFrom() requires exactly. The
  # application wants them as 64 hex characters and stops at startup on any
  # other length, so .hex is the attribute rather than .base64.
  length = 32
}

locals {
  # provider-key-secret is owned rather than seeded: Terraform generates it, so
  # a difference between configuration and vault is drift to correct, which is
  # the definition of owned above.
  #
  # admin-database-url and analytics-surrogate-secret are owned on the same
  # test: Terraform generated the password and the key material inside them.
  owned_secrets = merge({
    "database-url"           = local.app_url
    "migration-database-url" = local.migration_url
    }, var.provider_key_secret_enabled ? {
    "provider-key-secret" = random_bytes.provider_key_secret[0].base64
    } : {}, var.operator_portal_enabled ? {
    "admin-database-url" = local.operator_url
    } : {}, var.analytics_enabled ? {
    "analytics-surrogate-secret" = random_bytes.analytics_surrogate_secret[0].hex
  } : {})
  seeded_secrets = {
    "github-client-id"     = var.github_client_id
    "github-client-secret" = var.github_client_secret
    "github-redirect-uri"  = var.github_redirect_uri
  }
}

resource "azurerm_key_vault_secret" "owned" {
  for_each     = local.owned_secrets
  name         = each.key
  value        = each.value
  key_vault_id = azurerm_key_vault.this.id
  content_type = "text/plain"
  tags         = var.tags

  depends_on = [azurerm_role_assignment.deployer_writes_secrets]
}

# The GitHub App's two secrets, ADDRESSED RATHER THAN READ.
#
# See the variable comments: GitHub mints the private key and shows it once, so
# Terraform cannot create it and must not manage it. A person puts both in the
# vault and the container app references them by id.
#
# THESE WERE `data "azurerm_key_vault_secret"` AND THAT MADE EVERY PLAN A
# PRIVILEGED READ. The container app never wants the secret's VALUE, only its
# versionless id, which is a URI built from the vault's own uri and the secret's
# name. Both are already here, so a data source was spending a Key Vault data
# plane read to obtain a string that is a function of two things in this
# configuration. Reading a secret to learn its address is the wrong shape.
#
# What that cost, measured rather than argued. `terraform plan -refresh=false`
# does NOT serve a data source from state; it evaluates it, so the read happened
# on every plan. On staging that passed, because the plan identity holds Key
# Vault Secrets Officer there. On production it holds Contributor on the group
# and nothing on the vault, so the moment `github_app_id` was set the production
# plan check failed and could only have been fixed by granting a pull request
# identity read access to production's App private key. That key signs as the
# installation on every customer repository this product is installed on, and a
# pull request can edit the workflow that uses the identity in the same commit.
# Constructing the id needs no role at all.
#
# WHAT THIS GIVES UP, and it is a real trade rather than a free win. The data
# source also asserted the secret EXISTS, so a missing secret failed at plan.
# Now it fails at apply, with Azure naming the secret it could not find. That
# check was only ever load bearing on staging: on production the identity that
# runs the plan cannot read the vault, so the plan could not assert it there
# either. An apply that stops with "secret not found" beats a plan that only
# verifies the environment where it does not matter.
#
# Empty on no App, so a stack without one renders no secret block at all, which
# is what the count on the old data sources was for.
#
# Addressed, not read: the trailing slash on vault_uri is trimmed rather than
# assumed, so this cannot depend on how the provider spells it.
locals {
  github_app_secret_ids = var.github_app_id == "" ? {} : {
    "github-app-private-key"    = "${trimsuffix(azurerm_key_vault.this.vault_uri, "/")}/secrets/${var.github_app_private_key_secret_name}"
    "github-app-webhook-secret" = "${trimsuffix(azurerm_key_vault.this.vault_uri, "/")}/secrets/${var.github_app_webhook_secret_name}"
  }
}

# Stripe's two credentials and Resend's one, read rather than written, and the
# reason is the same one the GitHub App's key gets.
#
# THESE ARE NOT SEEDED EITHER, and the difference from github-client-secret
# above is worth stating because it looks like the same case. A seeded secret is
# written once as a placeholder and then owned by the operator, which means
# there is a window where the vault holds a placeholder and the application is
# running with it. For an OAuth secret that window is a sign-in that fails. For
# a Stripe key it is billing reporting itself as ON at startup while every
# charge is refused by Stripe, which is exactly the "partially configured"
# state the application prints a warning about -- reached by the tool that was
# supposed to configure it.
#
# So there is no placeholder and no tfvars input for the VALUE. A person puts
# the real secret in the vault, then sets the switch that turns the feature on;
# a plan with the switch on and no secret in the vault fails on these data
# sources, naming the secret, which is a truthful refusal rather than a deploy
# that comes up broken. self-hosting/azure.md has the two commands in order.
data "azurerm_key_vault_secret" "stripe_secret_key" {
  count        = var.stripe_price_team == "" ? 0 : 1
  name         = var.stripe_secret_key_secret_name
  key_vault_id = azurerm_key_vault.this.id
}

data "azurerm_key_vault_secret" "stripe_webhook_secret" {
  count        = var.stripe_price_team == "" ? 0 : 1
  name         = var.stripe_webhook_secret_secret_name
  key_vault_id = azurerm_key_vault.this.id
}

data "azurerm_key_vault_secret" "resend_api_key" {
  count        = var.mail_from == "" ? 0 : 1
  name         = var.resend_api_key_secret_name
  key_vault_id = azurerm_key_vault.this.id
}

# NO EXPIRY ON THESE THREE, AND THE REPORT THAT ASKS FOR ONE IS THREE THINGS
# WRONG AT ONCE.
#
# tfsec reports six LOW results here under azure-keyvault-ensure-secret-expiry,
# "Secret should have an expiry date specified". Three things about that:
#
# IT IS THREE SECRETS, NOT SIX. tfsec emits each one twice, once against this
# resource and once against the module call in stacks/control-plane. The three
# are github-client-id, github-client-secret and github-redirect-uri, and two of
# them are not credentials at all: an OAuth client id is in the address bar of
# every person who signs in, and the redirect URI is written in plain text in
# production.tfvars in this repository. Only github-client-secret is a secret,
# and it is the one an operator rotates by hand at GitHub.
#
# IT DOES NOT SEE THE SECRETS THAT MATTER, AND THE LIST GREW WHILE THIS WAS
# BEING WRITTEN. azurerm_key_vault_secret.owned above sets no expiration_date
# either and tfsec reports nothing at all about it. It holds five:
#
#   database-url                the application's database credential
#   migration-database-url      the one that can run DDL
#   provider-key-secret         the sealing key for customers' provider keys
#   admin-database-url          the operator portal's antifailure_admin login
#   analytics-surrogate-secret  the key every analytics surrogate is derived from
#
# The last two arrived after this comment was first written, which is the point
# rather than an aside: the unscanned half of this vault grows and the scanner
# stays quiet about it. Rewriting local.owned_secrets as a literal map makes the
# same rule fire on all five, so the silence is tfsec failing to evaluate that
# expression rather than a difference in this configuration. Anybody who "fixed"
# the six results the scanner prints would leave both database credentials, the
# provider sealing key and the analytics key carrying the exact property the rule
# exists to complain about, and the report would come back clean. That is worse
# than the finding.
#
# AND AN EXPIRY WOULD ENFORCE NOTHING. This is the part that looks backwards and
# is not. Key Vault's own documentation says exp on a SECRET "is for
# informational purposes only", and that "a secret's get operation works for
# not-yet-valid and expired secrets". So a date here is neither the scheduled
# outage it resembles nor a control: the app keeps reading the secret either
# way. It is metadata.
#
# Both ways to write that metadata in Terraform are worse than leaving it out:
#
#   A literal date is a date somebody has to edit by hand, and when it passes
#   nothing breaks, so nobody finds out. An expiry whose lapsing is invisible is
#   protection that is not there, which is the failure tools/vulncheck exists to
#   catch in the file next door.
#
#   timeadd(timestamp(), ...) is evaluated on every plan, so it would show these
#   three secrets as changed forever. stacks/tfstate/variables.tf already refuses
#   timestamp() for that reason on the policy exemption, and this file argues at
#   length that a plan which always shows a diff is a plan people stop reading.
#
# The seeded half has a third problem on top. `az keyvault secret set`, which
# self-hosting/control-plane.md tells the operator to use, writes a NEW VERSION
# carrying no expiry at all. Terraform would put one back at the next apply,
# which might be months later, and in between the vault says one thing and the
# configuration says another.
#
# WHAT WOULD MAKE IT REAL, which is the trigger to revisit rather than a date
# picked to look responsible. Key Vault raises SecretNearExpiry and
# SecretExpired through Event Grid, and modules/alerting already owns this
# stack's action groups. An expiry wired to those is a rotation reminder for
# github-client-secret and worth having. An expiry with nothing watching it is a
# date in a portal.
#tfsec:ignore:azure-keyvault-ensure-secret-expiry:exp:2027-03-03
resource "azurerm_key_vault_secret" "seeded" {
  for_each     = local.seeded_secrets
  name         = each.key
  value        = each.value
  key_vault_id = azurerm_key_vault.this.id
  content_type = "text/plain"
  tags         = var.tags

  lifecycle {
    # Seeded once, then owned by the operator. See the block comment above.
    ignore_changes = [value]
  }

  depends_on = [azurerm_role_assignment.deployer_writes_secrets]
}

locals {
  # One map so callers do not have to know which half a name lives in.
  secret_by_name = merge(azurerm_key_vault_secret.owned, azurerm_key_vault_secret.seeded)
}

# ---------------------------------------------------------------------------
# The rename, declared instead of executed.
#
# `azurerm_key_vault_secret.this` was split into `.owned` and `.seeded` so that
# Terraform could stop overwriting the values an operator sets by hand. The
# split changed the resource ADDRESSES and nothing moved the state, so every
# plan since has proposed to destroy all six secrets and create six new ones
# with the same names.
#
# That is not a cosmetic diff. `terraform apply` on that plan deletes the live
# github-client-secret and writes back whatever the caller passed in
# TF_VAR_github_client_secret -- and infra.yml passes the literal string
# "plan-only". It is the most plausible explanation for how this vault came to
# hold "PLACEHOLDER-not-a-real-oauth-app" in the first place, and it would have
# done it again to the real secret on the next apply by anybody.
#
# `moved` rather than `terraform state mv`, because a state move fixes one
# person's copy and this fixes it in the repository: every plan, in CI and on
# every machine, reads these and treats the rename as a rename.
# ---------------------------------------------------------------------------

moved {
  from = azurerm_key_vault_secret.this["database-url"]
  to   = azurerm_key_vault_secret.owned["database-url"]
}

moved {
  from = azurerm_key_vault_secret.this["migration-database-url"]
  to   = azurerm_key_vault_secret.owned["migration-database-url"]
}

moved {
  from = azurerm_key_vault_secret.this["provider-key-secret"]
  to   = azurerm_key_vault_secret.owned["provider-key-secret"]
}

moved {
  from = azurerm_key_vault_secret.this["github-client-id"]
  to   = azurerm_key_vault_secret.seeded["github-client-id"]
}

moved {
  from = azurerm_key_vault_secret.this["github-client-secret"]
  to   = azurerm_key_vault_secret.seeded["github-client-secret"]
}

moved {
  from = azurerm_key_vault_secret.this["github-redirect-uri"]
  to   = azurerm_key_vault_secret.seeded["github-redirect-uri"]
}
