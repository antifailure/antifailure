// Workload definitions, their versions, and the lifecycle of a run.
//
// WHERE THE DEADLINE IS RESOLVED, AND WHY IT IS NOT A GLOBAL SWEEP.
//
// A run that is requested and never picked up, or picked up and never reported
// on, has to end up saying so. The obvious shape is a sweeper on the
// housekeeping interval, and that shape is a trap here, twice over.
//
// Every policy on these tables keys on current_org(), so a statement that runs
// with no tenant set matches nothing and reports success. That is not a
// hypothetical: `sweepDeviceAuthorizations` ran on the interval and deleted
// zero rows for the life of the process, and migration 0016 exists because of
// it. Measured on a real database while writing this, `sweepSessions` still
// does exactly that today and is reported separately.
//
// The fix 0016 used was a second permissive policy restricted to rows nobody
// can still use. It does not transfer. That policy is FOR ALL, so it grants
// SELECT as well, and an expired device authorization carries nothing; a
// workload run carries a repository name, a branch and a workload id, so the
// same shape would let any tenant read any other tenant's overdue runs. And
// because permissive policies OR together, a row reachable through the second
// policy is also updatable into the reader's own organization.
//
// So resolution is per tenant and it runs inside the caller's own transaction,
// at the top of every route that reads or writes a workload. It is one UPDATE
// against a partial index holding only runs that can still time out, so it
// costs nothing when there is nothing to do. An organization that opens Studio
// resolves its own overdue runs, which is every organization that could be
// affected by one: the harm a stuck run does is that it blocks the next run of
// the same workload, and the next run is a request that resolves it first.
//
// What that leaves is an organization nobody looks at, where a run stays
// `running` in a table nobody reads. That is table state rather than incorrect
// behaviour, and closing it needs a connection row-level security does not
// apply to, which the control plane holds only when AF_MAINTENANCE_DATABASE_URL
// names a superuser or a BYPASSRLS role. Said here rather than papered over.

import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { parseBody, type WorkloadBody, type WorkloadKind } from './bodies.ts'

/**
 * How long a run is believed without a word from the engine.
 *
 * Two hours, which is four times the thirty minute job timeout the example
 * workflow declares, so a queued Actions job on a busy account is late rather
 * than abandoned. An engine that is running extends it by heartbeat, so this
 * bounds silence rather than work.
 */
export const RUN_DEADLINE_MS = 2 * 60 * 60 * 1000

/**
 * How long a claim holds a run before another engine may take it.
 *
 * The same fifteen minutes a command lease gets, and for the same reason: it
 * has to outlast a slow start and not outlast a dead process.
 */
export const RUN_LEASE_MS = 15 * 60 * 1000

export const TERMINAL_STATES = [
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
  'abandoned',
] as const

type RunState =
  | (typeof TERMINAL_STATES)[number]
  | 'requested'
  | 'accepted'
  | 'running'

// ---------------------------------------------------------------------------
// Deadline resolution
// ---------------------------------------------------------------------------

interface Resolution {
  abandoned: number
}

/**
 * Ends runs nothing has spoken about since their deadline.
 *
 * `abandoned` rather than `failed`, and the distinction is the point. A failure
 * is something the engine reported. This is the control plane admitting it
 * never heard, which is a different sentence to put in front of a person and a
 * different thing to act on: a failed run is a defect in the change, and an
 * abandoned one is a defect in the plumbing.
 *
 * The lease is cleared with the state so that a run cannot be abandoned while
 * an engine still believes it holds it and then acknowledged afterwards. The
 * projection refuses to move a terminal run, so a late report lands as a note
 * rather than as a resurrection.
 *
 * THE SENTENCE DEPENDS ON WHAT HAPPENED TO THE LEASE, and it has to, because
 * since the engine stopped reporting on a run it had lost, a run somebody took
 * and a run whose engine died read identically here. Both are silence at the
 * deadline. The counts on the row are the only thing that separates them, and a
 * console that renders `detail` and nothing else should still get the true
 * sentence rather than the generic one. See migration 0024.
 */
export async function resolveOverdueRuns(db: Db, now: Date): Promise<Resolution> {
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE workload_runs SET
      state = 'abandoned',
      finished_at = ${now.toISOString()}::timestamptz,
      -- No failure_code, deliberately. That column carries the code the ENGINE
      -- reported, from engine/internal/errors/catalog.yaml, and this is the one
      -- outcome no engine reported at all. An invented code that looks like a
      -- catalogued one is the worst error identifier this product can produce:
      -- somebody searches the errors reference for it and finds nothing.
      detail = COALESCE(detail, CASE
        WHEN lease_takeovers > 0 AND unheld_reports > 0 THEN
          'This run changed hands: another engine claimed it after the first engine''s lease ' ||
          'expired, and the first engine then tried to end it and was refused. So the first ' ||
          'engine did reach the end of its work and tried to say so, and the engine holding the ' ||
          'run is the one that said nothing before the deadline. It may have run: what is ' ||
          'missing is the report, not necessarily the work.'
        WHEN lease_takeovers > 0 THEN
          'This run changed hands: another engine claimed it after the first engine''s lease ' ||
          'expired, and then neither of them reported before the deadline. It may have run, on ' ||
          'either runner: what is missing is the report, not necessarily the work.'
        WHEN accepted_at IS NULL THEN
          'No engine ever claimed this run and none reported on it before its deadline. The ' ||
          'dispatch may never have reached an engine, or an engine may have been handed the run ' ||
          'directly and never got its report back. It may have run: what is missing is the ' ||
          'report, not necessarily the work.'
        ELSE
          'No engine reported on this run before its deadline. It may have run: what is missing ' ||
          'is the report, not necessarily the work.'
        END),
      lease_holder = NULL,
      lease_expires_at = NULL,
      updated_at = ${now.toISOString()}::timestamptz
    WHERE state IN ('requested', 'accepted', 'running')
      AND deadline_at <= ${now.toISOString()}::timestamptz
    RETURNING id`)
  return { abandoned: rows.length }
}

// ---------------------------------------------------------------------------
// Definitions
// ---------------------------------------------------------------------------

export interface WorkloadRow extends Record<string, unknown> {
  id: string
  slug: string
  name: string
  kind: WorkloadKind
  description: string | null
  repository: string
  repository_id: string
  archived_at: Date | string | null
  created_at: Date | string
  latest_version: number | null
}

export const workloadColumns = sql`
  w.id, w.slug, w.name, w.kind::text AS kind, w.description,
  w.repository_id, r.full_name AS repository, w.archived_at, w.created_at,
  (SELECT max(v.version) FROM workload_versions v WHERE v.workload_id = w.id) AS latest_version`

export async function findWorkload(db: Db, slug: string): Promise<WorkloadRow | null> {
  const rows = await db.execute<WorkloadRow>(sql`
    SELECT ${workloadColumns} FROM workloads w
    JOIN repositories r ON r.id = w.repository_id
    WHERE w.slug = ${slug} AND w.archived_at IS NULL`)
  return rows[0] ?? null
}

interface VersionRow extends Record<string, unknown> {
  id: string
  version: number
  body: Record<string, unknown>
  body_digest: string
  notes: string | null
  source: 'authored' | 'promoted'
  promoted_from_run_id: string | null
  created_at: Date | string
}

export const versionColumns = sql`
  id, version, body, body_digest, notes, source::text AS source,
  promoted_from_run_id, created_at`

/**
 * Appends a version, or returns the head when nothing changed.
 *
 * The advisory lock is what makes the version number a number rather than a
 * race: two people saving at once would both read `max(version) = 3` and both
 * try to write 4, and while the unique constraint would refuse the loser, it
 * would refuse it with a 23505 that reads to the caller as a bug rather than as
 * "somebody else saved first". Taking the lock first means the loser reads 4
 * and writes 5.
 *
 * `created: false` when the body is byte for byte what the head already says.
 * Saving a form nobody edited is the ordinary case, and answering it with a new
 * version would fill the history with entries that differ in nothing, which is
 * the same noise as a pull request comment posted once per push.
 */
export async function appendVersion(
  db: Db,
  input: {
    orgId: string
    workloadId: string
    kind: WorkloadKind
    rawBody: unknown
    notes?: string | null
    source?: 'authored' | 'promoted'
    promotedFromRunId?: string | null
    createdBy?: string | null
  },
): Promise<{ version: VersionRow; created: boolean }> {
  const { body, digest } = parseBody(input.kind, input.rawBody)

  await db.execute(sql`SELECT pg_advisory_xact_lock(hashtext(${`workload:${input.workloadId}`}))`)

  const head = await db.execute<VersionRow>(sql`
    SELECT ${versionColumns} FROM workload_versions
    WHERE workload_id = ${input.workloadId}
    ORDER BY version DESC LIMIT 1`)
  const current = head[0]
  if (current && current.body_digest === digest && (input.source ?? 'authored') === current.source) {
    return { version: current, created: false }
  }

  const next = (current?.version ?? 0) + 1
  const rows = await db.execute<VersionRow>(sql`
    INSERT INTO workload_versions (
      org_id, workload_id, version, body, body_digest, notes, source,
      promoted_from_run_id, created_by)
    VALUES (${input.orgId}, ${input.workloadId}, ${next}, ${JSON.stringify(body)}::jsonb,
            ${digest}, ${input.notes ?? null},
            ${input.source ?? 'authored'}::workload_version_source,
            ${input.promotedFromRunId ?? null}, ${input.createdBy ?? null})
    RETURNING ${versionColumns}`)
  return { version: rows[0]!, created: true }
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

export interface RunRow extends Record<string, unknown> {
  id: string
  workload_id: string
  workload_slug: string
  kind: WorkloadKind
  version: number
  state: RunState
  env_id: string
  repository: string
  git_ref: string
  attempt: number
  retry_of: string | null
  superseded_by: string | null
  verdict: string | null
  failure_code: string | null
  detail: string | null
  /** The `af` command the engine reported, or null when no engine has
   *  reported yet. A console that finds null says no command was recorded
   *  rather than assembling one that would drift from what ran. */
  reproduce_command: string | null
  manifest_digest: string | null
  requested_at: Date | string
  accepted_at: Date | string | null
  started_at: Date | string | null
  finished_at: Date | string | null
  deadline_at: Date | string
  cancel_requested_at: Date | string | null
  cancelled_at: Date | string | null
  dispatched_at: Date | string | null
  /** How many times the lease moved to a different engine, and when the last
   *  one was. Zero on the ordinary run: a first claim is not a takeover and
   *  neither is a re-claim by the same holder. */
  lease_takeovers: number
  lease_lost_at: Date | string | null
  /** How many terminal events arrived from an engine that does not hold this
   *  run and were refused, and when the last one was. A number above zero says
   *  the engine that lost the run was still alive at that moment and reached the
   *  end of its work, which is what distinguishes it from one that died. */
  unheld_reports: number
  unheld_report_at: Date | string | null
}

export const runColumns = sql`
  wr.id, wr.workload_id, w.slug AS workload_slug, w.kind::text AS kind,
  v.version, wr.state::text AS state, e.env_id, wr.repository, wr.git_ref,
  wr.attempt, wr.retry_of, wr.superseded_by, wr.verdict::text AS verdict,
  wr.failure_code, wr.detail, wr.reproduce_command, wr.manifest_digest,
  wr.requested_at, wr.accepted_at, wr.started_at,
  wr.finished_at, wr.deadline_at, wr.cancel_requested_at, wr.cancelled_at,
  wr.dispatched_at, wr.lease_takeovers, wr.lease_lost_at,
  wr.unheld_reports, wr.unheld_report_at`

export const runJoins = sql`
  FROM workload_runs wr
  JOIN workloads w ON w.id = wr.workload_id
  JOIN workload_versions v ON v.id = wr.workload_version_id
  JOIN environments e ON e.id = wr.environment_id`

export async function readRun(db: Db, runId: string): Promise<RunRow | null> {
  const rows = await db.execute<RunRow>(sql`
    SELECT ${runColumns} ${runJoins} WHERE wr.id = ${runId}`)
  return rows[0] ?? null
}

export interface StartInput {
  orgId: string
  workloadId: string
  versionId: string
  environmentId: string
  repository: string
  gitRef: string
  workflowFile: string | null
  requestKey: string
  requestedBy: string | null
  attempt?: number
  retryOf?: string | null
  now: Date
}

export interface StartOutcome {
  runId: string
  created: boolean
  /** Set when an identical request already made a run, or when another run of
   *  the same workload is already going on this environment. */
  reason?: string
}

/**
 * Writes the run, or names the one that is already going.
 *
 * Two collisions are possible and they mean different things, so they are told
 * apart by which constraint fired rather than by a read beforehand. A read
 * beforehand answers a question about a moment that has passed: two starts
 * landing together both see no live run and both insert, and only the database
 * can decide which of them wins.
 *
 *   org_id, request_key    the same request twice. A double click, a retried
 *                          HTTP call, a console that fired on two renders. The
 *                          answer is the run the first one made.
 *   workload_id,           a different request for work that is already going.
 *   environment_id         The answer is a refusal naming that run, because
 *                          two copies of one workload against one environment
 *                          measure each other rather than the application.
 */
export async function startRun(db: Db, input: StartInput): Promise<StartOutcome> {
  const now = input.now.toISOString()
  const deadline = new Date(input.now.getTime() + RUN_DEADLINE_MS).toISOString()

  // ON CONFLICT DO NOTHING with no target, so it covers BOTH unique indexes:
  // the request key and the one live run per workload per environment.
  //
  // Not a catch, and the reason is a property of this whole codebase rather
  // than of this file. postgres.js records the first failed query of a
  // transaction and rethrows it after the callback returns, whatever the
  // callback did about it, so catching a constraint violation inside
  // `withTenant`, rolling back to a savepoint and carrying on does not work:
  // the recovery runs, the later statements succeed, and the transaction still
  // rejects with the original error. Measured, not assumed. So every collision
  // this code turns into a sentence has to be one it never causes, and the read
  // back below is the only way to tell the two apart.
  const inserted = await db.execute<{ id: string }>(sql`
    INSERT INTO workload_runs (
      org_id, workload_id, workload_version_id, environment_id,
      requested_by, requested_at, request_key, repository, git_ref,
      workflow_file, deadline_at, attempt, retry_of, created_at, updated_at)
    VALUES (
      ${input.orgId}, ${input.workloadId}, ${input.versionId}, ${input.environmentId},
      ${input.requestedBy}, ${now}::timestamptz, ${input.requestKey}, ${input.repository},
      ${input.gitRef}, ${input.workflowFile}, ${deadline}::timestamptz,
      ${input.attempt ?? 1}, ${input.retryOf ?? null}, ${now}::timestamptz, ${now}::timestamptz)
    ON CONFLICT DO NOTHING
    RETURNING id`)
  if (inserted[0]) return { runId: inserted[0].id, created: true }

  // Which collision it was, and the two mean different things. The request key
  // first, because a repeated request is an answer and a live run is a refusal,
  // and a caller who sent the same key twice asked for the first thing.
  const repeated = await db.execute<{ id: string }>(sql`
    SELECT id FROM workload_runs WHERE request_key = ${input.requestKey}`)
  if (repeated[0]) {
    return {
      runId: repeated[0].id,
      created: false,
      reason: 'This exact request had already been made, so it is the same run rather than a second one.',
    }
  }

  const live = await db.execute<{ id: string; state: string }>(sql`
    SELECT id, state::text AS state FROM workload_runs
    WHERE workload_id = ${input.workloadId} AND environment_id = ${input.environmentId}
      AND state IN ('requested', 'accepted', 'running')`)
  if (live[0]) {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        `This workload is already ${live[0].state} against that environment, as run ` +
        `${live[0].id}. Two copies of one workload against one environment measure ` +
        `each other rather than the application. Cancel it, or wait for it to finish.`,
    })
  }

  // Neither, which means something conflicted that this function does not know
  // about. Thrown rather than returned as a null the caller would have to
  // handle: a run that neither exists nor can be created means an index was
  // added without this being taught about it, and that is a bug to see.
  throw new Error('a workload run could neither be created nor read back')
}

/**
 * An engine taking a run.
 *
 * The transition is `requested` to `accepted`, and it is a compare and set on
 * the state rather than a read and an update, so two engines polling the same
 * environment cannot both take it.
 *
 * Deliberately tolerant of a run that has already started. An engine's events
 * go through a sink that batches for five seconds and spools to disk when the
 * control plane is unreachable, so `workload.started` genuinely can be ingested
 * before the claim that was supposed to precede it. Refusing the claim then
 * would leave a running run with no lease and no heartbeat, and it would be
 * abandoned at its deadline while the work was going fine.
 *
 * A claim that takes the run from SOMEBODY ELSE is counted, and that count is
 * the only thing that later distinguishes a run somebody took from a run whose
 * engine died. Both go quiet and both end as `abandoned`, because an engine that
 * has lost its lease correctly stops reporting rather than ending a run another
 * engine is running. See migration 0024.
 *
 * A re-claim by the SAME holder is not a takeover. That is a runner that
 * restarted and asked again, and its own report is still the only one there will
 * be. The holder is a token identifier, so two runners sharing one token cannot
 * be told apart here: that is a real limit and it is recorded rather than
 * papered over, because the fix for it is a per-process identity in the claim
 * and that is an engine change, not this one.
 */
export async function claimRun(
  db: Db,
  input: { environmentId: string; holder: string; now: Date },
): Promise<{ runId: string; versionId: string } | null> {
  const now = input.now.toISOString()
  const leaseUntil = new Date(input.now.getTime() + RUN_LEASE_MS).toISOString()
  const rows = await db.execute<{ id: string; workload_version_id: string }>(sql`
    WITH claimable AS (
      SELECT id FROM workload_runs
      WHERE environment_id = ${input.environmentId}
        AND (
          state = 'requested'
          OR (state IN ('accepted', 'running') AND lease_expires_at < ${now}::timestamptz)
        )
        AND deadline_at > ${now}::timestamptz
        AND cancel_requested_at IS NULL
      ORDER BY requested_at
      LIMIT 1
      FOR UPDATE SKIP LOCKED
    )
    UPDATE workload_runs wr SET
      state = CASE WHEN wr.state = 'requested' THEN 'accepted'::workload_run_state ELSE wr.state END,
      accepted_at = COALESCE(wr.accepted_at, ${now}::timestamptz),
      -- The right hand side reads the OLD holder, which is the whole point:
      -- this is a takeover only when somebody else was holding it.
      lease_takeovers = wr.lease_takeovers + CASE
        WHEN wr.lease_holder IS NOT NULL AND wr.lease_holder <> ${input.holder} THEN 1
        ELSE 0 END,
      lease_lost_at = CASE
        WHEN wr.lease_holder IS NOT NULL AND wr.lease_holder <> ${input.holder}
        THEN ${now}::timestamptz ELSE wr.lease_lost_at END,
      lease_holder = ${input.holder},
      lease_expires_at = ${leaseUntil}::timestamptz,
      updated_at = ${now}::timestamptz
    FROM claimable
    WHERE wr.id = claimable.id
    RETURNING wr.id, wr.workload_version_id`)
  const row = rows[0]
  return row ? { runId: row.id, versionId: row.workload_version_id } : null
}

/**
 * An engine saying it is still there.
 *
 * Pushes the deadline as well as the lease. The deadline bounds silence, so a
 * run that keeps reporting is never abandoned for taking a long time, and a run
 * whose engine died is abandoned two hours after its last word rather than two
 * hours after it was asked for.
 */
export async function heartbeat(
  db: Db,
  input: { runId: string; holder: string; now: Date },
): Promise<{ held: boolean; cancelRequested: boolean }> {
  const now = input.now.toISOString()
  const rows = await db.execute<{ id: string; cancel_requested_at: Date | null }>(sql`
    UPDATE workload_runs SET
      lease_expires_at = ${new Date(input.now.getTime() + RUN_LEASE_MS).toISOString()}::timestamptz,
      deadline_at = ${new Date(input.now.getTime() + RUN_DEADLINE_MS).toISOString()}::timestamptz,
      updated_at = ${now}::timestamptz
    WHERE id = ${input.runId}
      AND state IN ('accepted', 'running')
      AND lease_holder = ${input.holder}
    RETURNING id, cancel_requested_at`)
  // The cancel travels back on the beat that is already happening. Without it
  // the only way a console cancel reaches a running engine is a poll of
  // /v1/commands/claim, which costs a minute of latency and takes a lease on
  // every other command it happens to return. The column is on the row this
  // statement already updates, so it is free.
  return { held: rows.length > 0, cancelRequested: rows[0]?.cancel_requested_at != null }
}

/**
 * Records that somebody asked for a run to stop.
 *
 * Only the request is recorded here. Whether it stopped is what the command in
 * workloads/commands.ts is for, and the two are separate columns because they
 * are separate facts: the whole defect this workstream started from was a
 * request recorded as an outcome.
 *
 * A run that is already terminal is not cancelled and does not become
 * cancellable. The caller is told which it was, so a console can say "that run
 * finished four minutes ago" instead of showing a cancel that did nothing.
 */
export async function requestCancel(
  db: Db,
  input: { runId: string; reason: string | null; by: string | null; now: Date },
): Promise<'requested' | 'already_requested' | 'already_finished' | 'not_found'> {
  const rows = await db.execute<{ state: RunState; cancel_requested_at: Date | null }>(sql`
    SELECT state::text AS state, cancel_requested_at FROM workload_runs WHERE id = ${input.runId}`)
  const row = rows[0]
  if (!row) return 'not_found'
  if ((TERMINAL_STATES as readonly string[]).includes(row.state)) return 'already_finished'
  if (row.cancel_requested_at) return 'already_requested'

  const updated = await db.execute<{ id: string }>(sql`
    UPDATE workload_runs SET
      cancel_requested_at = ${input.now.toISOString()}::timestamptz,
      cancel_requested_by = ${input.by},
      cancel_reason = ${input.reason},
      updated_at = ${input.now.toISOString()}::timestamptz
    WHERE id = ${input.runId}
      AND state IN ('requested', 'accepted', 'running')
      AND cancel_requested_at IS NULL
    RETURNING id`)
  // Zero rows means the run reached a terminal state between the read above
  // and this statement, which is the cancel racing the completion. The run
  // finished, so that is the answer.
  return updated.length > 0 ? 'requested' : 'already_finished'
}

/**
 * Ends a cancelled run that no engine will ever report on.
 *
 * A cancel of a run in `requested` has nobody to reach: no engine has claimed
 * it, so no engine will ever send a terminal event for it, and waiting for one
 * would leave it open until its deadline. So a cancel of an unclaimed run
 * settles it here and now, and a cancel of a claimed one waits for the engine
 * to say what happened, because it may well have finished the work.
 */
export async function cancelUnclaimed(
  db: Db,
  input: { runId: string; now: Date },
): Promise<boolean> {
  const now = input.now.toISOString()
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE workload_runs SET
      state = 'cancelled',
      cancelled_at = ${now}::timestamptz,
      finished_at = ${now}::timestamptz,
      detail = COALESCE(detail, 'Cancelled before any engine claimed it, so nothing ran.'),
      lease_holder = NULL,
      lease_expires_at = NULL,
      updated_at = ${now}::timestamptz
    WHERE id = ${input.runId} AND state = 'requested'
    RETURNING id`)
  return rows.length > 0
}

/**
 * Marks a run as superseded by its retry.
 *
 * Only a terminal run may be retried, so this cannot orphan work that is still
 * going, and a run that has already been superseded is refused by the caller
 * naming the run that took its place. Retrying the same failure twice and
 * getting two independent successors is how a history stops being a history.
 */
export async function markSuperseded(
  db: Db,
  input: { runId: string; by: string; now: Date },
): Promise<boolean> {
  const rows = await db.execute<{ id: string }>(sql`
    UPDATE workload_runs SET superseded_by = ${input.by}, updated_at = ${input.now.toISOString()}::timestamptz
    WHERE id = ${input.runId} AND superseded_by IS NULL
    RETURNING id`)
  return rows.length > 0
}
