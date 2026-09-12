// Sealing a customer's provider key.
//
// The threat model is not an attacker with a database dump. It is US: the key
// belongs to somebody else, they are paying for what it spends, and the only
// code that should ever hold the plaintext is the code handing it to the
// provider. Everything else, a console page, an event, a log line, a support
// bundle, a screenshot, must be structurally unable to obtain it.
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
//
// WHY THERE IS A KEYRING HERE AND NOT A KEY. This module used to take one
// Buffer, and that single parameter was the whole of a one way door: replacing
// the sealing secret made every stored key stop opening, permanently, and the
// failure was silent because a value that will not decrypt under the key you
// are holding looks exactly like a value somebody tampered with. Rows already
// carried a key version and nothing read it, so the column recorded a rotation
// nobody could perform.
//
// A rotation needs two keys open at once: the one the old rows were sealed
// under and the one new rows are sealed under. So the unit is a SET of keys
// addressed by version, which is the same shape and the same reasoning as
// AF_LICENSE_PUBLIC_KEYS in ee/engine/license/keys.go: plural from the start,
// because something that trusts exactly one key cannot rotate without
// invalidating everything in the field, and the rotation will happen.
//
// AND THE VERSION IS IN THE ASSOCIATED DATA, which is the part that is easy to
// get wrong while believing it works. The AAD has always been
// `org:provider:version`, so a row sealed under v1 does not open with the v2
// AAD even when the v1 key is to hand. Any code that opens a row therefore has
// to build the AAD from THE ROW'S version rather than from whatever version
// this process is currently sealing under, and re-sealing has to compute a new
// AAD as well as new ciphertext. That is why `open` takes the row's version as
// a required field rather than reading a module constant.

import { createCipheriv, createDecipheriv, createHash, randomBytes, timingSafeEqual } from 'node:crypto'

/**
 * The version a single key is understood as, and the first version there ever
 * was.
 *
 * Every row written before this file held a keyring carries it, and
 * `AF_PROVIDER_KEY_SECRET` on its own still means exactly this version, so an
 * installation that has never rotated needs no new configuration and its rows
 * keep opening byte for byte.
 */
export const FIRST_KEY_VERSION = 'v1'

const ALGORITHM = 'aes-256-gcm'
const NONCE_BYTES = 12
const TAG_BYTES = 16
const KEY_BYTES = 32

/** The names the environment variables go by, in one place because three
 *  messages and the configuration reference all have to agree about them. */
export const SEALING_KEY_ENV = 'AF_PROVIDER_KEY_SECRET'
export const SEALING_KEYS_ENV = 'AF_PROVIDER_KEY_SECRETS'
export const SEALING_VERSION_ENV = 'AF_PROVIDER_KEY_VERSION'

/**
 * Anything wrong with sealing: the configuration, the stored value, or the key.
 *
 * Kept as the base class of the two below rather than replaced by them, because
 * every caller that already treats a sealing failure as one thing keeps
 * working. What the subclasses add is the ability to tell the two apart where
 * telling them apart is the whole diagnosis.
 */
export class SealError extends Error {}

/**
 * The stored value would not authenticate under the key held for its version.
 *
 * One message for a tampered row, a row bound to a different organization, and
 * a key that is simply the wrong bytes for the version it is filed under.
 * Telling those apart would say which of them it was, and the honest answer for
 * all three is the same: this cannot be opened here.
 */
export class CannotOpenError extends SealError {}

/**
 * The row names a sealing key version this process does not hold.
 *
 * THIS CLASS IS THE POINT OF THE FILE. Before it existed, an operator who
 * replaced the sealing secret saw "the stored value has been altered" on every
 * customer's key, which sends whoever reads it to look for tampering, or for a
 * bad restore, or for a bug in the query. The actual fact is that the control
 * plane is not holding the key those rows were sealed under, which is a
 * configuration an operator can fix in a minute once somebody says so.
 *
 * It names the version and the versions that ARE held, and it names no key
 * material: a version label is an operator's own word, and the count and names
 * of held keys are in the start-up log already.
 */
export class MissingSealingKeyError extends SealError {
  /** The version the row asked for, as it appeared in the row. */
  readonly keyVersion: string
  /** The versions this process holds, so the message and a caller see the same
   *  set without the caller reparsing the sentence. */
  readonly held: readonly string[]

  constructor(keyVersion: string, held: readonly string[]) {
    const safe = quoteVersion(keyVersion)
    super(
      `This key was sealed under sealing key version ${safe}, which this control plane does ` +
        `not hold. ` +
        (held.length === 0
          ? `It holds no sealing keys at all, so ${SEALING_KEY_ENV} or ${SEALING_KEYS_ENV} is unset.`
          : `It holds ${held.join(', ')}.`) +
        ` This is a configuration rather than a damaged row: add a sealing key for ${safe} to ` +
        `${SEALING_KEYS_ENV}, whose form is v2=<32 bytes of base64>, and restart. Nothing is ` +
        `lost while the old key still exists, and re-sealing every row under a version this ` +
        `control plane holds is what removes the need for it.`,
    )
    this.keyVersion = keyVersion
    this.held = held
  }
}

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

/** Enough of a row to open it. The version is required rather than optional,
 *  because a default here would silently reintroduce the bug this file exists
 *  to remove: opening a v1 row with the v2 AAD. */
export interface Openable {
  ciphertext: Buffer
  nonce: Buffer
  keyVersion: string
}

// ---------------------------------------------------------------------------
// The keys, plural
// ---------------------------------------------------------------------------

/**
 * What a version label may be.
 *
 * Bounded rather than free text, for two reasons that are both about where the
 * label ends up. It goes into the associated data, so it is part of what
 * authenticates a ciphertext and a surprising byte there is a row that stops
 * opening. And it goes into the message above, which an operator reads out of a
 * log, so a label carrying a newline or an escape sequence would be a log line
 * somebody else's tooling has to parse.
 *
 * `v1` through `v9` and beyond satisfy it, and so does a date like `2026-09`,
 * because insisting on one spelling of an operator's own bookkeeping is a
 * refusal that teaches nothing.
 */
const VERSION_SHAPE = /^[a-z0-9][a-z0-9._-]{0,31}$/

/** How a version appears in a message, when it may not be a version at all.
 *
 *  A row read out of the database can carry anything a writer put there, and
 *  the one place that value is quoted is an error an operator reads. So a label
 *  that does not satisfy the grammar is shown as its own JSON string rather
 *  than spliced into a sentence raw. */
function quoteVersion(version: string): string {
  if (VERSION_SHAPE.test(version)) return `"${version}"`
  return JSON.stringify(version.slice(0, 64))
}

/**
 * The sealing keys this process holds, addressed by version.
 *
 * More than one may be open at once and that is the entire mechanism: during a
 * rotation the old key opens the rows that have not been re-sealed yet and the
 * new one seals everything written from now on. Which one seals is `current`,
 * stated rather than inferred, because "the newest" and "the last one in the
 * string" are both orderings somebody can change by accident while believing
 * they changed nothing.
 */
export class Keyring {
  private readonly keys: ReadonlyMap<string, Buffer>
  /** The version new values are sealed under. */
  readonly current: string

  private constructor(keys: ReadonlyMap<string, Buffer>, current: string) {
    this.keys = keys
    this.current = current
  }

  /** One key, under the version a single key has always meant. The shape every
   *  installation that has not rotated is in, and what a test wants. */
  static of(key: Buffer, version: string = FIRST_KEY_VERSION): Keyring {
    return Keyring.from([[version, key]], version)
  }

  /**
   * A keyring from versions and keys.
   *
   * Refuses a key that is not 32 bytes for the reason `sealingKeyFrom` gives,
   * refuses a version that is not a version, and refuses a `current` it does
   * not hold: sealing somebody else's credential under a key you are only
   * assuming is there is not a mistake to make at the first save.
   */
  static from(entries: Iterable<readonly [string, Buffer]>, current?: string): Keyring {
    const keys = new Map<string, Buffer>()
    for (const [version, key] of entries) {
      if (!VERSION_SHAPE.test(version)) {
        throw new SealError(
          `${quoteVersion(version)} is not a sealing key version. A version is up to 32 ` +
            `characters of lower case letters, digits, dot, dash or underscore, such as v2.`,
        )
      }
      if (key.length !== KEY_BYTES) {
        throw new SealError(
          `the sealing key for version ${quoteVersion(version)} must be ${KEY_BYTES} bytes ` +
            `(got ${key.length})`,
        )
      }
      const already = keys.get(version)
      if (already && !already.equals(key)) {
        // Two different keys under one version is the one configuration that
        // cannot be resolved by choosing: whichever one is picked, half the
        // rows filed under that version were sealed with the other. Refusing
        // names the version and never compares anything out loud.
        throw new SealError(
          `two different sealing keys are configured for version ${quoteVersion(version)}. ` +
            `Rows under one version were sealed with one key, so there is no safe choice ` +
            `between them. Give the new key a version of its own.`,
        )
      }
      keys.set(version, key)
    }
    if (keys.size === 0) throw new SealError('a keyring needs at least one sealing key')

    const chosen = current ?? (keys.size === 1 ? [...keys.keys()][0]! : undefined)
    if (chosen === undefined) {
      // Deliberately a refusal rather than a guess. This process holds several
      // keys, which only happens during a rotation, and the question "which one
      // do new rows get sealed under" has a right answer that the operator
      // knows and this code does not.
      throw new SealError(
        `${keys.size} sealing keys are configured (${[...keys.keys()].sort().join(', ')}) and ` +
          `${SEALING_VERSION_ENV} does not say which one new keys are sealed under. Set it to ` +
          `one of them. It may be omitted only while exactly one key is configured.`,
      )
    }
    if (!keys.has(chosen)) {
      throw new SealError(
        `${SEALING_VERSION_ENV} is ${quoteVersion(chosen)} and no sealing key is configured ` +
          `for that version. Configured: ${[...keys.keys()].sort().join(', ')}.`,
      )
    }
    return new Keyring(keys, chosen)
  }

  /** Whether a row filed under this version can be opened here. */
  has(version: string): boolean {
    return this.keys.has(version)
  }

  /** The key for a version, or the error that says which version is missing. */
  keyFor(version: string): Buffer {
    const key = this.keys.get(version)
    if (!key) throw new MissingSealingKeyError(version, this.versions())
    return key
  }

  /**
   * The same keys, sealing under a different one of them.
   *
   * What re-sealing needs: the set does not change, only which member of it new
   * ciphertext is produced under. A version this keyring does not hold is
   * refused by `from`, so a re-seal to a key nobody configured cannot start.
   */
  sealingUnder(version: string): Keyring {
    if (version === this.current) return this
    return Keyring.from([...this.keys].map(([v, k]) => [v, k] as const), version)
  }

  /** Every version held, sorted, for a message or a log line. Never the keys. */
  versions(): string[] {
    return [...this.keys.keys()].sort()
  }

  get size(): number {
    return this.keys.size
  }

  /**
   * The sentence the start-up log prints.
   *
   * Versions and a count, never key material. During a rotation this is the
   * line that proves a revision actually picked the new key up, which is the
   * step the runbook cannot otherwise verify without decrypting something.
   */
  summary(): string {
    const held = this.versions().join(', ')
    return this.size === 1
      ? `provider keys can be stored: one sealing key, version ${held}`
      : `provider keys can be stored: ${this.size} sealing keys (${held}), sealing under ${this.current}`
  }
}

/**
 * Reads one sealing key from one base64 value.
 *
 * Refuses anything that is not exactly 32 bytes. A short key is not "weaker
 * encryption", it is a different failure: Node would throw deep inside the
 * cipher on first use, which is at the moment somebody saves a key rather than
 * at start-up, so an installation would look healthy and break on the one
 * action this feature exists for.
 */
export function sealingKeyFrom(value: string | undefined, name = SEALING_KEY_ENV): Buffer | null {
  if (!value) return null
  let key: Buffer
  try {
    key = decodeBase64(value.trim())
  } catch {
    throw new SealError(`${name} is not valid base64.`)
  }
  if (key.length !== KEY_BYTES) {
    throw new SealError(
      `${name} must be ${KEY_BYTES} bytes of base64 (got ${key.length}). ` +
        'Generate one with: openssl rand -base64 32',
    )
  }
  return key
}

/**
 * Standard and URL-safe base64 both accepted, and neither silently.
 *
 * Buffer.from(x, 'base64') accepts both alphabets and, worse, ignores anything
 * it does not recognise, so a truncated paste or a value with a stray quote
 * decodes to fewer bytes rather than failing. The length check above is what
 * catches that, and re-encoding here is what makes it catch a value that
 * happens to land on 32 bytes after characters were dropped.
 */
function decodeBase64(value: string): Buffer {
  const key = Buffer.from(value, 'base64')
  const canonical = key.toString('base64').replace(/=+$/, '')
  const given = value.replace(/=+$/, '').replace(/-/g, '+').replace(/_/g, '/')
  if (canonical !== given) {
    throw new Error('not canonical base64')
  }
  return key
}

/**
 * Builds the keyring from the environment, or null when no key is configured.
 *
 * Three variables, and only the first is needed by an installation that has
 * never rotated:
 *
 *   AF_PROVIDER_KEY_SECRET   32 bytes of base64. Means version v1, which is
 *                            what every existing row is filed under.
 *   AF_PROVIDER_KEY_SECRETS  version=base64,version=base64. The rotation set,
 *                            in the same grammar as AF_LICENSE_PUBLIC_KEYS.
 *   AF_PROVIDER_KEY_VERSION  which version new keys are sealed under. Optional
 *                            while exactly one key is configured.
 *
 * Both key variables are MERGED rather than one overriding the other, and that
 * is what makes the hosted rotation a single new secret rather than a rewrite
 * of the existing one. v1 stays where Terraform generated it, the operator adds
 * v2 in a secret of their own, and nobody has to read the old value out of the
 * vault to compose a combined string. A v1 given in both with different bytes
 * is refused rather than resolved, by `Keyring.from`.
 */
export function keyringFrom(env: {
  AF_PROVIDER_KEY_SECRET?: string | undefined
  AF_PROVIDER_KEY_SECRETS?: string | undefined
  AF_PROVIDER_KEY_VERSION?: string | undefined
}): Keyring | null {
  const entries: [string, Buffer][] = []

  const single = sealingKeyFrom(env.AF_PROVIDER_KEY_SECRET)
  if (single) entries.push([FIRST_KEY_VERSION, single])

  for (const entry of (env.AF_PROVIDER_KEY_SECRETS ?? '').split(',')) {
    const text = entry.trim()
    if (text === '') continue
    // The FIRST equals sign, and both halves have to be non-empty. Base64 of 32
    // bytes ends in one pad character, so a bare key pasted into this variable
    // splits into a 43 character "version" and an empty key rather than failing
    // to split at all, and that is the mistake worth naming precisely.
    const cut = text.indexOf('=')
    const version = cut < 0 ? '' : text.slice(0, cut).trim()
    const encoded = cut < 0 ? '' : text.slice(cut + 1).trim()
    if (version === '' || encoded === '') {
      throw new SealError(
        `${SEALING_KEYS_ENV} holds an entry that is not version=key. The form is ` +
          `v1=<32 bytes of base64>,v2=<32 bytes of base64>. A key with no version in front of ` +
          `it belongs in ${SEALING_KEY_ENV}, which means version ${FIRST_KEY_VERSION}.`,
      )
    }
    const key = sealingKeyFrom(encoded, `${SEALING_KEYS_ENV} entry ${quoteVersion(version)}`)!
    entries.push([version, key])
  }

  if (entries.length === 0) {
    const asked = env.AF_PROVIDER_KEY_VERSION?.trim()
    if (asked) {
      // A version with no keys is a half-applied configuration, and the half
      // that is missing is the one that matters. Saying so beats reporting the
      // feature as simply switched off.
      throw new SealError(
        `${SEALING_VERSION_ENV} is ${quoteVersion(asked)} and no sealing key is configured. ` +
          `Set ${SEALING_KEY_ENV} or ${SEALING_KEYS_ENV}, or unset ${SEALING_VERSION_ENV}.`,
      )
    }
    return null
  }

  const current = env.AF_PROVIDER_KEY_VERSION?.trim()
  return Keyring.from(entries, current === undefined || current === '' ? undefined : current)
}

// ---------------------------------------------------------------------------
// Sealing and opening
// ---------------------------------------------------------------------------

/** What a ciphertext is bound to. A row that moves between these does not open.
 *
 *  The version is in here, so re-sealing is not just new ciphertext under a new
 *  key: it is a new AAD as well, and a row opened under one version has to be
 *  sealed under the other's. */
function associatedData(orgId: string, provider: string, keyVersion: string): Buffer {
  return Buffer.from(`${orgId}:${provider}:${keyVersion}`, 'utf8')
}

export function seal(
  keyring: Keyring,
  plaintext: string,
  bound: { orgId: string; provider: string },
): Sealed {
  if (!plaintext) throw new SealError('There is no key to store.')
  const keyVersion = keyring.current
  const nonce = randomBytes(NONCE_BYTES)
  const cipher = createCipheriv(ALGORITHM, keyring.keyFor(keyVersion), nonce)
  cipher.setAAD(associatedData(bound.orgId, bound.provider, keyVersion))
  const body = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()])
  const tag = cipher.getAuthTag()

  return {
    ciphertext: Buffer.concat([body, tag]),
    nonce,
    keyVersion,
    fingerprint: fingerprintOf(plaintext),
    last4: plaintext.slice(-4),
  }
}

export function open(
  keyring: Keyring,
  sealed: Openable,
  bound: { orgId: string; provider: string },
): string {
  // BEFORE anything else, including the length check, because "I am not holding
  // that key" is true regardless of what the bytes look like and it is the
  // sentence an operator mid-rotation needs. A truncated row under a version
  // nobody holds is two problems, and the one to report is the one that
  // explains every other row in the table at the same time.
  const key = keyring.keyFor(sealed.keyVersion)

  if (sealed.ciphertext.length <= TAG_BYTES) {
    throw new SealError('The stored key is too short to be a sealed value.')
  }
  const body = sealed.ciphertext.subarray(0, sealed.ciphertext.length - TAG_BYTES)
  const tag = sealed.ciphertext.subarray(sealed.ciphertext.length - TAG_BYTES)

  const decipher = createDecipheriv(ALGORITHM, key, sealed.nonce)
  decipher.setAAD(associatedData(bound.orgId, bound.provider, sealed.keyVersion))
  decipher.setAuthTag(tag)
  try {
    return Buffer.concat([decipher.update(body), decipher.final()]).toString('utf8')
  } catch {
    throw new CannotOpenError(
      'This key cannot be opened. It was sealed with a different secret under the same version, ' +
        'for a different organization, or the stored value has been altered. The sealing key ' +
        `for version ${quoteVersion(sealed.keyVersion)} IS configured here, so this is not a ` +
        'missing key.',
    )
  }
}

/**
 * Opens a value under its own version and seals it under another.
 *
 * Here rather than in the re-sealing tool, because the invariant that makes a
 * rotation safe is a property of this layer: the new value is OPENED AGAIN,
 * with the new key and the new associated data, and compared against what came
 * out of the old one, before this function returns anything a caller could
 * write. So a row is either replaced by a value already proven to open or not
 * touched at all, and there is no arrangement of a failure part way through
 * that leaves ciphertext nothing can read.
 *
 * The fingerprint is checked too, against the one stored beside the row. It is
 * SHA-256 of the plaintext, so a mismatch means the value that came out is not
 * the value that went in, which no correct key can produce; it is the cheap
 * proof that this opened the right row rather than merely opened something.
 */
export function resealValue(
  keyring: Keyring,
  sealed: Openable & { fingerprint?: string },
  bound: { orgId: string; provider: string },
  to: string = keyring.current,
): Sealed {
  const plaintext = open(keyring, sealed, bound)
  if (sealed.fingerprint !== undefined && fingerprintOf(plaintext) !== sealed.fingerprint) {
    throw new SealError(
      'the value opened does not match the fingerprint stored beside it, so this row is not ' +
        'what it says it is and it has not been re-sealed',
    )
  }
  const under = keyring.sealingUnder(to)
  const next = seal(under, plaintext, bound)
  if (open(under, next, bound) !== plaintext) {
    // Unreachable unless the cipher itself is wrong, and asserted anyway: this
    // is the one line standing between a rotation and a table of ciphertext
    // nothing can read, and an assertion that never fires costs one decryption
    // per row.
    throw new SealError('the re-sealed value did not open again, so the row has not been changed')
  }
  return next
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
 * somebody pastes something that is not a key at all, a password, a URL, the
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
