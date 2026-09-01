// The pull request lifecycle: one generation per head commit, start to finish.
//
// READ THIS FIRST, BECAUSE EVERYTHING ELSE FOLLOWS FROM IT. A result belongs to
// a commit. A pull request is a moving target, and every ordering that breaks
// this feature is the same ordering wearing a different hat: two things are
// true at once about two different commits, and the one that wrote last wins.
// So nothing here is keyed on the pull request. The generation is keyed on
// (pull request, head SHA), the check run is created against that SHA, the
// comment carries that SHA in its first line, and a writer whose SHA is not the
// pull request's current head declines to touch the comment.
//
// THE STATE MACHINE, and the three ways in.
//
//   pull_request opened/reopened/synchronize/ready_for_review -> a generation
//   workflow_run in_progress/completed -> that generation advances
//   the job's own report -> that generation finishes
//
// and three ways out that are not the job finishing: the pull request closes,
// a newer commit supersedes it, or the deadline passes. All three are terminal
// and none of them is a pass.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not run anything. The engine builds
// the environment inside the customer's own continuous integration, where their
// database and their credentials already are, and the one route this control
// plane has into that runtime is asking GitHub to cancel the run. That is
// enough for teardown because `af ci` tears the environment down on a cancelled
// job, and it is the only thing that is honest: this service holds no cluster
// credential and no address, by design.

import { randomBytes, createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db, Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { GitHubApiError, GitHubPermissionError, type RepositoryApi } from './api.ts'
import {
  CHECK_NAME,
  COMMENT_MARKER,
  checkOutputFor,
  commentBody,
  shaOfComment,
  shortSha,
  type TeardownState,
} from './render.ts'
import {
  checkShapeFor,
  stateFromReport,
  type GenerationState,
  type ReportCounts,
} from './states.ts'

/**
 * The label a maintainer adds to let a fork's commit be checked.
 *
 * The manifest's `github.fork_policy` names `label` as the default and this is
 * the label it means. It is spelled here rather than read from the manifest,
 * because the control plane does not have the manifest: it lives in the
 * customer's repository and this service never reads a line of their code.
 * See docs/reference/manifest.md, which now says which half of that setting the
 * engine reads and which half this does.
 */
export const FORK_APPROVAL_LABEL = 'antifailure:allow'

/** How long a generation may say nothing before it is unverified. */
export const DEFAULT_DEADLINE_MS = 45 * 60 * 1000

/**
 * What the deadline sweeper writes, and the only way to tell a timeout apart
 * from a run that reported `unverified` itself.
 *
 * A constant rather than a sentence written in two places. The check's
 * conclusion differs between the two, `timed_out` against `action_required`,
 * and a comparison against a prose prefix would silently stop distinguishing
 * them the first time somebody reworded the sentence.
 */
export const TIMED_OUT_DETAIL =
  'Nothing reported before the deadline, so nothing was verified. The workflow may never have ' +
  'started, or it may have failed before Antifailure ran.'

/** How long a job's callback credential is good for. Above the deadline, so a
 *  run that reports right at its limit is not refused for being late by a
 *  second, and far below anything worth stealing. */
export const CALLBACK_TTL_MS = 60 * 60 * 1000

/** How many times a teardown is attempted before it is given up on and said so. */
export const TEARDOWN_ATTEMPTS = 5

/** How long one sweeper holds a teardown request. Longer than a GitHub call and
 *  much shorter than a person noticing, so a process that dies holding one
 *  costs a minute rather than the request. */
export const TEARDOWN_LEASE_MS = 60 * 1000

export interface LifecycleDeps {
  pool: Pool
  clock: Clock
  api: RepositoryApi
  /** Where the console lives, for the links in the comment. Null means no link
   *  is offered, which is what a deployment without a console should say. */
  consoleBase: string | null
  deadlineMs?: number
  /** Names this process in a teardown lease, so a stuck request says who has
   *  it. Any stable string; the hostname is what production passes. */
  holder?: string
}

export interface DeliveryResult {
  handled: boolean
  detail: string
  orgId: string | null
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

interface RepositoryRow extends Record<string, unknown> {
  id: string
  org_id: string
  full_name: string
}

interface InstallationRow extends Record<string, unknown> {
  installation_id: string
}

interface PullRequestRow extends Record<string, unknown> {
  id: string
  org_id: string
  repository_id: string
  number: number
  head_sha: string
  head_ref: string
  base_ref: string
  head_repository: string
  from_fork: boolean
  draft: boolean
  state: string
  approved_sha: string | null
  comment_id: string | null
  comment_sha: string | null
}

interface GenerationRow extends Record<string, unknown> {
  id: string
  org_id: string
  pull_request_id: string
  head_sha: string
  attempt: number
  state: GenerationState
  detail: string | null
  check_run_id: string | null
  workflow_run_id: string | null
  env_id: string | null
  verdict: Record<string, unknown> | null
  deadline_at: Date | string
  finished_at: Date | string | null
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

async function repositoryByName(db: Db, fullName: string): Promise<RepositoryRow | null> {
  const rows = await db.execute<RepositoryRow>(sql`
    SELECT id, org_id, full_name FROM repositories WHERE full_name = ${fullName}`)
  return rows[0] ?? null
}

/**
 * The installation that covers a repository's owner.
 *
 * By owner rather than "the organization's first installation", because an
 * organization can have more than one and a token minted for the wrong one is a
 * 404 on every call. The owner is the first segment of the full name, which is
 * how GitHub spells it and how the repositories table stores it.
 */
async function installationFor(db: Db, fullName: string): Promise<number | null> {
  const owner = fullName.split('/')[0] ?? ''
  const rows = await db.execute<InstallationRow>(sql`
    SELECT installation_id FROM github_installations
    WHERE lower(account_login) = ${owner.toLowerCase()} AND suspended_at IS NULL
    ORDER BY created_at ASC LIMIT 1`)
  const row = rows[0]
  return row ? Number(row.installation_id) : null
}

/**
 * The GitHub account an organization's installation is on.
 *
 * Needed wherever work starts on a connection that has a tenant but no account:
 * the job's own report, and the two sweepers. Everything the lifecycle writes
 * goes through the account scope, because that is the scope the delivery
 * policies are written against, so this is the bridge between the two.
 */
async function accountLoginFor(deps: LifecycleDeps, orgId: string): Promise<string | null> {
  return deps.pool.withTenant({ orgId }, async (db) => {
    const rows = await db.execute<{ account_login: string }>(sql`
      SELECT account_login FROM github_installations
      WHERE suspended_at IS NULL ORDER BY created_at ASC LIMIT 1`)
    return rows[0]?.account_login ?? null
  })
}

async function pullRequestById(db: Db, id: string): Promise<PullRequestRow | null> {
  const rows = await db.execute<PullRequestRow>(sql`
    SELECT id, org_id, repository_id, number, head_sha, head_ref, base_ref,
           head_repository, from_fork, draft, state, approved_sha,
           comment_id::text AS comment_id, comment_sha
    FROM pull_requests WHERE id = ${id}`)
  return rows[0] ?? null
}

async function generationById(db: Db, id: string): Promise<GenerationRow | null> {
  const rows = await db.execute<GenerationRow>(sql`
    SELECT id, org_id, pull_request_id, head_sha, attempt, state::text AS state, detail,
           check_run_id::text AS check_run_id, workflow_run_id::text AS workflow_run_id,
           env_id, verdict, deadline_at, finished_at
    FROM pr_generations WHERE id = ${id}`)
  return rows[0] ?? null
}

// ---------------------------------------------------------------------------
// The delivery entry point
// ---------------------------------------------------------------------------

/**
 * Handles the pull request half of a delivery.
 *
 * Returns `handled: false` for an event this does not act on, so the caller can
 * pass it to the installation handler instead. Nothing here throws for a
 * payload shaped in a way it did not expect: GitHub retries a 5xx, so answering
 * 500 to a delivery that will be refused identically forever is a retry storm
 * against an endpoint that cannot do anything with it.
 */
export async function handleLifecycleDelivery(
  deps: LifecycleDeps,
  login: string,
  event: string,
  payload: Record<string, unknown>,
): Promise<DeliveryResult> {
  switch (event) {
    case 'pull_request':
      return handlePullRequest(deps, login, payload)
    case 'workflow_run':
      return handleWorkflowRun(deps, login, payload)
    case 'check_run':
    case 'check_suite':
      return handleRerequest(deps, login, payload)
    default:
      return {
        handled: false,
        detail: 'not a pull request event',
        orgId: null,
      }
  }
}

// ---------------------------------------------------------------------------
// pull_request
// ---------------------------------------------------------------------------

interface PullRequestPayload {
  action: string
  number?: number
  pull_request?: {
    number?: number
    title?: string
    draft?: boolean
    state?: string
    merged?: boolean
    head?: { sha?: string; ref?: string; repo?: { full_name?: string } | null }
    base?: { ref?: string; repo?: { full_name?: string } | null }
  }
  repository?: { full_name?: string }
  label?: { name?: string }
  sender?: { login?: string }
}

async function handlePullRequest(
  deps: LifecycleDeps,
  login: string,
  raw: Record<string, unknown>,
): Promise<DeliveryResult> {
  const payload = raw as unknown as PullRequestPayload
  const action = typeof payload.action === 'string' ? payload.action : ''
  const pr = payload.pull_request
  const repository = payload.repository?.full_name
  const number = pr?.number ?? payload.number
  const headSha = pr?.head?.sha

  if (!repository || typeof number !== 'number' || !headSha) {
    return {
      handled: false,
      detail: 'no pull request in the payload',
      orgId: null,
    }
  }

  const stored = await deps.pool.withGitHubAccount(login, async (db) => {
    const repo = await repositoryByName(db, repository)
    if (!repo) return null
    // The head repository is what says whether this is a fork. GitHub sends
    // `head.repo: null` for a pull request whose fork has since been deleted,
    // and that case is a fork rather than an unknown: treating a missing head
    // repository as "same repository" would run a deleted fork's code with the
    // base repository's trust.
    const headRepository = pr?.head?.repo?.full_name ?? null
    const fromFork = headRepository === null || headRepository !== repository
    const state = pr?.merged ? 'merged' : pr?.state === 'closed' ? 'closed' : 'open'
    const now = deps.clock.now().toISOString()

    const rows = await db.execute<{ id: string }>(sql`
      INSERT INTO pull_requests (
        org_id, repository_id, number, title, head_sha, head_ref, base_ref,
        head_repository, from_fork, draft, state, opened_at, closed_at, updated_at)
      VALUES (
        ${repo.org_id}::uuid, ${repo.id}::uuid, ${number}, ${pr?.title ?? null},
        ${headSha}, ${pr?.head?.ref ?? ''}, ${pr?.base?.ref ?? ''},
        ${headRepository ?? `${repository} (fork deleted)`}, ${fromFork},
        ${pr?.draft ?? false}, ${state}, ${now}::timestamptz,
        ${state === 'open' ? null : now}::timestamptz, ${now}::timestamptz)
      ON CONFLICT (repository_id, number) DO UPDATE SET
        title = EXCLUDED.title,
        head_sha = EXCLUDED.head_sha,
        head_ref = EXCLUDED.head_ref,
        base_ref = EXCLUDED.base_ref,
        head_repository = EXCLUDED.head_repository,
        from_fork = EXCLUDED.from_fork,
        draft = EXCLUDED.draft,
        state = EXCLUDED.state,
        -- Only ever set, never cleared: a reopened pull request keeps the
        -- record of when it closed, and "closed_at in the past on an open pull
        -- request" is answerable by reading state rather than by losing the
        -- timestamp.
        closed_at = CASE WHEN EXCLUDED.state = 'open' THEN pull_requests.closed_at
                         ELSE coalesce(pull_requests.closed_at, EXCLUDED.closed_at) END,
        -- THE FORK APPROVAL IS VOID THE MOMENT THE HEAD MOVES. A maintainer
        -- approved one commit after reading it; the next push is code nobody
        -- looked at, and carrying the approval forward would be the whole
        -- attack this column exists to stop. Kept only while the head is
        -- unchanged.
        approved_sha = CASE WHEN pull_requests.approved_sha = EXCLUDED.head_sha
                            THEN pull_requests.approved_sha ELSE NULL END,
        approved_by = CASE WHEN pull_requests.approved_sha = EXCLUDED.head_sha
                           THEN pull_requests.approved_by ELSE NULL END,
        approved_at = CASE WHEN pull_requests.approved_sha = EXCLUDED.head_sha
                           THEN pull_requests.approved_at ELSE NULL END,
        updated_at = EXCLUDED.updated_at
      RETURNING id`)
    return { id: rows[0]!.id, orgId: repo.org_id, repository, fromFork }
  })

  if (!stored) {
    return {
      handled: false,
      detail: `${repository} is not a repository this control plane knows about`,
      orgId: null,
    }
  }

  switch (action) {
    case 'opened':
    case 'reopened':
    case 'synchronize':
    case 'ready_for_review': {
      const outcome = await startGeneration(deps, login, stored.id, headSha)
      return { handled: true, detail: outcome, orgId: stored.orgId }
    }
    case 'labeled':
    case 'unlabeled': {
      const label = payload.label?.name ?? ''
      if (label !== FORK_APPROVAL_LABEL) {
        return {
          handled: true,
          detail: `${label || 'a label'} is not the approval label`,
          orgId: stored.orgId,
        }
      }
      if (action === 'labeled') {
        await approveFork(deps, login, stored.id, headSha, payload.sender?.login ?? null)
        const outcome = await startGeneration(deps, login, stored.id, headSha)
        return {
          handled: true,
          detail: `${shortSha(headSha)} approved, ${outcome}`,
          orgId: stored.orgId,
        }
      }
      await withdrawFork(deps, login, stored.id)
      await stopWork(deps, login, stored.id, 'the approval label was removed')
      return {
        handled: true,
        detail: 'approval withdrawn',
        orgId: stored.orgId,
      }
    }
    case 'converted_to_draft': {
      await stopWork(deps, login, stored.id, 'the pull request went back to draft')
      return {
        handled: true,
        detail: 'draft, so nothing is running',
        orgId: stored.orgId,
      }
    }
    case 'closed': {
      await stopWork(
        deps,
        login,
        stored.id,
        payload.pull_request?.merged
          ? 'the pull request was merged'
          : 'the pull request was closed',
      )
      return {
        handled: true,
        detail: 'closed, work stopped and teardown asked for',
        orgId: stored.orgId,
      }
    }
    default:
      return {
        handled: true,
        detail: `${action} recorded`,
        orgId: stored.orgId,
      }
  }
}

async function approveFork(
  deps: LifecycleDeps,
  login: string,
  pullRequestId: string,
  headSha: string,
  by: string | null,
): Promise<void> {
  await deps.pool.withGitHubAccount(login, async (db) => {
    // Fenced on the head. A label delivery that arrives after a push would
    // otherwise approve a commit whose contents the labeller never saw, which
    // is the ordering an attacker would arrange on purpose.
    await db.execute(sql`
      UPDATE pull_requests
      SET approved_sha = ${headSha}, approved_by = ${by}, approved_at = ${deps.clock.now().toISOString()},
          updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${pullRequestId}::uuid AND head_sha = ${headSha}`)
  })
}

async function withdrawFork(
  deps: LifecycleDeps,
  login: string,
  pullRequestId: string,
): Promise<void> {
  await deps.pool.withGitHubAccount(login, async (db) => {
    await db.execute(sql`
      UPDATE pull_requests
      SET approved_sha = NULL, approved_by = NULL, approved_at = NULL,
          updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${pullRequestId}::uuid`)
  })
}

// ---------------------------------------------------------------------------
// Starting and stopping work
// ---------------------------------------------------------------------------

/**
 * Makes sure there is exactly one generation for this head, and supersedes any
 * older one.
 *
 * The superseding is the important half and it happens BEFORE the new row
 * exists, so that at no point are two generations of one pull request open. An
 * old generation that finishes after this still writes its own check run, which
 * is correct: that check belongs to that commit. What it may not do is touch
 * the comment, and the fence in publishComment is what stops it.
 */
async function startGeneration(
  deps: LifecycleDeps,
  login: string,
  pullRequestId: string,
  headSha: string,
): Promise<string> {
  const deadlineMs = deps.deadlineMs ?? DEFAULT_DEADLINE_MS
  const now = deps.clock.now()

  type Started = {
    generationId: string
    superseded: { id: string; workflow_run_id: string | null }[]
    blocked: string | null
    repositoryId: string
  }
  const decision: { skip: string } | Started | null = await deps.pool.withGitHubAccount(
    login,
    async (db) => {
      const pr = await pullRequestById(db, pullRequestId)
      if (!pr) return null

      if (pr.draft) {
        return { skip: 'the pull request is a draft, so nothing was started' }
      }
      if (pr.state !== 'open') {
        return { skip: 'the pull request is not open, so nothing was started' }
      }

      // Anything open on another commit is finished as cancelled, and its
      // environment is asked for.
      const superseded = await db.execute<{
        id: string
        workflow_run_id: string | null
      }>(sql`
      UPDATE pr_generations
      SET state = 'cancelled',
          detail = ${`Superseded by ${shortSha(headSha)}.`},
          finished_at = ${now.toISOString()},
          updated_at = ${now.toISOString()},
          -- The callback dies with the generation. A job still running against
          -- the old commit cannot report a result for work nobody is waiting
          -- for, which is the same fence as the comment's, one layer down.
          callback_hash = NULL,
          callback_expires_at = NULL
      WHERE pull_request_id = ${pullRequestId}::uuid
        AND head_sha <> ${headSha}
        AND state IN ('queued', 'running')
      RETURNING id, workflow_run_id::text AS workflow_run_id`)

      // The fork gate. Blocked rather than refused, because refusing the delivery
      // would leave the pull request with no check at all and nothing saying why.
      const blocked =
        pr.from_fork && pr.approved_sha !== headSha
          ? `This pull request is from a fork, and ${shortSha(headSha)} has not been approved. ` +
            `A maintainer who has read this exact commit adds the \`${FORK_APPROVAL_LABEL}\` label, ` +
            `and the approval covers that commit alone: the next push withdraws it. ` +
            `A fork's own job cannot report a result whatever is granted, because GitHub gives ` +
            `it a read-only token and no workflow identity, so after approving, start a run from ` +
            `the console or from the Actions tab.`
          : null

      const rows = await db.execute<{
        id: string
        state: string
        attempt: number
      }>(sql`
      INSERT INTO pr_generations (
        org_id, pull_request_id, head_sha, state, detail, deadline_at, queued_at, updated_at)
      VALUES (
        ${pr.org_id}::uuid, ${pullRequestId}::uuid, ${headSha},
        ${blocked ? 'blocked' : 'queued'}::pr_generation_state, ${blocked},
        ${new Date(now.getTime() + deadlineMs).toISOString()}::timestamptz,
        ${now.toISOString()}::timestamptz, ${now.toISOString()}::timestamptz)
      -- A second delivery about the same head is the ordinary case, not an
      -- error: GitHub sends synchronize twice for one push often enough that
      -- treating it as a conflict would double every check.
      --
      -- Two states move back to queued here and no others. Blocked, because a
      -- fork approval arrives as a second delivery about the same commit, and
      -- an arm that did nothing would change the pull request row and leave the
      -- check saying blocked forever. Cancelled, because reopening a closed
      -- pull request is GitHub about to start a new run on the same commit, and
      -- a check that stayed cancelled would contradict it.
      --
      -- A generation that already has a VERDICT is left alone. Re-running one
      -- of those is the Re-run button, which bumps the attempt; quietly
      -- re-queueing a recorded pass or failure from an ordinary delivery would
      -- lose an answer somebody may already have read.
      ON CONFLICT (pull_request_id, head_sha) DO UPDATE SET
        state = CASE
          WHEN pr_generations.state IN ('blocked', 'cancelled') AND EXCLUDED.state = 'queued'
          THEN 'queued'::pr_generation_state
          ELSE pr_generations.state END,
        detail = CASE
          WHEN pr_generations.state IN ('blocked', 'cancelled') AND EXCLUDED.state = 'queued'
          THEN NULL ELSE pr_generations.detail END,
        finished_at = CASE
          WHEN pr_generations.state IN ('blocked', 'cancelled') AND EXCLUDED.state = 'queued'
          THEN NULL ELSE pr_generations.finished_at END,
        deadline_at = CASE
          WHEN pr_generations.state IN ('blocked', 'cancelled') AND EXCLUDED.state = 'queued'
          THEN EXCLUDED.deadline_at ELSE pr_generations.deadline_at END,
        updated_at = ${now.toISOString()}
      RETURNING id, state::text AS state, attempt`)

      return {
        generationId: rows[0]!.id,
        superseded,
        blocked,
        repositoryId: pr.repository_id,
      }
    },
  )

  if (!decision) return 'the pull request vanished between two statements'
  if ('skip' in decision) return decision.skip

  // The stopping happens outside the transaction, because it reaches GitHub.
  for (const old of decision.superseded) {
    await requestTeardownFor(deps, login, old.id, 'superseded by a newer commit')
    await publish(deps, login, old.id)
  }

  await publish(deps, login, decision.generationId)
  return decision.blocked ? 'blocked pending a fork approval' : 'a check is queued'
}

/**
 * Ends every open generation of a pull request, and asks for the environments.
 *
 * One function for close, merge, draft and withdrawal, because they are the
 * same action with a different sentence, and four copies of it is four places
 * to forget the teardown.
 */
async function stopWork(
  deps: LifecycleDeps,
  login: string,
  pullRequestId: string,
  reason: string,
): Promise<void> {
  const now = deps.clock.now()
  const stopped = await deps.pool.withGitHubAccount(login, async (db) =>
    db.execute<{ id: string }>(sql`
      UPDATE pr_generations
      SET state = 'cancelled',
          detail = ${`Stopped because ${reason}.`},
          finished_at = ${now.toISOString()},
          updated_at = ${now.toISOString()},
          callback_hash = NULL,
          callback_expires_at = NULL
      -- EVERY open generation of this pull request, not one commit's. There
      -- is only ever one open at a time, because starting a generation
      -- supersedes the others, but a pull request closing has to stop whatever
      -- is running rather than whatever the delivery happened to name: the
      -- close payload carries the head at close time, and a run started
      -- against an earlier commit is exactly the one nobody would think to
      -- stop.
      WHERE pull_request_id = ${pullRequestId}::uuid
        AND state IN ('queued', 'running')
      RETURNING id`),
  )

  for (const generation of stopped) {
    await requestTeardownFor(deps, login, generation.id, reason)
    await publish(deps, login, generation.id)
  }
}

// ---------------------------------------------------------------------------
// workflow_run
// ---------------------------------------------------------------------------

interface WorkflowRunPayload {
  action: string
  workflow_run?: {
    id?: number
    head_sha?: string
    status?: string
    conclusion?: string | null
  }
  repository?: { full_name?: string }
}

async function handleWorkflowRun(
  deps: LifecycleDeps,
  login: string,
  raw: Record<string, unknown>,
): Promise<DeliveryResult> {
  const payload = raw as unknown as WorkflowRunPayload
  const run = payload.workflow_run
  const repository = payload.repository?.full_name
  if (!repository || typeof run?.id !== 'number' || !run.head_sha) {
    return {
      handled: false,
      detail: 'no workflow run in the payload',
      orgId: null,
    }
  }
  const headSha = run.head_sha

  const found = await deps.pool.withGitHubAccount(login, async (db) => {
    const repo = await repositoryByName(db, repository)
    if (!repo) return null
    const rows = await db.execute<{ id: string; state: GenerationState }>(sql`
      SELECT g.id, g.state::text AS state
      FROM pr_generations g JOIN pull_requests p ON p.id = g.pull_request_id
      WHERE p.repository_id = ${repo.id}::uuid AND g.head_sha = ${headSha}
      ORDER BY g.queued_at DESC LIMIT 1`)
    const generation = rows[0]
    if (!generation) return { orgId: repo.org_id, generation: null }

    // The run id is bound to the generation the first time it is seen, and it
    // is what makes teardown possible: cancelling this run is the only route
    // this control plane has into the runtime holding the environment.
    await db.execute(sql`
      UPDATE pr_generations SET workflow_run_id = ${run.id}, updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${generation.id}::uuid AND workflow_run_id IS NULL`)
    return { orgId: repo.org_id, generation }
  })

  if (!found) {
    return {
      handled: false,
      detail: `${repository} is not connected`,
      orgId: null,
    }
  }
  if (!found.generation) {
    // A run for a commit with no generation: a push to a branch with no pull
    // request, or a workflow this control plane did not ask for. Recorded and
    // ignored rather than treated as an error.
    return {
      handled: true,
      detail: `no generation for ${shortSha(headSha)}`,
      orgId: found.orgId,
    }
  }
  const generationId = found.generation.id

  if (payload.action === 'in_progress' || payload.action === 'requested') {
    await markRunning(deps, login, generationId)
    await publish(deps, login, generationId)
    return { handled: true, detail: 'the run started', orgId: found.orgId }
  }

  if (payload.action !== 'completed') {
    return {
      handled: true,
      detail: `${payload.action} recorded`,
      orgId: found.orgId,
    }
  }

  // The run is over. If the job never reported, this is the moment the check
  // has to say so, and the answer is never `passed`.
  //
  // THIS IS THE DEFECT THIS WHOLE FEATURE EXISTS DOWNSTREAM OF. A green
  // workflow run means the job exited zero, and `af ci` exits zero on a run
  // that verified nothing. Pull request 49 demonstrated six blocked workflows
  // inside a successful job. Reading GitHub's conclusion as the verdict is
  // reading the exit code, one layer up.
  const conclusion = run.conclusion ?? 'unknown'
  const outcome = await deps.pool.withGitHubAccount(login, async (db) => {
    const rows = await db.execute<{ id: string; state: GenerationState }>(sql`
      SELECT id, state::text AS state FROM pr_generations WHERE id = ${generationId}::uuid`)
    const current = rows[0]
    if (!current) return 'the generation vanished'
    if (current.state !== 'queued' && current.state !== 'running') {
      // The job reported first, which is the ordinary ordering. What GitHub
      // says about the job does not overwrite what the job said about the code.
      return `already ${current.state}`
    }

    const { state, detail } = unreportedOutcome(conclusion)
    await db.execute(sql`
      UPDATE pr_generations
      SET state = ${state}::pr_generation_state, detail = ${detail},
          finished_at = ${deps.clock.now().toISOString()},
          updated_at = ${deps.clock.now().toISOString()},
          callback_hash = NULL, callback_expires_at = NULL
      WHERE id = ${generationId}::uuid`)
    return `the run ended ${conclusion} and reported nothing: ${state}`
  })

  await requestTeardownFor(deps, login, generationId, 'the workflow run finished')
  await publish(deps, login, generationId)
  return { handled: true, detail: outcome, orgId: found.orgId }
}

/** What a finished run that never reported means. Never `passed`. */
function unreportedOutcome(conclusion: string): {
  state: GenerationState
  detail: string
} {
  switch (conclusion) {
    case 'cancelled':
      return {
        state: 'cancelled',
        detail: 'The workflow run was cancelled before Antifailure reported anything.',
      }
    case 'success':
      return {
        state: 'unverified',
        detail:
          'The workflow run finished successfully and Antifailure never reported a result, so ' +
          'nothing was verified. A job that exits zero without running the check is the failure ' +
          'this state exists to make visible. Check that the workflow runs `af ci` and posts its ' +
          'report.',
      }
    default:
      return {
        state: 'blocked',
        detail:
          `The workflow run ended ${conclusion} before Antifailure reported anything, so the ` +
          'code was never checked. Read the job log: this is a failure of the run rather than a ' +
          'finding about the change.',
      }
  }
}

async function markRunning(
  deps: LifecycleDeps,
  login: string,
  generationId: string,
): Promise<void> {
  await deps.pool.withGitHubAccount(login, async (db) => {
    await db.execute(sql`
      UPDATE pr_generations
      SET state = 'running', started_at = coalesce(started_at, ${deps.clock.now().toISOString()}),
          updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${generationId}::uuid AND state = 'queued'`)
  })
}

// ---------------------------------------------------------------------------
// check_run: the Re-run button
// ---------------------------------------------------------------------------

interface CheckRunPayload {
  action: string
  check_run?: { id?: number; head_sha?: string }
  check_suite?: { id?: number; head_sha?: string }
  repository?: { full_name?: string }
}

/**
 * The Re-run button, in both of its shapes.
 *
 * GitHub has two. Re-run on one check sends `check_run` rerequested, and
 * "Re-run all checks" on the checks page sends `check_suite` rerequested.
 * Handling only the first leaves the button most people press doing nothing at
 * all, with no error anywhere, which is the shape of defect this repository
 * keeps finding. Both carry the head commit and that is all this needs.
 */
async function handleRerequest(
  deps: LifecycleDeps,
  login: string,
  raw: Record<string, unknown>,
): Promise<DeliveryResult> {
  const payload = raw as unknown as CheckRunPayload
  if (payload.action !== 'rerequested') {
    return {
      handled: true,
      detail: `${payload.action} is not acted on`,
      orgId: null,
    }
  }
  const repository = payload.repository?.full_name
  // Either shape. check_run carries the commit for one check, check_suite for
  // all of them, and both name the same commit.
  const headSha = payload.check_run?.head_sha ?? payload.check_suite?.head_sha
  if (!repository || !headSha) {
    return {
      handled: false,
      detail: 'no check run in the payload',
      orgId: null,
    }
  }

  const reopened = await deps.pool.withGitHubAccount(login, async (db) => {
    const repo = await repositoryByName(db, repository)
    if (!repo) return null
    const rows = await db.execute<{
      id: string
      workflow_run_id: string | null
      attempt: number
    }>(sql`
      UPDATE pr_generations g
      SET state = 'queued', detail = NULL, verdict = NULL, env_id = NULL,
          attempt = g.attempt + 1,
          started_at = NULL, finished_at = NULL,
          deadline_at = ${new Date(
            deps.clock.now().getTime() + (deps.deadlineMs ?? DEFAULT_DEADLINE_MS),
          ).toISOString()}::timestamptz,
          updated_at = ${deps.clock.now().toISOString()},
          callback_hash = NULL, callback_expires_at = NULL
      FROM pull_requests p
      WHERE p.id = g.pull_request_id
        AND p.repository_id = ${repo.id}::uuid
        AND g.head_sha = ${headSha}
      RETURNING g.id, g.workflow_run_id::text AS workflow_run_id, g.attempt`)
    return {
      orgId: repo.org_id,
      row: rows[0] ?? null,
      installationId: await installationFor(db, repository),
    }
  })

  if (!reopened)
    return {
      handled: false,
      detail: `${repository} is not connected`,
      orgId: null,
    }
  if (!reopened.row) {
    return {
      handled: true,
      detail: `nothing to re-run for ${shortSha(headSha)}`,
      orgId: reopened.orgId,
    }
  }

  let detail = `attempt ${reopened.row.attempt} queued`
  const runId = reopened.row.workflow_run_id
  if (runId && reopened.installationId) {
    // Re-running the RUN, not dispatching the workflow. A dispatch names a ref
    // and a ref moves, so somebody pressing Re-run on an older commit's check
    // would get a run against whatever the branch points at now, reported under
    // the commit they asked about.
    try {
      await deps.api.rerunWorkflowRun(reopened.installationId, repository, Number(runId))
    } catch (err) {
      detail = describeApiFailure(err, `ask GitHub to re-run workflow run ${runId}`)
    }
  } else {
    detail =
      'this commit has no workflow run to re-run, so the check is queued and nothing was started'
  }

  await publish(deps, login, reopened.row.id)
  return { handled: true, detail, orgId: reopened.orgId }
}

// ---------------------------------------------------------------------------
// The job reporting back
// ---------------------------------------------------------------------------

export interface IncomingReport {
  headSha: string
  /** The Markdown `af ci --report` wrote, for the comment. */
  markdown: string | null
  /** The run as `af ci --report-json` wrote it. Decoded tolerantly. */
  report: unknown
}

export interface ReportOutcome {
  state: GenerationState
  detail: string
}

/**
 * Records what a job said about one commit.
 *
 * The generation is found by the callback credential rather than by anything in
 * the body, so a job cannot report about a commit it was not issued a
 * credential for. The head SHA in the body is checked anyway, because a job
 * reporting a different commit from the one it was given is a bug worth naming
 * rather than a request worth serving.
 */
export async function recordReport(
  deps: LifecycleDeps,
  callbackHash: Buffer,
  incoming: IncomingReport,
): Promise<{
  status: 'recorded' | 'refused'
  detail: string
  state?: GenerationState
}> {
  const now = deps.clock.now()
  const decoded = decodeReport(incoming.report)
  const state = stateFromReport(decoded.counts)

  // ONE TABLE ON THIS CONNECTION. The callback declaration in migration 0021
  // makes exactly the generation row reachable, and nothing else: pull_requests
  // and repositories carry no policy keyed on a callback, so a join to them
  // here would return no rows and read as "that credential is not valid",
  // which is the most misleading possible answer to a correct credential.
  // Everything past this point runs scoped to the tenant the generation names.
  const found = await deps.pool.withPullRequestCallback(callbackHash, async (db) => {
    const rows = await db.execute<{
      id: string
      org_id: string
      head_sha: string
      state: GenerationState
      callback_expires_at: Date | string | null
    }>(sql`
      SELECT id, org_id, head_sha, state::text AS state, callback_expires_at
      FROM pr_generations WHERE callback_hash = ${callbackHash}`)
    return rows[0] ?? null
  })

  if (!found) {
    return {
      status: 'refused',
      detail:
        'That credential does not belong to any commit this control plane is waiting on. It was ' +
        'issued for one commit and is withdrawn when the head moves, the pull request closes, or ' +
        'the run is superseded.',
    }
  }
  if (found.state !== 'queued' && found.state !== 'running') {
    // One report per commit. A second one is either a leaked credential being
    // used to rewrite a result somebody has already read, or a job retrying
    // after its answer was recorded, and neither may overwrite the first.
    return {
      status: 'refused',
      detail: `${shortSha(found.head_sha)} already has a result: ${found.state}.`,
    }
  }
  if (found.callback_expires_at && new Date(found.callback_expires_at).getTime() <= now.getTime()) {
    return {
      status: 'refused',
      detail: 'That credential has expired. Ask for a new one.',
    }
  }
  if (found.head_sha !== incoming.headSha) {
    return {
      status: 'refused',
      detail:
        `That credential was issued for ${shortSha(found.head_sha)} and the report is about ` +
        `${shortSha(incoming.headSha)}. A result belongs to one commit.`,
    }
  }

  await deps.pool.withPullRequestCallback(callbackHash, async (db) => {
    await db.execute(sql`
      UPDATE pr_generations
      SET state = ${state}::pr_generation_state,
          detail = ${decoded.headline},
          env_id = ${decoded.environment},
          verdict = ${JSON.stringify({
            counts: decoded.counts,
            environment: decoded.environment,
            url: decoded.url,
            duration: decoded.duration,
            markdown: (incoming.markdown ?? '').slice(0, 60_000),
          })}::jsonb,
          finished_at = ${now.toISOString()},
          updated_at = ${now.toISOString()},
          -- SPENT BY EXPIRY, not by clearing the hash. Clearing it is what a
          -- superseded generation does, on a connection scoped to the account,
          -- and it cannot be done from HERE: this connection reaches this row
          -- only because the stored hash matches the credential it declared, so
          -- a statement that removed the hash would be writing a row its own
          -- policy can no longer admit. Expiring it refuses a second report
          -- just as firmly, and the state check above refuses it first.
          callback_expires_at = ${now.toISOString()}
      WHERE id = ${found.id}::uuid`)
  })

  const login = await accountLoginFor(deps, found.org_id)
  if (login) await publish(deps, login, found.id)
  return { status: 'recorded', detail: `recorded as ${state}`, state }
}

interface DecodedReport {
  counts: ReportCounts
  environment: string | null
  url: string | null
  duration: string | null
  headline: string | null
}

/**
 * Reads an engine report tolerantly.
 *
 * It crosses a version boundary: the engine that wrote it may be older or newer
 * than this control plane. So one element that does not decode is skipped and
 * the rest is kept, rather than the whole report being discarded, because a
 * report discarded over one malformed field is a pull request with no answer
 * on it. A verdict word this control plane does not know counts as unverified,
 * never as passed.
 */
export function decodeReport(value: unknown): DecodedReport {
  const run = (value ?? {}) as Record<string, unknown>
  const counts: ReportCounts = {
    passed: 0,
    failed: 0,
    flaky: 0,
    blocked: 0,
    unverified: 0,
  }

  const workflows = Array.isArray(run.Workflows) ? run.Workflows : []
  for (const item of workflows) {
    const workflow = item as { Verdict?: unknown }
    switch (workflow?.Verdict) {
      case 'pass':
        counts.passed += 1
        break
      case 'fail':
        counts.failed += 1
        break
      case 'flaky':
        counts.flaky += 1
        break
      case 'blocked':
        counts.blocked += 1
        break
      default:
        // Including `unverified`, and including anything this control plane has
        // never heard of. The safe direction for an unknown answer about
        // whether software works is "we do not know".
        counts.unverified += 1
    }
  }

  // An invariant that was shown to be broken is a failure of the change, and it
  // is the one this product leads with. A report whose workflows all passed and
  // whose invariant did not hold must not read as a pass.
  const invariants = Array.isArray(run.Invariants) ? run.Invariants : []
  for (const item of invariants) {
    const invariant = item as { Held?: unknown; Error?: unknown }
    if (typeof invariant?.Error === 'string' && invariant.Error !== '') {
      counts.unverified += 1
    } else if (invariant?.Held === false) {
      counts.failed += 1
    }
  }

  return {
    counts,
    environment: text(run.Environment),
    url: text(run.URL),
    duration: text(run.Duration),
    headline: headlineFor(counts),
  }
}

function headlineFor(counts: ReportCounts): string {
  const parts = [
    `${counts.passed} passed`,
    `${counts.failed} failed`,
    `${counts.flaky} flaky`,
    `${counts.blocked} blocked`,
    `${counts.unverified} unverified`,
  ]
  return parts.join(', ') + '.'
}

function text(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null
}

// ---------------------------------------------------------------------------
// Publishing
// ---------------------------------------------------------------------------

interface PublishState {
  generation: GenerationRow
  pullRequest: PullRequestRow
  repository: string
  installationId: number | null
  teardown: TeardownState
}

/**
 * Puts the current state of one generation on GitHub: the check, then the
 * comment.
 *
 * The check first, because it is the thing a branch rule blocks on and the
 * comment is the thing a person reads. If the installation does not hold
 * `checks: write`, the check is skipped and the comment says so, rather than
 * the whole publish failing: `pull-requests: write` IS granted today and a
 * pull request with an explanation on it beats a pull request with silence.
 *
 * Nothing here throws. A publish that failed would fail the delivery, GitHub
 * would retry it, and the retry would be refused by the delivery ledger as a
 * replay, so a transient GitHub error would turn into a permanently
 * unpublished result.
 */
export async function publish(
  deps: LifecycleDeps,
  login: string,
  generationId: string,
): Promise<{ published: boolean; detail: string }> {
  const state = await loadPublishState(deps, login, generationId)
  if (!state) return { published: false, detail: 'no such generation' }
  if (state.installationId === null) {
    return {
      published: false,
      detail: 'this account has no active installation',
    }
  }

  const timedOut = timedOutFor(state.generation)
  const verdict = (state.generation.verdict ?? {}) as Record<string, unknown>
  const input = {
    state: state.generation.state,
    timedOut,
    headSha: state.generation.head_sha,
    attempt: state.generation.attempt,
    detail: state.generation.detail,
    reportMarkdown:
      typeof verdict.markdown === 'string' && verdict.markdown ? verdict.markdown : null,
    envId: state.generation.env_id,
    teardown: state.teardown,
    consoleBase: deps.consoleBase,
    checksUnavailable: null as string | null,
  }

  let checksUnavailable: string | null = null
  try {
    await publishCheck(deps, login, state, input)
  } catch (err) {
    if (err instanceof GitHubPermissionError) {
      checksUnavailable = err.remedy
    } else {
      checksUnavailable = `GitHub refused: ${describeApiFailure(err, 'publish a check run')}`
    }
  }

  const comment = await publishComment(deps, login, state, {
    ...input,
    checksUnavailable,
  })
  return {
    published: true,
    detail: checksUnavailable ? `comment only (${comment})` : `check and comment (${comment})`,
  }
}

async function loadPublishState(
  deps: LifecycleDeps,
  login: string,
  generationId: string,
): Promise<PublishState | null> {
  return deps.pool.withGitHubAccount(login, async (db) => {
    const generation = await generationById(db, generationId)
    if (!generation) return null
    const pullRequest = await pullRequestById(db, generation.pull_request_id)
    if (!pullRequest) return null
    const repos = await db.execute<{ full_name: string }>(sql`
      SELECT full_name FROM repositories WHERE id = ${pullRequest.repository_id}::uuid`)
    const repository = repos[0]?.full_name
    if (!repository) return null

    const teardownRows = await db.execute<{ state: string }>(sql`
      SELECT state FROM teardown_requests
      WHERE generation_id = ${generationId}::uuid
      ORDER BY requested_at DESC LIMIT 1`)
    const teardown = (teardownRows[0]?.state ?? 'none') as TeardownState

    return {
      generation,
      pullRequest,
      repository,
      installationId: await installationFor(db, repository),
      teardown,
    }
  })
}

function timedOutFor(generation: GenerationRow): boolean {
  return generation.state === 'unverified' && generation.detail === TIMED_OUT_DETAIL
}

async function publishCheck(
  deps: LifecycleDeps,
  login: string,
  state: PublishState,
  input: Parameters<typeof checkOutputFor>[0],
): Promise<void> {
  const installationId = state.installationId!
  const shape = checkShapeFor(state.generation.state, input.timedOut)
  const payload = {
    name: CHECK_NAME,
    headSha: state.generation.head_sha,
    status: shape.status,
    ...(shape.conclusion ? { conclusion: shape.conclusion } : {}),
    ...(deps.consoleBase
      ? {
          detailsUrl: `${deps.consoleBase.replace(/\/+$/, '')}/runs?commit=${encodeURIComponent(
            state.generation.head_sha,
          )}`,
        }
      : {}),
    ...(shape.status === 'completed' ? { completedAt: deps.clock.now().toISOString() } : {}),
    output: checkOutputFor(input),
  }

  // Cached id, then GitHub's own answer, then create. The middle step is what
  // makes two concurrent publishes converge on one run instead of creating two,
  // and it is also what recovers after a database restore that lost the id.
  let checkRunId = state.generation.check_run_id ? Number(state.generation.check_run_id) : null
  if (checkRunId === null) {
    checkRunId = await deps.api.findCheckRun(
      installationId,
      state.repository,
      state.generation.head_sha,
      CHECK_NAME,
    )
  }

  if (checkRunId === null) {
    checkRunId = await deps.api.createCheckRun(installationId, state.repository, payload)
  } else {
    await deps.api.updateCheckRun(installationId, state.repository, checkRunId, payload)
  }

  await deps.pool.withGitHubAccount(login, async (db) => {
    await db.execute(sql`
      UPDATE pr_generations SET check_run_id = ${checkRunId}, updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${state.generation.id}::uuid`)
  })
}

/**
 * The one comment, and the fence that keeps it about the current head.
 *
 * A generation whose head is not the pull request's head does not write. That
 * is the whole compare-and-set, and it is the difference between a comment that
 * is occasionally wrong in a way nobody can detect and one that is right.
 */
async function publishComment(
  deps: LifecycleDeps,
  login: string,
  state: PublishState,
  input: Parameters<typeof commentBody>[0],
): Promise<string> {
  if (state.generation.head_sha !== state.pullRequest.head_sha) {
    return `not written: ${shortSha(state.generation.head_sha)} is no longer the head`
  }
  const installationId = state.installationId!
  const body = commentBody(input)

  try {
    const existing = await deps.api.findComment(
      installationId,
      state.repository,
      state.pullRequest.number,
      COMMENT_MARKER,
    )

    if (existing) {
      // The comment on GitHub carries the head it reports. A writer whose head
      // is older than what is already there does not overwrite it, even though
      // the check above said this generation IS the head: the two reads are not
      // atomic, and between them somebody can push.
      const published = shaOfComment(existing.body)
      if (published && published !== state.generation.head_sha) {
        const fresher = await deps.pool.withGitHubAccount(login, async (db) => {
          const rows = await db.execute<{ head_sha: string }>(sql`
            SELECT head_sha FROM pull_requests WHERE id = ${state.pullRequest.id}::uuid`)
          return rows[0]?.head_sha ?? null
        })
        if (fresher && fresher !== state.generation.head_sha) {
          return `not written: the head moved to ${shortSha(fresher)} while this was being written`
        }
      }
      await deps.api.updateComment(installationId, state.repository, existing.id, body)
      await rememberComment(deps, login, state, existing.id)
      return 'updated'
    }

    const id = await deps.api.createComment(
      installationId,
      state.repository,
      state.pullRequest.number,
      body,
    )
    await rememberComment(deps, login, state, id)
    return 'created'
  } catch (err) {
    return `not written: ${describeApiFailure(err, 'write the comment')}`
  }
}

async function rememberComment(
  deps: LifecycleDeps,
  login: string,
  state: PublishState,
  commentId: number,
): Promise<void> {
  await deps.pool.withGitHubAccount(login, async (db) => {
    // Fenced in the statement, so two writers racing cannot leave the row
    // claiming an older head than the comment actually reports.
    await db.execute(sql`
      UPDATE pull_requests
      SET comment_id = ${commentId}, comment_sha = ${state.generation.head_sha},
          comment_updated_at = ${deps.clock.now().toISOString()},
          updated_at = ${deps.clock.now().toISOString()}
      WHERE id = ${state.pullRequest.id}::uuid AND head_sha = ${state.generation.head_sha}`)
  })
}

// ---------------------------------------------------------------------------
// The callback credential
// ---------------------------------------------------------------------------

export function hashCallback(token: string): Buffer {
  return createHash('sha256').update(token, 'utf8').digest()
}

/**
 * Issues a credential scoped to one generation.
 *
 * Refused for a fork commit nobody has approved. That refusal is the second
 * half of "a fork stays secretless"; the first half is GitHub's, which withholds
 * secrets and the workflow identity token from a fork's pull request job
 * entirely. Both are real and neither is sufficient alone: GitHub's covers the
 * repository's own secrets, and this covers the credential this control plane
 * would otherwise hand out.
 *
 * Neither covers the third thing, and this comment used to read as though there
 * were only two. Both halves above are about what GitHub hands a job. Neither
 * says anything about what is already sitting on the machine the job runs on: on
 * a self hosted runner the Docker daemon, an existing registry login and the
 * network are ambient, and GitHub withholds none of them from a fork's code. The
 * engine's own fork gate is what covers that, and it has to, because this control
 * plane never sees that machine.
 */
export async function issueCallback(
  deps: LifecycleDeps,
  login: string,
  claim: {
    repository: string
    headSha: string
    workflowRunId: number | null
    /** Which workflow, and which attempt of it, out of the verified identity.
     *  Recorded HERE rather than when the report arrives, because the report
     *  presents the credential this call issues and not the identity that
     *  earned it: a reader of the row learns who wrote it rather than who it
     *  claims to be. */
    reportedBy: string
  },
): Promise<{ token: string; generationId: string } | { refused: string }> {
  const token = randomBytes(32).toString('base64url')
  const hash = hashCallback(token)
  const now = deps.clock.now()

  const result: { refused: string } | { generationId: string } = await deps.pool.withGitHubAccount(
    login,
    async (db) => {
      const repo = await repositoryByName(db, claim.repository)
      if (!repo)
        return {
          refused: `${claim.repository} is not connected to this control plane`,
        }

      const rows = await db.execute<{
        id: string
        state: GenerationState
        from_fork: boolean
        approved_sha: string | null
        pr_head: string
      }>(sql`
      SELECT g.id, g.state::text AS state, p.from_fork, p.approved_sha, p.head_sha AS pr_head
      FROM pr_generations g JOIN pull_requests p ON p.id = g.pull_request_id
      WHERE p.repository_id = ${repo.id}::uuid AND g.head_sha = ${claim.headSha}`)
      const generation = rows[0]
      if (!generation) {
        return { refused: `no check is waiting on ${shortSha(claim.headSha)}` }
      }
      if (generation.state !== 'queued' && generation.state !== 'running') {
        return {
          refused: `the check on ${shortSha(claim.headSha)} is already ${generation.state}`,
        }
      }
      if (generation.from_fork && generation.approved_sha !== claim.headSha) {
        return {
          refused:
            `${shortSha(claim.headSha)} is from a fork and has not been approved. A maintainer who ` +
            `has read this exact commit adds the \`${FORK_APPROVAL_LABEL}\` label.`,
        }
      }

      await db.execute(sql`
      UPDATE pr_generations
      SET callback_hash = ${hash},
          callback_expires_at = ${new Date(now.getTime() + CALLBACK_TTL_MS).toISOString()}::timestamptz,
          reported_by = ${claim.reportedBy.slice(0, 400)},
          workflow_run_id = coalesce(workflow_run_id, ${claim.workflowRunId}),
          state = CASE WHEN state = 'queued' THEN 'running' ELSE state END,
          started_at = coalesce(started_at, ${now.toISOString()}),
          updated_at = ${now.toISOString()}
      WHERE id = ${generation.id}::uuid`)
      return { generationId: generation.id }
    },
  )

  if ('refused' in result) return result
  return { token, generationId: result.generationId }
}

// ---------------------------------------------------------------------------
// Teardown
// ---------------------------------------------------------------------------

/** Asks for the environment behind a generation to be removed.
 *
 *  Not exported. The console's teardown verb writes its own row, because it
 *  runs on a connection scoped to the tenant and this one runs on a connection
 *  scoped to the GitHub account, and a function that had to work under both
 *  would take the scope as an argument, which is a seam nobody needs yet. */
async function requestTeardownFor(
  deps: LifecycleDeps,
  login: string,
  generationId: string,
  reason: string,
): Promise<void> {
  await deps.pool.withGitHubAccount(login, async (db) => {
    const rows = await db.execute<{
      org_id: string
      env_id: string | null
      workflow_run_id: string | null
      repository_id: string
    }>(sql`
      SELECT g.org_id, g.env_id, g.workflow_run_id::text AS workflow_run_id, p.repository_id
      FROM pr_generations g JOIN pull_requests p ON p.id = g.pull_request_id
      WHERE g.id = ${generationId}::uuid`)
    const row = rows[0]
    if (!row) return
    if (!row.env_id && !row.workflow_run_id) {
      // Nothing to reach. Recording a request nothing can act on would put
      // "teardown pending" on a pull request forever, which reads as a leak
      // rather than as there being nothing to remove.
      return
    }
    await db.execute(sql`
      INSERT INTO teardown_requests (
        org_id, env_id, repository_id, workflow_run_id, generation_id, reason, requested_at, updated_at)
      SELECT ${row.org_id}::uuid, ${row.env_id}, ${row.repository_id}::uuid,
             ${row.workflow_run_id}::bigint, ${generationId}::uuid, ${reason.slice(0, 200)},
             ${deps.clock.now().toISOString()}::timestamptz, ${deps.clock.now().toISOString()}::timestamptz
      -- One live request per generation. Four deliveries can each decide the
      -- work should stop, and four teardown requests for one environment is
      -- four cancels and three confusing rows.
      WHERE NOT EXISTS (
        SELECT 1 FROM teardown_requests t
        WHERE t.generation_id = ${generationId}::uuid AND t.state IN ('pending', 'leased'))`)
  })
}

export interface TeardownSweep {
  acknowledged: number
  retried: number
  abandoned: number
}

/**
 * Works through the teardown queue.
 *
 * A LEASE, not a flag, and the takeover is the point: a process that dies
 * holding a request leaves a row the next pass takes when the lease expires,
 * rather than a row nobody may touch again.
 *
 * ACKNOWLEDGED MEANS THE RUNTIME SAID SO. The workflow run reached a terminal
 * status at GitHub, or the engine reported the environment torn down. A cancel
 * that was accepted is not an acknowledgement: GitHub records the request and
 * the run stops some time later, so a pass that acknowledged on the accepted
 * cancel would be marking a row and calling it cleanup, which is the exact
 * defect this queue exists to fix.
 */
export async function sweepTeardowns(deps: LifecycleDeps): Promise<TeardownSweep> {
  const now = deps.clock.now()
  const holder = deps.holder ?? 'control-plane'
  const sweep: TeardownSweep = { acknowledged: 0, retried: 0, abandoned: 0 }

  // Two steps, and the split is not tidiness. A sweeper has no tenant, so on
  // that connection the only policy that admits anything is the narrow read in
  // migration 0021: due rows, SELECT, nothing else. It cannot write, and it
  // cannot see the repository the request names. So it reads WHICH work is due
  // here and does everything else on a connection scoped to that work's own
  // organization, where the ordinary tenant policy applies.
  //
  // The precedent is migration 0016. sweepDeviceAuthorizations ran for the life
  // of the process and deleted zero rows, forever, because every policy on that
  // table keyed on a value the sweeper did not declare, and a DELETE that
  // matches nothing reports success.
  const due = await deps.pool.withSweeper(async (db) =>
    db.execute<{ id: string; org_id: string }>(sql`
      SELECT id, org_id FROM teardown_requests
      WHERE state IN ('pending', 'leased')
        AND (leased_until IS NULL OR leased_until < ${now.toISOString()}::timestamptz)
      ORDER BY requested_at
      LIMIT 20`),
  )

  const claimed: {
    id: string
    org_id: string
    env_id: string | null
    workflow_run_id: string | null
    attempts: number
    repository: string | null
  }[] = []

  for (const candidate of due) {
    // The claim is a compare-and-set rather than a lock held across the work.
    // Two replicas sweeping at the same instant is the ordinary case, and the
    // one that loses this UPDATE gets no row back and moves on.
    const taken = await deps.pool.withTenant({ orgId: candidate.org_id }, async (db) => {
      const rows = await db.execute<{
        id: string
        org_id: string
        env_id: string | null
        workflow_run_id: string | null
        attempts: number
        repository_id: string | null
      }>(sql`
        UPDATE teardown_requests
        SET state = 'leased', lease_holder = ${holder},
            leased_until = ${new Date(now.getTime() + TEARDOWN_LEASE_MS).toISOString()}::timestamptz,
            attempts = attempts + 1,
            updated_at = ${now.toISOString()}
        WHERE id = ${candidate.id}::uuid
          AND state IN ('pending', 'leased')
          AND (leased_until IS NULL OR leased_until < ${now.toISOString()}::timestamptz)
        RETURNING id, org_id, env_id, workflow_run_id::text AS workflow_run_id,
                  attempts, repository_id::text AS repository_id`)
      const row = rows[0]
      if (!row) return null
      // The repository is read separately rather than joined into the UPDATE.
      // A LEFT JOIN there produces one row per repository when repository_id is
      // null, which is an ambiguous update and several RETURNING rows for one
      // request.
      let repository: string | null = null
      if (row.repository_id) {
        const repos = await db.execute<{ full_name: string }>(sql`
          SELECT full_name FROM repositories WHERE id = ${row.repository_id}::uuid`)
        repository = repos[0]?.full_name ?? null
      }
      return { ...row, repository }
    })
    if (taken) claimed.push(taken)
  }

  for (const request of claimed) {
    const outcome = await attemptTeardown(deps, request)
    if (outcome.done) {
      sweep.acknowledged += 1
      await finishTeardown(deps, request.id, request.org_id, request.env_id, 'acknowledged', null)
      continue
    }
    if (request.attempts >= TEARDOWN_ATTEMPTS) {
      sweep.abandoned += 1
      await finishTeardown(
        deps,
        request.id,
        request.org_id,
        request.env_id,
        'abandoned',
        outcome.error,
      )
      continue
    }
    sweep.retried += 1
    // Scoped to the tenant, like every other write here. The sweeper's own
    // connection may only READ its queue: the policy in migration 0021 is FOR
    // SELECT, deliberately, so an UPDATE on it matches nothing and reports
    // success. This was written as withoutTenant and the lease was never given
    // back, which is migration 0016's defect exactly, one release later.
    await deps.pool.withTenant({ orgId: request.org_id }, async (db) => {
      await db.execute(sql`
        UPDATE teardown_requests
        SET state = 'pending', lease_holder = NULL, leased_until = NULL,
            last_error = ${outcome.error}, updated_at = ${now.toISOString()}
        WHERE id = ${request.id}::uuid`)
    })
  }

  return sweep
}

async function attemptTeardown(
  deps: LifecycleDeps,
  request: {
    id: string
    org_id: string
    env_id: string | null
    workflow_run_id: string | null
    repository: string | null
  },
): Promise<{ done: boolean; error: string | null }> {
  // The engine's own word first. An environment the engine has already reported
  // torn down is gone whatever GitHub says about the job, and asking GitHub
  // first would cancel a run that had already finished cleanly.
  if (request.env_id) {
    const alreadyGone = await deps.pool.withTenant({ orgId: request.org_id }, async (db) => {
      const rows = await db.execute<{ state: string }>(sql`
        SELECT state::text AS state FROM environments WHERE env_id = ${request.env_id}`)
      return rows[0]?.state === 'torn_down'
    })
    if (alreadyGone) return { done: true, error: null }
  }

  if (!request.workflow_run_id || !request.repository) {
    return {
      done: false,
      error:
        'There is no workflow run holding this environment, so this control plane has no route ' +
        'to the machine it is on. It holds no cluster credential and no address by design. Run ' +
        '`af down` on that branch, or `af env prune` on the machine that built it.',
    }
  }

  const installationId = await deps.pool.withTenant({ orgId: request.org_id }, async (db) =>
    installationFor(db, request.repository!),
  )
  if (installationId === null) {
    return {
      done: false,
      error: 'this account has no active GitHub App installation',
    }
  }

  try {
    const run = await deps.api.workflowRun(
      installationId,
      request.repository,
      Number(request.workflow_run_id),
    )
    if (run === null) {
      // GitHub has no such run. It was deleted along with its logs, and a run
      // that no longer exists is not running.
      return { done: true, error: null }
    }
    if (run.status === 'completed') {
      // The job ended, and `af ci` tears down on every exit including a
      // cancelled one. This is the acknowledgement.
      return { done: true, error: null }
    }
    await deps.api.cancelWorkflowRun(
      installationId,
      request.repository,
      Number(request.workflow_run_id),
    )
    return {
      done: false,
      error: 'the cancel was accepted and the run has not reached a terminal state yet',
    }
  } catch (err) {
    return {
      done: false,
      error: describeApiFailure(err, 'reach the workflow run'),
    }
  }
}

async function finishTeardown(
  deps: LifecycleDeps,
  id: string,
  orgId: string,
  envId: string | null,
  state: 'acknowledged' | 'abandoned',
  error: string | null,
): Promise<void> {
  const now = deps.clock.now()
  await deps.pool.withTenant({ orgId }, async (db) => {
    await db.execute(sql`
      UPDATE teardown_requests
      SET state = ${state}, lease_holder = NULL, leased_until = NULL,
          last_error = ${error}, acknowledged_at = ${state === 'acknowledged' ? now.toISOString() : null},
          updated_at = ${now.toISOString()}
      WHERE id = ${id}::uuid`)

    // The environments row moves ONLY on an acknowledgement. It used to move on
    // the request, which is how the console's teardown button changed a word on
    // a page while the containers kept running.
    if (state === 'acknowledged' && envId) {
      await db.execute(sql`
        UPDATE environments
        SET state = 'torn_down', torn_down_at = coalesce(torn_down_at, ${now.toISOString()}),
            updated_at = ${now.toISOString()}
        WHERE env_id = ${envId} AND state <> 'torn_down'`)
    }
  })
}

// ---------------------------------------------------------------------------
// The deadline
// ---------------------------------------------------------------------------

/**
 * Finishes generations that never said anything.
 *
 * A check that spins for a week is worse than one that gives up, because
 * nobody can tell it from a slow one, and a required check that never
 * concludes holds a merge forever with no explanation.
 */
export async function sweepGenerations(deps: LifecycleDeps): Promise<{ timedOut: number }> {
  const now = deps.clock.now()
  // Read on the connection with no tenant, write on the one scoped to the
  // account. The narrow policy in migration 0021 lets a sweeper SEE its own
  // queue and nothing else; everything the lifecycle writes goes through the
  // account scope the delivery policies are written against. Doing the UPDATE
  // here instead would match zero rows and report success, which is migration
  // 0016's defect exactly.
  const due = await deps.pool.withSweeper(async (db) =>
    db.execute<{ id: string; org_id: string }>(sql`
      SELECT id, org_id FROM pr_generations
      WHERE state IN ('queued', 'running') AND deadline_at < ${now.toISOString()}::timestamptz
      ORDER BY deadline_at
      LIMIT 50`),
  )

  let timedOut = 0
  for (const candidate of due) {
    const login = await accountLoginFor(deps, candidate.org_id)
    if (!login) continue
    const moved = await deps.pool.withGitHubAccount(login, async (db) =>
      db.execute<{ id: string }>(sql`
        UPDATE pr_generations
        SET state = 'unverified',
            detail = ${TIMED_OUT_DETAIL},
            finished_at = ${now.toISOString()},
            updated_at = ${now.toISOString()},
            callback_hash = NULL, callback_expires_at = NULL
        WHERE id = ${candidate.id}::uuid AND state IN ('queued', 'running')
        RETURNING id`),
    )
    if (moved.length === 0) continue
    timedOut += 1
    await requestTeardownFor(deps, login, candidate.id, 'the check timed out')
    await publish(deps, login, candidate.id)
  }
  return { timedOut }
}

// ---------------------------------------------------------------------------

/** One sentence about a GitHub failure, with no status code and no JSON body.
 *  Both of those reach a pull request comment, where they help nobody. */
function describeApiFailure(err: unknown, what: string): string {
  if (err instanceof GitHubPermissionError) return err.message
  if (err instanceof GitHubApiError) {
    return `GitHub would not ${what}. ${err.message}`
  }
  return `Could not ${what}: ${err instanceof Error ? err.message : String(err)}`
}
