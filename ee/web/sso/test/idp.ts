// A real identity provider's signing key, made at run time.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Nothing here is committed. Keys and certificates are generated when the suite
// runs, into a temporary directory that is removed afterwards, because a
// private key in a test fixture is a private key in the repository, in every
// clone of it, and in every container image built from it. The rule is absolute
// and it has no "but it is only a test key" exception: a scanner cannot tell,
// an auditor will not accept the distinction, and the repository has a gate
// (tools/scanrepo) that is right to fail on one.
//
// The signing is done with xml-crypto, the same library the verifier uses. That
// is a deliberate choice and it has a real cost: a bug in the library that
// affects both signing and verifying would be invisible to these tests. It is
// still the right trade, because the property under test is not "xml-crypto
// computes RSA correctly", it is "this code refuses documents it should refuse",
// and every negative vector below is constructed by taking a genuinely valid
// signature and then doing something to the document. The positive control at
// the top of the suite is what stops the whole file passing vacuously.

import { randomUUID } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { SignedXml } from 'xml-crypto'

export interface Idp {
  privateKey: string
  /** Base64 DER, as it appears in identity provider metadata. */
  certificate: string
  certificatePem: string
}

const made: string[] = []

/** An RSA key and a self-signed certificate for it. */
export function makeIdp(commonName = 'idp.test'): Idp {
  const dir = mkdtempSync(path.join(tmpdir(), 'af-sso-test-'))
  made.push(dir)
  const keyPath = path.join(dir, 'key.pem')
  const certPath = path.join(dir, 'cert.pem')

  execFileSync(
    'openssl',
    [
      'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
      '-keyout', keyPath, '-out', certPath,
      '-days', '1', '-subj', `/CN=${commonName}`,
      '-sha256',
    ],
    { stdio: 'pipe' },
  )

  const certificatePem = readFileSync(certPath, 'utf8')
  return {
    privateKey: readFileSync(keyPath, 'utf8'),
    certificatePem,
    certificate: certificatePem
      .replace(/-----(BEGIN|END) CERTIFICATE-----/g, '')
      .replace(/\s+/g, ''),
  }
}

/** Removes every temporary directory this module made. */
export function cleanupIdps(): void {
  for (const dir of made.splice(0)) rmSync(dir, { recursive: true, force: true })
}

export interface ResponseShape {
  issuer?: string
  audience?: string
  destination?: string
  inResponseTo?: string | null
  assertionId?: string
  responseId?: string
  nameId?: string
  notBefore?: Date
  notOnOrAfter?: Date
  issueInstant?: Date
  sessionNotOnOrAfter?: Date | null
  attributes?: Record<string, string[]>
  statusCode?: string
}

const ISO = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, 'Z')

/** An unsigned but otherwise ordinary response, as a string. */
export function buildResponse(shape: ResponseShape = {}): string {
  const now = shape.issueInstant ?? new Date()
  const notBefore = shape.notBefore ?? new Date(now.getTime() - 60_000)
  const notOnOrAfter = shape.notOnOrAfter ?? new Date(now.getTime() + 5 * 60_000)
  // Unique by default. A fixed identifier means the second response any suite
  // builds is a replay of the first, and the replay cache refuses it with a
  // message about replay that reads like a bug in whatever was being tested.
  // A test that wants a deterministic id passes one.
  const unique = randomUUID()
  const assertionId = shape.assertionId ?? `_a-${unique}`
  const responseId = shape.responseId ?? `_r-${unique}`
  const issuer = shape.issuer ?? 'https://idp.test/metadata'
  const audience = shape.audience ?? 'https://antifailure.test/sso/saml/handle/metadata'
  const destination = shape.destination ?? 'https://antifailure.test/sso/saml/handle/acs'
  const nameId = shape.nameId ?? 'ada@example.test'
  const status = shape.statusCode ?? 'urn:oasis:names:tc:SAML:2.0:status:Success'
  const inResponseTo =
    shape.inResponseTo === null ? '' : ` InResponseTo="${shape.inResponseTo ?? '_request-1'}"`
  const sessionExpiry =
    shape.sessionNotOnOrAfter === null
      ? ''
      : ` SessionNotOnOrAfter="${ISO(shape.sessionNotOnOrAfter ?? new Date(now.getTime() + 8 * 3600_000))}"`

  const attributes = Object.entries(shape.attributes ?? {})
  const attributeStatement = attributes.length
    ? `<saml:AttributeStatement>${attributes
        .map(
          ([name, values]) =>
            `<saml:Attribute Name="${name}">${values
              .map((v) => `<saml:AttributeValue>${v}</saml:AttributeValue>`)
              .join('')}</saml:Attribute>`,
        )
        .join('')}</saml:AttributeStatement>`
    : ''

  return `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="${responseId}"${inResponseTo} Version="2.0" IssueInstant="${ISO(now)}" Destination="${destination}"><saml:Issuer>${issuer}</saml:Issuer><samlp:Status><samlp:StatusCode Value="${status}"/></samlp:Status><saml:Assertion ID="${assertionId}" Version="2.0" IssueInstant="${ISO(now)}"><saml:Issuer>${issuer}</saml:Issuer><saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">${nameId}</saml:NameID><saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData${inResponseTo} NotOnOrAfter="${ISO(notOnOrAfter)}" Recipient="${destination}"/></saml:SubjectConfirmation></saml:Subject><saml:Conditions NotBefore="${ISO(notBefore)}" NotOnOrAfter="${ISO(notOnOrAfter)}"><saml:AudienceRestriction><saml:Audience>${audience}</saml:Audience></saml:AudienceRestriction></saml:Conditions><saml:AuthnStatement AuthnInstant="${ISO(now)}"${sessionExpiry} SessionIndex="_session-1"><saml:AuthnContext><saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>${attributeStatement}</saml:Assertion></samlp:Response>`
}

export interface SignOptions {
  /** Which element the signature covers. */
  over?: 'assertion' | 'response'
  signatureAlgorithm?: string
  digestAlgorithm?: string
  /** Put the signer's own certificate in KeyInfo, as a real provider does and
   *  as an attacker also does. */
  embedCertificate?: boolean
  transforms?: string[]
  /** Reference a different element than the one being signed. */
  referenceId?: string
}

const EXC_C14N = 'http://www.w3.org/2001/10/xml-exc-c14n#'
const ENVELOPED = 'http://www.w3.org/2000/09/xmldsig#enveloped-signature'

/** Signs a response the way a provider would. */
export function sign(xml: string, idp: Idp, options: SignOptions = {}): string {
  const over = options.over ?? 'assertion'
  const signer = new SignedXml({
    privateKey: idp.privateKey,
    publicCert: options.embedCertificate ? idp.certificatePem : undefined,
    signatureAlgorithm:
      (options.signatureAlgorithm as never) ??
      ('http://www.w3.org/2001/04/xmldsig-more#rsa-sha256' as never),
    canonicalizationAlgorithm: EXC_C14N as never,
    getKeyInfoContent: options.embedCertificate ? SignedXml.getKeyInfoContent : (() => null),
  })

  const xpath =
    over === 'assertion'
      ? "//*[local-name(.)='Assertion']"
      : "/*[local-name(.)='Response']"
  const id =
    options.referenceId ??
    (over === 'assertion'
      ? (xml.match(/<saml:Assertion ID="([^"]+)"/)?.[1] ?? '')
      : (xml.match(/<samlp:Response[^>]*\bID="([^"]+)"/)?.[1] ?? ''))

  signer.addReference({
    xpath,
    transforms: (options.transforms as never) ?? ([ENVELOPED, EXC_C14N] as never),
    digestAlgorithm:
      (options.digestAlgorithm as never) ?? ('http://www.w3.org/2001/04/xmlenc#sha256' as never),
    uri: `#${id}`,
  })

  signer.computeSignature(xml, {
    location: {
      reference: over === 'assertion' ? xpath : "/*[local-name(.)='Response']",
      action: 'append',
    },
  })

  let signed = signer.getSignedXml()
  // xml-crypto appends the signature as the last child. A real provider puts
  // it immediately after Issuer, and where it sits changes canonicalization, so
  // the tests should not all depend on one placement. This moves it for the
  // assertion case, which is the common shape from Okta and Entra ID.
  if (over === 'assertion') {
    const match = signed.match(/<Signature xmlns="http:\/\/www\.w3\.org\/2000\/09\/xmldsig#">[\s\S]*?<\/Signature>/)
    if (match) {
      const block = match[0]
      signed = signed.replace(block, '')
      signed = signed.replace(
        /(<saml:Assertion[^>]*>)(<saml:Issuer>[^<]*<\/saml:Issuer>)/,
        `$1$2${block}`,
      )
    }
  }
  return signed
}
