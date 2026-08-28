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

# Events are kept for a year on staging. Null would keep them forever, which on
# a partitioned table means a partition per month and no reason to ever drop
# one.
event_retention_months = 12

# The vault that already exists. Without this the module computes "afcp-kv",
# which is a different vault, and a plan that renames a Key Vault destroys it.
key_vault_name = "afcp-kv-centralus"
