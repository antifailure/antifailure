# What the pull request plan job is allowed to see.
#
# READER, at this resource group and nowhere else. It is enough to refresh every
# resource in the stack and it is not enough to change one. There is no
# subscription scoped assignment anywhere in this project, which is what makes a
# cleanup scoped to `project=antifailure` unable to reach anybody else's work,
# and what makes this credential worthless to anybody who steals it.
#
# WHAT IS DELIBERATELY MISSING: Key Vault Secrets User. Refreshing an
# azurerm_key_vault_secret reads the secret's VALUE, so granting it would put
# the live database URLs into the plan job's memory on every pull request, and a
# pull request can edit the workflow that runs there in the same commit. The
# plan therefore runs with -refresh=false, and .github/workflows/infra.yml says
# so at the line that does it. Comparing configuration against state is exactly
# what "would this change destroy something" asks, and it is the question the
# job exists to answer.
resource "azurerm_role_assignment" "ci_reads_the_group" {
  count                = var.ci_principal_id == "" ? 0 : 1
  scope                = module.foundation.resource_group_id
  role_definition_name = "Reader"
  principal_id         = var.ci_principal_id
}

# What the DEPLOY job is allowed to do, which is a great deal more.
#
# CONTRIBUTOR, at this resource group and nowhere else. cd.yml updates the
# container app's image, starts the bootstrap job and shifts ingress traffic, and
# none of that is possible with the Reader above. Without this assignment the
# production job fails at its first `az containerapp` call, so continuous
# deployment cannot reach production at all.
#
# THIS IS THE SAME PRINCIPAL AS THE READER ABOVE, AND THAT IS THE UNCOMFORTABLE
# PART, stated here rather than left for somebody to discover from a role
# listing. One identity both plans pull requests and deploys, so the sentence in
# the comment above about a credential being worthless to anybody who steals it
# does not hold on a stack that sets this: a pull request can edit the workflow
# that uses the credential in the same commit that runs it. Splitting the two
# identities is the real fix and it breaks continuous deployment until the
# second one is federated, so it is a decision somebody has to make rather than
# something to slip into this file.
#
# Contributor supersedes Reader, so setting both variables to the same object id
# creates two assignments where one would do. Production sets only this one.
#
# WHY THIS IS HERE AND NOT AN `az role assignment create`. Staging's equivalent
# grant was made by hand and is therefore invisible to anybody reading this
# stack, absent from its state, and unable to survive a rebuild. It is also the
# reason `az role assignment list` on staging's group returns Contributor while
# this file describes Reader. A grant that decides whether deployment works
# belongs in the code that everything else about the environment lives in.
#
# THIS BLOCK HAS BEEN DELETED ONCE ALREADY AND THAT IS WHY THE PARAGRAPH BELOW
# EXISTS. ff893073, whose subject and whole message are about a Postgres test
# port, carried a stale checkout of twelve unrelated files and took this
# resource, its variable and its production value with it. Nothing failed:
# production kept serving, because deploys run through `az containerapp update`
# rather than through Terraform, and the stack could not be planned at all until
# the step added in this same branch. The grant simply became a live assignment
# in Azure and in state that no configuration declared, which is to say a
# pending `1 to destroy` sitting in front of whoever applied next. The gate in
# tools/planguard exists to make that specific silence audible, and it reads the
# acknowledgement file beside it rather than this comment.
resource "azurerm_role_assignment" "cd_deploys_the_group" {
  count                = var.cd_principal_id == "" ? 0 : 1
  scope                = module.foundation.resource_group_id
  role_definition_name = "Contributor"
  principal_id         = var.cd_principal_id
}

# What the deploy job needs in order to PLAN the app before it deploys it.
#
# KEY VAULT SECRETS USER, on this stack's vault and nowhere else. cd.yml runs
# deploy/cd/apply-config.sh before deploy.sh, which plans the container app
# from the tfvars with a refresh. A refresh of the app refreshes its
# dependencies, and those include every azurerm_key_vault_secret the app
# references, and refreshing one of those reads the secret's VALUE. Without
# this grant the plan fails on the first vault read with a 403 and no
# configuration reaches production without a person, which is the exact state
# this branch exists to end.
#
# WHY THE REFRESH IS NOT SIMPLY TURNED OFF, since infra.yml's plan job avoids
# this grant by planning with -refresh=false. The app's image is in
# ignore_changes, so the value an apply writes back for it is the value in the
# prior state. With a refresh, that is the digest deploy.sh last shipped. With
# -refresh=false it is whatever the state recorded at the last apply, which
# after any number of deploys is an older build, and the apply would create a
# revision on it. Every hand apply on 2026-09-05 refreshed, which is why the
# revisions it made carried the serving image.
#
# THE SAME TRADE AS THE CONTRIBUTOR ABOVE, and the same paragraph applies: the
# plan job's identity is this identity, so a pull request that edits infra.yml
# in the same commit could read the vault. Staging has been in that position
# since 2026-08-28, when the identity was handed Key Vault Secrets Officer on
# afcp-kv-centralus by hand, and every plan since has read that vault; the
# infra.yml comment on the staging plan says so. This declares the narrower
# read role, for production, where the grant did not exist at all on
# 2026-09-06 (`az role assignment list` on afcpprod-kv-centralus for
# af-infra-ci returned nothing).
#
# THIS RESOURCE IS NOT INSIDE THE TARGETED APPLY that cd.yml runs. The app does
# not depend on it, so the configuration apply never creates it, and the first
# production run of apply-config.sh fails on the vault read until somebody
# applies this once by hand:
#
#   terraform apply -var-file=production.tfvars \
#     -target='azurerm_role_assignment.cd_refreshes_secrets[0]'
#
# Staging's hand-made Officer grant already covers staging. Setting
# cd_principal_id in staging.tfvars would also declare a second Contributor
# over the hand-made one, which Azure refuses as RoleAssignmentExists, so
# staging stays as ci.tf's Contributor paragraph describes it: granted by hand,
# recorded nowhere, and working.
resource "azurerm_role_assignment" "cd_refreshes_secrets" {
  count                = var.cd_principal_id == "" ? 0 : 1
  scope                = module.control_plane.key_vault_id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = var.cd_principal_id
}
