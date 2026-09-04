// The organization a person gets by signing up, when nobody has given them one.
//
// WHY THIS DID NOT EXIST, because the gap is not obvious from any one file.
//
// Every organization on this control plane used to begin with a GitHub App
// installation. `rememberInstallation` in github/webhook.ts creates the row and
// its own comment says why: an installation IS the moment a tenant begins.
// That is right for a company connecting its repositories and it leaves one
// person with nowhere to stand. Somebody who signs in without installing an App
// gets a user row, a session, and no membership of anything, and the console
// answers with `NoOrganization`: "Your account is not a member of an
// organization on this control plane, so there is nothing to show you yet. Not
// an empty dashboard. Nothing." That screen is honest and it is the whole
// product for a person who arrived to try it.
//
// So a sign-in that lands in no organization at all creates one, on the plan a
// new organization already gets, and that person owns it.
//
// THE SLUG IS NOT A DETAIL. It is derived from the GitHub login by the same
// `slugFor` the installation path uses, and `github_login` is written with it.
// `rememberInstallation` upserts ON CONFLICT (slug), so the day this person
// installs the App on their own account, the installation ADOPTS this row
// rather than creating a second organization beside it. Their environments,
// their audit chain and their plan survive the step. bootstrap-org.ts relies on
// exactly the same property for a self-hosted operator, and this is that
// argument applied to a person instead of an operator.
//
// WHAT IT DELIBERATELY WILL NOT DO
//
// It will not touch an organization that already exists. The insert is
// ON CONFLICT DO NOTHING with no update, so a slug already taken ends this
// quietly and the person sees the empty state they would have seen anyway. It
// can be taken by their own installation, by a colleague whose login slugifies
// to the same string, or by an operator's bootstrap. Adopting a row we did not create would
// mean handing somebody a tenant on the strength of a name collision, and two
// GitHub logins CAN slugify to one slug: `some.org` and `some-org` both become
// `some-org`.
//
// It will not run for somebody who is already in an organization. Membership
// anywhere is the signal that this person has a place, and creating a personal
// tenant beside it would put a second empty organization in their switcher.
//
// It will not create a user. That rule is older than this file and it holds
// here for the reason breakglass and bootstrap-org both give: an account is
// proof somebody signed in, and this runs only after a sign-in has produced
// one.

import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import { appendAudit } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { slugFor, SlugError } from '../slug.ts'

/**
 * Whether this installation hands a tenant to somebody who arrives with none.
 *
 * DEFAULT OFF, and the direction is the point rather than an opinion about
 * convenience. What this turns on is the ability for anybody the sign-in path
 * admits to obtain an organization with real quotas and real compute against
 * them. On a plane that runs an allowlist those are named people and the
 * setting is harmless; on a plane with `AF_SIGNIN_ALLOWLIST` unset, which the
 * process announces at start-up as "sign-in is OPEN", they are everybody.
 *
 * hosted.ts makes this argument for `AF_OPERATOR_SETS_PLAN` and it is the same
 * argument: the dangerous configuration is the one where nothing has been
 * configured, so the flag has to mean the permissive thing, and forgetting it
 * has to close the door rather than open it. An operator who has not thought
 * about self serve signup yet gets the behaviour that existed before this file.
 *
 * Antifailure's own hosted planes set it, in the tfvars beside the allowlist,
 * because self serve signup is what those planes are for.
 */
export function selfServeSignupFrom(value: string | undefined | null): boolean {
  const raw = value?.trim().toLowerCase()
  if (!raw || raw === '0' || raw === 'false') return false
  if (raw === '1' || raw === 'true') return true
  throw new Error(
    `AF_SELF_SERVE_SIGNUP must be 1, 0 or unset; received ${JSON.stringify(value)}.`,
  )
}

/** What the process says at start-up, so the mode is never a guess. The
 *  allowlist gets a line of its own for the same reason and they are read
 *  together: one decides who may sign in, this decides what they find. */
export function describeSelfServeSignup(enabled: boolean): string {
  return enabled
    ? 'self serve signup is ON: somebody who signs in with no organization gets their own, on the free plan (AF_SELF_SERVE_SIGNUP=1)'
    : 'self serve signup is OFF: somebody who signs in with no organization stays in none until an installation or an invitation gives them one (AF_SELF_SERVE_SIGNUP is not set)'
}

export interface ProvisionedOrganization {
  orgId: string
  slug: string
}

/**
 * Creates the organization, and makes this person its owner.
 *
 * Returns null when there was nothing to do, which is not a failure and is the
 * ordinary answer in three cases: the login has no characters a slug may hold,
 * the slug is already taken, or two sign-ins raced and the other one won. The
 * caller carries on with no organization, which is a state the console and
 * every procedure already handle.
 *
 * TWO TRANSACTIONS, NOT ONE, and the seam is where it is because the two halves
 * are entitled to their rows for different reasons. The organization is written
 * under `withGitHubAccount`, whose policy in migration 0013 confines it to rows
 * whose `github_login` is the account named, and the account is named by GitHub
 * moments ago for this person. The membership is written under `withTenant`,
 * which is the isolation rule every other member row is written under, and the
 * evidence entitling it is the organization this transaction just created for
 * exactly this user.
 *
 * The cost of the seam is a window in which the organization exists with no
 * members, which is the state every organization created by an installation
 * sits in until somebody signs in. `grantMembership` already treats an
 * organization with no members as unclaimed and the console shows nothing for
 * an organization nobody belongs to, so a failure between the halves leaves a
 * row that the next sign-in by the same person will not adopt (the slug is
 * taken) and that nobody else can enter (they are not its member and its
 * `github_login` is not theirs). That is a stranded row rather than a leak, and
 * it is recoverable with break-glass, which is the tool that exists for it.
 */
export async function provisionPersonalOrganization(
  pool: Pool,
  clock: Clock,
  input: { userId: string; login: string; label: string },
): Promise<ProvisionedOrganization | null> {
  let slug: string
  try {
    slug = slugFor(input.login)
  } catch (err) {
    // A login made entirely of characters a slug may not contain. GitHub does
    // not issue those today, and a sign-in must not fail over one if it ever
    // does: the person lands with no organization, exactly as before this file.
    if (err instanceof SlugError) return null
    throw err
  }

  const created = await pool.withGitHubAccount(input.login, async (db) => {
    // ON CONFLICT DO NOTHING with no inference clause, for the reason
    // enterprise/invitations.ts sets out at length: naming a unique index makes
    // Postgres check the proposed row against the SELECT policies, and the
    // failure it produces reports the WITH CHECK message, which points at the
    // wrong half of the statement. DO NOTHING on its own needs neither.
    //
    // And no DO UPDATE, which is the difference between this and
    // rememberInstallation. That one is answering a delivery that PROVES the
    // account owns the row it is upserting. This is answering a sign-in, where
    // a slug collision between two logins is a coincidence rather than
    // evidence, and updating somebody else's organization on a coincidence is
    // how a tenant gets handed to the wrong person.
    const rows = await db.execute<{ id: string }>(sql`
      INSERT INTO organizations (slug, name, github_login, created_at, updated_at)
      VALUES (${slug}, ${input.login}, ${input.login},
              ${clock.now().toISOString()}, ${clock.now().toISOString()})
      ON CONFLICT DO NOTHING
      RETURNING id`)
    return rows[0]?.id ?? null
  })

  if (!created) return null

  await pool.withTenant({ orgId: created, userId: input.userId }, async (db) => {
    // `owner` rather than `admin`, and `manual` rather than `github`.
    //
    // signin.ts explains why the first member of an organization has to be an
    // owner: `billing.manage` is owner-only, so an organization whose only
    // member is an admin has nobody who can reach the plan, the payment method
    // or the spending caps. That failure is worse here than there, because
    // there is nobody else to promote them.
    //
    // `manual` marks the role as this application's decision, which is what
    // stops a later `members.sync` from taking it back. If this person installs
    // the App on their own account afterwards, the installation adopts this
    // organization and GitHub starts reporting on it; without `manual`, the
    // first sync that mapped them to GitHub's `admin` would demote the owner of
    // a tenant they created, and nobody would hold billing again.
    await db.execute(sql`
      INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
      VALUES (${created}::uuid, ${input.userId}::uuid, 'owner', 'manual',
              ${clock.now().toISOString()}, ${clock.now().toISOString()})
      ON CONFLICT (org_id, user_id) DO NOTHING`)

    // Two entries rather than one, because they are two different facts and a
    // security review has to be able to ask each of them separately: this
    // organization was created by a signup rather than by an installation or by
    // an operator, and this person holds owner because they created it rather
    // than because GitHub said so.
    await appendAudit(db, {
      orgId: created,
      actorUserId: input.userId,
      actorLabel: input.label,
      action: 'organization.created',
      targetType: 'organization',
      targetId: slug,
      origin: 'signup',
      detail: { name: input.login, githubLogin: input.login, reason: 'self serve signup' },
      occurredAt: clock.now(),
    })
    await appendAudit(db, {
      orgId: created,
      actorUserId: input.userId,
      actorLabel: input.label,
      action: 'member.bootstrapped',
      targetType: 'member',
      targetId: input.login,
      origin: 'signup',
      detail: { role: 'owner', githubRole: null, reason: 'created the organization at signup' },
      occurredAt: clock.now(),
    })
  })

  return { orgId: created, slug }
}
