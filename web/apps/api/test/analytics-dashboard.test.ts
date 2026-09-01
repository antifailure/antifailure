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
