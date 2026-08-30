// The sign-in exchange, and organization membership.
//
// The two rules that decide the shape of this file:
//
// A user is identified by their GitHub numeric id, never by their login or
// their email address. A login can be renamed and then claimed by somebody
// else, and an email address can be moved between accounts. The id is the only
// identifier GitHub promises is stable, and matching on anything else is how an
// account gets handed to the wrong person.
//
// Membership is derived from GitHub, not stored independently. Somebody removed
// from the GitHub organization loses access on the next sync or the next
// webhook, whichever is first, and nobody has to remember to remove them here
// as well.

import { randomBytes } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { GitHubClient, GitHubUser } from './github.ts'
import type { Role } from '../permissions.ts'

/** How long the browser has to come back from GitHub. */
export const OAUTH_STATE_TTL_MS = 10 * 60 * 1000

export class SignInError extends Error {}

/**
 * Who may sign in at all.
 *
 * Null means anyone GitHub authenticates, which is the right default for a
 * self-hosted installation: the operator already decided who reaches the
 * instance, and a second list to maintain is a second thing to get wrong.
 *
 * A list means exactly those GitHub logins and nobody else. That is what the
 * hosted staging deployment runs with while signups are closed, and it is
 * enforced BEFORE any row is written, so a refused person leaves no user
 * record, no session, and nothing to clean up.
 *
 * The distinction that matters: refusing here is not the same as the
 * membership check further down. Somebody not on this list cannot sign in.
 * Somebody on it signs in and still sees nothing until an installation exists
 * for one of their organizations. Both are needed, because the first is a
 * closed door and the second is what makes an open one safe.
 */
export type SignInAllowlist = ReadonlySet<string> | null

/**
 * Reads the allowlist from a comma or whitespace separated string.
 *
 * An unset variable is null, meaning open. A variable set to something that
 * contains no logins at all is NOT open: it is a list of nobody, and it
 * refuses everyone. That asymmetry is deliberate. `AF_SIGNIN_ALLOWLIST=""` is
 * far more likely to be a broken deployment script than a considered decision
 * to let the world in, and the safe reading of an ambiguous configuration is
 * the closed one.
 */
export function parseAllowlist(value: string | undefined | null): SignInAllowlist {
  if (value === undefined || value === null) return null
  const logins = value
    .split(/[,\s]+/)
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean)
  return new Set(logins)
}

/** What the process should say at startup, so the mode is never a guess. */
export function describeAllowlist(list: SignInAllowlist): string {
  if (list === null) {
    return 'sign-in is OPEN: any GitHub account may sign in (AF_SIGNIN_ALLOWLIST is not set)'
  }
  if (list.size === 0) {
    return 'sign-in is CLOSED TO EVERYONE: AF_SIGNIN_ALLOWLIST is set but names nobody'
  }
  return `sign-in is restricted to ${list.size} account(s) by AF_SIGNIN_ALLOWLIST`
}

/**
 * Starts the exchange: records a state value and returns where to send the
 * browser.
 *
 * The state is stored rather than signed into a cookie so that it can be
 * deleted on use. A signed cookie proves the value was issued by us and cannot
 * prove it has not already been redeemed, and a replayable callback is a
 * session fixation primitive.
 */
export async function beginSignIn(
  pool: Pool,
  clock: Clock,
  github: GitHubClient,
  redirectTo?: string,
): Promise<{ url: string; state: string }> {
  const state = randomBytes(32).toString('base64url')
  const expiresAt = new Date(clock.now().getTime() + OAUTH_STATE_TTL_MS)
  await pool.withoutTenant(async (db) => {
    await db.execute(sql`
      INSERT INTO oauth_states (state, redirect_to, created_at, expires_at)
      VALUES (${state}, ${safeRedirect(redirectTo)}, ${clock.now().toISOString()}, ${expiresAt.toISOString()})`)
  })
  return { url: github.authorizeUrl(state), state }
}

/**
 * Only a path on this application is accepted as a return target.
 *
 * An absolute URL here is an open redirect, and an open redirect on the
 * sign-in path is the one that matters: it is the link people are trained to
 * click, and it arrives carrying a session.
 */
export function safeRedirect(value: string | undefined | null): string | null {
  if (!value) return null
  if (!value.startsWith('/')) return null
  // Protocol-relative: "//evil.test" is an absolute URL that begins with a
  // slash, and it is the form that gets past a naive check.
  if (value.startsWith('//')) return null
  if (value.includes('\\')) return null
  return value
}

export interface CompletedSignIn {
  userId: string
  /** The organization the session lands in, when the user is in exactly one. */
  orgId: string | null
  redirectTo: string | null
  label: string
}

/**
 * Completes the exchange.
 *
 * Consuming the state and exchanging the code both happen before anything is
 * written, so a replayed callback cannot create a user or a session.
 */
export async function completeSignIn(
  pool: Pool,
  clock: Clock,
  github: GitHubClient,
  input: { code: string; state: string },
  allowlist: SignInAllowlist = null,
): Promise<CompletedSignIn> {
  const consumed = await pool.withoutTenant(async (db) => {
    // Deleted and returned in one statement, so two callbacks racing on the
    // same state cannot both find it.
    const rows = await db.execute<{ redirect_to: string | null; expires_at: Date | string }>(sql`
      DELETE FROM oauth_states WHERE state = ${input.state}
      RETURNING redirect_to, expires_at`)
    return rows[0] ?? null
  })

  if (!consumed) {
    // One message for "never issued", "already used", and "expired". Telling
    // them apart tells an attacker probing state values which of their guesses
    // was once real.
    throw new SignInError('This sign-in link is no longer valid. Start again.')
  }
  const expiresAt = consumed.expires_at instanceof Date ? consumed.expires_at : new Date(consumed.expires_at)
  if (expiresAt.getTime() <= clock.now().getTime()) {
    throw new SignInError('This sign-in link is no longer valid. Start again.')
  }

  const { user, accessToken } = await github.exchangeCode(input.code)

  // Before the user row, before the membership lookup, before anything is
  // written. A refused sign-in must leave nothing behind: no account to find
  // later, no row that makes it look as though somebody nearly got in.
  //
  // Checked on the GitHub numeric id's login rather than on the email address,
  // for the same reason the rest of this file matches on the id: an address can
  // be moved between accounts.
  if (allowlist !== null && !allowlist.has(user.login.toLowerCase())) {
    throw new SignInError(
      'This installation is not open for sign-ups. Ask an owner to add your ' +
        'GitHub account to the allowlist.',
    )
  }

  const orgs = await github.organizationsFor(accessToken)

  // The id being upserted is declared so that the row can be read back. See
  // migrations/0006: writing to the users table is allowed, reading it is not,
  // and an upsert needs both.
  // Three declarations, because sign-in is the one operation that legitimately
  // spans tenants: deciding which organizations somebody may enter cannot be
  // done from inside one of them. Each value here came from GitHub moments ago
  // for this person, and each policy returns only the rows it names.
  const userId = await pool.withoutTenant(
    (db) => upsertUser(db, clock, user),
    { githubIds: [user.id] },
  )

  const logins = orgs.map((o) => o.login.toLowerCase())

  return pool.withoutTenant(
    async (db) => {
      // Membership is granted only where an installation exists. Belonging to a
      // GitHub organization is not by itself a reason to see another company's
      // environments: somebody has to have installed the App for it first.
      //
      // sql.param, not a bare array. An array interpolated directly becomes a
      // record of one parameter per element, which is a tuple rather than an
      // array, and the mistake reads as correct in every language this is
      // written in.
      const installed = logins.length
        ? await db.execute<{ org_id: string; installation_id: string; account_login: string }>(sql`
            SELECT org_id, installation_id, account_login FROM github_installations
            WHERE lower(account_login) = ANY(${sql.param(logins)}::text[])
              AND suspended_at IS NULL`)
        : []

      for (const row of installed) {
        // The role comes from GitHub rather than from a constant here.
        //
        // This used to write 'member' for everybody, which is wrong in the one
        // case that matters most: the person who installed the App, on the
        // organization they own, arrives as a plain member and cannot manage
        // members, mint engine tokens, export the audit log, approve a masking
        // or egress change, or store a provider key. Their own control plane
        // refuses them, and the only way out was a database console.
        //
        // An organization owner becomes an admin rather than an owner, for the
        // reason syncMembership gives further down: owner also holds billing,
        // and that is this application's decision rather than GitHub's.
        const upstream = await github
          .roleIn(Number(row.installation_id), row.account_login, user.login)
          .catch(() => null)
        const role: Role = upstream === 'admin' ? 'admin' : 'member'

        await db.execute(sql`
          INSERT INTO members (org_id, user_id, role, source)
          VALUES (${row.org_id}, ${userId}, ${role}, 'github')
          ON CONFLICT (org_id, user_id) DO UPDATE SET
            -- Two things this must not do. It must not overwrite a role set by
            -- hand in this application, which is what source = 'manual' marks.
            -- And it must not act on a role GitHub declined to report: null is
            -- "ask again later", not "demote them", or a rate limit during
            -- sign-in would quietly strip an administrator of their rights.
            role = CASE
              WHEN members.source = 'github' AND ${upstream !== null}
                THEN ${role}
              ELSE members.role
            END,
            updated_at = ${clock.now().toISOString()}`)
      }

      // Memberships that already existed count too: an installation may have
      // been set up while this person was signed in, or by somebody else.
      const memberships = await db.execute<{ org_id: string }>(sql`
        SELECT org_id FROM members WHERE user_id = ${userId} ORDER BY created_at ASC`)

      return {
        userId,
        // An organization is chosen only when there is exactly one. With
        // several, the caller shows a picker: guessing puts somebody in the
        // wrong tenant, where every page is empty for no visible reason.
        orgId: memberships.length === 1 ? memberships[0]!.org_id : null,
        redirectTo: consumed.redirect_to,
        label: user.name || user.login,
      }
    },
    { signinUserId: userId, githubLogins: logins },
  )
}

async function upsertUser(db: Db, clock: Clock, user: GitHubUser): Promise<string> {
  const rows = await db.execute<{ id: string }>(sql`
    INSERT INTO users (github_id, github_login, email, name, avatar_url, created_at, updated_at)
    VALUES (${user.id}, ${user.login}, ${user.email.toLowerCase()}, ${user.name},
            ${user.avatarUrl}, ${clock.now().toISOString()}, ${clock.now().toISOString()})
    ON CONFLICT (github_id) DO UPDATE SET
      github_login = EXCLUDED.github_login,
      email = EXCLUDED.email,
      name = EXCLUDED.name,
      avatar_url = EXCLUDED.avatar_url,
      updated_at = EXCLUDED.updated_at
    RETURNING id`)
  return rows[0]!.id
}

export interface SyncReport {
  added: string[]
  removed: string[]
  changed: { login: string; from: Role; to: Role }[]
}

/**
 * Brings an organization's membership in line with GitHub's.
 *
 * Removals happen here, and they are the reason this function refuses an empty
 * member list. A GitHub outage that answers with an empty array would otherwise
 * remove every member of the organization, including the owner, and nobody
 * could sign in to undo it.
 */
export async function syncMembership(
  pool: Pool,
  clock: Clock,
  github: GitHubClient,
  input: { orgId: string; installationId: number; orgLogin: string; actorLabel: string },
): Promise<SyncReport> {
  const upstream = await github.membersOf(input.installationId, input.orgLogin)
  if (upstream.length === 0) {
    throw new SignInError(
      `GitHub reported no members for ${input.orgLogin}. Refusing to sync, because applying that ` +
        `would remove everyone including the owners, and an outage looks exactly like this.`,
    )
  }

  return pool.withTenant({ orgId: input.orgId }, async (db) => {
    const current = await db.execute<{ user_id: string; github_login: string; role: Role; source: string }>(sql`
      SELECT m.user_id, u.github_login, m.role, m.source
      FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${input.orgId}`)

    const report: SyncReport = { added: [], removed: [], changed: [] }
    const byLogin = new Map(current.map((m) => [m.github_login.toLowerCase(), m]))
    const seen = new Set<string>()

    for (const { user, role } of upstream) {
      const login = user.login.toLowerCase()
      seen.add(login)
      // GitHub has two roles. An organization owner becomes an admin here
      // rather than an owner, because owner also controls billing and that is
      // a decision for this application, not for GitHub.
      const mapped: Role = role === 'admin' ? 'admin' : 'member'
      const existing = byLogin.get(login)

      if (!existing) {
        const userId = await upsertUserInTenant(db, clock, user)
        await db.execute(sql`
          INSERT INTO members (org_id, user_id, role, source)
          VALUES (${input.orgId}, ${userId}, ${mapped}, 'github')
          ON CONFLICT (org_id, user_id) DO NOTHING`)
        report.added.push(user.login)
        continue
      }

      // A role set by hand in this application is not overwritten by GitHub's
      // idea of it. Somebody promoted to owner here stays an owner.
      if (existing.source === 'github' && existing.role !== mapped) {
        await db.execute(sql`
          UPDATE members SET role = ${mapped}, updated_at = ${clock.now().toISOString()}
          WHERE org_id = ${input.orgId} AND user_id = ${existing.user_id}`)
        report.changed.push({ login: user.login, from: existing.role, to: mapped })
      }
    }

    for (const member of current) {
      if (seen.has(member.github_login.toLowerCase())) continue
      // Only memberships GitHub created are removed by GitHub. One added here
      // by an administrator is theirs to remove.
      if (member.source !== 'github') continue
      await db.execute(sql`
        DELETE FROM members WHERE org_id = ${input.orgId} AND user_id = ${member.user_id}`)
      // Their sessions go too, so removal takes effect now rather than at the
      // end of whatever session they already hold.
      await db.execute(sql`
        UPDATE sessions SET revoked_at = ${clock.now().toISOString()}
        WHERE user_id = ${member.user_id} AND org_id = ${input.orgId}`)
      report.removed.push(member.github_login)
    }

    if (report.added.length || report.removed.length || report.changed.length) {
      await appendAudit(db, {
        orgId: input.orgId,
        actorLabel: input.actorLabel,
        action: 'members.synced',
        targetType: 'organization',
        targetId: input.orgLogin,
        origin: 'github',
        detail: { ...report },
        occurredAt: clock.now(),
      })
    }
    return report
  }, { githubIds: upstream.map((m) => m.user.id) })
}

async function upsertUserInTenant(db: Db, clock: Clock, user: GitHubUser): Promise<string> {
  const rows = await db.execute<{ id: string }>(sql`
    INSERT INTO users (github_id, github_login, email, name, avatar_url, created_at, updated_at)
    VALUES (${user.id}, ${user.login}, ${user.email.toLowerCase()}, ${user.name},
            ${user.avatarUrl}, ${clock.now().toISOString()}, ${clock.now().toISOString()})
    ON CONFLICT (github_id) DO UPDATE SET
      github_login = EXCLUDED.github_login, email = EXCLUDED.email,
      name = EXCLUDED.name, updated_at = EXCLUDED.updated_at
    RETURNING id`)
  return rows[0]!.id
}
