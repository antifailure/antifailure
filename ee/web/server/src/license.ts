// The licence, read by the control plane rather than only by the engine.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY THIS FILE EXISTS AT ALL, WHICH IS NOT "TO REIMPLEMENT SOMETHING".
//
// ee/engine/license parses licences in Go, and it is the definition. It runs in
// the engine process. Single sign-on and provisioning run in the control plane,
// which is Node, has no engine in it, and has never had a licence key anywhere
// near it. ee/web/sso/src/provision.ts says so in a comment it has carried
// since it was written: seats are "supplied by the host rather than read here,
// because the license is parsed by the enterprise engine module and this is the
// control plane". The host it names did not exist, so nothing supplied them,
// so the seat limit AF-EE-004 sells has never been applied to anybody.
//
// THE HAZARD, NAMED RATHER THAN IGNORED. Two implementations of one decision is
// this repository's most expensive recurring defect, and a second licence
// parser is exactly that shape. Three things hold them together and none of
// them is a comment:
//
//   1. The wire format is deliberately not a JWT and carries no algorithm
//      field, so there is no negotiation to disagree about. Ed25519 over the
//      raw payload bytes, and both languages have that primitive natively.
//   2. ee/license-vectors.json is a corpus of tokens with the verdict each one
//      must produce. ee/engine/license/vectors_test.go emits it and
//      test/vectors.test.ts here reads the same file. A divergence fails one
//      side or the other on the next run rather than in a customer's
//      installation.
//
//      THIS PARAGRAPH WAS FICTION WHEN IT WAS WRITTEN, and it is recorded
//      rather than quietly corrected. The corpus did not exist, neither of the
//      two files it named existed, and the string appeared nowhere in either
//      suite. It said "reads it" in the present tense while nothing read
//      anything, in the file whose whole purpose is to name this hazard
//      honestly. ee/README.md records three claims of exactly this shape and
//      the lesson written under them is the reason this note stays: a claim
//      resting on an invented mechanism reads identically to a true one until
//      somebody goes looking. Building it found a real divergence on the first
//      run, which is what the paragraph had been promising to prevent.
//   3. This file is deliberately smaller than the Go one. It does not issue,
//      does not rotate, and does not persist. It answers one question.
//
// WHAT IT DOES NOT DO, AND THE REASON IS THE SAME AS THE GO SIDE'S.
// Verification is offline. There is no revocation fetch, no telemetry, and no
// call to anything. An expired licence does not stop the control plane: it
// falls back to the community behaviour with every enterprise setting left on
// disk, so a renewal restores them unchanged and nobody's sign-in breaks
// because a purchase order moved slowly.

import { createPublicKey, verify, type KeyObject } from 'node:crypto'

/** One thing a licence may permit. The set is closed at issue time, not here:
 *  a licence issued for a newer release names features an older build has never
 *  heard of, and refusing the whole licence over one would turn every ordering
 *  of upgrade and renewal into an outage for features the customer did buy. */
export type Feature =
  | 'sso'
  | 'scim'
  | 'rbac'
  | 'audit_stream'
  | 'policy_enforcement'
  | 'multi_runtime'
  | 'enterprise_secrets'
  | 'billing'
  | 'enterprise_dashboard'
  | 'support_access'
  | 'compliance_packs'
  | 'air_gapped'
  | 'cloud_database'
  | 'cloud_runtime'

/** Every feature a licence can carry, sorted, matching license.AllFeatures.
 *
 *  "Matching" is checked rather than asserted: ee/license-vectors.json carries
 *  the Go side's list and test/vectors.test.ts compares this one against it. It
 *  used to be a sentence, and a sentence is what the two lists had between them
 *  for as long as both existed. */
export const ALL_FEATURES: readonly Feature[] = [
  'air_gapped',
  'audit_stream',
  'billing',
  'cloud_database',
  'cloud_runtime',
  'compliance_packs',
  'enterprise_dashboard',
  'enterprise_secrets',
  'multi_runtime',
  'policy_enforcement',
  'rbac',
  'scim',
  'sso',
  'support_access',
]

/** Features no build enforces anywhere, so a licence naming one permits
 *  nothing.
 *
 *  THE DIVERGENCE THIS EXISTS TO END. The Go reader has always filtered these,
 *  in Evaluate, by asking license.Shipped. This one did not: it permitted every
 *  feature a licence named. So the same key made the engine say billing is not
 *  permitted and the control plane print that it is, in one deployment, to one
 *  customer, with neither binary aware of the other. It is not reachable through
 *  the two gates the entry point mounts today, and it IS reachable through the
 *  startup line that reports what the licence permits right now, which is the
 *  line an operator reads to check what they bought.
 *
 *  The definition is ee/engine/license's notShipped map, which carries the
 *  reason each one is refused; licensegen prints those and this list does not
 *  need them. The names are held together by ee/license-vectors.json, which
 *  carries the Go side's list and a case whose licence names one. */
export const NOT_SHIPPED: readonly string[] = ['billing', 'enterprise_dashboard']

export interface Claims {
  id: string
  org: string
  plan: string
  features: string[]
  seats: number
  issuedAt: Date | null
  expiresAt: Date
  graceDays: number
  trial: boolean
  keyId: string
}

export type State =
  | 'active'
  | 'grace'
  | 'expired'
  | 'revoked'
  | 'clock_rollback'
  | 'wrong_org'
  | 'none'

export interface Status {
  state: State
  claims: Claims | null
  daysLeft: number
  warning: string
  /** Whether a feature is permitted right now. The only question the rest of
   *  the control plane asks: an expired licence, a revoked one, a rolled back
   *  clock and no licence at all all answer false, because the caller's job is
   *  to fall back to the community behaviour and it should not have to know
   *  which of those happened. */
  enabled(feature: string): boolean
  /** Whether the licence is being acted on. */
  honoured(): boolean
  /** Whether one more member would pass the seat limit. Reported, never
   *  enforced by removing somebody: automatically deleting a customer's
   *  colleagues because a renewal is late is not a behaviour to have. */
  seatsExceeded(current: number): boolean
}

/** Why a token is not a licence. The same three the Go side distinguishes,
 *  because they send an operator to three different places. */
export type Refusal = 'malformed' | 'tampered' | 'unknown_key'

export class LicenseRefused extends Error {
  // A field and an assignment rather than a parameter property, because the ee
  // web packages compile with erasableSyntaxOnly: Node strips types and runs
  // the file, and a parameter property is syntax that would have to be emitted.
  readonly refusal: Refusal

  constructor(refusal: Refusal, message: string) {
    super(message)
    this.name = 'LicenseRefused'
    this.refusal = refusal
  }
}

export const DEFAULT_GRACE_DAYS = 14
const TOKEN_PREFIX = 'aflic_'
const SIGNATURE_BYTES = 64
const DAY_MS = 24 * 60 * 60 * 1000
/** An hour, because ordinary time synchronisation moves a clock by seconds and
 *  a virtual machine resuming from a snapshot can move it by more. */
const ROLLBACK_TOLERANCE_MS = 60 * 60 * 1000

/** The variable an operator supplies public keys in, the same name and the same
 *  `kid=base64,kid=base64` form the engine reads. One format, so a key pasted
 *  into a deployment configures both binaries. */
export const TRUSTED_KEYS_ENV = 'AF_LICENSE_PUBLIC_KEYS'
export const LICENSE_ENV = 'AF_LICENSE_KEY'
export const ORG_ENV = 'AF_ORG'

/** What an installation with no trusted keys is told. A build from source has
 *  none, which is not a fault, and "signed by a key this build does not know"
 *  would send somebody to check a licence that is fine. */
export const NO_KEYS_MESSAGE =
  `this installation carries no licence signing keys, so no licence can be verified. ` +
  `Set ${TRUSTED_KEYS_ENV} to kid=base64 for an installation that mints its own licences`

function decodeBase64(value: string): Buffer {
  // Standard and raw URL encodings both accepted, because a key pasted out of
  // an email arrives in whichever one the sender's tool produced, and refusing
  // one of them is a support ticket rather than a security property.
  const standard = Buffer.from(value, 'base64')
  if (standard.length > 0) return standard
  return Buffer.from(value, 'base64url')
}

/**
 * The keys this installation trusts, from the environment.
 *
 * An ed25519 public key is 32 raw bytes and Node wants a key object, so each
 * one is wrapped in the DER prefix for an Ed25519 SubjectPublicKeyInfo. That
 * prefix is a constant, not a parser: there is exactly one shape an Ed25519
 * public key has.
 */
export function trustedKeys(spec: string | undefined): Map<string, KeyObject> {
  const keys = new Map<string, KeyObject>()
  for (const rawEntry of (spec ?? '').split(',')) {
    const entry = rawEntry.trim()
    if (!entry) continue
    const cut = entry.indexOf('=')
    if (cut < 0) throw new LicenseRefused('malformed', `${TRUSTED_KEYS_ENV}: an entry is not kid=key`)
    const id = entry.slice(0, cut).trim()
    if (!id) throw new LicenseRefused('malformed', `${TRUSTED_KEYS_ENV}: an entry has no key identifier`)
    const raw = decodeBase64(entry.slice(cut + 1).trim())
    if (raw.length !== 32) {
      throw new LicenseRefused(
        'malformed',
        `${TRUSTED_KEYS_ENV}: the key for ${id} is ${raw.length} bytes and an ed25519 public key is 32`,
      )
    }
    const der = Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), raw])
    keys.set(id, createPublicKey({ key: der, format: 'der', type: 'spki' }))
  }
  return keys
}

function asDate(value: unknown): Date | null {
  if (typeof value !== 'string' || value === '') return null
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

/**
 * Verifies a token's signature and returns its claims.
 *
 * It does not evaluate expiry, the organization, or the clock. Those depend on
 * the moment and on the caller's state, and mixing them in would mean a
 * signature check that can fail for reasons that have nothing to do with the
 * signature.
 */
export function parseLicense(token: string, keys: Map<string, KeyObject>): Claims {
  // Whitespace and newlines come from a licence pasted out of an email, and
  // refusing that is a support ticket rather than a security property.
  const cleaned = token.trim().split(/\s+/).join('')
  if (cleaned === '') throw new LicenseRefused('malformed', 'the license key is empty')
  if (!cleaned.startsWith(TOKEN_PREFIX)) {
    throw new LicenseRefused('malformed', `the license key should start with ${TOKEN_PREFIX}`)
  }
  const body = cleaned.slice(TOKEN_PREFIX.length)
  const dot = body.indexOf('.')
  if (dot <= 0 || dot === body.length - 1) {
    throw new LicenseRefused('malformed', 'the license key should be two parts separated by a dot')
  }

  const encodedPayload = body.slice(0, dot)
  const payload = Buffer.from(encodedPayload, 'base64url')
  // Node's base64url decoder does not refuse rubbish, it discards it, so a
  // round trip is the only way to know the input was actually base64url. A
  // decoder that silently truncates would turn a corrupted licence into a
  // signature failure and send somebody to look for tampering.
  if (payload.toString('base64url') !== encodedPayload) {
    throw new LicenseRefused('malformed', 'the first part is not base64url')
  }
  const encodedSignature = body.slice(dot + 1)
  const signature = Buffer.from(encodedSignature, 'base64url')
  if (signature.toString('base64url') !== encodedSignature) {
    throw new LicenseRefused('malformed', 'the second part is not base64url')
  }
  if (signature.length !== SIGNATURE_BYTES) {
    throw new LicenseRefused('tampered', 'the signature is the wrong length')
  }

  // Decoded before the signature is checked, only to read the key identifier.
  // Nothing from this is trusted or returned unless the signature verifies
  // afterwards.
  let raw: Record<string, unknown>
  try {
    raw = JSON.parse(payload.toString('utf8')) as Record<string, unknown>
  } catch {
    throw new LicenseRefused('malformed', 'the payload is not JSON')
  }
  if (raw === null || typeof raw !== 'object') {
    throw new LicenseRefused('malformed', 'the payload is not JSON')
  }

  const keyId = typeof raw.kid === 'string' ? raw.kid : ''
  const key = keys.get(keyId)
  if (!key) {
    throw new LicenseRefused(
      'unknown_key',
      keys.size === 0
        ? NO_KEYS_MESSAGE
        : `the license key was signed by key ${JSON.stringify(keyId)}, which this installation does not know`,
    )
  }
  if (!verify(null, payload, key, signature)) {
    throw new LicenseRefused('tampered', "the license key's signature does not verify")
  }

  const org = typeof raw.org === 'string' ? raw.org : ''
  if (org === '') throw new LicenseRefused('malformed', 'the license names no organization')
  const expiresAt = asDate(raw.expires_at)
  if (!expiresAt) throw new LicenseRefused('malformed', 'the license has no expiry')

  return {
    id: typeof raw.id === 'string' ? raw.id : '',
    org,
    plan: typeof raw.plan === 'string' ? raw.plan : '',
    // Tolerant on the read boundary: a features array carrying something that
    // is not a string is dropped rather than throwing, because one odd element
    // must never blank the whole licence.
    features: Array.isArray(raw.features) ? raw.features.filter((f): f is string => typeof f === 'string') : [],
    seats: typeof raw.seats === 'number' && Number.isFinite(raw.seats) ? raw.seats : 0,
    issuedAt: asDate(raw.issued_at),
    expiresAt,
    graceDays: typeof raw.grace_days === 'number' && Number.isFinite(raw.grace_days) ? raw.grace_days : 0,
    trial: raw.trial === true,
    keyId,
  }
}

export interface Evaluation {
  /** The organization this installation runs as. A licence issued to another
   *  one is refused, which is what stops a key from being passed around. */
  org: string
  now: Date
  /** The latest moment this licence was previously evaluated at, if the caller
   *  keeps one. Absent means it has never been seen. */
  lastSeen?: Date | null
  /** Licence identifiers that have been withdrawn. Only consulted when the
   *  operator supplied a list; there is no fetch. */
  revoked?: ReadonlySet<string>
}

/** Rounds up, so "expires in 0 days" never appears for a licence that is still
 *  valid for another few hours. */
function daysBetween(from: Date, to: Date): number {
  const d = to.getTime() - from.getTime()
  if (d <= 0) return 0
  return Math.ceil(d / DAY_MS)
}

function statusOf(
  state: State,
  claims: Claims | null,
  features: Set<string>,
  daysLeft: number,
  warning: string,
): Status {
  return {
    state,
    claims,
    daysLeft,
    warning,
    enabled: (feature) => features.has(feature),
    honoured: () => state === 'active' || state === 'grace',
    seatsExceeded: (current) => {
      if (!(state === 'active' || state === 'grace')) return false
      if (!claims || claims.seats <= 0) return false
      return current >= claims.seats
    },
  }
}

/** The status of an installation with no licence: the community edition. Not an
 *  error and not a warning. Most installations are this, deliberately and
 *  permanently. */
export function none(): Status {
  return statusOf('none', null, new Set(), 0, '')
}

/** Turns claims into a status at a moment. */
export function evaluate(claims: Claims, ev: Evaluation): Status {
  const empty = new Set<string>()

  if (ev.revoked?.has(claims.id)) {
    return statusOf('revoked', claims, empty, 0,
      'This license has been revoked. Ask about it at https://antifailure.dev/contact.')
  }

  if (claims.org.trim().toLowerCase() !== ev.org.trim().toLowerCase()) {
    return statusOf('wrong_org', claims, empty, 0,
      `This license was issued to ${claims.org} and this installation is ${ev.org}.`)
  }

  // The clock check comes before expiry, because a rolled back clock makes an
  // expired licence look current and that is exactly what somebody moving the
  // clock is trying to achieve.
  if (ev.lastSeen && ev.now.getTime() + ROLLBACK_TOLERANCE_MS < ev.lastSeen.getTime()) {
    return statusOf('clock_rollback', claims, empty, 0,
      `This machine's clock reads ${ev.now.toISOString()}, which is earlier than the ` +
        `${ev.lastSeen.toISOString()} this license was last checked at. Enterprise features stay ` +
        `off until the clock passes that time.`)
  }

  const graceDays = claims.graceDays > 0 ? claims.graceDays : DEFAULT_GRACE_DAYS
  const graceEnds = new Date(claims.expiresAt.getTime())
  graceEnds.setUTCDate(graceEnds.getUTCDate() + graceDays)

  const expired = claims.expiresAt.toUTCString()
  if (ev.now.getTime() >= graceEnds.getTime()) {
    // Negated through a variable, and zero returned as itself.
    //
    // At the exact instant the grace period ends, daysBetween is 0 and `-0` in
    // JavaScript is a value distinct from `0`: Object.is separates them and so
    // does a strict comparison in a test. The Go side is an int and has no such
    // value, so the two implementations answered differently at a boundary that
    // is reached once per licence. Found by the shared corpus on its first run,
    // which is what the corpus is for.
    const behind = daysBetween(graceEnds, ev.now)
    return statusOf('expired', claims, empty, behind === 0 ? 0 : -behind,
      `This license expired on ${expired} and its grace period has ended. Enterprise features ` +
        `are off and every enterprise setting is preserved; renewing turns them back on unchanged.`)
  }

  // Filtered, not copied. A feature this build enforces nowhere is carried in
  // the claims and never permitted, which is also how an unknown name from a
  // later release is treated: the licence names something no binary can act on,
  // and answering true would tell every caller a capability is available when
  // asking for it does nothing.
  const permitted = new Set<string>(claims.features.filter((f) => !NOT_SHIPPED.includes(f)))
  let state: State
  let daysLeft: number
  let warning: string
  if (ev.now.getTime() < claims.expiresAt.getTime()) {
    state = 'active'
    daysLeft = daysBetween(ev.now, claims.expiresAt)
    warning = daysLeft <= 30 ? `This license expires in ${daysLeft} days.` : ''
  } else {
    state = 'grace'
    daysLeft = daysBetween(ev.now, graceEnds)
    warning =
      `This license expired on ${expired}. Enterprise features keep working for ${daysLeft} more ` +
      `days, then fall back to the community behaviour. Nothing is deleted and renewing restores them.`
  }
  if (claims.trial) warning = `This is a trial license. ${warning}`.trim()
  return statusOf(state, claims, permitted, daysLeft, warning)
}

/**
 * The licence this process is running under, read from the environment.
 *
 * A malformed or unverifiable licence THROWS rather than degrading to the
 * community edition quietly. That direction is deliberate and it is the
 * opposite of expiry's: an expired licence is an ordinary commercial event and
 * the software keeps running, while a licence that does not parse means
 * somebody pasted the wrong thing into a deployment, and an enterprise control
 * plane that starts as a community one because of a typo is the failure this
 * whole lane exists to end.
 */
export function licenseFromEnv(env: NodeJS.ProcessEnv, now: Date): Status {
  const token = env[LICENSE_ENV]?.trim()
  if (!token) return none()
  const org = env[ORG_ENV]?.trim()
  if (!org) {
    throw new LicenseRefused(
      'malformed',
      `${LICENSE_ENV} is set and ${ORG_ENV} is not. A license names the organization it was ` +
        `issued to and this installation has to say which one it is, or a key issued to anybody ` +
        `would work here.`,
    )
  }
  const claims = parseLicense(token, trustedKeys(env[TRUSTED_KEYS_ENV]))
  const revoked = new Set(
    (env.AF_LICENSE_REVOKED ?? '').split(',').map((s) => s.trim()).filter((s) => s !== ''),
  )
  return evaluate(claims, { org, now, revoked })
}
