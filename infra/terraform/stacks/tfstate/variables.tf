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
