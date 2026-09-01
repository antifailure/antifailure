// Keeping the events partitions ahead of the writes, and behind the retention.
//
// A range-partitioned table with no partition for an incoming row does not slow
// down, it fails. So the only safe posture is to stay ahead: months are created
// before anything needs them, and the margin is wide enough that missing a run
// or two is uneventful rather than an outage.
//
// The other half is the reason partitioning is here at all. Dropping a month is
// a catalogue update and a file unlink. Deleting the same rows from a heap is a
// long transaction, a lock, and a vacuum afterwards. Everything below exists so
// that retention is the first of those and never the second.
//
// This runs as the owner, not as the application. The application role is
// deliberately not allowed to run DDL, because a role that can ALTER TABLE can
// drop the policies that isolate tenants.

import type postgres from 'postgres'

/**
 * A partitioned table this manages, and the policy its partitions carry.
 *
 * The policy travels with the table because a partition created without one is
 * a copy of the table with nothing protecting it, and the two tables here want
 * opposite rules: `events` confines a partition to one tenant, and
 * `analytics_events` confines it to being written and never read. Naming both
 * in one place is what stops the second table from becoming a copy of this
 * file with two words changed.
 */
export interface PartitionedTable {
  name: string
  policy: {
    name: string
    /** The rows a statement may see. Composed by this module, never by a
     *  caller: it is spliced into DDL. */
    using: string | null
    /** The rows a statement may write. */
    withCheck: string | null
    /** Which statements the policy covers. */
    forCommand: 'ALL' | 'INSERT' | 'SELECT'
  }
}

/** The engine's event stream, isolated per tenant. */
export const EVENTS: PartitionedTable = {
  name: 'events',
  policy: {
    name: 'tenant_isolation',
    using: 'org_id = current_org()',
    withCheck: 'org_id = current_org()',
    forCommand: 'ALL',
  },
}

/**
 * The analytics stream, which the application writes and never reads.
 *
 * There is no SELECT grant on it, so a read already raises 42501 rather than
 * returning zero rows. This is the second lock: a GRANT added later by somebody
 * who did not read migrations/0024 still cannot open a read.
 */
export const ANALYTICS_EVENTS: PartitionedTable = {
  name: 'analytics_events',
  policy: {
    name: 'analytics_stream_is_write_only',
    using: null,
    withCheck: 'true',
    forCommand: 'INSERT',
  },
}

/** The table this manages when a caller names none. */
export const PARTITIONED_TABLE = EVENTS.name

export interface PartitionPlan {
  /** Months to create, oldest first, as 'YYYY-MM-01' boundaries. */
  create: string[]
  /** Partitions to drop, oldest first. */
  drop: string[]
}

export interface PartitionOptions {
  /** How many months to keep created beyond the current one. */
  monthsAhead?: number
  /** Drop partitions whose whole range is older than this many months.
   *  Undefined means never drop, which is the default: a retention policy is
   *  a decision an operator makes, not one a library makes for them. */
  retentionMonths?: number
  /** Overridden by tests. Production passes nothing and gets the real clock. */
  now?: Date
  /** Which partitioned table to manage. Defaults to the engine's events. */
  table?: PartitionedTable
}

export interface PartitionState {
  name: string
  /** Inclusive lower bound, or null for the default partition. */
  from: Date | null
  /** Exclusive upper bound, or null for the default partition. */
  to: Date | null
  isDefault: boolean
}

const MONTHS_AHEAD = 3

/** The first instant of the month containing `at`, in UTC. */
export function monthStart(at: Date): Date {
  return new Date(Date.UTC(at.getUTCFullYear(), at.getUTCMonth(), 1))
}

/** `n` months after `at`, in UTC. Handles the year boundary because Date.UTC
 *  normalises a month index outside 0..11. */
export function addMonths(at: Date, n: number): Date {
  return new Date(Date.UTC(at.getUTCFullYear(), at.getUTCMonth() + n, 1))
}

/** The partition name for a month, matching what the migration created. */
export function partitionName(month: Date, table = PARTITIONED_TABLE): string {
  const y = month.getUTCFullYear()
  const m = String(month.getUTCMonth() + 1).padStart(2, '0')
  return `${table}_${y}_${m}`
}

/** Reads what partitions exist and what range each covers. */
export async function currentPartitions(
  sql: postgres.Sql,
  table = PARTITIONED_TABLE,
): Promise<PartitionState[]> {
  const rows = await sql<{ name: string; bounds: string }[]>`
    SELECT c.relname AS name, pg_get_expr(c.relpartbound, c.oid) AS bounds
    FROM pg_class c
    JOIN pg_inherits i ON i.inhrelid = c.oid
    WHERE i.inhparent = ${`public.${table}`}::regclass
    ORDER BY c.relname`

  return rows.map((r) => {
    if (/DEFAULT/i.test(r.bounds)) {
      return { name: r.name, from: null, to: null, isDefault: true }
    }
    // FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00')
    const found = r.bounds.match(/FROM \('([^']+)'\) TO \('([^']+)'\)/)
    const from = found?.[1]
    const to = found?.[2]
    if (from === undefined || to === undefined) {
      throw new Error(`partition ${r.name} has bounds this cannot read: ${r.bounds}`)
    }
    return { name: r.name, from: new Date(from), to: new Date(to), isDefault: false }
  })
}

/**
 * Works out what to create and what to drop, without touching the database.
 *
 * Separated from applying it so the decision can be tested directly and so an
 * operator can see what a retention setting would do before it does it.
 */
export function plan(
  existing: PartitionState[],
  opts: PartitionOptions & { now: Date },
): PartitionPlan {
  const ahead = opts.monthsAhead ?? MONTHS_AHEAD
  const table = (opts.table ?? EVENTS).name
  const current = monthStart(opts.now)
  const have = new Set(existing.filter((p) => !p.isDefault).map((p) => p.name))

  const create: string[] = []
  for (let i = 0; i <= ahead; i++) {
    const month = addMonths(current, i)
    if (!have.has(partitionName(month, table))) create.push(month.toISOString())
  }

  const drop: string[] = []
  if (opts.retentionMonths !== undefined) {
    // The cutoff is the start of the oldest month worth keeping. A partition
    // goes only when its whole range is before that, never when it merely
    // starts before it, so retention never removes a row inside the window.
    const cutoff = addMonths(current, -opts.retentionMonths)
    for (const p of existing) {
      // The default partition is never dropped. It holds late arrivals whose
      // month is already gone, and dropping it would take rows nobody has
      // decided about along with it. pruneDefault handles those by age.
      if (p.isDefault || p.to === null) continue
      if (p.to.getTime() <= cutoff.getTime()) drop.push(p.name)
    }
    drop.sort()
  }

  return { create, drop }
}

export interface ApplyResult {
  created: string[]
  dropped: string[]
}

/**
 * Brings the partitions in line with the plan.
 *
 * Each statement runs on its own rather than in one transaction, because
 * CREATE TABLE ... PARTITION OF takes a lock on the parent and holding several
 * of those across a single long transaction is how a maintenance job turns into
 * an incident. A run that half-applies is safe: the next one finishes it.
 */
export async function apply(
  sql: postgres.Sql,
  opts: PartitionOptions = {},
): Promise<ApplyResult> {
  const target = opts.table ?? EVENTS
  const table = target.name
  const now = opts.now ?? new Date()
  const decided = plan(await currentPartitions(sql, table), { ...opts, now })

  const created: string[] = []
  for (const iso of decided.create) {
    const from = new Date(iso)
    const to = addMonths(from, 1)
    const name = partitionName(from, table)
    try {
      await createMonth(sql, target, name, from, to)
    } catch (err) {
      // IF NOT EXISTS is not accepted for a partition, and two managers
      // running at once is a normal thing during a rolling deploy. The loser
      // sees 42P07, and that is a success.
      if (isDuplicateTable(err)) continue
      throw err
    }
    created.push(name)
  }

  const dropped: string[] = []
  for (const name of decided.drop) {
    await sql.unsafe(`DROP TABLE IF EXISTS ${ident(name)}`)
    dropped.push(name)
  }

  return { created, dropped }
}

/**
 * Creates one month, moving anything the default partition is already holding
 * for it.
 *
 * The fast path is a plain CREATE, and it is the path taken every time the
 * manager is running normally, because a month is created before anything can
 * be written into it.
 *
 * The other path is what happens when it was not running. If the job is stopped
 * for long enough that the current month has no partition, every event written
 * meanwhile lands in the default partition, and from then on Postgres refuses
 * to create that month at all: the new partition's constraint would be violated
 * by rows the default already holds. A manager that only knew the fast path
 * would fail on every run from then on, and the backlog would grow.
 *
 * So the rows are moved. Detach the default, create the month, move the rows
 * that belong to it out of the detached table and back in through the parent,
 * and reattach. All in one transaction, because a default partition that is
 * detached is a default partition that is not catching anything.
 */
async function createMonth(
  sql: postgres.Sql,
  target: PartitionedTable,
  name: string,
  from: Date,
  to: Date,
): Promise<void> {
  const table = target.name
  const bounds = `FOR VALUES FROM ('${from.toISOString()}') TO ('${to.toISOString()}')`
  const dflt = `${table}_default`

  const stranded = await sql.unsafe(
    `SELECT 1 FROM ${ident(dflt)} WHERE occurred_at >= $1 AND occurred_at < $2 LIMIT 1`,
    [from.toISOString(), to.toISOString()],
  )

  if (stranded.length === 0) {
    await sql.unsafe(`CREATE TABLE ${ident(name)} PARTITION OF ${ident(table)} ${bounds}`)
    await protect(sql, target, name)
    return
  }

  await sql.begin(async (tx) => {
    await tx.unsafe(`ALTER TABLE ${ident(table)} DETACH PARTITION ${ident(dflt)}`)
    await tx.unsafe(`CREATE TABLE ${ident(name)} PARTITION OF ${ident(table)} ${bounds}`)
    // Through the parent rather than into the partition by name, so routing is
    // Postgres's job and a boundary this code got wrong is an error rather than
    // a row filed in the wrong month.
    await tx.unsafe(
      `WITH moved AS (
         DELETE FROM ${ident(dflt)}
         WHERE occurred_at >= $1 AND occurred_at < $2
         RETURNING *
       )
       INSERT INTO ${ident(table)} SELECT * FROM moved`,
      [from.toISOString(), to.toISOString()],
    )
    await tx.unsafe(
      `ALTER TABLE ${ident(table)} ATTACH PARTITION ${ident(dflt)} DEFAULT`,
    )
    await protect(tx, target, name)
  })
}

/**
 * Gives a new partition the same isolation the parent has.
 *
 * Reading through the parent applies the parent's policy and never a
 * partition's, so this changes nothing about the application's path. It is here
 * so that a query naming a partition directly is isolated by the same rule, and
 * so the isolation suite, which walks every table it finds, does not find one
 * without a policy.
 */
async function protect(
  sql: postgres.Sql | postgres.TransactionSql,
  target: PartitionedTable,
  name: string,
): Promise<void> {
  const { policy } = target
  await sql.unsafe(`ALTER TABLE ${ident(name)} ENABLE ROW LEVEL SECURITY`)
  await sql.unsafe(`ALTER TABLE ${ident(name)} FORCE ROW LEVEL SECURITY`)
  // Composed from the descriptor above and never from anything a caller
  // supplied. ident() covers the identifier; the predicate is a constant in
  // this module, which is why it can be spliced at all.
  await sql.unsafe(
    `CREATE POLICY ${ident(policy.name)} ON ${ident(name)} ` +
      `FOR ${policy.forCommand} TO antifailure_app` +
      (policy.using === null ? '' : ` USING (${policy.using})`) +
      (policy.withCheck === null ? '' : ` WITH CHECK (${policy.withCheck})`),
  )
}

export interface PruneResult {
  deleted: number
}

/**
 * Deletes from the default partition rows older than the retention window.
 *
 * The default partition only ever holds events whose month was already gone
 * when they arrived, which is a late sender and not the normal case. It is
 * small, so this is a plain DELETE and does not need to be anything cleverer.
 * Bounded per call so that a backlog is worked off over several runs rather
 * than in one statement holding a lock for minutes.
 */
export async function pruneDefault(
  sql: postgres.Sql,
  opts: { retentionMonths: number; now?: Date; limit?: number; table?: PartitionedTable },
): Promise<PruneResult> {
  const now = opts.now ?? new Date()
  const cutoff = addMonths(monthStart(now), -opts.retentionMonths)
  const limit = opts.limit ?? 10_000
  const table = `${(opts.table ?? EVENTS).name}_default`

  const rows = await sql.unsafe(
    `DELETE FROM ${ident(table)} WHERE ctid IN (` +
      `SELECT ctid FROM ${ident(table)} WHERE occurred_at < $1 LIMIT ${Number(limit)}` +
      `) RETURNING 1`,
    [cutoff.toISOString()],
  )
  return { deleted: rows.length }
}

/** Double-quotes an identifier this module composed. Nothing user-supplied
 *  reaches here, and this is why that stays true if something ever does. */
function ident(name: string): string {
  if (!/^[a-z_][a-z0-9_]*$/.test(name)) throw new Error(`refusing to quote ${name}`)
  return `"${name}"`
}

function isDuplicateTable(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { code?: string }).code === '42P07'
}

// ---------------------------------------------------------------------------
// Archival
//
// Dropping a month is only safe if whoever wants it has already taken a copy.
// This reads one partition out in batches so that an operator can write it
// wherever they keep cold data, and so that reading a month with millions of
// rows in it does not mean holding millions of rows in memory.
// ---------------------------------------------------------------------------

export interface ArchivedEvent {
  id: string
  org_id: string
  idempotency_key: string
  env_id: string | null
  environment_id: string | null
  run_id: string | null
  sequence: string
  type: string
  payload: unknown
  occurred_at: string
  received_at: string
}

/**
 * Streams one partition out, oldest first, in batches.
 *
 * Keyset pagination on (occurred_at, id) rather than OFFSET, because OFFSET
 * re-reads everything it skips and gets slower the further in it goes, which is
 * exactly backwards for a job whose whole purpose is to reach the end.
 *
 * Timestamps come back as strings in RFC 3339 rather than as Date, because this
 * output is written somewhere else and read by something that is not this
 * process. A driver's timestamp parsing is not a format anybody else agreed to.
 */
export async function* readPartition(
  sql: postgres.Sql,
  name: string,
  opts: { batchSize?: number } = {},
): AsyncGenerator<ArchivedEvent[]> {
  const batch = Math.max(1, opts.batchSize ?? 1_000)
  const table = ident(name)
  let afterAt: string | null = null
  let afterId: string | null = null

  for (;;) {
    const rows: ArchivedEvent[] = await sql.unsafe(
      `SELECT id::text, org_id::text, idempotency_key, env_id,
              environment_id::text, run_id::text, sequence::text, type, payload,
              to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS occurred_at,
              to_char(received_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS received_at
       FROM ${table}
       ${afterAt === null ? '' : 'WHERE (occurred_at, id) > ($1::timestamptz, $2::uuid)'}
       ORDER BY occurred_at, id
       LIMIT ${batch}`,
      afterAt === null ? [] : [afterAt, afterId],
    )
    if (rows.length === 0) return
    yield rows

    const last = rows[rows.length - 1]
    if (last === undefined) return
    afterAt = last.occurred_at
    afterId = last.id
    if (rows.length < batch) return
  }
}
