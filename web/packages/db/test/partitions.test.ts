// The events partitions, against a real Postgres.
//
// The decision to test here is not "does CREATE TABLE work". It is the set of
// orderings a maintenance job actually meets: running twice, running beside
// another copy of itself, and not running at all for long enough that the
// writes went somewhere else. The last of those is the one that turns into an
// outage, because Postgres refuses to create a month the default partition is
// already holding rows for, and a manager that cannot create it never catches
// up on its own.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import {
  addMonths,
  apply,
  currentPartitions,
  monthStart,
  partitionName,
  plan,
  pruneDefault,
  readPartition,
  type PartitionState,
} from '../src/partitions.ts'
import { available, seedTenant, setup, type Fixture, type Harness } from './harness.ts'

const has = await available()

describe('partition planning', { skip: has ? false : 'no database' }, () => {
  const now = new Date('2026-05-17T12:00:00Z')

  function month(iso: string, name?: string): PartitionState {
    const from = new Date(iso)
    return {
      name: name ?? partitionName(from),
      from,
      to: addMonths(from, 1),
      isDefault: false,
    }
  }

  const dflt: PartitionState = { name: 'events_default', from: null, to: null, isDefault: true }

  it('creates the current month and the months ahead of it', () => {
    const decided = plan([dflt], { now, monthsAhead: 3 })
    assert.deepEqual(
      decided.create.map((iso) => partitionName(new Date(iso))),
      ['events_2026_05', 'events_2026_06', 'events_2026_07', 'events_2026_08'],
    )
  })

  it('creates nothing when it is already ahead, so a second run is free', () => {
    const existing = [dflt, month('2026-05-01'), month('2026-06-01'), month('2026-07-01'), month('2026-08-01')]
    assert.deepEqual(plan(existing, { now, monthsAhead: 3 }).create, [])
  })

  it('crosses the year boundary without inventing a month 13', () => {
    const december = new Date('2026-12-02T00:00:00Z')
    const decided = plan([dflt], { now: december, monthsAhead: 2 })
    assert.deepEqual(
      decided.create.map((iso) => partitionName(new Date(iso))),
      ['events_2026_12', 'events_2027_01', 'events_2027_02'],
    )
  })

  it('drops nothing at all unless a retention was asked for', () => {
    // The default. A library that starts deleting an operator's data because
    // they installed it is a library nobody should install.
    const existing = [dflt, month('2019-01-01'), month('2026-05-01')]
    assert.deepEqual(plan(existing, { now, monthsAhead: 1 }).drop, [])
  })

  it('drops only the months whose whole range is behind the retention window', () => {
    const existing = [
      dflt,
      month('2026-01-01'),
      month('2026-02-01'),
      // Two months back with a three month retention: inside the window.
      month('2026-03-01'),
      month('2026-04-01'),
      month('2026-05-01'),
    ]
    const decided = plan(existing, { now, monthsAhead: 0, retentionMonths: 3 })
    assert.deepEqual(decided.drop, ['events_2026_01'])
  })

  it('never drops the default partition, whatever the retention', () => {
    // It holds late arrivals whose month is already gone. Dropping it takes
    // rows nobody decided about along with it, so age is handled by
    // pruneDefault, one bounded delete at a time.
    const decided = plan([dflt, month('2000-01-01')], {
      now,
      monthsAhead: 0,
      retentionMonths: 1,
    })
    assert.ok(!decided.drop.includes('events_default'), 'the default partition was scheduled for a drop')
    assert.deepEqual(decided.drop, ['events_2000_01'])
  })

  it('keeps a month that merely starts before the cutoff', () => {
    // A negative control on the comparison. Using from instead of to here
    // would delete the month the cutoff falls inside, and every row in it,
    // while an operator believed their retention was three months.
    const boundary = new Date('2026-05-01T00:00:00Z')
    const decided = plan([dflt, month('2026-02-01')], {
      now: boundary,
      monthsAhead: 0,
      retentionMonths: 3,
    })
    assert.deepEqual(decided.drop, [], 'the month the retention cutoff falls inside was dropped')
  })
})

describe('partition management', { skip: has ? false : 'no database' }, () => {
  let h: Harness
  let org: Fixture

  before(async () => {
    h = await setup()
    org = await seedTenant(h.admin, 'partitions')
  })
  after(async () => {
    await h.close()
  })

  // Far enough from the calendar that these tests cannot collide with the
  // months the migration made, or with each other.
  const era = new Date('2031-03-10T00:00:00Z')

  async function insert(at: Date, key = randomUUID()): Promise<void> {
    await h.admin`
      INSERT INTO events (org_id, idempotency_key, env_id, type, occurred_at, sequence)
      VALUES (${org.orgId}, ${key}, ${`env-${org.slug}`}, 'environment.ready', ${at}, 1)`
  }

  async function partitionOf(key: string): Promise<string | undefined> {
    const [row] = await h.admin<{ p: string }[]>`
      SELECT tableoid::regclass::text AS p FROM events
      WHERE org_id = ${org.orgId} AND idempotency_key = ${key}`
    return row?.p
  }

  it('creates the months it planned, and gives each one the parent’s isolation', async () => {
    const result = await apply(h.admin, { now: era, monthsAhead: 2 })
    assert.deepEqual(result.created, ['events_2031_03', 'events_2031_04', 'events_2031_05'])

    for (const name of result.created) {
      const [row] = await h.admin<{ sec: boolean; forced: boolean; policies: string }[]>`
        SELECT c.relrowsecurity AS sec, c.relforcerowsecurity AS forced,
               (SELECT count(*) FROM pg_policy p WHERE p.polrelid = c.oid)::text AS policies
        FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relname = ${name}`
      assert.equal(row?.sec, true, `${name} has no row-level security`)
      assert.equal(row?.forced, true, `${name} does not force row-level security`)
      assert.equal(Number(row?.policies), 1, `${name} has no isolation policy`)
    }
  })

  it('is a no-op the second time, so running it on a schedule is safe', async () => {
    const again = await apply(h.admin, { now: era, monthsAhead: 2 })
    assert.deepEqual(again.created, [])
    assert.deepEqual(again.dropped, [])
  })

  it('survives two copies of itself running at once', async () => {
    // What a rolling deploy looks like. One of them loses the race on every
    // CREATE and has to treat that as success, not as an error worth paging
    // somebody about.
    const later = new Date('2031-09-04T00:00:00Z')
    const [a, b] = await Promise.all([
      apply(h.admin, { now: later, monthsAhead: 1 }),
      apply(h.admin, { now: later, monthsAhead: 1 }),
    ])
    const between = [...a.created, ...b.created].sort()
    assert.deepEqual(between, ['events_2031_09', 'events_2031_10'], 'a month was created twice or not at all')
  })

  it('routes a write into the month it occurred in', async () => {
    const key = randomUUID()
    await insert(new Date('2031-04-14T09:00:00Z'), key)
    assert.equal(await partitionOf(key), 'events_2031_04')
  })

  it('puts an event with no month of its own into the default partition rather than failing', async () => {
    // A sender far enough behind that its month is gone. Storing it beats
    // refusing it: the row is the evidence of whatever went wrong.
    const key = randomUUID()
    await insert(new Date('2030-01-05T00:00:00Z'), key)
    assert.equal(await partitionOf(key), 'events_default')
  })

  it('creates a month the default partition is already holding rows for', async () => {
    // The ordering that breaks a manager that only knows CREATE TABLE.
    //
    // The job stops. Writes carry on and land in the default partition because
    // their month does not exist. The job comes back, tries to create that
    // month, and Postgres refuses: the new partition's constraint would be
    // violated by rows the default already holds. Without the repair below,
    // every run from then on fails the same way and the backlog only grows.
    const stranded = new Date('2032-07-19T00:00:00Z')
    const key = randomUUID()
    await insert(stranded, key)
    assert.equal(await partitionOf(key), 'events_default', 'the fixture did not strand the row')

    const result = await apply(h.admin, { now: stranded, monthsAhead: 0 })
    assert.deepEqual(result.created, ['events_2032_07'])
    assert.equal(await partitionOf(key), 'events_2032_07', 'the stranded row was not moved into its month')

    // And the default is still attached and still catching, which a repair
    // that forgot to reattach would not be.
    const parts = await currentPartitions(h.admin)
    assert.ok(parts.some((p) => p.isDefault), 'the default partition was not reattached')

    const late = randomUUID()
    await insert(new Date('2030-02-02T00:00:00Z'), late)
    assert.equal(await partitionOf(late), 'events_default')
  })

  it('drops a month past the retention, and the rows go with it', async () => {
    const old = new Date('2033-01-09T00:00:00Z')
    await apply(h.admin, { now: old, monthsAhead: 0 })
    const key = randomUUID()
    await insert(old, key)
    assert.equal(await partitionOf(key), 'events_2033_01')

    // Eight months later with a three month retention.
    const result = await apply(h.admin, { now: new Date('2033-09-02T00:00:00Z'), monthsAhead: 0, retentionMonths: 3 })
    assert.ok(result.dropped.includes('events_2033_01'), `expected the month to be dropped, dropped ${result.dropped}`)

    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events WHERE org_id = ${org.orgId} AND idempotency_key = ${key}`
    assert.equal(Number(row?.n), 0)
  })

  it('prunes the default partition by age, a bounded number at a time', async () => {
    const keys = [randomUUID(), randomUUID(), randomUUID()]
    for (const k of keys) await insert(new Date('2029-06-01T00:00:00Z'), k)
    const keep = randomUUID()
    await insert(new Date('2034-02-01T00:00:00Z'), keep)
    await apply(h.admin, { now: new Date('2034-02-01T00:00:00Z'), monthsAhead: 0 })

    const first = await pruneDefault(h.admin, {
      retentionMonths: 3,
      now: new Date('2034-05-01T00:00:00Z'),
      limit: 2,
    })
    assert.equal(first.deleted, 2, 'the limit was not respected')

    const rest = await pruneDefault(h.admin, {
      retentionMonths: 3,
      now: new Date('2034-05-01T00:00:00Z'),
      limit: 100,
    })
    assert.ok(rest.deleted >= 1, 'the remainder was not pruned on the next run')

    const [survived] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events WHERE org_id = ${org.orgId} AND idempotency_key = ${keep}`
    assert.equal(Number(survived?.n), 1, 'pruning took a row from inside the retention window')
  })
})

describe('reading a partition out', { skip: has ? false : 'no database' }, () => {
  let h: Harness
  let org: Fixture
  const month = new Date('2035-04-01T00:00:00Z')

  before(async () => {
    h = await setup()
    org = await seedTenant(h.admin, 'archive')
    await apply(h.admin, { now: month, monthsAhead: 0 })
    // readPartition reads a whole partition rather than one tenant's rows,
    // because archiving a month is what it is for. Every other test here scopes
    // its assertions by org_id and does not care what else is in the table;
    // this one counts everything in the partition, so it owns the partition or
    // it counts somebody else's rows. seedTenant makes a new org per run, so
    // without this the rows accumulate and the count grows by 25 each time:
    // green on a fresh database, red on the second run, and the failure blames
    // pagination for what is a fixture problem.
    await h.admin`DELETE FROM events_2035_04`
    for (let i = 0; i < 25; i++) {
      await h.admin`
        INSERT INTO events (org_id, idempotency_key, env_id, type, occurred_at, sequence)
        VALUES (${org.orgId}, ${`arch-${i}`}, ${`env-${org.slug}`}, 'environment.ready',
                ${new Date(month.getTime() + i * 3_600_000)}, ${i})`
    }
  })
  after(async () => {
    // Leave the partition as it was found, so a later suite counting this
    // month does not inherit these 25 rows.
    await h.admin`DELETE FROM events_2035_04`
    await h.close()
  })

  it('yields every row exactly once, in batches, oldest first', async () => {
    const seen: string[] = []
    let batches = 0
    for await (const batch of readPartition(h.admin, 'events_2035_04', { batchSize: 7 })) {
      batches++
      assert.ok(batch.length <= 7, 'a batch came back larger than the size asked for')
      for (const row of batch) seen.push(row.idempotency_key)
    }

    assert.equal(seen.length, 25, 'the number of rows read is not the number written')
    assert.equal(new Set(seen).size, 25, 'a row came back twice, so the pagination is losing its place')
    assert.ok(batches >= 4, `expected several batches at a size of 7, got ${batches}`)
    assert.deepEqual(
      seen,
      Array.from({ length: 25 }, (_, i) => `arch-${i}`),
      'the rows did not come back in occurred_at order',
    )
  })

  it('renders timestamps as text in a format somebody else can read', async () => {
    // The output is written somewhere this process will never read again. A
    // driver's Date parsing is not a format anyone else agreed to.
    const [batch] = await collect(readPartition(h.admin, 'events_2035_04', { batchSize: 1 }))
    const row = batch?.[0]
    assert.ok(row, 'no rows')
    assert.match(row.occurred_at, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z$/)
    assert.equal(typeof row.sequence, 'string', 'a bigint came back as a JavaScript number')
  })

  it('yields nothing at all for a month with no rows in it', async () => {
    await apply(h.admin, { now: new Date('2035-11-01T00:00:00Z'), monthsAhead: 0 })
    const batches = await collect(readPartition(h.admin, 'events_2035_11'))
    assert.deepEqual(batches, [])
  })
})

async function collect<T>(gen: AsyncGenerator<T>): Promise<T[]> {
  const out: T[] = []
  for await (const item of gen) out.push(item)
  return out
}
