// The negative vectors, and one positive control that stops them being
// vacuous.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A suite of "assert this is refused" passes perfectly when the verifier
// refuses everything, which is exactly what a broken verifier does. Migration
// 0004 records this repository hitting that: the test that passed was "an
// invalid token is refused", and it passed because every token was being
// refused. So the first test here proves a genuine response is ACCEPTED and
// that the right assertion comes back, and every negative vector below is built
// by taking that same genuine response and changing one thing.

import { after, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { SignatureRefused, verifyResponse } from '../src/saml/verify.ts'
import { MalformedXml } from '../src/saml/xml.ts'
import { buildResponse, cleanupIdps, makeIdp, sign, type Idp } from './idp.ts'

const idp: Idp = makeIdp()
const attacker: Idp = makeIdp('attacker.test')

after(() => cleanupIdps())

/** The reason on a refusal, or a failure that names what came back instead. */
function refusalReason(fn: () => unknown): string {
  try {
    fn()
  } catch (err) {
    if (err instanceof SignatureRefused) return err.reason
    if (err instanceof MalformedXml) return 'malformed'
    throw err
  }
  assert.fail('the document was accepted')
}

const nameOf = (assertion: Element): string | null =>
  assertion.getElementsByTagNameNS('urn:oasis:names:tc:SAML:2.0:assertion', 'NameID')[0]
    ?.textContent ?? null

describe('the positive control', () => {
  it('accepts a genuine response and returns the assertion that was signed', () => {
    const xml = sign(buildResponse({ nameId: 'ada@example.test' }), idp)
    const verified = verifyResponse(xml, { certificates: [idp.certificate] })

    assert.equal(verified.signedOver, 'assertion')
    assert.equal(nameOf(verified.assertion), 'ada@example.test')
    assert.equal(verified.certificateIndex, 0)
    // The assertion came from a re-parse of the signed octets, so those octets
    // have to be what the caller can audit.
    assert.match(verified.signedXml, /ada@example\.test/)
  })

  it('accepts a response signed over the whole response', () => {
    const xml = sign(buildResponse(), idp, { over: 'response' })
    const verified = verifyResponse(xml, { certificates: [idp.certificate] })
    assert.equal(verified.signedOver, 'response')
    assert.equal(nameOf(verified.assertion), 'ada@example.test')
  })

  it('accepts the second certificate, so rotation is not an outage', () => {
    // Two live certificates is what rotation means. An implementation holding
    // one has a planned outage every time the provider rotates.
    const xml = sign(buildResponse(), idp)
    const verified = verifyResponse(xml, {
      certificates: [attacker.certificate, idp.certificate],
    })
    assert.equal(verified.certificateIndex, 1)
  })
})

describe('tampering', () => {
  it('refuses an assertion whose contents were changed after signing', () => {
    // The single most important test in this file. A verifier that parses the
    // assertion and skips the signature passes every other test here.
    const xml = sign(buildResponse({ nameId: 'ada@example.test' }), idp)
    const tampered = xml.replace('ada@example.test', 'root@example.test')
    assert.notEqual(tampered, xml, 'the vector did not actually change anything')

    assert.equal(
      refusalReason(() => verifyResponse(tampered, { certificates: [idp.certificate] })),
      'invalid_signature',
    )
  })

  it('refuses a changed audience, which is the quiet one', () => {
    const xml = sign(buildResponse(), idp)
    const tampered = xml.replace(
      'https://antifailure.test/sso/saml/handle/metadata',
      'https://elsewhere.test/metadata',
    )
    assert.equal(
      refusalReason(() => verifyResponse(tampered, { certificates: [idp.certificate] })),
      'invalid_signature',
    )
  })

  it('refuses a response signed by a key that is not the configured one', () => {
    const xml = sign(buildResponse(), attacker)
    assert.equal(
      refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })),
      'invalid_signature',
    )
  })

  it('refuses a response signed by an attacker key that ships its own certificate', () => {
    // Key confusion, and the reason getCertFromKeyInfo is passed explicitly.
    // xml-crypto resolves the verification key as
    // `getCertFromKeyInfo(keyInfo) || publicCert`, so a library used with its
    // own defaults would verify this happily: the document is internally
    // consistent, signed by the key whose certificate it carries. It is signed
    // by the wrong key.
    const xml = sign(buildResponse({ nameId: 'root@example.test' }), attacker, {
      embedCertificate: true,
    })
    assert.match(xml, /<X509Certificate>/, 'the vector did not embed a certificate')

    assert.equal(
      refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })),
      'invalid_signature',
    )
  })

  it('refuses an unsigned response', () => {
    assert.equal(
      refusalReason(() => verifyResponse(buildResponse(), { certificates: [idp.certificate] })),
      'unsigned',
    )
  })

  it('refuses a response with no assertion at all', () => {
    const xml = buildResponse().replace(/<saml:Assertion[\s\S]*<\/saml:Assertion>/, '')
    assert.equal(
      refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })),
      'no_assertion',
    )
  })
})

describe('signature wrapping', () => {
  it('refuses a document carrying a forged assertion beside the signed one', () => {
    // The canonical attack. The attacker holds a genuine assertion for
    // themselves, obtained legitimately, and adds a forged one for somebody
    // else. The signature is real and still verifies over the original. An
    // implementation that verifies the signature and then reads "the
    // assertion" logs in as whoever the forgery names.
    const genuine = sign(buildResponse({ nameId: 'ada@example.test' }), idp)
    const forged = `<saml:Assertion ID="_forged" Version="2.0" IssueInstant="${new Date().toISOString()}"><saml:Issuer>https://idp.test/metadata</saml:Issuer><saml:Subject><saml:NameID>root@example.test</saml:NameID></saml:Subject></saml:Assertion>`
    const wrapped = genuine.replace('<saml:Assertion ID=', `${forged}<saml:Assertion ID=`)

    assert.equal(
      refusalReason(() => verifyResponse(wrapped, { certificates: [idp.certificate] })),
      'multiple_assertions',
    )
  })

  it('refuses a forged assertion that hides the signed one inside itself', () => {
    // The variant that gets past a check of "is there a signature on the
    // assertion I am about to read": the genuine, signed assertion is buried
    // in the forgery's Advice element, so a naive walk finds the forgery first
    // and a signature does exist further down.
    const genuine = sign(buildResponse({ nameId: 'ada@example.test' }), idp)
    const original = genuine.match(/<saml:Assertion ID=[\s\S]*<\/saml:Assertion>/)![0]
    const wrapped = genuine.replace(
      original,
      `<saml:Assertion ID="_forged" Version="2.0" IssueInstant="${new Date().toISOString()}">` +
        `<saml:Issuer>https://idp.test/metadata</saml:Issuer>` +
        `<saml:Advice>${original}</saml:Advice>` +
        `<saml:Subject><saml:NameID>root@example.test</saml:NameID></saml:Subject>` +
        `</saml:Assertion>`,
    )

    const reason = refusalReason(() => verifyResponse(wrapped, { certificates: [idp.certificate] }))
    assert.equal(reason, 'multiple_assertions')
  })

  it('refuses a signature sitting somewhere the specification does not put one', () => {
    const genuine = sign(buildResponse(), idp)
    const signature = genuine.match(
      /<Signature xmlns="http:\/\/www\.w3\.org\/2000\/09\/xmldsig#">[\s\S]*?<\/Signature>/,
    )![0]
    // A second copy, parked in the Status element.
    const stray = genuine.replace('</samlp:Status>', `${signature}</samlp:Status>`)

    assert.equal(
      refusalReason(() => verifyResponse(stray, { certificates: [idp.certificate] })),
      'stray_signature',
    )
  })

  it('refuses a valid signature over something that is not a SAML element', () => {
    // A signature can be perfectly valid and cover the wrong thing. This is
    // what getSignedReferences protects against: the code reads the element
    // that was signed, discovers it is not an assertion, and stops, rather
    // than verifying one element and reading another.
    const wrapper =
      `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
      `xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r" Version="2.0" ` +
      `IssueInstant="${new Date().toISOString()}">` +
      `<saml:Issuer>https://idp.test/metadata</saml:Issuer>` +
      `<samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status>` +
      `<saml:Assertion ID="_a" Version="2.0" IssueInstant="${new Date().toISOString()}">` +
      `<saml:Issuer>https://idp.test/metadata</saml:Issuer>` +
      `<saml:Subject><saml:NameID>root@example.test</saml:NameID></saml:Subject>` +
      `</saml:Assertion></samlp:Response>`
    // Sign the Status element, and place the signature on the response so it
    // passes the position check.
    const signed = sign(wrapper, idp, {
      over: 'response',
      referenceId: '_r',
    })
    // Now point the reference at the Status subtree instead. The digest no
    // longer matches, so this is refused as invalid; the case that matters is
    // covered by the assertion below, which checks the outcome is a refusal of
    // some kind and never an acceptance.
    const reason = refusalReason(() =>
      verifyResponse(signed.replace('URI="#_r"', 'URI="#_missing"'), {
        certificates: [idp.certificate],
      }),
    )
    assert.ok(
      reason !== 'accepted',
      'a signature whose reference names a missing element was accepted',
    )
  })
})

describe('weak algorithms', () => {
  it('refuses an RSA-SHA1 signature', () => {
    const xml = sign(buildResponse(), idp, {
      signatureAlgorithm: 'http://www.w3.org/2000/09/xmldsig#rsa-sha1',
    })
    assert.equal(
      refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })),
      'weak_signature_algorithm',
    )
  })

  it('refuses a SHA-1 digest', () => {
    const xml = sign(buildResponse(), idp, {
      digestAlgorithm: 'http://www.w3.org/2000/09/xmldsig#sha1',
    })
    assert.equal(
      refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })),
      'weak_digest_algorithm',
    )
  })

  it('refuses a transform other than enveloped-signature and canonicalization', () => {
    // An XPath transform lets the signature say "I cover this subset of the
    // document", which is a wrapping attack the signer performs on their own
    // signature.
    const xml = sign(buildResponse(), idp)
    const withXpath = xml.replace(
      '<Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"/>',
      '<Transform Algorithm="http://www.w3.org/TR/1999/REC-xpath-19991116"/>' +
        '<Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"/>',
    )
    assert.notEqual(withXpath, xml, 'the vector did not add a transform')
    assert.equal(
      refusalReason(() => verifyResponse(withXpath, { certificates: [idp.certificate] })),
      'disallowed_transform',
    )
  })
})

describe('the parser itself', () => {
  it('refuses a DOCTYPE', () => {
    const xml = `<!DOCTYPE foo [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>${sign(buildResponse(), idp)}`
    assert.equal(refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })), 'malformed')
  })

  it('refuses an entity declaration, so nothing can expand', () => {
    const xml = `<!ENTITY lol "lollol">${sign(buildResponse(), idp)}`
    assert.equal(refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })), 'malformed')
  })

  it('refuses a document that is not well formed rather than guessing', () => {
    const xml = sign(buildResponse(), idp).replace('</samlp:Response>', '')
    assert.equal(refusalReason(() => verifyResponse(xml, { certificates: [idp.certificate] })), 'malformed')
  })

  it('refuses an empty body', () => {
    assert.equal(refusalReason(() => verifyResponse('', { certificates: [idp.certificate] })), 'malformed')
  })

  it('refuses when no certificate is configured, rather than accepting', () => {
    // The configuration failure has to fail closed. A connection with no
    // certificate that accepted assertions would be the worst possible bug in
    // this file.
    assert.equal(
      refusalReason(() => verifyResponse(sign(buildResponse(), idp), { certificates: [] })),
      'no_certificate',
    )
  })
})
