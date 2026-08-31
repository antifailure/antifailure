output "app_url" { value = module.control_plane.app_url }
output "key_vault_name" { value = module.control_plane.key_vault_name }
output "key_vault_id" { value = module.control_plane.key_vault_id }
output "goldens_account" { value = module.control_plane.goldens_account }
output "bootstrap_job_name" { value = module.control_plane.bootstrap_job_name }
output "resource_group" { value = module.foundation.resource_group_name }


# Empty when alerting_enabled is false, which is how `terraform output` answers
# "is anything watching this" without a portal.
output "alert_names" {
  value = one(module.alerting[*].alert_names)
}

# The scope for `az monitor action-group test-notifications create`, which is
# the one command that proves a page reaches a person. An action group that
# creates cleanly and delivers nothing looks exactly like one that works, so
# this exists to make testing it a copy rather than an id assembled by hand.
output "action_group_id" {
  value = one(module.alerting[*].action_group_id)
}

# The value the asuid TXT record has to carry. Terraform writes that record
# itself when the zone is in Azure DNS; this is for an installation whose DNS is
# at a registrar, where binding the name is a person's job.
#
# SENSITIVE BECAUSE THE PROVIDER SAYS SO, AND WITHOUT THIS LINE NO PLAN OF THIS
# STACK RUNS AT ALL. azurerm marks custom_domain_verification_id sensitive, and
# Terraform refuses to evaluate a ROOT module output carrying a sensitive value
# unless the output says it meant to. A module output infers its sensitivity, so
# modules/control-plane needs nothing and this does. The refusal is
#
#   Error: Output refers to sensitive values
#
# and it is raised while evaluating outputs, which is AFTER the resource diff is
# built and printed. So the plan appears in full, ends with "49 to add, 0 to
# change, 0 to destroy", and then exits non-zero: it looks like a plan that
# worked. It arrived with the production and alerting work and it broke BOTH
# environments, staging included, because sensitivity is a property of the
# expression rather than of custom_domain, which is empty on staging.
#
# The value itself is not a secret: it ends up in public DNS as a TXT record.
# The marking only keeps it out of the plan summary, which is world readable on
# a public repository, and this file already declines to output the paging
# addresses for that same reason.
#
# Read with `terraform output -raw custom_domain_verification_id`; the bare form
# prints "(sensitive value)".
output "custom_domain_verification_id" {
  value     = module.control_plane.custom_domain_verification_id
  sensitive = true
}
