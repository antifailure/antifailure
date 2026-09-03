// Turning the stream into the numbers a dashboard reads.
//
// WHY A ROLLUP AT ALL, WHEN THE STREAM IS RIGHT THERE.
//
// Because the application cannot read the stream. GRANT on analytics_events is
// INSERT and nothing else, so a dashboard query against it raises 42501. The
// rollup runs as the schema owner, reads the stream, writes counts, and the
// counts are the only thing that comes back out. That is what makes "the
// control plane cannot look up what one organization did" a property somebody
// can check rather than a promise.
//
// WHY IT RECOMPUTES RATHER THAN ACCUMULATES.
//
// Events arrive late and out of order. An engine that was offline sends a day
// of backlog at once; a browser beacon retries after the tab was asleep. An
// accumulating rollup has to decide what to do with an event for a day it has
// already closed, and every answer is wrong: drop it and the day is short,
// add it and the number depends on when the job happened to run.
//
// Recomputing a day from the rows makes the question disappear. The same day
// recomputed twice gives the same answer, so the job is idempotent, a run that
// crashed halfway can simply be run again, and a late event is absorbed by the
// next run that covers its day. The cost is reading a few days of rows on a
// schedule, which is a bounded index scan.
//
// WHICH DAYS IT RECOMPUTES, AND THE TRADE IN THAT NUMBER.
//
// The last `lookbackDays` days, ending with today. Larger absorbs later
// arrivals and reads more rows; smaller is cheaper and settles a day sooner.
// Three days is the default because it covers a weekend outage of the sender
// and is four orders of magnitude below the retention window, so nothing about
// it is close to a limit.
//
// A day older than the lookback is SETTLED: an event for it will never be
// counted. That is written into analytics_rollup_state.settled_after and shown
// on the dashboard, because a chart whose last three days are still moving and
// whose older days are frozen is a chart that misleads anybody who does not
// know which is which.

import type postgres from 'postgres'
import type { Clock } from '../clock.ts'
import { CATALOG, EVENT_NAMES, type EventName } from './catalog.ts'
import { SUBJECT_DAYS_KEPT, recomputeInsights, type InsightResult } from './insights.ts'

export interface RollupOptions {
  /** How many days back to recompute, including today. */
  lookbackDays?: number
  /** Days of raw events to keep. Undefined never deletes, which is the default:
   *  retention is an operator's decision and not a library's. */
  retentionDays?: number
  now?: Date
}

export interface RollupResult {
  /**
   * Whether this call did the work.
   *
   * False when another process held the rollup lock, which is the ordinary
   * state on a deployment with more than one replica: they all start their
   * maintenance pass at once. Separate from `days` being empty, because a
   * rollup that ran over a day with no events and a rollup that never started
   * are different things and only one of them is worth logging.
   */
  ran: boolean
  /** The days recomputed, oldest first. */
  days: string[]
  /** Rows written into analytics_daily. */
  rows: number
  /** Raw events deleted by retention. */
  pruned: number
  /** The oldest day that will never be recomputed again. */
  settledAfter: string
  /** What the insight passes did. See insights.ts. */
  insights: InsightResult
}

const DEFAULT_LOOKBACK_DAYS = 3

/** The dimension columns each event rolls up under, as SQL over the payload.
 *  Built from the catalog rather than written twice, so an event that gains a
 *  dimension gains it here without anybody remembering to come and do it. */
function dimensionExpression(name: EventName, which: 0 | 1): string {
  const field = CATALOG[name].dimensions[which]
  if (field === undefined) return `''`
  // The field name comes from the catalog, which is source in this repository
  // and not input. Checked anyway, because the value is spliced into SQL and
  // "it comes from a constant" is exactly what stops being true later.
  if (!/^[a-z][a-z0-9_]*$/.test(field)) {
    throw new Error(`the catalog declares a dimension named ${field}, which cannot be a column`)
  }
  return `COALESCE(payload->>'${field}', '')`
}

/**
 * Recomputes the daily aggregates, and prunes the raw stream.
 *
 * Runs as the owner. Every statement is scoped to one day and one event name so
 * that a partial run leaves a consistent set of days rather than a half-written
 * one, and so a failure part-way through is finished by the next run.
 */
export async function rollUp(
  admin: postgres.Sql,
  clock: Clock,
  options: RollupOptions = {},
): Promise<RollupResult> {
  const lookback = Math.max(1, options.lookbackDays ?? DEFAULT_LOOKBACK_DAYS)
  const now = options.now ?? clock.now()
  const days: string[] = []
  for (let back = lookback - 1; back >= 0; back -= 1) {
    days.push(dayString(new Date(now.getTime() - back * 86_400_000)))
  }
  const settledAfter = days[0]!

  // ONE ROLLUP AT A TIME ACROSS THE WHOLE DEPLOYMENT.
  //
  // This rides the maintenance pass, every replica runs that pass, and it runs
  // once immediately on start. Production is configured for two replicas, so
  // two of these begin within milliseconds of each other on every deploy.
  //
  // recomputeDay is a DELETE and an INSERT in one transaction, which is right
  // for one writer and is a race for two: the second insert lands on rows the
  // first has already written and fails on the primary key. That aborts the
  // whole maintenance pass on that replica, so the failure is not a wrong
  // number, it is a dashboard that silently stops updating while a line goes
  // into a log nobody reads. Found by running three rollups at once, which no
  // test did until one asked what happens in that order.
  //
  // TRY rather than wait, unlike the migration lock. Waiting would make the
  // second replica recompute days the first has just finished, which is a full
  // scan of the stream for an answer that is already correct. There is nothing
  // to wait for.
  const lock = await admin.reserve()
  try {
    const [held] = await lock<{ ok: boolean }[]>`
      SELECT pg_try_advisory_lock(hashtext('antifailure.analytics.rollup')) AS ok`
    if (held?.ok !== true) {
      return {
        ran: false,
        days: [],
        rows: 0,
        pruned: 0,
        settledAfter,
        insights: {
          subjectDayRows: 0,
          activeRows: 0,
          funnelRows: 0,
          retentionRows: 0,
          subjectDaysPruned: 0,
          funnelsFinalBefore: settledAfter,
          cohortsCompleteThrough: null,
        },
      }
    }
    try {
      return await recompute(admin, days, now, lookback, settledAfter, options)
    } finally {
      await lock`SELECT pg_advisory_unlock(hashtext('antifailure.analytics.rollup'))`
    }
  } finally {
    lock.release()
  }
}

/** The rollup itself, with the lock above already held. */
async function recompute(
  admin: postgres.Sql,
  days: string[],
  now: Date,
  lookback: number,
  settledAfter: string,
  options: RollupOptions,
): Promise<RollupResult> {

  let rows = 0
  for (const day of days) {
    for (const name of EVENT_NAMES) {
      rows += await recomputeDay(admin, day, name)
    }
  }

  // The insights, after the daily counts and before the raw stream is pruned.
  //
  // AFTER, because the working set they start from is read out of the same rows
  // analytics_daily was just computed from, and a pass that ran first would
  // build this day's insights out of yesterday's stream.
  //
  // BEFORE the prune, because every one of them reads the raw stream for the
  // days it is recomputing, and pruning first would recompute a day whose rows
  // had just been deleted and write a zero over a real number. The two orders
  // differ only on the boundary day, which is exactly the kind of difference
  // that shows up as one wrong bar a month after somebody reorders these.
  const insights = await recomputeInsights(admin, days, now, lookback)

  // Retention on the raw stream, which is the half that carries surrogates.
  // The aggregates outlive it deliberately: a count of page views by source has
  // nothing in it to identify anybody, so keeping a year of those while the
  // rows they were computed from are gone is the shape a retention policy
  // should have.
  let pruned = 0
  if (options.retentionDays !== undefined) {
    pruned = await prune(admin, now, options.retentionDays)
  }

  await admin`
    UPDATE analytics_rollup_state
    SET last_run_at = ${now.toISOString()}::timestamptz,
        settled_after = ${settledAfter}::date,
        -- Three freshness answers rather than one, because these settle at
        -- different rates and a page that showed the daily figure for all of
        -- them would be wrong about two. A funnel with a thirty day window
        -- cannot be final for last week; a cohort's own week has to end before
        -- its first column is its size.
        funnels_final_before = ${insights.funnelsFinalBefore}::date,
        cohorts_complete_through = ${insights.cohortsCompleteThrough}::date,
        subject_days_kept = ${SUBJECT_DAYS_KEPT}
    WHERE id`

  return { ran: true, days, rows, pruned, settledAfter, insights }
}

/**
 * One day, one event name, computed from the rows and written over whatever was
 * there.
 *
 * DELETE then INSERT rather than an upsert, and the reason is a real failure
 * shape rather than taste: an upsert leaves behind a row for a dimension
 * combination that USED to have events on that day and no longer does, which
 * happens whenever the lookback recomputes a day whose rows were pruned. That
 * stale row then sits on the chart forever with a count nobody can trace.
 *
 * Both statements in one transaction, so a reader never sees a day with the old
 * rows removed and the new ones not yet written, which would render as a hole.
 */
async function recomputeDay(admin: postgres.Sql, day: string, name: EventName): Promise<number> {
  const dimA = dimensionExpression(name, 0)
  const dimB = dimensionExpression(name, 1)

  return admin.begin(async (tx) => {
    await tx`DELETE FROM analytics_daily WHERE day = ${day}::date AND name = ${name}`
    const inserted = await tx.unsafe(
      `INSERT INTO analytics_daily (day, name, dim_a, dim_b, events, organizations, sessions, computed_at)
       SELECT $1::date,
              $2,
              ${dimA},
              ${dimB},
              count(*),
              -- Distinct rather than a sum, because one organization creating
              -- forty environments is one adopter and forty events, and a
              -- funnel that cannot tell those apart is a funnel that reports
              -- the busiest customer as the whole market.
              count(DISTINCT org_surrogate),
              count(DISTINCT session_surrogate),
              $3::timestamptz
       FROM analytics_events
       WHERE occurred_at >= $1::date
         AND occurred_at < ($1::date + interval '1 day')
         AND name = $2
       GROUP BY 3, 4`,
      [day, name, new Date().toISOString()],
    )
    return inserted.count ?? 0
  })
}

/**
 * Deletes raw events older than the retention window.
 *
 * Bounded per call so a backlog is worked off over several runs rather than in
 * one statement holding locks for minutes, which is the same shape and the same
 * reason as pruneDefault in the partition manager.
 *
 * A whole partition older than the window is dropped by the partition manager,
 * which is a catalogue update. This handles the remainder: the partial month at
 * the boundary, and whatever landed in the default partition.
 */
async function prune(admin: postgres.Sql, now: Date, retentionDays: number): Promise<number> {
  const cutoff = new Date(now.getTime() - retentionDays * 86_400_000)
  const deleted = await admin`
    DELETE FROM analytics_events
    WHERE ctid IN (
      SELECT ctid FROM analytics_events WHERE occurred_at < ${cutoff.toISOString()}::timestamptz
      LIMIT 20000)`
  return deleted.count ?? 0
}

function dayString(at: Date): string {
  return at.toISOString().slice(0, 10)
}

/** Reads the retention from the environment, refusing a value that is not a
 *  whole number of days rather than silently keeping raw events forever. */
export function analyticsRetentionFromEnv(env: NodeJS.ProcessEnv): number | undefined {
  const raw = env.AF_ANALYTICS_RETENTION_DAYS
  if (raw === undefined || raw === '') return undefined
  const days = Number(raw)
  if (!Number.isInteger(days) || days < 1) {
    throw new Error(
      `AF_ANALYTICS_RETENTION_DAYS is ${JSON.stringify(raw)}; it has to be a whole number of days, at least 1`,
    )
  }
  return days
}
