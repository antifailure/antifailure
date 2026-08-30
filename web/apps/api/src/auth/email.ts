// Signing in with a link, for the places GitHub cannot reach.
//
// Read migrations/0012 beside this: the statements there are what enforce the
// three properties, and this file is the flow that uses them.
//
// The property that shapes every function here is that the caller learns
// nothing. `begin` answers the same way for an address with an account, an
// address without one, and an address that is not an address at all, and it
// answers before the mail is sent so the two cases do not differ in timing
// either. Anything else turns the sign-in form into a way to ask whether
// somebody works here.

import { randomBytes, createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Mailer } from './mail.ts'
import { safeRedirect } from './signin.ts'

/** How long somebody has to open the link. */
export const LINK_TTL_MS = 15 * 60 * 1000

export class EmailSignInError extends Error {}

/** What the flow needs to run. Absent means the path is off. */
export interface EmailSignInConfig {
  readonly mailer: Mailer
  /** Where the link points, without a trailing slash: the address a browser
   *  reaches this API on. */
  readonly baseUrl: string
  /** The product name in the subject line. */
  readonly productName?: string
}

export function hashLinkToken(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

/**
 * Issues a link, when the address belongs to somebody.
 *
 * Returns what to say to the caller, which is the same sentence in every case,
 * and the send to perform afterwards, which is null when there is nothing to
 * send. Splitting them is what keeps the response independent of the answer:
 * the handler replies first and sends second.
 */
export async function beginEmailSignIn(
  pool: Pool,
  clock: Clock,
  config: EmailSignInConfig,
  input: {
    email: string
    redirectTo?: string | null
    ip?: string | null
    userAgent?: string | null
  },
): Promise<{ send: (() => Promise<void>) | null }> {
  const address = normalise(input.email)
  if (!address) return { send: null }

  const known = await pool.withoutTenant(async (db) => {
    const rows = await db.execute<{ known: boolean }>(sql`
      SELECT email_signin_candidate(${address}) AS known`)
    return rows[0]?.known === true
  })
  if (!known) {
    // No row, no mail, and the caller cannot tell. There is deliberately no
    // sign-up here: an address becomes able to sign in by being invited into
    // an organization, never by asking for a link.
    return { send: null }
  }

  const token = randomBytes(32).toString('base64url')
  const tokenHash = hashLinkToken(token)
  const expiresAt = new Date(clock.now().getTime() + LINK_TTL_MS)

  await pool.withoutTenant(
    async (db) => {
      await db.execute(sql`
        INSERT INTO email_signin_tokens
          (token_hash, email, redirect_to, ip, user_agent, created_at, expires_at)
        VALUES (${tokenHash}, ${address}, ${safeRedirect(input.redirectTo) ?? null},
                ${input.ip ?? null}, ${input.userAgent ?? null},
                ${clock.now().toISOString()}, ${expiresAt.toISOString()})`)
    },
    { emailTokenHash: tokenHash },
  )

  const link = `${config.baseUrl.replace(/\/+$/, '')}/auth/email/callback?token=${encodeURIComponent(token)}`
  const product = config.productName ?? 'Antifailure'
  return {
    send: () =>
      config.mailer.send({
        to: address,
        subject: `Sign in to ${product}`,
        text: bodyText(product, link),
        html: bodyHtml(product, link),
      }),
  }
}

export interface RedeemedLink {
  userId: string
  /** The organization the session lands in, when the user is in exactly one. */
  orgId: string | null
  redirectTo: string | null
  label: string
  email: string
}

/**
 * Redeems a link.
 *
 * The order is deliberate. The user row is read while the token is still
 * unconsumed, because the policy that makes that row reachable requires it, and
 * then the token is consumed by an UPDATE that carries its own precondition. So
 * two browsers opening the same link race on one statement and exactly one of
 * them wins, rather than both passing a check the application made a moment
 * before it wrote.
 */
export async function redeemEmailSignIn(
  pool: Pool,
  clock: Clock,
  token: string,
): Promise<RedeemedLink> {
  const tokenHash = hashLinkToken(token)
  const now = clock.now().toISOString()

  const found = await pool.withoutTenant(
    async (db) => {
      const tokens = await db.execute<{ email: string; redirect_to: string | null }>(sql`
        SELECT email, redirect_to FROM email_signin_tokens
        WHERE token_hash = ${tokenHash} AND consumed_at IS NULL AND expires_at > ${now}`)
      const row = tokens[0]
      if (!row) return null

      const users = await db.execute<{ id: string; github_login: string; name: string | null; email: string }>(sql`
        SELECT id, github_login, name, email FROM users WHERE lower(email) = ${row.email}`)
      const user = users[0]
      if (!user) return null

      // The precondition is in the statement, not above it. This is the only
      // thing that makes the link single use.
      const consumed = await db.execute<{ id: string }>(sql`
        UPDATE email_signin_tokens SET consumed_at = ${now}
        WHERE token_hash = ${tokenHash} AND consumed_at IS NULL AND expires_at > ${now}
        RETURNING id`)
      if (consumed.length === 0) return null

      return {
        userId: user.id,
        redirectTo: safeRedirect(row.redirect_to),
        label: user.name || user.github_login,
        email: user.email,
      }
    },
    { emailTokenHash: tokenHash },
  )

  if (!found) {
    // One message for "never issued", "already used", and "expired", for the
    // same reason the OAuth path uses one: telling them apart tells somebody
    // grinding tokens which of their guesses was once real.
    throw new EmailSignInError('This sign-in link is no longer valid. Ask for another one.')
  }

  const orgId = await pool.withoutTenant(
    async (db) => {
      const memberships = await db.execute<{ org_id: string }>(sql`
        SELECT org_id FROM members WHERE user_id = ${found.userId} ORDER BY created_at ASC`)
      // Chosen only when there is exactly one, the same rule the OAuth path
      // uses. Guessing puts somebody in the wrong tenant, where every page is
      // empty for no visible reason.
      return memberships.length === 1 ? memberships[0]!.org_id : null
    },
    { signinUserId: found.userId },
  )

  return { ...found, orgId }
}

/**
 * Removes links that can no longer be used, and returns how many.
 *
 * Housekeeping rather than enforcement: expiry and single use are both decided
 * by the statements above, so a sweep that is late costs table size only. It
 * goes through a definer function because no policy matches a row whose token
 * the caller does not hold, and a policy that let the application role reach
 * every row is the thing migrations/0012 is arranged to avoid.
 */
export async function sweepEmailSignInTokens(pool: Pool, clock: Clock): Promise<number> {
  return pool.withoutTenant(async (db) => {
    const rows = await db.execute<{ n: string }>(sql`
      SELECT email_signin_sweep(${clock.now().toISOString()}::timestamptz) AS n`)
    return Number(rows[0]?.n ?? 0)
  })
}

function normalise(value: string): string | null {
  const trimmed = value.trim().toLowerCase()
  if (trimmed.length === 0 || trimmed.length > 320) return null
  // Deliberately not a full address grammar. The only thing that matters is
  // that it cannot be a header injection and that it has the shape of an
  // address; whether it exists is answered by the database.
  if (!/^[^\s@,;<>"]+@[^\s@,;<>"]+\.[^\s@,;<>"]+$/.test(trimmed)) return null
  return trimmed
}

function bodyText(product: string, link: string): string {
  return [
    `Open this link to sign in to ${product}:`,
    '',
    link,
    '',
    'It works once and expires in 15 minutes.',
    'If you did not ask for it, nothing has happened and you can ignore this.',
  ].join('\n')
}

function bodyHtml(product: string, link: string): string {
  const safe = escapeHtml(link)
  return [
    `<p>Open this link to sign in to ${escapeHtml(product)}:</p>`,
    `<p><a href="${safe}">${safe}</a></p>`,
    '<p>It works once and expires in 15 minutes.</p>',
    '<p>If you did not ask for it, nothing has happened and you can ignore this.</p>',
  ].join('\n')
}

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
}
