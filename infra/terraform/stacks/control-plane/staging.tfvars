# Staging: app.dev.antifailure.dev
#
# Everything here is a decision, not a secret. The three values that are secret
# (subscription id, GitHub client id, GitHub client secret) are passed as
# TF_VAR_ environment variables by the deploy workflow and never written here.
#
# The region is centralus rather than the eastus in the variable defaults. The
# subscription's bonfire-allowed-locations policy permits eastus, centralus and
# global; the existing group was created in centralus and moving a Postgres
# flexible server between regions means recreating it.

resource_group_name = "af-cp-centralus"
location            = "centralus"
name                = "afcp"

# The public origin. Both of these must agree with the OAuth App's registered
# callback exactly, or sign-in fails with a redirect_uri mismatch that GitHub
# reports and this application never sees.
app_base_url        = "https://app.dev.antifailure.dev"
github_redirect_uri = "https://app.dev.antifailure.dev/auth/github/callback"

# One replica that stays up. Scaling to zero would save a few dollars and make
# the post-deploy health gate meaningless: the first probe would be measuring a
# cold start rather than the revision.
min_replicas = 1
max_replicas = 3

# FIVE, NOT THE TEN THIS STACK USED TO INHERIT, AND THE ARITHMETIC IS THE
# REASON RATHER THAN THRIFT.
#
# B1ms answers max_connections = 50, and Postgres holds back
# reserved_connections (5) and superuser_reserved_connections (10) for roles
# this application is deliberately not a member of. So it gets 35, not 50.
#
#   (max_replicas 3 + one rollback revision at min_replicas 1) x 10 = 40
#   plus 4 for the bootstrap job, the maintenance job, break-glass and backup
#   = 44 against 35. It did not fit and nothing computed it.
#
# At 5 the same sum is 24, which fits with room. Five connections per replica
# is ample for a control plane whose sign-in allowlist has two people on it;
# production keeps 10 because its server hands out 844.
#
# What this number cost when nobody owned it: deploy/cd/deploy.sh left every
# superseded revision active on the theory that a revision at zero traffic is
# free. It is not, it keeps min_replicas running, and forty six deploys meant
# forty six control plane processes holding forty six pools against those
# thirty five slots. /readyz answered "remaining connection slots are reserved
# for roles with privileges of the pg_use_reserved_connections role".
pool_max = 5

# Events are kept for a year on staging. Null would keep them forever, which on
# a partitioned table means a partition per month and no reason to ever drop
# one.
event_retention_months = 12

# The vault that already exists. Without this the module computes "afcp-kv",
# which is a different vault, and a plan that renames a Key Vault destroys it.
key_vault_name = "afcp-kv-centralus"

# Who may sign in.
#
# This deployment ran for its first days with no allowlist at all, which the
# application announced in its own start-up log --
#
#   sign-in is OPEN: any GitHub account may sign in (AF_SIGNIN_ALLOWLIST is not set)
#
# -- and nobody read. The brief said "keep signups closed behind an allowlist";
# the code for it was written, tested, and given no value.
#
# GitHub logins, lower-cased on read. Adding somebody is a change here and an
# apply, which is deliberate: an allowlist that can be edited in a portal is one
# nobody can review.
signin_allowlist = ["virsanghavi", "maksymrajszewski"]

# The GitHub App.
#
# Not a secret: an App ID is public, it appears in every JWT this control plane
# mints. The two values that are secret -- the PEM private key and the webhook
# secret -- are NOT here and are not managed by Terraform at all. GitHub mints
# the key once and shows it once, so Terraform cannot create it and must not own
# it; keyvault.tf reads both with a data source instead, which is also why
# setting this variable on a stack whose vault is missing them fails at plan
# rather than at the first delivery.
#
# Empty means no App, which is a supported way to run this: sign-in still works
# and /webhooks/github refuses every delivery rather than accepting unsigned
# ones. It was empty for the whole first week, which is why github_installations
# was empty, which is why everybody who signed in landed with no tenant.
github_app_id = "4756201"

# The Key Vault Secrets Officer grant on the human who runs Terraform here.
#
# True because it is true: the assignment exists on afcp-kv-centralus, it is
# what makes `az keyvault secret set` work for the two secrets Terraform
# deliberately does not own, and leaving this at its default of false makes
# every plan propose to destroy it. Removing a grant as a side effect of an
# unrelated apply is how somebody loses access to a vault at the worst moment.
#
# The default stays false in the module and that is right: the principal is
# whoever is calling, so on a stack that more than one identity plans, each of
# them would propose to replace the other's. Here exactly one person does.
assign_deployer_secret_officer = true

# And pinned, rather than "whoever is calling". That is the whole reason the
# module defaults the flag off: with this null the grant follows the caller, so
# the next identity to plan this stack proposes to replace it. This is the
# object id the grant already names.
deployer_principal_id = "3537595b-8059-4839-9cd8-04325c824291"
