// The Product lane of the operator portal: twins, the runs on them, the
// branches they belong to, and the golden data they were built from.
//
// WHAT THIS FILE IS FOR. Four of the five product sections ask a question about
// somebody else's tenant that the customer's own console cannot answer, because
// answering it means reading across every organization at once: which twins are
// alive right now, which runs are failing, which branches are holding an
// environment open after their pull request closed, and whether a customer's
// golden data was ever verified. Every one of those is a cross tenant read, so
// every one of them goes through ctx.adminDb and none of them through ctx.pool.
//
// THE FIFTH SECTION IS NOT HERE, and that is the point of it. Experiments and
// Feature Flags is served entirely by admin.flags.* and admin.entitlements.*,
// which the money lane already owns and already built. Flags are rich: state,
// rollout, targets, an internal-only bit, and a kill with a reason on it.
// EXPERIMENTS DO NOT EXIST. There is no experiment table, no variant, no
// assignment, no exposure log and no results anywhere in the schema, and a
// rollout percent is not an experiment. So that page reads the flag routes and
// says the rest is not wired, in those words, rather than growing a route here
// that would have to invent its answer.
//
// THREE THINGS THIS FILE DELIBERATELY DOES NOT DO.
//
// It adds no write. Every lever these screens need already exists somewhere
// else and is already enforced somewhere a test can observe it: a teardown is
// admin.infra.teardownFleet, a suspension is admin.tenants.suspend, a kill is
// admin.flags.kill. A second button writing the same row from a second place is
// how two code paths come to disagree about what a teardown means. The one
// mutation this lane could plausibly have owned, cancelling a customer's
// workload run, was left out because runtime_commands is a queue an engine
// drains and there is no engine behind the operator pool, so the button would
// report success and change nothing observable. That is exactly the failure the
// router.ts header refuses to ship.
//
// It reads no secret. SAFE_COLUMNS in router.ts is the rule and the reason: RLS
// is row level and cannot restrict a column, so column safety on this path is
// an application property. Every query below names its columns. Two omissions
// are deliberate and both look harmless: artifacts.storage_key, a pointer into
// the object store that answers no question this page asks, and
// pr_generations.callback_hash, which is a credential digest.
//
// It computes no total. pageOf in router.ts avoids count queries because a
// count over a cross tenant table is the expensive half of every page, and
// these are the largest tables in the product. The console says how many rows
// it is showing and whether there are more, which is the honest form of the
// same answer.
//
// A COST THIS FILE PAYS AND CANNOT FIX ALONE. The unfiltered lists order by
// created_at across every tenant, and the indexes on runs, environments and
// pr_generations are all per organization or per parent row. So the unfiltered
// first page is a sort and the org filtered one is an index read. The fix, when
// somebody takes the migration, is one index per table on (created_at DESC,
// id DESC). The operator pool's statement timeout is 30 seconds rather than the
// application's, which is what makes the unfiltered view survivable meanwhile.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import { router } from '../trpc.ts'
import { adminProcedure, type AdminContext } from './trpc.ts'

const uuid = z.string().uuid()

/** The same page input the rest of the portal uses, so a caller that can page
 *  one operator list can page all of them. */
const page = z.object({
  limit: z.number().int().min(1).max(200).default(50),
  cursor: z.string().nullish(),
})

/**
 * What a reader should do about a run, in five words.
 *
 * Separate from the run's own `state`, and both are returned, because they
 * answer different questions and collapsing them loses the one that matters. A
 * load run whose state is `succeeded` can carry a `fail` verdict: the job ran
 * to completion and what it found was a failure. A console that colours the
 * badge from `state` alone paints that run green.
 *
 * `unknown` is a real value and not a gap. An agent run that reached `complete`
 * with no verdicts at all did nothing and reported nothing, and calling that
 * `passed` is the exit-code-zero-over-nothing defect this repository has
 * already shipped once. It gets its own word.
 */
export type RunStanding = 'running' | 'passed' | 'failed' | 'cancelled' | 'unknown'

/** The three families of run this product has. They are different objects with
 *  different columns, so the console filters between them rather than merging
 *  them into a table whose column set is true of none of the three. */
export type RunKind = 'agent' | 'load' | 'check'

/**
 * One row of the runs list, whichever family it came from.
 *
 * The fields here are the intersection that is genuinely true of all three: who
 * it belongs to, what code it ran against, when, how it ended and why. Anything
 * that is true of one family only lives on the detail route instead, which is
 * why this is a flat interface rather than a union with three optional halves.
 */
export interface RunRow {
  kind: RunKind
  id: string
  orgId: string
  orgSlug: string
  repository: string | null
  /** The branch, the git ref or the pull request head, whichever the family
   *  has. All three name the code that ran. */
  ref: string | null
  pullRequest: number | null
  /** The engine's environment identifier, when the run named one. */
  envId: string | null
  /** The family's own state word, unchanged. An operator looking for
   *  `abandoned` needs to see `abandoned` and not a word this file chose. */
  state: string
  standing: RunStanding
  /** What it found, when the state does not already say it. Null when nothing
   *  reported one, which is different from finding nothing. */
  verdict: string | null
  /** The one line that says why, when the row carries one. */
  failure: string | null
  /** The sort key, and the moment the row is filed under. */
  at: string
  startedAt: string | null
  finishedAt: string | null
  durationMs: number | null
}

// ---------------------------------------------------------------------------
// Cursors
// ---------------------------------------------------------------------------

/**
 * A keyset cursor over more than one column.
 *
 * `pageOf` in router.ts takes a single string because every list it pages is
 * ordered by a unique column. These are ordered by a timestamp, which is not
 * unique, so the tiebreak column has to travel with it or two rows sharing a
 * millisecond fall in the gap between pages. JSON in base64url rather than a
 * delimiter, because one of the values these carry is a git branch name and a
 * branch may contain any delimiter somebody picks.
 */
function encodeCursor(parts: readonly (string | number)[]): string {
  return Buffer.from(JSON.stringify(parts), 'utf8').toString('base64url')
}

/**
 * Reads one back, and REFUSES a cursor it cannot read.
 *
 * Falling back to the first page would be worse than the error: the caller
 * asked for page four, got page one, and every row it already showed appears
 * again in a list that never ends. The console's `More` would keep offering
 * more forever. An error stops at a message somebody can act on.
 */
function decodeCursor(cursor: string | null | undefined, arity: number): string[] | null {
  if (cursor === null || cursor === undefined || cursor === '') return null
  let parsed: unknown
  try {
    parsed = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8'))
  } catch {
    parsed = null
  }
  if (!Array.isArray(parsed) || parsed.length !== arity) {
    throw new TRPCError({
      code: 'BAD_REQUEST',
      message: 'That page cursor is not one this list issued. Reload the page to start again.',
    })
  }
  return parsed.map((p) => String(p))
}

function iso(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}

function isoOrNull(value: Date | string | null | undefined): string | null {
  return value === null || value === undefined ? null : iso(value)
}

/** Milliseconds between two moments, or null when either is missing. Computed
 *  here rather than in the console so that two screens showing the same run
 *  cannot round it differently. */
function durationMs(start: Date | string | null, end: Date | string | null): number | null {
  if (start === null || end === null) return null
  const ms = new Date(end).getTime() - new Date(start).getTime()
  return Number.isFinite(ms) ? ms : null
}

/** Turns one extra row into a cursor, the same way `pageOf` does. */
function pageOf<Row, Out>(
  rows: Row[],
  limit: number,
  cursorOf: (row: Row) => string,
  map: (row: Row) => Out,
): { rows: Out[]; nextCursor: string | null } {
  const more = rows.length > limit
  const visible = more ? rows.slice(0, limit) : rows
  return {
    rows: visible.map(map),
    nextCursor: more && visible.length > 0 ? cursorOf(visible[visible.length - 1]!) : null,
  }
}

// ---------------------------------------------------------------------------
// Twins
// ---------------------------------------------------------------------------

/**
 * "Twin" is the product's word for an environment and there is no twins table,
 * so every one of these reads `environments`.
 *
 * WHY THIS IS NOT admin.infra.twins. The infra lane's `fleet.ts` answers a
 * fleet health question and answers it well: the standing of a teardown, the
 * blast radius of tearing several down at once, and a cap of 500 rows with no
 * cursor. This answers the product question instead, which is "show me every
 * twin, in order, until I stop asking", so it is keyset paged and the console
 * over it uses `usePages` and `More`. A capped list that says nothing about
 * what it left out is precisely the failure `More` exists to prevent, so the
 * two lists are not interchangeable and this one is not a copy for its own
 * sake. Nothing here touches the teardown ledger, the blast radius or the
 * teardown write path, which stay the infra lane's.
 */
const twinScope = z.enum(['live', 'overdue', 'all'])

const twinsRouter = router({
  list: adminProcedure('admin.product.read')
    .input(
      page.extend({
        orgId: uuid.nullish(),
        /** `live` by default, because a fleet view whose default includes every
         *  environment ever created grows forever and answers nothing. */
        scope: twinScope.default('live'),
        /** Matches the engine's environment id, the repository or the branch,
         *  which are the three things somebody has in front of them when they
         *  come to this page. */
        query: z.string().trim().max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor, 2)
      const now = c.clock.now().toISOString()
      const like = input.query ? `%${input.query}%` : null

      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          env_id: string
          repository: string
          branch: string
          pull_request: number | null
          state: string
          preview_url: string | null
          runtime: string | null
          golden_version: string | null
          golden_verified: boolean | null
          created_at: Date | string
          updated_at: Date | string
          expires_at: Date | string | null
          torn_down_at: Date | string | null
          teardown_pending: boolean
          runs: string
        }>(sql`
          SELECT e.id, e.org_id, o.slug AS org_slug, e.env_id,
                 r.full_name AS repository, e.branch, e.pull_request,
                 e.state::text AS state, e.preview_url, e.runtime, e.golden_version,
                 g.verified AS golden_verified,
                 e.created_at, e.updated_at, e.expires_at, e.torn_down_at,
                 EXISTS (
                   SELECT 1 FROM teardown_requests t
                   WHERE t.environment_id = e.id AND t.state IN ('pending', 'leased')
                 ) AS teardown_pending,
                 (SELECT count(*) FROM runs rn WHERE rn.environment_id = e.id) AS runs
          FROM environments e
          JOIN organizations o ON o.id = e.org_id
          JOIN repositories r ON r.id = e.repository_id
          LEFT JOIN golden_versions g
            ON g.org_id = e.org_id AND g.repository_id = e.repository_id
           AND g.version = e.golden_version
          WHERE (${input.orgId ?? null}::uuid IS NULL OR e.org_id = ${input.orgId ?? null}::uuid)
            AND (
              ${input.scope} = 'all'
              OR (${input.scope} = 'live' AND e.state <> 'torn_down')
              OR (${input.scope} = 'overdue' AND e.state <> 'torn_down'
                  AND e.expires_at IS NOT NULL AND e.expires_at < ${now}::timestamptz)
            )
            AND (${like}::text IS NULL
                 OR e.env_id ILIKE ${like} OR e.branch ILIKE ${like}
                 OR r.full_name ILIKE ${like})
            AND (${cursor?.[0] ?? null}::timestamptz IS NULL
                 OR (e.created_at, e.id)
                    < (${cursor?.[0] ?? null}::timestamptz, ${cursor?.[1] ?? null}::uuid))
          ORDER BY e.created_at DESC, e.id DESC
          LIMIT ${input.limit + 1}`),
      )

      return pageOf(
        rows,
        input.limit,
        (r) => encodeCursor([iso(r.created_at), r.id]),
        (r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          envId: r.env_id,
          repository: r.repository,
          branch: r.branch,
          pullRequest: r.pull_request,
          state: r.state,
          previewUrl: r.preview_url,
          runtime: r.runtime,
          goldenVersion: r.golden_version,
          /** Null when the twin names no golden version, or names one this
           *  installation has no row for. Both are worth telling apart from
           *  `false`, which means a version exists and was never verified. */
          goldenVerified: r.golden_verified,
          createdAt: iso(r.created_at),
          updatedAt: iso(r.updated_at),
          expiresAt: isoOrNull(r.expires_at),
          tornDownAt: isoOrNull(r.torn_down_at),
          /** Past the lifetime it was created with and still not torn down.
           *  This is the row that is costing somebody money right now. */
          overdue:
            r.state !== 'torn_down' &&
            r.expires_at !== null &&
            new Date(r.expires_at as string).getTime() < new Date(now).getTime(),
          teardownPending: r.teardown_pending,
          runs: Number(r.runs),
        }),
      )
    }),

  /**
   * One twin, and everything hanging off it.
   *
   * Keyed on `environments.id` rather than on `env_id`, because `env_id` is
   * unique per organization and not across the installation, and a detail
   * route that took it would show one operator another tenant's environment
   * the first time two customers picked the same name.
   */
  get: adminProcedure('admin.product.read')
    .input(z.object({ id: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          org_id: string
          org_slug: string
          org_name: string
          env_id: string
          repository: string
          repository_id: string
          default_branch: string | null
          branch: string
          pull_request: number | null
          state: string
          preview_url: string | null
          runtime: string | null
          golden_version: string | null
          created_by: string | null
          last_sequence: string
          created_at: Date | string
          updated_at: Date | string
          expires_at: Date | string | null
          torn_down_at: Date | string | null
        }>(sql`
          SELECT e.id, e.org_id, o.slug AS org_slug, o.name AS org_name, e.env_id,
                 r.full_name AS repository, r.id AS repository_id, r.default_branch,
                 e.branch, e.pull_request, e.state::text AS state, e.preview_url,
                 e.runtime, e.golden_version, u.github_login AS created_by,
                 e.last_sequence, e.created_at, e.updated_at, e.expires_at, e.torn_down_at
          FROM environments e
          JOIN organizations o ON o.id = e.org_id
          JOIN repositories r ON r.id = e.repository_id
          LEFT JOIN users u ON u.id = e.created_by
          WHERE e.id = ${input.id}::uuid`)
        const env = rows[0]
        if (!env) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: 'No twin with that id. It may have been removed with its organization.',
          })
        }

        const golden = await db.execute<{
          version: string
          verified: boolean
          source_digest: string | null
          rules_digest: string | null
          size_bytes: string | null
          created_at: Date | string
        }>(sql`
          SELECT version, verified, source_digest, rules_digest, size_bytes, created_at
          FROM golden_versions
          WHERE org_id = ${env.org_id}::uuid
            AND repository_id = ${env.repository_id}::uuid
            AND version = ${env.golden_version}`)

        const runs = await db.execute<{
          id: string
          kind: string
          state: string
          started_at: Date | string | null
          finished_at: Date | string | null
          created_at: Date | string
          verdicts: string
          failing: string
        }>(sql`
          SELECT rn.id, rn.kind, rn.state::text AS state, rn.started_at, rn.finished_at,
                 rn.created_at,
                 (SELECT count(*) FROM verdicts v WHERE v.run_id = rn.id) AS verdicts,
                 (SELECT count(*) FROM verdicts v
                   WHERE v.run_id = rn.id AND v.value IN ('fail', 'blocked')) AS failing
          FROM runs rn
          WHERE rn.environment_id = ${input.id}::uuid
          ORDER BY rn.created_at DESC
          LIMIT 20`)

        const workloads = await db.execute<{
          id: string
          state: string
          verdict: string | null
          failure_code: string | null
          workload: string
          requested_at: Date | string
          finished_at: Date | string | null
        }>(sql`
          SELECT wr.id, wr.state::text AS state, wr.verdict::text AS verdict,
                 wr.failure_code, w.name AS workload, wr.requested_at, wr.finished_at
          FROM workload_runs wr
          JOIN workloads w ON w.id = wr.workload_id
          WHERE wr.environment_id = ${input.id}::uuid
          ORDER BY wr.requested_at DESC
          LIMIT 20`)

        // The teardown ledger belongs to the infra lane and this does not
        // duplicate it. What it reads is the three columns that answer the one
        // question this page has to answer on its own: has anybody already
        // asked for this twin to go away, and did anything happen. An operator
        // who cannot see that asks for it a second time.
        const teardowns = await db.execute<{
          id: string
          state: string
          reason: string
          attempts: number
          last_error: string | null
          requested_at: Date | string
          acknowledged_at: Date | string | null
        }>(sql`
          SELECT id, state, reason, attempts, last_error, requested_at, acknowledged_at
          FROM teardown_requests
          WHERE environment_id = ${input.id}::uuid
          ORDER BY requested_at DESC
          LIMIT 10`)

        const g = golden[0]
        return {
          id: env.id,
          orgId: env.org_id,
          orgSlug: env.org_slug,
          orgName: env.org_name,
          envId: env.env_id,
          repository: env.repository,
          defaultBranch: env.default_branch,
          branch: env.branch,
          pullRequest: env.pull_request,
          state: env.state,
          previewUrl: env.preview_url,
          runtime: env.runtime,
          goldenVersion: env.golden_version,
          golden: g
            ? {
                version: g.version,
                verified: g.verified,
                sourceDigest: g.source_digest,
                rulesDigest: g.rules_digest,
                sizeBytes: g.size_bytes === null ? null : Number(g.size_bytes),
                createdAt: iso(g.created_at),
              }
            : null,
          createdBy: env.created_by,
          lastSequence: Number(env.last_sequence),
          createdAt: iso(env.created_at),
          updatedAt: iso(env.updated_at),
          expiresAt: isoOrNull(env.expires_at),
          tornDownAt: isoOrNull(env.torn_down_at),
          runs: runs.map((r) => ({
            id: r.id,
            kind: r.kind,
            state: r.state,
            standing: agentStanding(r.state, Number(r.verdicts), Number(r.failing)),
            verdicts: Number(r.verdicts),
            failing: Number(r.failing),
            startedAt: isoOrNull(r.started_at),
            finishedAt: isoOrNull(r.finished_at),
            createdAt: iso(r.created_at),
          })),
          workloadRuns: workloads.map((w) => ({
            id: w.id,
            workload: w.workload,
            state: w.state,
            standing: loadStanding(w.state, w.verdict),
            verdict: w.verdict,
            failureCode: w.failure_code,
            requestedAt: iso(w.requested_at),
            finishedAt: isoOrNull(w.finished_at),
          })),
          teardowns: teardowns.map((t) => ({
            id: t.id,
            state: t.state,
            reason: t.reason,
            attempts: t.attempts,
            lastError: t.last_error,
            requestedAt: iso(t.requested_at),
            acknowledgedAt: isoOrNull(t.acknowledged_at),
          })),
        }
      })
    }),

  /**
   * Branches, which are not a table.
   *
   * A branch in this product is a column on `environments`, so this is a
   * grouping rather than a list, and the grouping is what makes it worth a
   * screen: one branch can hold several twins over its life, and the question
   * an operator has is about the branch and not about any one of them.
   *
   * The finding this page exists for is `orphaned`: a branch with a live twin
   * whose pull request is closed or merged. Nothing is wrong with the twin, the
   * data is not corrupt and no alarm fired. It is simply still running, and
   * still costing money, for a change that landed a fortnight ago.
   */
  branches: adminProcedure('admin.product.read')
    .input(
      page.extend({
        orgId: uuid.nullish(),
        scope: z.enum(['all', 'live', 'orphaned']).default('live'),
        query: z.string().trim().max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor, 3)
      const like = input.query ? `%${input.query}%` : null

      const rows = await c.adminDb(async (db) =>
        db.execute<{
          org_id: string
          org_slug: string
          repository_id: string
          repository: string
          branch: string
          twins: string
          live: string
          overdue: string
          pull_request: number | null
          pr_state: string | null
          pr_title: string | null
          pr_draft: boolean | null
          pr_from_fork: boolean | null
          pr_closed_at: Date | string | null
          last_activity: Date | string
          latest_state: string
        }>(sql`
          WITH grouped AS (
            SELECT e.org_id, e.repository_id, e.branch,
                   count(*) AS twins,
                   count(*) FILTER (WHERE e.state <> 'torn_down') AS live,
                   count(*) FILTER (
                     WHERE e.state <> 'torn_down' AND e.expires_at IS NOT NULL
                       AND e.expires_at < ${c.clock.now().toISOString()}::timestamptz
                   ) AS overdue,
                   -- A branch has at most one pull request open against it in
                   -- this product, and rows created before the pull request
                   -- existed carry null, so max() picks the one there is.
                   max(e.pull_request) AS pull_request,
                   max(e.updated_at) AS last_activity,
                   (array_agg(e.state::text ORDER BY e.updated_at DESC))[1] AS latest_state
            FROM environments e
            WHERE (${input.orgId ?? null}::uuid IS NULL
                   OR e.org_id = ${input.orgId ?? null}::uuid)
            GROUP BY e.org_id, e.repository_id, e.branch
          )
          SELECT b.org_id, o.slug AS org_slug, b.repository_id, r.full_name AS repository,
                 b.branch, b.twins, b.live, b.overdue, b.pull_request,
                 pr.state AS pr_state, pr.title AS pr_title, pr.draft AS pr_draft,
                 pr.from_fork AS pr_from_fork, pr.closed_at AS pr_closed_at,
                 b.last_activity, b.latest_state
          FROM grouped b
          JOIN organizations o ON o.id = b.org_id
          JOIN repositories r ON r.id = b.repository_id
          LEFT JOIN pull_requests pr
            ON pr.repository_id = b.repository_id AND pr.number = b.pull_request
          WHERE (
              ${input.scope} = 'all'
              OR (${input.scope} = 'live' AND b.live > 0)
              OR (${input.scope} = 'orphaned' AND b.live > 0
                  AND pr.state IS NOT NULL AND pr.state <> 'open')
            )
            AND (${like}::text IS NULL
                 OR b.branch ILIKE ${like} OR r.full_name ILIKE ${like})
            AND (${cursor?.[0] ?? null}::timestamptz IS NULL
                 OR (b.last_activity, b.repository_id, b.branch)
                    < (${cursor?.[0] ?? null}::timestamptz,
                       ${cursor?.[1] ?? null}::uuid,
                       ${cursor?.[2] ?? null}::text))
          ORDER BY b.last_activity DESC, b.repository_id DESC, b.branch DESC
          LIMIT ${input.limit + 1}`),
      )

      return pageOf(
        rows,
        input.limit,
        (r) => encodeCursor([iso(r.last_activity), r.repository_id, r.branch]),
        (r) => ({
          orgId: r.org_id,
          orgSlug: r.org_slug,
          repositoryId: r.repository_id,
          repository: r.repository,
          branch: r.branch,
          twins: Number(r.twins),
          live: Number(r.live),
          overdue: Number(r.overdue),
          pullRequest: r.pull_request,
          pullRequestState: r.pr_state,
          pullRequestTitle: r.pr_title,
          pullRequestDraft: r.pr_draft,
          /** A pull request from a fork, which is the one case where the code
           *  a twin was built from is not the customer's own. */
          pullRequestFromFork: r.pr_from_fork,
          pullRequestClosedAt: isoOrNull(r.pr_closed_at),
          /** Live twins on a branch whose pull request is closed or merged. */
          orphaned:
            Number(r.live) > 0 && r.pr_state !== null && r.pr_state !== 'open',
          lastActivity: iso(r.last_activity),
          latestState: r.latest_state,
        }),
      )
    }),
})

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

/**
 * A run that finished is not a run that passed.
 *
 * `runs.state` says whether the job completed. The verdicts say what it found.
 * A `complete` run with a failing verdict is a failure, and a `complete` run
 * with no verdicts at all found nothing and reported nothing, which is its own
 * answer and not a pass.
 */
function agentStanding(state: string, verdicts: number, failing: number): RunStanding {
  if (state === 'queued' || state === 'running') return 'running'
  if (state === 'cancelled') return 'cancelled'
  if (state === 'failed') return 'failed'
  if (failing > 0) return 'failed'
  if (verdicts === 0) return 'unknown'
  return 'passed'
}

/** The same split for a load run, where the verdict is a column rather than a
 *  row count. `flaky` counts as failed here: an operator filtering for things
 *  worth looking at wants a flaky run in the list. */
function loadStanding(state: string, verdict: string | null): RunStanding {
  if (state === 'requested' || state === 'accepted' || state === 'running') return 'running'
  if (state === 'cancelled') return 'cancelled'
  if (state === 'failed' || state === 'timed_out' || state === 'abandoned') return 'failed'
  if (verdict === 'fail' || verdict === 'blocked' || verdict === 'flaky') return 'failed'
  if (verdict === 'pass') return 'passed'
  return 'unknown'
}

/** And for a pull request check, where the state already carries the finding.
 *  `unverified` is the state a check reaches when it said nothing before its
 *  deadline, which is neither a pass nor a failure. */
function checkStanding(state: string): RunStanding {
  if (state === 'queued' || state === 'running') return 'running'
  if (state === 'cancelled') return 'cancelled'
  if (state === 'passed') return 'passed'
  if (state === 'failed' || state === 'blocked') return 'failed'
  return 'unknown'
}

const runsRouter = router({
  /**
   * The list an operator opens during an incident.
   *
   * ONE FAMILY AT A TIME, and that is a decision rather than a limitation. An
   * agent run, a load run and a pull request check are three different objects:
   * one has verdicts and artifacts, one has latency percentiles and a lease,
   * one has a head commit and a GitHub check. A single merged table would need
   * a column set that is true of none of them, and the columns that matter
   * during an incident are exactly the ones that would have to go. So the
   * family is a filter, the screen names which one it is showing, and every
   * column in the table means something for that family.
   *
   * `failedOnly` is the filter this page is really for. It is computed in SQL
   * rather than from the returned rows, because filtering after the page is
   * cut returns an arbitrary number of rows per page and eventually an empty
   * page with a cursor, which reads as the end of a list that has more in it.
   */
  list: adminProcedure('admin.product.read')
    .input(
      page.extend({
        kind: z.enum(['agent', 'load', 'check']).default('agent'),
        orgId: uuid.nullish(),
        /** One twin, reached from the twin page. */
        environmentId: uuid.nullish(),
        failedOnly: z.boolean().default(false),
        query: z.string().trim().max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor, 2)
      const at = cursor?.[0] ?? null
      const id = cursor?.[1] ?? null
      const org = input.orgId ?? null
      const env = input.environmentId ?? null
      const like = input.query ? `%${input.query}%` : null

      if (input.kind === 'agent') {
        const rows = await c.adminDb(async (db) =>
          db.execute<{
            id: string
            org_id: string
            org_slug: string
            repository: string
            branch: string
            pull_request: number | null
            env_id: string
            kind: string
            state: string
            started_at: Date | string | null
            finished_at: Date | string | null
            created_at: Date | string
            verdicts: string
            failing: string
            summary: string | null
          }>(sql`
            SELECT rn.id, rn.org_id, o.slug AS org_slug, rp.full_name AS repository,
                   e.branch, e.pull_request, e.env_id, rn.kind, rn.state::text AS state,
                   rn.started_at, rn.finished_at, rn.created_at,
                   (SELECT count(*) FROM verdicts v WHERE v.run_id = rn.id) AS verdicts,
                   (SELECT count(*) FROM verdicts v
                     WHERE v.run_id = rn.id AND v.value IN ('fail', 'blocked')) AS failing,
                   (SELECT v.summary FROM verdicts v
                     WHERE v.run_id = rn.id AND v.value IN ('fail', 'blocked')
                     ORDER BY v.created_at ASC LIMIT 1) AS summary
            FROM runs rn
            JOIN environments e ON e.id = rn.environment_id
            JOIN organizations o ON o.id = rn.org_id
            JOIN repositories rp ON rp.id = e.repository_id
            WHERE (${org}::uuid IS NULL OR rn.org_id = ${org}::uuid)
              AND (${env}::uuid IS NULL OR rn.environment_id = ${env}::uuid)
              AND (${like}::text IS NULL
                   OR e.env_id ILIKE ${like} OR e.branch ILIKE ${like}
                   OR rp.full_name ILIKE ${like} OR rn.kind ILIKE ${like})
              AND (${input.failedOnly} = false
                   OR rn.state = 'failed'
                   OR EXISTS (SELECT 1 FROM verdicts v
                               WHERE v.run_id = rn.id AND v.value IN ('fail', 'blocked')))
              AND (${at}::timestamptz IS NULL
                   OR (rn.created_at, rn.id) < (${at}::timestamptz, ${id}::uuid))
            ORDER BY rn.created_at DESC, rn.id DESC
            LIMIT ${input.limit + 1}`),
        )
        return pageOf(
          rows,
          input.limit,
          (r) => encodeCursor([iso(r.created_at), r.id]),
          (r): RunRow => ({
            kind: 'agent',
            id: r.id,
            orgId: r.org_id,
            orgSlug: r.org_slug,
            repository: r.repository,
            ref: r.branch,
            pullRequest: r.pull_request,
            envId: r.env_id,
            state: r.state,
            standing: agentStanding(r.state, Number(r.verdicts), Number(r.failing)),
            verdict:
              Number(r.verdicts) === 0
                ? null
                : `${Number(r.verdicts) - Number(r.failing)} of ${Number(r.verdicts)} passed`,
            failure: r.summary,
            at: iso(r.created_at),
            startedAt: isoOrNull(r.started_at),
            finishedAt: isoOrNull(r.finished_at),
            durationMs: durationMs(r.started_at, r.finished_at),
          }),
        )
      }

      if (input.kind === 'load') {
        const rows = await c.adminDb(async (db) =>
          db.execute<{
            id: string
            org_id: string
            org_slug: string
            repository: string
            git_ref: string
            env_id: string | null
            state: string
            verdict: string | null
            failure_code: string | null
            detail: string | null
            requested_at: Date | string
            started_at: Date | string | null
            finished_at: Date | string | null
          }>(sql`
            SELECT wr.id, wr.org_id, o.slug AS org_slug, wr.repository, wr.git_ref,
                   e.env_id, wr.state::text AS state, wr.verdict::text AS verdict,
                   wr.failure_code, wr.detail,
                   wr.requested_at, wr.started_at, wr.finished_at
            FROM workload_runs wr
            JOIN organizations o ON o.id = wr.org_id
            LEFT JOIN environments e ON e.id = wr.environment_id
            WHERE (${org}::uuid IS NULL OR wr.org_id = ${org}::uuid)
              AND (${env}::uuid IS NULL OR wr.environment_id = ${env}::uuid)
              AND (${like}::text IS NULL
                   OR wr.repository ILIKE ${like} OR wr.git_ref ILIKE ${like}
                   OR e.env_id ILIKE ${like} OR wr.failure_code ILIKE ${like})
              AND (${input.failedOnly} = false
                   OR wr.state IN ('failed', 'timed_out', 'abandoned')
                   OR wr.verdict IN ('fail', 'blocked', 'flaky'))
              AND (${at}::timestamptz IS NULL
                   OR (wr.requested_at, wr.id) < (${at}::timestamptz, ${id}::uuid))
            ORDER BY wr.requested_at DESC, wr.id DESC
            LIMIT ${input.limit + 1}`),
        )
        return pageOf(
          rows,
          input.limit,
          (r) => encodeCursor([iso(r.requested_at), r.id]),
          (r): RunRow => ({
            kind: 'load',
            id: r.id,
            orgId: r.org_id,
            orgSlug: r.org_slug,
            repository: r.repository,
            ref: r.git_ref,
            pullRequest: null,
            envId: r.env_id,
            state: r.state,
            standing: loadStanding(r.state, r.verdict),
            verdict: r.verdict,
            failure: r.failure_code ?? r.detail,
            at: iso(r.requested_at),
            startedAt: isoOrNull(r.started_at),
            finishedAt: isoOrNull(r.finished_at),
            durationMs: durationMs(r.started_at, r.finished_at),
          }),
        )
      }

      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          repository: string
          head_ref: string
          number: number
          env_id: string | null
          state: string
          detail: string | null
          queued_at: Date | string
          started_at: Date | string | null
          finished_at: Date | string | null
        }>(sql`
          SELECT g.id, g.org_id, o.slug AS org_slug, rp.full_name AS repository,
                 pr.head_ref, pr.number, g.env_id, g.state::text AS state, g.detail,
                 g.queued_at, g.started_at, g.finished_at
          FROM pr_generations g
          JOIN pull_requests pr ON pr.id = g.pull_request_id
          JOIN repositories rp ON rp.id = pr.repository_id
          JOIN organizations o ON o.id = g.org_id
          WHERE (${org}::uuid IS NULL OR g.org_id = ${org}::uuid)
            AND (${like}::text IS NULL
                 OR rp.full_name ILIKE ${like} OR pr.head_ref ILIKE ${like}
                 OR g.env_id ILIKE ${like})
            AND (${input.failedOnly} = false
                 OR g.state IN ('failed', 'blocked', 'unverified'))
            AND (${at}::timestamptz IS NULL
                 OR (g.queued_at, g.id) < (${at}::timestamptz, ${id}::uuid))
          ORDER BY g.queued_at DESC, g.id DESC
          LIMIT ${input.limit + 1}`),
      )
      return pageOf(
        rows,
        input.limit,
        (r) => encodeCursor([iso(r.queued_at), r.id]),
        (r): RunRow => ({
          kind: 'check',
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          repository: r.repository,
          ref: r.head_ref,
          pullRequest: r.number,
          envId: r.env_id,
          state: r.state,
          standing: checkStanding(r.state),
          verdict: null,
          failure: r.detail,
          at: iso(r.queued_at),
          startedAt: isoOrNull(r.started_at),
          finishedAt: isoOrNull(r.finished_at),
          durationMs: durationMs(r.started_at, r.finished_at),
        }),
      )
    }),

  /**
   * One run, in enough detail to say why it failed and what it touched.
   *
   * The shape depends on the family, so the answer carries the family on it and
   * the console switches. Three routes would have been the other option and the
   * console would then hold the same switch anyway, one level up.
   */
  get: adminProcedure('admin.product.read')
    .input(z.object({ kind: z.enum(['agent', 'load', 'check']), id: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        if (input.kind === 'agent') {
          const rows = await db.execute<{
            id: string
            org_id: string
            org_slug: string
            environment_id: string
            env_id: string
            repository: string
            branch: string
            pull_request: number | null
            preview_url: string | null
            kind: string
            state: string
            started_at: Date | string | null
            finished_at: Date | string | null
            created_at: Date | string
            last_sequence: string
          }>(sql`
            SELECT rn.id, rn.org_id, o.slug AS org_slug, rn.environment_id, e.env_id,
                   rp.full_name AS repository, e.branch, e.pull_request, e.preview_url,
                   rn.kind, rn.state::text AS state, rn.started_at, rn.finished_at,
                   rn.created_at, rn.last_sequence
            FROM runs rn
            JOIN environments e ON e.id = rn.environment_id
            JOIN organizations o ON o.id = rn.org_id
            JOIN repositories rp ON rp.id = e.repository_id
            WHERE rn.id = ${input.id}::uuid`)
          const run = rows[0]
          if (!run) throw notFound('agent run')

          const verdicts = await db.execute<{
            id: string
            workflow: string
            persona: string | null
            value: string
            summary: string | null
            steps: number
            duration_ms: number | null
            reproduction: unknown
            created_at: Date | string
          }>(sql`
            SELECT id, workflow, persona, value::text AS value, summary, steps,
                   duration_ms, reproduction, created_at
            FROM verdicts WHERE run_id = ${input.id}::uuid
            ORDER BY created_at ASC LIMIT 200`)

          // Artifacts without their storage key. See the file header: the key
          // is a pointer into the object store and answers no question this
          // page asks. `retained` is the one that does, because a missing
          // artifact and a discarded one look identical without it.
          const artifacts = await db.execute<{
            id: string
            kind: string
            step: number | null
            content_type: string | null
            size_bytes: string | null
            sha256: string | null
            retained: boolean
            created_at: Date | string
          }>(sql`
            SELECT id, kind, step, content_type, size_bytes, sha256, retained, created_at
            FROM artifacts WHERE run_id = ${input.id}::uuid
            ORDER BY step ASC NULLS LAST, created_at ASC LIMIT 200`)

          const failing = verdicts.filter((v) => v.value === 'fail' || v.value === 'blocked').length
          return {
            kind: 'agent' as const,
            id: run.id,
            orgId: run.org_id,
            orgSlug: run.org_slug,
            environmentId: run.environment_id,
            envId: run.env_id,
            repository: run.repository,
            branch: run.branch,
            pullRequest: run.pull_request,
            previewUrl: run.preview_url,
            runKind: run.kind,
            state: run.state,
            standing: agentStanding(run.state, verdicts.length, failing),
            startedAt: isoOrNull(run.started_at),
            finishedAt: isoOrNull(run.finished_at),
            createdAt: iso(run.created_at),
            durationMs: durationMs(run.started_at, run.finished_at),
            lastSequence: Number(run.last_sequence),
            verdicts: verdicts.map((v) => ({
              id: v.id,
              workflow: v.workflow,
              persona: v.persona,
              value: v.value,
              summary: v.summary,
              steps: v.steps,
              durationMs: v.duration_ms,
              /** The engine's own recipe for reproducing this verdict, as it
               *  reported it. Null when it reported none, which the console
               *  says rather than assembling a command that never ran. */
              reproduction: v.reproduction ?? null,
              createdAt: iso(v.created_at),
            })),
            artifacts: artifacts.map((a) => ({
              id: a.id,
              kind: a.kind,
              step: a.step,
              contentType: a.content_type,
              sizeBytes: a.size_bytes === null ? null : Number(a.size_bytes),
              sha256: a.sha256,
              retained: a.retained,
              createdAt: iso(a.created_at),
            })),
          }
        }

        if (input.kind === 'load') {
          const rows = await db.execute<Record<string, unknown>>(sql`
            SELECT wr.id, wr.org_id, o.slug AS org_slug, wr.environment_id, e.env_id,
                   wr.repository, wr.git_ref, wr.workflow_file, w.name AS workload,
                   w.slug AS workload_slug, w.kind::text AS workload_kind,
                   wv.version AS workload_version,
                   wr.state::text AS state, wr.verdict::text AS verdict, wr.failure_code,
                   wr.detail, wr.reproduce_command, wr.manifest_digest, wr.attempt,
                   wr.requested_at, wr.dispatched_at, wr.accepted_at, wr.started_at,
                   wr.finished_at, wr.deadline_at, wr.lease_holder, wr.lease_expires_at,
                   wr.lease_lost_at, wr.lease_takeovers, wr.cancel_requested_at,
                   wr.cancel_reason, wr.cancelled_at, wr.retry_of, wr.superseded_by
            FROM workload_runs wr
            JOIN workloads w ON w.id = wr.workload_id
            JOIN workload_versions wv ON wv.id = wr.workload_version_id
            JOIN organizations o ON o.id = wr.org_id
            LEFT JOIN environments e ON e.id = wr.environment_id
            WHERE wr.id = ${input.id}::uuid`)
          const run = rows[0]
          if (!run) throw notFound('load run')

          const results = await db.execute<Record<string, unknown>>(sql`
            SELECT kind::text AS kind, requests, failures, error_rate, target_rate,
                   achieved_rate, p50_ms, p90_ms, p95_ms, p99_ms, max_ms,
                   sessions, iterations, scheduled_ms,
                   workflows, workflows_passed, workflows_failed, workflows_flaky,
                   workflows_blocked, workflows_unverified, steps,
                   findings, goals, goals_reached,
                   duration_ms, source, error_reasons, refused_routes, recorded_at
            FROM workload_run_results
            WHERE workload_run_id = ${input.id}::uuid`)

          const started = (run.started_at ?? null) as Date | string | null
          const finished = (run.finished_at ?? null) as Date | string | null
          return {
            kind: 'load' as const,
            id: run.id as string,
            orgId: run.org_id as string,
            orgSlug: run.org_slug as string,
            environmentId: (run.environment_id as string | null) ?? null,
            envId: (run.env_id as string | null) ?? null,
            repository: run.repository as string,
            gitRef: run.git_ref as string,
            workflowFile: (run.workflow_file as string | null) ?? null,
            workload: run.workload as string,
            workloadSlug: run.workload_slug as string,
            workloadKind: run.workload_kind as string,
            /** Which authored version ran. A run points at a version and the
             *  version is what the run MEANS, so a detail page without it
             *  cannot say which definition produced these numbers. */
            workloadVersion: run.workload_version === null ? null : Number(run.workload_version),
            state: run.state as string,
            standing: loadStanding(run.state as string, (run.verdict as string | null) ?? null),
            verdict: (run.verdict as string | null) ?? null,
            failureCode: (run.failure_code as string | null) ?? null,
            detail: (run.detail as string | null) ?? null,
            /** The plain command that reproduces this run, as the engine
             *  reported it. Null until one reports, and the console says no
             *  command was recorded rather than assembling one that drifts
             *  from what actually ran. */
            reproduceCommand: (run.reproduce_command as string | null) ?? null,
            manifestDigest: (run.manifest_digest as string | null) ?? null,
            attempt: Number(run.attempt),
            requestedAt: iso(run.requested_at as Date),
            dispatchedAt: isoOrNull(run.dispatched_at as Date | null),
            acceptedAt: isoOrNull(run.accepted_at as Date | null),
            startedAt: isoOrNull(started),
            finishedAt: isoOrNull(finished),
            deadlineAt: iso(run.deadline_at as Date),
            durationMs: durationMs(started, finished),
            leaseHolder: (run.lease_holder as string | null) ?? null,
            leaseExpiresAt: isoOrNull(run.lease_expires_at as Date | null),
            /** A lease that was taken away from an engine that stopped
             *  reporting. A run with takeovers and no result is the shape of
             *  an engine that keeps dying, not of a slow test. */
            leaseLostAt: isoOrNull(run.lease_lost_at as Date | null),
            leaseTakeovers: Number(run.lease_takeovers ?? 0),
            cancelRequestedAt: isoOrNull(run.cancel_requested_at as Date | null),
            cancelReason: (run.cancel_reason as string | null) ?? null,
            cancelledAt: isoOrNull(run.cancelled_at as Date | null),
            retryOf: (run.retry_of as string | null) ?? null,
            supersededBy: (run.superseded_by as string | null) ?? null,
            result: results[0]
              ? {
                  ...results[0],
                  recordedAt: iso(results[0].recorded_at as Date),
                }
              : null,
          }
        }

        const rows = await db.execute<Record<string, unknown>>(sql`
          SELECT g.id, g.org_id, o.slug AS org_slug, rp.full_name AS repository,
                 pr.number, pr.title, pr.head_ref, pr.base_ref, pr.state AS pr_state,
                 pr.draft, pr.from_fork, pr.head_repository, pr.approved_sha,
                 pr.approved_by, pr.approved_at,
                 g.head_sha, g.attempt, g.state::text AS state, g.detail,
                 g.check_run_id, g.workflow_run_id, g.reported_by, g.env_id, g.verdict,
                 g.superseded_by, g.queued_at, g.started_at, g.finished_at, g.deadline_at
          FROM pr_generations g
          JOIN pull_requests pr ON pr.id = g.pull_request_id
          JOIN repositories rp ON rp.id = pr.repository_id
          JOIN organizations o ON o.id = g.org_id
          WHERE g.id = ${input.id}::uuid`)
        const gen = rows[0]
        if (!gen) throw notFound('pull request check')

        const started = (gen.started_at ?? null) as Date | string | null
        const finished = (gen.finished_at ?? null) as Date | string | null
        return {
          kind: 'check' as const,
          id: gen.id as string,
          orgId: gen.org_id as string,
          orgSlug: gen.org_slug as string,
          repository: gen.repository as string,
          pullRequest: Number(gen.number),
          title: (gen.title as string | null) ?? null,
          headRef: gen.head_ref as string,
          baseRef: gen.base_ref as string,
          pullRequestState: gen.pr_state as string,
          draft: Boolean(gen.draft),
          /** A head branch living in another repository, which is the whole of
           *  what makes a pull request untrusted here. */
          fromFork: Boolean(gen.from_fork),
          headRepository: gen.head_repository as string,
          approvedSha: (gen.approved_sha as string | null) ?? null,
          approvedBy: (gen.approved_by as string | null) ?? null,
          approvedAt: isoOrNull(gen.approved_at as Date | null),
          headSha: gen.head_sha as string,
          attempt: Number(gen.attempt),
          state: gen.state as string,
          standing: checkStanding(gen.state as string),
          detail: (gen.detail as string | null) ?? null,
          /** Null while the installation does not hold `checks: write`, which
           *  is a state this has to serve rather than crash in. */
          checkRunId: gen.check_run_id === null ? null : String(gen.check_run_id),
          workflowRunId: gen.workflow_run_id === null ? null : String(gen.workflow_run_id),
          reportedBy: (gen.reported_by as string | null) ?? null,
          envId: (gen.env_id as string | null) ?? null,
          verdict: gen.verdict ?? null,
          supersededBy: (gen.superseded_by as string | null) ?? null,
          queuedAt: iso(gen.queued_at as Date),
          startedAt: isoOrNull(started),
          finishedAt: isoOrNull(finished),
          deadlineAt: iso(gen.deadline_at as Date),
          durationMs: durationMs(started, finished),
        }
      })
    }),
})

// ---------------------------------------------------------------------------
// Safe state
// ---------------------------------------------------------------------------

/**
 * What this lane can honestly say about a customer's data, and what it cannot.
 *
 * TWO TABLES EXIST. `golden_versions` records that a scan produced a version,
 * whether it was verified and how big it was. `masking_rules` records which
 * column gets which transform and whether a human confirmed it.
 *
 * NOTHING ELSE DOES. There is no table describing a customer's live database
 * connection, no snapshot ledger and no restore history anywhere in the schema.
 * So this surface cannot answer "which database was cloned", "when was this
 * restored" or "how old is the copy". The console says that in those words
 * rather than drawing a panel over a number nobody measured, and this comment
 * exists so the next person adding to this file knows the absence was checked
 * rather than missed.
 *
 * The finding worth a screen is the one that IS backed: a masking rule the
 * classifier suggested and nobody confirmed. `confirmed = false` means a column
 * the scanner believes holds personal data is not being transformed, on a copy
 * somebody is running tests against.
 */
const dataRouter = router({
  goldens: adminProcedure('admin.product.data.read')
    .input(
      page.extend({
        orgId: uuid.nullish(),
        /** `unverified` is the default because it is the only one of the three
         *  that is a finding. A verified version needs nobody's attention. */
        scope: z.enum(['all', 'verified', 'unverified']).default('unverified'),
        query: z.string().trim().max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor, 2)
      const like = input.query ? `%${input.query}%` : null

      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          repository: string
          version: string
          source_digest: string | null
          rules_digest: string | null
          verified: boolean
          size_bytes: string | null
          created_at: Date | string
          twins: string
        }>(sql`
          SELECT g.id, g.org_id, o.slug AS org_slug, rp.full_name AS repository,
                 g.version, g.source_digest, g.rules_digest, g.verified, g.size_bytes,
                 g.created_at,
                 (SELECT count(*) FROM environments e
                   WHERE e.org_id = g.org_id AND e.repository_id = g.repository_id
                     AND e.golden_version = g.version AND e.state <> 'torn_down') AS twins
          FROM golden_versions g
          JOIN organizations o ON o.id = g.org_id
          JOIN repositories rp ON rp.id = g.repository_id
          WHERE (${input.orgId ?? null}::uuid IS NULL
                 OR g.org_id = ${input.orgId ?? null}::uuid)
            AND (${input.scope} = 'all'
                 OR (${input.scope} = 'verified' AND g.verified = true)
                 OR (${input.scope} = 'unverified' AND g.verified = false))
            AND (${like}::text IS NULL
                 OR rp.full_name ILIKE ${like} OR g.version ILIKE ${like})
            AND (${cursor?.[0] ?? null}::timestamptz IS NULL
                 OR (g.created_at, g.id)
                    < (${cursor?.[0] ?? null}::timestamptz, ${cursor?.[1] ?? null}::uuid))
          ORDER BY g.created_at DESC, g.id DESC
          LIMIT ${input.limit + 1}`),
      )

      return pageOf(
        rows,
        input.limit,
        (r) => encodeCursor([iso(r.created_at), r.id]),
        (r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          repository: r.repository,
          version: r.version,
          sourceDigest: r.source_digest,
          rulesDigest: r.rules_digest,
          verified: r.verified,
          sizeBytes: r.size_bytes === null ? null : Number(r.size_bytes),
          createdAt: iso(r.created_at),
          /** Live twins built from this version. An unverified version with
           *  twins on it is the row that matters; one with none is history. */
          twins: Number(r.twins),
        }),
      )
    }),

  masking: adminProcedure('admin.product.data.read')
    .input(
      page.extend({
        orgId: uuid.nullish(),
        scope: z.enum(['all', 'confirmed', 'unconfirmed']).default('unconfirmed'),
        query: z.string().trim().max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor, 2)
      const like = input.query ? `%${input.query}%` : null

      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          repository: string
          table_name: string
          column_name: string
          transform: string
          link: string | null
          reason: string | null
          confirmed: boolean
          created_at: Date | string
          updated_at: Date | string
        }>(sql`
          SELECT m.id, m.org_id, o.slug AS org_slug, rp.full_name AS repository,
                 m.table_name, m.column_name, m.transform, m.link, m.reason,
                 m.confirmed, m.created_at, m.updated_at
          FROM masking_rules m
          JOIN organizations o ON o.id = m.org_id
          JOIN repositories rp ON rp.id = m.repository_id
          WHERE (${input.orgId ?? null}::uuid IS NULL
                 OR m.org_id = ${input.orgId ?? null}::uuid)
            AND (${input.scope} = 'all'
                 OR (${input.scope} = 'confirmed' AND m.confirmed = true)
                 OR (${input.scope} = 'unconfirmed' AND m.confirmed = false))
            AND (${like}::text IS NULL
                 OR rp.full_name ILIKE ${like} OR m.table_name ILIKE ${like}
                 OR m.column_name ILIKE ${like} OR m.transform ILIKE ${like})
            AND (${cursor?.[0] ?? null}::timestamptz IS NULL
                 OR (m.created_at, m.id)
                    < (${cursor?.[0] ?? null}::timestamptz, ${cursor?.[1] ?? null}::uuid))
          ORDER BY m.created_at DESC, m.id DESC
          LIMIT ${input.limit + 1}`),
      )

      return pageOf(
        rows,
        input.limit,
        (r) => encodeCursor([iso(r.created_at), r.id]),
        (r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          repository: r.repository,
          table: r.table_name,
          column: r.column_name,
          transform: r.transform,
          /** The column this one is kept consistent with, so a masked foreign
           *  key still joins. */
          link: r.link,
          reason: r.reason,
          /** False means the classifier suggested it and no human has agreed,
           *  which means the column is not being transformed. */
          confirmed: r.confirmed,
          createdAt: iso(r.created_at),
          updatedAt: iso(r.updated_at),
        }),
      )
    }),
})

function notFound(what: string): TRPCError {
  return new TRPCError({
    code: 'NOT_FOUND',
    message: `No ${what} with that id. It may have been removed with its organization.`,
  })
}

/**
 * The Product namespace, mounted once at `admin.product` in router.ts.
 *
 * Nested rather than flat, so a path reads `admin.product.twins.list` and says
 * which section serves it. The matrix walks the whole tree, so every leaf below
 * is guarded the same way whatever depth it sits at.
 */
export const productRouter = router({
  twins: twinsRouter,
  runs: runsRouter,
  data: dataRouter,
})
