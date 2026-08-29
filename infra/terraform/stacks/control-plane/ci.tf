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
