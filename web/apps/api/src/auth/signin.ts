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
import { engagedReason } from '../admin/controls.ts'
import { provisionPersonalOrganization, type ProvisionedOrganization } from './provision.ts'

/** How long the browser has to come back from GitHub. */
export const OAUTH_STATE_TTL_MS = 10 * 60 * 1000

/**
 * Why a sign-in did not happen.
 *
 * `not-invited` is a different event from every other value here and the
 * difference is what the person sees. Nothing is broken, nothing expired and
 * nothing needs retrying: this installation admits a list of accounts and
 * theirs is not on it. A page that told them to "try again" would be sending
 * them around the same loop forever.
 *
 * `link-expired` is the one where trying again IS the fix. It is separated from
 * `exchange-failed` rather than being the default, because the default is what
 * a throw added later gets, and telling somebody their link expired when the
 * real answer was that GitHub reported no members for their organization sends
 * them to press the same button until they give up.
 */
export type SignInRefusal = 'not-invited' | 'link-expired' | 'exchange-failed'

export class SignInError extends Error {
  readonly refusal: SignInRefusal
  /**
   * Whether the OAuth authorization this exchange just created was withdrawn
   * again.
   *
   * Only ever true on a refusal, and it is not decoration. Refusing at the
   * callback means the visitor has already granted a third party application
   * access to their GitHub account, and a refusal that leaves that grant
   * standing has taken something from somebody it then turned away. The page
   * says the grant is gone only when this says it is, because a reassurance
   * that might not be true is worse than none.
   */
  readonly authorizationRevoked: boolean

  constructor(
    message: string,
    options: { refusal?: SignInRefusal; authorizationRevoked?: boolean } = {},
  ) {
    super(message)
    this.refusal = options.refusal ?? 'exchange-failed'
    this.authorizationRevoked = options.authorizationRevoked ?? false
  }
}

/**
 * Where somebody this installation will not admit is sent instead.
 *
 * Unset is the self-hosted default and means there is nowhere: an operator who
 * runs an allowlist has their own way of being asked, and inventing a link to
 * the vendor's contact form on their deployment would be wrong. The hosted
 * planes set it to the marketing site's contact page, where a form writes a row
 * a person reads.
 *
 * On a plane with no allowlist this is never rendered, because nobody is
 * refused. It is still worth setting, so that closing sign-ups later is one
 * decision rather than two.
 */
export function signupUrlFrom(value: string | undefined | null): string | undefined {
  if (!value?.trim()) return undefined
  let url: URL
  try {
    url = new URL(value.trim())
  } catch {
    throw new Error('AF_SIGNUP_URL must be an absolute http or https address.')
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new Error('AF_SIGNUP_URL must be an absolute http or https address.')
  }
  return url.toString()
}

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
 * How long a dead handshake is left in the table before the sweep takes it.
 *
 * The row is unusable ten minutes after it is written, so this is not an
 * expiry and nothing turns on getting it right. It is a margin, and what it
 * buys is that the sweep cannot race a callback that is still in flight even
 * if this process and the database disagree about the time by hours.
 * sweepDeviceAuthorizations takes the same day for the same reason.
 */
export const OAUTH_STATE_SWEEP_MARGIN_MS = 24 * 60 * 60 * 1000

/**
 * Removes handshakes nobody came back from, and returns how many.
 *
 * completeSignIn deletes a state ON REDEMPTION, in the statement that reads
 * it, and until this existed that was the only DELETE this table ever saw.
 * Nothing swept it. So every person who pressed "Continue with GitHub" and
 * closed the tab left a row behind for good, and so did the site deploy gate,
 * which probes GET /auth/github on every publish under -allow-write-probes.
 * The volume is trivial and was never the objection: the objection is a
 * security relevant table on an unauthenticated path with no upper bound.
 *
 * Housekeeping rather than enforcement, and the distinction is load bearing
 * rather than decorative. completeSignIn compares expires_at against its own
 * clock after it has taken the row, so a state this sweep has not reached yet
 * is already refused, and a sweep that is late costs table size and nothing
 * else. Nothing here can end a sign-in that is still in flight.
 *
 * IT NEEDS NO POLICY OF ITS OWN, WHICH IS NOT WHAT THE TWO SWEEPERS BEFORE IT
 * FOUND, and the difference is worth saying out loud because assuming it would
 * have been the whole bug. 0016 and 0024 are both named after a sweeper that
 * could not delete: every policy on device_authorizations and on sessions keys
 * on a value the caller declares, so a sweeper declaring none matched no row
 * and reported success over zero rows for the life of the process. The policy
 * on THIS table is handshakes_are_public, `FOR ALL TO antifailure_app USING
 * (true)`, written that way in 0002 because a handshake has no tenant and no
 * user to key on and the state value is the only thing worth holding. A
 * DELETE from the application role therefore reaches every expired row
 * already. Adding a role or a policy here would widen the schema to buy
 * something it already has.
 *
 * That is a claim about row level security, so it is measured rather than
 * asserted: the test runs this through the ordinary application pool, which
 * connects as antifailure_app, and first proves that role has neither
 * BYPASSRLS nor superuser, so a delete that removes the row is a delete that
 * went THROUGH the policy rather than around it.
 *
 * The count comes from RETURNING a constant. Returning the state values would
 * pull a table's worth of live 256-bit secrets into this process to count
 * them, which is a thing to avoid for the same reason sweepSessions does not
 * return an id.
 */
export async function sweepOAuthStates(pool: Pool, clock: Clock): Promise<number> {
  const cutoff = new Date(clock.now().getTime() - OAUTH_STATE_SWEEP_MARGIN_MS)
  return pool.withoutTenant(async (db) => {
    const rows = await db.execute<{ n: string }>(sql`
      WITH gone AS (
        DELETE FROM oauth_states WHERE expires_at < ${cutoff.toISOString()} RETURNING 1
      ) SELECT count(*) AS n FROM gone`)
    return Number(rows[0]?.n ?? 0)
  })
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
  /**
   * The slug of the organization self serve signup created for this person on
   * this sign-in, or null.
   *
   * Null covers three different things on purpose, because the caller does the
   * same thing for all of them: they already had somewhere to be, the setting
   * is off, or the attempt found the slug taken. It is not a success flag; it
   * is what to say to somebody who has just arrived somewhere new.
   */
  provisioned: string | null
}

/** What the caller knows that the exchange itself does not. */
export interface CompleteSignInOptions {
  /** Who may sign in at all. See parseAllowlist. */
  allowlist?: SignInAllowlist
  /** Whether somebody arriving with no organization is given one. See
   *  provision.ts, which also explains why the default is off. */
  selfServeSignup?: boolean
  /** Where a failure that must not fail the sign-in goes. Absent means it goes
   *  nowhere, which is right for a test and wrong for a server, so main.ts
   *  passes one. */
  log?: (message: string, error: unknown) => void
}

/**
 * Completes the exchange.
 *
 * Consuming the state and exchanging the code both happen before anything is
 * written, so a replayed callback cannot create a user or a session.
 */

/**
 * Refuses somebody this installation has never seen while sign-ups are paused.
 *
 * Named so that the catalog entry in admin/controls.ts points at a real
 * function, and exported so a test can drive it directly as well as through
 * the callback.
 *
 * A known account is never refused. That is the entire difference between this
 * and the allowlist, and it is the reason the switch is safe to reach for: an
 * operator pausing sign-ups during an incident must not also be signing out
 * every customer who is mid-task.
 */
export async function refuseNewAccounts(pool: Pool, githubId: number): Promise<void> {
  const reason = await pool.withoutTenant((db) => engagedReason(db, 'signups'))
  if (reason === null) return
  const known = await pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{ n: string }>(sql`
        SELECT count(*) AS n FROM users WHERE github_id = ${githubId}`)
      return Number(rows[0]?.n ?? 0) > 0
    },
    { githubIds: [githubId] },
  )
  if (known) return
  throw new SignInError(
    `New sign-ups are paused on this installation: ${reason}. ` +
      `Accounts that already exist are unaffected.`,
  )
}

export async function completeSignIn(
  pool: Pool,
  clock: Clock,
  github: GitHubClient,
  input: { code: string; state: string },
  options: CompleteSignInOptions = {},
): Promise<CompletedSignIn> {
  const allowlist = options.allowlist ?? null
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
    throw new SignInError('This sign-in link is no longer valid. Start again.', {
      refusal: 'link-expired',
    })
  }
  const expiresAt = consumed.expires_at instanceof Date ? consumed.expires_at : new Date(consumed.expires_at)
  if (expiresAt.getTime() <= clock.now().getTime()) {
    throw new SignInError('This sign-in link is no longer valid. Start again.', {
      refusal: 'link-expired',
    })
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
    // And nothing left on GitHub either.
    //
    // "Leaves nothing behind" used to mean nothing in this database. It was
    // never the whole of it: by the time this line runs, the visitor has
    // pressed Authorize on a third party application, and that grant sits on
    // their account until they go and find it. Refusing somebody and keeping
    // the access they just handed over is the part of this flow that reads as
    // dishonest, so it is given back.
    //
    // Not awaited for its answer's sake alone: whether it succeeded decides
    // what the page is allowed to claim.
    const { revoked } = await github.revokeAuthorization(accessToken)
    throw new SignInError(
      'This installation is not open for sign-ups. Ask an owner to add your ' +
        'GitHub account to the allowlist.',
      { refusal: 'not-invited', authorizationRevoked: revoked },
    )
  }

  // The installation's sign-up switch, checked in the same place and for the
  // same reason as the allowlist above: before the user row, before the
  // membership lookup, before anything is written.
  //
  // It refuses only people this installation has never seen. An allowlist
  // refuses everybody not named on it, including somebody who has been signing
  // in for a year, and that is the right shape for an allowlist and the wrong
  // shape for a pause. What an operator wants during an incident is "stop new
  // accounts appearing", not "lock out the customers".
  //
  // The githubIds declaration is what makes the read possible at all: see
  // migrations/0006, writing to users is permitted and reading it is not,
  // except for the ids the caller names. The id here came from GitHub moments
  // ago for the person holding this code.
  await refuseNewAccounts(pool, user.id)

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

  // The user's own login belongs in this list, and leaving it out was a whole
  // tenant nobody could enter. Installing the App on a personal account
  // creates an organization keyed on that login exactly as an organization
  // installation does, but /user/orgs never returns your own account, so the
  // rows existed, the installation was live, and every sign-in by the one
  // person entitled to it landed in no organization at all.
  const logins = [...new Set([user.login.toLowerCase(), ...orgs.map((o) => o.login.toLowerCase())])]

  // Membership is granted only where an installation exists. Belonging to a
  // GitHub organization is not by itself a reason to see another company's
  // environments: somebody has to have installed the App for it first.
  //
  // sql.param, not a bare array. An array interpolated directly becomes a
  // record of one parameter per element, which is a tuple rather than an array,
  // and the mistake reads as correct in every language this is written in.
  const installed = await pool.withoutTenant(
    async (db) =>
      logins.length
        ? await db.execute<{
            org_id: string
            installation_id: string
            account_login: string
            account_type: string
          }>(sql`
            SELECT org_id, installation_id, account_login, account_type FROM github_installations
            WHERE lower(account_login) = ANY(${sql.param(logins)}::text[])
              AND suspended_at IS NULL`)
        : [],
    // Only the logins. Each of these two reads declares the one value its own
    // policy keys on, rather than both declaring both: a transaction that names
    // something it does not use is a policy nobody can tell is unnecessary.
    { githubLogins: logins },
  )

  for (const row of installed) {
    await grantMembership(pool, clock, github, {
      orgId: row.org_id,
      installationId: Number(row.installation_id),
      orgLogin: row.account_login,
      userId,
      login: user.login,
      label: user.name || user.login,
      // A GitHub login names a user or an organization and never both, so an
      // installation on an account of type User whose login is this person's
      // is their own account and nobody else's.
      personalAccount:
        row.account_type === 'User' &&
        row.account_login.toLowerCase() === user.login.toLowerCase(),
    })
  }

  // Memberships that already existed count too: an installation may have been
  // set up while this person was signed in, or by somebody else.
  const memberships = await pool.withoutTenant(
    async (db) =>
      db.execute<{ org_id: string }>(sql`
        SELECT org_id FROM members WHERE user_id = ${userId} ORDER BY created_at ASC`),
    { signinUserId: userId },
  )

  // Signed in, admitted, and standing in nothing.
  //
  // READ AFTER the installation loop and after the existing memberships, never
  // instead of them, which is the whole ordering argument. Somebody who belongs
  // anywhere has a place already, whether they installed the App, a colleague
  // did, or they took up an invitation last week, and creating a personal
  // tenant beside it would put a second empty organization in their switcher
  // for no reason. Zero here means zero after every other way
  // of having one has been asked.
  //
  // Failing this must not fail the sign-in. Everything above it has already
  // committed, the person is a real authenticated user either way, and the
  // console has a state for holding no organization that predates this and
  // still works. So a refusal from the database here costs somebody the
  // convenience of arriving inside a tenant, not their login.
  let provisioned: ProvisionedOrganization | null = null
  if (memberships.length === 0 && options.selfServeSignup) {
    try {
      provisioned = await provisionPersonalOrganization(pool, clock, {
        userId,
        login: user.login,
        label: user.name || user.login,
      })
    } catch (err) {
      options.log?.('self serve signup could not create an organization', err)
    }
  }

  return {
    provisioned: provisioned?.slug ?? null,
    userId,
    // An organization is chosen only when there is exactly one. With several,
    // the caller shows a picker: guessing puts somebody in the wrong tenant,
    // where every page is empty for no visible reason.
    //
    // The one just created counts as that one. It cannot appear in the list
    // above, which was read before it existed, and it is reachable only in the
    // branch where the list was empty, so this can never disagree with the
    // rule: nothing to pick from, and then exactly one thing.
    orgId: provisioned ? provisioned.orgId : memberships.length === 1 ? memberships[0]!.org_id : null,
    redirectTo: consumed.redirect_to,
    label: user.name || user.login,
  }
}

/**
 * Writes one person's membership of one organization, in that organization's
 * own tenant scope.
 *
 * WHY THIS IS NOT PART OF THE SIGN-IN TRANSACTION any more. Deciding whether
 * this person is the FIRST member of the organization means counting the
 * organization's members, and the sign-in scope cannot: `signin_own_membership`
 * in migration 0007 exposes only rows where `user_id` is the person signing in,
 * so a count taken there is always zero and everybody would look like the
 * first. Widening that policy would have let a sign-in read other people's
 * membership rows; entering the organization's own scope reads them under the
 * isolation rule that already exists.
 *
 * The evidence that entitles this transaction to the tenant is the same
 * evidence that entitles it to write the row at all: GitHub said, moments ago
 * and for this person, that they belong to that organization, and an
 * installation exists for it.
 *
 * The cost is that memberships are no longer written all-or-nothing across
 * several organizations. That is the better failure: an error reaching GitHub
 * about the second organization should not discard the membership already
 * established for the first.
 */
export async function grantMembership(
  pool: Pool,
  clock: Clock,
  github: GitHubClient,
  input: {
    orgId: string
    installationId: number
    orgLogin: string
    userId: string
    login: string
    label: string
    /** The installation is on this person's own GitHub account. */
    personalAccount: boolean
  },
): Promise<void> {
  // The role comes from GitHub rather than from a constant here.
  //
  // This used to write 'member' for everybody, which is wrong in the one case
  // that matters most: the person who installed the App, on the organization
  // they own, arrives as a plain member and cannot manage members, mint engine
  // tokens, export the audit log, approve a masking or egress change, or store
  // a provider key. Their own control plane refuses them, and the only way out
  // was a database console.
  //
  // An organization owner becomes an admin rather than an owner, for the reason
  // syncMembership gives further down: owner also holds billing, and that is
  // this application's decision rather than GitHub's.
  //
  // A personal account is not asked about, because there is nothing to ask.
  // /orgs/<login> is not an organization when <login> is a person, so roleIn
  // can only fail, and a null here would make the account holder a plain
  // member of their own tenant with nobody holding billing.manage: the exact
  // dead end described below, reached by a different road. They installed the
  // App on their own account, which is the strongest evidence of
  // administration GitHub has to offer about it.
  const upstream: 'admin' | 'member' | null = input.personalAccount
    ? 'admin'
    : await github.roleIn(input.installationId, input.orgLogin, input.login).catch(() => null)
  const fromGitHub: Role = upstream === 'admin' ? 'admin' : 'member'

  await pool.withTenant({ orgId: input.orgId, userId: input.userId }, async (db) => {
    // Serialised per organization, for the reason appendAudit gives: two people
    // signing in at the same instant would both read an empty member list and
    // both claim to be the first. An advisory lock rather than SELECT ... FOR
    // UPDATE, because locking the rows a query returns locks nothing when it
    // returns none, and none is exactly the case being decided here. It is also
    // the only lock this transaction takes, so two sign-ins that share two
    // organizations cannot deadlock against each other.
    await db.execute(sql`SELECT pg_advisory_xact_lock(hashtext(${`members:${input.orgId}`}))`)

    const existing = await db.execute<{ n: string }>(sql`
      SELECT count(*) AS n FROM members WHERE org_id = ${input.orgId}`)
    const unclaimed = Number(existing[0]?.n ?? 0) === 0

    // The first member of an organization becomes its owner, and only then.
    //
    // Every organization here is created by an installation webhook, before
    // anybody has signed in, so a fresh organization has no members at all.
    // Mapping every GitHub administrator to `admin` therefore left every
    // organization on this control plane with NOBODY holding `billing.manage`,
    // which is owner-only: the plan, the payment method and the spending caps
    // were unreachable for everyone, on every tenant, from the day it was
    // created. An admin could promote themselves out of it, which is why this
    // was survivable, and nothing said that was the step required.
    //
    // GitHub still has to confirm the promotion. `upstream === null` means
    // GitHub would not say, and guessing upward on a null is the thing
    // `roleIn` returns null to prevent: an outage during the first sign-in
    // would hand ownership to whoever happened to arrive first. So a first
    // sign-in that GitHub cannot speak for gets `member`, exactly as before,
    // and the way out of an organization whose GitHub link is permanently
    // broken is the break-glass command in backup-cli, not a guess made here.
    const bootstrap = unclaimed && upstream === 'admin'
    const role: Role = bootstrap ? 'owner' : fromGitHub

    await db.execute(sql`
      INSERT INTO members (org_id, user_id, role, source)
      VALUES (${input.orgId}, ${input.userId}, ${role}, ${bootstrap ? 'manual' : 'github'})
      ON CONFLICT (org_id, user_id) DO UPDATE SET
        -- Two things this must not do. It must not overwrite a role set by
        -- hand in this application, which is what source = 'manual' marks.
        -- And it must not act on a role GitHub declined to report: null is
        -- "ask again later", not "demote them", or a rate limit during
        -- sign-in would quietly strip an administrator of their rights.
        --
        -- GitHub's answer, never the bootstrapped one. A conflict means a
        -- membership row already exists, which means the organization was not
        -- unclaimed, which means there was no bootstrap to carry here.
        role = CASE
          WHEN members.source = 'github' AND ${upstream !== null}
            THEN ${fromGitHub}
          ELSE members.role
        END,
        updated_at = ${clock.now().toISOString()}`)

    if (!bootstrap) return

    // Marked `manual` above and audited here, and the two go together. A
    // promotion this application decided is not GitHub's to take back on the
    // next sync, and an organization acquiring an owner without a person
    // asking for one is a thing a security review has to be able to find.
    await appendAudit(db, {
      orgId: input.orgId,
      actorUserId: input.userId,
      actorLabel: input.label,
      action: 'member.bootstrapped',
      targetType: 'member',
      targetId: input.login,
      origin: 'github',
      detail: { role, githubRole: upstream, reason: 'first member of the organization' },
      occurredAt: clock.now(),
    })
  })
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
