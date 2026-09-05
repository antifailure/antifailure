// What an organization may spend, in the one unit this system can measure.
//
// The unit is the environment-hour: one environment, held for one hour. It is
// chosen because it is the only thing here that is both what actually costs
// money and what the system already records. Every environment holds a
// database branch, a network and a container per service for as long as it
// exists, so the bill is very close to linear in this number, and the
// environments table already carries created_at and torn_down_at on every row.
// A cap in dollars would need a price list per runtime, per region and per
// service size, none of which this control plane has; a cap that cannot be
// measured is decoration, and this one is arithmetic over two columns.
//
// Two caps, because they refuse different mistakes.
//
// The per-run cap bounds what a single run may commit to: an environment
// created with a 30 day lifetime is 720 environment-hours promised in one
// call, and on the free plan that is the whole month in one dispatch. It is
// checked against the lifetime the environment will be created with, before
// anything is created, because the commitment is made at creation and refusing
// afterwards means the customer already paid for it.
//
// The per-day cap bounds accrual: a workflow stuck in a loop creating an
// environment per push stays inside every per-run cap and still produces a
// bill nobody expected. It is a rolling twenty four hours rather than a
// calendar day, because a calendar day resets at an hour that is midnight for
// somebody and the middle of the afternoon for somebody else, and because a
// runaway that starts at 23:00 should not get a fresh allowance at midnight.
//
// Neither cap ever destroys anything. Reaching one refuses the NEXT creation,
// exactly as checkQuota does, for the same reason: tearing down environments
// somebody is using because a number moved is not a behaviour a product should
// have.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

import { DEFAULT_PLAN } from './limits.ts'

/** What one plan may spend, in environment-hours. */
export interface CostCap {
  /** The most environment-hours one run may commit to in a single creation. */
  perRunHours: number
  /** The most an organization may accrue in any rolling twenty four hours. */
  perDayHours: number
}

/**
 * The caps per plan.
 *
 * The free numbers are the ones that matter, because free is where a runaway
 * costs the vendor rather than the customer and where nobody is watching a
 * bill. A day per run is exactly the default runtime.ttl, so the ordinary case
 * of one environment for one branch is never refused, and three of them a day
 * is more than a person opens by hand.
 *
 * The team and enterprise numbers are deliberately far above ordinary use.
 * They are not there to shape behaviour; they are there so that a loop is
 * refused before it becomes an invoice, which is the only thing a cap on a
 * paid plan is for.
 */
export const PLAN_COST_CAPS: Record<string, CostCap> = {
  free: { perRunHours: 24, perDayHours: 72 },
  team: { perRunHours: 168, perDayHours: 2_000 },
  enterprise: { perRunHours: 720, perDayHours: 20_000 },
}

/** Which cap a refusal is about. */
export type CapKind = 'per-run' | 'per-day'

export interface CapVerdict {
  allowed: boolean
  /** Which cap was reached, when one was. */
  kind: CapKind | null
  /** Environment-hours already accrued in the window, or committed by this run. */
  current: number
  /** The cap that applies. */
  limit: number
  /**
   * One paragraph naming the cap, the usage, and who can raise it.
   *
   * All three, because a refusal missing any of them ends in a support ticket
   * instead of somebody self-serving: without the number nobody knows how far
   * over they are, and without the role nobody knows who to ask.
   */
  reason: string
}

const ALLOWED: CapVerdict = { allowed: true, kind: null, current: 0, limit: 0, reason: '' }

export function capsFor(plan: string): CostCap {
  return PLAN_COST_CAPS[plan] ?? PLAN_COST_CAPS[DEFAULT_PLAN]!
}

/**
 * Whether a run that would hold an environment for `runHours` may proceed.
 *
 * `usedDayHours` is what the organization has already accrued in the rolling
 * window, from environmentHoursSince.
 *
 * The per-run cap is checked first and reported first when both are broken,
 * because it is the one the caller can do something about in the same breath:
 * shortening runtime.ttl fixes it now, whereas the daily cap only clears with
 * time or a plan change.
 */
export function checkCostCap(
  plan: string,
  runHours: number,
  usedDayHours: number,
): CapVerdict {
  const caps = capsFor(plan)

  if (runHours > caps.perRunHours) {
    return {
      allowed: false,
      kind: 'per-run',
      current: round(runHours),
      limit: caps.perRunHours,
      reason:
        `This run would hold an environment for ${hours(runHours)}, and the ${plan} plan allows ` +
        `${hours(caps.perRunHours)} in one run. Lower runtime.ttl in the manifest, or ask an owner ` +
        `of this organization to change the plan. Nothing was created and nothing was removed.`,
    }
  }

  // The projected total rather than the current one. Admitting a run that is
  // itself larger than the remaining allowance is how a cap is passed by
  // exactly one run, every time, and the customer sees a limit that does not
  // hold.
  const projected = usedDayHours + runHours
  if (projected > caps.perDayHours) {
    return {
      allowed: false,
      kind: 'per-day',
      current: round(usedDayHours),
      limit: caps.perDayHours,
      reason:
        `This organization has used ${hours(usedDayHours)} of environment time in the last ` +
        `24 hours, and the ${plan} plan allows ${hours(caps.perDayHours)}. This run would need ` +
        `another ${hours(runHours)}. Tear down an environment you are finished with, wait for ` +
        `the window to move, or ask an owner of this organization to change the plan. ` +
        `Nothing was created and nothing was removed.`,
    }
  }

  return ALLOWED
}

/** Two decimal places, so a refusal does not print sixteen. */
/** Two decimal places, so a verdict's numbers and its sentence agree.
 *
 *  Exported because the entitlement-aware verdicts report the same fields and a
 *  second rounding helper beside this one is how two code paths start
 *  disagreeing about the same number by a hundredth. */
export function round(n: number): number {
  return Math.round(n * 100) / 100
}

/** Hours the way somebody says them, so a message reads as a sentence. */
export function hours(n: number): string {
  if (n < 1) return `${Math.round(n * 60)} minutes`
  const r = round(n)
  return `${r} ${r === 1 ? 'hour' : 'hours'}`
}

// ---------------------------------------------------------------------------
// Measurement, and attribution
// ---------------------------------------------------------------------------

/**
 * Environment-hours an organization has accrued since a point in time.
 *
 * The overlap with the window, not the whole lifetime: an environment created
 * three days ago and still running has contributed 24 hours to a 24 hour
 * window, not 72. Computing it the other way makes one long-lived environment
 * permanently exceed every daily cap, so the customer is refused forever for
 * something they did once, and the number stops meaning "recent spend".
 *
 * An environment still running is counted up to `now` rather than left out.
 * Counting only what has finished would let an organization hold a hundred
 * environments and report zero usage, which is the exact shape of runaway this
 * cap exists to catch.
 *
 * `now` is passed rather than read, because every time in this application
 * comes from the injected clock and a cap on a rolling window is precisely the
 * kind of boundary a test has to be able to stand on.
 */
export async function environmentHoursSince(
  db: Db,
  orgId: string,
  since: Date,
  now: Date,
): Promise<number> {
  const rows = await db.execute<{ hours: string | number | null }>(sql`
    SELECT COALESCE(SUM(
      EXTRACT(EPOCH FROM (
        LEAST(COALESCE(torn_down_at, ${now.toISOString()}::timestamptz),
              ${now.toISOString()}::timestamptz)
        - GREATEST(created_at, ${since.toISOString()}::timestamptz)
      )) / 3600.0
    ), 0) AS hours
    FROM environment_usage
    WHERE org_id = ${orgId}
      AND created_at < ${now.toISOString()}::timestamptz
      AND COALESCE(torn_down_at, ${now.toISOString()}::timestamptz) > ${since.toISOString()}::timestamptz`)

  // COALESCE guarantees a row and a number, but the driver hands back numeric
  // as a string, and Number(null) is 0 while Number(undefined) is NaN. A NaN
  // here would compare false against every cap and silently admit everything,
  // which is the failure mode where a cap exists and enforces nothing.
  const raw = rows[0]?.hours
  const value = Number(raw ?? 0)
  return Number.isFinite(value) ? value : 0
}

/** One line of a bill: what ran, for which repository, and for how long. */
export interface CostLine {
  envId: string
  repository: string
  branch: string
  /** Null while the environment is still up. */
  tornDownAt: string | null
  createdAt: string
  hours: number
  /** How many runs were made against this environment. */
  runs: number
}

/**
 * Where an organization's environment time went.
 *
 * Without this a bill is one number nobody can act on: "you used 900
 * environment-hours" tells somebody neither which repository to look at nor
 * which branch left something up over a weekend. Every line names the
 * environment, the repository and the branch, and carries the run count, so
 * the three questions a person actually asks -- which repo, which branch,
 * which run -- are all answerable from one query.
 *
 * Ordered by hours descending, because the only line anybody reads first is
 * the expensive one.
 */
export async function costAttribution(
  db: Db,
  orgId: string,
  since: Date,
  now: Date,
  limit = 100,
): Promise<CostLine[]> {
  const rows = await db.execute<{
    env_id: string
    repository: string
    branch: string
    created_at: Date | string
    torn_down_at: Date | string | null
    hours: string | number | null
    runs: string | number | null
  }>(sql`
    SELECT u.env_id, COALESCE(r.full_name, 'Removed repository') AS repository,
           COALESCE(e.branch, 'Removed environment') AS branch,
           u.created_at, u.torn_down_at,
           EXTRACT(EPOCH FROM (
             LEAST(COALESCE(u.torn_down_at, ${now.toISOString()}::timestamptz),
                   ${now.toISOString()}::timestamptz)
             - GREATEST(u.created_at, ${since.toISOString()}::timestamptz)
           )) / 3600.0 AS hours,
           (SELECT count(*) FROM runs ru WHERE ru.environment_id = e.id) AS runs
    FROM environment_usage u
    LEFT JOIN environments e ON e.id = u.environment_id
    LEFT JOIN repositories r ON r.id = e.repository_id
    WHERE u.org_id = ${orgId}
      AND u.created_at < ${now.toISOString()}::timestamptz
      AND COALESCE(u.torn_down_at, ${now.toISOString()}::timestamptz) > ${since.toISOString()}::timestamptz
    ORDER BY hours DESC, u.env_id
    LIMIT ${limit}`)

  return rows.map((row) => ({
    envId: row.env_id,
    repository: row.repository,
    branch: row.branch,
    createdAt: asISO(row.created_at),
    tornDownAt: row.torn_down_at === null ? null : asISO(row.torn_down_at),
    hours: Math.round(Number(row.hours ?? 0) * 100) / 100,
    runs: Number(row.runs ?? 0),
  }))
}

/** The driver returns timestamptz as a Date or a string depending on the
 *  column and the query. Both are accepted on this read boundary; neither is
 *  guessed at. */
function asISO(v: Date | string): string {
  return v instanceof Date ? v.toISOString() : new Date(v).toISOString()
}
