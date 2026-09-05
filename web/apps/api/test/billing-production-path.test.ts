// The path a paying customer actually walks, joined end to end.
//
// WHY THIS FILE EXISTS ALONGSIDE billing.test.ts, WHICH ALREADY HAS SIXTY
// BILLING TESTS. Every hop of this path was already proven, and the path was
// not. billing.test.ts proves that checkout opens a session, that a signed
// delivery is applied, that `subscriptions.current` reports a plan, and that
// `checkQuota` computes the right verdict. Each of those is one hop, asserted
// against its own inputs. None of them asserts that the hops are CONNECTED:
// that the customer checkout created is the customer the delivery names, that
// the plan the delivery writes is the plan the quota gate reads, and that the
// gate which refused a fourth environment a moment ago now allows it.
//
// That gap is the shape of the failure this repository keeps finding in itself.
// A route returning 200 is not evidence that a plan changed, and a plan column
// holding `team` is not evidence that anybody got anything for their money. The
// only assertion that cannot be satisfied by a disconnected half is the one
// that watches the SAME organization be refused, pay, and then be allowed.
//
// So the first test below is a single organization walked through:
//
//   free plan  -> the fourth environment is REFUSED, naming 3 of 3
//   checkout   -> through the tRPC route, against the product's Stripe pack
//   delivery   -> through POST /webhooks/stripe, HMAC signed over raw bytes
//   entitlement-> organizations.plan and the billing_subscriptions row
//   quota      -> the SAME fourth environment is now ALLOWED
//   browser    -> subscriptions.current reports team, carrying no secret
//
// The rest of the file is the arrival orderings, driven through the two real
// entry points rather than by calling the handler directly. billing.test.ts and
// billing-ordering.test.ts already prove the orderings against
// `handleStripeDelivery`. That is the right level for the locking arguments and
// the wrong level for this question, because in production nothing calls that
// function: Stripe posts signed bytes at an HTTP route, and a person clicks a
// button that reaches a tRPC mutation. An ordering that is safe in the handler
// and unsafe through the endpoint would pass every existing test.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import {
  available,
  callProcedure,
  dropOrg,
  errorCode,
  seedOrg,
  signInAs,
  startApi,
  stripeAgainstMockPack,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'
import { RealStripeClient } from '../src/billing/stripe.ts'
import type { Billing } from '../src/billing/index.ts'
import type { StripeConfig } from '../src/billing/plans.ts'

const hasDatabase = await available()

// HMAC-SHA256 over "timestamp.body", written out rather than imported from the
// verifier for the reason billing.test.ts gives: a test that builds its input
// with the code under test passes just as well when both sides are wrong.
function signed(secret: string, body: string, at: Date): string {
  const t = Math.floor(at.getTime() / 1000)
  return `t=${t},v1=${createHmac('sha256', secret).update(`${t}.${body}`, 'utf8').digest('hex')}`
}

function envelope(type: string, object: Record<string, unknown>, created: Date, id?: string): string {
  return JSON.stringify({
    id: id ?? `evt_${randomUUID().replaceAll('-', '')}`,
    object: 'event',
    api_version: '2024-06-20',
    created: Math.floor(created.getTime() / 1000),
    livemode: false,
    type,
    pending_webhooks: 0,
    request: { id: null, idempotency_key: null },
    data: { object },
  })
}

function subscriptionObject(over: {
  id: string
  customer: string
  status?: string
  priceId?: string
  /** When STRIPE created it, which is what decides between two subscriptions
   *  that disagree. Not the arrival time and not the local ingest clock. */
  created?: number
}): Record<string, unknown> {
  return {
    id: over.id,
    object: 'subscription',
    customer: over.customer,
    created: over.created ?? 1767225600,
    status: over.status ?? 'active',
    current_period_start: 1767225600,
    current_period_end: 1769904000,
    cancel_at_period_end: false,
    canceled_at: null,
    items: {
      object: 'list',
      has_more: false,
      data: [{
        id: 'si_test',
        object: 'subscription_item',
        quantity: 1,
        price: { id: over.priceId ?? 'price_team_afmock', object: 'price' },
      }],
    },
  }
}

describe('the production billing path', { skip: !hasDatabase }, () => {
  let h: ApiHarness
  let config: StripeConfig
  const orgIds: string[] = []

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    config = stripe.config
    h = await startApi({ stripe: stripe.billing })

    // ORPHANED DELIVERIES FROM AN EARLIER RUN ARE CLEARED, and this is not
    // housekeeping, it is a correctness precondition for everything below.
    //
    // The mock pack mints `cus_` ids from a counter that restarts in every node
    // process, so a second run against a database a first run used is handed
    // the same customer id. Dropping an organization cascades the events it
    // owns, but an event still waiting for an organization has a null org_id
    // and survives. `resolvePending` keys on the CUSTOMER id, so the next run
    // to attach that reused id replays the previous run's parked events onto an
    // organization that bought nothing.
    //
    // Measured, not feared: a mutation run left an unresolved Enterprise
    // delivery behind, and the next run's freshly seeded organization was on
    // the `enterprise` plan before it had done anything, which read as a
    // product defect and is a property of the fixture. A real Stripe customer
    // id is globally unique and is attached to exactly one organization, so
    // nothing here weakens what the product is being asked to do.
    await h.admin`DELETE FROM billing_events WHERE org_id IS NULL`
  })

  after(async () => {
    for (const orgId of orgIds) await dropOrg(h.admin, orgId)
    await h.close()
  })

  /**
   * An organization that can reach the quota gate.
   *
   * The App has to be installed and the workflow has to exist, or
   * `environments.create` is refused one gate LATER than the quota and every
   * assertion below would be passing on the wrong refusal.
   */
  async function payingOrg(label: string): Promise<{ org: Org; owner: SignedIn }> {
    const org = await seedOrg(h.admin, label)
    orgIds.push(org.orgId)
    const owner = await signInAs(h, org, 'owner')
    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${Math.floor(Math.random() * 1e9)}, ${org.slug}, 'Organization')`
    h.github.addWorkflow(org.repository, 'antifailure.yml')
    return { org, owner }
  }

  /** Takes the organization to exactly the free plan's three environments.
   *  seedOrg already made one. */
  async function fillFreePlan(org: Org): Promise<void> {
    await h.admin`
      INSERT INTO environments (org_id, repository_id, env_id, branch, state)
      VALUES (${org.orgId}, ${org.repoId}, ${'env-fill-a'}, 'main', 'running'),
             (${org.orgId}, ${org.repoId}, ${'env-fill-b'}, 'main', 'running')`
  }

  function createEnvironment(owner: SignedIn, org: Org) {
    return callProcedure(h, owner, 'environments.create', 'mutation', { repository: org.repository })
  }

  async function planOf(orgId: string): Promise<string> {
    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${orgId}`
    return row!.plan
  }

  async function deliver(body: string, at = h.clock.now()) {
    return h.fetch('/webhooks/stripe', {
      method: 'POST',
      body,
      headers: { 'stripe-signature': signed(config.webhookSecret, body, at) },
    })
  }

  async function customerOf(orgId: string): Promise<string | null> {
    const [row] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${orgId}`
    return row?.stripe_customer_id ?? null
  }

  // -------------------------------------------------------------------------
  // The whole path, on one organization, in order.
  // -------------------------------------------------------------------------

  it('refuses, is paid for, and then allows the same request', async () => {
    const { org, owner } = await payingOrg('paid-path')
    await fillFreePlan(org)

    // 1. THE QUOTA IS EFFECTIVE BEFORE ANYBODY PAYS.
    //
    // Asserted first and on the refusal SENTENCE rather than only on the code,
    // because PRECONDITION_FAILED is also what a missing installation, a frozen
    // organization and a cost cap answer. Without the sentence this test would
    // pass while measuring a completely different gate, and would then "prove"
    // an upgrade that never touched the quota.
    const refused = await createEnvironment(owner, org)
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED', JSON.stringify(refused.body))
    assert.match(
      JSON.stringify(refused.body), /holding 3 of 3 environments on the free plan/,
      'the refusal did not come from the free plan quota',
    )

    // 2. CHECKOUT, through the route a browser reaches, against the product's
    // own Stripe pack rather than a fake written to agree with the client.
    const bought = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      successUrl: 'https://app.test/billing/done',
      cancelUrl: 'https://app.test/billing',
    })
    assert.equal(bought.status, 200, JSON.stringify(bought.body))
    const session = (bought.body as { result: { data: { url: string; sessionId: string } } }).result.data
    assert.match(session.url, /^https:\/\/checkout\.stripe\.com\//)

    // The customer checkout created is the one the delivery will name. Read
    // back rather than assumed: this is the join the rest of the path rests on,
    // and a delivery naming a customer nobody holds is the ordering that has
    // cost this repository money before.
    const recorded = await customerOf(org.orgId)
    assert.ok(recorded?.startsWith('cus_'), `checkout recorded no Stripe customer: ${recorded}`)
    const customer = recorded!

    // Paying has changed NOTHING yet, and that is correct. Stripe has taken the
    // money and this control plane has not been told. Asserted rather than
    // skipped over, because a plan that moved here would mean checkout was
    // entitling people who never completed a payment.
    assert.equal(await planOf(org.orgId), 'free', 'checkout alone moved the plan')

    // 3. THE SIGNED DELIVERY, over the real HTTP endpoint, HMAC over raw bytes.
    const body = envelope(
      'customer.subscription.created',
      subscriptionObject({ id: `sub_${randomUUID()}`, customer }),
      h.clock.now(),
    )
    const res = await deliver(body)
    const answered = await res.text()
    assert.equal(res.status, 200, answered)
    const outcome = JSON.parse(answered) as { handled: boolean; detail: string }
    assert.equal(outcome.handled, true, answered)

    // 4. THE ENTITLEMENT. Both the plan and the row behind it, because a plan
    // column written with no subscription to justify it is a grant, not a sale.
    assert.equal(await planOf(org.orgId), 'team', 'the delivery did not move the plan')
    const [subscription] = await h.admin<{ status: string; plan: string }[]>`
      SELECT status, plan FROM subscriptions WHERE org_id = ${org.orgId}`
    assert.equal(subscription?.status, 'active')
    assert.equal(subscription?.plan, 'team')

    // 5. THE QUOTA, EFFECTIVE. The same call that was refused above, unchanged,
    // by the same person, against the same organization. This is the assertion
    // that a disconnected half cannot satisfy: nothing here re-reads the plan
    // itself, it asks the product for the thing that was being withheld.
    const allowed = await createEnvironment(owner, org)
    assert.equal(
      errorCode(allowed.body), null,
      `paying for team did not lift the free plan quota: ${JSON.stringify(allowed.body)}`,
    )

    // 6. WHAT A SIGNED IN BROWSER IS SHOWN. `subscriptions.current` is what the
    // billing screen renders, so this is the last place the upgrade could be
    // true everywhere and invisible to the customer.
    const shown = await callProcedure(h, owner, 'subscriptions.current', 'query', {})
    assert.equal(shown.status, 200, JSON.stringify(shown.body))
    const data = (shown.body as { result: { data: Record<string, unknown> } }).result.data
    assert.equal(data.configured, true)
    assert.equal(data.plan, 'team', `the billing screen shows ${String(data.plan)}`)
    assert.ok(data.subscription, 'the billing screen shows no subscription after a purchase')

    // And it carries neither credential. Named literals rather than a regular
    // expression over the shape, because the two strings that must never leave
    // this process are the two this configuration actually holds.
    const serialized = JSON.stringify(data)
    assert.ok(!serialized.includes(config.secretKey), 'the API key reached the browser')
    assert.ok(!serialized.includes(config.webhookSecret), 'the webhook secret reached the browser')
  })

  // -------------------------------------------------------------------------
  // The arrival orderings, through the endpoints rather than the handler.
  // -------------------------------------------------------------------------

  it('ordering, delivery before the customer row exists, then resolved by checkout', async () => {
    // THE ORDERING THAT HAS COST THIS REPOSITORY MONEY, forced through both
    // real entry points rather than simulated by calling attachCustomer.
    //
    // `subscriptions.checkout` creates the customer at Stripe over the network
    // and only then opens a transaction to record it. Every instant between
    // those two is one in which Stripe knows a customer this control plane has
    // never heard of, and Stripe can send `customer.subscription.created` in
    // it. The gap is real, it is a network round trip wide, and nothing in the
    // process can shorten it.
    //
    // So the delivery is posted FROM INSIDE the Stripe call: the injected fetch
    // answers POST /v1/customers, reads the id out of the answer, posts a
    // signed delivery for that id at the live HTTP endpoint, and only then
    // returns to checkout. The webhook therefore lands while the route is still
    // between its network call and its write, which is not an interleaving a
    // test can reach by ordering two awaits.
    const { org, owner } = await payingOrg('late-attach')
    await fillFreePlan(org)

    // The SUITE'S OWN configuration is wrapped rather than a second
    // `stripeAgainstMockPack()`. A fresh pack mints its ids from a fresh
    // counter, so a second one hands out the same first `cus_` as the first
    // pack already did, and the two collide on `subscriptions.stripe_customer_id`
    // in the one database. That is a property of the fixture and not of the
    // product, and chasing it as a product defect would waste somebody's day.
    let delivered: { status: number; detail: string } | undefined
    const interleaving: StripeConfig = {
      ...config,
      fetch: async (input, init) => {
        const response = await config.fetch!(input, init)
        const url = new URL(input instanceof Request ? input.url : String(input))
        if (url.pathname === '/v1/customers' && delivered === undefined) {
          const created = (await response.clone().json()) as { id: string }
          const body = envelope(
            'customer.subscription.created',
            subscriptionObject({ id: `sub_${randomUUID()}`, customer: created.id }),
            h.clock.now(),
          )
          // Posted at the ORIGINAL harness, which is a second server object on
          // the same database holding the same webhook secret. That is what a
          // multiple-replica Container App is: the replica that took the
          // checkout is not the replica Stripe posts to.
          const res = await h.fetch('/webhooks/stripe', {
            method: 'POST',
            body,
            headers: { 'stripe-signature': signed(config.webhookSecret, body, h.clock.now()) },
          })
          const answered = (await res.json()) as { detail: string }
          delivered = { status: res.status, detail: answered.detail }
        }
        return response
      },
    }
    const interleaved = await startApi({
      stripe: { config: interleaving, client: new RealStripeClient(interleaving) } satisfies Billing,
    })
    try {
      // Its own FakeGitHub, so the workflow has to be registered here too or
      // `environments.create` below is refused by the workflow gate rather than
      // reaching the quota. That is not a hypothetical: it happened while this
      // test was being written, and the refusal code was identical.
      interleaved.github.addWorkflow(org.repository, 'antifailure.yml')
      const owner2 = await signInAs(interleaved, org, 'owner', 'late')
      const bought = await callProcedure(interleaved, owner2, 'subscriptions.checkout', 'mutation', {
        plan: 'team',
        successUrl: 'https://app.test/billing/done',
        cancelUrl: 'https://app.test/billing',
      })
      assert.equal(bought.status, 200, JSON.stringify(bought.body))

      // The early delivery was ACCEPTED and PARKED. Accepted, because refusing
      // it would make Stripe retry a delivery that is not wrong; parked,
      // because there is no organization to apply it to yet. A 500 here, or a
      // 200 that recorded nothing, are the two ways this becomes a customer who
      // paid and stayed on free.
      assert.ok(delivered, 'the delivery never landed inside the checkout call')
      assert.equal(delivered!.status, 200)
      assert.match(
        delivered!.detail, /no organization holds this customer yet/,
        `the early delivery was not parked: ${delivered!.detail}`,
      )

      // And attaching the customer applied it, with nothing having to sweep.
      assert.equal(await planOf(org.orgId), 'team', 'the parked delivery was never resolved')
      const allowed = await callProcedure(interleaved, owner2, 'environments.create', 'mutation', {
        repository: org.repository,
      })
      assert.equal(errorCode(allowed.body), null, JSON.stringify(allowed.body))
      void owner
    } finally {
      await interleaved.close()
    }
  })

  it('ordering, the same delivery twice, which is what a Stripe retry is', async () => {
    const { org, owner } = await payingOrg('retry')
    await fillFreePlan(org)
    await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team', successUrl: 'https://app.test/d', cancelUrl: 'https://app.test/c',
    })
    const customer = (await customerOf(org.orgId))!

    const body = envelope(
      'customer.subscription.created',
      subscriptionObject({ id: `sub_${randomUUID()}`, customer }),
      h.clock.now(),
      `evt_retry_${randomUUID().replaceAll('-', '')}`,
    )
    const first = await deliver(body)
    assert.equal(first.status, 200)
    const second = await deliver(body)
    const secondBody = await second.text()

    // 200 rather than a conflict. Stripe retries on anything that is not a 2xx,
    // so telling it "already handled" with a 409 would make it retry forever.
    assert.equal(second.status, 200, secondBody)
    const repeat = JSON.parse(secondBody) as { handled: boolean; detail: string }
    assert.equal(repeat.handled, true)
    assert.match(repeat.detail, /already handled/, repeat.detail)

    // One event row for one event id, whatever the delivery count. This is the
    // assertion, not the status: an endpoint that re-applied the event would
    // also answer 200 twice.
    // COUNTED BY ORGANIZATION, not by Stripe customer id, and that is not a
    // stylistic preference. The mock pack mints `cus_` ids from a counter that
    // starts again in every node process, so a second run against the same
    // database reuses the first run's customer and this count climbs by itself.
    // It cost a mutation run: a no-op mutation came back red at 4 against 2.
    // The organization is freshly seeded per run and cannot collide.
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM billing_events WHERE org_id = ${org.orgId}`
    assert.equal(Number(rows[0]!.n), 1, 'a retried delivery was recorded twice')
    assert.equal(await planOf(org.orgId), 'team')
  })

  it('ordering, two deliveries at once that disagree about the plan', async () => {
    // TWO DELIVERIES THAT DISAGREE, and that is the whole design of this test.
    //
    // An earlier draft posted two deliveries carrying the SAME subscription at
    // the same plan. It passed, and it was worthless: the two agreed, so the
    // organization ended on `team` whether the handlers serialized or trampled
    // each other. Breaking the advisory customer lock left it green, and so did
    // breaking the organization row lock in `recomputePlan`. A check that
    // cannot say no is worse than no check.
    //
    // So the two now disagree: an older Team subscription and a newer
    // Enterprise one, posted together. Exactly one answer is correct whichever
    // lands first, because `recomputePlan` decides on the time STRIPE created
    // the subscription rather than on the order the deliveries arrived. That
    // ordering is a production line a mutation can break, and this assertion
    // goes red when it is.
    //
    // What this does NOT prove is the locking. Removing either lock leaves it
    // green, because a lost update needs a real interleaving and asserting on
    // one would be a flaky test. billing-ordering.test.ts proves the lock ORDER
    // directly, which is the right instrument for it; this is not a second one.
    const { org, owner } = await payingOrg('concurrent')
    await fillFreePlan(org)
    await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team', successUrl: 'https://app.test/d', cancelUrl: 'https://app.test/c',
    })
    const customer = (await customerOf(org.orgId))!

    const [a, b] = await Promise.all([
      deliver(envelope(
        'customer.subscription.created',
        subscriptionObject({
          id: `sub_older_${randomUUID()}`, customer, created: 1767225600,
          priceId: 'price_team_afmock',
        }),
        h.clock.now(),
      )),
      deliver(envelope(
        'customer.subscription.created',
        subscriptionObject({
          id: `sub_newer_${randomUUID()}`, customer, created: 1769904000,
          priceId: 'price_enterprise_afmock',
        }),
        h.clock.now(),
      )),
    ])
    assert.equal(a.status, 200, await a.text())
    assert.equal(b.status, 200, await b.text())

    // Both landed. A concurrent delivery that is silently dropped is a customer
    // whose purchase Stripe believes it has already told this control plane
    // about, and will never mention again.
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM billing_events WHERE org_id = ${org.orgId}`
    assert.equal(Number(rows[0]!.n), 2, 'a concurrent delivery was lost')
    const subs = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM subscriptions WHERE org_id = ${org.orgId}`
    assert.equal(Number(subs[0]!.n), 2, 'a concurrent delivery wrote no subscription')

    // And the one Stripe created LATER decides, whichever of the two this
    // process happened to finish first.
    assert.equal(
      await planOf(org.orgId), 'enterprise',
      'the arrival order of two concurrent deliveries decided the plan',
    )

    const allowed = await createEnvironment(owner, org)
    assert.equal(errorCode(allowed.body), null, JSON.stringify(allowed.body))
  })

  it('ordering, the delivery never arrives, and the reachable route recovers it', async () => {
    const { org, owner } = await payingOrg('no-webhook')
    await fillFreePlan(org)
    await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team', successUrl: 'https://app.test/d', cancelUrl: 'https://app.test/c',
    })

    // The customer completed the checkout page, so a subscription EXISTS at
    // Stripe. The pack is Stripe here, and this is the object the delivery
    // would have been about.
    const customer = (await customerOf(org.orgId))!
    const at = await config.fetch!(new URL('/v1/subscriptions', 'https://api.stripe.com'), {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({
        customer,
        'items[0][price]': 'price_team_afmock',
        'items[0][quantity]': '1',
      }).toString(),
    })
    assert.equal(at.status, 200, await at.text())

    // Nothing is delivered at all. This is the endpoint being unreachable, the
    // signing secret being wrong, or the endpoint never having been created in
    // the Stripe dashboard, which is exactly the state this control plane is in
    // until somebody creates it. The customer has paid and holds nothing.
    assert.equal(await planOf(org.orgId), 'free')
    const stillRefused = await createEnvironment(owner, org)
    assert.equal(errorCode(stillRefused.body), 'PRECONDITION_FAILED')

    // The recovery has to be REACHABLE, not merely present. Called through the
    // tRPC route a person can press rather than by importing `reconcile`,
    // because a recovery nothing can invoke is the dead code this repository
    // keeps finding.
    const repaired = await callProcedure(h, owner, 'subscriptions.reconcile', 'mutation', {})
    assert.equal(repaired.status, 200, JSON.stringify(repaired.body))
    assert.equal(await planOf(org.orgId), 'team', 'reconcile did not recover the missed delivery')

    const allowed = await createEnvironment(owner, org)
    assert.equal(errorCode(allowed.body), null, JSON.stringify(allowed.body))
  })
})
