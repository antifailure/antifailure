# app.antifailure.dev, and everything Terraform can own of it.
#
# Four resources in one order that works and several that do not. Azure will not
# issue a managed certificate for a name it cannot prove you control, and it
# proves control by resolving DNS, so the records have to exist and be correct
# before the certificate is asked for, and the certificate has to exist before
# the binding can reference it.
#
#   CNAME <sub>        ->  the container app's default FQDN
#   TXT   asuid.<sub>  ->  the app's custom_domain_verification_id
#   managed certificate for the full name
#   custom domain binding on the app, referencing that certificate
#
# THE TXT RECORD IS NOT OPTIONAL AND IS THE PART PEOPLE MISS. A CNAME alone
# proves that a name points at an Azure endpoint, not that it points at YOUR
# endpoint, so Azure would otherwise let anyone with a CNAME claim a domain
# already parked on somebody else's app. asuid.<sub> carries the verification id
# that ties the name to this specific container app. The staging domain has
# exactly this pair, `app.dev` and `asuid.app.dev` in the antifailure.dev zone,
# put there by hand; this file is what stops the production pair being another
# thing somebody remembers to do.
#
# THE ZONE IS IN A DIFFERENT RESOURCE GROUP, which is why the group is a
# variable rather than derived. antifailure.dev is in af-web with the marketing
# site, and the identity that applies this stack needs DNS Zone Contributor
# there. That is the one grant this file needs and does not create: a stack that
# could grant itself write access to a zone in another group would defeat the
# point of scoping it.

locals {
  custom_domain_enabled = var.custom_domain != ""

  # "app.antifailure.dev" in the "antifailure.dev" zone is the record "app".
  # Azure DNS wants the name relative to the zone and rejects the full one.
  custom_domain_record = local.custom_domain_enabled ? trimsuffix(var.custom_domain, ".${var.dns_zone_name}") : ""
}

resource "azurerm_dns_cname_record" "app" {
  count = local.custom_domain_enabled ? 1 : 0

  name                = local.custom_domain_record
  zone_name           = var.dns_zone_name
  resource_group_name = var.dns_zone_resource_group
  record              = azurerm_container_app.this.ingress[0].fqdn

  # Five minutes rather than the hour Azure defaults to. This record is created
  # minutes before Azure is asked to validate it, and if anything queried the
  # name first, the negative answer is cached for the TTL of the zone's SOA.
  # A short TTL here is also what makes moving this name to another app a change
  # that takes effect in minutes rather than in an hour.
  ttl = 300

  tags = var.tags

  lifecycle {
    precondition {
      condition     = !local.custom_domain_enabled || endswith(var.custom_domain, ".${var.dns_zone_name}")
      error_message = "custom_domain ${var.custom_domain} is not inside the zone ${var.dns_zone_name}, so no record in that zone can serve it. Set dns_zone_name to the zone that actually holds the name."
    }
  }
}

resource "azurerm_dns_txt_record" "asuid" {
  count = local.custom_domain_enabled ? 1 : 0

  name                = "asuid.${local.custom_domain_record}"
  zone_name           = var.dns_zone_name
  resource_group_name = var.dns_zone_resource_group
  ttl                 = 300

  record {
    value = azurerm_container_app.this.custom_domain_verification_id
  }

  tags = var.tags
}

# The certificate Azure issues and renews for free.
#
# Not a certificate this repository holds. There is no private key here, nothing
# to rotate, and nothing that expires because somebody left. What can go wrong
# is a renewal that fails silently, which is why the alerting module runs a test
# that fails when the presented certificate has three weeks left.
#
# CNAME validation rather than HTTP or TXT, because the CNAME has to exist
# anyway for the domain to serve. HTTP validation would additionally require the
# app to be answering on the name before it has a certificate for the name.
resource "azurerm_container_app_environment_managed_certificate" "app" {
  count = local.custom_domain_enabled ? 1 : 0

  name                         = replace(var.custom_domain, ".", "-")
  container_app_environment_id = azurerm_container_app_environment.this.id
  subject_name                 = var.custom_domain
  domain_control_validation    = "CNAME"

  tags = var.tags

  # Both records, not just the CNAME. Azure reads them in the same validation
  # and a certificate request that arrives before the TXT record exists fails
  # with a message about domain ownership rather than about DNS.
  depends_on = [
    azurerm_dns_cname_record.app,
    azurerm_dns_txt_record.asuid,
  ]
}

# The binding, as a separate resource rather than an ingress block.
#
# azurerm_container_app's ingress carries a custom_domain list, and using both
# it and this resource makes the two fight: every apply would see the binding
# this resource created as an unexpected value inside the app's ingress and
# propose to remove it. app.tf therefore ignores that attribute, and this is the
# one place a custom domain is declared.
resource "azurerm_container_app_custom_domain" "app" {
  count = local.custom_domain_enabled ? 1 : 0

  name                                     = var.custom_domain
  container_app_id                         = azurerm_container_app.this.id
  container_app_environment_certificate_id = azurerm_container_app_environment_managed_certificate.app[0].id
  certificate_binding_type                 = "SniEnabled"
}
