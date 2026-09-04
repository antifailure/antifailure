import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { available, setup, type Harness } from '../../../packages/db/test/harness.ts'
import { costAttribution, environmentHoursSince } from '../src/costs.ts'
import { runMaintenance } from '../src/maintenance.ts'
import { FakeClock } from '../src/clock.ts'
import { adminUrl } from './harness.ts'

const has = await available()
const start = '2026-09-01T23:00:00Z'
const end = '2026-09-02T02:00:00Z'
const now = '2026-09-02T04:00:00Z'

describe('usage survives disposable environments', { skip: has ? false : 'no database' }, () => {
  let h: Harness
  const orgs: string[] = []
  before(async () => { h = await setup() })
  after(async () => {
    for (const org of orgs) await h.admin`DELETE FROM organizations WHERE id = ${org}`
    await h.close()
  })
  async function fixture(closed = true) {
    const slug = `usage-${randomUUID()}`
    const [org] = await h.admin<{ id: string }[]>`
      INSERT INTO organizations(slug, name) VALUES (${slug}, 'Usage') RETURNING id`
    const id = org!.id
    orgs.push(id)
    const [repo] = await h.admin<{ id: string }[]>`
      INSERT INTO repositories(org_id, full_name) VALUES (${id}, ${slug + '/app'}) RETURNING id`
    const [env] = await h.admin<{ id: string }[]>`
      INSERT INTO environments(org_id, repository_id, env_id, branch, created_at, torn_down_at, state)
      VALUES (${id}, ${repo!.id}, 'env', 'main', ${start}, ${closed ? end : null},
        ${closed ? 'torn_down' : 'running'}) RETURNING id`
    return { org: id, repo: repo!.id, env: env!.id }
  }
  async function roll(at = now) { await h.admin`SELECT roll_up_environment_usage(${at}::timestamptz)` }
  async function hours(org: string) {
    const rows = await h.admin<{ day: string; hours: number }[]>`
      SELECT day::text, hours::float8 AS hours FROM environment_usage_daily
      WHERE org_id = ${org} ORDER BY day`
    return Array.from(rows)
  }
  const expected = [{ day: '2026-09-01', hours: 1 }, { day: '2026-09-02', hours: 2 }]

  for (const ordering of ['delete then rollup', 'rollup then delete', 'concurrent rollups'] as const) {
    it(ordering, async () => {
      const f = await fixture()
      if (ordering === 'rollup then delete') await roll()
      await h.admin`DELETE FROM environments WHERE id = ${f.env}`
      if (ordering === 'concurrent rollups') await Promise.all([roll(), roll()])
      else await roll()
      assert.deepEqual(await hours(f.org), expected)
    })
  }
  it('repository cascade retains its completed interval', async () => {
    const f = await fixture()
    await h.admin`DELETE FROM repositories WHERE id = ${f.repo}`
    await roll()
    assert.deepEqual(await hours(f.org), expected)
  })
  it('recreating the same environment name starts a new interval without billing the gap', async () => {
    const f = await fixture()
    await h.admin`DELETE FROM environments WHERE id = ${f.env}`
    await h.admin`INSERT INTO environments(org_id, repository_id, env_id, branch, state, created_at, torn_down_at)
      VALUES (${f.org}, ${f.repo}, 'env', 'main', 'torn_down', '2026-09-03T00:00:00Z', '2026-09-03T01:00:00Z')`
    await roll('2026-09-03T02:00:00Z')
    assert.deepEqual(await hours(f.org), [...expected, { day: '2026-09-03', hours: 1 }])
  })
  it('late earlier creation repairs prior UTC days after a completed rollup', async () => {
    const f = await fixture()
    await roll()
    await h.admin`UPDATE environments SET created_at = '2026-09-01T22:00:00Z' WHERE id = ${f.env}`
    await roll()
    assert.deepEqual(await hours(f.org), [{ day: '2026-09-01', hours: 2 }, expected[1]])
  })
  it('close after rollup replaces the open measurement without double counting', async () => {
    const f = await fixture(false)
    await roll()
    await h.admin`UPDATE environments SET torn_down_at = ${end}, state = 'torn_down' WHERE id = ${f.env}`
    await roll()
    assert.deepEqual(await hours(f.org), expected)
  })
  it('an open environment accrues through maintenance with no close event', async () => {
    const f = await fixture(false)
    await roll('2026-09-02T01:00:00Z')
    await runMaintenance({ adminUrl }, new FakeClock(new Date(now)))
    assert.deepEqual(await hours(f.org), [expected[0], { day: '2026-09-02', hours: 4 }])
  })
  it('an older maintenance call cannot move a newer open measurement backwards', async () => {
    const f = await fixture(false)
    await roll()
    await roll('2026-09-02T01:00:00Z')
    assert.deepEqual(await hours(f.org), [expected[0], { day: '2026-09-02', hours: 4 }])
  })
  it('an older maintenance call still repairs a late earlier creation', async () => {
    const f = await fixture(false)
    await roll()
    await h.admin`UPDATE environments SET created_at = '2026-09-01T22:00:00Z' WHERE id = ${f.env}`
    await roll('2026-09-02T01:00:00Z')
    assert.deepEqual(await hours(f.org), [{ day: '2026-09-01', hours: 2 }, { day: '2026-09-02', hours: 4 }])
  })
  it('a future close remains eligible when its first rollup runs before that close', async () => {
    const f = await fixture()
    await roll('2026-09-02T01:00:00Z')
    await roll()
    assert.deepEqual(await hours(f.org), expected)
  })
  it('a teardown clock behind creation cannot block deletion or create negative credit', async () => {
    const f = await fixture(false)
    await h.admin`UPDATE environments SET torn_down_at = '2026-09-01T22:00:00Z', state = 'torn_down'
      WHERE id = ${f.env}`
    const used = await h.pool.withTenant({ orgId: f.org }, (db) =>
      environmentHoursSince(db, f.org, new Date('2026-09-01T00:00:00Z'), new Date(now)))
    assert.equal(used, 0)
  })
  it('deleting an organization erases intervals, daily history and checkpoints', async () => {
    const f = await fixture()
    await roll()
    await h.admin`DELETE FROM organizations WHERE id = ${f.org}`
    const rows = await h.admin<{ count: number }[]>`
      SELECT (SELECT count(*) FROM environment_usage WHERE org_id = ${f.org})::int
        + (SELECT count(*) FROM environment_usage_daily WHERE org_id = ${f.org})::int
        + (SELECT count(*) FROM usage_rollup_state WHERE org_id = ${f.org})::int AS count`
    assert.equal(rows[0]!.count, 0)
  })
  it('cleanup cannot reset the rolling cost cap', async () => {
    const f = await fixture()
    await h.admin`DELETE FROM environments WHERE id = ${f.env}`
    const used = await h.pool.withTenant({ orgId: f.org }, (db) =>
      environmentHoursSince(db, f.org, new Date('2026-09-01T00:00:00Z'), new Date(now)))
    assert.equal(used, 3)
  })
  it('a future creation cannot cancel another interval with negative hours', async () => {
    const f = await fixture()
    await h.admin`INSERT INTO environments(org_id, repository_id, env_id, branch, created_at)
      VALUES (${f.org}, ${f.repo}, 'future-start', 'main', '2026-09-03T00:00:00Z')`
    const used = await h.pool.withTenant({ orgId: f.org }, (db) =>
      environmentHoursSince(db, f.org, new Date('2026-09-01T00:00:00Z'), new Date(now)))
    assert.equal(used, 3)
  })
  it('attribution still explains consumption after repository removal', async () => {
    const f = await fixture()
    await h.admin`DELETE FROM repositories WHERE id = ${f.repo}`
    const rows = await h.pool.withTenant({ orgId: f.org }, (db) =>
      costAttribution(db, f.org, new Date('2026-09-01T00:00:00Z'), new Date(now)))
    assert.deepEqual(rows.map((row) => ({ hours: row.hours, repository: row.repository })),
      [{ hours: 3, repository: 'Removed repository' }])
  })
  it('a tenant reads only its own daily history', async () => {
    const a = await fixture()
    const b = await fixture()
    await roll()
    const rows = await h.pool.withTenant({ orgId: a.org }, (db) =>
      db.execute<{ org_id: string }>(sql`SELECT DISTINCT org_id FROM environment_usage_daily
        WHERE org_id IN (${a.org}::uuid, ${b.org}::uuid)`))
    assert.deepEqual(rows.map((r) => r.org_id), [a.org])
  })
  it('a tenant temporary table cannot shadow the privileged ledger writer', async () => {
    const f = await fixture()
    const id = randomUUID()
    const rows = await h.pool.withTenant({ orgId: f.org }, async (db) => {
      await db.execute(sql`CREATE TEMP TABLE environment_usage
        (LIKE public.environment_usage INCLUDING ALL) ON COMMIT DROP`)
      await db.execute(sql`INSERT INTO public.environments
        (id, org_id, repository_id, env_id, branch, created_at, torn_down_at, state)
        VALUES (${id}::uuid, ${f.org}::uuid, ${f.repo}::uuid, 'shadow-control', 'main',
          ${start}::timestamptz, ${end}::timestamptz, 'torn_down')`)
      return db.execute<{ retained: number; shadowed: number }>(sql`
        SELECT (SELECT count(*) FROM public.environment_usage WHERE environment_id = ${id}::uuid)::int AS retained,
          (SELECT count(*) FROM pg_temp.environment_usage)::int AS shadowed`)
    })
    assert.deepEqual(Array.from(rows), [{ retained: 1, shadowed: 0 }])
  })
})
