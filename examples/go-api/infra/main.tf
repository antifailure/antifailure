// The infrastructure this service runs on in production.
//
// It is here because `af change` reads it. Nothing in a run applies it: the
// environment a rehearsal brings up is built from antifailure.yaml, and these
// files are what a pull request changes when it changes production's own shape.
// Before the analyser read them, a pull request that touched only this
// directory selected no check at all and the whole run was skipped.
//
// Kept small on purpose. It is a worked example of what the analyser reads out
// of infrastructure as code, not a module anybody should copy into a cloud
// account.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  type        = string
  default     = "us-east-1"
  description = "Where the orders API and its database live."
}

locals {
  name = "orders-api"

  tags = {
    application = local.name
    managed_by  = "terraform"
  }
}
