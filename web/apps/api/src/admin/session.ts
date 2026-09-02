// Operator sign-in, and the credential that is never in the source.
//
// Separate from auth/session.ts on purpose and at every level: a different
// table, a different cookie, a different hash. The product's sessions table is
// populated by GitHub OAuth, so if operator power were a flag on a product
// session then compromising somebody's GitHub account would be compromising the
// platform. Two credentials that fail independently is the whole point, and the
// way to guarantee it is for this file to never read `sessions` at all.
//
// PASSWORDS. scrypt from node:crypto, so no dependency is added for this.
// Per-row salt, and the comparison is timing-safe. A row with a NULL hash
// cannot be signed in against, because there is no password that hashes to
// NULL, and that is the state a newly created operator sits in until somebody
// provisions one. THERE IS NO DEFAULT CREDENTIAL ANYWHERE IN THIS FILE OR THIS
// SCHEMA. The root operator's first password arrives from the environment at
// deployment, once, and is written by the bootstrap command.
//
// WHY NOT OAUTH FOR OPERATORS TOO. It was the obvious answer and it is circular:
// the identity provider is a third party, and the account that recovers a
// broken integration with that provider must not depend on it.

import { createHash, randomBytes, scrypt as scryptCb, timingSafeEqual } from 'node:crypto'
import { promisify } from 'node:util'
import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import { appendAdminAudit } from '@antifailure/db'
import type { AdminRole } from './permissions.ts'

const scrypt = promisify(scryptCb) as (
  password: string,
  salt: Buffer,
  keylen: number,
) => Promise<Buffer>

export const ADMIN_SESSION_COOKIE = 'af_admin_session'

/** How long an operator session lives at all, however active.
 *
 *  Twelve hours rather than the product's thirty days. An operator session
 *  reads every customer's data, so it is the one credential where the cost of
 *  signing in again is obviously smaller than the cost of a forgotten laptop. */
export const ADMIN_SESSION_LIFETIME_MS = 12 * 60 * 60 * 1000

/** scrypt parameters. N is the work factor and the only one worth tuning; the
 *  rest are the Node defaults. 2^15 is roughly 100ms on this hardware, which is
 *  the standard trade: slow enough to matter offline, fast enough that a person
 *  signing in does not notice. */
const SCRYPT_N = 32768
const SCRYPT_KEYLEN = 64
/** scrypt needs 128 * N * r bytes, with r defaulting to 8, which for this N is
 *  exactly Node's 32MB default ceiling. Exactly is not enough: OpenSSL counts
 *  its own overhead against the same budget and refuses with "memory limit
 *  exceeded" when the two are equal, which is a startup-shaped failure that
 *  only appears the first time anybody tries to sign in. Doubling it is the
 *  cheapest way to be certainly above the line rather than exactly on it. */
const SCRYPT_MAXMEM = 128 * SCRYPT_N * 8 * 2

export function hashAdminToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

export async function hashPassword(
  password: string,
): Promise<{ hash: Buffer; salt: Buffer }> {
  const salt = randomBytes(32)
  const hash = await scryptKey(password, salt)
  return { hash, salt }
}

async function scryptKey(password: string, salt: Buffer): Promise<Buffer> {
  // The cost parameter has to be passed through the options object, and
  // maxmem raised with it, or Node refuses N above its 32MB default.
  return new Promise((resolve, reject) => {
    scryptCb(
      password,
      salt,
      SCRYPT_KEYLEN,
      { N: SCRYPT_N, maxmem: SCRYPT_MAXMEM },
      (err, key) => (err ? reject(err) : resolve(key)),
    )
  })
}

/**
 * Whether the password matches, in constant time.
 *
 * A NULL stored hash returns false WITHOUT short-circuiting the work, because
 * returning immediately would make an unprovisioned account distinguishable
 * from a wrong password by timing alone, and that is a list of which operator
 * accounts are worth attacking.
 */
export async function passwordMatches(
  password: string,
  stored: { hash: Buffer | null; salt: Buffer | null },
): Promise<boolean> {
  const salt = stored.salt ?? randomBytes(32)
  const candidate = await scryptKey(password, salt)
  if (!stored.hash) return false
  if (stored.hash.length !== candidate.length) return false
  return timingSafeEqual(stored.hash, candidate)
}

export class AdminSignInError extends Error {}

/**
 * The one message every failed sign-in gets.
 *
 * Never "no such operator" and never "wrong password". Distinguishing them
 * turns this endpoint into an oracle for which email addresses hold operator
 * accounts, which is the first thing worth knowing before attacking one.
 */
const REFUSED = 'That email and password do not match an operator account.'

export interface AdminSignInResult {
  token: string
  expiresAt: Date
  actor: { adminUserId: string; label: string; email: string; role: AdminRole }
}

/**
 * Signs an operator in, and records the attempt either way.
 *
 * A FAILED attempt is audited as well as a successful one, and it is the more
 * valuable of the two: repeated failures against one operator account is the
 * signal somebody is being targeted. The audit write for a failure happens on
 * the sign-in scope, which holds no session by definition, which is why the
 * INSERT policy on the chain admits that scope.
 */
export async function adminSignIn(
  pool: Pool,
  input: { email: string; password: string; ip?: string | null; userAgent?: string | null },
  now: Date,
): Promise<AdminSignInResult> {
  const email = input.email.trim().toLowerCase()
  if (!email || !input.password) throw new AdminSignInError(REFUSED)

  const rows = await pool.withAdminSignin(email, async (db) =>
    db.execute<{
      id: string
      email: string
      name: string
      role: AdminRole
      password_hash: Buffer | null
      password_salt: Buffer | null
      suspended_at: Date | string | null
    }>(sql`
      SELECT id, email, name, role, password_hash, password_salt, suspended_at
      FROM admin_users WHERE email = ${email}`),
  )

  const row = rows[0] ?? null
  const ok =
    row !== null &&
    row.suspended_at === null &&
    (await passwordMatches(input.password, {
      hash: row.password_hash,
      salt: row.password_salt,
    }))

  if (!ok) {
    // Recorded before the refusal is thrown, on the scope that has no session.
    await pool.withAdminSignin(email, async (db) => {
      await appendAdminAudit(db, {
        adminUserId: row?.id ?? null,
        actorLabel: email,
        action: 'admin.signin_failed',
        targetType: 'admin_user',
        targetId: row?.id ?? null,
        origin: 'admin',
        ip: input.ip ?? null,
        severity: 'notice',
        detail: {
          reason:
            row === null
              ? 'no operator account'
              : row.suspended_at !== null
                ? 'account suspended'
                : 'wrong password',
        },
        occurredAt: now,
      })
    })
    throw new AdminSignInError(REFUSED)
  }

  const token = randomBytes(32).toString('base64url')
  const tokenHash = hashAdminToken(token)
  const expiresAt = new Date(now.getTime() + ADMIN_SESSION_LIFETIME_MS)

  // The session row and its audit entry commit together. The INSERT comes
  // first because the chain's policy needs a live session to admit a
  // non-sign-in append, and because a session that exists without its record
  // is the failure this whole design is arranged against.
  await pool.withPlatformAdmin(tokenHash, async (db) => {
    await db.execute(sql`
      INSERT INTO admin_sessions (token_hash, admin_user_id, ip, user_agent,
                                  created_at, last_seen_at, expires_at)
      VALUES (${tokenHash}, ${row!.id}, ${input.ip ?? null}::inet, ${input.userAgent ?? null},
              ${now.toISOString()}, ${now.toISOString()}, ${expiresAt.toISOString()})`)
    await db.execute(sql`
      UPDATE admin_users SET last_signed_in_at = ${now.toISOString()},
                             updated_at = ${now.toISOString()}
      WHERE id = ${row!.id}::uuid`)
    await appendAdminAudit(db, {
      adminUserId: row!.id,
      actorLabel: `${row!.name} <${row!.email}>`,
      action: 'admin.signed_in',
      targetType: 'admin_user',
      targetId: row!.id,
      origin: 'admin',
      ip: input.ip ?? null,
      severity: 'notice',
      occurredAt: now,
    })
  })

  return {
    token,
    expiresAt,
    actor: { adminUserId: row!.id, label: row!.name, email: row!.email, role: row!.role },
  }
}

/**
 * Ends an operator session.
 *
 * Deleted rather than marked revoked, matching the product's own reasoning:
 * a revoked row that some later query forgets to filter is a working session,
 * and nothing in an ended operator session is worth keeping. The audit entry
 * is written first, while the session still exists to authorise the append.
 */
export async function adminSignOut(pool: Pool, token: string, now: Date): Promise<void> {
  const hash = hashAdminToken(token)
  await pool.withPlatformAdmin(hash, async (db) => {
    const rows = await db.execute<{ admin_user_id: string; label: string }>(sql`
      SELECT s.admin_user_id, u.name AS label
      FROM admin_sessions s JOIN admin_users u ON u.id = s.admin_user_id
      WHERE s.token_hash = ${hash}`)
    const row = rows[0]
    if (!row) return
    await appendAdminAudit(db, {
      adminUserId: row.admin_user_id,
      actorLabel: row.label,
      action: 'admin.signed_out',
      targetType: 'admin_session',
      origin: 'admin',
      severity: 'info',
      occurredAt: now,
    })
    await db.execute(sql`DELETE FROM admin_sessions WHERE token_hash = ${hash}`)
  })
}

/** The cookie an operator session travels in.
 *
 *  __Host- prefix, which browsers enforce: it requires Secure, requires Path=/,
 *  and FORBIDS a Domain attribute, so a subdomain takeover cannot plant this
 *  cookie the way it can plant an ordinary one. SameSite=Strict rather than the
 *  product's Lax, because there is no cross-site flow that needs to arrive at
 *  the operator portal already signed in. */
export function adminSessionCookie(token: string, expiresAt: Date, secure: boolean): string {
  const name = secure ? `__Host-${ADMIN_SESSION_COOKIE}` : ADMIN_SESSION_COOKIE
  const parts = [
    `${name}=${token}`,
    'Path=/',
    'HttpOnly',
    'SameSite=Strict',
    `Expires=${expiresAt.toUTCString()}`,
  ]
  if (secure) parts.push('Secure')
  return parts.join('; ')
}

export function clearedAdminCookie(secure: boolean): string {
  const name = secure ? `__Host-${ADMIN_SESSION_COOKIE}` : ADMIN_SESSION_COOKIE
  const parts = [`${name}=`, 'Path=/', 'HttpOnly', 'SameSite=Strict', 'Max-Age=0']
  if (secure) parts.push('Secure')
  return parts.join('; ')
}


/** What a resolved operator cookie proves. The gate in admin/trpc.ts turns this
 *  into its AdminActor; keeping the two separate means the resolver can be used
 *  by the sign-out route and the shell's "who am I" without either of them
 *  passing through a permission they do not need. */
export interface ResolvedAdminSession {
  adminUserId: string
  label: string
  email: string
  role: AdminRole
  sessionId: string
  /** The hash of the cookie this request arrived with, which is what the
   *  cross-tenant scope declares. Carried rather than re-derived, because
   *  re-hashing the token at each use is one more place to get it wrong. */
  sessionHash: Buffer
  impersonating: boolean
  impersonatedUserId: string | null
}

/**
 * Resolves an operator cookie to an actor, or null.
 *
 * Separate from the middleware so the shell's own "who am I" endpoint and the
 * sign-out route can use it without going through a permission they do not
 * need. Returns null rather than throwing for every reason a session can be
 * unusable, because the caller's answer is the same in all of them and a
 * distinction here would tell an attacker which of their guesses was closer.
 */
export async function resolveAdminSession(
  pool: Pool,
  token: string | null,
  now: Date,
): Promise<ResolvedAdminSession | null> {
  if (!token) return null
  const hash = hashAdminToken(token)

  const rows = await pool.withPlatformAdmin(hash, async (db) =>
    db.execute<{
      session_id: string
      admin_user_id: string
      email: string
      name: string
      role: AdminRole
      suspended_at: Date | string | null
      expires_at: Date | string
      revoked_at: Date | string | null
      impersonated_user_id: string | null
    }>(sql`
      SELECT s.id AS session_id, s.admin_user_id, u.email, u.name, u.role,
             u.suspended_at, s.expires_at, s.revoked_at,
             s.impersonated_user_id
      FROM admin_sessions s
      JOIN admin_users u ON u.id = s.admin_user_id
      WHERE s.token_hash = ${hash}`),
  )

  const row = rows[0]
  if (!row) return null
  if (row.revoked_at !== null) return null
  if (row.suspended_at !== null) return null
  if (new Date(row.expires_at).getTime() <= now.getTime()) return null

  return {
    adminUserId: row.admin_user_id,
    label: row.name,
    email: row.email,
    role: row.role,
    sessionId: row.session_id,
    sessionHash: hash,
    impersonating: row.impersonated_user_id !== null,
    impersonatedUserId: row.impersonated_user_id,
  }
}
