// Sessions, and the three things that go wrong with them.
//
// Fixation: a session created before sign-in and kept afterwards lets an
// attacker who planted the cookie ride the victim's login. Every sign-in
// creates a new session and destroys the one it replaced, so there is no
// identifier that spans the two.
//
// Theft from the database: what is stored is a hash. The token exists in the
// user's cookie and nowhere else, so a leaked backup is a list of hashes.
//
// Cross-site request forgery: the cookie is SameSite=Lax, which stops the
// common case, and every mutating request also has to present a token derived
// from the session token. Lax alone is not enough, because a top-level POST
// from a form on another site is not blocked by Lax in every browser, and
// because a subdomain takeover puts an attacker inside SameSite.
//
// The token is HMAC(session token, "csrf") rather than a stored secret. It is
// safe to hand to the page, being a one-way function of the cookie rather than
// the cookie, and an attacker on another origin cannot read the cookie and so
// cannot derive it. Nothing is stored, so nothing has to be kept in step when a
// session rotates and nothing in the row is worth reading.

import { createHash, randomBytes, timingSafeEqual, createHmac } from 'node:crypto'
import { eq, and, sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import { schema } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Role } from '../permissions.ts'

export const SESSION_COOKIE = 'af_session'
export const CSRF_HEADER = 'x-antifailure-csrf'

/** How long a session lives without being used again. */
export const IDLE_TIMEOUT_MS = 12 * 60 * 60 * 1000
/** How long a session lives at all, however active. */
export const ABSOLUTE_LIFETIME_MS = 30 * 24 * 60 * 60 * 1000

export interface IssuedSession {
  /** The value that goes in the cookie. Returned once and never stored. */
  token: string
  csrfToken: string
  expiresAt: Date
}

export function hashToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

/**
 * Creates a session for a user.
 *
 * `replacing` is the session being upgraded, if any. Passing it is what makes
 * this a rotation rather than an accumulation: sign-in with a session already
 * in hand must not leave the old one usable.
 */
export async function issueSession(
  pool: Pool,
  clock: Clock,
  input: {
    userId: string
    orgId: string | null
    ip?: string
    userAgent?: string
    replacing?: string
  },
): Promise<IssuedSession> {
  const token = randomBytes(32).toString('base64url')
  const tokenHash = hashToken(token)
  const expiresAt = new Date(clock.now().getTime() + ABSOLUTE_LIFETIME_MS)

  // Removing the session being replaced happens in its own transaction, because
  // the policy on this table returns the row whose hash the transaction
  // declares, and these are two different sessions. Deleted rather than marked
  // revoked: a revoked row that some later query forgets to filter is a working
  // session, and nothing in an old session is worth keeping.
  if (input.replacing) {
    const oldHash = hashToken(input.replacing)
    await pool.withoutTenant(
      async (db) => {
        await db.execute(sql`DELETE FROM sessions WHERE token_hash = ${oldHash}`)
      },
      { sessionHash: oldHash },
    )
  }

  await pool.withoutTenant(
    async (db) => {
      await db.execute(sql`
        INSERT INTO sessions (token_hash, user_id, org_id, ip, user_agent,
                              created_at, last_seen_at, expires_at)
        VALUES (${tokenHash}, ${input.userId}, ${input.orgId},
                ${input.ip ?? null}, ${input.userAgent ?? null},
                ${clock.now().toISOString()}, ${clock.now().toISOString()},
                ${expiresAt.toISOString()})`)
    },
    { sessionHash: tokenHash },
  )

  return { token, csrfToken: csrfTokenFor(token), expiresAt }
}

export interface ResolvedSession {
  sessionId: string
  userId: string
  orgId: string | null
  /** The organization's human name, for a page to say which tenant it is
   *  showing. Being in the wrong one is the most confusing state this product
   *  has, because every page is simply empty, and an identifier in the corner
   *  answers a question a UUID does not. */
  orgSlug: string | null
  label: string
  role: Role | null
  plan: string | null
  /** What a mutating request must present, derived from the token the caller
   *  already holds. */
  csrfToken: string
  expiresAt: Date
}

/**
 * Resolves a cookie to a session, or returns null.
 *
 * Expiry is checked here rather than left to a sweeper, because a sweeper that
 * is behind is a window in which expired sessions work. The sweeper exists too,
 * to keep the table small, and it is not what enforces the deadline.
 *
 * The role is read here, on every request, and not carried in the session.
 * Removing somebody from an organization has to take effect on their next
 * request, not on their next sign-in.
 */
export async function resolveSession(
  pool: Pool,
  clock: Clock,
  token: string,
): Promise<ResolvedSession | null> {
  const tokenHash = hashToken(token)
  const now = clock.now()

  // Two queries, and the split is not an optimisation.
  //
  // The first runs with no tenant, because the tenant is what it is working
  // out. It can reach the session row by presenting the token hash, and the
  // user row because that user owns the session being presented. It cannot
  // reach anything else, which is the point.
  //
  // The second runs scoped to the organization the session named, so reading
  // the role goes through the ordinary tenant policy rather than through a
  // special case. The alternative was a policy letting an unauthenticated
  // connection read the members table, and there is no version of that which
  // is safe.
  const base = await pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{
        id: string
        user_id: string
        org_id: string | null
        expires_at: Date | string
        last_seen_at: Date | string
        revoked_at: Date | string | null
        github_login: string
        name: string | null
      }>(sql`
        SELECT s.id, s.user_id, s.org_id, s.expires_at, s.last_seen_at,
               s.revoked_at, u.github_login, u.name
        FROM sessions s
        JOIN users u ON u.id = s.user_id
        WHERE s.token_hash = ${tokenHash}`)

      const row = rows[0]
      if (!row) return null
      if (row.revoked_at) return null

      const expiresAt = asDate(row.expires_at)
      const lastSeen = asDate(row.last_seen_at)
      // Expiry is enforced here rather than left to a sweeper. A sweeper that
      // is behind is a window in which expired sessions work.
      if (expiresAt.getTime() <= now.getTime()) return null
      if (now.getTime() - lastSeen.getTime() > IDLE_TIMEOUT_MS) return null

      // Written back only when it has moved by more than a minute. Updating on
      // every request turns every read into a write and makes this the busiest
      // table in the database.
      if (now.getTime() - lastSeen.getTime() > 60_000) {
        await db.execute(sql`
          UPDATE sessions SET last_seen_at = ${now.toISOString()} WHERE id = ${row.id}`)
      }

      return {
        sessionId: row.id,
        userId: row.user_id,
        orgId: row.org_id,
        label: row.name || row.github_login,
        csrfToken: csrfTokenFor(token),
        expiresAt,
      }
    },
    { sessionHash: tokenHash },
  )

  if (!base) return null
  if (!base.orgId) return { ...base, orgSlug: null, role: null, plan: null }

  // Read on every request rather than carried in the session. Removing
  // somebody from an organization has to take effect on their next request,
  // not on their next sign in, and a role in a cookie takes effect whenever
  // they happen to sign in again, which may be never.
  // The role and the organization's name in one round trip, because they are
  // read on every single request and a second query for a label is a second
  // query on every page of the application.
  const scoped = await pool.withTenant({ orgId: base.orgId, userId: base.userId }, async (db) => {
    const rows = await db.execute<{ role: Role | null; slug: string | null; plan: string }>(sql`
      SELECT m.role, o.slug, o.plan
      FROM organizations o
      LEFT JOIN members m ON m.org_id = o.id AND m.user_id = ${base.userId}
      WHERE o.id = ${base.orgId}`)
    return {
      role: rows[0]?.role ?? null,
      slug: rows[0]?.slug ?? null,
      plan: rows[0]?.plan ?? null,
    }
  })

  return { ...base, role: scoped.role, orgSlug: scoped.slug, plan: scoped.plan }
}

export async function revokeSession(pool: Pool, token: string): Promise<void> {
  const tokenHash = hashToken(token)
  await pool.withoutTenant(
    async (db) => {
      await db.execute(sql`DELETE FROM sessions WHERE token_hash = ${tokenHash}`)
    },
    { sessionHash: tokenHash },
  )
}

/**
 * Removes expired sessions. Housekeeping, not enforcement.
 *
 * It ran through withoutTenant until 0024 and deleted nothing, on every
 * instance, for as long as it existed. Every policy on this table keys on the
 * acting user, on the hash of a presented token, or on the tenant, and a
 * sweeper has none of the three, so the DELETE matched no row and reported
 * success. A statement that matches nothing does not raise.
 *
 * withSessionSweeper enters a role of its own for the length of this
 * transaction. The policy admitting that role restricts it to rows already
 * expired by the DATABASE's clock, and the WHERE below restricts it to rows
 * expired by the APPLICATION's. A row has to be past both to be deleted, so
 * the cutoff passed from here can only ever narrow what is removed, never
 * widen it: this cannot reach a live session even if the clock it is given is
 * wrong.
 *
 * The count comes from RETURNING a constant rather than a column, because the
 * sweeper is granted SELECT on expires_at and on nothing else. Returning id
 * would be refused, which is the right refusal in the wrong place.
 */
export async function sweepSessions(pool: Pool, clock: Clock): Promise<number> {
  return pool.withSessionSweeper(async (db) => {
    const rows = await db.execute<{ n: string }>(sql`
      WITH gone AS (
        DELETE FROM sessions WHERE expires_at <= ${clock.now().toISOString()} RETURNING 1
      ) SELECT count(*) AS n FROM gone`)
    return Number(rows[0]?.n ?? 0)
  })
}

/**
 * The token a mutating request has to present.
 *
 * Derived from the session token rather than stored. Holding this does not
 * reveal the session token, which is what makes it safe to give to the page,
 * and deriving it means there is nothing to invalidate when a session rotates.
 */
export function csrfTokenFor(sessionToken: string): string {
  return createHmac('sha256', sessionToken).update('csrf').digest('base64url')
}

export function csrfMatches(sessionToken: string, presented: string | undefined | null): boolean {
  if (!presented) return false
  const expected = Buffer.from(csrfTokenFor(sessionToken), 'utf8')
  const actual = Buffer.from(presented, 'utf8')
  // Length is compared first because timingSafeEqual throws on a mismatch, and
  // the length of a token is not a secret.
  if (expected.length !== actual.length) return false
  return timingSafeEqual(expected, actual)
}

function asDate(v: Date | string): Date {
  return v instanceof Date ? v : new Date(v)
}

/** The Set-Cookie value for a session. */
export function sessionCookie(token: string, expiresAt: Date, secure: boolean): string {
  const parts = [
    `${SESSION_COOKIE}=${token}`,
    'Path=/',
    'HttpOnly',
    // Lax rather than Strict, because Strict means arriving from a link in a
    // pull request comment lands you signed out, which is the single most
    // common way somebody opens this application.
    'SameSite=Lax',
    `Expires=${expiresAt.toUTCString()}`,
  ]
  if (secure) parts.push('Secure')
  return parts.join('; ')
}

export function clearedCookie(secure: boolean): string {
  const parts = [`${SESSION_COOKIE}=`, 'Path=/', 'HttpOnly', 'SameSite=Lax', 'Max-Age=0']
  if (secure) parts.push('Secure')
  return parts.join('; ')
}

/** Reads one cookie out of a Cookie header without a dependency. */
export function readCookie(header: string | null | undefined, name: string): string | null {
  if (!header) return null
  for (const part of header.split(';')) {
    const eq = part.indexOf('=')
    if (eq < 0) continue
    if (part.slice(0, eq).trim() === name) return part.slice(eq + 1).trim()
  }
  return null
}

export { eq, and, schema }
export type { Db }
