// Two people pressing Subscribe at the same moment.
//
// Checkout is the one mutation here that creates state in two systems that
// cannot share a transaction: a customer at Stripe and a row in this database.
// Concurrency across that boundary was never tested, and the failure it hides
// is permanent rather than untidy. An organization with two Stripe customers
// has its payments, its invoices and its subscription split across two objects
// that both look real in the dashboard, and no webhook can put them back
// together, because each delivery is correct about the customer it names.
//
// Two owners on a call, one link pasted into a channel, or one impatient double
// click are all this takes, and none of them is unusual on the day a company
// decides to buy.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import {
  available,
  startApi,
  seedOrg,
  signInAs,
  callProcedure,
  dropOrg,
  stripeAgainstMockPack,
  type ApiHarness,
} from './harness.ts'
import { RealStripeClient } from '../src/billing/stripe.ts'

describe('two checkouts at the same instant', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const created: string[] = []
  /** Every form body that reached Stripe, by path. */
  let posted: { path: string; body: URLSearchParams }[] = []

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    const underneath = stripe.config.fetch!
    h = await startApi({
      stripe: {
        config: stripe.config,
        client: new RealStripeClient({
          ...stripe.config,
          fetch: async (input, init) => {
            const url = new URL(input instanceof Request ? input.url : String(input))
            if (init?.method === 'POST' && typeof init.body === 'string') {
              posted.push({ path: url.pathname, body: new URLSearchParams(init.body) })
            }
            return underneath(input, init)
          },
        }),
      },
    })
  })
  after(async () => {
    for (const orgId of created) await dropOrg(h.admin, orgId)
    await h.close()
  })

  async function freshOrg(label: string) {
    const org = await seedOrg(h.admin, label)
    created.push(org.orgId)
    return org
  }

  const buy = { plan: 'enterprise', seats: 3, successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no' }

  it('creates one customer for the organization, not one per request', async () => {
    posted = []
    const org = await freshOrg('race-new')
    const owner = await signInAs(h, org, 'owner')

    // Genuinely in flight together. Awaiting one and then the other tests the
    // sequence that already worked and says nothing about the race.
    const [a, b] = await Promise.all([
      callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy),
      callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy),
    ])
    assert.equal(a.status, 200, `first checkout: ${JSON.stringify(a.body)}`)
    assert.equal(b.status, 200, `second checkout: ${JSON.stringify(b.body)}`)

    const rows = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.equal(rows.length, 1, 'the organization ended up with more than one customer row')
    const customerId = rows[0]!.stripe_customer_id

    // The assertion that actually matters. Both checkout sessions have to name
    // the SAME customer: a session opened against the customer that lost the
    // race would take a real payment onto an object this database does not
    // know about, and the subscription webhook for it would arrive naming a
    // customer no organization claims.
    const sessions = posted.filter((p) => p.path === '/v1/checkout/sessions')
    assert.equal(sessions.length, 2, `expected two checkout sessions, saw ${sessions.length}`)
    for (const session of sessions) {
      assert.equal(
        session.body.get('customer'),
        customerId,
        'a checkout session was opened against a customer this organization is not attached to',
      )
    }
  })

  it('refuses the second of two when the first has already been paid', async () => {
    // The guard that exists is a read followed by a refusal, and a read cannot
    // see a subscription that does not exist yet. What it does cover is the
    // ordinary case: once a live subscription is recorded, no further checkout
    // opens. This is the cell of the table where the webhook wins the race.
    const org = await freshOrg('race-paid')
    const owner = await signInAs(h, org, 'owner')

    const first = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy)
    assert.equal(first.status, 200)

    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    // Written directly, and that is honest here in a way it would not be in a
    // recovery test. Nothing below claims anything about how a subscription
    // comes to exist; this is the precondition, and the assertion is about the
    // checkout guard reading it. A test that hand-wrote the row and then said
    // reconciliation had recovered it would be proving its own setup.
    await h.admin`
      INSERT INTO subscriptions (org_id, stripe_subscription_id, stripe_customer_id, plan, status,
                                 price_id, quantity, last_event_at)
      VALUES (${org.orgId}, 'sub_race_paid', ${customer!.stripe_customer_id}, 'enterprise',
              'active', 'price_enterprise_afmock', 3, now())`

    const second = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy)
    assert.equal(second.status, 412, `a second checkout was allowed: ${JSON.stringify(second.body)}`)
  })

  it('an organization that already has a customer opens both sessions against it', async () => {
    // The other half of the race: the customer exists before either request
    // arrives, so neither creates one and both must reuse it. This is the
    // common shape for a renewal or a plan change after a cancellation, where
    // the customer has been there for months.
    posted = []
    const org = await freshOrg('race-existing')
    const owner = await signInAs(h, org, 'owner')

    const warm = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy)
    assert.equal(warm.status, 200)
    const [existing] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`

    posted = []
    const [a, b] = await Promise.all([
      callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy),
      callProcedure(h, owner, 'subscriptions.checkout', 'mutation', buy),
    ])
    assert.equal(a.status, 200)
    assert.equal(b.status, 200)

    assert.equal(
      posted.filter((p) => p.path === '/v1/customers').length,
      0,
      'a customer was created for an organization that already had one',
    )
    for (const session of posted.filter((p) => p.path === '/v1/checkout/sessions')) {
      assert.equal(session.body.get('customer'), existing!.stripe_customer_id)
    }
  })
})
