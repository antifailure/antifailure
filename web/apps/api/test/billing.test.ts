// Taking money, against the engine's own Stripe mock pack.
//
// Nothing here reaches the network, and there is no Stripe account behind any
// of it. The client under test is RealStripeClient, the one that ships, with
// its fetch pointed at `engine/internal/mockpack` running the pack the product
// distributes. The webhook events are produced with the same signing scheme the
// engine's simulator uses, and a separate test proves this verifier accepts
// events the engine actually signed.
//
// THE ORDERINGS ARE THE POINT. A test suite that exercises one arrival order
// has not tested an event-driven flow, and this repository has already shipped
// a bug of exactly that shape: a payout webhook fired into nothing because a
// second signup path created the row later, and every test passed because every
// test used one ordering. So each ordering below is a test with the ordering in
// its name:
//
//   created then paid            the ordinary purchase
//   the same event twice         a Stripe retry
//   deleted then updated         out of order, the late one must change nothing
//   webhook then customer        the event that arrived before the row existed
//   customer then no webhook     the delivery that never came, fixed by reconciling
//   cancel while an upgrade is in flight
//   downgrade while over the lower plan's seat limit
//
// The last three are the ones a happy-path suite never reaches. The downgrade
// was added with the change that made a plan's seat number the only one there
// is: checkout used to sell a per unit quantity that entitled nothing, so a
// downgrade is now the only way an organization ends up holding more members
// than its plan allows, and what happens then had never been asserted.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { sql } from 'drizzle-orm'
import type { Billing } from '../src/billing/index.ts'
import {
  handleStripeDelivery,
  parseStripeEvent,
  verifyStripeSignature,
  SIGNATURE_TOLERANCE_SECONDS,
  type StripeEvent,
} from '../src/billing/webhook.ts'
import { PLANS, planForPrice, planForStatus, stripeConfigFrom } from '../src/billing/plans.ts'
import { RealStripeClient } from '../src/billing/stripe.ts'
import {
  available, startApi, seedOrg, signInAs, callProcedure, errorCode, dropOrg,
  stripeAgainstMockPack, type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

const here = path.dirname(fileURLToPath(import.meta.url))
const webhookVectorPath = path.join(here, '..', '..', '..', '..', 'schemas', 'webhook-vectors.json')

// ---------------------------------------------------------------------------
// Signing, the way the engine's simulator signs.
//
// HMAC-SHA256 over "timestamp.body". Written out here rather than imported,
// because importing the verifier's own arithmetic to build its input would be
// the verifier checking itself: it would pass just as well if both sides signed
// the wrong bytes. The corpus test below is what ties this to the engine.
// ---------------------------------------------------------------------------

function signed(secret: string, body: string, at: Date): string {
  const t = Math.floor(at.getTime() / 1000)
  const mac = createHmac('sha256', secret).update(`${t}.${body}`, 'utf8').digest('hex')
  return `t=${t},v1=${mac}`
}

/** One Stripe event envelope, in the shape Stripe sends. */
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

function eventOf(raw: string): StripeEvent {
  const parsed = parseStripeEvent(raw)
  assert.ok(parsed, 'the fixture is not a Stripe event')
  return parsed
}

/** A subscription object as Stripe sends it, with the plan on its first item. */
function subscriptionObject(over: {
  id: string
  customer: string
  status?: string
  priceId?: string
  quantity?: number
  cancelAtPeriodEnd?: boolean
  canceledAt?: number | null
}): Record<string, unknown> {
  return {
    id: over.id,
    object: 'subscription',
    customer: over.customer,
    status: over.status ?? 'active',
    current_period_start: 1767225600,
    current_period_end: 1769904000,
    cancel_at_period_end: over.cancelAtPeriodEnd ?? false,
    canceled_at: over.canceledAt ?? null,
    items: {
      object: 'list',
      has_more: false,
      data: [
        {
          id: 'si_test',
          object: 'subscription_item',
          quantity: over.quantity ?? 1,
          price: { id: over.priceId ?? 'price_team_afmock', object: 'price' },
        },
      ],
    },
  }
}

function invoiceObject(over: {
  id: string
  customer: string
  status?: string
  subscription?: string | null
  amountPaid?: number
}): Record<string, unknown> {
  return {
    id: over.id,
    object: 'invoice',
    customer: over.customer,
    subscription: over.subscription ?? null,
    number: 'AF-0001',
    status: over.status ?? 'paid',
    amount_due: 4900,
    amount_paid: over.amountPaid ?? 4900,
    currency: 'usd',
    hosted_invoice_url: 'https://invoice.stripe.com/i/afmock',
    period_start: 1767225600,
    period_end: 1769904000,
  }
}

// ---------------------------------------------------------------------------
// The parts that need no database
// ---------------------------------------------------------------------------

describe('the signature check, which is what everything else rests on', () => {
  const secret = 'whsec_a_secret_only_stripe_and_this_process_hold'
  const body = '{"id":"evt_1","type":"invoice.paid"}'
  const at = new Date('2026-08-30T12:00:00Z')

  it('accepts a signature over these exact bytes', () => {
    assert.equal(verifyStripeSignature(secret, body, signed(secret, body, at), at), null)
  })

  it('refuses a body that was changed after it was signed', () => {
    const header = signed(secret, body, at)
    assert.equal(
      verifyStripeSignature(secret, `${body} `, header, at),
      'the signature does not match',
    )
  })

  it('refuses a signature made with another secret', () => {
    const header = signed('whsec_somebody_elses', body, at)
    assert.equal(verifyStripeSignature(secret, body, header, at), 'the signature does not match')
  })

  it('refuses an unsigned delivery', () => {
    assert.equal(verifyStripeSignature(secret, body, undefined, at), 'no signature header')
    assert.equal(verifyStripeSignature(secret, body, '', at), 'no signature header')
    assert.equal(verifyStripeSignature(secret, body, 'nonsense', at), 'the signature header is malformed')
    assert.equal(verifyStripeSignature(secret, body, 't=123', at), 'the signature header is malformed')
  })

  it('refuses a stale signature, so a captured delivery cannot be replayed', () => {
    // The reason the timestamp is inside the signature at all. The delivery
    // worth replaying is invoice.paid.
    const old = new Date(at.getTime() - (SIGNATURE_TOLERANCE_SECONDS + 1) * 1000)
    const header = signed(secret, body, old)
    assert.equal(verifyStripeSignature(secret, body, header, at), 'the timestamp is outside the tolerance')

    // And one just inside it still works, so the check is a window rather than
    // a coincidence.
    const recent = new Date(at.getTime() - (SIGNATURE_TOLERANCE_SECONDS - 1) * 1000)
    assert.equal(verifyStripeSignature(secret, body, signed(secret, body, recent), at), null)
  })

  it('refuses a signature dated in the future', () => {
    // A check that only looked backwards would accept a header dated next year,
    // which is a replay with the clock turned the other way.
    const ahead = new Date(at.getTime() + (SIGNATURE_TOLERANCE_SECONDS + 1) * 1000)
    assert.equal(
      verifyStripeSignature(secret, body, signed(secret, body, ahead), at),
      'the timestamp is outside the tolerance',
    )
  })

  it('accepts a header carrying two signatures, which is what a secret rotation sends', () => {
    const mine = signed(secret, body, at)
    const theirs = signed('whsec_the_other_one', body, at)
    const both = `${mine},${theirs.slice(theirs.indexOf('v1='))}`
    assert.equal(verifyStripeSignature(secret, body, both, at), null)
    // And the other way round, so this does not pass by reading only the first.
    const reversed = `${theirs},${mine.slice(mine.indexOf('v1='))}`
    assert.equal(verifyStripeSignature(secret, body, reversed, at), null)
  })

  it('accepts events the engine’s own webhook simulator signed', async () => {
    // The claim `engine/internal/webhook` exists to make: an application that
    // verifies signatures accepts what it produces. Until now that was only
    // ever checked against the signer's own Verify, which is the signer
    // checking its own arithmetic.
    const corpus = JSON.parse(await readFile(webhookVectorPath, 'utf8')) as {
      events: { why: string; type: string; secret: string; signed_at: number; body: string; headers: Record<string, string> }[]
    }
    assert.ok(corpus.events.length >= 5, `only ${corpus.events.length} events in the corpus`)

    for (const event of corpus.events) {
      const at2 = new Date(event.signed_at * 1000)
      assert.equal(
        verifyStripeSignature(event.secret, event.body, event.headers['Stripe-Signature'], at2),
        null,
        `${event.type}: the engine signed it and this verifier refused it`,
      )
      // The envelope has to parse too. A signature that verifies over a body
      // this control plane cannot read is a delivery that 400s forever.
      const parsed = parseStripeEvent(event.body)
      assert.ok(parsed, `${event.type}: verified but did not parse`)
      assert.equal(parsed.type, event.type)
    }

    // Every event in the corpus carries a distinct id, because a handler that
    // is idempotent on the event id drops the second of two events that share
    // one. The engine used to number them by the second.
    const ids = corpus.events.map((e) => (JSON.parse(e.body) as { id: string }).id)
    assert.equal(new Set(ids).size, ids.length, 'two events the engine signed share an id')
  })
})

describe('the envelope', () => {
  it('refuses a body that is not a Stripe event, so the endpoint answers 400 and Stripe stops', () => {
    assert.equal(parseStripeEvent('not json'), null)
    assert.equal(parseStripeEvent('[]'), null)
    assert.equal(parseStripeEvent('{"id":"evt_1"}'), null, 'no type')
    assert.equal(parseStripeEvent('{"id":"evt_1","type":"invoice.paid"}'), null, 'no created')
    assert.equal(
      parseStripeEvent('{"id":"evt_1","type":"invoice.paid","created":1,"data":{"object":[]}}'),
      null,
      'the object is a list',
    )
  })

  it('reads the envelope Stripe sends', () => {
    const event = eventOf(envelope('invoice.paid', { id: 'in_1' }, new Date('2026-08-30T12:00:00Z')))
    assert.equal(event.type, 'invoice.paid')
    assert.equal(event.created.toISOString(), '2026-08-30T12:00:00.000Z')
    assert.equal(event.object.id, 'in_1')
  })
})

describe('the plan a price sells', () => {
  const config = {
    secretKey: 'sk', webhookSecret: 'wh',
    prices: { team: 'price_team', enterprise: 'price_ent' },
  }

  it('maps a configured price to its plan', () => {
    assert.equal(planForPrice(config, 'price_team'), 'team')
    assert.equal(planForPrice(config, 'price_ent'), 'enterprise')
  })

  it('answers null for a price nobody configured, rather than falling back to free', () => {
    // Somebody who bought through a link nobody here configured has paid.
    // Entitling them to the free plan would take away capacity they just
    // bought, which is the wrong direction to be wrong in.
    assert.equal(planForPrice(config, 'price_from_a_link'), null)
    assert.equal(planForPrice(config, null), null)
  })

  it('keeps the plan while Stripe is still retrying a failed payment', () => {
    // The dunning decision. Cutting somebody off at past_due takes their
    // environments away on the day their card expired, hours before the retry
    // that was going to succeed.
    assert.equal(planForStatus('past_due', 'team'), 'team')
    assert.equal(planForStatus('active', 'team'), 'team')
    assert.equal(planForStatus('trialing', 'team'), 'team')
  })

  it('drops the plan when Stripe has given up', () => {
    assert.equal(planForStatus('canceled', 'team'), 'free')
    assert.equal(planForStatus('unpaid', 'team'), 'free')
    assert.equal(planForStatus('incomplete_expired', 'team'), 'free')
  })

  it('changes nothing for a status it has never heard of', () => {
    assert.equal(planForStatus('a_status_stripe_added_later', 'team'), null)
  })
})

describe('the configuration', () => {
  it('is off when nothing is set, which is the self-hosted default', () => {
    const { config, summary } = stripeConfigFrom({})
    assert.equal(config, null)
    assert.match(summary, /billing is off/)
  })

  it('is off and says so loudly when it is half set', () => {
    // The dangerous state: an operator who set three of four believes billing
    // works, and the one they missed is usually the webhook secret, which fails
    // only when a real customer pays.
    const { config, summary } = stripeConfigFrom({
      AF_STRIPE_SECRET_KEY: 'sk_test',
      AF_STRIPE_PRICE_TEAM: 'price_1',
      AF_STRIPE_PRICE_ENTERPRISE: 'price_2',
    })
    assert.equal(config, null)
    assert.match(summary, /partially configured/)
    assert.match(summary, /AF_STRIPE_WEBHOOK_SECRET/)
  })

  it('is on when all of it is set', () => {
    const { config } = stripeConfigFrom({
      AF_STRIPE_SECRET_KEY: 'sk_test',
      AF_STRIPE_WEBHOOK_SECRET: 'whsec_test',
      AF_STRIPE_PRICE_TEAM: 'price_1',
      AF_STRIPE_PRICE_ENTERPRISE: 'price_2',
    })
    assert.ok(config)
    assert.equal(config.prices.team, 'price_1')
  })

  // -------------------------------------------------------------------------
  // A plan with no price, which is a supported state and used to be an outage.
  //
  // AF_STRIPE_PRICE_ENTERPRISE was the fourth REQUIRED variable, so a
  // deployment that set the secret key, the webhook secret and the Team price
  // and nothing else landed in the "partially configured" branch: config came
  // back null, billing was entirely OFF, and the Team price that does exist
  // could not be sold either. That is the exact shape this product sells in.
  // Team has a recurring price somebody can buy on their own; Enterprise is
  // arranged with a person and has no price at all.
  //
  // Measured before the change, by calling this function: secret + webhook +
  // TEAM gave `config: null` and "billing is OFF and partially configured:
  // AF_STRIPE_PRICE_ENTERPRISE not set".
  // -------------------------------------------------------------------------

  it('is ON with no Enterprise price, because Enterprise is arranged with a person', () => {
    const { config, summary } = stripeConfigFrom({
      AF_STRIPE_SECRET_KEY: 'sk_test',
      AF_STRIPE_WEBHOOK_SECRET: 'whsec_test',
      AF_STRIPE_PRICE_TEAM: 'price_1',
    })
    assert.ok(
      config,
      'billing is off with no Enterprise price, so the Team price that exists cannot be sold either',
    )
    assert.equal(config.prices.team, 'price_1')
    // Absent, not empty. An empty string would compare equal to another empty
    // string in planForPrice and hand somebody the largest plan.
    assert.equal(config.prices.enterprise, undefined)
    // The startup line has to name both halves, or the first Enterprise
    // refusal reads like an outage to whoever is on call.
    assert.match(summary, /billing is on/)
    assert.match(summary, /team sold self-serve/)
    assert.match(summary, /enterprise has no price/)
  })

  it('is still OFF when a genuinely required variable is missing', () => {
    // The relaxation above must not have relaxed the other three. Each one is
    // dropped on its own, because a check that only ever tests the whole set
    // cannot tell which member it is actually enforcing.
    for (const dropped of [
      'AF_STRIPE_SECRET_KEY',
      'AF_STRIPE_WEBHOOK_SECRET',
      'AF_STRIPE_PRICE_TEAM',
    ]) {
      const env: Record<string, string> = {
        AF_STRIPE_SECRET_KEY: 'sk_test',
        AF_STRIPE_WEBHOOK_SECRET: 'whsec_test',
        AF_STRIPE_PRICE_TEAM: 'price_1',
      }
      delete env[dropped]
      const { config, summary } = stripeConfigFrom(env)
      assert.equal(config, null, `billing came on without ${dropped}`)
      assert.match(summary, /partially configured/)
      assert.match(summary, new RegExp(dropped))
    }
  })

  it('calls an installation that set only the Enterprise price half configured, not untouched', () => {
    // "Nothing set at all" is the self-hosted default and is fine. It is
    // decided by the required list plus the optional one, so setting only the
    // optional variable is not mistaken for having set nothing.
    const { config, summary } = stripeConfigFrom({ AF_STRIPE_PRICE_ENTERPRISE: 'price_2' })
    assert.equal(config, null)
    assert.match(summary, /partially configured/)
  })

  it('a plan with no price matches no subscription, whatever Stripe sent', () => {
    // The one that would be silent. `config.prices.enterprise` is undefined, and
    // a subscription with no items has a null price id: an unguarded lookup
    // compares undefined against undefined, matches, and moves somebody onto
    // the largest plan for nothing.
    const { config } = stripeConfigFrom({
      AF_STRIPE_SECRET_KEY: 'sk_test',
      AF_STRIPE_WEBHOOK_SECRET: 'whsec_test',
      AF_STRIPE_PRICE_TEAM: 'price_1',
    })
    assert.ok(config)
    assert.equal(planForPrice(config, null), null)
    assert.equal(planForPrice(config, ''), null)
    assert.equal(planForPrice(config, 'price_nobody_configured'), null)
    // And the plan that DOES have a price still resolves, so this is not
    // passing by refusing everything.
    assert.equal(planForPrice(config, 'price_1'), 'team')
  })
})

describe('the subscription collection boundary', () => {
  it('keeps valid subscriptions when another element is malformed or belongs to another customer', async () => {
    const requested: string[] = []
    const client = new RealStripeClient({
      secretKey: 'sk_test',
      webhookSecret: 'whsec_test',
      prices: { team: 'price_team', enterprise: 'price_enterprise' },
      fetch: async (input) => {
        requested.push(input instanceof Request ? input.url : String(input))
        return new Response(JSON.stringify({
          object: 'list',
          has_more: false,
          data: [
            subscriptionObject({ id: 'sub_good', customer: 'cus_right' }),
            null,
            { object: 'subscription', customer: 'cus_right' },
            subscriptionObject({ id: 'sub_other', customer: 'cus_wrong' }),
          ],
        }), { status: 200, headers: { 'content-type': 'application/json' } })
      },
    })

    const subscriptions = await client.listSubscriptions('cus_right', 50)
    assert.deepEqual(subscriptions.map((subscription) => subscription.id), ['sub_good'])
    const url = new URL(requested[0]!)
    assert.equal(url.searchParams.get('customer'), 'cus_right')
    assert.equal(url.searchParams.get('status'), 'all')
    assert.equal(url.searchParams.get('limit'), '50')
  })
})

// ---------------------------------------------------------------------------
// Everything that needs a database
// ---------------------------------------------------------------------------

describe('billing', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let billing: Billing
  let org: Org
  let owner: SignedIn
  let viewer: SignedIn
  const sentToStripe: { path: string; body: string }[] = []

  /** The most recent request to a path, or undefined. */
  function lastSent(path: string): { path: string; body: string } | undefined {
    return [...sentToStripe].reverse().find((r) => r.path === path)
  }

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    // Every request body that reaches Stripe, so a test can assert about a
    // parameter that is supposed to be ABSENT. RealStripeClient reads
    // config.fetch at call time, so wrapping it after the client is built is
    // enough; the pack still answers, so nothing else in the suite changes.
    const pack = stripe.config.fetch!
    stripe.config.fetch = async (input, init) => {
      const url = new URL(input instanceof Request ? input.url : String(input))
      sentToStripe.push({
        path: url.pathname,
        body: typeof init?.body === 'string' ? init.body : '',
      })
      return pack(input, init)
    }
    billing = stripe.billing
    h = await startApi({ stripe: stripe.billing })
    org = await seedOrg(h.admin, 'billing')
    owner = await signInAs(h, org, 'owner')
    viewer = await signInAs(h, org, 'viewer')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  /** A fresh organization, so one test's subscription cannot decide another's
   *  plan. Every ordering test needs its own. */
  async function freshOrg(label: string): Promise<{ org: Org; customerId: string }> {
    const fresh = await seedOrg(h.admin, label)
    const customerId = `cus_${label}${randomUUID().slice(0, 8).replaceAll('-', '')}`
    return { org: fresh, customerId }
  }

  async function attach(orgId: string, customerId: string): Promise<void> {
    await h.admin`
      INSERT INTO billing_customers (org_id, stripe_customer_id, email)
      VALUES (${orgId}, ${customerId}, 'billing@example.test')`
  }

  async function planOf(orgId: string): Promise<string> {
    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${orgId}`
    return row!.plan
  }

  async function deliver(raw: string) {
    return handleStripeDelivery(h.pool, h.clock, billing.config, eventOf(raw), h.analytics)
  }

  // -------------------------------------------------------------------------
  // The orderings
  // -------------------------------------------------------------------------

  it('ordering: created then paid, which is the ordinary purchase', async () => {
    const { org: o, customerId } = await freshOrg('ordinary')
    await attach(o.orgId, customerId)
    assert.equal(await planOf(o.orgId), 'free')

    const created = h.clock.now()
    const first = await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: 'sub_ordinary', customer: customerId, quantity: 3 }),
        created,
      ),
    )
    assert.equal(first.handled, true, first.detail)
    assert.equal(await planOf(o.orgId), 'team', 'the plan did not move')

    const paid = await deliver(
      envelope(
        'invoice.paid',
        invoiceObject({ id: 'in_ordinary', customer: customerId, subscription: 'sub_ordinary' }),
        new Date(created.getTime() + 1000),
      ),
    )
    assert.equal(paid.handled, true, paid.detail)

    const [row] = await h.admin<{ plan: string; quantity: number; status: string }[]>`
      SELECT plan, quantity, status FROM subscriptions WHERE stripe_subscription_id = 'sub_ordinary'`
    assert.equal(row!.plan, 'team')
    assert.equal(row!.quantity, 3, 'the seat count did not come off the subscription item')
    assert.equal(row!.status, 'active')

    const [invoice] = await h.admin<{ amount_paid: string; paid_at: Date | null }[]>`
      SELECT amount_paid, paid_at FROM invoices WHERE stripe_invoice_id = 'in_ordinary'`
    assert.equal(Number(invoice!.amount_paid), 4900)
    assert.ok(invoice!.paid_at, 'a paid invoice has no paid_at')

    await dropOrg(h.admin, o.orgId)
  })

  it('ordering: the same event twice changes nothing the second time', async () => {
    const { org: o, customerId } = await freshOrg('retry')
    await attach(o.orgId, customerId)

    // One envelope, delivered twice, which is exactly what a Stripe retry is.
    const raw = envelope(
      'customer.subscription.created',
      subscriptionObject({ id: 'sub_retry', customer: customerId, quantity: 5 }),
      h.clock.now(),
      'evt_retry_fixed_id',
    )
    const first = await deliver(raw)
    const second = await deliver(raw)

    assert.equal(first.handled, true, first.detail)
    assert.equal(second.handled, true, second.detail)
    assert.match(second.detail, /already handled/)

    const [count] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM subscriptions WHERE org_id = ${o.orgId}`
    assert.equal(Number(count!.n), 1, 'a retry created a second subscription')

    const [ledger] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM billing_events WHERE stripe_event_id = 'evt_retry_fixed_id'`
    assert.equal(Number(ledger!.n), 1)

    await dropOrg(h.admin, o.orgId)
  })

  it('ordering: deleted then updated, where the late one must change nothing', async () => {
    // The failure this prevents: a subscription that was cancelled comes back,
    // and somebody who stopped paying keeps the product. Stripe promises no
    // ordering and it retries, so this is not a rare case.
    const { org: o, customerId } = await freshOrg('reorder')
    await attach(o.orgId, customerId)

    const earlier = h.clock.now()
    const later = new Date(earlier.getTime() + 60_000)

    await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: 'sub_reorder', customer: customerId }),
        earlier,
      ),
    )
    assert.equal(await planOf(o.orgId), 'team')

    // The cancellation, created later, arrives first.
    const deleted = await deliver(
      envelope(
        'customer.subscription.deleted',
        subscriptionObject({
          id: 'sub_reorder', customer: customerId, status: 'canceled',
          canceledAt: Math.floor(later.getTime() / 1000),
        }),
        later,
      ),
    )
    assert.equal(deleted.handled, true, deleted.detail)
    assert.equal(await planOf(o.orgId), 'free', 'the cancellation did not drop the plan')

    // The update, created in between, arrives after it.
    const stale = await deliver(
      envelope(
        'customer.subscription.updated',
        subscriptionObject({
          id: 'sub_reorder', customer: customerId, status: 'active',
          priceId: 'price_enterprise_afmock',
        }),
        new Date(earlier.getTime() + 30_000),
      ),
    )
    assert.equal(stale.handled, true)
    assert.match(stale.detail, /older event/)
    assert.equal(await planOf(o.orgId), 'free', 'a late event resurrected a cancelled subscription')

    const [row] = await h.admin<{ status: string; plan: string }[]>`
      SELECT status, plan FROM subscriptions WHERE stripe_subscription_id = 'sub_reorder'`
    assert.equal(row!.status, 'canceled')
    assert.equal(row!.plan, 'team', 'the stale event rewrote the plan the row records')

    const [ledger] = await h.admin<{ outcome: string }[]>`
      SELECT outcome FROM billing_events
      WHERE org_id = ${o.orgId} AND type = 'customer.subscription.updated'`
    assert.equal(ledger!.outcome, 'stale', 'the ledger does not record that it was stale')

    await dropOrg(h.admin, o.orgId)
  })

  it('ordering: the webhook arrives before any organization holds the customer', async () => {
    // The ordering this repository has already shipped a bug in. The event is
    // recorded rather than dropped, and applied the moment the customer is
    // attached, rather than waiting for a delivery that has already happened.
    const { org: o, customerId } = await freshOrg('early')

    const outcome = await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: 'sub_early', customer: customerId, quantity: 2 }),
        h.clock.now(),
      ),
    )
    assert.equal(outcome.handled, true, outcome.detail)
    assert.match(outcome.detail, /no organization holds this customer yet/)

    const [pending] = await h.admin<{ outcome: string; org_id: string | null }[]>`
      SELECT outcome, org_id FROM billing_events WHERE stripe_customer_id = ${customerId}`
    assert.equal(pending!.outcome, 'unresolved')
    assert.equal(pending!.org_id, null)
    assert.equal(await planOf(o.orgId), 'free')

    // The organization catches up. Attaching resolves what was waiting.
    const { attachCustomer } = await import('../src/billing/store.ts')
    const attached = await h.pool.withTenant({ orgId: o.orgId }, async (db) =>
      attachCustomer(db, h.clock, billing.config, o.orgId, { id: customerId, email: null }, h.analytics),
    )
    assert.equal(attached.created, true)
    assert.equal(attached.resolved, 1, 'the waiting event was not replayed')

    assert.equal(await planOf(o.orgId), 'team', 'the replayed event did not move the plan')
    const [row] = await h.admin<{ quantity: number }[]>`
      SELECT quantity FROM subscriptions WHERE stripe_subscription_id = 'sub_early'`
    assert.equal(row!.quantity, 2)

    const [resolved] = await h.admin<{ outcome: string; org_id: string }[]>`
      SELECT outcome, org_id FROM billing_events WHERE stripe_customer_id = ${customerId}`
    assert.equal(resolved!.outcome, 'applied')
    assert.equal(resolved!.org_id, o.orgId)

    await dropOrg(h.admin, o.orgId)
  })

  it('ordering: the webhook never arrives, and the reachable reconcile route discovers it', async () => {
    // Nothing in a webhook handler can fix a webhook that was not made. This is
    // the only thing that can, and it has to be reachable by a person rather
    // than only by a sweep nobody can trigger during an incident.
    const { org: o, customerId } = await freshOrg('missed')
    await attach(o.orgId, customerId)

    // A subscription exists at Stripe. The pack is Stripe here.
    const created = await billing.client.createCheckoutSession({
      customerId, priceId: 'price_team_afmock', orgId: o.orgId,
      successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no',
    })
    assert.ok(created.url.endsWith(created.id), 'the checkout url does not name its own session')

    const at = await billing.config.fetch!(
      new URL('/v1/subscriptions', 'https://api.stripe.com'),
      {
        method: 'POST',
        headers: { 'content-type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({
          customer: customerId,
          'items[0][price]': 'price_team_afmock',
          'items[0][quantity]': '4',
        }).toString(),
      },
    )
    const atStripe = (await at.json()) as { id: string }

    // No local subscription row. That is what a genuinely lost creation event
    // leaves behind, and it is the case a reconciler that only reads local ids
    // can never discover.
    assert.equal(await planOf(o.orgId), 'free')

    const session = await signInAs(h, o, 'owner')
    const response = await callProcedure(h, session, 'subscriptions.reconcile', 'mutation', {})
    assert.equal(response.status, 200, JSON.stringify(response.body))
    const result = (response.body as {
      result: { data: { checked: number; changed: number; notes: string[] } }
    }).result.data
    assert.equal(result.checked, 1)
    assert.equal(result.changed >= 1, true, `nothing changed: ${result.notes.join('; ')}`)
    assert.equal(await planOf(o.orgId), 'team', 'reconciling did not fix the missed delivery')

    const [row] = await h.admin<{ quantity: number; status: string }[]>`
      SELECT quantity, status FROM subscriptions WHERE stripe_subscription_id = ${atStripe.id}`
    assert.equal(row!.status, 'active')
    assert.equal(row!.quantity, 4, 'the seat count did not come back from Stripe')

    await dropOrg(h.admin, o.orgId)
  })

  it('ordering: a subscription cancelled while an upgrade is in flight', async () => {
    // Two writers race for one row: the customer portal changed the plan, and
    // the same customer cancelled a second later. Whichever delivery arrives
    // second, the outcome has to be the one Stripe created last.
    const { org: o, customerId } = await freshOrg('inflight')
    await attach(o.orgId, customerId)

    const t0 = h.clock.now()
    await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: 'sub_inflight', customer: customerId }),
        t0,
      ),
    )

    const upgrade = envelope(
      'customer.subscription.updated',
      subscriptionObject({
        id: 'sub_inflight', customer: customerId, priceId: 'price_enterprise_afmock',
      }),
      new Date(t0.getTime() + 10_000),
    )
    const cancel = envelope(
      'customer.subscription.deleted',
      subscriptionObject({
        id: 'sub_inflight', customer: customerId, status: 'canceled',
        priceId: 'price_enterprise_afmock',
        canceledAt: Math.floor((t0.getTime() + 20_000) / 1000),
      }),
      new Date(t0.getTime() + 20_000),
    )

    // Cancel first, upgrade second, which is the order that would go wrong.
    await deliver(cancel)
    await deliver(upgrade)

    assert.equal(await planOf(o.orgId), 'free', 'the in-flight upgrade landed after the cancellation')
    const [row] = await h.admin<{ status: string }[]>`
      SELECT status FROM subscriptions WHERE stripe_subscription_id = 'sub_inflight'`
    assert.equal(row!.status, 'canceled')

    await dropOrg(h.admin, o.orgId)
  })

  it('ordering: a plan downgrade while the organization holds more than the lower plan allows', async () => {
    // The ordering a happy-path suite never reaches, and the one that matters
    // most now that seats come from the plan alone.
    //
    // An organization on `team` holds 50 seats. It downgrades, and the members
    // it already has do not fit. Two things have to be true and they pull in
    // opposite directions: NOTHING IS REMOVED, because taking somebody's
    // colleague off their account over a plan change is not a behaviour this
    // product has, and the NEXT invitation is refused, because the limit is
    // real or it is decoration.
    //
    // This is also the case that made a purchased seat count indefensible. A
    // quantity that entitled seats would have to be re-read on every downgrade
    // and would disagree with the plan for as long as Stripe took to send the
    // update. The plan constant has no such window.
    const { org: o, customerId } = await freshOrg('downgrade')
    await attach(o.orgId, customerId)
    // A fresh subscription id per run. The fixed ids elsewhere in this file
    // rely on ON DELETE CASCADE to clear the row when the organization is
    // dropped, which only happens if the test PASSED: one failure leaves the
    // row behind, and every later run then hits the watermark on a row it
    // cannot see and fails somewhere else entirely.
    const subId = `sub_down_${randomUUID().slice(0, 8)}`

    const t0 = h.clock.now()
    await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: subId, customer: customerId, priceId: 'price_team_afmock' }),
        t0,
      ),
    )
    assert.equal(await planOf(o.orgId), 'team')

    // Six people, which fits inside team's fifty and not inside free's five.
    const owner6 = await signInAs(h, o, 'owner')
    for (let i = 0; i < 5; i += 1) {
      const ok = await callProcedure(h, owner6, 'invitations.create', 'mutation', {
        email: `down-${i}@example.test`, role: 'member',
      })
      assert.equal(errorCode(ok.body), null, `invitation ${i}: ${JSON.stringify(ok.body)}`)
    }
    const heldBefore = await h.admin<{ n: string }[]>`
      SELECT (SELECT count(*) FROM members WHERE org_id = ${o.orgId})
           + (SELECT count(*) FROM invitations
               WHERE org_id = ${o.orgId} AND accepted_at IS NULL AND revoked_at IS NULL) AS n`
    assert.equal(Number(heldBefore[0]!.n), 6, 'the organization is not over the lower plan yet')

    // Stripe says the subscription is gone. The plan drops to free, whose
    // limit is five, and six people are already inside it.
    await deliver(
      envelope(
        'customer.subscription.deleted',
        subscriptionObject({
          id: subId, customer: customerId, status: 'canceled',
          priceId: 'price_team_afmock', canceledAt: Math.floor(t0.getTime() / 1000) + 60,
        }),
        new Date(t0.getTime() + 60_000),
      ),
    )
    assert.equal(await planOf(o.orgId), 'free', 'the downgrade did not land')

    // NOTHING WAS REMOVED.
    const heldAfter = await h.admin<{ n: string }[]>`
      SELECT (SELECT count(*) FROM members WHERE org_id = ${o.orgId})
           + (SELECT count(*) FROM invitations
               WHERE org_id = ${o.orgId} AND accepted_at IS NULL AND revoked_at IS NULL) AS n`
    assert.equal(
      Number(heldAfter[0]!.n), 6,
      'the downgrade removed a member or an invitation. A plan change must never do that.',
    )

    // AND THE NEXT ONE IS REFUSED, naming the plan rather than a number that
    // came from a purchase.
    const refused = await callProcedure(h, owner6, 'invitations.create', 'mutation', {
      email: 'down-over@example.test', role: 'member',
    })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(
      JSON.stringify(refused.body), /6 of 5 seats/,
      'the refusal did not count what the organization is actually holding',
    )
    assert.match(JSON.stringify(refused.body), /the free plan allows/)
    assert.match(JSON.stringify(refused.body), /Nobody was removed/)

    await dropOrg(h.admin, o.orgId)
  })

  it('a failed payment does not by itself take the plan away', async () => {
    // The dunning decision, end to end. Stripe retries and emails while it
    // does; cutting somebody off here takes their environments away hours
    // before the retry that was going to succeed.
    const { org: o, customerId } = await freshOrg('dunning')
    await attach(o.orgId, customerId)
    const t0 = h.clock.now()

    await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: 'sub_dunning', customer: customerId }),
        t0,
      ),
    )
    assert.equal(await planOf(o.orgId), 'team')

    await deliver(
      envelope(
        'invoice.payment_failed',
        invoiceObject({
          id: 'in_dunning', customer: customerId, subscription: 'sub_dunning',
          status: 'open', amountPaid: 0,
        }),
        new Date(t0.getTime() + 1000),
      ),
    )
    assert.equal(await planOf(o.orgId), 'team', 'a failed payment cut somebody off on its own')

    // past_due does not either. Only Stripe giving up does.
    await deliver(
      envelope(
        'customer.subscription.updated',
        subscriptionObject({ id: 'sub_dunning', customer: customerId, status: 'past_due' }),
        new Date(t0.getTime() + 2000),
      ),
    )
    assert.equal(await planOf(o.orgId), 'team', 'past_due cut somebody off')

    await deliver(
      envelope(
        'customer.subscription.updated',
        subscriptionObject({ id: 'sub_dunning', customer: customerId, status: 'unpaid' }),
        new Date(t0.getTime() + 3000),
      ),
    )
    assert.equal(await planOf(o.orgId), 'free', 'Stripe gave up and the plan stayed')

    await dropOrg(h.admin, o.orgId)
  })

  it('a price nobody configured leaves the plan alone rather than dropping it to free', async () => {
    const { org: o, customerId } = await freshOrg('unknownprice')
    await attach(o.orgId, customerId)

    await deliver(
      envelope(
        'customer.subscription.created',
        subscriptionObject({ id: 'sub_unknown', customer: customerId, priceId: 'price_from_a_link' }),
        h.clock.now(),
      ),
    )
    // The row records the subscription; the plan falls back to free because
    // nothing entitles it, and the row says which price it was so a person can
    // work out what happened.
    const [row] = await h.admin<{ price_id: string; status: string }[]>`
      SELECT price_id, status FROM subscriptions WHERE stripe_subscription_id = 'sub_unknown'`
    assert.equal(row!.price_id, 'price_from_a_link')
    assert.equal(row!.status, 'active')

    await dropOrg(h.admin, o.orgId)
  })

  it('creating a customer carries an idempotency key scoped to the organization', async () => {
    // The customer is created at Stripe and the local row is written after it,
    // in a transaction that can fail. Without the key the retry creates a
    // SECOND Stripe customer for the same organization and orphans the first,
    // and an organization billed twice then has two customers that both look
    // real in the dashboard.
    //
    // What is asserted is that the header is SENT. That Stripe returns the
    // first customer for a repeated key is Stripe's behaviour, the mock pack
    // does not implement it, and it cannot be proven here without an account.
    const { org: o } = await freshOrg('idem')
    const session = await signInAs(h, o, 'owner')

    const seen: { path: string; key: string | null }[] = []
    const watched = await stripeAgainstMockPack()
    const underneath = watched.config.fetch!
    const spy = await startApi({
      stripe: {
        config: watched.config,
        client: new (await import('../src/billing/stripe.ts')).RealStripeClient({
          ...watched.config,
          fetch: async (input, init) => {
            const url = new URL(input instanceof Request ? input.url : String(input))
            const headers = new Headers(init?.headers)
            seen.push({ path: url.pathname, key: headers.get('idempotency-key') })
            return underneath(input, init)
          },
        }),
      },
    })
    const spyOrg = await seedOrg(spy.admin, 'idemspy')
    const spySession = await signInAs(spy, spyOrg, 'owner')

    const { status } = await callProcedure(spy, spySession, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no',
    })
    assert.equal(status, 200)

    const customerCall = seen.find((c) => c.path === '/v1/customers')
    assert.ok(customerCall, 'no customer was created')
    assert.equal(customerCall.key, `af-customer-${spyOrg.orgId}`)

    // And deliberately not on the checkout session: Stripe returns the same
    // session for a repeated key, so an organization that cancelled and came
    // back would be sent to a stale expired page forever.
    const sessionCall = seen.find((c) => c.path === '/v1/checkout/sessions')
    assert.ok(sessionCall, 'no checkout session was opened')
    assert.equal(sessionCall.key, null)

    await dropOrg(spy.admin, spyOrg.orgId)
    await spy.close()
    await dropOrg(h.admin, o.orgId)
    void session
  })

  it('a payment method is recorded as metadata and never as a card', async () => {
    const { org: o, customerId } = await freshOrg('card')
    await attach(o.orgId, customerId)

    await deliver(
      envelope(
        'payment_method.attached',
        {
          id: 'pm_card', object: 'payment_method', type: 'card', customer: customerId,
          card: { brand: 'visa', last4: '4242', exp_month: 12, exp_year: 2034 },
        },
        h.clock.now(),
      ),
    )

    const [row] = await h.admin<{
      brand: string; last4: string; exp_month: number; detached_at: Date | null
    }[]>`
      SELECT brand, last4, exp_month, detached_at FROM payment_methods
      WHERE stripe_payment_method_id = 'pm_card'`
    assert.equal(row!.brand, 'visa')
    assert.equal(row!.last4, '4242')
    assert.equal(row!.exp_month, 12)
    assert.equal(row!.detached_at, null)

    await deliver(
      envelope(
        'payment_method.detached',
        {
          id: 'pm_card', object: 'payment_method', type: 'card', customer: customerId,
          card: { brand: 'visa', last4: '4242', exp_month: 12, exp_year: 2034 },
        },
        new Date(h.clock.now().getTime() + 1000),
      ),
    )
    const [after] = await h.admin<{ detached_at: Date | null }[]>`
      SELECT detached_at FROM payment_methods WHERE stripe_payment_method_id = 'pm_card'`
    assert.ok(after!.detached_at, 'detaching a card did not record it')

    await dropOrg(h.admin, o.orgId)
  })

  it('an event about a customer nobody knows cannot be scoped, and is not recorded', async () => {
    const outcome = await deliver(
      envelope('customer.subscription.created', { id: 'sub_x', object: 'subscription' }, h.clock.now()),
    )
    assert.equal(outcome.handled, false)
    assert.match(outcome.detail, /no customer in the payload/)
  })

  it('an event type this control plane does not act on is acknowledged and not recorded', async () => {
    const { org: o, customerId } = await freshOrg('unhandled')
    await attach(o.orgId, customerId)

    const outcome = await deliver(
      envelope('charge.dispute.created', { id: 'dp_1', customer: customerId }, h.clock.now()),
    )
    assert.equal(outcome.handled, false)
    const [count] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM billing_events WHERE stripe_customer_id = ${customerId}`
    assert.equal(Number(count!.n), 0)

    await dropOrg(h.admin, o.orgId)
  })

  // -------------------------------------------------------------------------
  // Isolation. The highest risk part of this change.
  // -------------------------------------------------------------------------

  it('a verified delivery cannot write a row for an organization it does not name', async () => {
    // The residual risk the policies exist for: a handler bug, a mixed-up
    // variable, a loop reusing the wrong identifier. The delivery declares a
    // customer; the policy ties the row's organization to the one that already
    // owns that customer, so this write reaches nothing.
    const alice = await freshOrg('alice')
    const bob = await freshOrg('bob')
    await attach(alice.org.orgId, alice.customerId)
    await attach(bob.org.orgId, bob.customerId)

    const failed = await h.pool
      .withStripeCustomer(alice.customerId, async (db) => {
        await db.execute(sql`
          INSERT INTO subscriptions (
            org_id, stripe_subscription_id, stripe_customer_id, plan, status, last_event_at)
          VALUES (${bob.org.orgId}::uuid, 'sub_stolen', ${alice.customerId}, 'enterprise',
                  'active', now())`)
      })
      .then(() => null, (e: unknown) => e)
    assert.ok(failed, "a delivery about alice's customer wrote a row for bob")

    // And it cannot move another organization's plan either.
    await h.pool.withStripeCustomer(alice.customerId, async (db) => {
      await db.execute(sql`
        UPDATE organizations SET plan = 'enterprise' WHERE id = ${bob.org.orgId}::uuid`)
    })
    assert.equal(await planOf(bob.org.orgId), 'free', "a delivery moved another organization's plan")

    // The one it does name still works, so this is isolation rather than a
    // policy that refuses everything.
    await h.pool.withStripeCustomer(alice.customerId, async (db) => {
      await db.execute(sql`
        INSERT INTO subscriptions (
          org_id, stripe_subscription_id, stripe_customer_id, plan, status, last_event_at)
        VALUES (${alice.org.orgId}::uuid, 'sub_alice', ${alice.customerId}, 'team',
                'active', now())`)
    })
    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM subscriptions WHERE stripe_subscription_id = 'sub_alice'`
    assert.equal(Number(row!.n), 1, 'the delivery could not write its own subscription either')

    await dropOrg(h.admin, alice.org.orgId)
    await dropOrg(h.admin, bob.org.orgId)
  })

  it('a delivery cannot read another customer’s billing rows', async () => {
    const alice = await freshOrg('readalice')
    const bob = await freshOrg('readbob')
    await attach(alice.org.orgId, alice.customerId)
    await attach(bob.org.orgId, bob.customerId)
    await h.admin`
      INSERT INTO subscriptions (
        org_id, stripe_subscription_id, stripe_customer_id, plan, status, last_event_at)
      VALUES (${bob.org.orgId}, 'sub_bobs', ${bob.customerId}, 'enterprise', 'active', now())`

    const visible = await h.pool.withStripeCustomer(alice.customerId, async (db) =>
      db.execute<{ stripe_subscription_id: string }>(sql`
        SELECT stripe_subscription_id FROM subscriptions`),
    )
    assert.deepEqual(visible.map((r) => r.stripe_subscription_id), [])

    const customers = await h.pool.withStripeCustomer(alice.customerId, async (db) =>
      db.execute<{ stripe_customer_id: string }>(sql`SELECT stripe_customer_id FROM billing_customers`),
    )
    assert.deepEqual(customers.map((r) => r.stripe_customer_id), [alice.customerId])

    await dropOrg(h.admin, alice.org.orgId)
    await dropOrg(h.admin, bob.org.orgId)
  })

  it('the database and PLAN_QUOTAS agree about which plans exist', async () => {
    // The plan list is written twice: once as the keys of PLAN_QUOTAS, which
    // decides what a plan entitles, and once as a CHECK constraint on
    // subscriptions, which decides what can be stored. A plan added to one and
    // not the other fails the INSERT on the webhook, which is the one path that
    // must never drop a message, and it fails only for the customers who bought
    // the new plan.
    const [constraint] = await h.admin<{ definition: string }[]>`
      SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint
      WHERE conname = 'subscriptions_plan'`
    assert.ok(constraint, 'subscriptions has no plan constraint at all')

    for (const plan of PLANS) {
      assert.ok(
        constraint.definition.includes(`'${plan}'`),
        `PLAN_QUOTAS knows ${plan} and the database refuses it: ${constraint.definition}`,
      )
    }
    // And nothing the database allows is a plan PLAN_QUOTAS has never heard of,
    // which would enforce free limits on somebody paying.
    for (const quoted of constraint.definition.match(/'[a-z_]+'/g) ?? []) {
      const plan = quoted.slice(1, -1)
      assert.ok(
        PLANS.includes(plan),
        `the database allows the plan ${plan} and PLAN_QUOTAS has no limits for it`,
      )
    }
  })

  it('billing rows are append-only: the application role cannot delete one', async () => {
    const failure = await h.pool
      .withTenant({ orgId: org.orgId }, async (db) => {
        await db.execute(sql`DELETE FROM invoices WHERE org_id = ${org.orgId}::uuid`)
      })
      .then(() => null, (e: unknown) => e)
    assert.ok(failure, 'the application role can delete an invoice')
  })

  // -------------------------------------------------------------------------
  // The routes
  // -------------------------------------------------------------------------

  it('checkout creates a customer, opens a session, and sends the browser somewhere real', async () => {
    const { status, body } = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      successUrl: 'https://app.test/billing/done',
      cancelUrl: 'https://app.test/billing',
    })
    assert.equal(status, 200, JSON.stringify(body))
    const result = (body as { result: { data: { url: string; sessionId: string } } }).result.data
    assert.match(result.url, /^https:\/\/checkout\.stripe\.com\//)
    assert.ok(
      result.url.endsWith(result.sessionId),
      `the url has to name the session it opened: ${result.url} against ${result.sessionId}`,
    )

    // The customer was recorded, so a second checkout does not create another.
    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.ok(customer!.stripe_customer_id.startsWith('cus_'))

    const [entry] = await h.admin<{ action: string }[]>`
      SELECT action FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'billing.checkout_started'`
    assert.ok(entry, 'starting a purchase was not audited')
  })

  // -------------------------------------------------------------------------
  // The quantity checkout does not send.
  //
  // THE DEFECT. `subscriptions.checkout` took `seats` between one and a
  // thousand and passed it to Stripe as `line_items[0][quantity]`, so the price
  // multiplied by it. Nothing read it back: the seat limit is a per plan
  // constant in entitlements.ts. An organization that bought 3 seats on `team`
  // got 50, and one that bought 200 also got 50, at two hundred times the
  // price. What this sells is one hosted control plane per organization, at a
  // flat fee, so there is no number for a price to multiply.
  //
  // Asserted on the BODY THAT REACHED STRIPE rather than on the input schema,
  // because the input schema is not what an invoice is computed from. A route
  // that quietly defaulted the quantity to something would satisfy a schema
  // assertion and still bill per unit.
  // -------------------------------------------------------------------------

  it('checkout sends Stripe no quantity, because nothing here is sold per unit', async () => {
    const before = sentToStripe.length
    const { status, body } = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      successUrl: 'https://app.test/billing/done',
      cancelUrl: 'https://app.test/billing',
    })
    assert.equal(status, 200, JSON.stringify(body))

    const sent = lastSent('/v1/checkout/sessions')
    // The negative control. Without it, a recorder that captured nothing would
    // make the assertion below pass against an empty string forever.
    assert.ok(sent, 'nothing was recorded for /v1/checkout/sessions, so this proves nothing')
    assert.ok(sentToStripe.length > before, 'the recorder did not see this call')
    const form = new URLSearchParams(sent.body)
    assert.equal(
      form.get('line_items[0][price]'), 'price_team_afmock',
      'the recorded body is not the checkout session body, so the assertion below is vacuous',
    )

    assert.equal(
      form.has('line_items[0][quantity]'), false,
      'checkout sent Stripe a per unit quantity. The price multiplies by it and nothing ' +
        'entitles anything from it, so this is a charge for something the buyer does not get.',
    )
  })

  it('a caller that asks for two hundred seats is billed for none of them', async () => {
    // The exact shape of the defect, driven from the outside. `seats` is gone
    // from the validator, so zod strips it: the call succeeds and the number
    // reaches neither Stripe nor the audit trail. Silently ignoring is the
    // right direction to be wrong in here, because the alternative failure
    // charges somebody.
    const { status, body } = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      seats: 200,
      successUrl: 'https://app.test/billing/done',
      cancelUrl: 'https://app.test/billing',
    })
    assert.equal(status, 200, JSON.stringify(body))

    const sent = lastSent('/v1/checkout/sessions')
    assert.ok(sent, 'nothing was recorded for /v1/checkout/sessions, so this proves nothing')
    const form = new URLSearchParams(sent.body)
    assert.equal(form.get('line_items[0][price]'), 'price_team_afmock')
    assert.equal(
      form.has('line_items[0][quantity]'), false,
      'a seat count in the input reached Stripe as a quantity',
    )
    assert.equal(sent.body.includes('200'), false, `200 reached Stripe: ${sent.body}`)

    // And the audit entry does not record a seat count either, because there
    // is no longer any such thing to record.
    const [entry] = await h.admin<{ detail: Record<string, unknown> }[]>`
      SELECT detail FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'billing.checkout_started'
      ORDER BY occurred_at DESC LIMIT 1`
    assert.ok(entry, 'starting a purchase was not audited')
    assert.equal('seats' in entry.detail, false, `the audit entry still records seats: ${JSON.stringify(entry.detail)}`)
  })

  it('checkout refuses a second subscription while one is live', async () => {
    await h.admin`
      INSERT INTO subscriptions (
        org_id, stripe_subscription_id, stripe_customer_id, plan, status, last_event_at)
      VALUES (${org.orgId}, 'sub_live_already',
              (SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}),
              'team', 'active', now())`

    const { body } = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'enterprise',
      successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no',
    })
    assert.equal(errorCode(body), 'PRECONDITION_FAILED')
  })

  it('checkout on a plan with no price says it is arranged with a person, and calls no Stripe', async () => {
    // END TO END, through the route, against a control plane configured the way
    // the live one is: a Team price and no Enterprise price.
    //
    // Before this, `prices.enterprise` was an empty string and it went to
    // Stripe as `line_items[0][price]=`. Stripe answers a generic
    // invalid_request_error, which reaches the buyer as "could not open a
    // checkout page": a wall with no explanation, on the plan with the largest
    // cheque behind it. The refusal now happens before any call is made, which
    // is asserted rather than assumed.
    const noEnterprise = await stripeAgainstMockPack({
      prices: { team: 'price_team_afmock' },
    })
    const called: string[] = []
    const pack = noEnterprise.config.fetch!
    noEnterprise.config.fetch = async (input, init) => {
      called.push(new URL(input instanceof Request ? input.url : String(input)).pathname)
      return pack(input, init)
    }
    const solo = await startApi({ stripe: noEnterprise.billing })
    const soloOrg = await seedOrg(solo.admin, 'noentprice')
    const soloOwner = await signInAs(solo, soloOrg, 'owner')
    // A customer already attached, with an id of this run's own.
    //
    // Two reasons. It keeps the Stripe calls below down to the one this test is
    // about, so "nothing reached Stripe" means the refusal and not the absence
    // of a customer. And the mock pack mints `cus_mock00000000000001` for the
    // first customer of every pack instance, while stripe_customer_id is unique
    // across the whole table, so a second harness creating a customer collides
    // with the one the outer suite already made.
    const soloCustomer = `cus_noent${randomUUID().slice(0, 8).replaceAll('-', '')}`
    await solo.admin`
      INSERT INTO billing_customers (org_id, stripe_customer_id, email)
      VALUES (${soloOrg.orgId}, ${soloCustomer}, 'billing@example.test')`

    const refused = await callProcedure(solo, soloOwner, 'subscriptions.checkout', 'mutation', {
      plan: 'enterprise',
      successUrl: 'https://app.test/ok',
      cancelUrl: 'https://app.test/no',
    })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED', JSON.stringify(refused.body))
    // The MESSAGE, not the envelope. The tRPC error body is
    // `{"error":{"message":...}}`, so asserting over the whole thing means the
    // word "error" is always present and the tone check below can never fail.
    const said = (refused.body as { error: { message: string } }).error.message
    assert.ok(said.length > 40, `the refusal has no message to read: ${JSON.stringify(refused.body)}`)
    // The words are asserted because the WORDING is the feature here. This
    // refusal lands on somebody trying to buy the largest plan, and the worst
    // outcome is that they read it as an outage and leave. It has to say what
    // happens next and who does it.
    assert.match(said, /agreed with a person rather than bought here/)
    assert.match(said, /antifailure\.dev\/contact/, 'the refusal does not say where to go instead')
    assert.match(said, /somebody will arrange it/, 'the refusal does not say anybody will act on it')
    assert.match(said, /Nothing was charged/)
    // And it never reads as a failure of the system.
    assert.doesNotMatch(said, /error|failed|unavailable|not configured/i, said)

    // NOTHING REACHED STRIPE. A refusal that first created a customer would
    // leave a customer behind at Stripe for somebody who cannot buy anything.
    // Length rather than deepEqual against a literal: node's deepEqual narrows
    // its first argument to the type of the second, so comparing against `[]`
    // makes `called` a never[] and the assertion below stops compiling.
    assert.equal(called.length, 0, `Stripe was called before the refusal: ${called.join(', ')}`)

    // And the plan that DOES have a price still sells, so this is not passing
    // by refusing everything.
    const bought = await callProcedure(solo, soloOwner, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      successUrl: 'https://app.test/ok',
      cancelUrl: 'https://app.test/no',
    })
    assert.equal(bought.status, 200, JSON.stringify(bought.body))
    assert.ok(
      called.includes('/v1/checkout/sessions'),
      'the Team checkout did not reach Stripe either, so the assertion above proves nothing',
    )

    await solo.admin`DELETE FROM subscriptions WHERE org_id = ${soloOrg.orgId}`
    await solo.admin`DELETE FROM billing_customers WHERE org_id = ${soloOrg.orgId}`
    await dropOrg(solo.admin, soloOrg.orgId)
    await solo.close()
  })

  it('the portal opens for a customer that exists and refuses for one that does not', async () => {
    const { status, body } = await callProcedure(h, owner, 'subscriptions.portal', 'mutation', {
      returnUrl: 'https://app.test/billing',
    })
    assert.equal(status, 200, JSON.stringify(body))
    const url = (body as { result: { data: { url: string } } }).result.data.url
    assert.match(url, /^https:\/\/billing\.stripe\.com\//)

    const other = await seedOrg(h.admin, 'noportal')
    const session = await signInAs(h, other, 'owner')
    const refused = await callProcedure(h, session, 'subscriptions.portal', 'mutation', {
      returnUrl: 'https://app.test/billing',
    })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    await dropOrg(h.admin, other.orgId)
  })

  it('cancelling ends the subscription at the end of the period and tears nothing down', async () => {
    const { org: o, customerId } = await freshOrg('cancelroute')
    await attach(o.orgId, customerId)
    const session = await signInAs(h, o, 'owner')

    // A subscription that exists at Stripe, so the cancel reaches something.
    const created = await billing.config.fetch!(
      new URL('/v1/subscriptions', 'https://api.stripe.com'),
      {
        method: 'POST',
        headers: { 'content-type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({
          customer: customerId, 'items[0][price]': 'price_team_afmock',
        }).toString(),
      },
    )
    const atStripe = (await created.json()) as { id: string }
    await h.admin`
      INSERT INTO subscriptions (
        org_id, stripe_subscription_id, stripe_customer_id, plan, status,
        current_period_end, last_event_at)
      VALUES (${o.orgId}, ${atStripe.id}, ${customerId}, 'team', 'active',
              now() + interval '20 days', now())`
    await h.admin`UPDATE organizations SET plan = 'team' WHERE id = ${o.orgId}`

    const { status, body } = await callProcedure(h, session, 'subscriptions.cancel', 'mutation', {
      reason: 'moving to the free plan',
    })
    assert.equal(status, 200, JSON.stringify(body))
    const result = (body as { result: { data: { cancelAtPeriodEnd: boolean } } }).result.data
    assert.equal(result.cancelAtPeriodEnd, true, 'the cancel did not ask for the end of the period')

    // The plan is untouched until Stripe says the subscription ended.
    assert.equal(await planOf(o.orgId), 'team', 'cancelling took the plan away immediately')

    const [row] = await h.admin<{ cancel_at_period_end: boolean }[]>`
      SELECT cancel_at_period_end FROM subscriptions WHERE stripe_subscription_id = ${atStripe.id}`
    assert.equal(row!.cancel_at_period_end, true, 'the row does not record the cancellation')

    await dropOrg(h.admin, o.orgId)
  })

  it('current reports the plan, the subscription and the card without any secret', async () => {
    const { status, body } = await callProcedure(h, owner, 'subscriptions.current', 'query', {})
    assert.equal(status, 200, JSON.stringify(body))
    const data = (body as { result: { data: Record<string, unknown> } }).result.data
    assert.equal(data.configured, true)
    assert.ok(data.customer, 'no customer after a checkout')

    const serialized = JSON.stringify(data)
    assert.ok(!serialized.includes('sk_test'), 'the API key reached a response')
    assert.ok(!serialized.includes('whsec'), 'the webhook secret reached a response')
  })

  it('every billing route needs billing.manage', async () => {
    // The matrix test proves this for every route by every role. This is the
    // one case worth naming here, because it is the permission the catalog has
    // always carried and nothing has ever guarded.
    for (const [path, type, input] of [
      ['subscriptions.current', 'query', {}],
      ['subscriptions.invoices', 'query', { limit: 5 }],
      ['subscriptions.portal', 'mutation', { returnUrl: 'https://app.test/b' }],
      ['subscriptions.reconcile', 'mutation', {}],
    ] as const) {
      const { body } = await callProcedure(h, viewer, path, type, input)
      assert.equal(errorCode(body), 'FORBIDDEN', `${path} was reachable by a viewer`)
    }
  })

  it('a control plane with no Stripe serves the billing screen and refuses to charge', async () => {
    // The self-hosted default. Every route has to answer rather than crash, and
    // the refusal has to name the variables an operator would set.
    const plain = await startApi()
    const plainOrg = await seedOrg(plain.admin, 'nostripe')
    const session = await signInAs(plain, plainOrg, 'owner')

    const read = await callProcedure(plain, session, 'subscriptions.current', 'query', {})
    assert.equal(read.status, 200, JSON.stringify(read.body))
    const data = (read.body as { result: { data: { configured: boolean } } }).result.data
    assert.equal(data.configured, false)

    const charge = await callProcedure(plain, session, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no',
    })
    assert.equal(errorCode(charge.body), 'PRECONDITION_FAILED')
    assert.match(
      JSON.stringify(charge.body),
      /AF_STRIPE_SECRET_KEY/,
      'the refusal does not say which variables to set',
    )

    await dropOrg(plain.admin, plainOrg.orgId)
    await plain.close()
  })

  // -------------------------------------------------------------------------
  // The endpoint
  // -------------------------------------------------------------------------

  it('the endpoint refuses an unsigned delivery before it parses anything', async () => {
    const body = envelope(
      'customer.subscription.created',
      subscriptionObject({ id: 'sub_unsigned', customer: 'cus_unsigned' }),
      h.clock.now(),
    )
    const res = await h.fetch('/webhooks/stripe', { method: 'POST', body })
    assert.equal(res.status, 401)
    const answered = (await res.json()) as { error: string }
    assert.equal(answered.error, 'That delivery could not be verified.')
    assert.ok(
      !JSON.stringify(answered).includes('signature header'),
      'the refusal says which check failed, which helps somebody iterate towards a valid one',
    )
  })

  it('the endpoint accepts a signed delivery and applies it', async () => {
    const { org: o, customerId } = await freshOrg('endpoint')
    await attach(o.orgId, customerId)

    const body = envelope(
      'customer.subscription.created',
      subscriptionObject({ id: 'sub_endpoint', customer: customerId }),
      h.clock.now(),
    )
    const res = await h.fetch('/webhooks/stripe', {
      method: 'POST',
      body,
      headers: { 'stripe-signature': signed(billing.config.webhookSecret, body, h.clock.now()) },
    })
    assert.equal(res.status, 200, await res.text())
    assert.equal(await planOf(o.orgId), 'team')

    await dropOrg(h.admin, o.orgId)
  })

  it('the endpoint answers 400 for a body that will never parse, so Stripe stops retrying', async () => {
    const body = '{"not":"an event"}'
    const res = await h.fetch('/webhooks/stripe', {
      method: 'POST',
      body,
      headers: { 'stripe-signature': signed(billing.config.webhookSecret, body, h.clock.now()) },
    })
    assert.equal(res.status, 400)
  })

  it('a control plane with no Stripe answers 503 rather than accepting a delivery', async () => {
    const plain = await startApi()
    const res = await plain.fetch('/webhooks/stripe', { method: 'POST', body: '{}' })
    assert.equal(res.status, 503)
    await plain.close()
  })
})
