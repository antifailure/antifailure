// Encrypting the two secrets a connection holds.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// An OIDC client secret and the service provider's private key are the only
// values in this feature that are worth stealing from the database. Row-level
// security keeps one tenant from reading another's, and it does nothing at all
// about a leaked backup, a replica somebody forgot, or a support engineer with
// a read-only connection. So they are encrypted before they are written, under
// a key the database never holds.
//
// AES-256-GCM, because the alternative worth having is authenticated
// encryption and this is the one Node ships. CBC would decrypt a tampered
// ciphertext into rubbish and hand it to the token endpoint, which is a
// distinguishable failure and therefore an oracle.
//
// The organization id is authenticated as additional data. That is not
// theatre: without it, a ciphertext is a portable blob, and anybody who can
// write a row can copy another tenant's encrypted client secret into their own
// connection and have the server decrypt it for them. Binding the ciphertext to
// the tenant it was written for makes that copy fail to decrypt.
//
// The format carries a version byte so a key can be rotated without a
// migration: a reader that meets a version it does not know says so, rather
// than decrypting with the wrong key and producing rubbish.

import { createCipheriv, createDecipheriv, randomBytes, timingSafeEqual } from 'node:crypto'

export class SecretUnavailable extends Error {}

const VERSION = 1
const IV_BYTES = 12
const TAG_BYTES = 16

/** The key, from the environment, checked once and loudly. */
export function keyFromEnv(env: NodeJS.ProcessEnv = process.env): Buffer {
  const value = env.AF_EE_SSO_KEY
  if (!value) {
    throw new SecretUnavailable(
      'AF_EE_SSO_KEY is not set. Single sign-on needs it to encrypt the client secret and the ' +
        'service provider key it stores. Generate one with: openssl rand -base64 32',
    )
  }
  const key = Buffer.from(value, 'base64')
  if (key.length !== 32) {
    throw new SecretUnavailable(
      `AF_EE_SSO_KEY decodes to ${key.length} bytes and 32 are required. Generate one with: ` +
        `openssl rand -base64 32`,
    )
  }
  // A key of all one byte is the shape of a placeholder somebody meant to
  // replace, and it is worth refusing rather than protecting nothing quietly.
  if (timingSafeEqual(key, Buffer.alloc(32, key[0]!))) {
    throw new SecretUnavailable('AF_EE_SSO_KEY is a single repeated byte, which is not a key.')
  }
  return key
}

/** version || iv || tag || ciphertext */
export function seal(plaintext: string, key: Buffer, orgId: string): Buffer {
  const iv = randomBytes(IV_BYTES)
  const cipher = createCipheriv('aes-256-gcm', key, iv)
  cipher.setAAD(Buffer.from(orgId, 'utf8'))
  const body = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()])
  return Buffer.concat([Buffer.from([VERSION]), iv, cipher.getAuthTag(), body])
}

export function open(sealed: Buffer, key: Buffer, orgId: string): string {
  if (sealed.length < 1 + IV_BYTES + TAG_BYTES) {
    throw new SecretUnavailable('The stored secret is truncated.')
  }
  const version = sealed[0]
  if (version !== VERSION) {
    throw new SecretUnavailable(
      `The stored secret is version ${version} and this build understands version ${VERSION}. ` +
        `It was written by a newer release, or by a different key format.`,
    )
  }
  const iv = sealed.subarray(1, 1 + IV_BYTES)
  const tag = sealed.subarray(1 + IV_BYTES, 1 + IV_BYTES + TAG_BYTES)
  const body = sealed.subarray(1 + IV_BYTES + TAG_BYTES)

  const decipher = createDecipheriv('aes-256-gcm', key, iv)
  decipher.setAAD(Buffer.from(orgId, 'utf8'))
  decipher.setAuthTag(tag)
  try {
    return Buffer.concat([decipher.update(body), decipher.final()]).toString('utf8')
  } catch {
    // One message for a wrong key, a tampered ciphertext, and a ciphertext
    // copied from another tenant. Telling them apart tells somebody probing
    // which of those they achieved.
    throw new SecretUnavailable(
      'The stored secret could not be decrypted. Either AF_EE_SSO_KEY has changed, or the row was ' +
        'written for a different organization.',
    )
  }
}
