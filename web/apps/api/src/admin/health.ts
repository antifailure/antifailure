// Whether this control plane is working, in facts rather than in a colour.
//
// A health page that renders a green light next to the word "healthy" is a
// page that tells an operator nothing they can act on. Every check here
// answers three questions instead: what was counted, what number would be
// wrong, and what to do when it is. The verdict is derived from the count and
// the threshold rather than stored, so there is no way for the light to
// disagree with the number printed beside it.
//
// All of it is arithmetic over tables this control plane already writes. That
// is deliberate. A health surface with its own collection path is a second
// system to keep running, and the first thing that breaks in an incident is
// the thing that was only exercised during incidents.
//
// Every function here takes a Db and filters by nothing. The scope is the
// caller's: handed a tenant-scoped connection these answer for one
// organization, and handed the admin portal's bypassing connection they answer
// for the installation. That seam is why there is no second copy of these
// queries for the per-organization console.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

/** Whether a check is fine, worth looking at, or actively wrong. */
export type Verdict = 'ok' | 'degraded' | 'failing'

export interface Check {
  /** Stable identifier, used as a key and never shown. */
  id: string
  /** What was measured, in the words an operator reads. */
  title: string
  verdict: Verdict
  /** The measurement itself. Rendered as a number, right aligned. */
  value: number
  /** What the number counts, for the row's second line. */
  unit: string
  /** Why this verdict and not another, naming the threshold. */
  detail: string
  /** The one thing to do about it, when there is something. Null when the
   *  check is ok, because "no action needed" is noise on a page whose whole
   *  job is to make the actionable rows stand out. */
  remedy: string | null
}

/**
 * How long a teardown request may sit before the queue is considered stuck.
 *
 * The sweeper leases for minutes and retries, so a request pending for an hour
 * has survived many passes and is not waiting, it is failing. Below that,
 * pending is the ordinary state of a request made a moment ago.
 */
const TEARDOWN_STUCK_MS = 60 * 60 * 1000

/**
 * How far behind ingestion may fall before it is worth saying so.
 *
 * Events carry both occurred_at, stamped by the engine, and received_at,
 * stamped here. The gap is the engine's buffering plus the network, and a
 * minute of it is ordinary. Ten minutes means events are arriving for work
 * that finished long ago, which makes every run view wrong without making any
 * of them look wrong.
 */
const INGEST_LAG_DEGRADED_MS = 10 * 60 * 1000
const INGEST_LAG_FAILING_MS = 60 * 60 * 1000

function verdictFor(value: number, degradedAt: number, failingAt: number): Verdict {
  if (value >= failingAt) return 'failing'
  if (value >= degradedAt) return 'degraded'
  return 'ok'
}

/**
 * Environments past their own expiry that nothing has torn down.
 *
 * This is the check that costs money. Every environment holds a database
 * branch, a network and a container per service for as long as it exists, so
 * one that outlived its TTL is a bill accruing for work nobody is waiting on.
 * The reaper is supposed to remove these and this counts the ones it did not,
 * which is the only way to notice that the reaper has stopped: a reaper that
 * has died reports nothing at all, and nothing is what a working one reports
 * too.
 */
export async function leakedEnvironments(db: Db, now: Date): Promise<Check> {
  const rows = await db.execute<{ n: string }>(sql`
    SELECT count(*) AS n FROM environments
    WHERE state <> 'torn_down'
      AND expires_at IS NOT NULL
      AND expires_at < ${now.toISOString()}::timestamptz`)
  const n = Number(rows[0]?.n ?? 0)
  return {
    id: 'leaked-environments',
    title: 'Environments past their expiry',
    verdict: verdictFor(n, 1, 10),
    value: n,
    unit: n === 1 ? 'environment' : 'environments',
    detail:
      n === 0
        ? 'Every environment that has an expiry is inside it.'
        : `${n} ${n === 1 ? 'environment is' : 'environments are'} past the lifetime they were created with and have not been torn down. Each one is still holding a database branch, a network and its containers.`,
    remedy:
      n === 0
        ? null
        : 'Check that the reaper is running on the runtime holding these. Tearing one down from here records a request; it does not reach the containers by itself.',
  }
}

/**
 * Teardown requests the runtime has not confirmed.
 *
 * The distinction this makes is the reason the check exists. A request that is
 * pending is waiting for a runtime to confirm; a request that is abandoned ran
 * out of attempts and the environment behind it is, as far as anything here
 * knows, still running. Collapsing those into one "teardown queue" number
 * would hide the second inside the first, and the second is the one that
 * leaks.
 */
export async function teardownBacklog(db: Db, now: Date): Promise<Check[]> {
  const rows = await db.execute<{ state: string; n: string; oldest: Date | string | null }>(sql`
    SELECT state, count(*) AS n, min(requested_at) AS oldest
    FROM teardown_requests
    WHERE state IN ('pending', 'leased', 'abandoned')
    GROUP BY state`)

  const by = new Map(rows.map((r) => [r.state, r]))
  const waiting = Number(by.get('pending')?.n ?? 0) + Number(by.get('leased')?.n ?? 0)
  const oldestWaiting = [by.get('pending')?.oldest, by.get('leased')?.oldest]
    .filter((d): d is Date | string => d !== null && d !== undefined)
    .map((d) => new Date(d).getTime())
    .sort((a, b) => a - b)[0]
  const waitedMs = oldestWaiting === undefined ? 0 : now.getTime() - oldestWaiting
  const abandoned = Number(by.get('abandoned')?.n ?? 0)

  return [
    {
      id: 'teardown-waiting',
      title: 'Teardowns waiting for a runtime to confirm',
      // The count alone is not the signal. One request made a second ago and
      // one made yesterday are the same number and completely different
      // situations, so the age decides the verdict and the count is the value.
      verdict:
        waiting === 0 ? 'ok' : waitedMs >= TEARDOWN_STUCK_MS ? 'failing' : 'ok',
      value: waiting,
      unit: waiting === 1 ? 'request' : 'requests',
      detail:
        waiting === 0
          ? 'Nothing is waiting. Every teardown asked for has been confirmed or abandoned.'
          : waitedMs >= TEARDOWN_STUCK_MS
            ? `The oldest has been waiting ${Math.floor(waitedMs / 60000)} minutes. The sweeper leases for minutes and retries, so this has survived many passes and is not waiting, it is failing.`
            : 'Asked for recently. A request is confirmed when the runtime says the environment is gone, not when the cancel is accepted.',
      remedy:
        waiting > 0 && waitedMs >= TEARDOWN_STUCK_MS
          ? 'Open the teardown ledger and read last_error on the oldest rows. A request with no workflow run and no env id has nothing to reach.'
          : null,
    },
    {
      id: 'teardown-abandoned',
      title: 'Teardowns abandoned after every attempt failed',
      // Any abandoned row is a failure by definition: the queue gave up, and
      // nothing else will try. There is no tolerable quantity of these, so the
      // degraded threshold is one.
      verdict: abandoned === 0 ? 'ok' : 'failing',
      value: abandoned,
      unit: abandoned === 1 ? 'request' : 'requests',
      detail:
        abandoned === 0
          ? 'No teardown has run out of attempts.'
          : `${abandoned} ${abandoned === 1 ? 'request' : 'requests'} ran out of attempts. Nothing will try again, and the environment each one names is still running as far as this control plane knows.`,
      remedy:
        abandoned === 0
          ? null
          : 'Each of these is a possible leak. Confirm on the runtime whether the environment is actually gone, then tear it down by hand if it is not.',
    },
  ]
}

/**
 * How far behind the event stream is.
 *
 * Measured as the gap between when the newest event happened and when it
 * arrived, rather than as the age of the newest event. Age would report an
 * outage on a quiet installation where nothing has run for an hour, which is
 * not a fault, and the whole point of a health page is that a row going red
 * means something.
 */
export async function ingestionLag(db: Db): Promise<Check> {
  const rows = await db.execute<{ lag_ms: string | null; newest: Date | string | null }>(sql`
    SELECT extract(epoch FROM (received_at - occurred_at)) * 1000 AS lag_ms, received_at AS newest
    FROM events ORDER BY received_at DESC LIMIT 1`)
  const row = rows[0]
  // No events at all is not a fault. A fresh installation and a broken one look
  // identical from here, and calling the fresh one broken is the failure mode
  // that teaches operators to ignore the page.
  if (!row || row.lag_ms === null) {
    return {
      id: 'ingestion-lag',
      title: 'Delay between an event happening and arriving',
      verdict: 'ok',
      value: 0,
      unit: 'seconds',
      detail: 'No events have been ingested yet, so there is no delay to measure.',
      remedy: null,
    }
  }
  // A clock skewed the other way produces a negative gap, which is a real
  // thing to see and not a number to take the absolute value of: it means an
  // engine's clock is ahead, and hiding it would hide the cause of every
  // out-of-order run view that follows.
  const lagMs = Number(row.lag_ms)
  const seconds = Math.round(lagMs / 1000)
  return {
    id: 'ingestion-lag',
    title: 'Delay between an event happening and arriving',
    verdict:
      lagMs < 0
        ? 'degraded'
        : verdictFor(lagMs, INGEST_LAG_DEGRADED_MS, INGEST_LAG_FAILING_MS),
    value: seconds,
    unit: 'seconds',
    detail:
      lagMs < 0
        ? `The newest event says it happened ${Math.abs(seconds)} seconds after it arrived. An engine's clock is ahead of this one, and every run view orders by a time that is wrong.`
        : `The newest event took ${seconds} seconds to arrive. Above ${INGEST_LAG_DEGRADED_MS / 60000} minutes means events are landing for work that finished long ago.`,
    remedy:
      lagMs < 0
        ? 'Check the clock on the engine that sent it. Ordering by occurred_at is wrong until it is fixed.'
        : lagMs >= INGEST_LAG_DEGRADED_MS
          ? 'Check the engines for a backed up event buffer, and this control plane for ingestion rate limiting.'
          : null,
  }
}

/**
 * Webhook deliveries that arrived and were never handled.
 *
 * A delivery with no handled_at is one this control plane accepted and did not
 * finish, which is how a pull request ends up with no check and no explanation.
 * GitHub will not send it again: the delivery was answered.
 */
export async function unhandledDeliveries(db: Db, now: Date): Promise<Check> {
  // A window rather than all time. A delivery received two seconds ago and not
  // yet handled is a request in flight, and counting it would make the row
  // flicker red under ordinary load.
  const since = new Date(now.getTime() - 24 * 60 * 60 * 1000)
  const rows = await db.execute<{ n: string }>(sql`
    SELECT count(*) AS n FROM github_deliveries
    WHERE handled_at IS NULL
      AND received_at < ${new Date(now.getTime() - 5 * 60 * 1000).toISOString()}::timestamptz
      AND received_at >= ${since.toISOString()}::timestamptz`)
  const n = Number(rows[0]?.n ?? 0)
  return {
    id: 'unhandled-deliveries',
    title: 'GitHub deliveries accepted and never handled',
    verdict: n === 0 ? 'ok' : n < 5 ? 'degraded' : 'failing',
    value: n,
    unit: n === 1 ? 'delivery' : 'deliveries',
    detail:
      n === 0
        ? 'Every delivery in the last day that is older than five minutes was handled.'
        : `${n} ${n === 1 ? 'delivery' : 'deliveries'} in the last day ${n === 1 ? 'was' : 'were'} accepted and never finished. GitHub will not send ${n === 1 ? 'it' : 'them'} again, because the delivery was answered.`,
    remedy:
      n === 0
        ? null
        : 'Open the delivery ledger, filter to unhandled, and read the event and action. Each one is a pull request that silently got no check.',
  }
}

/**
 * Pull request checks that passed their own deadline without finishing.
 *
 * The deadline is stamped when the generation is queued, so this is the
 * control plane's own promise about when it would give up, measured against
 * what actually happened. A generation still queued or running past it is a
 * check that will sit on somebody's pull request saying "in progress" forever.
 */
export async function stalledGenerations(db: Db, now: Date): Promise<Check> {
  const rows = await db.execute<{ n: string }>(sql`
    SELECT count(*) AS n FROM pr_generations
    WHERE state IN ('queued', 'running')
      AND deadline_at < ${now.toISOString()}::timestamptz`)
  const n = Number(rows[0]?.n ?? 0)
  return {
    id: 'stalled-generations',
    title: 'Pull request checks past their deadline',
    verdict: n === 0 ? 'ok' : n < 3 ? 'degraded' : 'failing',
    value: n,
    unit: n === 1 ? 'check' : 'checks',
    detail:
      n === 0
        ? 'Every queued or running check is inside the deadline it was given.'
        : `${n} ${n === 1 ? 'check is' : 'checks are'} past the deadline stamped when ${n === 1 ? 'it was' : 'they were'} queued. Each shows as in progress on a pull request and will not resolve on its own.`,
    remedy:
      n === 0
        ? null
        : 'Open pull request generations and read the detail on each. A generation with no workflow run never dispatched; one with a run is waiting on the customer CI.',
  }
}

/**
 * Organizations stopped from creating anything new.
 *
 * Not a fault, which is why it never reports worse than degraded. It is here
 * because a suspension is invisible from every other screen and an operator
 * debugging "their runs do not start" should not have to think of it.
 */
export async function suspendedOrganizations(db: Db): Promise<Check> {
  const rows = await db.execute<{ n: string }>(sql`
    SELECT count(*) AS n FROM organizations WHERE suspended_at IS NOT NULL`)
  const n = Number(rows[0]?.n ?? 0)
  return {
    id: 'suspended-organizations',
    title: 'Organizations suspended',
    verdict: n === 0 ? 'ok' : 'degraded',
    value: n,
    unit: n === 1 ? 'organization' : 'organizations',
    detail:
      n === 0
        ? 'No organization is suspended.'
        : `${n} ${n === 1 ? 'organization cannot' : 'organizations cannot'} start new work. What was already running is untouched and can still be read.`,
    remedy: n === 0 ? null : 'Each suspension carries the reason it was set and who set it.',
  }
}

/**
 * Every check, in the order an operator should read them.
 *
 * Ordered by what a red row costs rather than by subject: a leaked environment
 * is money, an abandoned teardown is a leak, and a suspension is information.
 * Sorting by severity at render time instead would reorder the page under the
 * reader whenever a number moved.
 */
export async function healthChecks(db: Db, now: Date): Promise<Check[]> {
  const [leaked, teardown, lag, deliveries, generations, suspended] = await Promise.all([
    leakedEnvironments(db, now),
    teardownBacklog(db, now),
    ingestionLag(db),
    unhandledDeliveries(db, now),
    stalledGenerations(db, now),
    suspendedOrganizations(db),
  ])
  return [leaked, ...teardown, lag, deliveries, generations, suspended]
}

/** The worst verdict among a set of checks, for a single summary line. */
export function worst(checks: readonly Check[]): Verdict {
  if (checks.some((c) => c.verdict === 'failing')) return 'failing'
  if (checks.some((c) => c.verdict === 'degraded')) return 'degraded'
  return 'ok'
}
