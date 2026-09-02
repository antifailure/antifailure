// What an engine's events do to a workload run.
//
// The same three properties ingest.ts is built on apply here and are not
// re-implemented: duplicates are dropped by the events table's unique
// constraint before this is called, order is decided by the sequence rather
// than by arrival, and a batch with one bad event keeps the good ones.
//
// What this file adds is the fourth: a transition is a compare and set on the
// state, in one statement, and NOTHING is written unless that statement moved a
// row. A report about a run that has already ended writes no results, no route
// metrics and no thresholds, because it moved nothing. That is what makes
// duplicate reports free, and it is what makes a late report about an abandoned
// run a note rather than a resurrection.
//
// THE ORDERING THIS FILE EXISTS TO GET RIGHT.
//
// A workload run is minted by the control plane, so an event can only name a
// run this database created. The orderings that are still reachable, and what
// each does:
//
//   started then finished    the ordinary case.
//   finished then started    the started event lost a race in the spool. The
//                            finish moved the run to a terminal state and the
//                            start then matches nothing.
//   finished twice           the second matches nothing, and the result tables
//                            have no UPDATE grant at all, so even a bug could
//                            not rewrite the first.
//   an unknown run id        another organization's run, a run whose workload
//                            was archived and cascaded, or a sender confusion.
//                            Stored, unprojected, and said out loud.
//   a report after the       the deadline already ended the run. The report is
//   deadline                 stored and noted, and the run keeps saying nobody
//                            reported in time, which is the true statement.
//   a terminal event from    REFUSED, and counted. Another engine holds the run
//   an engine that no        and may be running it right now.
//   longer holds the run
//
// THE FIFTH ROW, AND WHY IT IS NOT MERELY DEFENSIVE.
//
// A lease is fifteen minutes and a heartbeat extends it. An engine on a runner
// with bad connectivity can miss several and keep the run; miss enough and the
// lease expires and a SECOND engine polling the same environment claims it and
// starts doing the work.
//
// The statement below used to be gated on the run's STATE alone. So the FIRST
// engine's terminal event ended the run, and the second engine's report then
// arrived against a row that was already terminal and was refused as a note.
// The measurements of the engine that actually did the work were destroyed by
// the engine that had lost it. The engine side closed the near end of this: an
// engine told 409 by its heartbeat now stops reporting entirely. This closes the
// far end, which is the half the engine cannot reach, because an engine that
// never got an answer to a heartbeat never learns it lost anything.
//
// A run with NO holder still accepts a terminal event, and that arm is
// deliberate rather than lax. Two ordinary things produce it: a run given to an
// engine by `--run-id`, which deliberately declines to claim so that somebody
// reproducing a hosted run does not take CI's next one, and the spool race where
// the terminal event overtakes the claim. Refusing those would abandon runs that
// finished.
//
// It cannot destroy a competing report either, and that is an argument rather
// than a hope. `claimRun` takes a run that is `requested`, or one whose lease
// has EXPIRED; a run in `accepted` or `running` with a null `lease_expires_at`
// fails that comparison and is claimable by nobody. So a run with no holder that
// an engine is working on has no second engine to lose a report to.
//
// The one ordering this does not make safe is a run somebody is reproducing by
// `--run-id` while CI claims the same run. There the holder wins and the person
// reproducing it is refused, which is the consistent rule and is visible: the
// event is stored whole, the refusal is counted, and the sentence says which
// engine holds it. The alternative, last writer wins, is what this whole file is
// getting away from.
//
// A refusal is COUNTED on the row rather than only answered in the response.
// An engine that stood down and was refused is proof the mechanism worked, and
// it is what tells an abandoned run that changed hands from one whose only
// engine died. See migration 0027.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { WorkloadKind } from './bodies.ts'
import { decodeReport, writeReport, ReportRefused } from './results.ts'
import { settleCancelForRun } from './commands.ts'

/** The event types this file handles. Exported so ingest.ts declares them in
 *  one place and the drift test on the engine side reads that one place. */
export const WORKLOAD_EVENT_TYPES = [
  'workload.started',
  'workload.finished',
  'workload.cancelled',
] as const

export type WorkloadEventType = (typeof WORKLOAD_EVENT_TYPES)[number]

export function isWorkloadEvent(type: string): type is WorkloadEventType {
  return (WORKLOAD_EVENT_TYPES as readonly string[]).includes(type)
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/**
 * The terminal states an engine may name for itself.
 *
 * `abandoned` is deliberately absent: it is the control plane admitting it
 * never heard, and an engine that is reporting has by definition been heard.
 */
const TERMINAL_FROM_ENGINE = new Set(['succeeded', 'failed', 'cancelled', 'timed_out'])

/**
 * Applies one workload event.
 *
 * Returns null when the event was projected and a sentence when it was not. The
 * sentence reaches the sender in the batch response, which is what turned a
 * silently swallowed environment event into a visible number when the same
 * shape was fixed in ingest.ts.
 */
export async function projectWorkloadEvent(
  db: Db,
  clock: Clock,
  orgId: string,
  /** The token the batch was authenticated with, which is the same string
   *  `claimRun` writes into `lease_holder`. It is what says whether the sender
   *  still holds the run it is talking about. */
  holder: string,
  event: { type: string; sequence?: number; occurredAt: string; payload?: Record<string, unknown> },
): Promise<string | null> {
  const payload = event.payload ?? {}
  const runId = typeof payload.workload_run_id === 'string' ? payload.workload_run_id.trim() : ''
  if (!UUID.test(runId)) {
    return (
      'Stored, but this event carries no workload_run_id that could name a run. Every ' +
      'workload.* event has to carry the run identifier the control plane handed the engine.'
    )
  }

  // The run is read before anything is written, for the kind, which decides how
  // the report is decoded. The read is scoped by the ordinary tenant policy, so
  // a run belonging to another organization is simply not there and gets the
  // same answer as one that does not exist.
  const rows = await db.execute<{ id: string; kind: WorkloadKind; state: string }>(sql`
    SELECT wr.id, w.kind::text AS kind, wr.state::text AS state
    FROM workload_runs wr JOIN workloads w ON w.id = wr.workload_id
    WHERE wr.id = ${runId}`)
  const run = rows[0]
  if (!run) {
    return (
      `Stored, but ${runId} is not a workload run this control plane has, so nothing was ` +
      `updated. A run is created here and its identifier is handed to the engine, so an ` +
      `identifier this organization does not hold is one from somewhere else.`
    )
  }

  const sequence = event.sequence ?? 0
  const now = clock.now().toISOString()

  switch (event.type) {
    case 'workload.started':
      return started(db, run.id, sequence, event.occurredAt, now)
    case 'workload.cancelled':
      return terminal(db, clock, {
        runId: run.id,
        kind: run.kind,
        orgId,
        holder,
        state: 'cancelled',
        sequence,
        occurredAt: event.occurredAt,
        now,
        payload,
        writeResults: false,
      })
    case 'workload.finished': {
      // The engine says whether the work itself completed. The verdict inside
      // the report says what it found, and the two are deliberately not the
      // same column: a run that finished cleanly and failed every threshold is
      // `succeeded` with a verdict of `fail`, and collapsing them is how an
      // exit code over work that never happened reads as a pass.
      // `state` first, because the engine has terminal states this control
      // plane also has and `outcome` cannot express. A run that passed its own
      // --timeout is `timed_out`, which is an interruption rather than a
      // failure, and reading only `outcome` recorded it as SUCCEEDED: the enum
      // had a value nothing could ever reach, which is the same shape as a
      // green exit code over work that never happened. Found by studio-emitters
      // reading this switch rather than by anything running.
      const outcome = typeof payload.outcome === 'string' ? payload.outcome : 'succeeded'
      const declared = typeof payload.state === 'string' ? payload.state : outcome
      const state = TERMINAL_FROM_ENGINE.has(declared)
        ? (declared as Terminal['state'])
        : outcome === 'failed'
          ? 'failed'
          : 'succeeded'
      return terminal(db, clock, {
        runId: run.id,
        kind: run.kind,
        orgId,
        holder,
        state,
        sequence,
        occurredAt: event.occurredAt,
        now,
        payload,
        writeResults: true,
      })
    }
    default:
      return null
  }
}

async function started(
  db: Db,
  runId: string,
  sequence: number,
  occurredAt: string,
  now: string,
): Promise<string | null> {
  const moved = await db.execute<{ id: string }>(sql`
    UPDATE workload_runs SET
      state = 'running',
      started_at = COALESCE(started_at, ${occurredAt}::timestamptz),
      accepted_at = COALESCE(accepted_at, ${occurredAt}::timestamptz),
      last_sequence = GREATEST(last_sequence, ${sequence}),
      updated_at = ${now}::timestamptz
    WHERE id = ${runId}
      AND state IN ('requested', 'accepted')
      AND last_sequence < ${sequence}
    RETURNING id`)
  if (moved.length > 0) return null

  const state = await db.execute<{ state: string }>(sql`
    SELECT state::text AS state FROM workload_runs WHERE id = ${runId}`)
  return (
    `Stored, and the run was already ${state[0]?.state ?? 'gone'}, so the start changed nothing. ` +
    `A start that arrives after a finish is an ordering, not a fault: the run is not moved back.`
  )
}

interface Terminal {
  runId: string
  kind: WorkloadKind
  orgId: string
  /** Who sent it. See the header's fifth row. */
  holder: string
  state: 'succeeded' | 'failed' | 'cancelled' | 'timed_out'
  sequence: number
  occurredAt: string
  now: string
  payload: Record<string, unknown>
  writeResults: boolean
}

/**
 * The one statement that ends a run, and the gate everything else hangs off.
 *
 * Results are written only when this moved a row. Doing it the other way round,
 * writing the measurements and then trying to end the run, would leave a
 * duplicate report inserting rows against a run that had already been abandoned
 * and reported on, which is a history that grows a second answer.
 *
 * TWO CONDITIONS, NOT ONE. The state has to be live AND the sender has to be the
 * holder, or there has to be no holder at all. State alone was the defect: it
 * let an engine that had lost the run end it, and the report of the engine that
 * was actually running it then arrived against a terminal row and was refused.
 * See the header.
 */
async function terminal(db: Db, clock: Clock, t: Terminal): Promise<string | null> {
  let report: ReturnType<typeof decodeReport> | null = null
  if (t.writeResults) {
    try {
      report = decodeReport(t.kind, t.payload)
    } catch (error) {
      if (error instanceof ReportRefused) {
        return `Stored, and nothing was applied: ${error.message}.`
      }
      throw error
    }
  }

  const verdict = report?.verdict ?? null
  const detail = report?.detail ?? textOf(t.payload.detail)
  const failureCode = report?.failureCode ?? textOf(t.payload.failure_code)
  // The command that reproduces the run, as the engine reported it, and the
  // digest of the manifest it read. Stored rather than rebuilt: a command a
  // console assembles from a form drifts from the one that actually ran, and
  // being the same one is the only reason to print it.
  const reproduce = obj(t.payload.reproduce)
  const command = textOf(reproduce.command)
  const manifestDigest = textOf(reproduce.manifest_digest)

  const moved = await db.execute<{ id: string }>(sql`
    UPDATE workload_runs SET
      state = ${t.state}::workload_run_state,
      finished_at = ${t.occurredAt}::timestamptz,
      cancelled_at = CASE WHEN ${t.state} = 'cancelled'
                          THEN ${t.occurredAt}::timestamptz ELSE cancelled_at END,
      verdict = COALESCE(${verdict}::verdict_value, verdict),
      detail = COALESCE(${detail}, detail),
      failure_code = COALESCE(${failureCode}, failure_code),
      reproduce_command = COALESCE(${command}, reproduce_command),
      manifest_digest = COALESCE(${manifestDigest}, manifest_digest),
      last_sequence = GREATEST(last_sequence, ${t.sequence}),
      lease_holder = NULL,
      lease_expires_at = NULL,
      updated_at = ${t.now}::timestamptz
    WHERE id = ${t.runId} AND state IN ('requested', 'accepted', 'running')
      -- Or there is no holder, which is the --run-id run nobody claimed and
      -- the spool race where the report overtook the claim.
      AND (lease_holder IS NULL OR lease_holder = ${t.holder})
    RETURNING id`)

  if (moved.length === 0) {
    // Which of the two refusals it was, decided by a statement rather than by a
    // read followed by a decision. The read-then-decide version has a window in
    // which another engine claims the run between the two, and it would report
    // the wrong one of these two sentences to whichever engine lost that race.
    // This also records the refusal in the same breath as detecting it, so the
    // count cannot drift from the answer that was given.
    const unheld = await db.execute<{ holder: string }>(sql`
      UPDATE workload_runs SET
        unheld_reports = unheld_reports + 1,
        unheld_report_at = ${t.now}::timestamptz,
        updated_at = ${t.now}::timestamptz
      WHERE id = ${t.runId}
        AND state IN ('requested', 'accepted', 'running')
        AND lease_holder IS NOT NULL AND lease_holder <> ${t.holder}
      RETURNING lease_holder AS holder`)
    if (unheld.length > 0) {
      return (
        `Stored, and nothing was applied: this run is held by another engine now, so this ` +
        `report cannot end it. The lease was taken after it expired and the engine holding it ` +
        `may be running the work right now; ending the run here would refuse that engine's ` +
        `report when it arrives. Nothing in this event is lost, it is kept whole in the event ` +
        `log, and the run records that an engine which no longer holds it tried to end it. ` +
        `Stop and claim again.`
      )
    }

    const state = await db.execute<{ state: string }>(sql`
      SELECT state::text AS state FROM workload_runs WHERE id = ${t.runId}`)
    return (
      `Stored, and the run was already ${state[0]?.state ?? 'gone'}, so nothing was applied. ` +
      `Nothing about a finished run is rewritten by a later report about it.`
    )
  }

  if (report) {
    await writeReport(db, { orgId: t.orgId, runId: t.runId, kind: t.kind, report: report })
  }

  // A cancel that was outstanding is settled here rather than left for an
  // engine that has nothing left to cancel. See settleCancelForRun.
  await settleCancelForRun(db, { runId: t.runId, finalState: t.state, now: clock.now() })

  const skipped = report?.skipped
  if (skipped && skipped.routes + skipped.thresholds + skipped.evidence > 0) {
    // Said out loud rather than swallowed. A decoder that quietly drops what it
    // cannot read is how a report loses half its routes and the console shows a
    // clean run, and the number is the only thing that makes it findable.
    return (
      `Applied, and ${skipped.routes} route measurements, ${skipped.thresholds} thresholds and ` +
      `${skipped.evidence} pieces of evidence could not be read and were left out. The rest of ` +
      `the report was written.`
    )
  }
  return null
}

function obj(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function textOf(value: unknown): string | null {
  if (typeof value !== 'string') return null
  const trimmed = value.trim()
  if (trimmed.length === 0 || trimmed.length > 2000) return null
  return trimmed
}
