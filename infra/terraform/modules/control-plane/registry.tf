# Where the image is pulled from.
#
# Not ghcr.io, and that is not a preference. The antifailure organization
# disallows public packages, so ghcr.io/antifailure/control-plane is private and
# a Container App can only pull it by holding a registry username and password
# for the lifetime of the revision. That is a long-lived credential sitting in a
# revision's secrets to solve a problem that a registry in the same tenant does
# not have.
#
# With a registry here, the app's user-assigned identity holds AcrPull and the
# pull is an Entra token exchange. There is no registry secret anywhere: not in
# a revision, not in Key Vault, not in a GitHub secret.
#
# It costs about 5 USD a month for Basic, against a 300 USD budget on this
# group. The ghcr publish in control-plane-image.yml is kept: it is the image
# docs/self-hosting/control-plane.md tells operators to run, and it is a
# separate concern from how this deployment pulls.

resource "azurerm_container_registry" "this" {
  count = var.registry_enabled ? 1 : 0

  name                = substr(replace("${var.name}acr", "-", ""), 0, 50)
  resource_group_name = var.resource_group_name
  location            = var.location
  sku                 = var.registry_sku

  # The identity that pulls is an Entra principal, so nothing needs the admin
  # user. Leaving it on would create a second way in that is a shared password
  # rather than an identity, and shared passwords do not appear in an access
  # review.
  admin_enabled = false

  tags = var.tags
}

# The application, the bootstrap job, and the maintenance job all run as this
# identity, so one assignment covers every pull.
resource "azurerm_role_assignment" "app_pulls_images" {
  count = var.registry_enabled ? 1 : 0

  scope                = azurerm_container_registry.this[0].id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_user_assigned_identity.app.principal_id
}

# Continuous deployment pushes here. Scoped to this registry: a principal that
# can push images can replace what production runs, so it gets that on one
# registry rather than on the resource group.
resource "azurerm_role_assignment" "ci_pushes_images" {
  count = var.registry_enabled && var.ci_principal_id != "" ? 1 : 0

  scope                = azurerm_container_registry.this[0].id
  role_definition_name = "AcrPush"
  principal_id         = var.ci_principal_id
}
