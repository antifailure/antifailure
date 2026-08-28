output "app_url" { value = module.control_plane.app_url }
output "key_vault_name" { value = module.control_plane.key_vault_name }
output "goldens_account" { value = module.control_plane.goldens_account }
output "bootstrap_job_name" { value = module.control_plane.bootstrap_job_name }
output "resource_group" { value = module.foundation.resource_group_name }

output "registry_login_server" {
  value       = module.control_plane.registry_login_server
  description = "Where continuous deployment pushes. Empty when the module's registry is disabled."
}
