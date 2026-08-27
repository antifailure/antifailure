# Written straight into the backend config the other stacks init with, so the
# two cannot disagree:
#
#   terraform -chdir=../tfstate output -raw backend_hcl > ../control-plane/backend.hcl
output "backend_hcl" {
  value = <<-EOT
    resource_group_name  = "${module.foundation.resource_group_name}"
    storage_account_name = "${azurerm_storage_account.state.name}"
    container_name       = "${azurerm_storage_container.state.name}"
    key                  = "control-plane.tfstate"
    use_azuread_auth     = true
  EOT
}
