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
import type { Analytics } from '../analytics/record.ts'
import type { GitHubClient } from '../auth/github.ts'
import { grantMembership } from '../auth/signin.ts'
import { verifySignature } from './app.ts'
import { slugFor } from '../slug.ts'
import { requestSetups, retryRefusedSetups } from './setup.ts'

// `WebhookError` used to be declared here and thrown from exactly one place,
// `slugFor`, which has moved to src/slug.ts and throws `SlugError` instead.
// Nothing ever caught it, in this file or anywhere else, so keeping the class
// would have left an exported type with no thrower and no catcher: a name that
// reads as a handled failure mode and is neither.

/** The collaborators a delivery is handled with. See handleDelivery. */
export interface DeliveryDeps {
  /**
   * The client adoptInstaller reaches GitHub with. Optional and nullable,
   * because a control plane with no App configured still receives and refuses
   * deliveries, and adoptInstaller returns null on its first line without one.
   */
  github?: GitHubClient | null
  /** Where the organization event goes. Never null: the recorder does nothing
   *  when analytics is unconfigured, so no call site has to check. */
  analytics: Analytics
  /**
   * Drops the cached installation token, when there is a cache to drop from.
   *
   * Optional because the webhook has to work with no App configured, which is
   * every self-hosted control plane that has not created one yet.
   */
  forgetTokens?: (installationId: number) => void
}

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

/** The `changes` object on a repository event. GitHub sends the old name on
 *  `renamed` and the old owner on `transferred`, and nothing else here reads
 *  it. */
interface RepositoryChanges {
  repository?: { name?: { from?: string } }
  owner?: { from?: { organization?: { login?: string }; user?: { login?: string } } }
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
  /**
   * What this handler needs that is not the delivery itself.
   *
   * AN OBJECT RATHER THAN MORE POSITIONAL PARAMETERS, and the reason is
   * that three of these arrived within a day of each other on different
   * branches and collided on the same line. `github` is how adoptInstaller
   * reaches GitHub; `analytics` is where the organization event goes;
   * `forgetTokens` drops the cached installation token. None is a property of
   * the delivery, all are collaborators, and a sixth positional argument is
   * where the next one goes wrong silently: a null in the wrong slot compiles.
   */
  deps: DeliveryDeps,
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
      deps.forgetTokens?.(id)
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
      const orgId = await rememberInstallation(pool, clock, account, id, deps.analytics, { live: true })
      await rememberRepositories(pool, clock, account.login, orgId, repos)
      // The pull request that adds the workflow file, asked for and not done:
      // this handler is leased for two minutes and GitHub times a delivery out
      // at ten seconds, so the four GitHub calls belong to sweepSetups. A
      // permission grant is the one action that can change the answer for a
      // setup GitHub already refused, so it puts those back in the queue too.
      const setups = await requestSetupsFor(pool, clock, account.login, orgId, repos, {
        retryRefused: action === 'new_permissions_accepted',
      })
      const adopted = await adoptInstaller(pool, clock, deps.github ?? null, {
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
          (setups.queued ? `, ${setups.queued} setup pull requests queued` : '') +
          (setups.retried ? `, ${setups.retried} refused setups retried` : '') +
          (adopted ? `, ${adopted} adopted` : ''),
      }
    }

    case 'installation_repositories': {
      const id = installation?.id
      const account = installation?.account
      if (typeof id !== 'number' || !account?.login) {
        return { event, action, handled: false, detail: 'no installation in the payload' }
      }
      const orgId = await rememberInstallation(pool, clock, account, id, deps.analytics)
      const added = Array.isArray(payload.repositories_added)
        ? (payload.repositories_added as Repo[])
        : []
      const removed = Array.isArray(payload.repositories_removed)
        ? (payload.repositories_removed as Repo[])
        : []
      await rememberRepositories(pool, clock, account.login, orgId, added)
      const setups = await requestSetupsFor(pool, clock, account.login, orgId, added, {
        retryRefused: false,
      })
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
        detail:
          `${added.length} added, ${removed.length} archived` +
          (setups.queued ? `, ${setups.queued} setup pull requests queued` : ''),
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
      const changes = payload.changes as RepositoryChanges | undefined

      // A transfer BEFORE rememberInstallation, because that call creates an
      // organization for whatever login the payload names, and on a transfer
      // the payload names the new owner. See transferRepository for why the
      // new owner is not taken on trust.
      if (action === 'transferred') {
        const detail = await transferRepository(pool, clock, {
          repo,
          installationId: id,
          newOwner: account,
          oldOwner: changes?.owner?.from?.organization?.login ?? changes?.owner?.from?.user?.login ?? null,
        })
        return { event, action, handled: true, detail }
      }

      const orgId = await rememberInstallation(pool, clock, account, id, deps.analytics)
      if (action === 'deleted' || action === 'archived') {
        await archiveRepositories(pool, clock, account.login, orgId, [repo])
        return { event, action, handled: true, detail: `${repo.full_name} archived` }
      }
      if (action === 'renamed') {
        const from = changes?.repository?.name?.from
        const detail = await renameRepository(pool, clock, account.login, orgId, repo, {
          previousFullName: typeof from === 'string' && from ? `${login}/${from}` : null,
        })
        if (detail) return { event, action, handled: true, detail }
        // Nothing here knew the old name or the id, so there is nothing to
        // rename: it is recorded under the name it has now, below.
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
  analytics: Analytics,
  options: { live: boolean } = { live: false },
): Promise<string> {
  const login = account.login
  return pool.withGitHubAccount(login, async (db) => {
    const slug = slugFor(login)
    // `xmax = 0` in the RETURNING below is true only on a row this statement
    // INSERTED. Every installation delivery reaches this upsert and most are
    // for organizations that already exist, so counting the statement rather
    // than the insert would count one organization once per delivery forever.
    //
    // The explanation sits above the statement rather than inside it because
    // the gate in tenancy.test.ts that requires every write to this table to
    // name its columns reads source text and cannot tell code from a comment.
    // It said so, and said a false positive costs a rewording. This is the
    // rewording.
    const rows = await db.execute<{ id: string; created: boolean }>(sql`
      INSERT INTO organizations (slug, name, github_login)
      VALUES (${slug}, ${login}, ${login})
      ON CONFLICT (slug) DO UPDATE SET
        github_login = EXCLUDED.github_login,
        updated_at = ${clock.now().toISOString()}
      RETURNING id, (xmax = 0) AS created`)
    const orgId = rows[0]!.id

    if (rows[0]!.created) {
      await analytics.record(db, {
        name: 'identity.organization_created',
        occurredAt: clock.now(),
        orgId,
        payload: {},
      })
    }

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

/**
 * A rename keeps the row and changes the name on it.
 *
 * THE ROW IS THE HISTORY. environments, runs, pull_requests, repository_setups,
 * the cost ledger and the load definitions all point at repositories.id, and
 * before this a rename went through rememberRepositories, whose upsert keys on
 * (org_id, full_name): the new name matched nothing, so it inserted a second
 * row, and the old row kept every environment and every verdict under a name
 * GitHub no longer serves. Nothing archived it and nothing pointed at it. The
 * next pull request on the renamed repository looked up the new name, found
 * the empty row, and started the product's record of that repository from
 * zero.
 *
 * The row is found by github_id first, which GitHub keeps stable across every
 * rename and transfer, and by the old full name when the row predates the id
 * being recorded. The lookup is inside the delivery's own account scope, so a
 * rename can only ever move a name within the organization the delivery is
 * about.
 *
 * Returns the outcome for the delivery log, or null when no row was found,
 * which the caller answers by recording the repository under its new name.
 *
 * ONE NAME PER ORGANIZATION IS A CONSTRAINT, and the rename has to respect it.
 * GitHub only allows a rename to a name nobody holds, but this table can still
 * hold the new name from a repository that was deleted or moved away under it
 * years ago, archived and never removed. Renaming onto it would violate the
 * unique constraint and the delivery would answer 500, which GitHub retries
 * into the same 500 forever. So when another row holds the new name, the
 * rename falls back to what it did before, one row per name with the old one
 * archived, and says so in the outcome. That loses the id continuity for that
 * one repository and keeps the delivery answerable, which is the right trade
 * for a case that needs a deleted repository's ghost to line up with a live
 * one's new name.
 */
async function renameRepository(
  pool: Pool,
  clock: Clock,
  login: string,
  orgId: string,
  repo: Repo,
  options: { previousFullName: string | null },
): Promise<string | null> {
  return pool.withGitHubAccount(login, async (db) => {
    const found = await db.execute<{ id: string; full_name: string }>(sql`
      SELECT id, full_name FROM repositories
      WHERE org_id = ${orgId}::uuid
        AND (github_id = ${repo.id ?? null}
             OR (${options.previousFullName}::text IS NOT NULL
                 AND full_name = ${options.previousFullName}))
      ORDER BY (github_id = ${repo.id ?? null}) DESC NULLS LAST, archived_at NULLS FIRST
      LIMIT 1`)
    const row = found[0]
    if (!row) return null
    if (row.full_name === repo.full_name) {
      return `${repo.full_name} already carries that name`
    }

    const holder = await db.execute<{ id: string }>(sql`
      SELECT id FROM repositories
      WHERE org_id = ${orgId}::uuid AND full_name = ${repo.full_name} AND id <> ${row.id}::uuid`)
    if (holder.length > 0) {
      await db.execute(sql`
        INSERT INTO repositories (org_id, full_name, github_id, private, default_branch, archived_at, updated_at)
        VALUES (${orgId}::uuid, ${repo.full_name}, ${repo.id ?? null},
                ${repo.private ?? true}, ${repo.default_branch ?? 'main'}, NULL,
                ${clock.now().toISOString()})
        ON CONFLICT (org_id, full_name) DO UPDATE SET
          github_id = coalesce(EXCLUDED.github_id, repositories.github_id),
          private = EXCLUDED.private,
          default_branch = EXCLUDED.default_branch,
          archived_at = NULL,
          updated_at = ${clock.now().toISOString()}`)
      await db.execute(sql`
        UPDATE repositories
        SET archived_at = coalesce(archived_at, ${clock.now().toISOString()}),
            updated_at = ${clock.now().toISOString()}
        WHERE id = ${row.id}::uuid`)
      return (
        `${row.full_name} renamed to ${repo.full_name}, but a row already held that name, ` +
        `so the old row is archived and the new name starts a new record`
      )
    }

    await db.execute(sql`
      UPDATE repositories
      SET full_name = ${repo.full_name},
          github_id = coalesce(${repo.id ?? null}, github_id),
          private = ${repo.private ?? true},
          default_branch = ${repo.default_branch ?? 'main'},
          updated_at = ${clock.now().toISOString()}
      WHERE id = ${row.id}::uuid`)
    return `${row.full_name} renamed to ${repo.full_name}`
  })
}

/**
 * A transfer moves a repository between two GitHub owners, and here an owner
 * is a tenant.
 *
 * The row does NOT move with it, and that is a decision rather than a gap.
 * Every table that points at the repository carries its own org_id and its own
 * row-level policy, so moving repositories.org_id alone would leave the old
 * tenant's environments and verdicts joined to a repository they can no longer
 * see, and moving all of it would hand one customer another customer's run
 * history, cost ledger and captured messages because somebody on GitHub
 * pressed Transfer. The policies in 0013 refuse the cross-tenant write
 * outright: a delivery is scoped to one account and can write only that
 * account's rows. So the old owner keeps its history, archived, exactly as an
 * installation_repositories removal would leave it, and the new owner starts a
 * fresh record under its own tenant, exactly as a new repository would.
 *
 * WHAT MADE THIS A BUG rather than a design note. Before this, the transferred
 * action fell through to rememberRepositories under the NEW owner, after
 * rememberInstallation had created an organization and an installation row
 * for that owner from the delivery's installation id. GitHub sends this event
 * to the new owner's installation, so usually that owner does have the App,
 * but the handler never checked, and a delivery about an owner this control
 * plane had never seen would have minted a tenant for them and pointed the
 * delivery's installation id at it. The old row, meanwhile, was left live
 * under the old tenant, still named for a repository that had left.
 *
 * So the new owner is believed only when the installation the delivery names
 * is already recorded for that owner, which is exactly what the row-level
 * policy on github_installations lets the new owner's scope see. An owner
 * with no installation here gets nothing written for them; the delivery is
 * still handled, because the half that matters, archiving the old row, does
 * not depend on the new owner at all.
 */
async function transferRepository(
  pool: Pool,
  clock: Clock,
  input: { repo: Repo; installationId: number; newOwner: Account; oldOwner: string | null },
): Promise<string> {
  const { repo, newOwner, oldOwner } = input
  const parts: string[] = []
  const name = repo.full_name.slice(repo.full_name.indexOf('/') + 1)

  if (oldOwner) {
    // The old owner's scope, which can see the old owner's rows and nothing
    // else. The old name is reconstructed from the old owner and the
    // repository's name, because GitHub sends only the owner that changed.
    const archived = await pool.withGitHubAccount(oldOwner, async (db) => {
      const rows = await db.execute<{ id: string; full_name: string }>(sql`
        UPDATE repositories
        SET archived_at = coalesce(archived_at, ${clock.now().toISOString()}),
            updated_at = ${clock.now().toISOString()}
        WHERE github_id = ${repo.id ?? null} OR full_name = ${`${oldOwner}/${name}`}
        RETURNING id, full_name`)
      return rows.map((r) => r.full_name)
    })
    parts.push(
      archived.length > 0
        ? `${archived.join(', ')} archived under ${oldOwner}`
        : `${oldOwner} had no record of it`,
    )
  } else {
    parts.push('the delivery names no previous owner')
  }

  const known = await pool.withGitHubAccount(newOwner.login, async (db) => {
    const rows = await db.execute<{ org_id: string }>(sql`
      SELECT org_id FROM github_installations
      WHERE installation_id = ${input.installationId}
        AND lower(account_login) = ${newOwner.login.toLowerCase()}
        AND suspended_at IS NULL`)
    return rows[0]?.org_id ?? null
  })
  if (known) {
    await rememberRepositories(pool, clock, newOwner.login, known, [repo])
    parts.push(`${repo.full_name} recorded under ${newOwner.login}`)
  } else {
    parts.push(`${newOwner.login} has no installation here, so nothing was recorded for it`)
  }
  return parts.join('; ')
}

/**
 * Enqueues the setup pull request for each repository, on the delivery's own
 * account scope, and on a permission grant puts the refused ones back.
 *
 * Runs AFTER rememberRepositories on purpose: the enqueue joins the repository
 * rows, so a repository this delivery is the first to name has to exist before
 * it can be queued. An installation_repositories delivery that arrives before
 * the installation event therefore queues its repositories itself, and the
 * installation event that follows lands on the same rows and queues nothing
 * twice.
 */
async function requestSetupsFor(
  pool: Pool,
  clock: Clock,
  login: string,
  orgId: string,
  repos: Repo[],
  options: { retryRefused: boolean },
): Promise<{ queued: number; retried: number }> {
  const names = repos.map((r) => r?.full_name).filter((n): n is string => Boolean(n))
  if (names.length === 0 && !options.retryRefused) return { queued: 0, retried: 0 }
  return pool.withGitHubAccount(login, async (db) => ({
    queued: await requestSetups(db, clock, orgId, names),
    retried: options.retryRefused ? await retryRefusedSetups(db, clock, orgId) : 0,
  }))
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

export { verifySignature }
// Moved to src/slug.ts, which is what lets self serve signup derive the same
// slug from the same login without this file and auth/signin.ts importing each
// other. Re-exported here because that is where its callers and its tests have
// always looked for it.
export { slugFor }
