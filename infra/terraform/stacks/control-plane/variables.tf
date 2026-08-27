variable "subscription_id" {
  type        = string
  description = "Supplied by CI or ARM_SUBSCRIPTION_ID. Not committed: no resource identifier belongs in this repository."
}

variable "resource_group_name" {
  type    = string
  default = "af-cp-scus"
}

variable "location" {
  type        = string
  default     = "southcentralus"
  description = "Both southcentralus and eastus were confirmed to have 65 cores of quota on this subscription, so the choice is the spec's naming rather than a capacity constraint."
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
