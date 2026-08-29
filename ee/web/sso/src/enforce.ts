// Requiring single sign-on, and the way back in when that goes wrong.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Enforcement is the point of buying this. An organization that has connected
// its identity provider but left GitHub sign-in working has bought a
// convenience, not a control: somebody removed from the directory on their last
// day still holds a GitHub account and still gets in.
//
// It is also the single most dangerous switch in the product, because the
// failure mode is total and self-inflicted. An administrator pastes metadata
// with a typo, turns on enforcement, signs out, and now nobody at that company
// can reach their own control plane, including the people who could fix it.
// There is no self-service recovery from that and a support ticket at a weekend
// is not an answer.
//
// So enforcement always ships with recovery codes, and this file is written so
// the two cannot be separated: turning enforcement on is what generates them,
// and the caller is handed them exactly once.
//
// The recovery path is deliberately NOT a second way to authenticate. It is a
// decision not to apply enforcement to a sign-in that has already happened:
// GitHub establishes who the person is, the policy leaves them signed in with
// no tenant, and the code is presented from that state. That is what keeps this
// from needing an unauthenticated lookup keyed on a six-word code, which would
// be a guessable secret reachable by anybody on the internet.

import { createHash, randomBytes, timingSafeEqual } from 'node:crypto'
import type { Pool } from '@antifailure/db'
import { appendAudit, sql } from '@antifailure/db'
import type { SignInAttempt, SignInDecision } from '@antifailure/api'

export class BreakGlassRefused extends Error {}

/** How many codes are issued when enforcement is turned on. */
export const RECOVERY_CODE_COUNT = 10

export function hashCode(code: string): Buffer {
  // Normalised first, so that the dashes and the case a person types make no
  // difference. A recovery code is read off paper at a bad moment.
  return createHash('sha256').update(normalise(code), 'utf8').digest()
}

function normalise(code: string): string {
  return code.replace(/[\s-]/g, '').toUpperCase()
}

/**
 * Generates a recovery code.
 *
 * Crockford's alphabet without I, L, O and U: no character can be confused with
 * another when read aloud or copied off a printout, and none of the four spells
 * anything unfortunate. Twenty characters is a hundred bits.
 */
export function generateCode(): string {
  const alphabet = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'
  const bytes = randomBytes(20)
  const body = [...bytes].map((b) => alphabet[b % alphabet.length]).join('')
  return `${body.slice(0, 5)}-${body.slice(5, 10)}-${body.slice(10, 15)}-${body.slice(15, 20)}`
}

export interface EnforcementInput {
  pool: Pool
  orgId: string
  connectionId: string
  actorUserId: string
  actorLabel: string
  now: Date
}

/**
 * Turns enforcement on and returns the recovery codes, once.
 *
 * The codes are returned rather than stored in the clear and they are never
 * retrievable again, which is the same reason session tokens are hashed. An
 * administrator who loses them turns enforcement off and on again from a
 * session that still works, which is a nuisance; an administrator who could
 * read them back from the database would mean a leaked backup is a set of keys
 * around enforcement.
 *
 * Enforcement is refused unless the connection is enabled. Requiring a provider
 * that is switched off is how an organization locks itself out in one click,
 * and the check costs nothing.
 */
export async function enforce(input: EnforcementInput): Promise<{ codes: string[] }> {
  const codes = Array.from({ length: RECOVERY_CODE_COUNT }, generateCode)

  await input.pool.withTenant({ orgId: input.orgId, userId: input.actorUserId }, async (db) => {
    const rows = await db.execute<{ enabled: boolean; display_name: string }>(sql`
      SELECT enabled, display_name FROM sso_connections
      WHERE id = ${input.connectionId} AND org_id = ${input.orgId}`)
    const connection = rows[0]
    if (!connection) throw new BreakGlassRefused('There is no such connection.')
    if (!connection.enabled) {
      throw new BreakGlassRefused(
        'This connection is not enabled, so requiring it would lock every member out ' +
          'immediately. Enable it and sign in through it once first.',
      )
    }

    // Codes from a previous enforcement are removed. Leaving them would mean a
    // code printed a year ago and since forgotten still works, and the set an
    // administrator is holding right now is the only set that should.
    await db.execute(sql`DELETE FROM sso_break_glass_codes WHERE org_id = ${input.orgId}`)
    for (const code of codes) {
      await db.execute(sql`
        INSERT INTO sso_break_glass_codes (org_id, code_hash) VALUES (${input.orgId}, ${hashCode(code)})`)
    }

    await db.execute(sql`
      UPDATE sso_connections SET enforced = true, updated_at = ${input.now.toISOString()}
      WHERE id = ${input.connectionId} AND org_id = ${input.orgId}`)

    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.actorUserId,
      actorLabel: input.actorLabel,
      action: 'sso.enforcement.enabled',
      targetType: 'sso_connection',
      targetId: input.connectionId,
      origin: 'web',
      detail: { connection: connection.display_name, recoveryCodesIssued: codes.length },
      occurredAt: input.now,
    })
  })

  return { codes }
}

/** Turns enforcement off. The codes go with it: they protect nothing now. */
export async function relax(input: EnforcementInput): Promise<void> {
  await input.pool.withTenant({ orgId: input.orgId, userId: input.actorUserId }, async (db) => {
    await db.execute(sql`
      UPDATE sso_connections SET enforced = false, updated_at = ${input.now.toISOString()}
      WHERE id = ${input.connectionId} AND org_id = ${input.orgId}`)
    await db.execute(sql`DELETE FROM sso_break_glass_codes WHERE org_id = ${input.orgId}`)
    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.actorUserId,
      actorLabel: input.actorLabel,
      action: 'sso.enforcement.disabled',
      targetType: 'sso_connection',
      targetId: input.connectionId,
      origin: 'web',
      detail: {},
      occurredAt: input.now,
    })
  })
}

/** Whether an organization requires single sign-on right now. */
export async function isEnforced(pool: Pool, orgId: string): Promise<boolean> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{ n: string }>(sql`
      SELECT count(*) AS n FROM sso_connections
      WHERE org_id = ${orgId} AND enabled AND enforced`),
  )
  return Number(rows[0]?.n ?? 0) > 0
}

/**
 * The policy the community sign-in path consults.
 *
 * Returns null for the organization when it requires single sign-on, which
 * leaves the person signed in with no tenant rather than refused outright. That
 * is the state the recovery path starts from, and it is also simply the honest
 * answer: GitHub proved who they are, and this organization does not accept
 * that as a way in.
 */
export function signInPolicy(pool: Pool): (attempt: SignInAttempt) => Promise<SignInDecision> {
  return async (attempt) => {
    if (!attempt.orgId) return { orgId: null }
    if (!(await isEnforced(pool, attempt.orgId))) return { orgId: attempt.orgId }
    return { orgId: null, note: 'sso_required' }
  }
}

export interface BreakGlassInput {
  pool: Pool
  orgId: string
  userId: string
  code: string
  now: Date
  ip?: string | null
  userAgent?: string | null
}

/**
 * Spends a recovery code, and says whether it was good.
 *
 * Three things have to be true and each is checked here rather than by the
 * caller, because a caller that forgets one produces a hole with no symptom.
 *
 * The person must already be authenticated. This runs with their user id,
 * established by a completed GitHub sign-in.
 *
 * They must be an OWNER of the organization. A member with a recovery code
 * would be able to walk around enforcement for themselves, which is most of
 * what enforcement is for. The specification says owner and it means it.
 *
 * The code must be unused. The UPDATE that spends it carries the used_at IS
 * NULL condition, so two requests presenting the same code cannot both succeed:
 * the second updates no rows. Checking first and updating after would let both
 * through, which turns a single-use code into a reusable one for as long as
 * somebody keeps trying.
 */
export async function spendRecoveryCode(input: BreakGlassInput): Promise<void> {
  const normalised = normalise(input.code)
  if (normalised.length !== 20) {
    // The same message as a wrong code. A different one tells somebody
    // guessing which of their attempts had the right shape.
    throw new BreakGlassRefused('That recovery code is not valid.')
  }

  await input.pool.withTenant({ orgId: input.orgId, userId: input.userId }, async (db) => {
    const membership = await db.execute<{ role: string }>(sql`
      SELECT role::text AS role FROM members
      WHERE org_id = ${input.orgId} AND user_id = ${input.userId}`)
    if (membership[0]?.role !== 'owner') {
      throw new BreakGlassRefused(
        'Only an owner of this organization can use a recovery code. Ask an owner to sign in ' +
          'through the identity provider, or to use one of theirs.',
      )
    }

    const spent = await db.execute<{ id: string }>(sql`
      UPDATE sso_break_glass_codes
      SET used_at = ${input.now.toISOString()}, used_by = ${input.userId}
      WHERE org_id = ${input.orgId} AND code_hash = ${hashCode(input.code)} AND used_at IS NULL
      RETURNING id`)

    if (spent.length === 0) {
      throw new BreakGlassRefused('That recovery code is not valid, or has already been used.')
    }

    const left = await db.execute<{ n: string }>(sql`
      SELECT count(*) AS n FROM sso_break_glass_codes
      WHERE org_id = ${input.orgId} AND used_at IS NULL`)

    // Loudly. A break-glass login is the event a security review looks for and
    // it should be impossible to perform one quietly.
    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.userId,
      actorLabel: 'break-glass',
      action: 'sso.break_glass.used',
      targetType: 'organization',
      targetId: input.orgId,
      origin: 'web',
      detail: {
        remainingCodes: Number(left[0]?.n ?? 0),
        ip: input.ip ?? null,
        userAgent: input.userAgent ?? null,
      },
      occurredAt: input.now,
    })
  })
}

/** Constant-time comparison, for callers holding two hashes. */
export function hashesMatch(a: Buffer, b: Buffer): boolean {
  if (a.length !== b.length) return false
  return timingSafeEqual(a, b)
}
