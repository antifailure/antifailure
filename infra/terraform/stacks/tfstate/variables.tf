variable "subscription_id" {
  type = string
}

variable "resource_group_name" {
  type    = string
  default = "af-tfstate-eastus"
}

variable "location" {
  type    = string
  default = "eastus"
}

variable "storage_account_name" {
  type        = string
  description = "Globally unique, 3 to 24 lower case letters and digits. Not defaulted: a name that collides fails late and confusingly."
  validation {
    condition     = can(regex("^[a-z0-9]{3,24}$", var.storage_account_name))
    error_message = "A storage account name is 3 to 24 lower case letters and digits."
  }
}

variable "public_data_policy_assignment_name" {
  type        = string
  default     = "bonfire-deny-public-data"
  description = "The subscription scoped assignment this resource group is exempted from, and ONLY this one. Named as a variable so the exemption cannot silently widen to a different assignment by editing a string in the middle of a resource block."
}

variable "exemption_expires_on" {
  type        = string
  default     = "2027-08-27T00:00:00Z"
  description = <<-EOT
    RFC3339. A fixed date rather than timestamp() plus a duration, because
    timestamp() is evaluated on every plan and would show this resource as
    changed forever, which trains everybody to ignore a diff on a policy
    exemption. Bumping this is meant to be a deliberate edit somebody reviews.
  EOT
  validation {
    condition     = can(formatdate("YYYY-MM-DD", var.exemption_expires_on))
    error_message = "exemption_expires_on must be an RFC3339 timestamp, for example 2027-08-27T00:00:00Z."
  }
}

variable "ci_principal_id" {
  type        = string
  default     = ""
  description = <<-EOT
    Object id of the service principal GitHub Actions federates into, which gets
    READ access to the state and nothing more. Empty disables the grant.

    A count on this is safe where a count on a resource attribute is not: this is
    a real input, known at plan time. `count` on something unknown until apply
    fails the entire plan, which is how the location guard in the control plane
    stack came to be silently skipped.
  EOT
}
