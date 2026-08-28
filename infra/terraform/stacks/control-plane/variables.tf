variable "subscription_id" {
  type        = string
  description = "Supplied by CI or ARM_SUBSCRIPTION_ID. Not committed: no resource identifier belongs in this repository."
}

variable "resource_group_name" {
  type    = string
  default = "af-cp-centralus"
}

variable "location" {
  type        = string
  default     = "centralus"
  description = <<-EOT
    THE THIRD REGION THIS STACK HAS TRIED, AND EACH MOVE WAS FORCED BY A
    DIFFERENT SYSTEM THAT A PLAN CANNOT SEE.

    southcentralus, which the spec names, is denied by a policy assignment
    called bonfire-allowed-locations. It is a deny EFFECT, so a plan in
    southcentralus is perfectly clean and every resource is refused at apply.

    eastus is allowed by that policy and is cheaper, so this defaulted there.
    An apply then failed on the database with

      ParameterOutOfRange: The value of 'Version' should be in: []

    and the empty list is literal. `az postgres flexible-server list-skus -l
    eastus` returns supportedServerVersions: [] with the reason "Provisioning
    is restricted in this region." PostgreSQL flexible server cannot be created
    in eastus on this subscription at all, at any version, in any SKU. Every
    other resource in this stack creates there quite happily, which is why the
    apply got 26 resources deep before finding out.

    centralus offers versions 11 through 18 and every burstable SKU, and is
    allowed by the policy. It is the only region that satisfies all three
    gates. It costs about 2 USD a month more than eastus would have.

    QUOTA WAS NEVER THE CONSTRAINT. Both regions have 65 cores. Quota is the
    thing everybody checks and it was the one thing that was never in the way.
  EOT

  # This validation lives HERE and not only on the module, and that is the
  # whole point of the comment.
  #
  # The module's location comes from module.foundation.location, which is
  # azurerm_resource_group.this.location and therefore unknown until apply.
  # Terraform SKIPS a validation whose value is unknown, silently, so the
  # module-level check never fired and a plan in southcentralus was clean. A
  # guard that quietly does not run is worse than no guard, because somebody
  # trusts it. This variable is a real input, known at plan time, so this one
  # actually runs.
  #
  # It cannot check the third gate. Regional service restriction is not
  # expressible in a variable validation because it is a property of the
  # subscription that has to be asked for over the network. `azguard region`
  # asks, and self-hosting/azure.md says to run it first.
  validation {
    condition     = contains(["centralus"], var.location)
    error_message = "bonfire-allowed-locations denies every region except eastus, centralus and global, and PostgreSQL flexible server provisioning is RESTRICTED in eastus on this subscription (az postgres flexible-server list-skus -l eastus returns supportedServerVersions: []). centralus is the only region that satisfies both. Run `go run ./tools/azguard region centralus` before changing this."
  }
}

variable "name" {
  type    = string
  default = "afcp"
}

variable "monthly_budget_usd" {
  type    = number
  default = 300
}

variable "budget_contact_emails" {
  type    = list(string)
  default = []
}

variable "log_retention_days" {
  type    = number
  default = 30
}

variable "image_repository" {
  type    = string
  default = "ghcr.io/antifailure/control-plane"
}

variable "image_tag" {
  type    = string
  default = "v0.1.1"
}

variable "image_digest" {
  type    = string
  default = ""
}

variable "database_sku" {
  type    = string
  default = "B_Standard_B1ms"
}

variable "database_storage_mb" {
  type    = number
  default = 32768
}

variable "backup_retention_days" {
  type    = number
  default = 14
}

variable "high_availability" {
  type    = bool
  default = false
}

variable "min_replicas" {
  type    = number
  default = 1
}

variable "max_replicas" {
  type    = number
  default = 3
}

variable "app_base_url" {
  type    = string
  default = ""
}

variable "github_client_id" {
  type      = string
  sensitive = true
}

variable "github_client_secret" {
  type      = string
  sensitive = true
}

variable "github_redirect_uri" {
  type = string
}

variable "ci_principal_id" {
  type        = string
  default     = ""
  description = "Object id of the service principal GitHub Actions federates into. Gets Reader on this resource group and nothing else. Empty disables the grant."
}
