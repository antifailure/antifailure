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
    RETURNING id`)

  if (moved.length === 0) {
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
