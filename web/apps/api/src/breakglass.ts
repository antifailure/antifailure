// The way back in when nobody can sign in.
//
// Everything about access to this control plane derives from GitHub. Who may
// sign in is an allowlist of GitHub logins, whether a person is a member comes
// from a GitHub App installation, and what role they get is read from GitHub
// with an installation token. That is the right shape and it has one hole: it
// has no floor. Delete the App, rotate the OAuth client secret into the wrong
// variable, or lose the private key, and nobody can sign in, nobody can be made
// an owner, and the control plane holds the only record of who was.
//
// WHY THIS IS NOT THE MECHANISM THE ENTERPRISE EDITION ALREADY HAS. Single
// sign-on has `sso_break_glass_codes`, and it does not generalise to this,
// because it is not a way in. It is a decision not to apply enforcement to a
// sign-in THAT HAS ALREADY HAPPENED through GitHub: the person is holding a
// session, the code is spent from it, and `spendRecoveryCode` requires them to
// already be an owner. Every one of those preconditions is exactly what is
// missing here. Reusing it would have meant inventing an unauthenticated lookup
// keyed on a guessable code, which is the design migration 0014 explicitly
// rejected, and it would have put a second way into the product for a fault
// that happens outside it.
//
// So this is not a login and grants no session. It is an operator, holding the
// database credential, writing one membership row and the audit entry that says
// they did. What it buys over the psql they could have run instead is precisely
// that entry: the audit chain records the break-glass, and a repair made by
// hand records nothing.
//
// Two limits worth stating, because they bound what a leaked credential does
// with this. It cannot create a user, so it can only promote somebody who has
// already signed in at least once; if nobody ever has, the fault is the OAuth
// configuration and this is not the tool. And it cannot leave an organization
// with no owner, which is the state it exists to get out of.

import { createPool, appendAudit, sql, type Pool } from '@antifailure/db'
import { ROLES, type Role } from './permissions.ts'

export class BreakGlassRefused extends Error {}

/** Reads a role off the command line, or says which words are roles. */
export function parseRole(value: string): Role {
  const found = ROLES.find((r) => r === value)
  if (!found) throw new BreakGlassRefused(`${value} is not a role. They are: ${ROLES.join(', ')}.`)
  return found
}

export interface BreakGlassInput {
  /** A connection string row-level security does not apply to. */
  adminUrl: string
  /** The organization's slug or its id. */
  org: string
  githubLogin: string
  role: Role
  /** Written into the audit entry. Required, and that is the deliberateness. */
  reason: string
  /** Reports what would change and writes nothing. */
  dryRun?: boolean
  /** Who ran it, for the audit entry. */
  operator?: string
  now?: Date
}

export interface BreakGlassResult {
  orgId: string
  orgSlug: string
  userId: string
  githubLogin: string
  /** The role they held, or null when they were not a member at all. */
  from: Role | null
  to: Role
  /** False for a dry run, and only for a dry run. */
  applied: boolean
  /** The audit entry's sequence number, so the operator can go and read it. */
  auditSeq: number | null
}

/**
 * Sets one person's role in one organization, through the migration role.
 *
 * The write is marked `manual` for the same reason the Members page marks its
 * writes that way: a repair that the next membership sync undoes is not a
 * repair. See syncMembership, which leaves a manual role alone.
 */
export async function breakGlass(input: BreakGlassInput): Promise<BreakGlassResult> {
  const reason = input.reason.trim()
  if (!reason) {
    throw new BreakGlassRefused(
      'A break-glass needs a reason. It goes into the audit entry, and an entry that says a ' +
        'break-glass happened without saying why is a question somebody has to answer later.',
    )
  }
  const login = input.githubLogin.trim()
  if (!login) throw new BreakGlassRefused('Name the GitHub account to act on.')

  const now = input.now ?? new Date()
  const pool = createPool({ url: input.adminUrl, max: 1, rowSecurity: false })
  try {
    await assertUnrestricted(pool)

    const org = await findOrganization(pool, input.org)
    const user = await findUser(pool, login)

    return await pool.withTenant({ orgId: org.id }, async (db) => {
      // The same lock sign-in takes, for the same reason: this reads a count of
      // owners and then decides from it, and a sign-in landing between the two
      // would make the decision stale.
      await db.execute(sql`SELECT pg_advisory_xact_lock(hashtext(${`members:${org.id}`}))`)

      const current = await db.execute<{ role: Role }>(sql`
        SELECT role::text AS role FROM members
        WHERE org_id = ${org.id} AND user_id = ${user.id}`)
      const from = current[0]?.role ?? null

      const owners = await db.execute<{ n: string }>(sql`
        SELECT count(*) AS n FROM members WHERE org_id = ${org.id} AND role = 'owner'`)
      // Refused before the write rather than repaired after it, exactly as
      // members.setRole refuses it over the API. An organization with no owner
      // cannot grant anybody the permission to become one, and this command is
      // what somebody reaches for having already discovered that.
      if (from === 'owner' && input.role !== 'owner' && Number(owners[0]?.n ?? 0) === 1) {
        throw new BreakGlassRefused(
          `${login} is the only owner of ${org.slug}. Make somebody else an owner first, or the ` +
            'organization is left with nobody who can manage members or billing, which is the ' +
            'state this command exists to get out of.',
        )
      }

      const result: BreakGlassResult = {
        orgId: org.id,
        orgSlug: org.slug,
        userId: user.id,
        githubLogin: user.login,
        from,
        to: input.role,
        applied: false,
        auditSeq: null,
      }
      if (input.dryRun) return result

      await db.execute(sql`
        INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
        VALUES (${org.id}, ${user.id}, ${input.role}, 'manual',
                ${now.toISOString()}, ${now.toISOString()})
        ON CONFLICT (org_id, user_id) DO UPDATE SET
          role = ${input.role}, source = 'manual', updated_at = ${now.toISOString()}`)

      // Written on every real run, including one that changed nothing. The fact
      // a security review is looking for is that somebody reached for this
      // command against this organization, and whether the row happened to move
      // is a detail inside it.
      const entry = await appendAudit(db, {
        orgId: org.id,
        // No user did this. The label is what survives, the same way the single
        // sign-on recovery path labels its entry.
        actorUserId: null,
        actorLabel: 'break-glass',
        action: 'member.break_glass',
        targetType: 'member',
        targetId: user.login,
        origin: 'operator',
        detail: {
          role: input.role,
          previousRole: from,
          added: from === null,
          changed: from !== input.role,
          reason,
          operator: input.operator ?? null,
        },
        occurredAt: now,
      })

      return { ...result, applied: true, auditSeq: entry.seq }
    })
  } finally {
    await pool.close()
  }
}

/**
 * Proves the connection can actually see the rows before anything depends on
 * it, and says what to do when it cannot.
 *
 * A probe rather than a look at `pg_roles`, because the question is not which
 * attributes the role carries, it is whether the policies apply to it, and that
 * depends on FORCE, on policy membership and on who owns the table. The probe
 * answers it directly. With `row_security = off` set on the pool, a connection
 * the policies do apply to raises here instead of quietly reading nothing.
 */
async function assertUnrestricted(pool: Pool): Promise<void> {
  try {
    await pool.withoutTenant((db) => db.execute(sql`SELECT 1 FROM members LIMIT 1`))
  } catch (error) {
    throw new BreakGlassRefused(
      `This connection cannot read the members table: ${reasonFor(error)}\n\n` +
        'Every tenant table is FORCE ROW LEVEL SECURITY, so the role that owns the schema is ' +
        'subject to the policies like anybody else, and the policies name only antifailure_app. ' +
        'Use a connection row-level security does not apply to: the cluster superuser, or a role ' +
        'with BYPASSRLS.',
    )
  }
}

/**
 * The database's own words, not the driver's.
 *
 * drizzle wraps a failure as "Failed query: <sql>" and hangs the server's
 * message off `cause`. The server's is the one that says "query would be
 * affected by row-level security policy for table members", which is the whole
 * diagnosis, and the wrapper's says nothing at all.
 */
function reasonFor(error: unknown): string {
  const cause = (error as { cause?: unknown }).cause
  if (cause instanceof Error && cause.message) return cause.message
  return error instanceof Error ? error.message : String(error)
}

async function findOrganization(pool: Pool, needle: string): Promise<{ id: string; slug: string }> {
  // id::text rather than a cast of the argument. Casting a slug to uuid raises
  // a Postgres error about the input syntax, which reads as a broken command
  // rather than as "that organization does not exist".
  const rows = await pool.withoutTenant((db) =>
    db.execute<{ id: string; slug: string }>(sql`
      SELECT id, slug FROM organizations WHERE slug = ${needle} OR id::text = ${needle}`),
  )
  if (rows.length === 0) {
    throw new BreakGlassRefused(`There is no organization with the slug or id ${needle}.`)
  }
  return { id: rows[0]!.id, slug: rows[0]!.slug }
}

async function findUser(pool: Pool, login: string): Promise<{ id: string; login: string }> {
  // Matched case-insensitively, because GitHub logins are, and typed at three
  // in the morning by somebody reading them off a screen.
  //
  // github_login is not unique: the unique identity is github_id, and a login
  // freed by a rename can be taken by somebody else. Several matches is
  // therefore a real state and it is refused rather than resolved by picking
  // one, because picking one here is picking which person gets the role.
  const rows = await pool.withoutTenant((db) =>
    db.execute<{ id: string; github_login: string; github_id: string }>(sql`
      SELECT id, github_login, github_id::text AS github_id FROM users
      WHERE lower(github_login) = lower(${login}) ORDER BY created_at ASC`),
  )
  if (rows.length === 0) {
    throw new BreakGlassRefused(
      `No account here has the GitHub login ${login}. This command cannot create one: it can only ` +
        'give a role to somebody who has signed in at least once. If nobody ever has, what is ' +
        'broken is the OAuth configuration and no database write will fix it.',
    )
  }
  if (rows.length > 1) {
    const ids = rows.map((r) => `${r.github_login} (github id ${r.github_id})`).join(', ')
    throw new BreakGlassRefused(
      `${login} matches more than one account: ${ids}. A GitHub login can be renamed and reclaimed, ` +
        'so these are different people. Resolve which one you mean before granting anything.',
    )
  }
  return { id: rows[0]!.id, login: rows[0]!.github_login }
}
