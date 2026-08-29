// Verifying an id_token, and the two mistakes that make it worthless.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// An id_token is a JWT, and a JWT is a credential whose own header tells you
// how to check it. That design has produced two vulnerabilities so often that
// they have names, and both are defended here by refusing to read the header
// as an instruction.
//
//   alg: none. The header says the token is unsigned, the library sees no
//   signature to check, and returns success. Every claim in it is then whatever
//   the attacker typed.
//
//   Algorithm confusion. The header says HS256. The verifier reaches for "the
//   key" and finds the provider's RSA PUBLIC key, which is published at a URL
//   anybody can fetch, and uses it as an HMAC secret. Anybody who can read the
//   JWKS can then mint tokens.
//
// The defence for both is the same and it is not a special case for each: the
// algorithm comes from the ALLOW-LIST, checked against the key we already hold,
// and the header is only ever consulted to select among algorithms we were
// already willing to accept. No HMAC algorithm is in the list at all, so there
// is no code path in which a public key is used as a shared secret.
//
// The claims are checked afterwards, and the one that is skipped most often is
// the nonce. Without it, an id_token obtained by the attacker for their own
// account at the same provider can be injected into somebody else's login flow.

import { createHash, createPublicKey, timingSafeEqual, verify as verifySignature } from 'node:crypto'
import type { KeyObject } from 'node:crypto'

export class TokenRefused extends Error {
  readonly reason: string

  constructor(reason: string, message: string) {
    super(message)
    this.name = 'TokenRefused'
    this.reason = reason
  }
}

/**
 * Signature algorithms this accepts, and what each needs from node's verifier.
 *
 * No HS256 and no none, deliberately and permanently. A provider that only
 * offers HMAC-signed id_tokens is a provider whose tokens are only as good as a
 * shared secret in our database, and supporting that would mean the two attacks
 * above have a code path to reach.
 */
const ALGORITHMS: Record<
  string,
  { hash: string; keyType: string; padding?: number; dsaEncoding?: 'ieee-p1363' }
> = {
  RS256: { hash: 'sha256', keyType: 'rsa' },
  RS384: { hash: 'sha384', keyType: 'rsa' },
  RS512: { hash: 'sha512', keyType: 'rsa' },
  // RSASSA-PSS. constants.RSA_PKCS1_PSS_PADDING is 6 and the salt length
  // matching the digest is 'RSA_PSS_SALTLEN_DIGEST', which is -1.
  PS256: { hash: 'sha256', keyType: 'rsa', padding: 6 },
  PS384: { hash: 'sha384', keyType: 'rsa', padding: 6 },
  PS512: { hash: 'sha512', keyType: 'rsa', padding: 6 },
  // ECDSA in JWS is the raw r||s pair, not the DER sequence node produces by
  // default. Getting this wrong makes every ES256 token fail to verify, which
  // reads as "the provider is broken".
  ES256: { hash: 'sha256', keyType: 'ec', dsaEncoding: 'ieee-p1363' },
  ES384: { hash: 'sha384', keyType: 'ec', dsaEncoding: 'ieee-p1363' },
  ES512: { hash: 'sha512', keyType: 'ec', dsaEncoding: 'ieee-p1363' },
}

export interface Jwk {
  kty: string
  kid?: string
  use?: string
  alg?: string
  n?: string
  e?: string
  crv?: string
  x?: string
  y?: string
}

export interface JwtClaims {
  iss?: string
  sub?: string
  aud?: string | string[]
  exp?: number
  iat?: number
  nbf?: number
  nonce?: string
  azp?: string
  at_hash?: string
  email?: string
  email_verified?: boolean
  name?: string
  given_name?: string
  family_name?: string
  groups?: string[] | string
  roles?: string[] | string
  [claim: string]: unknown
}

export interface VerifyTokenOptions {
  keys: readonly Jwk[]
  issuer: string
  clientId: string
  /** The nonce this login sent. Required: a login without one cannot tell its
   *  own token from one obtained elsewhere. */
  nonce: string
  clockSkewSeconds: number
  now: Date
  /** The access token, when at_hash should be checked against it. */
  accessToken?: string | null
}

/** Verifies an id_token and returns its claims. */
export function verifyIdToken(token: string, options: VerifyTokenOptions): JwtClaims {
  const parts = token.split('.')
  if (parts.length !== 3) {
    throw new TokenRefused('malformed', 'The id_token is not a JWT.')
  }
  const [headerPart, payloadPart, signaturePart] = parts as [string, string, string]

  const header = decodeJson(headerPart, 'header') as { alg?: string; kid?: string; typ?: string }
  const algorithm = header.alg ?? ''

  // Before the key is even looked at. `none` never reaches a code path that
  // could return success, and neither does any HMAC.
  const spec = ALGORITHMS[algorithm]
  if (!spec) {
    throw new TokenRefused(
      'bad_algorithm',
      `The id_token is signed with ${algorithm || 'no stated algorithm'}, which is not accepted. ` +
        `Only RSA and ECDSA signatures with SHA-256 or better are: "none" would make the token ` +
        `unsigned, and an HMAC would let anybody holding the provider's published public key ` +
        `mint one.`,
    )
  }

  const signature = base64UrlDecode(signaturePart, 'signature')
  const signedInput = Buffer.from(`${headerPart}.${payloadPart}`, 'ascii')

  // Candidate keys. A kid narrows it to one, which is the normal case and what
  // makes rotation cheap. Without a kid every key of the right type is tried,
  // which is correct and slower, and is what a small provider sends.
  const candidates = options.keys.filter((key) => {
    if (key.use && key.use !== 'sig') return false
    if (header.kid && key.kid) return key.kid === header.kid
    return true
  })
  if (candidates.length === 0) {
    throw new TokenRefused(
      'no_key',
      header.kid
        ? `The provider's key set has no signing key with kid ${header.kid}. It may have rotated ` +
          `since this was last fetched.`
        : `The provider's key set has no signing key.`,
    )
  }

  const verified = candidates.some((jwk) => {
    let key: KeyObject
    try {
      key = createPublicKey({ key: jwk as never, format: 'jwk' })
    } catch {
      return false
    }
    // The key's own type has to match the algorithm the header named. This is
    // the second half of the confusion defence: even inside the allow-list, an
    // RSA key may not be used to check something claiming to be ECDSA.
    if (key.asymmetricKeyType !== spec.keyType) return false
    try {
      return verifySignature(
        spec.hash,
        signedInput,
        {
          key,
          ...(spec.padding ? { padding: spec.padding, saltLength: -1 } : {}),
          ...(spec.dsaEncoding ? { dsaEncoding: spec.dsaEncoding } : {}),
        },
        signature,
      )
    } catch {
      return false
    }
  })

  if (!verified) {
    throw new TokenRefused('invalid_signature', 'The signature on the id_token is not valid.')
  }

  // Everything below runs only after the signature verified.
  const claims = decodeJson(payloadPart, 'payload') as JwtClaims
  const now = Math.floor(options.now.getTime() / 1000)
  const skew = options.clockSkewSeconds

  if (claims.iss !== options.issuer) {
    throw new TokenRefused(
      'wrong_issuer',
      `The id_token was issued by ${claims.iss ?? 'nobody'} and this connection expects ` +
        `${options.issuer}.`,
    )
  }

  const audiences = Array.isArray(claims.aud) ? claims.aud : claims.aud ? [claims.aud] : []
  if (!audiences.includes(options.clientId)) {
    throw new TokenRefused(
      'wrong_audience',
      `The id_token was issued for ${audiences.join(', ') || 'nobody'} and this connection is ` +
        `${options.clientId}. A token the same provider issued for a different application is a ` +
        `valid token for somebody else's login.`,
    )
  }
  if (audiences.length > 1 && claims.azp !== options.clientId) {
    // With several audiences the specification requires azp, and it is what
    // says which of them the token is actually for.
    throw new TokenRefused(
      'wrong_authorized_party',
      'The id_token names several audiences and its authorized party is not this application.',
    )
  }

  if (typeof claims.exp !== 'number') {
    throw new TokenRefused('no_expiry', 'The id_token states no expiry, so it would never expire.')
  }
  if (now - skew >= claims.exp) {
    throw new TokenRefused('expired', 'The id_token has expired. Start the sign-in again.')
  }
  if (typeof claims.iat === 'number' && claims.iat - skew > now) {
    throw new TokenRefused(
      'not_yet_valid',
      `The id_token was issued at ${new Date(claims.iat * 1000).toISOString()}, which is in the ` +
        `future by more than the ${skew} second tolerance. Check both clocks.`,
    )
  }
  if (typeof claims.nbf === 'number' && claims.nbf - skew > now) {
    throw new TokenRefused('not_yet_valid', 'The id_token is not valid yet.')
  }

  // The nonce, compared in constant time because it is a secret this login
  // generated and a comparison that returns early leaks it a character at a
  // time to anybody who can measure.
  if (!claims.nonce || !constantTimeEquals(claims.nonce, options.nonce)) {
    throw new TokenRefused(
      'wrong_nonce',
      'The id_token does not carry the nonce this sign-in sent. Without that, a token the holder ' +
        'obtained for their own account at the same provider could be injected into this login.',
    )
  }

  if (options.accessToken && typeof claims.at_hash === 'string') {
    // Binds the id_token to the access token that came with it, so the two
    // cannot be mixed from different responses.
    const digest = createHash(spec.hash).update(options.accessToken, 'ascii').digest()
    const expected = digest.subarray(0, digest.length / 2).toString('base64url')
    if (!constantTimeEquals(expected, claims.at_hash)) {
      throw new TokenRefused(
        'wrong_at_hash',
        'The id_token does not match the access token it arrived with.',
      )
    }
  }

  return claims
}

function decodeJson(part: string, what: string): unknown {
  let parsed: unknown
  try {
    parsed = JSON.parse(base64UrlDecode(part, what).toString('utf8'))
  } catch {
    throw new TokenRefused('malformed', `The id_token ${what} is not JSON.`)
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new TokenRefused('malformed', `The id_token ${what} is not an object.`)
  }
  return parsed
}

function base64UrlDecode(value: string, what: string): Buffer {
  if (!/^[A-Za-z0-9_-]*$/.test(value)) {
    throw new TokenRefused('malformed', `The id_token ${what} is not base64url.`)
  }
  return Buffer.from(value, 'base64url')
}

function constantTimeEquals(a: string, b: string): boolean {
  const left = Buffer.from(a, 'utf8')
  const right = Buffer.from(b, 'utf8')
  // Length first, because timingSafeEqual throws on a mismatch and the length
  // of a nonce is not a secret.
  if (left.length !== right.length) return false
  return timingSafeEqual(left, right)
}
