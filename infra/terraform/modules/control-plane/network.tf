# The network the control plane lives in.
#
# Two delegated subnets, because both services demand delegation and a subnet
# can only be delegated to one of them. The database is reachable from inside
# this network and from nowhere else: no public endpoint, no firewall rule
# listing an office IP that stopped being the office two years ago.

terraform {
  required_version = ">= 1.9.0"
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.16" }
    random  = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

resource "azurerm_virtual_network" "this" {
  name                = "${var.name}-vnet"
  location            = var.location
  resource_group_name = var.resource_group_name
  address_space       = [var.vnet_cidr]
  tags                = var.tags
}

# Container Apps wants a reasonably large subnet: the platform runs its own
# infrastructure inside it and a /27 that looks sufficient will fail at create
# time with a message about address space.
resource "azurerm_subnet" "apps" {
  name                 = "apps"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [cidrsubnet(var.vnet_cidr, 4, 0)]

  delegation {
    name = "container-apps"
    service_delegation {
      name    = "Microsoft.App/environments"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

resource "azurerm_subnet" "database" {
  name                 = "database"
  resource_group_name  = var.resource_group_name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [cidrsubnet(var.vnet_cidr, 8, 16)]

  # DECLARED BECAUSE AZURE ADDS IT, NOT BECAUSE THIS MODULE WANTS IT.
  #
  # Creating a flexible server on a delegated subnet makes the platform attach
  # the Microsoft.Storage service endpoint itself, for the server's own backup
  # and storage traffic. Terraform did not put it there, so the next plan
  # proposes to REMOVE it, and it does so quietly in the middle of an unrelated
  # diff: a two-line `~ service_endpoints` change under a subnet nobody was
  # thinking about.
  #
  # Left undeclared, every future apply strips a setting the database service
  # depends on and Azure puts it back, so the stack never converges and every
  # plan carries a change that is not a change. Declaring it makes the plan
  # honest and stops the fight.
  service_endpoints = ["Microsoft.Storage"]

  delegation {
    name = "postgres"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

# Private access to Postgres resolves through this zone. Without the link, the
# server's FQDN resolves to nothing from inside the VNet and every connection
# fails with a DNS error that looks nothing like a networking problem.
resource "azurerm_private_dns_zone" "postgres" {
  name                = "${var.name}.private.postgres.database.azure.com"
  resource_group_name = var.resource_group_name
  tags                = var.tags
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                  = "${var.name}-postgres-link"
  resource_group_name   = var.resource_group_name
  private_dns_zone_name = azurerm_private_dns_zone.postgres.name
  virtual_network_id    = azurerm_virtual_network.this.id
  registration_enabled  = false
  tags                  = var.tags
}
