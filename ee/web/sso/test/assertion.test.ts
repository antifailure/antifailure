// What an assertion has to say, and every way it can fail to say it.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Same discipline as the signature suite: a positive control first, so that a
// readAssertion that threw on everything could not pass this file.
//
// The InResponseTo cases are written out as four orderings rather than one
// happy path, because that is where this goes wrong. A login started here and a
// login started at the provider are two different flows that arrive at the same
// endpoint, and the check that is right for one is a hole in the other: an
// implementation that ignores InResponseTo when it is unexpected will accept a
// response captured from a real service-provider-initiated login and replayed
// as an unsolicited one.

import { after, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { AssertionRefused, readAssertion, statusMessage } from '../src/saml/response.ts'
import { verifyResponse } from '../src/saml/verify.ts'
import { parseXml } from '../src/saml/xml.ts'
import { buildResponse, cleanupIdps, makeIdp, sign, type ResponseShape } from './idp.ts'

const idp = makeIdp()
after(() => cleanupIdps())

const AUDIENCE = 'https://antifailure.test/sso/saml/handle/metadata'
const RECIPIENT = 'https://antifailure.test/sso/saml/handle/acs'
const ISSUER = 'https://idp.test/metadata'
const NOW = new Date('2026-06-01T12:00:00Z')

function expectations(over: Partial<Parameters<typeof readAssertion>[1]> = {}) {
  return {
    audience: AUDIENCE,
    recipient: RECIPIENT,
    issuer: ISSUER,
    inResponseTo: '_request-1' as string | null,
    clockSkewSeconds: 300,
    now: NOW,
    ...over,
  }
}

/** A signed, verified assertion built from one shape. */
function assertionFrom(shape: ResponseShape = {}): Element {
  const xml = sign(buildResponse({ issueInstant: NOW, ...shape }), idp)
  return verifyResponse(xml, { certificates: [idp.certificate] }).assertion
}

function reasonFor(shape: ResponseShape, over: Parameters<typeof expectations>[0] = {}): string {
  try {
    readAssertion(assertionFrom(shape), expectations(over))
  } catch (err) {
    if (err instanceof AssertionRefused) return err.reason
    throw err
  }
  assert.fail('the assertion was accepted')
}

describe('the positive control', () => {
  it('accepts an ordinary assertion and reads what it says', () => {
    const facts = readAssertion(
      assertionFrom({
        nameId: 'ada@example.test',
        // Named explicitly, because the fixture now generates a unique id by
        // default and this case is asserting that the id is read back.
        assertionId: '_assertion-1',
        attributes: {
          'http://schemas.microsoft.com/ws/2008/06/identity/claims/groups': ['Engineering', 'Owners'],
          'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname': ['Ada'],
          'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname': ['Lovelace'],
        },
      }),
      expectations(),
    )

    assert.equal(facts.email, 'ada@example.test')
    assert.equal(facts.givenName, 'Ada')
    assert.equal(facts.familyName, 'Lovelace')
    assert.deepEqual(facts.groups, ['Engineering', 'Owners'])
    assert.equal(facts.id, '_assertion-1')
    assert.ok(facts.notOnOrAfter > NOW)
    assert.ok(facts.sessionNotOnOrAfter, 'the session lifetime the provider stated was dropped')
  })

  it('accepts an unsolicited assertion when none was requested', () => {
    const facts = readAssertion(
      assertionFrom({ inResponseTo: null }),
      expectations({ inResponseTo: null }),
    )
    assert.equal(facts.email, 'ada@example.test')
  })
})

describe('the validity window', () => {
  it('refuses an assertion that has expired', () => {
    assert.equal(
      reasonFor({ notOnOrAfter: new Date(NOW.getTime() - 10 * 60_000) }),
      'expired',
    )
  })

  it('refuses an assertion that is not valid yet', () => {
    assert.equal(reasonFor({ notBefore: new Date(NOW.getTime() + 10 * 60_000) }), 'not_yet_valid')
  })

  it('tolerates a provider clock that is a little ahead', () => {
    // Four minutes ahead, inside the five-minute tolerance. Without skew this
    // is a login that fails and an error that blames the user.
    const facts = readAssertion(
      assertionFrom({ notBefore: new Date(NOW.getTime() + 4 * 60_000) }),
      expectations(),
    )
    assert.equal(facts.email, 'ada@example.test')
  })

  it('tolerates a provider clock that is a little behind', () => {
    const facts = readAssertion(
      assertionFrom({ notOnOrAfter: new Date(NOW.getTime() - 4 * 60_000) }),
      expectations(),
    )
    assert.equal(facts.email, 'ada@example.test')
  })

  it('stops tolerating past the configured bound', () => {
    // The same assertion the previous case accepted, with the tolerance turned
    // down. If this passed, the skew setting would be decoration.
    assert.equal(
      reasonFor(
        { notOnOrAfter: new Date(NOW.getTime() - 4 * 60_000) },
        { clockSkewSeconds: 60 },
      ),
      'expired',
    )
  })

  it('refuses an assertion with no expiry, rather than inventing one', () => {
    const xml = sign(buildResponse({ issueInstant: NOW }), idp).replace(
      /(<saml:Conditions[^>]*?) NotOnOrAfter="[^"]*"/,
      '$1',
    )
    // Tampering breaks the signature, so this one is built and verified as a
    // document in its own right rather than through assertionFrom.
    const rebuilt = sign(
      buildResponse({ issueInstant: NOW }).replace(
        /(<saml:Conditions[^>]*?) NotOnOrAfter="[^"]*"/,
        '$1',
      ),
      idp,
    )
    assert.notEqual(rebuilt, xml)
    const assertionElement = verifyResponse(rebuilt, { certificates: [idp.certificate] }).assertion
    assert.throws(
      () => readAssertion(assertionElement, expectations()),
      (err: unknown) => err instanceof AssertionRefused && err.reason === 'no_expiry',
    )
  })

  it('refuses a timestamp that is not a timestamp', () => {
    const rebuilt = sign(
      buildResponse({ issueInstant: NOW }).replace(
        /(<saml:Conditions[^>]*?)NotOnOrAfter="[^"]*"/,
        '$1NotOnOrAfter="next tuesday"',
      ),
      idp,
    )
    const assertionElement = verifyResponse(rebuilt, { certificates: [idp.certificate] }).assertion
    assert.throws(
      () => readAssertion(assertionElement, expectations()),
      (err: unknown) => err instanceof AssertionRefused && err.reason === 'bad_timestamp',
    )
  })
})

describe('who the assertion was for', () => {
  it("refuses an assertion issued for somebody else's service", () => {
    // The quiet one. Every signature is valid; the assertion was simply issued
    // for a different service provider that shares this identity provider.
    assert.equal(reasonFor({ audience: 'https://someone-else.test/metadata' }), 'wrong_audience')
  })

  it('refuses an assertion addressed to a different endpoint', () => {
    assert.equal(
      reasonFor({ destination: 'https://antifailure.test/sso/saml/other-handle/acs' }),
      'wrong_recipient',
    )
  })

  it('refuses an assertion from a different issuer', () => {
    assert.equal(reasonFor({ issuer: 'https://other-idp.test/metadata' }), 'wrong_issuer')
  })
})

describe('InResponseTo, in all four orderings', () => {
  it('a request was made and the response answers it', () => {
    const facts = readAssertion(
      assertionFrom({ inResponseTo: '_request-1' }),
      expectations({ inResponseTo: '_request-1' }),
    )
    assert.equal(facts.email, 'ada@example.test')
  })

  it('a request was made and the response answers a different one', () => {
    assert.equal(
      reasonFor({ inResponseTo: '_someone-elses-request' }, { inResponseTo: '_request-1' }),
      'wrong_in_response_to',
    )
  })

  it('a request was made and the response answers none', () => {
    assert.equal(
      reasonFor({ inResponseTo: null }, { inResponseTo: '_request-1' }),
      'wrong_in_response_to',
    )
  })

  it('no request was made and the response answers one', () => {
    // The ordering an implementation skips. Somebody captures a response from
    // a real login started here and presents it at the provider-initiated
    // endpoint, where there is no request to compare against. Accepting it
    // because "unsolicited responses have no InResponseTo" is the hole.
    assert.equal(
      reasonFor({ inResponseTo: '_request-1' }, { inResponseTo: null }),
      'unexpected_in_response_to',
    )
  })
})

describe('what the assertion says about the person', () => {
  it('takes the email from a claim when there is one', () => {
    const facts = readAssertion(
      assertionFrom({
        nameId: 'ADA-LOVELACE-0001',
        attributes: {
          'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress': ['Ada@Example.Test'],
        },
      }),
      expectations(),
    )
    // Lowercased. Providers disagree about case and Ada@Example.com must not
    // become a second account beside ada@example.com.
    assert.equal(facts.email, 'ada@example.test')
    assert.equal(facts.nameId, 'ADA-LOVELACE-0001')
  })

  it('falls back to an email-shaped NameID', () => {
    const facts = readAssertion(assertionFrom({ nameId: 'Grace@Example.Test' }), expectations())
    assert.equal(facts.email, 'grace@example.test')
  })

  it('refuses an assertion with no email anywhere', () => {
    assert.equal(reasonFor({ nameId: 'ADA-LOVELACE-0001' }), 'no_email')
  })

  it('reads groups sent as one attribute with several values', () => {
    const facts = readAssertion(
      assertionFrom({ attributes: { groups: ['Engineering', 'Owners'] } }),
      expectations(),
    )
    assert.deepEqual(facts.groups, ['Engineering', 'Owners'])
  })
})

describe('the status of a failed response', () => {
  it('reports what the provider said when there is no assertion', () => {
    const doc = parseXml(
      buildResponse({ statusCode: 'urn:oasis:names:tc:SAML:2.0:status:Requester' }),
    )
    assert.equal(statusMessage(doc), 'Requester')
  })

  it('says nothing about a successful response', () => {
    assert.equal(statusMessage(parseXml(buildResponse())), null)
  })
})
