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
resource "azurerm_role_assignment" "cd_deploys_the_group" {
  count                = var.cd_principal_id == "" ? 0 : 1
  scope                = module.foundation.resource_group_id
  role_definition_name = "Contributor"
  principal_id         = var.cd_principal_id
}
