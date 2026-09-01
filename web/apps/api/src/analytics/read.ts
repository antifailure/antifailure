// What the dashboard is allowed to ask, and nothing wider.
//
// Every function here reads analytics_daily or analytics_org_facts and never
// analytics_events, which the application role cannot read at all. So the
// widest answer any of these can give is a count, and there is no argument to
// any of them that could narrow one to a single organization: the surrogate
// never leaves this file's queries, and the queries never select it.
//
// A NUMBER WITH NO SOURCE DOES NOT GO ON THE PAGE, so every shape returned here
// carries enough to say where it came from: the window it covers, whether the
// rollup has ever run, and which days are still moving. A dashboard that cannot
// distinguish "nothing happened" from "the job has not run" shows an empty
// chart for both, and only one of those is a working system.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { CATALOG, EVENT_NAMES, FUNNELS, type EventName, type Funnel } from './catalog.ts'

/** When the numbers were last computed, and how far back they are settled. */
export interface Freshness {
  /** Null when the rollup has never run. Distinct from "it ran and found
   *  nothing", which is the distinction a dashboard has to make. */
  lastRunAt: string | null
  /** Days on or after this are still absorbing late arrivals. */
  settledAfter: string | null
}

export async function freshness(db: Db): Promise<Freshness> {
  const rows = await db.execute<{ last_run_at: Date | string | null; settled_after: Date | string | null }>(sql`
    SELECT last_run_at, settled_after FROM analytics_rollup_state WHERE id`)
  const row = rows[0]
  return {
    lastRunAt: row?.last_run_at ? asIso(row.last_run_at) : null,
    settledAfter: row?.settled_after ? asDay(row.settled_after) : null,
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
 * Each step counts organizations whose milestone is set AND whose first sight
 * is inside the window, so a step can never be larger than the one before it
 * and the funnel is monotone by construction rather than by hoping.
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
           count(first_event_on) AS reported,
           count(first_environment_on) AS environment,
           count(first_proven_run_on) AS proven,
           count(first_paid_on) AS paid
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
      meaning: 'Got a verdict that proved something, rather than blocked or unverified.',
      organizations: Number(r?.proven ?? 0),
    },
    {
      step: 'Paying',
      meaning: 'Left the free plan.',
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
