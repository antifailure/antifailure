// What GitHub tells us, and what we are willing to believe.
//
// A webhook is an unauthenticated endpoint that anybody on the internet can
// POST to. Everything below rests on one check: the HMAC in
// `x-hub-signature-256`, computed over the raw body with a secret only GitHub
// and this process hold. A request that fails it is refused before it is
// parsed, because parsing attacker-controlled JSON to decide whether to trust
// it has the order backwards.
//
// After that check the payload is trusted about ONE account: the one it names.
// That is what the row-level security policies below key on, and it is why this
// file sets `antifailure.github_account` rather than opening the tables up. A
// delivery about `antifailure` can write rows for `antifailure` and cannot
// touch another tenant, even if the handler has a bug.
//
// WHY THE INSTALLATION EVENTS MATTER. Sign-in already reads
// github_installations to decide which organizations somebody may enter. Until
// something writes that table, every person who signs in lands in no
// organization and the console renders an empty state that looks like a bug.
// This is the thing that writes it.

import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { GitHubClient } from '../auth/github.ts'
import { grantMembership } from '../auth/signin.ts'
import { verifySignature } from './app.ts'

export class WebhookError extends Error {}

/** What one delivery did, for the response and for a log line. */
export interface WebhookOutcome {
  event: string
  action: string | null
  handled: boolean
  /** A short, non-sensitive description for the caller and the delivery log. */
  detail: string
}

interface Account {
  login: string
  type: string
}

/** The GitHub account that performed the action, on every delivery. */
interface Sender {
  id?: number
  login?: string
}

interface Repo {
  id: number
  full_name: string
  private?: boolean
  default_branch?: string
}

/**
 * Handles one verified delivery.
 *
 * Returns rather than throws for anything that is not our problem: an event we
 * do not subscribe to, an action we do not act on, a payload shaped in a way we
 * did not expect. GitHub retries a delivery that answers 5xx, so answering 500
 * to an event this control plane will never handle produces a retry storm
 * against an endpoint that will refuse it identically every time.
 */
export async function handleDelivery(
  pool: Pool,
  clock: Clock,
  event: string,
  payload: Record<string, unknown>,
  github?: GitHubClient | null,
  /**
   * Drops the cached installation token, when there is a cache to drop from.
   *
   * Optional because the webhook has to work with no App configured, which is
   * every self-hosted control plane that has not created one yet.
   */
  forgetTokens?: (installationId: number) => void,
): Promise<WebhookOutcome> {
  const action = typeof payload.action === 'string' ? payload.action : null
  const installation = payload.installation as
    | { id?: number; account?: Account; repository_selection?: string }
    | undefined

  switch (event) {
    case 'ping':
      return { event, action, handled: true, detail: 'ping acknowledged' }

    case 'installation': {
      const id = installation?.id
      const account = installation?.account
      if (typeof id !== 'number' || !account?.login) {
        return { event, action, handled: false, detail: 'no installation in the payload' }
      }
      // Every `installation` action changes what a token minted for it is
      // worth, so the cached one is dropped before any of them is handled.
      //
      // This is the fix for an hour of wrong answers that a person actually
      // sat through. An installation token is cached for its full hour, GitHub
      // invalidates the outstanding ones the moment a grant changes, and
      // `new_permissions_accepted` is that moment. On 2026-08-31 the Actions
      // write grant was accepted at 00:38:54Z against a process that had
      // minted a token at 00:35:58Z, so the console answered 403 until roughly
      // 01:36Z while the permission it was complaining about had already been
      // granted. Nothing invalidated the token, because `forget` had no
      // callers anywhere in the tree.
      //
      // Unconditional across the actions rather than matched to
      // `new_permissions_accepted` alone: suspend, unsuspend and deleted all
      // change what the token can do, `created` can arrive for an id a failed
      // earlier attempt already cached, and dropping a token that did not need
      // dropping costs one mint.
      forgetTokens?.(id)
      if (action === 'deleted') {
        await forgetInstallation(pool, account.login, id)
        return { event, action, handled: true, detail: `installation ${id} removed` }
      }
      if (action === 'suspend' || action === 'unsuspend') {
        await setSuspended(pool, clock, account.login, id, action === 'suspend')
        return { event, action, handled: true, detail: `installation ${id} ${action}ed` }
      }
      // created, new_permissions_accepted, and anything else that means "this
      // installation exists": recorded the same way, because the row we want is
      // the same row and writing it twice is harmless.
      const repos = Array.isArray(payload.repositories) ? (payload.repositories as Repo[]) : []
      // `live` here and nowhere else. This is the only event that means the
      // installation exists right now, so it is the only one allowed to clear a
      // suspension. See rememberInstallation.
      const orgId = await rememberInstallation(pool, clock, account, id, { live: true })
      await rememberRepositories(pool, clock, account.login, orgId, repos)
      const adopted = await adoptInstaller(pool, clock, github ?? null, {
        orgId,
        account,
        installationId: id,
        sender: payload.sender as Sender | undefined,
      })
      return {
        event,
        action,
        handled: true,
        detail:
          `installation ${id} for ${account.login}, ${repos.length} repositories` +
          (adopted ? `, ${adopted} adopted` : ''),
      }
    }

    case 'installation_repositories': {
      const id = installation?.id
      const account = installation?.account
      if (typeof id !== 'number' || !account?.login) {
        return { event, action, handled: false, detail: 'no installation in the payload' }
      }
      const orgId = await rememberInstallation(pool, clock, account, id)
      const added = Array.isArray(payload.repositories_added)
        ? (payload.repositories_added as Repo[])
        : []
      const removed = Array.isArray(payload.repositories_removed)
        ? (payload.repositories_removed as Repo[])
        : []
      await rememberRepositories(pool, clock, account.login, orgId, added)
      // Archived rather than deleted. A repository removed from an installation
      // still has runs, verdicts and artifacts that happened, and deleting the
      // row would cascade them away: the history of what this product found is
      // the product, and losing it because somebody unticked a checkbox is not
      // a trade anybody agreed to.
      await archiveRepositories(pool, clock, account.login, orgId, removed)
      return {
        event,
        action,
        handled: true,
        detail: `${added.length} added, ${removed.length} archived`,
      }
    }

    case 'repository': {
      const id = installation?.id
      const repo = payload.repository as (Repo & { owner?: Account }) | undefined
      // NOT installation.account. Every event except `installation` and
      // `installation_repositories` carries the MINIMAL installation object --
      // `{ id, node_id }` and nothing else -- so reading the account off it
      // meant this branch answered "no repository in the payload" for every
      // repository delivery GitHub has ever sent, silently, with a 200. The
      // owner is on the repository and on the organization, in the same signed
      // body, and is trusted for the same reason.
      const org = payload.organization as { login?: string } | undefined
      const login = org?.login ?? repo?.owner?.login
      if (typeof id !== 'number' || !login || !repo?.full_name) {
        return { event, action, handled: false, detail: 'no repository in the payload' }
      }
      const account: Account = { login, type: repo.owner?.type ?? 'Organization' }
      const orgId = await rememberInstallation(pool, clock, account, id)
      if (action === 'deleted' || action === 'archived') {
        await archiveRepositories(pool, clock, account.login, orgId, [repo])
        return { event, action, handled: true, detail: `${repo.full_name} archived` }
      }
      await rememberRepositories(pool, clock, account.login, orgId, [repo])
      return { event, action, handled: true, detail: `${repo.full_name} recorded` }
    }

    // Membership changes are noted and deliberately not acted on here. Sign-in
    // is what grants a person a tenant, and it does so from the organizations
    // GitHub reports for THAT person at the moment they sign in. Writing
    // membership from a webhook would create rows for people who have never
    // signed in and have no user row to point at.
    //
    // adoptInstaller above is not an exception to that rule, it is the one
    // case that satisfies it: the installer is named by the delivery, and it
    // acts only when a user row for them already exists.
    case 'member':
    case 'organization':
      return { event, action, handled: false, detail: 'membership is resolved at sign-in' }

    default:
      return { event, action, handled: false, detail: 'not an event this control plane acts on' }
  }
}

/**
 * The organization and the installation, written together.
 *
 * An organization is created when one does not exist, because an installation
 * IS the moment a tenant begins: somebody chose to install the App on their
 * account, and there is no earlier point at which to ask them to sign up.
 */
async function rememberInstallation(
  pool: Pool,
  clock: Clock,
  account: Account,
  installationId: number,
  options: { live: boolean } = { live: false },
): Promise<string> {
  const login = account.login
  return pool.withGitHubAccount(login, async (db) => {
    const slug = slugFor(login)
    const rows = await db.execute<{ id: string }>(sql`
      INSERT INTO organizations (slug, name, github_login)
      VALUES (${slug}, ${login}, ${login})
      ON CONFLICT (slug) DO UPDATE SET
        github_login = EXCLUDED.github_login,
        updated_at = ${clock.now().toISOString()}
      RETURNING id`)
    const orgId = rows[0]!.id

    await db.execute(sql`
      INSERT INTO github_installations
        (org_id, installation_id, account_login, account_type, created_at, updated_at)
      VALUES (${orgId}::uuid, ${installationId}, ${login}, ${account.type ?? 'Organization'},
              ${clock.now().toISOString()}, ${clock.now().toISOString()})
      ON CONFLICT (installation_id) DO UPDATE SET
        account_login = EXCLUDED.account_login,
        account_type = EXCLUDED.account_type,
        -- Cleared only by a delivery that says the installation is live right
        -- now, which is the installation event and nothing else. This used to
        -- clear it unconditionally, and every caller reaches it: a repository
        -- or installation_repositories delivery retried after a suspend or an
        -- uninstall would put suspended_at back to null, and sign-in grants
        -- membership on exactly "suspended_at IS NULL". GitHub does not promise
        -- delivery order and retries a failed delivery for hours, so the
        -- ordering that restores access somebody revoked is an ordinary one.
        suspended_at = CASE WHEN ${options.live} THEN NULL
                            ELSE github_installations.suspended_at END,
        updated_at = ${clock.now().toISOString()}`)
    return orgId
  })
}

async function setSuspended(
  pool: Pool,
  clock: Clock,
  login: string,
  installationId: number,
  suspended: boolean,
): Promise<void> {
  await pool.withGitHubAccount(login, async (db) => {
    await db.execute(sql`
      UPDATE github_installations
      SET suspended_at = ${suspended ? clock.now().toISOString() : null},
          updated_at = ${clock.now().toISOString()}
      WHERE installation_id = ${installationId}`)
  })
}

/**
 * An uninstall marks the installation suspended rather than deleting it.
 *
 * Deleting cascades to nothing here, but it does lose the record that this
 * account was ever connected, and "when did they uninstall" is the first
 * question anybody asks about a customer who left. Sign-in already ignores a
 * suspended installation, so the access consequence is identical.
 */
async function forgetInstallation(pool: Pool, login: string, installationId: number): Promise<void> {
  await pool.withGitHubAccount(login, async (db) => {
    await db.execute(sql`
      UPDATE github_installations
      SET suspended_at = coalesce(suspended_at, now()), updated_at = now()
      WHERE installation_id = ${installationId}`)
  })
}

async function rememberRepositories(
  pool: Pool,
  clock: Clock,
  login: string,
  orgId: string,
  repos: Repo[],
): Promise<void> {
  if (repos.length === 0) return
  await pool.withGitHubAccount(login, async (db) => {
    for (const repo of repos) {
      if (!repo?.full_name) continue
      await db.execute(sql`
        INSERT INTO repositories (org_id, full_name, github_id, private, default_branch, updated_at)
        VALUES (${orgId}::uuid, ${repo.full_name}, ${repo.id ?? null},
                ${repo.private ?? true}, ${repo.default_branch ?? 'main'},
                ${clock.now().toISOString()})
        ON CONFLICT (org_id, full_name) DO UPDATE SET
          github_id = coalesce(EXCLUDED.github_id, repositories.github_id),
          private = EXCLUDED.private,
          -- Un-archived on purpose: re-adding a repository to an installation
          -- is somebody restoring it, and it should stop reading as archived.
          archived_at = NULL,
          updated_at = ${clock.now().toISOString()}`)
    }
  })
}

async function archiveRepositories(
  pool: Pool,
  clock: Clock,
  login: string,
  orgId: string,
  repos: Repo[],
): Promise<void> {
  if (repos.length === 0) return
  await pool.withGitHubAccount(login, async (db) => {
    for (const repo of repos) {
      if (!repo?.full_name) continue
      await db.execute(sql`
        UPDATE repositories
        SET archived_at = coalesce(archived_at, ${clock.now().toISOString()}),
            updated_at = ${clock.now().toISOString()}
        WHERE org_id = ${orgId}::uuid AND full_name = ${repo.full_name}`)
    }
  })
}

/**
 * Gives the person who installed the App the organization it just created.
 *
 * THIS IS AN ORDERING FIX AND THE ORDERING IS THE COMMON ONE. Signing in and
 * installing the App are two events with no guaranteed order, and the flow the
 * product actually recommends puts them in the order this repairs: somebody
 * signs in, has no organization because nothing is installed yet, and follows
 * the button that installs it. Sign-in already handles installation-then-signin
 * because it reads the installation table on the way through. The reverse
 * arrived at a tenant that existed, that they administered, and that nothing
 * would ever attach them to, because the only writer of membership had already
 * run. They were left pressing a second sign-in to re-run an exchange they had
 * just completed, on a page that could not say why.
 *
 * So the late-created row resolves itself on creation rather than waiting for
 * an event that already fired.
 *
 * The three conditions, all required, none of them a guess. GitHub signed the
 * delivery. The delivery names the sender, and a user row already exists for
 * that GitHub id, which means this is somebody who has signed in here rather
 * than a stranger being given an account. And the role still comes from
 * GitHub through the same grantMembership the sign-in path uses, rather than
 * from a second membership writer with its own rules: two writers that agree
 * today are two writers that disagree after the next change to either.
 *
 * Returns the login it adopted, or null when there was nobody to adopt, which
 * is the ordinary case for an installation by somebody who has never signed in.
 */
async function adoptInstaller(
  pool: Pool,
  clock: Clock,
  github: GitHubClient | null,
  input: { orgId: string; account: Account; installationId: number; sender: Sender | undefined },
): Promise<string | null> {
  // No client configured means no way to ask GitHub for a role, and guessing
  // one is what the null answer from roleIn exists to prevent.
  if (!github) return null
  const senderId = input.sender?.id
  const senderLogin = input.sender?.login
  if (typeof senderId !== 'number' || typeof senderLogin !== 'string' || !senderLogin) return null

  const user = await pool.withoutTenant(
    async (db) => {
      const rows = await db.execute<{ id: string; name: string | null; github_login: string }>(sql`
        SELECT id, name, github_login FROM users WHERE github_id = ${senderId}`)
      return rows[0] ?? null
    },
    { githubIds: [senderId] },
  )
  // Nobody by that id has ever signed in. Nothing to attach, and inventing a
  // user row from a webhook is the thing the comment above refuses.
  if (!user) return null

  await grantMembership(pool, clock, github, {
    orgId: input.orgId,
    installationId: input.installationId,
    orgLogin: input.account.login,
    userId: user.id,
    login: senderLogin,
    label: user.name || user.github_login,
    personalAccount:
      input.account.type === 'User' &&
      input.account.login.toLowerCase() === senderLogin.toLowerCase(),
  })

  // The session they are holding right now, so the tab they left open on the
  // empty state becomes the organization without a second sign-in.
  //
  // Only a session that is in no organization. A session already inside a
  // tenant is somebody working, and moving it would take them out of the
  // organization they are looking at because a colleague installed the App
  // somewhere else. own_sessions is the policy that permits this: it is their
  // own session, and the tenant is one they now belong to.
  await pool.withTenant({ orgId: input.orgId, userId: user.id }, async (db) => {
    await db.execute(sql`
      UPDATE sessions SET org_id = ${input.orgId}::uuid
      WHERE user_id = ${user.id}::uuid
        AND org_id IS NULL
        AND revoked_at IS NULL
        AND expires_at > ${clock.now().toISOString()}`)
  })

  return senderLogin
}

/**
 * A GitHub login turned into something the slug constraint accepts.
 *
 * organizations_slug_shape is `^[a-z0-9][a-z0-9-]{0,62}$`. GitHub logins are
 * already close to that, but not identical: they may contain uppercase, and the
 * constraint would reject one, which would make an installation from an account
 * with a capital letter fail with a constraint violation rather than work.
 */
export function slugFor(login: string): string {
  const slug = login
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, '-')
    .replace(/^-+/, '')
    .slice(0, 63)
  if (!slug) throw new WebhookError(`"${login}" has no characters a slug may contain.`)
  return slug
}

export { verifySignature }
