// Partition maintenance, as the process actually runs it.
//
// The manager itself is tested in the db package against every ordering that
// matters. What is left here is the part that only exists in this process: that
// a pass opens a privileged connection, does the work, and closes it again;
// that a failing pass does not stop the schedule; and that a retention read
// from the environment is either a number of months or a refusal to start.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { mkdtemp, readFile, readdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { archivePartition, retentionFromEnv, runMaintenance, startMaintenance } from '../src/maintenance.ts'
import { FakeClock } from '../src/clock.ts'
import { adminUrl, available, seedOrg, startApi, type ApiHarness } from './harness.ts'

const has = await available()

describe('the retention read from the environment', () => {
  it('is absent when nothing asked for one, which keeps everything', () => {
    assert.equal(retentionFromEnv({}), undefined)
    assert.equal(retentionFromEnv({ AF_EVENT_RETENTION_MONTHS: '' }), undefined)
  })

  it('is a whole number of months when one was asked for', () => {
    assert.equal(retentionFromEnv({ AF_EVENT_RETENTION_MONTHS: '6' }), 6)
  })

  it('refuses a value that is not one, rather than keeping everything quietly', () => {
    // The failure this prevents: a typo means no retention, the table grows
    // for a year, and nobody finds out until the disk does.
    for (const bad of ['0', '-1', '3.5', 'six', 'true']) {
      assert.throws(
        () => retentionFromEnv({ AF_EVENT_RETENTION_MONTHS: bad }),
        /whole number of months/,
        `${bad} was accepted`,
      )
    }
  })
})

describe('a maintenance pass', { skip: has ? false : 'no database' }, () => {
  let h: ApiHarness

  before(async () => {
    h = await startApi()
  })
  after(async () => {
    await h.close()
  })

  it('creates the months ahead and closes the connection it used', async () => {
    const clock = new FakeClock('2037-02-11T00:00:00Z')
    const before_ = await connectionCount(h)

    const run = await runMaintenance({ adminUrl, monthsAhead: 2 }, clock)
    // Both partitioned tables, because a pass keeps analytics_events ahead of
    // its writes exactly as it does events: see the comment in maintenance.ts.
    // Still the exact list rather than a length or a subset, so a month that
    // stops being created on either table fails here.
    assert.deepEqual(run.created, [
      'events_2037_02', 'events_2037_03', 'events_2037_04',
      'analytics_events_2037_02', 'analytics_events_2037_03', 'analytics_events_2037_04',
    ])
    assert.deepEqual(run.dropped, [])
    assert.equal(run.pruned, 0, 'pruning happened with no retention asked for')

    // The privileged connection is not left open between passes. Polled
    // because the close is asynchronous on the server's side.
    await settles(async () => (await connectionCount(h)) <= before_)
  })

  it('drops and prunes only once a retention has been asked for', async () => {
    const org = await seedOrg(h.admin, 'maint')
    const clock = new FakeClock('2038-01-06T00:00:00Z')
    await runMaintenance({ adminUrl, monthsAhead: 0 }, clock)

    await h.admin`
      INSERT INTO events (org_id, idempotency_key, env_id, type, occurred_at, sequence)
      VALUES (${org.orgId}, ${randomUUID()}, ${org.envId}, 'environment.ready',
              ${new Date('2038-01-06T00:00:00Z')}, 1)`
    // A late arrival, so it lands in the default partition rather than a month.
    await h.admin`
      INSERT INTO events (org_id, idempotency_key, env_id, type, occurred_at, sequence)
      VALUES (${org.orgId}, ${randomUUID()}, ${org.envId}, 'environment.ready',
              ${new Date('2036-05-01T00:00:00Z')}, 1)`

    const kept = await runMaintenance({ adminUrl, monthsAhead: 0 }, new FakeClock('2038-09-01T00:00:00Z'))
    assert.deepEqual(kept.dropped, [], 'a month was dropped with no retention set')
    assert.equal(kept.pruned, 0, 'the default was pruned with no retention set')

    const swept = await runMaintenance(
      { adminUrl, monthsAhead: 0, retentionMonths: 3 },
      new FakeClock('2038-09-01T00:00:00Z'),
    )
    assert.ok(swept.dropped.includes('events_2038_01'), `expected the month dropped, got ${swept.dropped}`)
    assert.ok(swept.pruned >= 1, 'the late arrival was not pruned')
  })

  it('writes a month out before dropping it, and drops nothing if that fails', async () => {
    // The one irreversible thing this job does. A month deleted with no copy
    // anywhere cannot be undone, so a write that did not finish has to cost a
    // retention run and not the events.
    const org = await seedOrg(h.admin, 'archive')
    const dir = await mkdtemp(path.join(tmpdir(), 'af-archive-'))
    const when = new Date('2039-03-04T00:00:00Z')
    await runMaintenance({ adminUrl, monthsAhead: 0 }, new FakeClock(when.toISOString()))
    for (let i = 0; i < 3; i++) {
      await h.admin`
        INSERT INTO events (org_id, idempotency_key, env_id, type, occurred_at, sequence)
        VALUES (${org.orgId}, ${randomUUID()}, ${org.envId}, 'environment.ready', ${when}, ${i})`
    }

    // A directory that cannot be written to. The month must survive.
    const errors: unknown[] = []
    const blocked = await runMaintenance(
      {
        adminUrl,
        monthsAhead: 0,
        retentionMonths: 3,
        archiveDir: '/dev/null/not-a-directory',
        onError: (err) => errors.push(err),
      },
      new FakeClock('2039-11-01T00:00:00Z'),
    )
    assert.deepEqual(blocked.dropped, [], 'a month was dropped after the archive failed')
    assert.equal(errors.length, 1, 'the archive failure was not reported')

    const [still] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events WHERE org_id = ${org.orgId}`
    assert.equal(Number(still?.n), 3, 'events went missing after a failed archive')

    // And with somewhere to write, the file lands complete and then the month
    // goes.
    const run = await runMaintenance(
      { adminUrl, monthsAhead: 0, retentionMonths: 3, archiveDir: dir },
      new FakeClock('2039-11-01T00:00:00Z'),
    )
    assert.ok(run.archived.includes('events_2039_03'), `expected an archive, got ${run.archived}`)
    assert.ok(run.dropped.includes('events_2039_03'), `expected the drop, got ${run.dropped}`)

    // No .partial left behind: a half-written file that looks finished is
    // worse than none, because the drop that follows trusts it.
    const files = await readdir(dir)
    assert.ok(files.includes('events_2039_03.jsonl'), `no archive written: ${files.join(', ')}`)
    assert.deepEqual(
      files.filter((f) => f.endsWith('.partial')),
      [],
      'a half-written archive was left behind looking like a finished one',
    )

    const lines = (await readFile(path.join(dir, 'events_2039_03.jsonl'), 'utf8'))
      .trimEnd()
      .split('\n')
    assert.equal(lines.length, 3, 'the archive does not hold every row')
    const first = JSON.parse(lines[0]!)
    assert.equal(first.org_id, org.orgId)
    assert.match(first.occurred_at, /^2039-03-04T/)

    const [gone] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events WHERE org_id = ${org.orgId}`
    assert.equal(Number(gone?.n), 0)
  })

  it('archives an empty month as an empty file rather than no file', async () => {
    // So that a month with nothing in it is distinguishable from a month
    // nobody archived.
    const dir = await mkdtemp(path.join(tmpdir(), 'af-archive-'))
    await runMaintenance({ adminUrl, monthsAhead: 0 }, new FakeClock('2040-06-02T00:00:00Z'))
    const file = await archivePartition(h.admin, 'events_2040_06', dir)
    assert.equal(await readFile(file, 'utf8'), '')
  })

  it('keeps running after a pass that fails', async () => {
    // The failure mode this guards: the schedule gives up on a transient
    // error, months stop being created, and the first thing anybody notices is
    // inserts failing because there is no partition to put them in.
    const errors: unknown[] = []
    const handle = startMaintenance(
      {
        adminUrl: 'postgres://nobody:nobody@127.0.0.1:1/nothing',
        intervalMs: 20,
        onError: (err) => errors.push(err),
      },
      new FakeClock(),
    )
    await settles(async () => errors.length >= 2)
    handle.stop()
    assert.ok(errors.length >= 2, 'the schedule stopped after the first failure')
  })
})

async function connectionCount(h: ApiHarness): Promise<number> {
  const [row] = await h.admin<{ n: string }[]>`
    SELECT count(*) AS n FROM pg_stat_activity WHERE datname = current_database()`
  return Number(row?.n ?? 0)
}

/** Polls a condition rather than sleeping a guessed duration. */
async function settles(cond: () => Promise<boolean>, timeoutMs = 5_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (await cond()) return
    if (Date.now() > deadline) throw new Error('condition did not settle in time')
    await new Promise((r) => setTimeout(r, 25))
  }
}
