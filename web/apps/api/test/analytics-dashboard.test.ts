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

    it('lets an admin of the operator organization read it', async () => {
      const session = await signInAs(h, operator, 'admin')
      const res = await callProcedure(h, session, 'analytics.overview', 'query', { days: 28 })
      assert.equal(res.status, 200, JSON.stringify(res.body))
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

    it('refuses a viewer of the operator organization, who lacks the permission', async () => {
      const session = await signInAs(h, operator, 'viewer')
      const res = await callProcedure(h, session, 'analytics.overview', 'query', { days: 28 })
      assert.equal(errorCode(res.body), 'FORBIDDEN')
    })

    it('tells the console whether THIS organization operates the installation', async () => {
      // One of the two fields the shared console helper reads. The other is
      // role, because only owners and admins hold analytics.read.
      //
      // /analytics sat in every customer's sidebar because the console gated
      // it on analytics.read, which owners and admins of EVERY organization
      // hold. So a customer clicked an installation-wide dashboard and met a
      // refusal written for somebody else, and the console had no field that
      // could have told it otherwise: the slug is deliberately not sent, since
      // naming the operator to every tenant is a fact about somebody else.
      const asOperator = await signInAs(h, operator, 'owner')
      const mine = await h.fetch('/auth/session', { headers: { cookie: asOperator.cookie } })
      const mineBody = (await mine.json()) as { role: string; analyticsOperator?: boolean }
      assert.equal(mineBody.role, 'owner')
      assert.equal(mineBody.analyticsOperator, true)

      // The positive control above is what makes this line mean something: an
      // owner of another organization holds the same permission and the same
      // role, and differs only in the installation relationship this field
      // reports.
      const asCustomer = await signInAs(h, customer, 'owner')
      const theirs = await h.fetch('/auth/session', { headers: { cookie: asCustomer.cookie } })
      const theirsBody = (await theirs.json()) as { role: string; analyticsOperator?: boolean }
      assert.equal(theirsBody.role, 'owner')
      assert.equal(theirsBody.analyticsOperator, false)

      // And the slug itself never leaves the control plane.
      assert.ok(
        !JSON.stringify(theirsBody).includes(operator.slug),
        'the session named the operating organization to a tenant',
      )
    })

    it('reports no analytics operator at all when the variable is unset', async () => {
      // The state every self-hosted installation starts in, and the one this
      // repository's own production plane was in: with nothing configured the
      // entry must be hidden from EVERYBODY, including the operator, because
      // the page behind it can only refuse.
      const unset = await startApi()
      try {
        const org = await seedOrg(unset.admin, 'noopsorg')
        const session = await signInAs(unset, org, 'owner')
        const res = await unset.fetch('/auth/session', { headers: { cookie: session.cookie } })
        const body = (await res.json()) as { analyticsOperator?: boolean }
        assert.equal(body.analyticsOperator, false)
        await dropOrg(unset.admin, org.orgId)
      } finally {
        await unset.close()
      }
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
