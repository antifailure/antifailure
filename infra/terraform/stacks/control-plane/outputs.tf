output "app_url" { value = module.control_plane.app_url }
output "key_vault_name" { value = module.control_plane.key_vault_name }
output "key_vault_id" { value = module.control_plane.key_vault_id }
output "goldens_account" { value = module.control_plane.goldens_account }
output "bootstrap_job_name" { value = module.control_plane.bootstrap_job_name }
output "resource_group" { value = module.foundation.resource_group_name }

