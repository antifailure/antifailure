// The grouped store of the control plane's own failures.
//
// WHAT EACH TEST IS AIMED AT. This store's failure modes are all quiet ones: it
// under-reports and the page still looks like an answer. So every test here is
// pointed at a specific way a number could be lower than the truth without
// anybody noticing, and the database-backed half of the file is written against
// real SQL because the two properties that matter most, the cap and the
// monotonic timestamps, live in one statement and cannot be checked anywhere
// else.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import postgres from 'postgres'
import { createPool, migrate, type Pool } from '@antifailure/db'
import {
  DEFAULT_FAILURE_RETENTION_DAYS,
  createFailureStore,
  failureRetentionFrom,
  failureStoreConfig,
  failureStoreEnabledFrom,
  fingerprintOf,
  statusOf,
  type Failure,
} from '../src/failures.ts'
import { runMaintenance } from '../src/maintenance.ts'
import { Counter } from '../src/metrics.ts'
import { FakeClock } from '../src/clock.ts'
import { adminUrl, appUrl, available } from './harness.ts'

function failure(over: Partial<Failure> = {}): Failure {
  return {
    source: 'http',
    route: 'GET /v1/environments/:envId',
    method: 'GET',
    kind: 'DrizzleQueryError',
    providerCode: '42501',
    requestId: 'req-1',
    ...over,
  }
}

/* -------------------------------------------------------------------------
 * The fingerprint
 * ---------------------------------------------------------------------- */

describe('the fingerprint', () => {
  it('is the same for the same failure and different for every field that differs', () => {
    const base = fingerprintOf(failure())
    assert.equal(base, fingerprintOf(failure({ requestId: 'req-99' })))

    // One break per assertion: `assert` stops at the first failure, so five
    // separate statements is five separate reports rather than one.
    assert.notEqual(base, fingerprintOf(failure({ source: 'trpc' })))
    assert.notEqual(base, fingerprintOf(failure({ method: 'POST' })))
    assert.notEqual(base, fingerprintOf(failure({ route: 'GET /v1/runs/:runId' })))
    assert.notEqual(base, fingerprintOf(failure({ kind: 'TypeError' })))
    assert.notEqual(base, fingerprintOf(failure({ providerCode: '23505' })))
    assert.notEqual(base, fingerprintOf(failure({ providerCode: null })))
  })

  it('cannot be made to collide by moving a character from one field to the next', () => {
    // The reading a collision produces is the worst this feature can produce: a
    // failure that looks like it stopped, because its occurrences are being
    // added to somebody else's row. A separator, any separator, is a character
    // some field might one day carry.
    const a = fingerprintOf(failure({ route: 'AB', kind: 'C' }))
    const b = fingerprintOf(failure({ route: 'A', kind: 'BC' }))
    assert.notEqual(a, b)

    const c = fingerprintOf(failure({ route: 'A\nB', kind: 'C' }))
    const d = fingerprintOf(failure({ route: 'A', kind: 'B\nC' }))
    assert.notEqual(c, d)
  })
})

/* -------------------------------------------------------------------------
 * The buffer, with no database
 * ---------------------------------------------------------------------- */

describe('the in-process buffer', () => {
  function store(over: Parameters<typeof createFailureStore>[0]['bufferCap'] = undefined) {
    const counter = new Counter('af_control_plane_failures_total', 'test')
    const s = createFailureStore({
      pool: null,
      clock: new FakeClock(),
      version: 'test',
      counter,
      ...(over === undefined ? {} : { bufferCap: over }),
    })
    return { s, counter }
  }

  it('counts every failure as observed even when there is nowhere to write it', () => {
    const { s, counter } = store()
    assert.equal(s.enabled, false)
    s.record(failure())
    s.record(failure())
    // The denominator. Without it, a store that is switched off and a control
    // plane that is not failing produce the identical absence.
    assert.equal(counter.get({ outcome: 'observed' }), 2)
    assert.equal(s.buffered(), 0)
  })

  it('drops a NEW group at a full buffer and lets an existing one keep counting', async () => {
    // Both halves matter and they fail differently. Dropping the new one is the
    // bound; letting the existing one keep counting is what stops a full buffer
    // reading as an outage that ended.
    const pool = fakePool()
    const counter = new Counter('af_control_plane_failures_total', 'test')
    const s = createFailureStore({
      pool: pool.pool,
      clock: new FakeClock(),
      version: 'test',
      counter,
      bufferCap: 2,
    })

    s.record(failure({ route: 'A' }))
    s.record(failure({ route: 'B' }))
    assert.equal(s.buffered(), 2)

    s.record(failure({ route: 'C' }))
    assert.equal(s.buffered(), 2, 'a third distinct group must not be admitted')
    assert.equal(counter.get({ outcome: 'dropped' }), 1)

    s.record(failure({ route: 'A' }))
    assert.equal(s.buffered(), 2)
    await s.flush()
    const a = pool.written.find((w) => w.route === 'A')
    assert.equal(a?.count, 2, 'an existing group keeps counting at a full buffer')
  })

  it('clamps a field past its column bound rather than letting the batch be refused', async () => {
    const pool = fakePool()
    const s = createFailureStore({
      pool: pool.pool,
      clock: new FakeClock(),
      version: 'test',
    })
    s.record(failure({ kind: 'K'.repeat(400) }))
    await s.flush()
    // 100 is the CHECK constraint in migration 0042. Over it, the flush raises
    // 23514 and takes every other group in the batch with it.
    assert.equal(pool.written[0]?.kind.length, 100)
  })
})

/* -------------------------------------------------------------------------
 * Orderings, with a pool that can be made to fail
 * ---------------------------------------------------------------------- */

/**
 * A pool that records what the store asked it to write and can be told to
 * raise.
 *
 * Deliberately not a real database for these three: what is under test is the
 * ORDER of events around a flush, and a real database makes the interesting
 * orderings hard to reach and the assertions about them indirect.
 */
/**
 * The values the store bound into one upsert, read positionally.
 *
 * A drizzle `sql` template holds its literal text as StringChunk objects and
 * its interpolated values as the values themselves, so the parameters are
 * every chunk that is not a StringChunk, in order.
 *
 * THE LENGTH CHECK IS THE POINT. Reading index 2 out of a list this fake
 * guessed the shape of is exactly the kind of instrument that answers a nearby
 * question: if `writeGroup` ever binds a value in a different place, every
 * assertion below would go on comparing the wrong field against the right
 * answer and passing. Fourteen is what failures.ts binds, and being wrong about
 * it has to be a failure rather than a silence.
 */
function paramsOf(query: unknown): { route: string; kind: string; count: number } {
  const chunks = (query as { queryChunks?: unknown[] }).queryChunks
  assert.ok(Array.isArray(chunks), 'the store did not pass a drizzle sql template')
  const params = chunks.filter((c) => c === null || typeof c !== 'object')
  assert.equal(
    params.length,
    14,
    'writeGroup binds a different number of values than this fake reads positions from',
  )
  return { route: String(params[2]), kind: String(params[4]), count: Number(params[6]) }
}

function fakePool() {
  const written: { route: string; kind: string; count: number }[] = []
  let raise = false
  let onWrite: (() => void) | null = null
  const pool = {
    async withoutTenant<T>(fn: (db: unknown) => Promise<T>): Promise<T> {
      return fn({
        async execute(query: unknown) {
          if (raise) throw new Error('the database did not answer')
          written.push(paramsOf(query))
          onWrite?.()
          return [{ fingerprint: 'x' }]
        },
      } as unknown as Parameters<typeof fn>[0])
    },
  } as unknown as Pool
  return {
    pool,
    written,
    fail(on: boolean) {
      raise = on
    },
    duringWrite(fn: (() => void) | null) {
      onWrite = fn
    },
  }
}

describe('the orderings around a flush', () => {
  it('a flush with nothing buffered writes nothing and says so', async () => {
    const pool = fakePool()
    const s = createFailureStore({ pool: pool.pool, clock: new FakeClock(), version: 'v' })
    assert.deepEqual(await s.flush(), { applied: 0, capped: 0, failed: 0 })
    assert.equal(pool.written.length, 0)
  })

  it('a failed flush holds the occurrences and the next one writes all of them', async () => {
    // The ordering that matters most: the control plane's database being
    // unreachable is exactly when every request fails, so shedding the buffer
    // there would discard the shape of the outage the store exists to record.
    const pool = fakePool()
    const counter = new Counter('af_control_plane_failures_total', 'test')
    const s = createFailureStore({
      pool: pool.pool,
      clock: new FakeClock(),
      version: 'v',
      counter,
    })

    pool.fail(true)
    s.record(failure())
    s.record(failure())
    const first = await s.flush()
    assert.equal(first.failed, 1)
    assert.equal(pool.written.length, 0)
    assert.equal(counter.get({ outcome: 'failed' }), 2)
    assert.equal(s.buffered(), 1, 'the group is back in the buffer')

    pool.fail(false)
    s.record(failure())
    const second = await s.flush()
    assert.equal(second.applied, 1)
    assert.equal(pool.written[0]?.count, 3, 'nothing was lost and nothing was double counted')
    assert.equal(s.buffered(), 0)
  })

  it('a failure arriving DURING a flush is written by the next one, exactly once', async () => {
    // The buffer is swapped rather than drained for this. Draining would let an
    // arrival land in the map being iterated, which is either lost or counted
    // twice depending on where the iterator was.
    const pool = fakePool()
    const s = createFailureStore({ pool: pool.pool, clock: new FakeClock(), version: 'v' })
    s.record(failure())
    pool.duringWrite(() => s.record(failure()))
    await s.flush()
    assert.equal(pool.written[0]?.count, 1)

    pool.duringWrite(null)
    await s.flush()
    assert.equal(pool.written.length, 2)
    assert.equal(pool.written[1]?.count, 1, 'the arrival is written once, not twice and not never')
  })
})

/* -------------------------------------------------------------------------
 * Configuration
 * ---------------------------------------------------------------------- */

describe('the configuration', () => {
  it('records by default and stops only when told to', () => {
    assert.equal(failureStoreEnabledFrom({}), true)
    assert.equal(failureStoreEnabledFrom({ AF_FAILURE_STORE: 'off' }), false)
    assert.equal(failureStoreEnabledFrom({ AF_FAILURE_STORE: '0' }), false)
    assert.equal(failureStoreEnabledFrom({ AF_FAILURE_STORE: 'on' }), true)
  })

  it('refuses a retention that is not a whole number of days rather than picking one', () => {
    assert.equal(failureRetentionFrom({}), DEFAULT_FAILURE_RETENTION_DAYS)
    assert.equal(failureRetentionFrom({ AF_FAILURE_RETENTION_DAYS: '7' }), 7)
    assert.throws(() => failureRetentionFrom({ AF_FAILURE_RETENTION_DAYS: '0' }))
    assert.throws(() => failureRetentionFrom({ AF_FAILURE_RETENTION_DAYS: 'a week' }))
  })

  it('says the store is full at the cap and not one group later', () => {
    // The field an operator reads to know the list below is shorter than the
    // truth. It cannot be exercised through the route, which would need five
    // hundred groups in the table, so it is asserted here in both directions.
    const config = { recording: true, cap: 500, retentionDays: 30 }
    assert.equal(statusOf(499, config).atCap, false)
    assert.equal(statusOf(500, config).atCap, true)
    assert.equal(statusOf(501, config).atCap, true)
    assert.equal(statusOf(0, { ...config, recording: false }).recording, false)
    assert.equal(statusOf(0, { ...config, retentionDays: null }).retentionDays, null)
  })

  it('reports no retention at all when nothing on the installation can sweep', () => {
    // The page prints this, and printing a configured number that nothing
    // enforces is the shape of lie the whole feature exists to avoid. The
    // application role has no DELETE, so the sweep needs the maintenance
    // credential and its absence is the real answer.
    assert.equal(failureStoreConfig({}).retentionDays, null)
    assert.equal(
      failureStoreConfig({ AF_MAINTENANCE_DATABASE_URL: 'postgres://x' }).retentionDays,
      DEFAULT_FAILURE_RETENTION_DAYS,
    )
    assert.equal(
      failureStoreConfig({ AF_MIGRATION_DATABASE_URL: 'postgres://x' }).retentionDays,
      DEFAULT_FAILURE_RETENTION_DAYS,
    )
  })
})

/* -------------------------------------------------------------------------
 * The statement itself
 * ---------------------------------------------------------------------- */

describe('the upsert, against real SQL', { skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let admin: postgres.Sql
  let pool: Pool

  before(async () => {
    admin = postgres(adminUrl, { max: 2, onnotice: () => {} })
    await migrate(admin)
    await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)
    pool = createPool({ url: appUrl(), max: 3 })
  })

  after(async () => {
    await pool.close()
    await admin.end({ timeout: 5 })
  })

  async function clear() {
    await admin`DELETE FROM control_plane_failures`
  }

  function storeAt(at: string, version = 'v1', groupCap?: number) {
    return createFailureStore({
      pool,
      clock: new FakeClock(at),
      version,
      ...(groupCap === undefined ? {} : { groupCap }),
    })
  }

  it('adds occurrences to a group rather than replacing it', async () => {
    await clear()
    const a = storeAt('2026-01-01T00:00:00.000Z')
    a.record(failure())
    a.record(failure())
    await a.flush()

    const b = storeAt('2026-01-01T00:05:00.000Z')
    b.record(failure())
    await b.flush()

    const rows = await admin`SELECT occurrences, first_seen_at, last_seen_at FROM control_plane_failures`
    assert.equal(rows.length, 1)
    assert.equal(Number(rows[0]!.occurrences), 3)
    assert.equal(new Date(rows[0]!.first_seen_at).toISOString(), '2026-01-01T00:00:00.000Z')
    assert.equal(new Date(rows[0]!.last_seen_at).toISOString(), '2026-01-01T00:05:00.000Z')
  })

  it('never moves last seen backwards, however late a replica flushes', async () => {
    // Several replicas write this table and a slow one can flush a batch that
    // is minutes old. Without GREATEST, that batch would relabel the group as
    // last seen in the past, which reads as a failure that has stopped.
    await clear()
    const now = storeAt('2026-01-01T12:00:00.000Z', 'v2')
    now.record(failure({ requestId: 'newest' }))
    await now.flush()

    const late = storeAt('2026-01-01T11:00:00.000Z', 'v1')
    late.record(failure({ requestId: 'older' }))
    await late.flush()

    const rows = await admin`
      SELECT last_seen_at, first_seen_at, last_request_id, last_seen_version, first_seen_version
      FROM control_plane_failures`
    assert.equal(new Date(rows[0]!.last_seen_at).toISOString(), '2026-01-01T12:00:00.000Z')
    assert.equal(rows[0]!.last_request_id, 'newest')
    assert.equal(rows[0]!.last_seen_version, 'v2')
    // The older flush DOES move first seen and the build that first produced
    // it, which is the half that is supposed to move.
    assert.equal(new Date(rows[0]!.first_seen_at).toISOString(), '2026-01-01T11:00:00.000Z')
    assert.equal(rows[0]!.first_seen_version, 'v1')
  })

  it('refuses a NEW group at the cap and keeps counting the ones already there', async () => {
    // Both halves in one test because the statement is one statement, and the
    // first version of it tested only the count, so at the cap EVERY group
    // stopped counting. On a page that reads as an incident that ended.
    await clear()
    const capped = storeAt('2026-01-01T00:00:00.000Z', 'v1', 2)
    capped.record(failure({ route: 'A' }))
    capped.record(failure({ route: 'B' }))
    await capped.flush()
    assert.equal(Number((await admin`SELECT count(*) AS n FROM control_plane_failures`)[0]!.n), 2)

    const more = storeAt('2026-01-01T00:01:00.000Z', 'v1', 2)
    more.record(failure({ route: 'C' }))
    more.record(failure({ route: 'A' }))
    const result = await more.flush()

    assert.equal(result.capped, 1, 'the new group is refused')
    assert.equal(result.applied, 1, 'the existing group is not')
    const rows = await admin`SELECT route, occurrences FROM control_plane_failures ORDER BY route`
    assert.deepEqual(rows.map((r) => r.route), ['A', 'B'])
    assert.equal(Number(rows[0]!.occurrences), 2, 'A kept counting past the cap')
  })

  it('sweeps by the last occurrence and not the first', async () => {
    // A group first seen four months ago and last seen this morning is the most
    // interesting row on the page. Sweeping by its age would delete exactly the
    // long running failure an operator is trying to date.
    await clear()
    const old = storeAt('2026-01-01T00:00:00.000Z')
    old.record(failure({ route: 'gone' }))
    await old.flush()

    const longRunning = storeAt('2026-01-01T00:00:00.000Z')
    longRunning.record(failure({ route: 'kept' }))
    await longRunning.flush()
    const stillHappening = storeAt('2026-06-01T00:00:00.000Z')
    stillHappening.record(failure({ route: 'kept' }))
    await stillHappening.flush()

    const run = await runMaintenance(
      { adminUrl, failureRetentionDays: 30, log: () => {} },
      new FakeClock('2026-06-02T00:00:00.000Z'),
    )
    assert.equal(run.failuresPruned, 1)
    const rows = await admin`SELECT route FROM control_plane_failures`
    assert.deepEqual(rows.map((r) => r.route), ['kept'])
  })

  it('keeps everything when no retention is configured', async () => {
    await clear()
    const old = storeAt('2020-01-01T00:00:00.000Z')
    old.record(failure({ route: 'ancient' }))
    await old.flush()
    const run = await runMaintenance({ adminUrl, log: () => {} }, new FakeClock('2026-06-02T00:00:00.000Z'))
    assert.equal(run.failuresPruned, 0)
    assert.equal(Number((await admin`SELECT count(*) AS n FROM control_plane_failures`)[0]!.n), 1)
  })
})
