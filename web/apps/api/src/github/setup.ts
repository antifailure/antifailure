// The pull request that adds the workflow file.
//
// Installing the App connected a repository and then nothing happened. The
// installation was recorded, the repository was listed in the console, and no
// check ever ran on any pull request, because a check needs a workflow file in
// the repository and nothing had put one there. The documentation sent people
// to copy a file by hand, and the repository that sat "connected" for a month
// with zero runs was the ordinary outcome.
//
// So the App opens the pull request itself. The delivery that records the
// installation enqueues one row per repository (requestSetups, called from
// webhook.ts) and does no GitHub calls: a delivery is leased for two minutes
// and GitHub's own timeout on it is ten seconds. sweepSetups runs beside
// sweepTeardowns and does the four calls under a lease, so a process that dies
// holding one costs a minute rather than the repository.
//
// WHAT IT WRITES, AND WHERE. One file, `.github/workflows/antifailure.yml`, on
// a branch of its own, `antifailure/setup`, never on the default branch. The
// file is the same one `af init` writes and the same one the documentation
// shows, and setup.test.ts holds it byte for byte equal to
// examples/github-workflow.yml so the three cannot drift. Nothing runs in the
// customer's repository until a person merges the pull request, and the body
// says so.
//
// THE PERMISSION THIS DID NOT HAVE. Writing a file needs `contents: write`,
// and the App was created with Contents read. Widening it raises a request
// against every installation and grants nothing until somebody accepts, so
// until then every setup lands in `needs_permission` with the remedy in
// last_error, the console shows the repository as needing the grant, and the
// `new_permissions_accepted` delivery that follows the acceptance puts the row
// back in the queue. Nothing is retried in a loop against a refusal that will
// answer identically forever.

import { readFileSync } from 'node:fs'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { GitHubApiError, GitHubPermissionError, type RepositoryApi } from './api.ts'

/** How many times a setup is attempted before it is given up on and said so. */
export const SETUP_ATTEMPTS = 5

/** How long one sweeper holds a setup. Four GitHub calls fit inside it many
 *  times over, and a process that dies holding one costs a minute. */
export const SETUP_LEASE_MS = 60 * 1000

/** The branch the file is committed to. Under a prefix of its own so it cannot
 *  collide with a branch a person named, and so a repository's branch list
 *  says who made it. */
export const SETUP_BRANCH = 'antifailure/setup'

export const WORKFLOW_PATH = '.github/workflows/antifailure.yml'

export const SETUP_TITLE = 'Check every pull request with Antifailure'

export const SETUP_DOCS_URL = 'https://antifailure.dev/docs/getting-started/pull-requests'

/**
 * The file that goes into the customer's repository.
 *
 * Read once at import from a copy beside this file, because the api is copied
 * into its container as a directory and a path that reached outside it to
 * examples/ would resolve on a developer's machine and nowhere else.
 * setup.test.ts is what keeps the copy equal to examples/github-workflow.yml.
 */
export const WORKFLOW_TEMPLATE = readFileSync(
  new URL('./setup/antifailure.yml', import.meta.url),
  'utf8',
)

/**
 * The name of the repository variable the hosted control plane reads, taken
 * from the workflow itself rather than written here twice. It is a VARIABLE
 * and not a secret on purpose: it is an address, and a secret would be masked
 * out of every log line that mentioned it.
 *
 * Derived rather than declared for two reasons. The pull request body names
 * whatever the workflow it ships actually reads, so the two cannot drift. And
 * the names in this file are the CUSTOMER'S repository settings, not this
 * process's: config-docs.test.ts scrapes every AF_ token out of src/ as a
 * variable the control plane reads and requires a row in the configuration
 * reference for each, which would be a lie for these. The optional secret
 * names live beside the template in optional-secrets.json for the same reason.
 */
export const CONTROL_PLANE_VARIABLE = ((): string => {
  const found = /\bvars\.([A-Z][A-Z0-9_]+)/.exec(WORKFLOW_TEMPLATE)
  if (!found) {
    throw new Error(
      'setup/antifailure.yml reads no repository variable, so the pull request body cannot name one',
    )
  }
  return found[1]!
})()

/** The secrets the workflow can use and does not need, by name, with what
 *  each one unlocks. Customer-side names, kept as data for the reason above. */
export const OPTIONAL_SECRETS: readonly { name: string; unlocks: string }[] = JSON.parse(
  readFileSync(new URL('./setup/optional-secrets.json', import.meta.url), 'utf8'),
) as { name: string; unlocks: string }[]

export interface SetupDeps {
  pool: Pool
  clock: Clock
  api: RepositoryApi
  /** This control plane's public address, for the pull request body. Null
   *  means the body says "the address of this control plane" and nothing
   *  more, which is what a deployment without one should say. */
  consoleBase: string | null
  /** Names this process in a lease. Any stable string. */
  holder?: string
}

export type SetupState =
  | 'queued'
  | 'leased'
  | 'opened'
  | 'present'
  | 'needs_permission'
  | 'failed'
  | 'skipped'

// ---------------------------------------------------------------------------
// Enqueueing, from the webhook
// ---------------------------------------------------------------------------

/**
 * Asks for a setup of each named repository, once per repository ever.
 *
 * Runs on the connection the delivery handler already holds, scoped to the
 * account, so the policy that admits the write is the same one that admits the
 * repository row it joins. ON CONFLICT DO NOTHING is the idempotence: a
 * redelivered installation event, an installation_repositories event naming a
 * repository the installation event already carried, and a repository removed
 * and re-added all land on the row that exists.
 *
 * Returns how many rows were created, for the delivery's outcome line.
 */
export async function requestSetups(
  db: Db,
  clock: Clock,
  orgId: string,
  fullNames: string[],
): Promise<number> {
  if (fullNames.length === 0) return 0
  const now = clock.now().toISOString()
  // IN rather than ANY: drizzle renders a JS array as a parenthesised list,
  // which is what IN wants and what ANY(...::text[]) would refuse.
  const rows = await db.execute<{ id: string }>(sql`
    INSERT INTO repository_setups (org_id, repository_id, state, requested_at, updated_at)
    SELECT r.org_id, r.id, 'queued', ${now}::timestamptz, ${now}::timestamptz
    FROM repositories r
    WHERE r.org_id = ${orgId}::uuid
      AND r.full_name IN ${fullNames}
      AND r.archived_at IS NULL
    ON CONFLICT (repository_id) DO NOTHING
    RETURNING id`)
  return rows.length
}

/**
 * Puts every setup that was refused for a missing permission back in the
 * queue. Called on `new_permissions_accepted`, which is the one delivery that
 * means the refusal might now answer differently.
 *
 * All of the organization's refused rows rather than the ones the payload
 * names, because the permission is a property of the installation and not of
 * a repository: accepting it changes the answer for every repository at once.
 */
export async function retryRefusedSetups(db: Db, clock: Clock, orgId: string): Promise<number> {
  const now = clock.now().toISOString()
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE repository_setups
    SET state = 'queued', attempts = 0, lease_holder = NULL, leased_until = NULL,
        last_error = NULL, updated_at = ${now}::timestamptz
    WHERE org_id = ${orgId}::uuid AND state = 'needs_permission'
    RETURNING id`)
  return rows.length
}

// ---------------------------------------------------------------------------
// The pull request
// ---------------------------------------------------------------------------

/**
 * What the pull request says. Plain prose, because the person reading it is
 * deciding whether to merge a workflow file into their repository and the
 * body is the only place that decision is explained.
 */
export function setupPullRequestBody(input: {
  repository: string
  defaultBranch: string
  controlPlane: string | null
}): string {
  const plane = input.controlPlane
    ? `\`${input.controlPlane}\``
    : 'the public address of the control plane that opened this pull request'
  return [
    `This pull request adds \`${WORKFLOW_PATH}\`, the workflow that runs Antifailure on ` +
      `every pull request in ${input.repository}. It was opened by the Antifailure GitHub ` +
      `App when it was installed on this repository.`,
    '',
    '## What happens once it is merged',
    '',
    `Every pull request against \`${input.defaultBranch}\` gets a disposable copy of the ` +
      `application, built from the manifest in the repository, and Antifailure runs the ` +
      `workflows the manifest names against it. The result arrives as one comment on the ` +
      `pull request, edited in place on every push rather than posted again, and as a check ` +
      `a branch protection rule can require.`,
    '',
    'Nothing runs until this pull request is merged. The workflow file is on the branch ' +
      `\`${SETUP_BRANCH}\` and nowhere else, and the App has not changed anything on ` +
      `\`${input.defaultBranch}\`.`,
    '',
    'Pull requests from forks wait for a maintainer to add the `antifailure:allow` label ' +
      'before anything runs, because a fork can change the workflow and the manifest, and ' +
      'running whatever a stranger pushed against your secrets is not a decision this file ' +
      'should make for you.',
    '',
    '## Secrets',
    '',
    'No secret is required. The check runs on an empty database with the schema the ' +
      'migrations build, and the report says so at the top. These are optional and each one ' +
      'unlocks something:',
    '',
    '- The production database, under the name `database.source_url_env` in the manifest ' +
      'chooses, such as `PRODUCTION_DATABASE_URL`. With it the copy is a masked branch of ' +
      'production rather than an empty schema.',
    ...OPTIONAL_SECRETS.map((secret) => `- \`${secret.name}\` ${secret.unlocks}`),
    '',
    'Secrets are read by name from the repository settings. The workflow passes them to ' +
      'Antifailure and to nothing else, and the job prints none of them.',
    '',
    '## The hosted control plane',
    '',
    `If you use a control plane, set one repository variable, \`${CONTROL_PLANE_VARIABLE}\`, ` +
      `to its public address, which for this one is ${plane}. It is a variable rather than a ` +
      `secret because it is an address, not a credential. With it set, the control plane ` +
      `keeps one check and one comment per pull request and can ask this workflow to build an ` +
      `environment from the console. Without it the workflow comments for itself and needs ` +
      `nothing from anybody.`,
    '',
    `The rest is at ${SETUP_DOCS_URL}.`,
  ].join('\n')
}

// ---------------------------------------------------------------------------
// The sweep
// ---------------------------------------------------------------------------

export interface SetupSweep {
  opened: number
  present: number
  needsPermission: number
  skipped: number
  retried: number
  failed: number
}

interface ClaimedSetup {
  id: string
  orgId: string
  login: string
  attempts: number
  installationId: number | null
  repository: string | null
  defaultBranch: string
  archived: boolean
}

/**
 * Works through the setup queue.
 *
 * The same two-step shape as sweepTeardowns, and the split matters for the same
 * reason. A sweeper has no tenant, so on its own connection the only policy
 * that admits anything is the narrow read in migration 0040: due rows, SELECT,
 * nothing else. It reads WHICH work is due there and does everything else on a
 * connection scoped to the account the work belongs to, where the delivery
 * policy applies, which is the scope every other write to this table uses.
 */
export async function sweepSetups(deps: SetupDeps): Promise<SetupSweep> {
  const now = deps.clock.now()
  const holder = deps.holder ?? 'control-plane'
  const sweep: SetupSweep = {
    opened: 0,
    present: 0,
    needsPermission: 0,
    skipped: 0,
    retried: 0,
    failed: 0,
  }

  const due = await deps.pool.withSweeper(async (db) =>
    db.execute<{ id: string; org_id: string }>(sql`
      SELECT id, org_id FROM repository_setups
      WHERE state IN ('queued', 'leased')
        AND (leased_until IS NULL OR leased_until < ${now.toISOString()}::timestamptz)
      ORDER BY requested_at
      LIMIT 20`),
  )

  for (const candidate of due) {
    const login = await accountLoginFor(deps.pool, candidate.org_id)
    // No live installation means no token to act with. The row stays queued
    // and costs nothing; the next installation delivery is what changes it.
    if (login === null) continue

    const claimed = await claimSetup(deps, login, candidate, holder)
    if (!claimed) continue

    const outcome = await attemptSetup(deps, claimed)
    switch (outcome.state) {
      case 'opened':
        sweep.opened += 1
        break
      case 'present':
        sweep.present += 1
        break
      case 'needs_permission':
        sweep.needsPermission += 1
        break
      case 'skipped':
        sweep.skipped += 1
        break
      case 'failed':
        sweep.failed += 1
        break
      case 'queued':
        sweep.retried += 1
        break
    }
    await finishSetup(deps, claimed, outcome)
  }

  return sweep
}

async function accountLoginFor(pool: Pool, orgId: string): Promise<string | null> {
  return pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{ account_login: string }>(sql`
      SELECT account_login FROM github_installations
      WHERE suspended_at IS NULL ORDER BY created_at ASC LIMIT 1`)
    return rows[0]?.account_login ?? null
  })
}

async function claimSetup(
  deps: SetupDeps,
  login: string,
  candidate: { id: string; org_id: string },
  holder: string,
): Promise<ClaimedSetup | null> {
  const now = deps.clock.now()
  return deps.pool.withGitHubAccount(login, async (db) => {
    // A compare-and-set rather than a lock held across the work. Two replicas
    // sweeping at the same instant is the ordinary case, and the one that
    // loses this UPDATE gets no row back and moves on.
    const rows = await db.execute<{
      id: string
      org_id: string
      repository_id: string
      attempts: number
    }>(sql`
      UPDATE repository_setups
      SET state = 'leased', lease_holder = ${holder},
          leased_until = ${new Date(now.getTime() + SETUP_LEASE_MS).toISOString()}::timestamptz,
          attempts = attempts + 1,
          updated_at = ${now.toISOString()}
      WHERE id = ${candidate.id}::uuid
        AND state IN ('queued', 'leased')
        AND (leased_until IS NULL OR leased_until < ${now.toISOString()}::timestamptz)
      RETURNING id, org_id, repository_id::text AS repository_id, attempts`)
    const row = rows[0]
    if (!row) return null

    const repos = await db.execute<{
      full_name: string
      default_branch: string
      archived_at: Date | null
    }>(sql`
      SELECT full_name, default_branch, archived_at FROM repositories
      WHERE id = ${row.repository_id}::uuid`)
    const repo = repos[0]

    const installations = await db.execute<{ installation_id: string }>(sql`
      SELECT installation_id FROM github_installations
      WHERE lower(account_login) = ${login.toLowerCase()} AND suspended_at IS NULL
      ORDER BY created_at ASC LIMIT 1`)
    const installation = installations[0]

    return {
      id: row.id,
      orgId: row.org_id,
      login,
      attempts: row.attempts,
      installationId: installation ? Number(installation.installation_id) : null,
      repository: repo?.full_name ?? null,
      defaultBranch: repo?.default_branch || 'main',
      archived: repo?.archived_at != null,
    }
  })
}

interface SetupOutcome {
  state: 'opened' | 'present' | 'needs_permission' | 'skipped' | 'failed' | 'queued'
  error: string | null
  pullRequest: { number: number; url: string } | null
}

async function attemptSetup(deps: SetupDeps, setup: ClaimedSetup): Promise<SetupOutcome> {
  const none = { pullRequest: null }
  if (!setup.repository) {
    // The repository row went away under the lease. The cascade will take
    // this row too; recorded rather than retried so the sweep does not spin.
    return { state: 'skipped', error: 'the repository is no longer connected', ...none }
  }
  if (setup.archived) {
    return {
      state: 'skipped',
      error: 'the repository is archived, so no pull request was opened',
      ...none,
    }
  }
  if (setup.installationId === null) {
    return retryOrFail(setup, 'this account has no active GitHub App installation')
  }

  const { api } = deps
  const installationId = setup.installationId
  const repository = setup.repository
  try {
    if (await api.fileExists(installationId, repository, WORKFLOW_PATH, setup.defaultBranch)) {
      return { state: 'present', error: null, ...none }
    }
    const head = await api.branchHead(installationId, repository, setup.defaultBranch)
    await api.createBranch(installationId, repository, SETUP_BRANCH, head)
    await api.putFile(installationId, repository, {
      path: WORKFLOW_PATH,
      branch: SETUP_BRANCH,
      message: 'Add the Antifailure workflow',
      content: WORKFLOW_TEMPLATE,
    })
    const opened = await api.createPullRequest(installationId, repository, {
      title: SETUP_TITLE,
      head: SETUP_BRANCH,
      base: setup.defaultBranch,
      body: setupPullRequestBody({
        repository,
        defaultBranch: setup.defaultBranch,
        controlPlane: deps.consoleBase ? deps.consoleBase.replace(/\/+$/, '') : null,
      }),
    })
    return { state: 'opened', error: null, pullRequest: opened }
  } catch (err) {
    if (err instanceof GitHubPermissionError) {
      // Will answer identically forever, so it is recorded and not retried.
      // The new_permissions_accepted delivery is what puts it back.
      return { state: 'needs_permission', error: err.message, ...none }
    }
    return retryOrFail(setup, describeFailure(err))
  }
}

function retryOrFail(setup: ClaimedSetup, error: string): SetupOutcome {
  if (setup.attempts >= SETUP_ATTEMPTS) {
    return {
      state: 'failed',
      error: `Given up after ${setup.attempts} attempts. The last one: ${error}`,
      pullRequest: null,
    }
  }
  return { state: 'queued', error, pullRequest: null }
}

function describeFailure(err: unknown): string {
  if (err instanceof GitHubApiError) return `GitHub answered ${err.status}. ${err.message}`
  return err instanceof Error ? err.message : String(err)
}

async function finishSetup(
  deps: SetupDeps,
  setup: ClaimedSetup,
  outcome: SetupOutcome,
): Promise<void> {
  const now = deps.clock.now().toISOString()
  const terminal = outcome.state !== 'queued'
  await deps.pool.withGitHubAccount(setup.login, async (db) => {
    await db.execute(sql`
      UPDATE repository_setups
      SET state = ${outcome.state}, lease_holder = NULL, leased_until = NULL,
          last_error = ${outcome.error},
          branch = ${outcome.state === 'opened' ? SETUP_BRANCH : null},
          pull_request_number = ${outcome.pullRequest?.number ?? null},
          pull_request_url = ${outcome.pullRequest?.url ?? null},
          finished_at = ${terminal ? now : null}::timestamptz,
          updated_at = ${now}::timestamptz
      WHERE id = ${setup.id}::uuid`)
  })
}
