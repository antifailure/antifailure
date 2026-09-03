// What the dashboard is allowed to ask, and nothing wider.
//
// Every function here reads a table of COUNTS and never a table that carries a
// surrogate. analytics_events, which the application role cannot read at all,
// and analytics_subject_days, which it has no grant on either, are both out of
// reach here by permission rather than by habit. So the widest answer any of
// these can give is a count, and there is no argument to any of them that could
// narrow one to a single organization.
//
// The one exception is analytics_org_facts, which the application does read and
// which is keyed by a surrogate. Nothing here selects that column: every query
// over it is an aggregate, and the analytics migration explains why the table
// is shaped that way.
//
// A NUMBER WITH NO SOURCE DOES NOT GO ON THE PAGE, so every shape returned here
// carries enough to say where it came from: the window it covers, whether the
// rollup has ever run, and which days are still moving. A dashboard that cannot
// distinguish "nothing happened" from "the job has not run" shows an empty
// chart for both, and only one of those is a working system.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import {
  CATALOG,
  EVENT_NAMES,
  FUNNELS,
  funnelDefinition,
  type EventName,
  type Funnel,
} from './catalog.ts'

/** When the numbers were last computed, and how far back they are settled. */
export interface Freshness {
  /** Null when the rollup has never run. Distinct from "it ran and found
   *  nothing", which is the distinction a dashboard has to make. */
  lastRunAt: string | null
  /** Days on or after this are still absorbing late arrivals. */
  settledAfter: string | null
  /**
   * Three freshness answers rather than one, because the three insight shapes
   * settle at different rates and showing the daily figure beside all of them
   * would be wrong about two.
   *
   * A funnel week on or after `funnelsFinalBefore` can still gain conversions,
   * because a subject that entered it is still inside its window. A cohort week
   * after `cohortsCompleteThrough` has not finished taking members, so its
   * first column is not yet its size. And `subjectDaysKept` says how far back a
   * cohort grid can reach at all, so an empty corner reads as a retention
   * policy rather than as a product with no customers.
   */
  funnelsFinalBefore: string | null
  cohortsCompleteThrough: string | null
  subjectDaysKept: number | null
}

export async function freshness(db: Db): Promise<Freshness> {
  const rows = await db.execute<{
    last_run_at: Date | string | null
    settled_after: Date | string | null
    funnels_final_before: Date | string | null
    cohorts_complete_through: Date | string | null
    subject_days_kept: number | string | null
  }>(sql`
    SELECT last_run_at, settled_after, funnels_final_before,
           cohorts_complete_through, subject_days_kept
    FROM analytics_rollup_state WHERE id`)
  const row = rows[0]
  return {
    lastRunAt: row?.last_run_at ? asIso(row.last_run_at) : null,
    settledAfter: row?.settled_after ? asDay(row.settled_after) : null,
    funnelsFinalBefore: row?.funnels_final_before ? asDay(row.funnels_final_before) : null,
    cohortsCompleteThrough: row?.cohorts_complete_through
      ? asDay(row.cohorts_complete_through)
      : null,
    subjectDaysKept:
      row?.subject_days_kept === null || row?.subject_days_kept === undefined
        ? null
        : Number(row.subject_days_kept),
  }
}

export interface DailyPoint {
  day: string
  events: number
  organizations: number
  sessions: number
}

export interface Breakdown {
  /** The dimension value, or the empty string when the event declares none. */
  value: string
  events: number
  organizations: number
  sessions: number
}

/**
 * One event's totals per day, over a window.
 *
 * Every day in the window appears, including the ones with no rows. A series
 * that skips its empty days draws a line straight across an outage, which is
 * the one shape a chart must never do.
 */
export async function series(
  db: Db,
  name: EventName,
  days: number,
  today: Date,
): Promise<DailyPoint[]> {
  const from = dayString(new Date(today.getTime() - (days - 1) * 86_400_000))
  const rows = await db.execute<{
    day: Date | string
    events: string | number
    organizations: string | number
    sessions: string | number
  }>(sql`
    SELECT d.day::date AS day,
           COALESCE(sum(a.events), 0) AS events,
           COALESCE(sum(a.organizations), 0) AS organizations,
           COALESCE(sum(a.sessions), 0) AS sessions
    FROM generate_series(${from}::date, ${dayString(today)}::date, interval '1 day') AS d(day)
    LEFT JOIN analytics_daily a ON a.day = d.day::date AND a.name = ${name}
    GROUP BY d.day
    ORDER BY d.day`)
  return rows.map((r) => ({
    day: asDay(r.day),
    events: Number(r.events),
    organizations: Number(r.organizations),
    sessions: Number(r.sessions),
  }))
}

/**
 * One event, grouped by one of its two dimensions, over a window.
 *
 * Note what is summed and what is not. `events` adds up: a day with three page
 * views and a day with four is seven. `organizations` and `sessions` do NOT,
 * because the same organization active on both days is counted twice by a sum.
 * The rollup only stores a distinct count per day, so summing them here would
 * be wrong and this returns the daily PEAK instead, which is a number that is
 * true and is labelled as what it is on the page rather than a distinct count
 * that is quietly not one.
 */
export async function breakdown(
  db: Db,
  name: EventName,
  which: 'a' | 'b',
  days: number,
  today: Date,
): Promise<Breakdown[]> {
  const from = dayString(new Date(today.getTime() - (days - 1) * 86_400_000))
  const column = which === 'a' ? sql`dim_a` : sql`dim_b`
  const rows = await db.execute<{
    value: string
    events: string | number
    organizations: string | number
    sessions: string | number
  }>(sql`
    SELECT ${column} AS value,
           sum(events) AS events,
           max(organizations) AS organizations,
           max(sessions) AS sessions
    FROM analytics_daily
    WHERE name = ${name} AND day >= ${from}::date AND day <= ${dayString(today)}::date
    GROUP BY ${column}
    ORDER BY sum(events) DESC, ${column}`)
  return rows.map((r) => ({
    value: r.value,
    events: Number(r.events),
    organizations: Number(r.organizations),
    sessions: Number(r.sessions),
  }))
}

/**
 * The organization funnel, from first sight to paying.
 *
 * Read from analytics_org_facts rather than from the stream, because every step
 * here is a milestone and a milestone is a column. See the catalog's
 * DERIVED_FROM_FACTS for why that is not an omission.
 *
 * EACH STEP REQUIRES EVERY STEP BEFORE IT, and that is not pedantry.
 *
 * The obvious query counts each milestone column on its own, and it was the
 * first one here. It produced a page showing 3 organizations that brought an
 * environment up and 6 that got a proven run, under a caption promising a step
 * could never be wider than the one above it. The caption was wrong and the
 * numbers were right: the milestones are independent columns, and an engine
 * that reports a verdict without ever reporting a lifecycle event sets one and
 * not the other, which is exactly the state the engine is in today.
 *
 * Found by looking at the rendered page, not by reading the code and not by the
 * test, which asserted monotonicity over whatever rows happened to exist and
 * passed. There is a case in the suite now that puts a proven run on an
 * organization with no environment.
 *
 * So each step ANDs in the ones before it, which is what a funnel means: the
 * number at step four is how many got all the way to step four.
 */
export interface FunnelStep {
  step: string
  /** What this number is, in the words somebody reading the page needs. */
  meaning: string
  organizations: number
}

export async function organizationFunnel(
  db: Db,
  days: number,
  today: Date,
): Promise<FunnelStep[]> {
  const from = dayString(new Date(today.getTime() - (days - 1) * 86_400_000))
  const rows = await db.execute<{
    seen: string | number
    reported: string | number
    environment: string | number
    proven: string | number
    paid: string | number
  }>(sql`
    SELECT count(*) AS seen,
           count(*) FILTER (WHERE first_event_on IS NOT NULL) AS reported,
           count(*) FILTER (
             WHERE first_event_on IS NOT NULL
               AND first_environment_on IS NOT NULL) AS environment,
           count(*) FILTER (
             WHERE first_event_on IS NOT NULL
               AND first_environment_on IS NOT NULL
               AND first_proven_run_on IS NOT NULL) AS proven,
           count(*) FILTER (
             WHERE first_event_on IS NOT NULL
               AND first_environment_on IS NOT NULL
               AND first_proven_run_on IS NOT NULL
               AND first_paid_on IS NOT NULL) AS paid
    FROM analytics_org_facts
    WHERE first_seen_on >= ${from}::date AND first_seen_on <= ${dayString(today)}::date`)
  const r = rows[0]
  return [
    {
      step: 'Organizations',
      meaning: 'Reached the control plane for the first time in this window.',
      organizations: Number(r?.seen ?? 0),
    },
    {
      step: 'Engine reported',
      meaning: 'An engine of theirs sent at least one event.',
      organizations: Number(r?.reported ?? 0),
    },
    {
      step: 'Environment up',
      meaning: 'Brought at least one environment up.',
      organizations: Number(r?.environment ?? 0),
    },
    {
      step: 'Proven run',
      meaning:
        'Brought an environment up AND got a verdict that proved something, rather than ' +
        'blocked or unverified.',
      organizations: Number(r?.proven ?? 0),
    },
    {
      step: 'Paying',
      meaning: 'Reached every step above and left the free plan.',
      organizations: Number(r?.paid ?? 0),
    },
  ]
}

/** How many organizations are on each plan, as of the last event seen. */
export interface PlanMix {
  plan: string
  organizations: number
}

export async function planMix(db: Db): Promise<PlanMix[]> {
  const rows = await db.execute<{ plan: string | null; n: string | number }>(sql`
    SELECT COALESCE(plan, 'free') AS plan, count(*) AS n
    FROM analytics_org_facts
    GROUP BY 1
    ORDER BY count(*) DESC, 1`)
  return rows.map((r) => ({ plan: r.plan ?? 'free', organizations: Number(r.n) }))
}

/**
 * Retention, as how many organizations are still doing anything.
 *
 * Deliberately not a cohort grid. A grid needs a cell per cohort per week and
 * is unreadable at the volume this product is at, and worse, most of its cells
 * would hold one or two organizations, which is close enough to naming them.
 * These three numbers answer the question a founder actually asks and cannot
 * single anybody out.
 */
export interface Retention {
  /** Every organization the analytics store has ever seen. */
  total: number
  activeLast7: number
  activeLast28: number
  /** Organizations whose last activity is older than 28 days. */
  dormant: number
}

export async function retention(db: Db, today: Date): Promise<Retention> {
  const rows = await db.execute<{
    total: string | number
    a7: string | number
    a28: string | number
  }>(sql`
    SELECT count(*) AS total,
           count(*) FILTER (WHERE last_active_on > ${dayString(new Date(today.getTime() - 7 * 86_400_000))}::date) AS a7,
           count(*) FILTER (WHERE last_active_on > ${dayString(new Date(today.getTime() - 28 * 86_400_000))}::date) AS a28
    FROM analytics_org_facts`)
  const r = rows[0]
  const total = Number(r?.total ?? 0)
  const activeLast28 = Number(r?.a28 ?? 0)
  return {
    total,
    activeLast7: Number(r?.a7 ?? 0),
    activeLast28,
    dormant: total - activeLast28,
  }
}

// ---------------------------------------------------------------------------
// The three insights a daily count cannot produce
//
// Each of these reads a table of COUNTS that the rollup materialized, never the
// working set it computed them from, which the application holds no SELECT on.
// See migrations/0033: that is a permission rather than a habit, so a query
// added here later still cannot follow one subject.
// ---------------------------------------------------------------------------

export interface ActivePoint {
  day: string
  subjects: number
}

/**
 * Distinct subjects over a rolling window, one point per day.
 *
 * The number analytics_daily could not give. Its `organizations` column is a
 * distinct count WITHIN a day, so summing a week double counts anybody active
 * on two days, which is why breakdown() returns the daily peak and says so.
 * This is the real distinct count, computed once by the rollup over the working
 * set and stored as a scalar.
 *
 * `name` empty means any event, which is the number usually meant by weekly
 * actives, and it is NOT the sum of the per event numbers: an organization that
 * created an environment and finished a run is one active organization and two
 * rows.
 *
 * Every day in the window appears, including the ones the rollup has no row
 * for, for the same reason series() fills its gaps: a line drawn straight
 * across a missing day is the one shape a chart must never make.
 */
export async function actives(
  db: Db,
  subjectKind: 'organization' | 'session',
  windowDays: number,
  name: EventName | '',
  days: number,
  today: Date,
): Promise<ActivePoint[]> {
  const from = dayString(new Date(today.getTime() - (days - 1) * 86_400_000))
  const rows = await db.execute<{ day: Date | string; subjects: string | number | null }>(sql`
    SELECT d.day::date AS day, COALESCE(a.subjects, 0) AS subjects
    FROM generate_series(${from}::date, ${dayString(today)}::date, interval '1 day') AS d(day)
    LEFT JOIN analytics_actives a
      ON a.day = d.day::date
     AND a.window_days = ${windowDays}
     AND a.subject_kind = ${subjectKind}
     AND a.name = ${name}
    ORDER BY d.day`)
  return rows.map((r) => ({ day: asDay(r.day), subjects: Number(r.subjects ?? 0) }))
}

export interface ConversionStep {
  step: string
  meaning: string
  /** How many subjects reached this step or any step after it. */
  subjects: number
  /** Of the subjects that reached the step before, what share reached this one.
   *  Null on the first step, which has nothing to be a share of. */
  ofPrevious: number | null
}

export interface Conversion {
  id: string
  title: string
  subject: string
  windowDays: number
  windowReason: string
  /** The entry weeks this covers, so a reader knows which cohort converted. */
  fromWeek: string
  toWeek: string
  steps: ConversionStep[]
  /** True when no subject entered the funnel in this window at all, which is a
   *  different page from a funnel that everybody dropped out of. */
  empty: boolean
}

/**
 * A declared funnel over a window of entry weeks.
 *
 * WHY THE STEPS ARE A RUNNING SUM AND NOT A COUNT PER STEP.
 *
 * The table stores how far each subject got, one depth per subject, and a
 * step's total is every depth at or above it. That makes the numbers monotone
 * BY CONSTRUCTION: a subject has exactly one depth, so step four counts a
 * subset of the rows step three counts and cannot be wider.
 *
 * read.ts learned this the hard way on the other funnel, which counted each
 * milestone column independently and rendered a step wider than the one above
 * it under a caption promising that could not happen. Storing a depth is what
 * makes the caption true rather than hopeful.
 */
export async function conversion(
  db: Db,
  id: string,
  weeks: number,
  today: Date,
): Promise<Conversion | null> {
  const definition = funnelDefinition(id)
  if (!definition) return null

  const fromWeek = weekStart(new Date(today.getTime() - (weeks - 1) * 7 * 86_400_000))
  const toWeek = weekStart(today)

  const rows = await db.execute<{ steps_completed: number | string; subjects: string | number }>(sql`
    SELECT steps_completed, sum(subjects) AS subjects
    FROM analytics_funnel_weeks
    WHERE funnel = ${id}
      AND entered_week >= ${dayString(fromWeek)}::date
      AND entered_week <= ${dayString(toWeek)}::date
    GROUP BY steps_completed`)

  const byDepth = new Map<number, number>()
  for (const row of rows) byDepth.set(Number(row.steps_completed), Number(row.subjects))

  let previous: number | null = null
  const steps: ConversionStep[] = definition.steps.map((step, index) => {
    const depth = index + 1
    let reached = 0
    for (const [d, n] of byDepth) if (d >= depth) reached += n
    const ofPrevious = previous === null || previous === 0 ? null : reached / previous
    previous = reached
    return {
      step: stepLabel(step.event, step.where),
      meaning: step.meaning,
      subjects: reached,
      ofPrevious: index === 0 ? null : ofPrevious,
    }
  })

  return {
    id: definition.id,
    title: definition.title,
    subject: definition.subject,
    windowDays: definition.windowDays,
    windowReason: definition.windowReason,
    fromWeek: dayString(fromWeek),
    toWeek: dayString(toWeek),
    steps,
    empty: (steps[0]?.subjects ?? 0) === 0,
  }
}

/** The step's name as a reader sees it: the event, and the values that count
 *  when the step only accepts some of them. Built here rather than stored,
 *  because it is a label and the catalog is the source for labels. */
function stepLabel(event: string, where?: { field: string; values: readonly string[] }): string {
  if (!where) return event
  return `${event} (${where.field}: ${where.values.join(', ')})`
}

/**
 * The smallest cohort this reports a rate for.
 *
 * A retention percentage over three organizations is not a measurement, it is
 * three organizations described as a fraction, and it moves by thirty three
 * points when one of them opens a laptop. Reporting it invites somebody to
 * quote it. So a cohort below this floor is returned with its counts and marked
 * as too small, and the page shows the count rather than a rate.
 *
 * The suppression is a DISPLAY rule over a true stored number rather than a
 * changed number in the table, so raising or lowering this floor later changes
 * what is shown and never what was recorded.
 */
export const MIN_COHORT_FOR_A_RATE = 5

export interface CohortRow {
  cohortWeek: string
  /** The cohort's own size, which is the row's week zero. */
  size: number
  /** Subjects still active, by weeks after the cohort week. Index 0 is the
   *  cohort itself, so it always equals `size`. */
  weeks: number[]
  /** False when the cohort is too small for a rate to mean anything. */
  enough: boolean
}

export interface RetentionGrid {
  rows: CohortRow[]
  /** How many return weeks the grid holds, which is how wide a row can be. */
  width: number
  /** True when no cohort has any members, which reads differently from a grid
   *  whose cohorts all churned. */
  empty: boolean
}

/**
 * Retention as a cohort grid: of the organizations first seen in one week, how
 * many were still doing anything each week after.
 *
 * WHY THIS EXISTS ALONGSIDE retention() RATHER THAN REPLACING IT.
 *
 * They answer different questions and both are worth having. retention() is
 * three scalars over last_active_on: how many are active now, which is the
 * number an operator checks daily. This is a grid: whether the organizations
 * who arrived in March are still here, which is the number that says whether
 * the product keeps people. A dashboard that only had the first would show a
 * healthy active count made entirely of new arrivals, which is what churn looks
 * like from the wrong angle.
 *
 * WHAT A ROW CANNOT SHOW. A cohort week older than the working set's retention
 * is not in the grid at all, because the days its members would have returned
 * on have been deleted. The page says how far back the grid reaches rather than
 * drawing an empty corner that reads as no customers.
 */
export async function retentionGrid(db: Db, today: Date): Promise<RetentionGrid> {
  const rows = await db.execute<{
    cohort_week: Date | string
    weeks_later: number | string
    subjects: string | number
  }>(sql`
    SELECT cohort_week, weeks_later, subjects
    FROM analytics_retention_cohorts
    WHERE subject_kind = 'organization'
    ORDER BY cohort_week DESC, weeks_later`)

  const byCohort = new Map<string, Map<number, number>>()
  for (const row of rows) {
    const week = asDay(row.cohort_week)
    let cells = byCohort.get(week)
    if (!cells) {
      cells = new Map()
      byCohort.set(week, cells)
    }
    cells.set(Number(row.weeks_later), Number(row.subjects))
  }

  let width = 0
  for (const cells of byCohort.values()) {
    for (const later of cells.keys()) if (later + 1 > width) width = later + 1
  }
  // A grid one column wide is every cohort's own size and no returns, which is
  // what a brand new deployment looks like. Two columns is the minimum that
  // shows anything, so the shape is stable rather than collapsing to a list.
  if (width < 2) width = 2

  const built: CohortRow[] = []
  for (const [week, cells] of byCohort) {
    const size = cells.get(0) ?? 0
    const weeks: number[] = []
    for (let i = 0; i < width; i += 1) weeks.push(cells.get(i) ?? 0)
    built.push({ cohortWeek: week, size, weeks, enough: size >= MIN_COHORT_FOR_A_RATE })
  }
  // Newest first, matching the order the query asked for and the order a reader
  // expects: the cohort they are still waiting on is the interesting one.
  built.sort((a, b) => (a.cohortWeek < b.cohortWeek ? 1 : -1))

  return {
    rows: built,
    width,
    empty: built.every((r) => r.size === 0),
  }
}

/** The Monday on or before a day, in UTC, matching date_trunc('week', ...). */
function weekStart(at: Date): Date {
  const day = new Date(Date.UTC(at.getUTCFullYear(), at.getUTCMonth(), at.getUTCDate()))
  const back = (day.getUTCDay() + 6) % 7
  return new Date(day.getTime() - back * 86_400_000)
}

/**
 * Every event in the catalog with whether anything has ever recorded one.
 *
 * This is the page that answers "is this producer wired", live, rather than
 * from a report somebody wrote once. An event declared, described, given a
 * dimension and emitted by nothing is the exact shape of a feature that looks
 * finished, and the only way to see it is to look at what has actually arrived.
 */
export interface CatalogRow {
  name: EventName
  funnel: Funnel
  answers: string
  privacyBasis: string
  producer: string
  /** Total events ever rolled up under this name. Zero means nothing has ever
   *  emitted one, which is a finding rather than a blank cell. */
  everRecorded: number
  /** The most recent day with any, or null. */
  lastSeenOn: string | null
}

export async function catalogStatus(db: Db): Promise<CatalogRow[]> {
  const rows = await db.execute<{ name: string; total: string | number; last_day: Date | string }>(sql`
    SELECT name, sum(events) AS total, max(day) AS last_day
    FROM analytics_daily
    GROUP BY name`)
  const seen = new Map(rows.map((r) => [r.name, r]))

  return EVENT_NAMES.map((name) => {
    const spec = CATALOG[name]
    const row = seen.get(name)
    return {
      name,
      funnel: spec.funnel,
      answers: spec.answers,
      privacyBasis: spec.privacyBasis,
      producer: spec.producer,
      everRecorded: row ? Number(row.total) : 0,
      lastSeenOn: row?.last_day ? asDay(row.last_day) : null,
    }
  })
}

/** The funnels, in the order the product happens in. */
export function funnelOrder(): readonly Funnel[] {
  return FUNNELS
}

function dayString(at: Date): string {
  return at.toISOString().slice(0, 10)
}

function asDay(v: Date | string): string {
  return v instanceof Date ? v.toISOString().slice(0, 10) : String(v).slice(0, 10)
}

function asIso(v: Date | string): string {
  return (v instanceof Date ? v : new Date(v)).toISOString()
}
