// Signing a terminal in, when the terminal has no browser.
//
// RFC 8628, the device authorization grant, adapted in one way that matters:
// the browser half of it is this application's ordinary GitHub sign-in rather
// than a second identity provider. `af login` asks for a pair of codes, prints
// the short one, and polls with the long one while a person approves it in a
// browser that already has a session.
//
// WHY NOT JUST PASTE A TOKEN. Because a token somebody pastes has to exist
// before it is pasted, which means it is created in a browser, copied through a
// clipboard, and usually left in a shell history file. The device grant never
// puts the credential in front of a person at all: the terminal receives it
// over TLS and puts it straight in the OS keyring.
//
// THE THREE THINGS THAT MAKE THE SHORT CODE SAFE, and all three are required:
//
//   1. The alphabet excludes every character people confuse. No O or 0, no I,
//      L or 1. Somebody reads this off one screen and types it into another,
//      and a code that is ambiguous to a human is a code that gets retyped
//      until it is guessed by accident.
//   2. It expires in fifteen minutes, and a guess only works while a real
//      request is outstanding.
//   3. The approve endpoint is rate limited by address in ENDPOINT_LIMITS.
//
// With 28 characters and 8 of them, a guess is one in 3.8e11 against the
// handful of codes alive at any moment. Take away the rate limit and that
// arithmetic stops mattering, which is why the limit is not optional.

import { createHash, randomBytes, randomInt, timingSafeEqual } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Clock } from '../clock.ts'

/** How long a person has to approve. RFC 8628 suggests a short window. */
export const DEVICE_CODE_TTL_MS = 15 * 60 * 1000

/** How often the terminal may poll, in seconds. */
export const DEVICE_POLL_INTERVAL_SECONDS = 5

/** How long the token a successful login receives is good for. */
export const CLI_TOKEN_TTL_MS = 90 * 24 * 60 * 60 * 1000

// No O, 0, I, L or 1. See the note above: this is a human transcription
// channel, and the failure it produces is silent.
const USER_CODE_ALPHABET = 'BCDFGHJKMNPQRSTVWXYZ23456789'

export class DeviceError extends Error {
  /** The RFC 8628 error code, which the CLI switches on. */
  readonly code: string
  constructor(code: string, message: string) {
    super(message)
    this.code = code
  }
}

export function hashDeviceCode(code: string): Buffer {
  return createHash('sha256').update(code).digest()
}

/** Formatted XXXX-XXXX, because eight characters in a row get miscounted. */
export function newUserCode(): string {
  const pick = (): string => USER_CODE_ALPHABET[randomInt(USER_CODE_ALPHABET.length)]!
  const half = (): string => Array.from({ length: 4 }, pick).join('')
  return `${half()}-${half()}`
}

/** Accepts what a person actually types: any case, with or without the dash. */
export function normaliseUserCode(input: string): string {
  const bare = input.trim().toUpperCase().replace(/[^A-Z0-9]/g, '')
  if (bare.length !== 8) return ''
  return `${bare.slice(0, 4)}-${bare.slice(4)}`
}

export interface DeviceAuthorization {
  deviceCode: string
  userCode: string
  verificationUri: string
  verificationUriComplete: string
  expiresIn: number
  interval: number
}

/**
 * Step one: the terminal asks.
 *
 * The scopes are recorded here and nowhere else. Approval cannot widen them,
 * because the token is minted from this column rather than from anything in
 * the approve request, and that is the whole reason they are stored rather
 * than passed along.
 */
export async function requestDeviceCode(
  pool: Pool,
  clock: Clock,
  input: { clientLabel: string; scopes: string[]; baseUrl: string },
): Promise<DeviceAuthorization> {
  const deviceCode = randomBytes(32).toString('base64url')
  const hash = hashDeviceCode(deviceCode)
  const userCode = newUserCode()
  const expiresAt = new Date(clock.now().getTime() + DEVICE_CODE_TTL_MS)

  await pool.withoutTenant(
    async (db) => {
      await db.execute(sql`
        INSERT INTO device_authorizations
          (device_code_hash, user_code, scopes, client_label, created_at, expires_at)
        VALUES (${hash}, ${userCode}, ${sql.param(input.scopes)}::text[],
                ${input.clientLabel.slice(0, 200)},
                ${clock.now().toISOString()}, ${expiresAt.toISOString()})`)
    },
    { deviceCodeHash: hash },
  )

  const base = input.baseUrl.replace(/\/$/, '')
  return {
    deviceCode,
    userCode,
    verificationUri: `${base}/device`,
    // With the code already in it, so the common path is a click rather than a
    // transcription. The short code still exists because the link cannot be
    // followed on a machine with no browser, which is the case this is for.
    verificationUriComplete: `${base}/device?code=${encodeURIComponent(userCode)}`,
    expiresIn: Math.floor(DEVICE_CODE_TTL_MS / 1000),
    interval: DEVICE_POLL_INTERVAL_SECONDS,
  }
}

export interface PendingApproval {
  userCode: string
  clientLabel: string
  scopes: string[]
  expiresAt: Date
}

/** What the approval screen shows: which terminal is asking, and for what. */
export async function describePending(
  pool: Pool,
  clock: Clock,
  userCode: string,
): Promise<PendingApproval | null> {
  const code = normaliseUserCode(userCode)
  if (!code) return null

  return pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{
        user_code: string
        client_label: string
        scopes: string[]
        expires_at: Date | string
        approved_at: Date | string | null
        denied_at: Date | string | null
      }>(sql`
        SELECT user_code, client_label, scopes, expires_at, approved_at, denied_at
        FROM device_authorizations WHERE user_code = ${code}`)
      const row = rows[0]
      if (!row) return null
      const expiresAt = asDate(row.expires_at)
      if (expiresAt.getTime() <= clock.now().getTime()) return null
      if (row.approved_at || row.denied_at) return null
      return {
        userCode: row.user_code,
        clientLabel: row.client_label,
        scopes: row.scopes,
        expiresAt,
      }
    },
    { deviceUserCode: code },
  )
}

/**
 * Step two: a person approves it, from a browser that has a session.
 *
 * The organization comes from the approving session, not from the request. A
 * terminal cannot ask to be let into a tenant; it can only receive the one the
 * person approving it is already in.
 */
export async function approveDeviceCode(
  pool: Pool,
  clock: Clock,
  input: { userCode: string; userId: string; orgId: string; actorLabel: string },
): Promise<{ approved: true }> {
  const code = normaliseUserCode(input.userCode)
  if (!code) throw new DeviceError('invalid_request', 'That is not a valid code.')

  await pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{ expires_at: Date | string; approved_at: Date | string | null; denied_at: Date | string | null }>(sql`
        SELECT expires_at, approved_at, denied_at FROM device_authorizations
        WHERE user_code = ${code}`)
      const row = rows[0]
      // One message for "never existed", "already used" and "expired". Telling
      // them apart turns this endpoint into a way of asking whether a code was
      // ever real, which is exactly the oracle the short alphabet cannot afford.
      if (!row || asDate(row.expires_at).getTime() <= clock.now().getTime() || row.approved_at || row.denied_at) {
        throw new DeviceError('expired_token', 'That code is not valid any more. Run af login again.')
      }
      await db.execute(sql`
        UPDATE device_authorizations
        SET approved_at = ${clock.now().toISOString()},
            approved_user_id = ${input.userId}::uuid,
            approved_org_id = ${input.orgId}::uuid
        WHERE user_code = ${code}`)
    },
    { deviceUserCode: code },
  )

  await pool.withTenant({ orgId: input.orgId, userId: input.userId }, async (db) => {
    await appendAudit(db, {
      orgId: input.orgId,
      actorLabel: input.actorLabel,
      action: 'device.approved',
      targetType: 'device_authorization',
      targetId: code,
      origin: 'web',
      detail: {},
      occurredAt: clock.now(),
    })
  })

  return { approved: true }
}

/** A person can also say no, which is not the same as letting it expire. */
export async function denyDeviceCode(
  pool: Pool,
  clock: Clock,
  userCode: string,
): Promise<{ denied: true }> {
  const code = normaliseUserCode(userCode)
  if (!code) throw new DeviceError('invalid_request', 'That is not a valid code.')
  await pool.withoutTenant(
    async (db) => {
      await db.execute(sql`
        UPDATE device_authorizations SET denied_at = ${clock.now().toISOString()}
        WHERE user_code = ${code} AND approved_at IS NULL AND denied_at IS NULL`)
    },
    { deviceUserCode: code },
  )
  return { denied: true }
}

export interface IssuedToken {
  accessToken: string
  tokenType: 'Bearer'
  expiresIn: number
  scopes: string[]
  orgId: string
}

/**
 * Step three: the terminal collects its token.
 *
 * Every refusal here is one of RFC 8628's four, because the CLI has to be able
 * to tell "keep waiting" from "give up", and a single generic error makes a
 * client either poll forever or quit on the first tick.
 */
export async function redeemDeviceCode(
  pool: Pool,
  clock: Clock,
  deviceCode: string,
): Promise<IssuedToken> {
  const hash = hashDeviceCode(deviceCode)

  // THE TRANSACTION RETURNS A VERDICT AND THROWS NOTHING, and that is not a
  // style preference.
  //
  // This function has to record last_polled_at on EVERY poll, including the
  // ones that answer "not yet", because that timestamp is the only thing
  // slow_down is computed from. Throwing DeviceError from inside the callback
  // rolls the transaction back, which discards the write that just happened, so
  // last_polled_at stayed null forever and slow_down could never fire. Every
  // client could poll as fast as it liked and the server would never say
  // otherwise.
  //
  // The bug was invisible from the outside: authorization_pending is the
  // correct answer to an unapproved poll, so the endpoint looked right. Only a
  // test that polled twice and expected the SECOND answer to differ found it.
  const verdict = await pool.withoutTenant(
    async (db): Promise<
      | { ok: true; scopes: string[]; clientLabel: string; userId: string; orgId: string }
      | { ok: false; code: string; message: string }
    > => {
      const rows = await db.execute<{
        id: string
        device_code_hash: Buffer
        scopes: string[]
        client_label: string
        expires_at: Date | string
        approved_at: Date | string | null
        approved_user_id: string | null
        approved_org_id: string | null
        denied_at: Date | string | null
        redeemed_at: Date | string | null
        last_polled_at: Date | string | null
      }>(sql`
        SELECT id, device_code_hash, scopes, client_label, expires_at, approved_at,
               approved_user_id, approved_org_id, denied_at, redeemed_at, last_polled_at
        FROM device_authorizations WHERE device_code_hash = ${hash}`)
      const row = rows[0]
      const gone = { ok: false as const, code: 'expired_token', message: 'This login request is not valid any more.' }
      if (!row) return gone

      // Compared again in constant time, so the code stays correct if the
      // lookup is ever changed to anything but an exact match.
      if (
        row.device_code_hash.length !== hash.length ||
        !timingSafeEqual(row.device_code_hash, hash)
      ) {
        return gone
      }

      const now = clock.now()

      // Recorded before any verdict is decided, so that it survives all of
      // them. See the note above.
      const lastPolled = row.last_polled_at ? asDate(row.last_polled_at) : null
      await db.execute(sql`
        UPDATE device_authorizations SET last_polled_at = ${now.toISOString()}
        WHERE id = ${row.id}::uuid`)

      if (row.redeemed_at) {
        // Single use. A device code that could be redeemed twice mints a second
        // token from anywhere it has ever been.
        return { ok: false, code: 'expired_token', message: 'This login has already been completed.' }
      }
      if (row.denied_at) {
        return { ok: false, code: 'access_denied', message: 'That login was declined.' }
      }
      if (asDate(row.expires_at).getTime() <= now.getTime()) {
        return { ok: false, code: 'expired_token', message: 'This login request expired. Run af login again.' }
      }

      if (!row.approved_at || !row.approved_user_id || !row.approved_org_id) {
        // Polling faster than the interval is told to back off rather than
        // refused, so a client with a tight loop slows down instead of failing.
        const tooSoon =
          lastPolled !== null &&
          now.getTime() - lastPolled.getTime() < DEVICE_POLL_INTERVAL_SECONDS * 1000
        return tooSoon
          ? { ok: false, code: 'slow_down', message: 'Polling too fast.' }
          : { ok: false, code: 'authorization_pending', message: 'Waiting for approval in the browser.' }
      }

      // Claimed in the same transaction that read the approval, so two
      // terminals racing on one device code cannot both come away with a token.
      const claimed = await db.execute<{ id: string }>(sql`
        UPDATE device_authorizations SET redeemed_at = ${now.toISOString()}
        WHERE id = ${row.id}::uuid AND redeemed_at IS NULL
        RETURNING id`)
      if (claimed.length === 0) {
        return { ok: false, code: 'expired_token', message: 'This login has already been completed.' }
      }

      return {
        ok: true,
        scopes: row.scopes,
        clientLabel: row.client_label,
        userId: row.approved_user_id,
        orgId: row.approved_org_id,
      }
    },
    { deviceCodeHash: hash },
  )

  if (!verdict.ok) throw new DeviceError(verdict.code, verdict.message)
  const outcome = verdict

  // The token itself. Written as an engine token of kind 'cli' rather than into
  // a second table: the hashing, the prefix, the revocation and the policy that
  // lets a bearer token find its own organization are already here and already
  // tested, and a parallel implementation of exactly those is where a subtle
  // difference becomes a security bug.
  const accessToken = `afu_${randomBytes(32).toString('base64url')}`
  const tokenHash = createHash('sha256').update(accessToken).digest()
  const expiresAt = new Date(clock.now().getTime() + CLI_TOKEN_TTL_MS)

  await pool.withTenant({ orgId: outcome.orgId, userId: outcome.userId }, async (db) => {
    await db.execute(sql`
      INSERT INTO engine_tokens
        (org_id, name, token_hash, prefix, created_by, kind, user_id, scopes, expires_at)
      VALUES (${outcome.orgId}::uuid, ${outcome.clientLabel}, ${tokenHash},
              ${accessToken.slice(0, 12)}, ${outcome.userId}::uuid, 'cli',
              ${outcome.userId}::uuid, ${sql.param(outcome.scopes)}::text[],
              ${expiresAt.toISOString()})`)
    await appendAudit(db, {
      orgId: outcome.orgId,
      actorLabel: outcome.clientLabel,
      action: 'device.token_issued',
      targetType: 'engine_token',
      targetId: accessToken.slice(0, 12),
      origin: 'cli',
      detail: { scopes: outcome.scopes },
      occurredAt: clock.now(),
    })
  })

  return {
    accessToken,
    tokenType: 'Bearer',
    expiresIn: Math.floor(CLI_TOKEN_TTL_MS / 1000),
    scopes: outcome.scopes,
    orgId: outcome.orgId,
  }
}

export interface CliIdentity {
  login: string
  name: string | null
  orgSlug: string
  role: string
  scopes: string[]
  expiresAt: Date | null
  tokenPrefix: string
}

/**
 * Who a CLI token belongs to, for `af whoami`.
 *
 * Returns null for a token that is revoked, expired, or not of kind 'cli'. An
 * engine token deliberately has no identity to report: a machine is not a
 * person, and answering with one would put a machine's actions in a human's
 * name.
 */
export async function identify(
  pool: Pool,
  clock: Clock,
  token: string,
): Promise<CliIdentity | null> {
  if (!token || token.length < 16) return null
  const hash = createHash('sha256').update(token).digest()

  const found = await pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{
        id: string
        org_id: string
        user_id: string | null
        token_hash: Buffer
        prefix: string
        kind: string
        scopes: string[]
        revoked_at: Date | string | null
        expires_at: Date | string | null
      }>(sql`
        SELECT id, org_id, user_id, token_hash, prefix, kind, scopes, revoked_at, expires_at
        FROM engine_tokens WHERE token_hash = ${hash}`)
      const row = rows[0]
      if (!row || row.kind !== 'cli' || !row.user_id) return null
      if (row.token_hash.length !== hash.length || !timingSafeEqual(row.token_hash, hash)) return null
      if (row.revoked_at) return null
      if (row.expires_at && asDate(row.expires_at).getTime() <= clock.now().getTime()) return null

      await db.execute(sql`
        UPDATE engine_tokens SET last_used_at = ${clock.now().toISOString()}
        WHERE id = ${row.id}::uuid`)
      return row
    },
    { engineTokenHash: hash },
  )
  if (!found || !found.user_id) return null

  return pool.withTenant({ orgId: found.org_id, userId: found.user_id }, async (db) => {
    const rows = await db.execute<{
      github_login: string
      name: string | null
      slug: string
      role: string
    }>(sql`
      SELECT u.github_login, u.name, o.slug, m.role
      FROM users u
      JOIN members m ON m.user_id = u.id AND m.org_id = ${found.org_id}::uuid
      JOIN organizations o ON o.id = m.org_id
      WHERE u.id = ${found.user_id}::uuid`)
    const row = rows[0]
    // A token whose membership has been removed is not an identity any more.
    // The token still exists; the person is no longer in the organization, and
    // answering with a stale role is how a removed member keeps working.
    if (!row) return null
    return {
      login: row.github_login,
      name: row.name,
      orgSlug: row.slug,
      role: row.role,
      scopes: found.scopes,
      expiresAt: found.expires_at ? asDate(found.expires_at) : null,
      tokenPrefix: found.prefix,
    }
  })
}

/** `af logout` on the server side: the token stops working for everyone. */
export async function revokeCliToken(
  pool: Pool,
  clock: Clock,
  token: string,
): Promise<{ revoked: boolean }> {
  const hash = createHash('sha256').update(token).digest()
  return pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{ id: string }>(sql`
        UPDATE engine_tokens SET revoked_at = ${clock.now().toISOString()}
        WHERE token_hash = ${hash} AND kind = 'cli' AND revoked_at IS NULL
        RETURNING id`)
      return { revoked: rows.length > 0 }
    },
    { engineTokenHash: hash },
  )
}

/**
 * Removes device authorizations that are finished with.
 *
 * Expiry is checked on every read, so a sweeper that is late costs table size
 * and nothing else. This exists so the table does not grow without bound on an
 * instance where people run af login all day.
 */
export async function sweepDeviceAuthorizations(pool: Pool, clock: Clock): Promise<number> {
  return pool.withoutTenant(async (db) => {
    const rows = await db.execute<{ id: string }>(sql`
      DELETE FROM device_authorizations
      WHERE expires_at < ${new Date(clock.now().getTime() - 24 * 60 * 60 * 1000).toISOString()}
      RETURNING id`)
    return rows.length
  })
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}
