// The signature check, and the reason it is written the way it is.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A SAML implementation that parses the assertion and skips the signature is
// worse than no single sign-on at all, because it looks like security and
// accepts anything. That is the failure everybody knows about. The one that
// actually happens to implementations that do check the signature is signature
// wrapping, and it is subtler: the attacker keeps a genuine, validly signed
// assertion they obtained legitimately, and adds a second forged one, arranged
// so the signature still verifies over the original while the application reads
// the forgery. Every part is real. The signature is valid. The application logs
// in as somebody else.
//
// The defence is one idea: READ WHAT WAS SIGNED, never sign what you read.
//
// xml-crypto validates the signature and then exposes getSignedReferences(),
// the canonicalized bytes of each reference it actually verified. Those bytes,
// and nothing else, are what this module hands on. Downstream code never sees
// the document the identity provider posted; it sees a fresh parse of the exact
// octets that were hashed. An attacker can add anything they like anywhere in
// the surrounding document and it is not in those octets, so it is not in what
// anybody reads. That makes wrapping structurally impossible rather than
// defended against case by case, which matters because the case-by-case
// defences have been bypassed repeatedly for fifteen years by moving the extra
// element somewhere the checks did not look.
//
// Four more rules, each closing something that has been used in the wild:
//
// The key comes from configuration, never from the document. KeyInfo carries a
// certificate and it is the attacker's certificate if they put it there.
// xml-crypto will use it if you let it: signed-xml.js resolves the key as
// `getCertFromKeyInfo(keyInfo) || publicCert`, so getCertFromKeyInfo is passed
// explicitly here as a function that returns null. There is a test that signs a
// response with an unrelated key, embeds that key's certificate in KeyInfo, and
// requires the result to be refused.
//
// HMAC is refused. If the signature algorithm may be an HMAC, an attacker who
// can guess or supply the key can forge a signature; key confusion between a
// public key and a shared secret is a whole CVE class. xml-crypto ships with
// HMAC disabled unless enableHMAC() is called, and this never calls it and
// checks the algorithm anyway.
//
// SHA-1 is refused, for digests and for signatures. It has been collidable
// since 2017.
//
// Transforms are limited to enveloped-signature and canonicalization. The
// transform list is a small programming language, and XPath transforms in
// particular let the signer say "the signature covers this subset of the
// document", which is a wrapping attack the signer performs on themselves.

import { SignedXml } from 'xml-crypto'
import { NS, all, atMostOne, certificateToPem, one, parseXml } from './xml.ts'

/** A response that was refused, with a reason fit to write to an audit log. */
export class SignatureRefused extends Error {
  readonly reason: string

  constructor(reason: string, message: string) {
    super(message)
    this.name = 'SignatureRefused'
    this.reason = reason
  }
}

const ALLOWED_SIGNATURE_ALGORITHMS = new Set([
  'http://www.w3.org/2001/04/xmldsig-more#rsa-sha256',
  'http://www.w3.org/2001/04/xmldsig-more#rsa-sha384',
  'http://www.w3.org/2001/04/xmldsig-more#rsa-sha512',
  'http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256',
  'http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha384',
  'http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha512',
  'http://www.w3.org/2007/05/xmldsig-more#sha256-rsa-MGF1',
])

const ALLOWED_DIGEST_ALGORITHMS = new Set([
  'http://www.w3.org/2001/04/xmlenc#sha256',
  'http://www.w3.org/2001/04/xmldsig-more#sha384',
  'http://www.w3.org/2001/04/xmlenc#sha512',
])

const ALLOWED_TRANSFORMS = new Set([
  'http://www.w3.org/2000/09/xmldsig#enveloped-signature',
  'http://www.w3.org/2001/10/xml-exc-c14n#',
  'http://www.w3.org/2001/10/xml-exc-c14n#WithComments',
  'http://www.w3.org/TR/2001/REC-xml-c14n-20010315',
  'http://www.w3.org/TR/2001/REC-xml-c14n-20010315#WithComments',
])

export interface VerifiedAssertion {
  /**
   * The assertion, re-parsed from the exact canonical bytes the signature
   * covered. Nothing outside these bytes influenced it.
   */
  assertion: Element
  /** The canonical XML those bytes are, kept for the audit record. */
  signedXml: string
  /** Where the signature sat: over the whole response, or over the assertion. */
  signedOver: 'response' | 'assertion'
  /** The certificate that verified it, so certificate rotation can be reported. */
  certificateIndex: number
}

export interface VerifyOptions {
  /**
   * The identity provider's signing certificates, base64 DER as they appear in
   * metadata. More than one because rotation means two are live at once, and an
   * implementation that holds a single certificate has a planned outage every
   * time the provider rotates.
   */
  certificates: readonly string[]
}

/**
 * Verifies a SAML response and returns the assertion that was actually signed.
 *
 * The return value is the only thing a caller may read. Deliberately, this
 * never returns the parsed response document.
 */
export function verifyResponse(xml: string, options: VerifyOptions): VerifiedAssertion {
  if (options.certificates.length === 0) {
    throw new SignatureRefused(
      'no_certificate',
      'This connection has no identity provider certificate, so no assertion can be verified.',
    )
  }

  const doc = parseXml(xml)

  // Before anything else: exactly one assertion in the document, counted
  // anywhere at any depth. A wrapped document has two, and refusing that shape
  // outright is cheap. It is not the defence; the defence is reading only the
  // signed bytes. It is the second lock.
  const assertions = all(doc, '//saml:Assertion')
  const encrypted = all(doc, '//saml:EncryptedAssertion')
  if (assertions.length + encrypted.length === 0) {
    throw new SignatureRefused('no_assertion', 'The response carries no assertion.')
  }
  if (assertions.length + encrypted.length > 1) {
    throw new SignatureRefused(
      'multiple_assertions',
      `The response carries ${assertions.length + encrypted.length} assertions. A response with ` +
        `more than one is refused: that shape is how a signature over one is made to look like a ` +
        `signature over another.`,
    )
  }

  // Candidate signatures, and only in the two places the specification puts
  // one. A Signature somewhere else is either meaningless or is the extra
  // element of a wrapping attempt.
  const candidates = [
    ...all(doc, '/samlp:Response/ds:Signature'),
    ...all(doc, '/samlp:Response/saml:Assertion/ds:Signature'),
  ]
  if (candidates.length === 0) {
    throw new SignatureRefused(
      'unsigned',
      'The response is not signed. An unsigned assertion is refused, whatever it says.',
    )
  }

  // Every signature in the document has to be one of the candidates. A valid
  // signature in an unexpected position means the document is not the shape
  // this code reasons about, and reasoning about it anyway is how the subtle
  // bugs get in.
  const everySignature = all(doc, '//ds:Signature')
  if (everySignature.length !== candidates.length) {
    throw new SignatureRefused(
      'stray_signature',
      'The response carries a signature somewhere other than on the response or the assertion.',
    )
  }

  let lastFailure: SignatureRefused | null = null
  for (const signature of candidates) {
    checkAlgorithms(signature)
    for (const [index, certificate] of options.certificates.entries()) {
      try {
        return checkOne(xml, doc, signature, certificate, index)
      } catch (err) {
        if (err instanceof SignatureRefused) {
          lastFailure = err
          continue
        }
        throw err
      }
    }
  }

  throw (
    lastFailure ??
    new SignatureRefused('invalid_signature', 'The signature on this response is not valid.')
  )
}

function checkOne(
  xml: string,
  doc: Document,
  signature: Element,
  certificate: string,
  certificateIndex: number,
): VerifiedAssertion {
  const verifier = new SignedXml({
    publicCert: certificateToPem(certificate),
    // The whole point. Without this, xml-crypto resolves the key as
    // `getCertFromKeyInfo(keyInfo) || publicCert` and a certificate the
    // attacker put in the document wins over the one an administrator
    // configured. Returning null here means the configured certificate is the
    // only key that can ever verify anything.
    getCertFromKeyInfo: () => null,
  })

  verifier.loadSignature(signature as unknown as Node)

  let valid = false
  try {
    valid = verifier.checkSignature(xml)
  } catch (err) {
    throw new SignatureRefused('invalid_signature', `The signature is not valid: ${(err as Error).message}`)
  }
  if (!valid) {
    throw new SignatureRefused('invalid_signature', 'The signature on this response is not valid.')
  }

  // Everything below here runs only after the signature verified, and reads
  // only what the signature covered.
  const signedFragments = verifier.getSignedReferences()
  if (signedFragments.length !== 1) {
    throw new SignatureRefused(
      'reference_count',
      `The signature covers ${signedFragments.length} references. Exactly one is expected, and a ` +
        `signature over several is refused rather than guessed about.`,
    )
  }
  const signedXml = signedFragments[0]!

  // A fresh parse of the canonical bytes that were hashed. This is the line
  // that makes wrapping structurally impossible: nothing outside these octets
  // can reach anything below.
  const signedDoc = parseXml(signedXml)
  const root = signedDoc.documentElement!
  const rootName = root.localName ?? root.nodeName

  let assertion: Element
  let signedOver: 'response' | 'assertion'

  if (rootName === 'Response' && root.namespaceURI === NS.samlp) {
    signedOver = 'response'
    assertion = one(signedDoc, '/samlp:Response/saml:Assertion', 'an assertion inside the signed response')
  } else if (rootName === 'Assertion' && root.namespaceURI === NS.saml) {
    signedOver = 'assertion'
    assertion = root
  } else {
    throw new SignatureRefused(
      'signed_wrong_element',
      `The signature covers a <${rootName}>, which is neither the response nor an assertion. ` +
        `A valid signature over the wrong element is exactly what a wrapping attack produces.`,
    )
  }

  // Belt and braces. The assertion that was signed must be the one assertion
  // the document contains, matched by identifier. If the two ever disagreed,
  // the code above would still be safe because it reads the signed copy, and
  // the disagreement itself would mean something is wrong.
  const inDocument = atMostOne(doc, '//saml:Assertion', 'an assertion')
  const signedId = assertion.getAttribute('ID')
  if (inDocument && signedId && inDocument.getAttribute('ID') !== signedId) {
    throw new SignatureRefused(
      'assertion_substituted',
      'The signed assertion is not the assertion the response carries.',
    )
  }

  return { assertion, signedXml, signedOver, certificateIndex }
}

function checkAlgorithms(signature: Element): void {
  const method = atMostOne(
    signature,
    './ds:SignedInfo/ds:SignatureMethod',
    'a signature method',
  )
  const algorithm = method?.getAttribute('Algorithm') ?? ''
  if (!ALLOWED_SIGNATURE_ALGORITHMS.has(algorithm)) {
    throw new SignatureRefused(
      'weak_signature_algorithm',
      `The signature uses ${algorithm || 'no stated algorithm'}. Only RSA and ECDSA with SHA-256 ` +
        `or better are accepted: SHA-1 has been collidable since 2017, and an HMAC would let ` +
        `anybody holding the shared secret forge an assertion.`,
    )
  }

  const references = all(signature, './ds:SignedInfo/ds:Reference')
  if (references.length !== 1) {
    throw new SignatureRefused(
      'reference_count',
      `The signature declares ${references.length} references; exactly one is expected.`,
    )
  }

  const digest = atMostOne(references[0]!, './ds:DigestMethod', 'a digest method')
  const digestAlgorithm = digest?.getAttribute('Algorithm') ?? ''
  if (!ALLOWED_DIGEST_ALGORITHMS.has(digestAlgorithm)) {
    throw new SignatureRefused(
      'weak_digest_algorithm',
      `The signature digests with ${digestAlgorithm || 'no stated algorithm'}. SHA-256 or better is required.`,
    )
  }

  for (const transform of all(references[0]!, './ds:Transforms/ds:Transform')) {
    const name = transform.getAttribute('Algorithm') ?? ''
    if (!ALLOWED_TRANSFORMS.has(name)) {
      throw new SignatureRefused(
        'disallowed_transform',
        `The signature applies the transform ${name || '(unnamed)'}. Only enveloped-signature and ` +
          `canonicalization are accepted, because a transform such as XPath lets the signature ` +
          `cover a chosen subset of the document rather than the whole of it.`,
      )
    }
  }
}
