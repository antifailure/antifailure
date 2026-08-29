// What an assertion has to say before anybody is signed in.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The signature is checked in verify.ts and everything here runs afterwards, on
// the re-parsed bytes that were signed. A valid signature says the identity
// provider wrote this; it says nothing at all about whether the assertion was
// meant for us, is still in date, or has been presented before. Those are three
// separate holes and each has been the whole of a real breach:
//
//   Audience. A signed assertion the same provider issued for a DIFFERENT
//   service provider is a valid signature over somebody else's login. Without
//   an audience check, any customer of any application that shares an identity
//   provider with us can replay their own assertion here and become the
//   corresponding user in our organization.
//
//   Validity window. An assertion is a bearer credential. Without NotBefore and
//   NotOnOrAfter enforcement, one captured from a log two years ago still works.
//
//   Replay. Inside its own window an assertion is valid more than once unless
//   something remembers it. That memory is a UNIQUE constraint rather than a
//   read-then-write, because a read-then-write lets two racing requests both
//   find it absent, which is exactly the moment an attacker replaying is
//   aiming for.
//
// Clock skew applies to every time comparison, in both directions, and the
// default is the five minutes the specification suggests. Without it, a
// provider whose clock is forty seconds ahead issues assertions that are not
// yet valid, every login fails, and the error blames the user.

import { NS, all, atMostOne, attr, one, text } from './xml.ts'

export class AssertionRefused extends Error {
  readonly reason: string

  constructor(reason: string, message: string) {
    super(message)
    this.name = 'AssertionRefused'
    this.reason = reason
  }
}

export interface AssertionExpectations {
  /** Our entity id, which must appear in the audience restriction. */
  audience: string
  /** Our assertion consumer service URL, matched against Recipient. */
  recipient: string
  /** The issuer this connection is configured for. */
  issuer: string
  /**
   * The AuthnRequest this is answering, when the login started here. Null for
   * a provider-initiated login, which legitimately answers no request.
   */
  inResponseTo: string | null
  clockSkewSeconds: number
  now: Date
}

export interface AssertionFacts {
  /** The identifier the replay cache remembers. */
  id: string
  /** Lowercased. Providers disagree about case and Ada@Example.com must not
   *  become a second account. */
  email: string
  nameId: string
  nameIdFormat: string | null
  displayName: string | null
  givenName: string | null
  familyName: string | null
  /** Group claim values, for the group-to-role mapping. */
  groups: string[]
  /** When this assertion stops being replayable, for the cache. */
  notOnOrAfter: Date
  /** What the provider says about how long the session may last, when it says
   *  anything. A provider that states a session lifetime is stating policy, and
   *  ignoring it means somebody removed from the directory keeps a live session
   *  here for as long as our own timeout allows. */
  sessionNotOnOrAfter: Date | null
  sessionIndex: string | null
}

// The claim names four providers use for one idea. Entra ID sends the long
// WS-Federation URIs, Okta and Google send short names, and a
// standards-compliant provider sends whatever its administrator typed. Written
// out rather than guessed at, in preference order.
const EMAIL_CLAIMS = [
  'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress',
  'urn:oid:0.9.2342.19200300.100.1.3',
  'email',
  'emailAddress',
  'mail',
  'Email',
]
const GIVEN_NAME_CLAIMS = [
  'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname',
  'urn:oid:2.5.4.42',
  'firstName',
  'given_name',
  'givenName',
]
const FAMILY_NAME_CLAIMS = [
  'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname',
  'urn:oid:2.5.4.4',
  'lastName',
  'family_name',
  'surname',
]
const DISPLAY_NAME_CLAIMS = [
  'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name',
  'urn:oid:2.16.840.1.113730.3.1.241',
  'displayName',
  'name',
]
const GROUP_CLAIMS = [
  'http://schemas.microsoft.com/ws/2008/06/identity/claims/groups',
  'http://schemas.xmlsoap.org/claims/Group',
  'groups',
  'Groups',
  'memberOf',
]

/**
 * Reads the status of a response, for the error message only.
 *
 * This is the one thing here that looks at the unsigned document, and it is
 * used solely to say something useful when the provider reported a failure and
 * so sent no assertion. Nothing is authorised on the strength of it. When there
 * IS an assertion, the assertion is what decides, and the status is ignored:
 * an attacker who edits Success into a failure achieves a worse error message.
 */
export function statusMessage(doc: Document): string | null {
  const code = atMostOne(doc, '/samlp:Response/samlp:Status/samlp:StatusCode', 'a status code')
  const value = code ? attr(code, 'Value') : null
  if (!value || value === 'urn:oasis:names:tc:SAML:2.0:status:Success') return null
  const detail = atMostOne(
    doc,
    '/samlp:Response/samlp:Status/samlp:StatusMessage',
    'a status message',
  )
  const short = value.split(':').pop() ?? value
  return text(detail) ? `${short}: ${text(detail)}` : short
}

/** Validates an assertion and returns what it says. */
export function readAssertion(
  assertion: Element,
  expect: AssertionExpectations,
): AssertionFacts {
  const skew = expect.clockSkewSeconds * 1000
  const now = expect.now.getTime()

  const id = attr(assertion, 'ID')
  if (!id) {
    throw new AssertionRefused(
      'no_assertion_id',
      'The assertion has no ID, so it cannot be remembered and a replay could not be detected.',
    )
  }

  const issuer = text(one(assertion, './saml:Issuer', 'an issuer on the assertion'))
  if (issuer !== expect.issuer) {
    throw new AssertionRefused(
      'wrong_issuer',
      `This assertion was issued by ${issuer ?? 'nobody'} and this connection expects ` +
        `${expect.issuer}.`,
    )
  }

  // ---- the validity window -------------------------------------------------
  const conditions = one(assertion, './saml:Conditions', 'a conditions element')
  const notBefore = parseInstant(attr(conditions, 'NotBefore'), 'Conditions/@NotBefore')
  const notOnOrAfter = parseInstant(attr(conditions, 'NotOnOrAfter'), 'Conditions/@NotOnOrAfter')

  if (!notOnOrAfter) {
    // An assertion with no expiry never expires, so it is a permanent
    // credential. Refused rather than given one of our own choosing.
    throw new AssertionRefused(
      'no_expiry',
      'The assertion states no NotOnOrAfter, so it would never expire.',
    )
  }
  if (notBefore && now + skew < notBefore.getTime()) {
    throw new AssertionRefused(
      'not_yet_valid',
      `This assertion is not valid until ${notBefore.toISOString()} and it is now ` +
        `${expect.now.toISOString()}. A tolerance of ${expect.clockSkewSeconds} seconds was already ` +
        `applied, so the identity provider's clock is further ahead than that: check both clocks.`,
    )
  }
  if (now - skew >= notOnOrAfter.getTime()) {
    throw new AssertionRefused(
      'expired',
      `This assertion expired at ${notOnOrAfter.toISOString()} and it is now ` +
        `${expect.now.toISOString()}. Start the sign-in again.`,
    )
  }

  // ---- who it was for ------------------------------------------------------
  const audiences = all(conditions, './saml:AudienceRestriction/saml:Audience').map((a) =>
    (a.textContent ?? '').trim(),
  )
  if (audiences.length === 0) {
    throw new AssertionRefused(
      'no_audience',
      'The assertion states no audience, so there is nothing to say it was meant for this service.',
    )
  }
  if (!audiences.includes(expect.audience)) {
    throw new AssertionRefused(
      'wrong_audience',
      `This assertion was issued for ${audiences.join(', ')} and this service is ` +
        `${expect.audience}. An assertion the same provider issued for a different service is a ` +
        `valid signature over somebody else's login, which is why this is refused.`,
    )
  }

  // ---- the subject, and the confirmation that it is a bearer credential ----
  const subject = one(assertion, './saml:Subject', 'a subject')
  const nameIdElement = one(subject, './saml:NameID', 'a NameID')
  const nameId = text(nameIdElement)
  if (!nameId) throw new AssertionRefused('no_name_id', 'The assertion names no subject.')

  const confirmations = all(subject, './saml:SubjectConfirmation').filter(
    (c) => attr(c, 'Method') === 'urn:oasis:names:tc:SAML:2.0:cm:bearer',
  )
  if (confirmations.length === 0) {
    throw new AssertionRefused(
      'no_bearer_confirmation',
      'The assertion carries no bearer subject confirmation, which is the only method supported.',
    )
  }

  // At least one confirmation has to be good. Providers send several and the
  // specification says one sufficing is enough, so the loop collects the last
  // reason rather than failing on the first.
  let confirmationFailure: AssertionRefused | null = null
  const confirmed = confirmations.some((confirmation) => {
    const data = atMostOne(confirmation, './saml:SubjectConfirmationData', 'confirmation data')
    if (!data) {
      confirmationFailure = new AssertionRefused(
        'no_confirmation_data',
        'The bearer confirmation carries no data, so its recipient and expiry cannot be checked.',
      )
      return false
    }

    const dataExpiry = parseInstant(attr(data, 'NotOnOrAfter'), 'SubjectConfirmationData/@NotOnOrAfter')
    if (!dataExpiry || now - skew >= dataExpiry.getTime()) {
      confirmationFailure = new AssertionRefused('expired', 'The bearer confirmation has expired.')
      return false
    }

    const recipient = attr(data, 'Recipient')
    if (recipient && recipient !== expect.recipient) {
      // Recipient is what stops an assertion meant for one endpoint being
      // posted to another. It is checked as an exact string because a
      // "starts with" or "same host" comparison is how a check like this
      // quietly stops meaning anything.
      confirmationFailure = new AssertionRefused(
        'wrong_recipient',
        `This assertion was addressed to ${recipient} and arrived at ${expect.recipient}.`,
      )
      return false
    }

    const answered = attr(data, 'InResponseTo')
    if (expect.inResponseTo === null) {
      // Provider-initiated. An assertion that answers a request cannot be one,
      // and accepting it here would let somebody take a response captured from
      // a real login and present it as an unsolicited one.
      if (answered) {
        confirmationFailure = new AssertionRefused(
          'unexpected_in_response_to',
          `This assertion answers request ${answered}, and no request was made from here. ` +
            `A response to a request nobody made is refused.`,
        )
        return false
      }
    } else if (answered !== expect.inResponseTo) {
      confirmationFailure = new AssertionRefused(
        'wrong_in_response_to',
        `This assertion answers request ${answered ?? '(none)'} and this browser started ` +
          `${expect.inResponseTo}.`,
      )
      return false
    }
    return true
  })

  if (!confirmed) {
    throw (
      confirmationFailure ??
      new AssertionRefused('no_bearer_confirmation', 'No bearer confirmation was acceptable.')
    )
  }

  // ---- what it says about the person --------------------------------------
  const attributes = collectAttributes(assertion)
  const emailClaim = firstOf(attributes, EMAIL_CLAIMS)
  const nameIdIsEmail =
    attr(nameIdElement, 'Format') === 'urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress' ||
    nameId.includes('@')
  const email = (emailClaim ?? (nameIdIsEmail ? nameId : null))?.toLowerCase() ?? null

  if (!email || !email.includes('@')) {
    throw new AssertionRefused(
      'no_email',
      'The assertion carries no email address, in the NameID or in any claim this understands. ' +
        'Map an email claim in the identity provider: it is what links a person to their account here.',
    )
  }

  const authn = atMostOne(assertion, './saml:AuthnStatement', 'an authentication statement')
  const sessionNotOnOrAfter = authn
    ? parseInstant(attr(authn, 'SessionNotOnOrAfter'), 'AuthnStatement/@SessionNotOnOrAfter')
    : null

  return {
    id,
    email,
    nameId,
    nameIdFormat: attr(nameIdElement, 'Format'),
    displayName: firstOf(attributes, DISPLAY_NAME_CLAIMS),
    givenName: firstOf(attributes, GIVEN_NAME_CLAIMS),
    familyName: firstOf(attributes, FAMILY_NAME_CLAIMS),
    groups: allOf(attributes, GROUP_CLAIMS),
    notOnOrAfter,
    sessionNotOnOrAfter,
    sessionIndex: authn ? attr(authn, 'SessionIndex') : null,
  }
}

type Attributes = Map<string, string[]>

function collectAttributes(assertion: Element): Attributes {
  const found: Attributes = new Map()
  for (const attribute of all(assertion, './saml:AttributeStatement/saml:Attribute')) {
    const name = attr(attribute, 'Name') ?? attr(attribute, 'FriendlyName')
    if (!name) continue
    const values = all(attribute, './saml:AttributeValue')
      .map((v) => (v.textContent ?? '').trim())
      .filter((v) => v !== '')
    if (values.length === 0) continue
    // Providers occasionally send the same claim twice. Merging rather than
    // replacing keeps both, which matters for group claims where each Attribute
    // may carry one group.
    found.set(name, [...(found.get(name) ?? []), ...values])
    const friendly = attr(attribute, 'FriendlyName')
    if (friendly && friendly !== name) {
      found.set(friendly, [...(found.get(friendly) ?? []), ...values])
    }
  }
  return found
}

function firstOf(attributes: Attributes, names: readonly string[]): string | null {
  for (const name of names) {
    const values = attributes.get(name)
    if (values && values[0]) return values[0]
  }
  return null
}

function allOf(attributes: Attributes, names: readonly string[]): string[] {
  for (const name of names) {
    const values = attributes.get(name)
    if (values && values.length) return values
  }
  return []
}

/**
 * A SAML instant, which is xsd:dateTime and must be UTC.
 *
 * Date.parse accepts a great deal that is not xsd:dateTime and returns
 * something plausible for most of it, so the shape is checked first. A
 * timestamp misread by an hour is a validity window misread by an hour, in
 * whichever direction is worse.
 */
function parseInstant(value: string | null, what: string): Date | null {
  if (!value) return null
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(value)) {
    throw new AssertionRefused('bad_timestamp', `${what} is not a valid timestamp: ${value}`)
  }
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    throw new AssertionRefused('bad_timestamp', `${what} is not a valid timestamp: ${value}`)
  }
  return parsed
}

export { NS }
