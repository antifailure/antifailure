// The three insights a daily count cannot produce, computed by the rollup.
//
// WHY THESE RUN AS THE OWNER AND NOT AS THE APPLICATION.
//
// Every one of them needs to follow one subject: across two days for a distinct
// count over a week, across two events for a funnel, across two weeks for a
// retention cohort. That means reading rows that carry a surrogate, and the
// application holds no SELECT on any of them. So the work happens here, on the
// privileged connection the partition manager already uses, and what comes back
// out is counts. See migrations/0033 for why that is a permission and not a
// convention.
//
// WHY EVERY PASS RECOMPUTES RATHER THAN ACCUMULATES.
//
// The same argument rollup.ts makes for analytics_daily, and it applies harder
// here. A late event does not just add to a count, it can change a subject's
// funnel depth from two to three, which is a row moving between buckets rather
// than a number growing. An accumulating version would have to find and undo
// the old bucket, and getting that wrong is invisible. Recomputing a bounded
// window makes the question disappear: the same input produces the same output
// however many times the job runs, and a run that died halfway is fixed by
// running it again.
//
// WHAT BOUNDS EACH WINDOW, WHICH IS THE ONLY REASON THIS IS AFFORDABLE.
//
//   subject days   one day of the stream per pass, the same days
//                  analytics_daily recomputes
//   actives        the same days, each reading back at most ACTIVE_WINDOWS days
//                  of the working set
//   funnels        the widest funnel window plus the lookback of the stream,
//                  and no further, because a subject whose window has closed
//                  cannot progress
//   retention      the retained cohort weeks of the working set
//
// Two of those read a large fraction of the working set rather than a narrow
// slice, and Postgres reads them sequentially. That is the correct plan for a
// bulk pass that runs once per maintenance interval, and the thing that bounds
// it is SUBJECT_DAYS_KEPT rather than an index. A comment beside each says what
// it costs at what row count, and a test measures the one that matters.

import type postgres from 'postgres'
import { CATALOG, FUNNEL_DEFINITIONS, type FunnelDefinition } from './catalog.ts'

/**
 * The windows a distinct count is kept for.
 *
 * One, seven and twenty eight days. Twenty eight rather than thirty because a
 * four week window contains the same number of each weekday, so it does not
 * wobble by a seventh depending on where the month started, which is the reason
 * every analytics product uses it.
 *
 * A window nobody asked for is a row nobody reads, so this list is short on
 * purpose and adding to it is a decision rather than a default.
 */
export const ACTIVE_WINDOWS = [1, 7, 28] as const

/** How many cohort weeks the retention grid holds. Twelve is a quarter, which
 *  is the period a retention question is usually asked over, and it is small
 *  enough that the grid fits on a screen at 320 pixels wide. */
export const RETENTION_WEEKS = 12

/**
 * How many days of the working set are kept.
 *
 * The grid needs activity from the oldest cohort's week to today, which is
 * RETENTION_WEEKS weeks. The extra fortnight is slack so that the oldest cohort
 * is complete rather than clipped on the day the pruning runs, and so a run
 * that was missed for a week does not lose a cohort.
 *
 * These rows carry a surrogate, so this is a retention policy and not a cache
 * size. It is deliberately far shorter than the aggregates it feeds, which is
 * the same shape as 0029's rule: the rows that carry a surrogate go, and the
 * counts computed from them stay.
 */
export const SUBJECT_DAYS_KEPT = RETENTION_WEEKS * 7 + 14

/** The two populations. A session never returns, so it has cohorts and no
 *  retention; an organization has both. */
const SUBJECT_KINDS = ['organization', 'session'] as const

export interface InsightResult {
  subjectDayRows: number
  activeRows: number
  funnelRows: number
  retentionRows: number
  subjectDaysPruned: number
  /** The oldest entry week whose funnel numbers can still change. */
  funnelsFinalBefore: string
  /** The newest cohort week whose row is complete, or null when the grid is
   *  empty. A cohort whose week has not ended is still accumulating. */
  cohortsCompleteThrough: string | null
}

/**
 * One day of the working set, recomputed from the stream.
 *
 * DELETE then INSERT for the same reason recomputeDay does it: an upsert leaves
 * behind a row for a subject that used to have an event on this day and no
 * longer does, which happens whenever a day whose raw rows were pruned is
 * recomputed. That subject would then sit in a distinct count forever.
 *
 * COST. One index scan of analytics_events_day_idx over one day, then a hash
 * aggregate. At ten thousand events a day this reads ten thousand rows and
 * writes at most that many; at a million a day it reads a million and writes
 * distinct(subject, name), which for a site is far smaller because a session
 * produces several events under the same name.
 */
export async function recomputeSubjectDays(admin: postgres.Sql, day: string): Promise<number> {
  return admin.begin(async (tx) => {
    await tx`DELETE FROM analytics_subject_days WHERE day = ${day}::date`
    const inserted = await tx`
      INSERT INTO analytics_subject_days (subject_kind, subject, name, day, events, computed_at)
      SELECT 'organization', org_surrogate, name, ${day}::date, count(*), now()
      FROM analytics_events
      WHERE occurred_at >= ${day}::date
        AND occurred_at < (${day}::date + interval '1 day')
        AND org_surrogate IS NOT NULL
      GROUP BY 2, 3
      UNION ALL
      SELECT 'session', session_surrogate, name, ${day}::date, count(*), now()
      FROM analytics_events
      WHERE occurred_at >= ${day}::date
        AND occurred_at < (${day}::date + interval '1 day')
        AND session_surrogate IS NOT NULL
      GROUP BY 2, 3`
    return inserted.count ?? 0
  })
}

/**
 * Distinct subjects over each window, as of one day.
 *
 * Both the per event counts and the total, and the total is not the sum of the
 * others: a session that viewed a page and pressed a button is one active
 * session and two rows. That difference is the whole reason this is
 * materialized rather than added up on the page.
 *
 * COST, AND A CLAIM THAT WAS WRONG BEFORE A TEST MEASURED IT. This reads the
 * widest window, twenty eight days, which is a LARGE FRACTION of everything the
 * working set retains, so Postgres reads it sequentially and is right to: an
 * index scan over a third of a table is slower than the table. The first
 * version of this comment promised an index-only scan and a test asserting it
 * failed against a hundred thousand rows.
 *
 * So what bounds this is SUBJECT_DAYS_KEPT and nothing else, and the number to
 * hold in mind is rows per day times that. It runs once per day recomputed on a
 * maintenance pass, never on a page load, which is what makes a bulk scan the
 * right shape here. The indexes earn their keep on the NARROW reads, a single
 * day or a single subject, where the cost has to follow the window rather than
 * the table; a test asserts that separately.
 */
export async function recomputeActives(admin: postgres.Sql, day: string): Promise<number> {
  const windows = [...ACTIVE_WINDOWS]
  return admin.begin(async (tx) => {
    await tx`DELETE FROM analytics_actives WHERE day = ${day}::date`
    const inserted = await tx`
      INSERT INTO analytics_actives (day, window_days, subject_kind, name, subjects, computed_at)
      SELECT ${day}::date, w.window_days, s.subject_kind, '', count(DISTINCT s.subject), now()
      FROM unnest(${windows}::smallint[]) AS w(window_days)
      JOIN analytics_subject_days s
        ON s.day > (${day}::date - w.window_days) AND s.day <= ${day}::date
      GROUP BY 2, 3
      UNION ALL
      SELECT ${day}::date, w.window_days, s.subject_kind, s.name, count(DISTINCT s.subject), now()
      FROM unnest(${windows}::smallint[]) AS w(window_days)
      JOIN analytics_subject_days s
        ON s.day > (${day}::date - w.window_days) AND s.day <= ${day}::date
      GROUP BY 2, 3, 4`
    return inserted.count ?? 0
  })
}

/**
 * The retention grid, recomputed whole.
 *
 * WHY THE COHORT COMES FROM THE FACTS TABLE AND THE RETURNS FROM THE WORKING
 * SET. They answer different questions and only one of them can be pruned.
 * analytics_org_facts.first_seen_on is the day an organization was FIRST seen
 * ever, and that row is never deleted, so a cohort is never wrong about its own
 * size. The working set holds a bounded window, so a return week outside it
 * would be missing. Restricting the cohorts to the window the working set
 * covers is what keeps the grid from showing a cohort whose later columns are
 * empty because the rows were pruned rather than because nobody came back.
 *
 * Week zero is the cohort's own size, taken from the facts rather than from
 * activity. An organization is in its cohort whether or not the working set
 * still holds the day it arrived, and a grid whose first column could be
 * smaller than its second is a grid nobody can read.
 *
 * WHY IT IS RECOMPUTED WHOLE RATHER THAN BY WEEK. Every cell of every cohort
 * can change: an organization returning today lands in a different column of
 * every cohort row it belongs to, which is one. The grid is RETENTION_WEEKS
 * squared, which is a hundred and forty four rows, so recomputing it costs less
 * than working out which cells to touch.
 *
 * COST. One index-only scan of the working set over RETENTION_WEEKS weeks
 * joined to the facts table by primary key. At a thousand organizations and
 * eighty four days that is under a hundred thousand index entries.
 */
export async function recomputeRetention(
  admin: postgres.Sql,
  today: Date,
): Promise<{ rows: number; completeThrough: string | null }> {
  // The oldest cohort the working set can honestly describe: a Monday, at least
  // RETENTION_WEEKS weeks back, and never older than what has been kept.
  const oldest = dayString(new Date(today.getTime() - RETENTION_WEEKS * 7 * 86_400_000))

  const rows = await admin.begin(async (tx) => {
    await tx`DELETE FROM analytics_retention_cohorts WHERE subject_kind = 'organization'`
    const inserted = await tx`
      INSERT INTO analytics_retention_cohorts (subject_kind, cohort_week, weeks_later, subjects, computed_at)
      WITH cohorts AS (
        SELECT org_surrogate AS subject,
               date_trunc('week', first_seen_on)::date AS cohort_week
        FROM analytics_org_facts
        WHERE first_seen_on >= date_trunc('week', ${oldest}::date)::date
      ),
      returns AS (
        SELECT c.cohort_week,
               (((date_trunc('week', s.day)::date - c.cohort_week) / 7))::smallint AS weeks_later,
               c.subject
        FROM cohorts c
        JOIN analytics_subject_days s
          ON s.subject_kind = 'organization'
         AND s.subject = c.subject
         AND s.day >= c.cohort_week
      )
      SELECT 'organization', cohort_week, 0::smallint, count(*), now()
      FROM cohorts
      GROUP BY 2
      UNION ALL
      SELECT 'organization', cohort_week, weeks_later, count(DISTINCT subject), now()
      FROM returns
      WHERE weeks_later BETWEEN 1 AND ${RETENTION_WEEKS}
      GROUP BY 2, 3`
    return inserted.count ?? 0
  })

  // The last cohort whose own week has finished. A cohort that started this
  // week is still taking members, so its zero column is not yet its size and
  // publishing it as one would show every fresh cohort retaining badly.
  const complete = await admin<{ week: Date | string | null }[]>`
    SELECT max(cohort_week) AS week
    FROM analytics_retention_cohorts
    WHERE subject_kind = 'organization'
      AND cohort_week < date_trunc('week', ${dayString(today)}::date)::date`
  const week = complete[0]?.week ?? null
  return { rows, completeThrough: week === null ? null : asDay(week) }
}

/**
 * How far each subject got through each declared funnel, as counts per entry
 * week and depth.
 *
 * Runs over the RAW stream rather than the working set, because a funnel needs
 * ordering within a day and the working set deliberately holds no time. See
 * migrations/0033 for why the working set does not carry one.
 *
 * WHY A SUBJECT WHOSE FIRST STEP FALLS OUTSIDE THE SCANNED RANGE IS DROPPED
 * RATHER THAN COUNTED. It is not a loss, it is what keeps the row from being
 * counted twice. Their entry week is older than the weeks this pass deletes, so
 * their row is already there and is not being recomputed; seeing their later
 * steps and no first step gives them depth zero and they are filtered out. The
 * window is what makes that correct: a subject whose window has closed cannot
 * gain a step, so a frozen row is a final row rather than a stale one.
 *
 * COST. One index scan of analytics_events_day_idx over windowDays plus the
 * lookback, filtered to the funnel's own event names, then one group per
 * subject and one array walk each. For the activation funnel that is thirty
 * three days of four event names; for acquisition, four days of three. At a
 * million events a month the activation scan reads roughly a million rows and
 * aggregates to one array per organization.
 */
export async function recomputeFunnels(
  admin: postgres.Sql,
  today: Date,
  lookbackDays: number,
): Promise<{ rows: number; finalBefore: string }> {
  let rows = 0
  let earliest = today
  for (const funnel of FUNNEL_DEFINITIONS) {
    const from = weekStart(
      new Date(today.getTime() - (funnel.windowDays + lookbackDays) * 86_400_000),
    )
    if (from < earliest) earliest = from
    rows += await recomputeOneFunnel(admin, funnel, from, today)
  }
  return { rows, finalBefore: dayString(earliest) }
}

async function recomputeOneFunnel(
  admin: postgres.Sql,
  funnel: FunnelDefinition,
  from: Date,
  today: Date,
): Promise<number> {
  const subjectColumn = funnel.subject === 'organization' ? 'org_surrogate' : 'session_surrogate'
  // Exclusive, and a day past today so an event stamped a few minutes from now
  // by a clock that is slightly fast is still inside the range rather than
  // invisible until tomorrow.
  const to = dayString(new Date(today.getTime() + 86_400_000))
  const fromDay = dayString(from)

  const statement = `
    INSERT INTO analytics_funnel_weeks (funnel, entered_week, steps_completed, subjects, computed_at)
    WITH stepped AS (
      SELECT ${subjectColumn} AS subject,
             occurred_at,
             ${stepExpression(funnel)} AS step
      FROM analytics_events
      WHERE occurred_at >= $2::date
        AND occurred_at < $3::date
        AND ${subjectColumn} IS NOT NULL
        AND name IN (${funnel.steps.map((s) => `'${s.event}'`).join(', ')})
    ),
    walked AS (
      SELECT subject,
             -- The FIRST time this subject took step one, which is the week the
             -- cohort belongs to even when a later attempt is the one that got
             -- further. A subject belongs to the week they first arrived.
             min(occurred_at) FILTER (WHERE step = 0) AS entered_at,
             analytics_funnel_depth(
               array_agg(step ORDER BY occurred_at, step),
               array_agg(occurred_at ORDER BY occurred_at, step),
               $4::interval,
               $5::int) AS depth
      FROM stepped
      WHERE step IS NOT NULL
      GROUP BY subject
    )
    SELECT $1, date_trunc('week', entered_at)::date, depth, count(*), now()
    FROM walked
    WHERE depth >= 1
      AND entered_at >= $2::date
    GROUP BY 2, 3`

  return admin.begin(async (tx) => {
    await tx`
      DELETE FROM analytics_funnel_weeks
      WHERE funnel = ${funnel.id} AND entered_week >= ${fromDay}::date`
    const inserted = await tx.unsafe(statement, [
      funnel.id,
      fromDay,
      to,
      `${funnel.windowDays} days`,
      funnel.steps.length,
    ])
    return inserted.count ?? 0
  })
}

/**
 * Which step of this funnel each event is, as SQL over the stream.
 *
 * Built from the catalog rather than written out, so a funnel that gains a step
 * gains it here without anybody remembering to come and do it. Every value
 * spliced in comes from source in this repository and never from a request, and
 * every one is checked anyway, because "it comes from a constant" is exactly
 * what stops being true later. The dimension expression in rollup.ts makes the
 * same argument for the same reason.
 */
export function stepExpression(funnel: FunnelDefinition): string {
  const branches = funnel.steps.map((step, index) => {
    if (!(step.event in CATALOG)) {
      throw new Error(`funnel ${funnel.id} names an event that is not in the catalog`)
    }
    let predicate = `name = '${step.event}'`
    if (step.where) {
      if (!/^[a-z][a-z0-9_]*$/.test(step.where.field)) {
        throw new Error(`funnel ${funnel.id} filters on a field name that cannot be a key`)
      }
      for (const value of step.where.values) {
        if (!/^[a-z][a-z0-9_]*$/.test(value)) {
          throw new Error(`funnel ${funnel.id} filters on a value that cannot be a literal`)
        }
      }
      const list = step.where.values.map((v) => `'${v}'`).join(', ')
      predicate += ` AND payload->>'${step.where.field}' IN (${list})`
    }
    return `WHEN ${predicate} THEN ${index}::smallint`
  })
  return `CASE ${branches.join(' ')} END`
}

/**
 * Deletes working set rows past the retention.
 *
 * A loop rather than one statement, because a backlog worked off in a single
 * DELETE holds locks for as long as it takes and a bounded statement that is
 * only ever run once leaves a table that grows faster than it is pruned. The
 * bound per statement is what keeps a lock short; the loop is what makes the
 * retention actually true. Capped, so a first run against a large table does
 * bounded work and the next run finishes it.
 */
export async function pruneSubjectDays(
  admin: postgres.Sql,
  today: Date,
  keepDays: number,
): Promise<number> {
  const cutoff = dayString(new Date(today.getTime() - keepDays * 86_400_000))
  let deleted = 0
  for (let pass = 0; pass < 20; pass += 1) {
    const gone = await admin`
      DELETE FROM analytics_subject_days
      WHERE ctid IN (
        SELECT ctid FROM analytics_subject_days WHERE day < ${cutoff}::date LIMIT 20000)`
    const count = gone.count ?? 0
    deleted += count
    if (count === 0) break
  }
  return deleted
}

/** Every insight pass, for the days the daily rollup is recomputing. */
export async function recomputeInsights(
  admin: postgres.Sql,
  days: readonly string[],
  today: Date,
  lookbackDays: number,
): Promise<InsightResult> {
  let subjectDayRows = 0
  for (const day of days) subjectDayRows += await recomputeSubjectDays(admin, day)

  // Actives after the working set and never before, or a window ending today
  // would be computed from yesterday's rows.
  let activeRows = 0
  for (const day of days) activeRows += await recomputeActives(admin, day)

  const funnels = await recomputeFunnels(admin, today, lookbackDays)
  const retention = await recomputeRetention(admin, today)
  const subjectDaysPruned = await pruneSubjectDays(admin, today, SUBJECT_DAYS_KEPT)

  return {
    subjectDayRows,
    activeRows,
    funnelRows: funnels.rows,
    retentionRows: retention.rows,
    subjectDaysPruned,
    funnelsFinalBefore: funnels.finalBefore,
    cohortsCompleteThrough: retention.completeThrough,
  }
}

/** Every population the insight tables hold, for a test that checks the two
 *  lists have not drifted from the check constraint. */
export function subjectKinds(): readonly string[] {
  return SUBJECT_KINDS
}

function dayString(at: Date): string {
  return at.toISOString().slice(0, 10)
}

/** The Monday on or before a day, in UTC, matching date_trunc('week', ...). */
function weekStart(at: Date): Date {
  const day = new Date(Date.UTC(at.getUTCFullYear(), at.getUTCMonth(), at.getUTCDate()))
  // getUTCDay is 0 for Sunday, so Sunday is six days after its Monday.
  const back = (day.getUTCDay() + 6) % 7
  return new Date(day.getTime() - back * 86_400_000)
}

function asDay(v: Date | string): string {
  return v instanceof Date ? v.toISOString().slice(0, 10) : String(v).slice(0, 10)
}
