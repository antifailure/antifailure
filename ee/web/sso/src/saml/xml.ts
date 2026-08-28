// Parsing XML that somebody else wrote, on purpose, to be dangerous.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A SAML response is attacker-controlled input that arrives at an
// unauthenticated endpoint, and the parser is the first thing it reaches. Two
// classes of problem live here and neither is about signatures.
//
// A DOCTYPE lets a document define entities. The classic result is XXE, where
// an entity resolves to file:///etc/passwd, and the other is a billion laughs,
// where nested entities expand to gigabytes. @xmldom/xmldom does not resolve
// external entities, which removes the first, and this refuses a DOCTYPE
// outright anyway, which removes both and costs nothing: no identity provider
// on earth sends one.
//
// Leniency is the second. xmldom's default behaviour on malformed input is to
// report a warning and carry on with a best guess, which means two parsers can
// disagree about what a document says. That disagreement is the whole of the
// signature-wrapping family of attacks: sign what one parser sees, read what
// another parser sees. So every warning and error here is fatal.

import { DOMParser } from '@xmldom/xmldom'
import xpath from 'xpath'

export const NS = {
  samlp: 'urn:oasis:names:tc:SAML:2.0:protocol',
  saml: 'urn:oasis:names:tc:SAML:2.0:assertion',
  ds: 'http://www.w3.org/2000/09/xmldsig#',
  xenc: 'http://www.w3.org/2001/04/xmlenc#',
  md: 'urn:oasis:names:tc:SAML:2.0:metadata',
} as const

/** Refused at the door, before any signature was considered. */
export class MalformedXml extends Error {}

const select = xpath.useNamespaces({ ...NS })

export function parseXml(xml: string): Document {
  if (typeof xml !== 'string' || xml.trim() === '') {
    throw new MalformedXml('The document is empty.')
  }
  // A leading byte order mark is stripped, and ONLY a leading byte order mark.
  //
  // Microsoft serves its federation metadata with one: the bytes EF BB BF sit
  // in front of the XML declaration. That is legal, every specification says a
  // parser should tolerate it, and xmldom reports it as "an xml declaration
  // which is only at the start of the document" because as far as it is
  // concerned the declaration is now at position 1. Since this parser makes
  // every warning fatal on purpose, the product refused Entra ID's real
  // metadata outright and told the administrator their document was malformed.
  //
  // Found by feeding this parser a document Microsoft wrote rather than one
  // written here, which is the entire reason for testing against a real
  // provider: no fixture written by the author of a parser has a byte order
  // mark in it.
  //
  // The fatal-warning rule is NOT relaxed. This removes one specific,
  // specified, leading code point and changes nothing else, because a parser
  // that recovers from malformed input is a parser with an opinion about what
  // the document meant.
  if (xml.charCodeAt(0) === 0xfeff) xml = xml.slice(1)
  // Cheap and absolute. A DOCTYPE has no legitimate use in a SAML message and
  // every use of one here is an attack.
  if (/<!DOCTYPE/i.test(xml)) {
    throw new MalformedXml('The document declares a DOCTYPE, which is refused.')
  }
  if (/<!ENTITY/i.test(xml)) {
    throw new MalformedXml('The document declares an entity, which is refused.')
  }

  const problems: string[] = []
  const parser = new DOMParser({
    // Every level is collected and any of them is fatal. A parser that
    // recovers from malformed input is a parser that has an opinion about
    // what the document meant, and a second parser will have a different one.
    onError: (level, message) => {
      problems.push(`${level}: ${message}`)
    },
  })

  let doc: Document
  try {
    doc = parser.parseFromString(xml, 'text/xml') as unknown as Document
  } catch (err) {
    throw new MalformedXml(`The document is not well formed: ${(err as Error).message}`)
  }
  if (problems.length > 0) {
    throw new MalformedXml(`The document is not well formed: ${problems[0]}`)
  }
  if (!doc?.documentElement) {
    throw new MalformedXml('The document has no root element.')
  }
  return doc
}

/** Every element matching an XPath, namespace-aware. */
export function all(node: Node, expression: string): Element[] {
  return select(expression, node as never) as unknown as Element[]
}

/**
 * Exactly one element, or a refusal.
 *
 * There is no "the first one" here, and that is deliberate. Taking the first
 * match when there are two is how a document with an extra Assertion in it gets
 * read differently by the verifier and by the consumer, which is the whole
 * mechanism of signature wrapping. If there are two, something is wrong and the
 * only safe answer is to stop.
 */
export function one(node: Node, expression: string, what: string): Element {
  const found = all(node, expression)
  if (found.length === 0) throw new MalformedXml(`The document has no ${what}.`)
  if (found.length > 1) {
    throw new MalformedXml(
      `The document has ${found.length} of ${what}, and a document with more than one is refused. ` +
        `That shape is how a signature over one element is made to appear to cover another.`,
    )
  }
  return found[0]!
}

/** One element or none, refusing two. */
export function atMostOne(node: Node, expression: string, what: string): Element | null {
  const found = all(node, expression)
  if (found.length === 0) return null
  if (found.length > 1) {
    throw new MalformedXml(`The document has ${found.length} of ${what}; at most one is allowed.`)
  }
  return found[0]!
}

export function attr(element: Element, name: string): string | null {
  const value = element.getAttribute(name)
  return value === null || value === '' ? null : value
}

export function text(element: Element | null): string | null {
  if (!element) return null
  const value = element.textContent
  return value === null ? null : value.trim() || null
}

/** Base64 with the whitespace identity providers insert, and nothing else. */
export function decodeBase64(value: string, what: string): Buffer {
  const compact = value.replace(/\s+/g, '')
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(compact)) {
    throw new MalformedXml(`The ${what} is not base64.`)
  }
  const buffer = Buffer.from(compact, 'base64')
  // Node's decoder ignores trailing rubbish rather than failing, so a value
  // that does not round-trip was not really base64 and is refused here.
  if (buffer.toString('base64').replace(/=+$/, '') !== compact.replace(/=+$/, '')) {
    throw new MalformedXml(`The ${what} is not base64.`)
  }
  return buffer
}

/** A bare base64 DER certificate as it appears in metadata, wrapped as PEM. */
export function certificateToPem(certificate: string): string {
  const body = certificate.replace(/-----(BEGIN|END) CERTIFICATE-----/g, '').replace(/\s+/g, '')
  if (body === '') throw new MalformedXml('The certificate is empty.')
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(body)) {
    throw new MalformedXml('The certificate is not base64 DER.')
  }
  const lines = body.match(/.{1,64}/g) ?? []
  return `-----BEGIN CERTIFICATE-----\n${lines.join('\n')}\n-----END CERTIFICATE-----\n`
}
