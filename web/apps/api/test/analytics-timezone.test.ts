import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { available, adminUrl, startApi, type ApiHarness } from './harness.ts'
import { FakeClock } from '../src/clock.ts'
import { rollUp } from '../src/analytics/rollup.ts'
import { CATALOG } from '../src/analytics/catalog.ts'

const hasDatabase = await available()

describe('analytics uses UTC boundaries on a local-time database', { skip: !hasDatabase }, () => {
  let h: ApiHarness
  let local: postgres.Sql
  before(async () => {
    h = await startApi()
    local = postgres(adminUrl, { max: 1, connection: { TimeZone: 'America/Chicago' }, onnotice: () => {} })
  })
  after(async () => {
    await local.end()
    await h.close()
  })

  for (const day of ['2040-03-12', '2040-11-05']) {
    describe(`the Monday after daylight saving changes, ${day}`, () => {
      before(async () => {
        const subject = randomUUID().replaceAll('-', '')
        for (const [name, minute, payload] of [
          ['site.page_viewed', '00', { route: 'home', source: 'search', entry: true }],
          ['site.cta_engaged', '01', { cta: 'waitlist_open', route: 'home' }],
          ['site.lead_submitted', '02', { source: 'search', landing: 'home', outcome: 'recorded' }],
        ] as const) {
          const spec = CATALOG[name]
          await local`
            INSERT INTO analytics_events (event_id, name, version, occurred_at, source,
              session_surrogate, actor_kind, privacy_basis, payload)
            VALUES (${randomUUID()}, ${name}, ${spec.version}, ${`${day}T00:${minute}:00Z`}::timestamptz,
              ${spec.source}, ${subject}, ${spec.actorKind}, ${spec.privacyBasis}, ${local.json(payload)})`
        }
        await rollUp(local, new FakeClock(`${day}T23:00:00Z`), { lookbackDays: 1 })
      })
      after(async () => {
        await local`DELETE FROM analytics_events WHERE occurred_at >= ${`${day}T00:00:00Z`}::timestamptz AND occurred_at < ${`${day}T23:59:59Z`}::timestamptz`
        await local`DELETE FROM analytics_daily WHERE day = ${day}::date`
        await local`DELETE FROM analytics_subject_days WHERE day = ${day}::date`
        await local`DELETE FROM analytics_actives WHERE day = ${day}::date`
        await local`DELETE FROM analytics_funnel_weeks WHERE entered_week = ${day}::date`
      })
      it('counts midnight events on their UTC day', async () => {
        const [row] = await local`SELECT sum(events)::int AS n FROM analytics_daily WHERE day = ${day}::date AND name = 'site.page_viewed'`
        assert.equal(row!.n, 1)
      })
      it('includes the midnight subject in the active working set', async () => {
        const [row] = await local`SELECT count(*)::int AS n FROM analytics_subject_days WHERE day = ${day}::date AND name = 'site.page_viewed'`
        assert.equal(row!.n, 1)
      })
      it('assigns a completed midnight funnel to its UTC entry week', async () => {
        const [row] = await local`SELECT sum(subjects)::int AS n FROM analytics_funnel_weeks WHERE entered_week = ${day}::date AND funnel = 'acquisition' AND steps_completed = 3`
        assert.equal(row!.n, 1)
      })
      it('restores the caller connection timezone after aggregation', async () => {
        const [row] = await local`SHOW TimeZone`
        assert.equal(row!.TimeZone, 'America/Chicago')
      })
    })
  }
})
