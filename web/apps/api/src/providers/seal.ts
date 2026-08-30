// Sealing a customer's provider key.
//
// The threat model is not an attacker with a database dump. It is US: the key
// belongs to somebody else, they are paying for what it spends, and the only
// code that should ever hold the plaintext is the code handing it to the
// provider. Everything else -- a console page, an event, a log line, a support
// bundle, a screenshot -- must be structurally unable to obtain it.
//
// So the column is ciphertext under AES-256-GCM, with the sealing key supplied
// by the environment (from Key Vault in the hosted deployment) and never stored
// in Postgres. A database dump on its own decrypts nothing.
//
// WHAT GCM BUYS THAT CBC WOULD NOT. The tag authenticates the ciphertext, so a
// row somebody edited fails to open rather than decrypting to a different key
// that then gets sent to Anthropic. Associated data binds each ciphertext to
// the organization and provider it was sealed for, which means a row copied
// from one tenant's table to another's does not open either. That is the attack
// this shape exists to stop, and it is the reason the AAD is not optional.

import { createCipheriv, createDecipheriv, createHash, randomBytes, timingSafeEqual } from 'node:crypto'

/** The current sealing key version. Stored per row so a rotation can find the
 *  rows that still need re-sealing. */
export const KEY_VERSION = 'v1'

const ALGORITHM = 'aes-256-gcm'
const NONCE_BYTES = 12
const TAG_BYTES = 16

export class SealError extends Error {}

export interface Sealed {
  /** The ciphertext with the GCM tag appended, which is how the tag is stored
   *  without a third column that could be separated from what it authenticates. */
  ciphertext: Buffer
  nonce: Buffer
  keyVersion: string
  /** SHA-256 of the plaintext, truncated. Lets a rotation prove the new key is
   *  actually different from the old one without either being displayed. */
  fingerprint: string
  /** The last four characters, which is what every provider's own console shows
   *  and is how somebody confirms they rotated the key they meant to. */
  last4: string
}

/**
 * Reads the sealing key from the environment.
 *
 * Refuses anything that is not exactly 32 bytes. A short key is not "weaker
 * encryption", it is a different failure: Node would throw deep inside the
 * cipher on first use, which is at the moment somebody saves a key rather than
 * at start-up, so an installation would look healthy and break on the one
 * action this feature exists for.
 */
export function sealingKeyFrom(value: string | undefined): Buffer | null {
  if (!value) return null
  let key: Buffer
  try {
    key = Buffer.from(value, 'base64')
  } catch {
    throw new SealError('AF_PROVIDER_KEY_SECRET is not valid base64.')
  }
  if (key.length !== 32) {
    throw new SealError(
      `AF_PROVIDER_KEY_SECRET must be 32 bytes of base64 (got ${key.length}). ` +
        'Generate one with: openssl rand -base64 32',
    )
  }
  return key
}

/** What a ciphertext is bound to. A row that moves between these does not open. */
function associatedData(orgId: string, provider: string): Buffer {
  return Buffer.from(`${orgId}:${provider}:${KEY_VERSION}`, 'utf8')
}

export function seal(
  sealingKey: Buffer,
  plaintext: string,
  bound: { orgId: string; provider: string },
): Sealed {
  if (!plaintext) throw new SealError('There is no key to store.')
  const nonce = randomBytes(NONCE_BYTES)
  const cipher = createCipheriv(ALGORITHM, sealingKey, nonce)
  cipher.setAAD(associatedData(bound.orgId, bound.provider))
  const body = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()])
  const tag = cipher.getAuthTag()

  return {
    ciphertext: Buffer.concat([body, tag]),
    nonce,
    keyVersion: KEY_VERSION,
    fingerprint: fingerprintOf(plaintext),
    last4: plaintext.slice(-4),
  }
}

export function open(
  sealingKey: Buffer,
  sealed: { ciphertext: Buffer; nonce: Buffer },
  bound: { orgId: string; provider: string },
): string {
  if (sealed.ciphertext.length <= TAG_BYTES) {
    throw new SealError('The stored key is too short to be a sealed value.')
  }
  const body = sealed.ciphertext.subarray(0, sealed.ciphertext.length - TAG_BYTES)
  const tag = sealed.ciphertext.subarray(sealed.ciphertext.length - TAG_BYTES)

  const decipher = createDecipheriv(ALGORITHM, sealingKey, sealed.nonce)
  decipher.setAAD(associatedData(bound.orgId, bound.provider))
  decipher.setAuthTag(tag)
  try {
    return Buffer.concat([decipher.update(body), decipher.final()]).toString('utf8')
  } catch {
    // One message for a wrong sealing key, a tampered row, and a row bound to a
    // different organization. Telling them apart would say which of those it
    // was, and the honest answer for all three is the same: this cannot be
    // opened here.
    throw new SealError(
      'This key cannot be opened. It was sealed with a different secret, for a different ' +
        'organization, or the stored value has been altered.',
    )
  }
}

/** A stable identifier for a plaintext, safe to store and to compare. */
export function fingerprintOf(plaintext: string): string {
  return createHash('sha256').update(plaintext, 'utf8').digest('hex').slice(0, 16)
}

/** Whether two plaintexts are the same, without either being logged. */
export function sameKey(a: string, b: string): boolean {
  const x = createHash('sha256').update(a, 'utf8').digest()
  const y = createHash('sha256').update(b, 'utf8').digest()
  return timingSafeEqual(x, y)
}

// ---------------------------------------------------------------------------
// What a key must look like before it is stored
// ---------------------------------------------------------------------------

export type Provider = 'anthropic' | 'openai'

export const PROVIDERS: Provider[] = ['anthropic', 'openai']

/**
 * Refuses a key that is obviously not one, before it is sealed.
 *
 * Not to be clever about formats, which change: to catch the three mistakes
 * that actually happen. Somebody pastes a key with a trailing newline from a
 * terminal, somebody pastes the wrong provider's key into the wrong field, and
 * somebody pastes something that is not a key at all -- a password, a URL, the
 * whole `export ANTHROPIC_API_KEY=...` line.
 *
 * Refusing here means the mistake is a message on a form rather than every run
 * failing with a 401 from a provider a week later.
 */
export function checkKeyShape(provider: Provider, key: string): string | null {
  const trimmed = key.trim()
  if (trimmed !== key) {
    // Not an error: trimmed silently, because a trailing newline from a
    // terminal paste is the single most common way this goes wrong and
    // refusing it would be pedantry.
  }
  if (trimmed.length < 20) return 'That is too short to be an API key.'
  if (/\s/.test(trimmed)) {
    return 'That contains a space or a newline. Paste the key on its own, not the whole export line.'
  }
  if (provider === 'anthropic' && !trimmed.startsWith('sk-ant-')) {
    return 'An Anthropic key starts with sk-ant-. Check you have not pasted the OpenAI one.'
  }
  if (provider === 'openai' && !trimmed.startsWith('sk-')) {
    return 'An OpenAI key starts with sk-.'
  }
  if (provider === 'openai' && trimmed.startsWith('sk-ant-')) {
    return 'That is an Anthropic key. Store it under Anthropic instead.'
  }
  return null
}

/** How a key is shown anywhere it is shown at all. */
export function displayKey(last4: string): string {
  return `••••••••${last4}`
}
