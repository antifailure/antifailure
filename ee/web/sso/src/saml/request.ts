// Sending a browser to the identity provider, and the metadata both sides
// exchange to make that possible.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The request goes out over the HTTP-Redirect binding: the AuthnRequest is
// deflated, base64'd, and put in a query parameter of a 302. The alternative,
// HTTP-POST, needs an HTML page carrying a form that submits itself with
// script, and this server answers `content-security-policy: default-src 'none'`
// to everything it serves. That header is right and the redirect binding needs
// no page at all, so there is nothing to weaken.
//
// The response comes back the other way, over HTTP-POST to the assertion
// consumer service, because an assertion is far too large for a URL. That
// direction needs no page from us either: the identity provider renders the
// form.

import { deflateRawSync } from 'node:zlib'
import { createSign, randomUUID } from 'node:crypto'
import { NS, all, atMostOne, attr, one, parseXml, text } from './xml.ts'

export interface AuthnRequestInput {
  /** Where the provider receives requests. */
  destination: string
  /** Our entity id. */
  issuer: string
  /** Where the assertion should be sent back. */
  acsUrl: string
  /** Round-tripped by the provider and matched on return. */
  relayState?: string | null
  issueInstant?: Date
  /** Ask the provider not to prompt again, or to prompt regardless. */
  forceAuthn?: boolean
  /**
   * Signs the request. Optional because most providers do not require it; when
   * one does, an unsigned request is refused with a message that does not say
   * why, which is a long afternoon.
   */
  signing?: { privateKey: string } | null
}

export interface AuthnRequest {
  /** The identifier to match against InResponseTo when the assertion arrives. */
  id: string
  /** The full URL to redirect the browser to. */
  redirectUrl: string
  xml: string
}

/** Builds an AuthnRequest and the redirect that carries it. */
export function buildAuthnRequest(input: AuthnRequestInput): AuthnRequest {
  // The identifier has to be an xsd:ID, which may not begin with a digit. A
  // bare UUID does about a third of the time, and the resulting failure is an
  // intermittent one that looks like the provider being flaky.
  const id = `_${randomUUID()}`
  const instant = (input.issueInstant ?? new Date()).toISOString().replace(/\.\d{3}Z$/, 'Z')

  const xml =
    `<samlp:AuthnRequest xmlns:samlp="${NS.samlp}" xmlns:saml="${NS.saml}" ` +
    `ID="${id}" Version="2.0" IssueInstant="${instant}" ` +
    `Destination="${escapeXml(input.destination)}" ` +
    `ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" ` +
    `AssertionConsumerServiceURL="${escapeXml(input.acsUrl)}"` +
    (input.forceAuthn ? ' ForceAuthn="true"' : '') +
    `><saml:Issuer>${escapeXml(input.issuer)}</saml:Issuer>` +
    `<samlp:NameIDPolicy Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress" AllowCreate="true"/>` +
    `</samlp:AuthnRequest>`

  // Raw deflate, not zlib. The binding says DEFLATE without a zlib header, and
  // a provider given the header rejects the request with a parse error that
  // names neither compression nor the header.
  const encoded = deflateRawSync(Buffer.from(xml, 'utf8')).toString('base64')

  const url = new URL(input.destination)
  url.searchParams.set('SAMLRequest', encoded)
  if (input.relayState) url.searchParams.set('RelayState', input.relayState)

  if (input.signing) {
    // The redirect binding signs the query string, not the XML: the parameters
    // in a fixed order, exactly as they will be sent. Building the string by
    // hand rather than reading it back from the URL, because the signature has
    // to cover the same encoding the provider will verify and a URL object may
    // normalise percent-encoding on the way out.
    const sigAlg = 'http://www.w3.org/2001/04/xmldsig-more#rsa-sha256'
    const parts = [`SAMLRequest=${encodeURIComponent(encoded)}`]
    if (input.relayState) parts.push(`RelayState=${encodeURIComponent(input.relayState)}`)
    parts.push(`SigAlg=${encodeURIComponent(sigAlg)}`)
    const signed = parts.join('&')

    const signer = createSign('RSA-SHA256')
    signer.update(signed)
    const signature = signer.sign(input.signing.privateKey, 'base64')

    return {
      id,
      xml,
      redirectUrl: `${url.origin}${url.pathname}?${signed}&Signature=${encodeURIComponent(signature)}`,
    }
  }

  return { id, xml, redirectUrl: url.toString() }
}

// ---------------------------------------------------------------------------
// Metadata
// ---------------------------------------------------------------------------

export interface ServiceProviderMetadata {
  entityId: string
  acsUrl: string
  /** Our own certificate, when one is configured, so a provider can verify our
   *  signed requests and encrypt assertions to us. */
  certificate?: string | null
  wantAssertionsSigned?: boolean
}

/** The document an administrator uploads to their identity provider. */
export function serviceProviderMetadata(sp: ServiceProviderMetadata): string {
  const keyDescriptor = sp.certificate
    ? `<md:KeyDescriptor use="signing"><ds:KeyInfo xmlns:ds="${NS.ds}"><ds:X509Data>` +
      `<ds:X509Certificate>${sp.certificate.replace(/-----(BEGIN|END) CERTIFICATE-----/g, '').replace(/\s+/g, '')}</ds:X509Certificate>` +
      `</ds:X509Data></ds:KeyInfo></md:KeyDescriptor>`
    : ''

  return (
    `<?xml version="1.0" encoding="UTF-8"?>` +
    `<md:EntityDescriptor xmlns:md="${NS.md}" entityID="${escapeXml(sp.entityId)}">` +
    `<md:SPSSODescriptor AuthnRequestsSigned="${sp.certificate ? 'true' : 'false'}" ` +
    // Always true. An unsigned assertion is refused by verify.ts whatever the
    // metadata says; stating it here is what stops a provider being configured
    // to send one and the failure being discovered by a person trying to log in.
    `WantAssertionsSigned="true" ` +
    `protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` +
    keyDescriptor +
    `<md:NameIDFormat>urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress</md:NameIDFormat>` +
    `<md:AssertionConsumerService ` +
    `Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" ` +
    `Location="${escapeXml(sp.acsUrl)}" index="0" isDefault="true"/>` +
    `</md:SPSSODescriptor></md:EntityDescriptor>`
  )
}

export interface IdentityProviderMetadata {
  entityId: string
  /** The HTTP-Redirect single sign-on endpoint. */
  ssoUrl: string
  /** Every signing certificate, in document order. Plural because a provider
   *  publishes two while rotating, and taking only the first turns their
   *  rotation into our outage. */
  certificates: string[]
}

export class MetadataRefused extends Error {}

/**
 * Reads what an administrator pasted in.
 *
 * This is not a security boundary: the metadata is configuration an
 * administrator supplies, not input from a stranger, and what it establishes is
 * which certificate to trust rather than whether to trust this document. It is
 * still parsed strictly, because the failure mode of being lenient here is a
 * connection that looks configured and refuses every login with a signature
 * error.
 */
export function parseIdentityProviderMetadata(xml: string): IdentityProviderMetadata {
  const doc = parseXml(xml)

  // A metadata document may describe several entities. Which one is meant is
  // then a guess, and a guess that picks the wrong certificate produces a
  // signature failure nobody can explain, so it is refused.
  const descriptors = all(doc, '//md:EntityDescriptor')
  const root = doc.documentElement!
  const rootIsDescriptor = root.localName === 'EntityDescriptor' && root.namespaceURI === NS.md
  const entity = rootIsDescriptor ? root : descriptors.length === 1 ? descriptors[0]! : null
  if (!entity) {
    throw new MetadataRefused(
      descriptors.length === 0
        ? 'This document is not SAML metadata: it has no EntityDescriptor.'
        : `This document describes ${descriptors.length} entities. Paste the metadata for one ` +
          `identity provider, so there is no question which certificate is being trusted.`,
    )
  }

  const entityId = attr(entity, 'entityID')
  if (!entityId) throw new MetadataRefused('The metadata states no entityID.')

  const idp = atMostOne(entity, './md:IDPSSODescriptor', 'an IDPSSODescriptor')
  if (!idp) {
    throw new MetadataRefused(
      'This metadata describes a service provider, not an identity provider. It is probably the ' +
        'document this product publishes, pasted back in.',
    )
  }

  const redirect = all(idp, './md:SingleSignOnService').find(
    (s) => attr(s, 'Binding') === 'urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect',
  )
  const ssoUrl = redirect ? attr(redirect, 'Location') : null
  if (!ssoUrl) {
    throw new MetadataRefused(
      'The metadata offers no HTTP-Redirect single sign-on endpoint, which is the binding used ' +
        'to send a browser to the provider.',
    )
  }
  if (!/^https:\/\//i.test(ssoUrl)) {
    throw new MetadataRefused(
      `The single sign-on endpoint is ${ssoUrl}. It must be https: a request over plain HTTP ` +
        `carries the relay state and the request identifier in clear.`,
    )
  }

  // Signing certificates only. A KeyDescriptor with use="encryption" is the
  // provider's key for receiving encrypted things, and trusting it to verify
  // signatures is a key-reuse mistake. A descriptor with no use attribute is
  // valid for both, which is what most providers publish.
  const certificates = all(idp, './md:KeyDescriptor')
    .filter((k) => {
      const use = attr(k, 'use')
      return use === null || use === 'signing'
    })
    .flatMap((k) => all(k, './/ds:X509Certificate'))
    .map((c) => (text(c) ?? '').replace(/\s+/g, ''))
    .filter((c) => c !== '')

  if (certificates.length === 0) {
    throw new MetadataRefused(
      'The metadata carries no signing certificate, so no assertion from this provider could ever ' +
        'be verified.',
    )
  }

  return { entityId, ssoUrl, certificates: [...new Set(certificates)] }
}

/** Where this service's SAML endpoints live for one connection. */
export function samlUrls(baseUrl: string, handle: string) {
  const base = baseUrl.endsWith('/') ? baseUrl.slice(0, -1) : baseUrl
  return {
    entityId: `${base}/sso/saml/${handle}/metadata`,
    metadataUrl: `${base}/sso/saml/${handle}/metadata`,
    acsUrl: `${base}/sso/saml/${handle}/acs`,
    loginUrl: `${base}/sso/saml/${handle}/login`,
  }
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}

export { one, text }
