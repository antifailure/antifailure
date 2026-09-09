// What failed in the control plane itself, grouped, counted, and kept where the
// operator portal can read it.
//
// THE FAILURE THIS ANSWERS. `app.onError` and the tRPC `onError` both already
// catch every unexpected failure this process has, and both already write a
// line naming the class, the driver code, the method and the route. That line
// goes to stdout. On the hosted control plane something ships stdout somewhere
// searchable. A self hoster running this container against their own Postgres,
// with no Log Analytics and no Grafana, has `docker logs`: no count, no first
// seen, no grouping, and so no answer to "what is failing right now" or "did it
// start when we deployed". The metric beside it, af_http_requests_total with a
// 5xx status class, says how many and never says what.
//
// WHY THIS IS NOT A SECOND METRICS SYSTEM, which the plan explicitly refuses.
// The counters in metrics.ts stay exactly as they are and this adds four series
// to that same registry. What it does not do is answer through them. Prometheus
// counters are per replica, the control plane runs in multiple revision mode
// with more than one replica behind one ingress, and a console request reaches
// whichever replica the ingress picked. A count read that way walks up and down
// as the load balancer moves, which is indistinguishable from a failure rate
// that is walking up and down. Postgres is shared by every replica, so a row
// here is the fleet's answer rather than one replica's, and it survives the
// revision swap that ends every replica's process memory.
//
// WHY A GROUP AND NOT A LOG LINE. A row per occurrence makes the table's size a
// function of how badly the installation is behaving, so the day the control
// plane starts failing is the day the disk fills. That is a self hoster's
// incident, caused by the thing we added to help them with incidents. One row
// per fingerprint is bounded by the CODE: the route keys come from
// ENDPOINT_LIMITS, the tRPC paths from the router, the class name is a
// JavaScript identifier and the provider code is a driver's enum. A bad day
// adds occurrences and no rows.
//
// WHAT NEVER ENTERS THE STORE, and this is the boundary the plan asks to be
// named. Not the message, not the stack, not a body, a query string, a
// parameter or a payload; not an org, a user, an email or an environment. The
// reason is specific rather than hygienic and it is already written on
// `app.onError`: Drizzle renders a query failure as "Failed query: <the whole
// statement>" with the parameters after it, so an error message from this stack
// can carry a tenant's event payload. THE BOUNDARY IS THIS MODULE'S SIGNATURE.
// `record` takes five bounded strings and a request id and has no parameter
// that could carry one of those values, so a caller cannot pass one by mistake.
// It is not enforced by a row level policy and cannot be: policies are row
// level, and the operator pool bypasses them anyway. Same argument as
// SAFE_COLUMNS in admin/router.ts.
//
// WHAT THIS DELIBERATELY CANNOT ANSWER. How many tenants a failure touched.
// That needs an organization identifier written next to a failure, which turns
// a bounded operational table into tenant data carrying its own retention
// argument. The Logs page says so in words rather than showing a zero, and
// points at workload_runs.failure_code, which it already groups, for the
// per-tenant count.

import { createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Pool } from '@antifailure/db'
import type { Counter } from './metrics.ts'
import type { Clock } from './clock.ts'

/** One failure, as the two handlers see it. Every field is a bounded string. */
export interface Failure {
  source: 'http' | 'trpc'
  /** The DECLARED route key, never the path that matched it. */
  route: string
  method: string
  /** The error's class name. */
  kind: string
  /** The driver's own code, when the cause carried one. */
  providerCode: string | null
  /** The id already on the 500 response and already on the log line. It names a
   *  request, not a person. */
  requestId: string | null
}

/**
 * The identity of a group.
 *
 * SHA-256 over the five bounded fields, so every replica and every restart
 * agrees without coordinating. The columns themselves would serve as a natural
 * key; the hash is what lets the console carry one opaque identifier per row.
 *
 * LENGTH PREFIXED RATHER THAN JOINED ON A SEPARATOR, and that is the whole
 * reason this is a function rather than a template string. Any separator is a
 * character some field might one day contain, and the moment one does, two
 * different failures hash to the same group. The reading that produces is the
 * worst one this feature can produce: a failure that appears to have stopped,
 * because its occurrences are being added to somebody else's row. A length
 * prefix has no such character, so the encoding is injective by construction
 * and stays injective if `err.name` ever carries something strange.
 */
export function fingerprintOf(f: Failure): string {
  const hash = createHash('sha256')
  for (const field of [f.source, f.method, f.route, f.kind, f.providerCode ?? '']) {
    hash.update(`${field.length}:${field}`)
  }
  return hash.digest('hex')
}

/**
 * Column bounds, mirrored from the CHECK constraints in migration 0042.
 *
 * Truncated here rather than left to the database, because a value over the
 * bound would raise 23514 at flush time and take the whole batch with it. A
 * class name of 101 characters is not worth losing a flush over, and silently
 * dropping the failure it describes is exactly the outcome this store exists to
 * prevent.
 */
const BOUNDS = { route: 200, method: 16, kind: 100, providerCode: 64, requestId: 100 } as const

function clamp(value: string, max: number): string {
  return value.length <= max ? value : value.slice(0, max)
}

interface Pending {
  source: 'http' | 'trpc'
  route: string
  method: string
  kind: string
  providerCode: string | null
  count: number
  firstAt: Date
  lastAt: Date
  lastRequestId: string | null
}

export interface FlushResult {
  /** Groups whose accumulated occurrences reached the table. */
  applied: number
  /** Groups the table refused because it is already at its group cap. Their
   *  occurrences are discarded rather than retried: the cap is not going to
   *  move on the next tick. */
  capped: number
  /** Groups whose write raised. Their occurrences go back in the buffer. */
  failed: number
}

export interface FailureStore {
  /** False when no store is configured. Every method still works and records
   *  nothing, so no caller has to check first. Same arrangement as analytics. */
  readonly enabled: boolean
  /** Never throws, never awaits, never touches the network. */
  record(f: Failure): void
  flush(): Promise<FlushResult>
  /** How many groups are waiting. For the shutdown path and for tests. */
  buffered(): number
}

export interface FailureStoreOptions {
  pool: Pool | null
  clock: Clock
  /** The build string, the same one af_control_plane_info carries. */
  version: string
  /** Increments with an `outcome` label. Optional so a test can omit it. */
  counter?: Counter
  /**
   * The most groups the table may hold.
   *
   * A second bound on top of "bounded by the code", because bounded by the code
   * is an argument and a LIMIT is a fact. Five hundred is far above the real
   * cardinality: there are fewer than a hundred declared routes and a handful of
   * error classes that reach these handlers.
   */
  groupCap?: number
  /**
   * The most groups the in-process buffer may hold between flushes.
   *
   * Reached only when the table is being asked to hold more distinct groups than
   * the cap allows anyway, so it is the same bound one tick earlier. A new
   * fingerprint arriving at a full buffer is DROPPED and counted, because
   * growing the buffer during an incident is how a process that was reporting an
   * outage becomes part of it.
   */
  bufferCap?: number
}

export const DEFAULT_GROUP_CAP = 500
export const DEFAULT_BUFFER_CAP = 500

/**
 * Reads the switch.
 *
 * The default is ON, and the reason it is safe to default on is the bound
 * above: the table cannot exceed the group cap, the writes are one statement per
 * distinct group per flush rather than one per failure, and a healthy
 * installation produces no failures and therefore no writes at all. A self
 * hoster on a small machine pays nothing until something breaks, which is when
 * they want it.
 */
export function failureStoreEnabledFrom(env: NodeJS.ProcessEnv): boolean {
  const raw = (env.AF_FAILURE_STORE ?? '').trim().toLowerCase()
  return raw !== 'off' && raw !== '0' && raw !== 'false'
}

/**
 * Builds the store.
 *
 * A null pool, or the switch off, gives a store that accepts everything and
 * writes nothing. That is not a stub: `record` is called from an error handler,
 * and an error handler that can itself fail because a store is not configured is
 * a worse failure than the one it was reporting.
 */
export function createFailureStore(options: FailureStoreOptions): FailureStore {
  const groupCap = options.groupCap ?? DEFAULT_GROUP_CAP
  const bufferCap = options.bufferCap ?? DEFAULT_BUFFER_CAP
  const enabled = options.pool !== null
  let buffer = new Map<string, Pending>()

  function count(outcome: string, by = 1): void {
    options.counter?.inc({ outcome }, by)
  }

  /** Adds occurrences to the buffer, obeying the buffer cap. Shared by `record`
   *  and by the merge back a failed flush performs, so a retry cannot grow the
   *  buffer past the bound that a first arrival obeys. */
  function accumulate(key: string, incoming: Pending): void {
    const existing = buffer.get(key)
    if (existing) {
      existing.count += incoming.count
      if (incoming.firstAt < existing.firstAt) existing.firstAt = incoming.firstAt
      // Not `>` : a retry merging back a batch stamped in the same millisecond
      // as a newer arrival must not move the request id backwards. The newer
      // one is already in `existing`, so ties keep it.
      if (incoming.lastAt > existing.lastAt) {
        existing.lastAt = incoming.lastAt
        existing.lastRequestId = incoming.lastRequestId
      }
      return
    }
    if (buffer.size >= bufferCap) {
      count('dropped', incoming.count)
      return
    }
    buffer.set(key, incoming)
  }

  return {
    enabled,

    record(f) {
      count('observed')
      if (!enabled) return
      const bounded: Failure = {
        source: f.source,
        route: clamp(f.route, BOUNDS.route) || 'unknown',
        method: clamp(f.method, BOUNDS.method) || 'unknown',
        kind: clamp(f.kind, BOUNDS.kind) || 'unknown',
        providerCode: f.providerCode ? clamp(f.providerCode, BOUNDS.providerCode) : null,
        requestId: f.requestId ? clamp(f.requestId, BOUNDS.requestId) : null,
      }
      const at = options.clock.now()
      accumulate(fingerprintOf(bounded), {
        source: bounded.source,
        route: bounded.route,
        method: bounded.method,
        kind: bounded.kind,
        providerCode: bounded.providerCode,
        count: 1,
        firstAt: at,
        lastAt: at,
        lastRequestId: bounded.requestId,
      })
    },

    async flush() {
      const result: FlushResult = { applied: 0, capped: 0, failed: 0 }
      const pool = options.pool
      if (!pool || buffer.size === 0) return result

      // Swapped rather than drained, so a failure arriving during the flush
      // lands in the new buffer and is not counted twice when a failed write is
      // merged back.
      const batch = buffer
      buffer = new Map()

      for (const [key, p] of batch) {
        try {
          const applied = await writeGroup(pool, key, p, options.version, groupCap)
          if (applied) {
            result.applied += 1
          } else {
            // The table is full of other groups. Retrying on the next tick
            // would fail identically, so the occurrences are discarded and the
            // fact that they were is a metric and a line on the page, never a
            // silence.
            result.capped += 1
            count('capped', p.count)
          }
        } catch {
          // The occurrences go back. A control plane whose database is down
          // fails every request, which is exactly when the buffer must hold
          // rather than shed, so that the store backfills the shape of the
          // outage the moment the database returns.
          result.failed += 1
          count('failed', p.count)
          accumulate(key, p)
        }
      }
      return result
    },

    buffered() {
      return buffer.size
    },
  }
}

/**
 * One group, in one statement.
 *
 * THE CAP AND THE UPSERT ARE THE SAME STATEMENT, and the order of the two
 * conditions is the whole of it. `EXISTS` first means a group already in the
 * table keeps counting up after the cap is reached; only a NEW fingerprint is
 * refused. The first version tested only the count, so at the cap every group
 * stopped counting, which reads as an outage that ended.
 *
 * Two replicas inserting two different new fingerprints at cap minus one can
 * both see the count pass and both insert, so the cap is exceeded by at most
 * the number of concurrent flushes. That is a stated bound rather than an
 * unbounded one, and tightening it would mean taking a lock on a table on the
 * error path.
 *
 * Returns whether a row was written. `RETURNING` gives a row on both the insert
 * and the conflict update, and none at all when the WHERE refused, so the
 * refusal is observable rather than inferred from an unchanged count.
 */
async function writeGroup(
  pool: Pool,
  fingerprint: string,
  p: Pending,
  version: string,
  groupCap: number,
): Promise<boolean> {
  // withoutTenant: this table carries no org_id and there is nothing for a
  // tenant setting to key on. See migration 0042.
  return pool.withoutTenant(async (db) => {
    const rows = await db.execute(sql`
      INSERT INTO control_plane_failures (
        fingerprint, source, route, method, kind, provider_code,
        occurrences, first_seen_at, last_seen_at,
        first_seen_version, last_seen_version, last_request_id)
      SELECT ${fingerprint}, ${p.source}, ${p.route}, ${p.method}, ${p.kind}, ${p.providerCode},
             ${p.count}, ${p.firstAt.toISOString()}, ${p.lastAt.toISOString()},
             ${version}, ${version}, ${p.lastRequestId}
      WHERE EXISTS (SELECT 1 FROM control_plane_failures WHERE fingerprint = ${fingerprint})
         OR (SELECT count(*) FROM control_plane_failures) < ${groupCap}
      ON CONFLICT (fingerprint) DO UPDATE SET
        occurrences = control_plane_failures.occurrences + EXCLUDED.occurrences,
        first_seen_at = LEAST(control_plane_failures.first_seen_at, EXCLUDED.first_seen_at),
        last_seen_at = GREATEST(control_plane_failures.last_seen_at, EXCLUDED.last_seen_at),
        -- The version columns follow their timestamps rather than the write
        -- order. A replica flushing late, or one still on the previous revision
        -- during a rollout, must not relabel a group with a build that is not
        -- the newest one to have produced it.
        first_seen_version = CASE
          WHEN EXCLUDED.first_seen_at < control_plane_failures.first_seen_at
          THEN EXCLUDED.first_seen_version ELSE control_plane_failures.first_seen_version END,
        last_seen_version = CASE
          WHEN EXCLUDED.last_seen_at >= control_plane_failures.last_seen_at
          THEN EXCLUDED.last_seen_version ELSE control_plane_failures.last_seen_version END,
        last_request_id = CASE
          WHEN EXCLUDED.last_seen_at >= control_plane_failures.last_seen_at
          THEN EXCLUDED.last_request_id ELSE control_plane_failures.last_request_id END
      RETURNING fingerprint`)
    return rowCountOf(rows) > 0
  })
}

/**
 * How many rows came back.
 *
 * `db.execute` in this codebase resolves to the rows array, which every caller
 * in admin/ reads as `rows[0]`. The object form is handled as well rather than
 * assumed away, because reading the wrong one yields `undefined`, whose length
 * is `undefined`, which compares false against zero and would report every
 * successful write as capped. A store that reports itself full is a store the
 * operator stops believing.
 */
function rowCountOf(result: unknown): number {
  if (Array.isArray(result)) return result.length
  const rows = (result as { rows?: unknown } | null)?.rows
  return Array.isArray(rows) ? rows.length : 0
}

/**
 * Retention, on the credential that owns the table.
 *
 * The application role is given no DELETE in 0042, deliberately: a role reached
 * through a request path should not be able to erase the record of what it did
 * to get there. So this runs beside the partition maintenance, on the same
 * administrative connection and on the same daily pass, because a second
 * scheduler is a second thing to notice had stopped.
 *
 * Retention here bounds STALENESS, not size. Size is bounded by the group cap,
 * which holds whether or not anything ever sweeps. An installation with no
 * administrative connection string configured therefore keeps a group whose
 * last occurrence was a year ago, and the page prints that date rather than
 * implying the failure is current.
 */
export const DEFAULT_FAILURE_RETENTION_DAYS = 30

export function failureRetentionFrom(env: NodeJS.ProcessEnv): number {
  const raw = (env.AF_FAILURE_RETENTION_DAYS ?? '').trim()
  if (raw === '') return DEFAULT_FAILURE_RETENTION_DAYS
  const days = Number(raw)
  if (!Number.isInteger(days) || days < 1) {
    throw new Error(
      'AF_FAILURE_RETENTION_DAYS has to be a whole number of days, one or more. ' +
        'Unset it to keep the default of ' +
        DEFAULT_FAILURE_RETENTION_DAYS +
        ' days.',
    )
  }
  return days
}

/**
 * What the page prints above the rows, from a count and the configuration.
 *
 * A PURE FUNCTION rather than three expressions inside the route, because the
 * one field here that matters most cannot be exercised through the route at
 * all: `atCap` needs five hundred groups in the table to turn true, so a route
 * test can only ever watch it say false. A check that has only been seen say
 * one thing has not been shown to be able to say the other, and `atCap` is the
 * field that tells an operator the list below is shorter than the truth.
 */
export function statusOf(
  groups: number,
  config: { recording: boolean; cap: number; retentionDays: number | null },
): { recording: boolean; groups: number; cap: number; atCap: boolean; retentionDays: number | null } {
  return {
    recording: config.recording,
    groups,
    cap: config.cap,
    // At, not past. A table holding exactly `cap` groups is one that will
    // refuse the next new fingerprint, and telling the operator only after it
    // has already started losing them is telling them too late.
    atCap: groups >= config.cap,
    retentionDays: config.retentionDays,
  }
}

/**
 * What the store is actually doing on this installation, for the page to print.
 *
 * READ FROM THE ENVIRONMENT AT CALL TIME rather than captured at boot, because
 * every value here is a fact about the process answering the request and the
 * page says so. A rolling deploy has two revisions serving at once and they can
 * disagree about all three.
 *
 * `retentionDays` is null when nothing sweeps, and the condition is the
 * administrative connection string rather than the retention number. Migration
 * 0042 gives the application role no DELETE, so the sweep runs on the
 * maintenance credential; an installation without one keeps every group for
 * good, and reporting the configured number there would be reporting a policy
 * nothing enforces. That is the shape of lie this whole feature exists to
 * avoid.
 */
export function failureStoreConfig(env: NodeJS.ProcessEnv): {
  recording: boolean
  cap: number
  retentionDays: number | null
} {
  const sweeps = Boolean(env.AF_MAINTENANCE_DATABASE_URL ?? env.AF_MIGRATION_DATABASE_URL)
  return {
    recording: failureStoreEnabledFrom(env),
    cap: DEFAULT_GROUP_CAP,
    retentionDays: sweeps ? failureRetentionFrom(env) : null,
  }
}

/**
 * The driver's own code, from anywhere in the cause chain.
 *
 * WALKED RATHER THAN READ ONE LEVEL DOWN, and the two error handlers need
 * different depths, which is why this is a function rather than a property
 * access repeated twice. A Drizzle failure escaping a REST route arrives as
 * `DrizzleQueryError` whose `cause` is the Postgres error, so one level finds
 * `42P01`. The same failure inside a procedure arrives wrapped again, as
 * `TRPCError` whose cause is the Drizzle error whose cause is the Postgres one,
 * and one level finds nothing at all.
 *
 * Reading one level in both places is what the first version did, and the
 * result was not an error: it was a null in the column that separates a missing
 * table from a connection reset, on the arm that produces most of this control
 * plane's failures. Every one of them would have fingerprinted into a single
 * group per procedure.
 *
 * Bounded depth and a seen set, because a cause chain is a linked list built by
 * whatever threw, and an error whose cause is itself would otherwise hang the
 * error handler.
 *
 * It returns the FIRST string `code` it finds, so the caller decides where the
 * walk starts. The tRPC handler starts at the cause rather than the error,
 * because a TRPCError carries a `code` of its own which is the tRPC enum and is
 * INTERNAL_SERVER_ERROR for every failure that reaches this store.
 */
export function providerCodeOf(err: unknown): string | null {
  const seen = new Set<unknown>()
  let current: unknown = err
  for (let depth = 0; depth < 8 && current !== null && typeof current === 'object'; depth++) {
    if (seen.has(current)) return null
    seen.add(current)
    const code = (current as { code?: unknown }).code
    if (typeof code === 'string' && code !== '') return code
    current = (current as { cause?: unknown }).cause
  }
  return null
}
