// The gate on the dashboard, which is the one gate on this server that a
// permission cannot express.
//
// Every other route answers about the caller's own organization, so the
// permission is the whole question. This one answers about the installation:
// the acquisition channels, the plan mix, how many organizations activated.
// Every organization has an owner, and an owner holds every permission inside
// their own organization, so a permission alone would hand the whole funnel to
// every customer who signs up.
//
// The permission matrix in permissions.test.ts proves analytics.read is
// enforced. What it cannot prove is the second half, because the fixture there
// is never the operator organization. That is what this file is for, and the
// case that matters is the one in the middle: an OWNER of a different
// organization, holding every permission there is, refused.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { available, callProcedure, dropOrg, errorCode, seedOrg, signInAs, startApi, type ApiHarness, type Org } from './harness.ts'
import { rollUp } from '../src/analytics/rollup.ts'

const hasDatabase = await available()

describe(
  'who may read the analytics dashboard',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    let operator: Org
    let customer: Org

    before(async () => {
      // The harness needs the slug before it can be configured with it, and the
      // slug is randomised per run so two suites cannot collide. So the
      // organization is seeded on a throwaway server and the real one is
      // started pointed at it.
      const seeding = await startApi()
      operator = await seedOrg(seeding.admin, 'operator')
      customer = await seedOrg(seeding.admin, 'customer')
      await seeding.close()

      h = await startApi({ analyticsOperatorOrgSlug: operator.slug })
    })

    after(async () => {
      await dropOrg(h.admin, operator.orgId)
      await dropOrg(h.admin, customer.orgId)
      await h.close()
    })

    it('lets an owner of the operator organization read it', async () => {
      const session = await signInAs(h, operator, 'owner')
      const res = await callProcedure(h, session, 'analytics.overview', 'query', { days: 28 })
      assert.equal(res.status, 200, JSON.stringify(res.body))
      const body = res.body as { result: { data: { provenance: { recording: boolean } } } }
      assert.equal(body.result.data.provenance.recording, true)
    })

    it('refuses the OWNER of another organization, who holds every permission in their own', async () => {
      // The case the permission matrix cannot see, and the reason this file
      // exists. A permission check alone answers yes here.
      const session = await signInAs(h, customer, 'owner')
      const res = await callProcedure(h, session, 'analytics.overview', 'query', { days: 28 })
      assert.equal(errorCode(res.body), 'FORBIDDEN')
    })

    it('does not name the operator organization when it refuses', async () => {
      // Telling a customer which organization operates the installation is a
      // fact about somebody else, and the useful next step for them is nothing.
      const session = await signInAs(h, customer, 'owner')
      const res = await callProcedure(h, session, 'analytics.catalog', 'query', {})
      const message = JSON.stringify(res.body)
      assert.ok(!message.includes(operator.slug), 'the refusal named the operator organization')
    })

    it('refuses a member of the operator organization, who lacks the permission', async () => {
      const session = await signInAs(h, operator, 'member')
      const res = await callProcedure(h, session, 'analytics.overview', 'query', { days: 28 })
      assert.equal(errorCode(res.body), 'FORBIDDEN')
    })

    it('refuses everyone, and says which variable to set, when none is configured', async () => {
      // The default. A dashboard that renders zeros because it is switched off
      // is indistinguishable from one that renders zeros because nobody came,
      // so the refusal names the variable rather than the page being empty.
      const unset = await startApi()
      try {
        const org = await seedOrg(unset.admin, 'unconfigured')
        const session = await signInAs(unset, org, 'owner')
        const res = await callProcedure(unset, session, 'analytics.overview', 'query', { days: 28 })
        assert.equal(errorCode(res.body), 'PRECONDITION_FAILED')
        assert.match(JSON.stringify(res.body), /AF_ANALYTICS_OPERATOR_ORG/)
        await dropOrg(unset.admin, org.orgId)
      } finally {
        await unset.close()
      }
    })

    it('says recording is off when no surrogate secret is configured', async () => {
      // Not an error. A control plane with analytics switched off still serves
      // the page, and the page says why every number on it is zero.
      const off = await startApi({
        analyticsSecret: null,
        analyticsOperatorOrgSlug: operator.slug,
      })
      try {
        const session = await signInAs(off, operator, 'owner')
        const res = await callProcedure(off, session, 'analytics.overview', 'query', { days: 28 })
        assert.equal(res.status, 200, JSON.stringify(res.body))
        const body = res.body as { result: { data: { provenance: { recording: boolean } } } }
        assert.equal(body.result.data.provenance.recording, false)
      } finally {
        await off.close()
      }
    })

    it('refuses an event name the catalog does not hold, rather than querying for it', async () => {
      const session = await signInAs(h, operator, 'owner')
      const res = await callProcedure(h, session, 'analytics.series', 'query', {
        days: 7,
        name: 'site.page_viewed; DROP TABLE analytics_daily',
      })
      assert.equal(errorCode(res.body), 'BAD_REQUEST')
    })

    it('answers with where the numbers came from, on every shape it returns', async () => {
      // A NUMBER WITH NO SOURCE DOES NOT GO ON THE PAGE, checked at the API so
      // the page has no way to draw a chart without the sentence beside it.
      const session = await signInAs(h, operator, 'owner')
      for (const [path, input] of [
        ['analytics.overview', { days: 7 }],
        ['analytics.catalog', {}],
        ['analytics.series', { days: 7, name: 'site.page_viewed' }],
      ] as const) {
        const res = await callProcedure(h, session, path, 'query', input)
        const body = res.body as { result?: { data?: { provenance?: Record<string, unknown> } } }
        const p = body.result?.data?.provenance
        assert.ok(p, `${path} answered with no provenance`)
        for (const field of ['from', 'to', 'windowDays', 'recording']) {
          assert.ok(field in p, `${path} provenance has no ${field}`)
        }
        // Present and possibly null, which is the point: null means the rollup
        // has never run, and the page says something different for it.
        assert.ok('lastRolledUpAt' in p, `${path} cannot tell empty from never computed`)
      }
    })

    it('refuses a beacon whose clock is wrong, in both directions, and keeps the rest', async () => {
      // A machine with a wrong clock is common, and an event dated next year
      // sorts to the top of every chart forever. The same bound ingest.ts puts
      // on an engine, for the same reason, and it is a per-event rejection
      // rather than a refused batch: a tab that was asleep flushes hours of
      // real events alongside one bad one.
      const day = 24 * 60 * 60 * 1000
      const now = h.clock.now().getTime()
      const send = (events: unknown[]) =>
        h.fetch('/v1/site/events', {
          method: 'POST',
          headers: { 'content-type': 'application/json', origin: 'https://www.test' },
          body: JSON.stringify({ events }),
        })

      // A fresh identifier per run, and this is not decoration. The identifier
      // was derived from the clock, and the harness clock starts at a fixed
      // instant, so the second run of this file against the same database
      // collided with the first: the good event came back as a duplicate and
      // the assertion below read 0 recorded. It passed alone and failed in the
      // suite, which is the only place that difference shows.
      const run = randomUUID()
      const one = (at: number) => ({
        id: `skew-${run}-${at}`,
        name: 'site.page_viewed',
        at: new Date(at).toISOString(),
        session: 'a-browsing-session-identifier',
        payload: { route: 'home', source: 'direct', entry: true },
      })

      const res = await send([one(now + 2 * day), one(now), one(now - 2 * day)])
      assert.equal(res.status, 207, 'a batch with refusals in it did not report as partial')
      const body = (await res.json()) as { recorded: number; rejected: number }
      assert.equal(body.rejected, 2, 'a clock two days out in either direction was accepted')
      assert.equal(body.recorded, 1, 'the good event in the batch was discarded with the bad ones')
    })

    it('accepts the body sendBeacon can actually send, which is not JSON typed', async () => {
      // WHY THIS IS NOT A DETAIL. The queue flushes on the way out of the page
      // through navigator.sendBeacon, because a fetch started during pagehide
      // is cancelled by some browsers. sendBeacon cannot answer a CORS
      // preflight, and application/json is not a safelisted content type, so a
      // JSON typed beacon would force a preflight and the request would simply
      // never be made. There is no error to see: the events vanish.
      //
      // So the beacon sends text/plain and the control plane parses the body
      // regardless of what the header says. That is a real contract between two
      // files in two npm projects, and this is the only place it is checked.
      const res = await h.fetch('/v1/site/events', {
        method: 'POST',
        headers: { 'content-type': 'text/plain;charset=UTF-8', origin: 'https://www.test' },
        body: JSON.stringify({
          events: [
            {
              id: `unload-${randomUUID()}`,
              name: 'site.page_viewed',
              at: h.clock.now().toISOString(),
              session: 'a-browsing-session-identifier',
              payload: { route: 'pricing', source: 'direct', entry: true },
            },
          ],
        }),
      })
      assert.equal(res.status, 202, 'the unload flush would be dropped for its content type')
      const body = (await res.json()) as { recorded: number }
      assert.equal(body.recorded, 1)
    })

    it('refuses a beacon from any origin but the configured one', async () => {
      const res = await h.fetch('/v1/site/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', origin: 'https://someone-else.test' },
        body: JSON.stringify({ events: [] }),
      })
      assert.equal(res.status, 403)

      // And with no Origin at all. A browser would refuse the response anyway;
      // a non-browser caller would not, and this is the line that bounds it.
      const bare = await h.fetch('/v1/site/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ events: [] }),
      })
      assert.equal(bare.status, 403)
    })

    it('reports every catalog event, including the ones nothing has ever emitted', async () => {
      const session = await signInAs(h, operator, 'owner')
      const res = await callProcedure(h, session, 'analytics.catalog', 'query', {})
      const body = res.body as {
        result: {
          data: {
            total: number
            events: { name: string; everRecorded: number; producer: string }[]
            funnels: { funnel: string; derivedFromFacts: string | null }[]
          }
        }
      }
      const data = body.result.data
      assert.equal(data.events.length, data.total)
      // An event with nothing recorded is reported as zero rather than being
      // left out, because a row missing from a table is invisible and a zero is
      // a finding.
      for (const e of data.events) {
        assert.equal(typeof e.everRecorded, 'number')
        assert.ok(e.producer.length > 0, `${e.name} is listed with no producer`)
      }
      assert.ok(
        data.funnels.some((f) => f.derivedFromFacts !== null),
        'no funnel says why it has no event of its own, so two of them read as oversights',
      )
    })
  },
)


// ---------------------------------------------------------------------------

/**
 * The beacon and the rollup are two events in a system, and the bug is never in
 * the state, it is in the ORDER.
 *
 * Every case below is one cell of a table: the beacon arriving before the day
 * is rolled up, after it, twice, twice across a rollup, never, and a rollup
 * with nothing to roll. The suite that existed asserted the states, one at a
 * time, all reached by the same ordering, which is the shape of test suite that
 * passes for months and then meets a real browser.
 *
 * THE CELL THAT MATTERS MOST is the retry. The client queue resends a batch the
 * server may already have accepted, because a request that timed out and a
 * request that failed are indistinguishable from a browser. If that
 * re-delivery counted a second time, every reader on a flaky connection would
 * inflate the numbers by an unknown factor, silently, in the direction that
 * looks like growth.
 */
describe(
  'the orderings a beacon and a rollup can arrive in',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness

    before(async () => {
      h = await startApi()
      // ONTO A DAY NO OTHER SUITE IS ON.
      //
      // FakeClock starts every harness in this repository at the same instant,
      // and analytics_daily is keyed by day, name and the two dimensions. So
      // the suite above, which posts a page view of its own, lands rows on the
      // same day as these and every count here read its events as well as its
      // own. It looked like a rollup bug and was two suites sharing a date.
      h.clock.advance(400 * 24 * 60 * 60 * 1000)
    })

    after(async () => {
      const day = today()
      await h.admin`
        DELETE FROM analytics_events
        WHERE occurred_at >= ${day}::date AND occurred_at < (${day}::date + interval '2 days')`
      await h.admin`DELETE FROM analytics_daily WHERE day >= ${day}::date`
      await h.close()
    })

    /** One posted beacon, through the route a browser posts to. */
    const send = (events: unknown[]) =>
      h.fetch('/v1/site/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', origin: 'https://www.test' },
        body: JSON.stringify({ events }),
      })

    interface Outcome {
      recorded: number
      duplicates: number
      rejected: number
      failed: number
    }

    /**
     * A batch with stable ids, because a retry is only a retry if it carries
     * the SAME ids. An event is stamped once, at the moment it happens, and
     * that is the whole reason a re-delivery is safe: it collides on the
     * primary key rather than adding a row. A fixture that generated fresh ids
     * on the second call would prove nothing and would pass.
     */
    function batch(run: string, count: number, at: Date, route = 'home'): unknown[] {
      return Array.from({ length: count }, (_, i) => ({
        id: `${run}-${i}`,
        name: 'site.page_viewed',
        at: at.toISOString(),
        session: `session-${run}`,
        payload: { route, source: 'direct', entry: i === 0 },
      }))
    }

    /** What analytics_daily holds for one day and one route, which is what the
     *  dashboard reads. Zero when the rollup has written nothing for it, which
     *  is a different answer from "no row", and the difference is the point of
     *  two of the cases below.
     *
     *  The route is dim_B. site.page_viewed rolls up under ['source', 'route'],
     *  in that order, and reading dim_a here returned zero for every case
     *  regardless of what the rollup had done, which is the shape of fixture
     *  bug that makes a whole ordering table look broken. */
    async function counted(day: string, route: string): Promise<number> {
      const rows = await h.admin<{ events: string }[]>`
        SELECT sum(events)::text AS events FROM analytics_daily
        WHERE day = ${day}::date AND name = 'site.page_viewed' AND dim_b = ${route}`
      return Number(rows[0]?.events ?? 0)
    }

    /** The rollup, over the day the harness clock is on. */
    const roll = () => rollUp(h.admin, h.clock, { lookbackDays: 1 })

    const today = () => h.clock.now().toISOString().slice(0, 10)

    it('event then write: the ordinary path, and the control on every case below', async () => {
      const run = randomUUID()
      const res = await send(batch(run, 3, h.clock.now(), 'blog'))
      assert.equal(res.status, 202)
      assert.deepEqual(await res.json(), { recorded: 3, duplicates: 0, rejected: 0, failed: 0 })

      await roll()
      assert.equal(await counted(today(), 'blog'), 3)
    })

    it('write then event: a beacon for a day already rolled up is absorbed by the next pass', async () => {
      // The queue holds events for three seconds and the unload flush can land
      // later still, so an event arriving after its day was computed is the
      // common case rather than the exotic one. A rollup that only ever added
      // forward would leave it out of the numbers permanently.
      const run = randomUUID()
      await send(batch(run, 1, h.clock.now(), 'pricing'))
      await roll()
      assert.equal(await counted(today(), 'pricing'), 1)

      await send(batch(`${run}-late`, 2, h.clock.now(), 'pricing'))
      // Before the second pass the count is still the old one, which is what
      // makes the provenance line on the dashboard necessary rather than
      // decorative.
      assert.equal(await counted(today(), 'pricing'), 1)

      await roll()
      assert.equal(await counted(today(), 'pricing'), 3, 'the late beacon was never counted')
    })

    it('event with no write: the events are held and the aggregate says nothing yet', async () => {
      // The failure this rules out is a rollup that has not run reading as a
      // measurement of zero. The rows are in the stream; the aggregate has no
      // row for them; those are different states and the dashboard renders
      // them differently.
      const run = randomUUID()
      await send(batch(run, 2, h.clock.now(), 'solutions'))
      assert.equal(await counted(today(), 'solutions'), 0)

      const raw = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM analytics_events
        WHERE session_surrogate IS NOT NULL AND payload->>'route' = 'solutions'`
      assert.ok(Number(raw[0]?.n ?? 0) >= 2, 'the events were not held pending a rollup')

      await roll()
      assert.equal(await counted(today(), 'solutions'), 2)
    })

    it('write with no event: a rollup over a day nothing happened on writes no row', async () => {
      await roll()
      assert.equal(await counted(today(), 'signup'), 0)
      const rows = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM analytics_daily
        WHERE day = ${today()}::date AND name = 'site.page_viewed' AND dim_b = 'signup'`
      assert.equal(Number(rows[0]?.n ?? 0), 0, 'a day with no events invented a zero row')
    })

    it('the retry: the same batch delivered twice is counted once', async () => {
      // THE CELL THE WHOLE FILE IS FOR. A browser cannot tell a request that
      // timed out from one that failed, so the queue resends. The second
      // delivery has to be reported as a duplicate and has to leave the
      // aggregate alone.
      const run = randomUUID()
      const events = batch(run, 4, h.clock.now(), 'product')

      const first = (await (await send(events)).json()) as Outcome
      assert.deepEqual(first, { recorded: 4, duplicates: 0, rejected: 0, failed: 0 })

      const again = await send(events)
      assert.equal(again.status, 202, 'a duplicate is not a refusal, and 207 would say it was')
      const second = (await again.json()) as Outcome
      assert.deepEqual(
        second,
        { recorded: 0, duplicates: 4, rejected: 0, failed: 0 },
        'a re-delivered batch was counted as new events',
      )

      await roll()
      assert.equal(await counted(today(), 'product'), 4, 'the retry double counted')
    })

    it('the retry across a rollup: re-delivery after the day was computed still counts once', async () => {
      // The harder half of the case above. The first delivery has already been
      // rolled up, so the duplicate is refused against a row the aggregate has
      // already read. A recompute that added the duplicate, or that recounted
      // the original alongside it, shows up here and nowhere else.
      const run = randomUUID()
      const events = batch(run, 3, h.clock.now(), 'legal')

      await send(events)
      await roll()
      assert.equal(await counted(today(), 'legal'), 3)

      const second = (await (await send(events)).json()) as Outcome
      assert.equal(second.duplicates, 3)
      assert.equal(second.recorded, 0)

      await roll()
      assert.equal(await counted(today(), 'legal'), 3, 'a retry after a rollup moved the count')
    })

    it('a beacon flushed after its session ended is still recorded, under the session it happened in', async () => {
      // The client ends a session after thirty minutes idle and starts a new
      // one. The server holds no session state at all, which is deliberate:
      // the identifier is the browser's and a server that decided when it
      // expired would be a server that tracked it. So the property to hold is
      // that a late flush carrying the OLD identifier is recorded rather than
      // refused, and recorded under that identifier rather than reassigned.
      const run = randomUUID()
      const at = h.clock.now()
      await send(batch(run, 1, at, 'home'))

      // Well past the client's idle timeout, and well inside the skew the
      // envelope allows. The event is stamped when it HAPPENED, not when it
      // was sent, which is what makes both of those true at once.
      const late = [
        {
          id: `${run}-stranded`,
          name: 'site.page_viewed',
          at: new Date(at.getTime() + 45 * 60 * 1000).toISOString(),
          session: `session-${run}`,
          payload: { route: 'home', source: 'internal', entry: false },
        },
      ]
      const res = await send(late)
      assert.equal(res.status, 202)
      assert.deepEqual(await res.json(), { recorded: 1, duplicates: 0, rejected: 0, failed: 0 })

      const rows = await h.admin<{ n: string }[]>`
        SELECT count(DISTINCT session_surrogate)::text AS n FROM analytics_events
        WHERE event_id IN (${`${run}-0`}, ${`${run}-stranded`})`
      assert.equal(
        Number(rows[0]?.n ?? 0),
        1,
        'the late flush was filed under a different session from the one it happened in',
      )
    })

    it('concurrent: three rollups at once, one does the work and the count is right', async () => {
      // THE ORDERING THAT FOUND A REAL DEFECT. Every replica runs the
      // maintenance pass, and runs it once immediately on start, so on a
      // deployment with two replicas two rollups begin within milliseconds of
      // each other on every deploy. recomputeDay is a DELETE and an INSERT in
      // one transaction, which is right for one writer and a race for two: the
      // second insert failed on the primary key and took the whole maintenance
      // pass down with it. The dashboard would then stop updating while a line
      // went into a log.
      //
      // Three rather than two, because the lock has to hold under more than
      // the one contender the fix was written against.
      const run = randomUUID()
      await send(batch(run, 5, h.clock.now(), 'other'))

      const results = await Promise.all([roll(), roll(), roll()])
      const ran = results.filter((r) => r.ran)
      assert.equal(ran.length, 1, 'more than one rollup did the work at the same time')
      assert.equal(await counted(today(), 'other'), 5, 'concurrent rollups moved the count')

      // And the ones that skipped said so, rather than reporting an empty day.
      for (const skipped of results.filter((r) => !r.ran)) {
        assert.deepEqual(skipped.days, [])
        assert.equal(skipped.rows, 0)
      }
    })

    it('and the lock is released, so the next pass rolls up rather than skipping forever', async () => {
      // The failure a try-lock invites: taken on a reserved connection and
      // never given back, so every rollup after the first silently does
      // nothing and the dashboard freezes at whatever it last said. That is a
      // worse outcome than the crash it replaced, because nothing reports it.
      const after = await roll()
      assert.equal(after.ran, true, 'the rollup lock was never released')
      assert.equal(await counted(today(), 'other'), 5)
    })
  },
)
