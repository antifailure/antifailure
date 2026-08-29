# The one policy exemption in this project, and why it exists.
#
# bonfire-deny-public-data is assigned at subscription scope and denies any
# storage account whose publicNetworkAccess is not "Disabled". `Disabled` is not
# a firewall default that a network rule can carve an exception out of: it turns
# the data plane off for everything that is not a private endpoint. A Terraform
# state account nobody can reach is worse than no remote state at all, because
# the CI plan job then plans from an empty state, and a plan from an empty state
# CANNOT REPORT A DESTROY. That is the entire reason the plan job exists.
#
# WHAT THIS EXEMPTION IS AND IS NOT. It is scoped to THIS RESOURCE GROUP. It
# changes no policy assignment, no policy definition, and nothing bonfire,
# Ravioli, postiz or any other project owns. Deleting it restores the policy
# instantly and denies the next write to this account. It names one assignment,
# not all of them: bonfire-sku-allowlist still applies here, which is why the
# account is Standard_LRS rather than the GRS this file originally asked for.
#
# THE CATEGORY IS "Mitigated" RATHER THAN "Waiver", AND THAT IS A CLAIM THIS
# FILE HAS TO EARN. A waiver says "we accept the risk". Mitigated says "the
# policy's intent is achieved by other means", so here are the other means, all
# of them enforced in main.tf a few lines away and all of them checkable:
#
#   shared_access_key_enabled = false   No storage key exists. A key is the one
#                                       credential that cannot be revoked per
#                                       person and appears in no audit trail as
#                                       a name, and this account holds every
#                                       database password the project generates.
#   allow_nested_items_to_be_public     No blob or container can be made
#     = false                           anonymously readable, whatever anyone
#                                       later sets on the container.
#   container_access_type = "private"   The container is not public today.
#   min_tls_version = "TLS1_2"          No downgrade.
#   RBAC on the data plane              Reading a byte requires a Microsoft
#                                       Entra identity holding a data role on
#                                       this account. Reaching the endpoint is
#                                       not the same as reading it.
#
# So what the exemption actually restores is REACHABILITY, not readability. The
# thing the policy was written to prevent, data readable by the public, is still
# prevented, by four settings instead of one.
resource "azurerm_resource_group_policy_exemption" "state_is_reachable" {
  name                 = "af-tfstate-reachable"
  resource_group_id    = module.foundation.resource_group_id
  policy_assignment_id = "/subscriptions/${var.subscription_id}/providers/Microsoft.Authorization/policyAssignments/${var.public_data_policy_assignment_name}"

  exemption_category = "Mitigated"
  display_name       = "Terraform state must be reachable by the runner that writes it"

  # Azure caps this field at 512 characters, so it says the operative facts and
  # the long reasoning stays in the comment above, where it is not truncated.
  # Azure caps this field at 512 characters, so it carries the operative facts
  # and the reasoning stays in the comment above, where it is not truncated.
  description = <<-EOT
    Exempts ONLY this resource group. publicNetworkAccess=Disabled makes a
    storage account reachable only via a private endpoint, and state no laptop
    or hosted runner can reach forces the CI plan job to plan from an empty
    state, where a destroy cannot appear. Intent preserved by other means: no
    storage keys, nothing anonymously public, private container, TLS1.2 floor,
    and every read needs an Entra identity with a data role. Reachable is not
    readable. Delete this and the next write to the account is denied.
  EOT

  # An exemption with no end date is a policy change wearing a temporary hat.
  # This one expires and has to be looked at again on purpose.
  expires_on = var.exemption_expires_on

  metadata = jsonencode({
    project      = "antifailure"
    owner        = "lane10"
    reason       = "terraform state reachability"
    reviewBefore = var.exemption_expires_on
  })
}
