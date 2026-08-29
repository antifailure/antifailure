# Written straight into the backend config the other stacks init with, so the
# two cannot disagree:
#
#   terraform -chdir=../tfstate output -raw backend_hcl > ../control-plane/backend.hcl
#
# THAT PROMISE HOLDS FOR A LAPTOP AND NOT FOR CI, and the gap cost a red check.
# .github/workflows/infra.yml cannot read this output, because reading it means
# running this stack, so it repeats these settings as -backend-config flags. It
# repeated four of the five and dropped use_azuread_auth, and the symptom was
# not "a missing setting": the backend silently fell back to a STORAGE KEY and
# called listKeys on an account with shared_access_key_enabled = false, which
# fails with an authorization error that reads as a missing role.
#
# If you change anything below, change the flags in that workflow in the SAME
# COMMIT. There is no mechanism keeping them honest, which is why this is
# written down here rather than assumed.
output "backend_hcl" {
  value = <<-EOT
    resource_group_name  = "${module.foundation.resource_group_name}"
    storage_account_name = "${azurerm_storage_account.state.name}"
    container_name       = "${azurerm_storage_container.state.name}"
    key                  = "control-plane.tfstate"
    use_azuread_auth     = true
  EOT
}
