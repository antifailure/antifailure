// One payable checkout per organization, and a paused subscription that can
// still resume.
//
// Two defects live here, and both of them charge a real customer twice.
//
// TWO PAYABLE PAGES. A checkout session creates no subscription, so while one is
// merely open there is nothing in the subscriptions table to refuse a second one
// on, and nothing at Stripe either. The only thing that ever tied two requests
// together was a thirty second idempotency bucket on the key sent to Stripe, so
// two billing owners who pressed Subscribe a minute apart fell either side of it,
// each got their own hosted page, and a checkout session stays payable for twenty
// four hours. Both pages could be paid. Against main at 67fcf2d7 this suite's
// first cell failed with two open sessions and named them.
//
// A PAUSED SUBSCRIPTION. Stripe reaches `paused` when a trial ends with no
// payment method, and the customer resumes it by adding one. It was in neither
// list the route consulted, so an organization whose subscription was paused was
// sold a second one, and the day a card was added it paid for both.
//
// ---------------------------------------------------------------------------
// The orderings, which are the deliverable
// ---------------------------------------------------------------------------
//
// THE ORDERINGS, each named by the cell that proves it:
//
//   presses       inside thirty seconds, a minute apart, two owners in two
//                 tabs, two requests in the same instant, two connections
//                 racing the claim
//   an open page  a second press resumes it; a different plan or return
//                 address expires it first; one opened before this release
//                 is expired by the next press
//   a lost page   a creation whose response was lost is adopted
//   an ended page expired by Stripe, replaced; forgotten by Stripe, refused
//                 until Refresh from Stripe clears it
//   paying        redirect before webhook, webhook never arriving, webhook
//                 before redirect, webhook after a second press, webhook
//                 retried, a cancelled subscription letting a buyer return
//   Stripe down   the open session listing, the subscription listing (404,
//                 a failed page, an unreadable row), and customer creation
//                 pressed once and pressed twice
//
// Every cell below is one arrival order with a verified outcome, and the rule
// they all serve is one sentence: at no moment may this organization have two
// checkout pages that can take a card. Where an ordering can no longer arise at
// all, the cell proves the mechanism that removes it rather than asserting
// nothing.
//
// THE PROVIDER. The client under test is the one that ships, with its transport
// pointed at the engine's own Stripe pack, which is the arrangement harness.ts
// explains. The pack answers everything except two behaviours it has no way to
// express, both of which this file supplies and both of which are Stripe's
// documented contract rather than this author's opinion:
//
//   a repeated idempotency key returns the saved first result
//   a key used by a request that is still executing answers 409
//
// https://docs.stripe.com/api/idempotent_requests. The pack cannot model either
// because its runtime is given a method, a path and a body and never sees a
// header, so the key does not reach it. That is a real gap in the pack and it is
// named in the pull request; it is not a gap in what is proven here, because the
// two rules are implemented from the documentation and the cells that depend on
// them say so.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import {
  available,
  startApi,
  seedOrg,
  signInAs,
  callProcedure,
  dropOrg,
  errorCode,
  stripeAgainstMockPack,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'
import { RealStripeClient, type StripeSubscription } from '../src/billing/stripe.ts'
import { LIVE_STATUSES, TERMINAL_STATUSES } from '../src/billing/plans.ts'
import { refusalFromStripe } from '../src/routers/subscriptions.ts'

const hasDatabase = await available()

/** What the console sends. */
const buy = {
  plan: 'team',
  successUrl: 'https://app.test/plan?checkout=success',
  cancelUrl: 'https://app.test/plan',
}

const WEBHOOK_SECRET = 'whsec_afmocktestsecret'

/**
 * The shipped pack, plus the behaviours a header-blind pack cannot have.
 *
 * `listed` and `objects` replace what Stripe says about this customer's
 * subscriptions, which is how a paused, active or cancelled subscription this
 * database has not been told about is put in front of the route. `paid` is how a
 * session the customer has completed is put in front of it, because the pack has
 * no route for paying one.
 */
class Provider {
  /** Every POST that reached Stripe, with the idempotency key it carried. */
  posted: { path: string; key: string | null; body: URLSearchParams }[] = []
  /** The subscription COLLECTION to answer with, or null for the pack's own.
   *  Empty is a state of its own and a real one: a listing that has not caught
   *  up with a purchase the customer has already made. */
  listed: Record<string, unknown>[] | null = null
  /** The collection in pages, followed by starting_after, for the strict read. */
  listedPages: Record<string, unknown>[][] | null = null
  /** The page index that answers 500, which is a failure mid pagination. */
  failOnPage: number | null = null
  /** Set to answer the subscription collection with 404. */
  listed404 = false
  /** Set to drop the connection for customer creation before it reaches
   *  Stripe, which is what an egress policy refusing api.stripe.com does. */
  unreachableCustomers = false
  /** Single subscriptions by id, for the read that asks about the one a
   *  completed page created. Separate from the collection on purpose, so a cell
   *  can hold a listing that disagrees with the object. */
  objects = new Map<string, Record<string, unknown>>()
  /** Sessions the customer has completed, and the subscription each created. */
  private completed = new Map<string, string | null>()
  /** Saved results per idempotency key, exactly as Stripe saves them. */
  private saved = new Map<string, { status: number; body: string }>()
  private executing = new Set<string>()
  /** Set to drop the response of the next session creation AFTER Stripe has
   *  committed it, which is the shape of a lost response. */
  loseResponse = false
  /** Set to make the open session listing fail, which is Stripe being
   *  unreachable at the moment the guard needs it most. */
  refuseListing = false
  /** Single sessions by id, replacing what the pack would answer: an object is
   *  returned instead, and null answers 404. How a substituted or forgotten
   *  session is put in front of the route. */
  sessionReads = new Map<string, Record<string, unknown> | null>()
  /** Holds the next creation until something releases it, so a race is run
   *  deterministically rather than hopefully. */
  private release: (() => void) | null = null
  private holding: Promise<void> | null = null
  /** Releases the held creation as soon as a concurrent request is refused for
   *  using the same key, which is the moment the race is proven. */
  releaseOnConflict = false

  private readonly underneath: typeof globalThis.fetch

  constructor(underneath: typeof globalThis.fetch) {
    this.underneath = underneath
  }

  /** Makes the next creation wait. */
  holdCreation(): void {
    this.holding = new Promise<void>((resolve) => {
      this.release = resolve
    })
  }

  letGo(): void {
    this.release?.()
    this.release = null
    this.holding = null
  }

  /** What Stripe says once the customer has paid on this page. */
  paid(sessionId: string, subscriptionId: string | null): void {
    this.completed.set(sessionId, subscriptionId)
  }

  fetch: typeof globalThis.fetch = async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input))
    const method = (init?.method ?? 'GET').toUpperCase()
    const key = new Headers(init?.headers).get('idempotency-key')
    const isCreate = method === 'POST' && url.pathname === '/v1/checkout/sessions'
    const answer = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'content-type': 'application/json' },
      })

    if (this.unreachableCustomers && method === 'POST' && url.pathname === '/v1/customers') {
      throw new TypeError('fetch failed')
    }
    if (method === 'POST') {
      this.posted.push({
        path: url.pathname,
        key,
        body: new URLSearchParams(typeof init?.body === 'string' ? init.body : ''),
      })
    }

    if (url.pathname === '/v1/subscriptions' && method === 'GET') {
      if (this.listed404) {
        return answer({ error: { type: 'invalid_request_error', code: 'resource_missing', message: 'No such customer' } }, 404)
      }
      if (this.listedPages !== null) {
        const after = url.searchParams.get('starting_after')
        const index = after === null ? 0 : this.listedPages.findIndex((p) => p.at(-1)?.id === after) + 1
        if (index === this.failOnPage) {
          return answer({ error: { type: 'api_error', message: 'Stripe is unreachable' } }, 500)
        }
        const data = this.listedPages[index] ?? []
        return answer({ object: 'list', data, has_more: index < this.listedPages.length - 1 })
      }
      if (this.listed !== null) return answer({ object: 'list', data: this.listed, has_more: false })
    }
    if (method === 'GET' && url.pathname.startsWith('/v1/subscriptions/')) {
      const held = this.objects.get(url.pathname.split('/').at(-1)!)
      if (held) return answer(held)
    }
    if (method === 'GET' && url.pathname.startsWith('/v1/checkout/sessions/')) {
      const id = url.pathname.split('/').at(-1)!
      if (this.sessionReads.has(id)) {
        const read = this.sessionReads.get(id)
        return read === null
          ? answer({ error: { type: 'invalid_request_error', code: 'resource_missing', message: 'No such checkout session' } }, 404)
          : answer(read)
      }
    }
    if (this.refuseListing && method === 'GET' && url.pathname === '/v1/checkout/sessions') {
      return answer({ error: { type: 'api_error', message: 'Stripe is unreachable' } }, 500)
    }

    if (method === 'POST' && key !== null) {
      // Stripe returns the saved result of the first request for a repeated key.
      const already = this.saved.get(key)
      if (already) return new Response(already.body, {
        status: already.status,
        headers: { 'content-type': 'application/json' },
      })
      // And refuses a key being used by a request that has not finished, saving
      // nothing, which is why the caller may retry it.
      if (this.executing.has(key)) {
        if (this.releaseOnConflict) this.letGo()
        return answer(
          {
            error: {
              type: 'invalid_request_error',
              code: 'idempotency_key_in_use',
              message:
                'There is currently another in-progress request using this Idempotency Key.',
            },
          },
          409,
        )
      }
      this.executing.add(key)
    }

    try {
      if (isCreate && this.holding) await this.holding
      const answered = await this.underneath(input, init)
      const body = await answered.text()
      if (method === 'POST' && key !== null) {
        this.saved.set(key, { status: answered.status, body })
      }
      if (isCreate && this.loseResponse) {
        this.loseResponse = false
        // Stripe committed and the answer never arrived. The page is payable and
        // nothing here knows its identifier.
        throw new TypeError('fetch failed')
      }
      return new Response(this.patch(url, body), {
        status: answered.status,
        headers: { 'content-type': 'application/json' },
      })
    } finally {
      if (key !== null) this.executing.delete(key)
    }
  }

  /** Applies what the customer has done to what Stripe says. A session the
   *  customer paid reads back complete, with no address and with the
   *  subscription it created, which is what Stripe returns. */
  private patch(url: URL, body: string): string {
    if (this.completed.size === 0) return body
    if (!url.pathname.startsWith('/v1/checkout/sessions')) return body
    let parsed: unknown
    try {
      parsed = JSON.parse(body)
    } catch {
      return body
    }
    const one = (value: unknown): unknown => {
      if (value === null || typeof value !== 'object') return value
      const session = value as Record<string, unknown>
      const id = session.id
      if (typeof id !== 'string' || !this.completed.has(id)) return value
      return { ...session, status: 'complete', url: null, subscription: this.completed.get(id) }
    }
    const root = parsed as Record<string, unknown>
    if (Array.isArray(root.data)) return JSON.stringify({ ...root, data: root.data.map(one) })
    return JSON.stringify(one(root))
  }

  /** One session as Stripe holds it, read through the same transport the route
   *  uses. Raw rather than through the client, so a reproduction against a tree
   *  that has no session reader still runs. */
  async session(id: string): Promise<{ id?: string; status?: string; url?: string | null }> {
    const answered = await this.fetch(`https://api.stripe.com/v1/checkout/sessions/${id}`, {
      headers: { authorization: 'Bearer sk_test_afmock' },
    })
    return (await answered.json()) as { id?: string; status?: string; url?: string | null }
  }

  /** Stripe expiring a page on its own, twenty four hours after it was opened. */
  async expire(id: string): Promise<void> {
    const answered = await this.fetch(
      `https://api.stripe.com/v1/checkout/sessions/${id}/expire`,
      { method: 'POST', headers: { authorization: 'Bearer sk_test_afmock' }, body: '' },
    )
    assert.equal(answered.status, 200, `the fixture could not expire ${id}`)
  }

  /** How many pages this run opened at Stripe. */
  creations(): number {
    return this.posted.filter((p) => p.path === '/v1/checkout/sessions').length
  }
}

/** A subscription object in Stripe's shape, in whatever status a cell needs. */
function subscriptionObject(over: {
  id: string
  customer: string
  status: string
  created?: number
  cancelAtPeriodEnd?: boolean
}): Record<string, unknown> {
  return {
    id: over.id,
    object: 'subscription',
    customer: over.customer,
    status: over.status,
    created: over.created ?? 1767225600,
    current_period_start: 1767225600,
    current_period_end: 1769904000,
    cancel_at_period_end: over.cancelAtPeriodEnd ?? false,
    canceled_at: null,
    items: {
      object: 'list',
      data: [{ id: 'si_test', price: { id: 'price_team_afmock' }, quantity: 1 }],
    },
  }
}

describe('one payable checkout per organization', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let provider: Provider
  const created: string[] = []

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    provider = new Provider(stripe.config.fetch!)
    h = await startApi({
      stripe: {
        config: stripe.config,
        client: new RealStripeClient({ ...stripe.config, fetch: provider.fetch }),
      },
    })
  })

  after(async () => {
    for (const orgId of created) await dropOrg(h.admin, orgId)
    await h.close()
  })

  async function freshOrg(label: string): Promise<{ org: Org; owner: SignedIn }> {
    const org = await seedOrg(h.admin, `${label}-${randomUUID().slice(0, 6)}`)
    created.push(org.orgId)
    provider.listed = null
    provider.objects.clear()
    provider.sessionReads.clear()
    provider.listedPages = null
    provider.failOnPage = null
    provider.listed404 = false
    provider.unreachableCustomers = false
    provider.refuseListing = false
    provider.releaseOnConflict = false
    // Released rather than left for the cell that set it. A concurrency cell
    // that fails before its own letGo leaves every later creation in this file
    // waiting on a hold nothing will release, so one red cell would hang the
    // rest of the suite instead of reporting beside it.
    provider.letGo()
    provider.loseResponse = false
    return { org, owner: await signInAs(h, org, 'owner') }
  }

  /** An organization whose Stripe customer already exists here and at Stripe:
   *  the returning buyer, and the only shape in which the Stripe read runs. */
  async function returningBuyer(
    label: string,
  ): Promise<{ org: Org; owner: SignedIn; customerId: string }> {
    const { org, owner } = await freshOrg(label)
    const customerId = `cus_${label.replaceAll('-', '')}${randomUUID().slice(0, 8)}`
    await h.admin`
      INSERT INTO billing_customers (org_id, stripe_customer_id, email)
      VALUES (${org.orgId}, ${customerId}, 'buyer@example.test')`
    return { org, owner, customerId }
  }

  function checkout(owner: SignedIn, over: Record<string, unknown> = {}) {
    return callProcedure(h, owner, 'subscriptions.checkout', 'mutation', { ...buy, ...over })
  }

  function sessionIdOf(body: unknown): string | null {
    return (body as { result?: { data?: { sessionId?: string } } }).result?.data?.sessionId ?? null
  }

  function messageOf(body: unknown): string {
    return (body as { error?: { message?: string } }).error?.message ?? ''
  }

  /**
   * Every page this customer can still pay on, asked of Stripe.
   *
   * The assertion every cell ends with. Counted at the provider rather than in
   * the attempt table, because what charges a customer twice is a hosted page
   * that takes a card, not a row: a page this database forgot about is exactly
   * the case that has to be counted.
   */
  async function payable(customerId: string): Promise<string[]> {
    const answered = await provider.fetch(
      `https://api.stripe.com/v1/checkout/sessions?customer=${customerId}&status=open&limit=100`,
      { headers: { authorization: 'Bearer sk_test_afmock' } },
    )
    const page = (await answered.json()) as { data?: { id?: string; status?: string; customer?: string }[] }
    return (page.data ?? [])
      .filter((s) => s.status === 'open' && s.customer === customerId)
      .map((s) => s.id ?? '')
  }

  async function attemptOf(orgId: string) {
    const [row] = await h.admin<{ attempt_id: string; stripe_session_id: string | null }[]>`
      SELECT attempt_id, stripe_session_id FROM billing_checkout_attempts WHERE org_id = ${orgId}`
    return row ?? null
  }

  /** One signed delivery, as Stripe makes it. A caller that passes `id` is
   *  delivering an event that already arrived, which is what a Stripe retry is. */
  async function deliver(
    type: string,
    object: Record<string, unknown>,
    id = `evt_${randomUUID().replaceAll('-', '')}`,
  ): Promise<void> {
    const body = JSON.stringify({
      id,
      type,
      created: Math.floor(h.clock.now().getTime() / 1000),
      data: { object },
    })
    const timestamp = Math.floor(h.clock.now().getTime() / 1000)
    const signature = createHmac('sha256', WEBHOOK_SECRET).update(`${timestamp}.${body}`).digest('hex')
    const answered = await h.fetch('/webhooks/stripe', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'stripe-signature': `t=${timestamp},v1=${signature}` },
      body,
    })
    assert.equal(answered.status, 200, `the fixture's webhook was refused: ${await answered.text()}`)
  }

  // -------------------------------------------------------------------------
  // Presses, and how many pages they leave behind
  // -------------------------------------------------------------------------

  it('two presses inside thirty seconds open one page', async () => {
    const { org, owner } = await freshOrg('double-click')
    const mark = provider.creations()
    const first = await checkout(owner)
    h.clock.advance(5 * 1000)
    const second = await checkout(owner)
    assert.equal(first.status, 200, JSON.stringify(first.body))
    assert.equal(second.status, 200, JSON.stringify(second.body))
    assert.equal(sessionIdOf(second.body), sessionIdOf(first.body), 'the second press got another page')
    assert.equal(provider.creations() - mark, 1, 'a second page was created at Stripe')
    const recorded = (await attemptOf(org.orgId))!.stripe_session_id
    assert.equal(recorded, sessionIdOf(first.body), 'the attempt did not record the page it opened')
  })

  it('two presses a minute apart leave one payable page, not two', async () => {
    // THE DEFECT, and the cell that failed against main naming both sessions.
    // A minute is past the thirty second bucket that used to be the only thing
    // tying two requests together: two billing owners, one on a call.
    const { org, owner } = await freshOrg('apart')
    const first = await checkout(owner)
    assert.equal(first.status, 200, JSON.stringify(first.body))

    h.clock.advance(60 * 1000)
    const second = await checkout(owner)
    assert.equal(second.status, 200, JSON.stringify(second.body))

    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    const open = await payable(customer!.stripe_customer_id)
    assert.equal(
      open.length,
      1,
      `${open.length} pages can still be paid, so this customer can be charged twice: ${open.join(', ')}`,
    )
    assert.equal(sessionIdOf(second.body), open[0])
    // And it is the page the first press opened. Retiring it and opening another
    // would also leave one payable page, and would take the card form away from
    // whoever is typing into it.
    assert.equal(
      sessionIdOf(second.body),
      sessionIdOf(first.body),
      'the second press retired the first page and opened another',
    )
  })

  it('two requests in the same instant open one page, and one attempt row', async () => {
    // Two replicas, or two owners pressing together. The lock in the claim
    // serialises the row; the identical idempotency key is what stops the
    // second request opening a second page in the window before the first has
    // recorded its own. Stripe's refusal of a key in use is the signal that the
    // other request is mid flight, and it is documented as retryable.
    const { org, owner } = await freshOrg('instant')
    const mark = provider.creations()
    provider.releaseOnConflict = true
    provider.holdCreation()
    const [a, b] = await Promise.all([checkout(owner), checkout(owner)])
    provider.letGo()

    assert.equal(a.status, 200, `first: ${JSON.stringify(a.body)}`)
    assert.equal(b.status, 200, `second: ${JSON.stringify(b.body)}`)
    assert.equal(sessionIdOf(a.body), sessionIdOf(b.body), 'the two requests opened different pages')

    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM billing_checkout_attempts WHERE org_id = ${org.orgId}`
    assert.equal(Number(rows[0]!.n), 1, 'the race left more than one purchase attempt')
    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.deepEqual(await payable(customer!.stripe_customer_id), [sessionIdOf(a.body)])
    assert.ok(provider.creations() - mark >= 1, 'nothing was opened at all')
  })

  it('two connections racing to claim the attempt leave one row, not two', async () => {
    // The invariant at the database, with no application in the way. Both
    // transactions take the organization's lock in the order the application
    // takes it and then claim; the primary key is what makes the second a
    // conflict rather than a second payable purchase.
    const { org } = await freshOrg('claim-race')
    const claim = (label: string) =>
      h.admin.begin(async (tx) => {
        await tx`SELECT id FROM organizations WHERE id = ${org.orgId} FOR UPDATE`
        const rows = await tx<{ attempt_id: string }[]>`
          INSERT INTO billing_checkout_attempts
            (org_id, stripe_customer_id, price_id, success_url, cancel_url)
          VALUES (${org.orgId}, ${`cus_${label}`}, 'price_team_afmock',
                  'https://app.test/ok', 'https://app.test/no')
          ON CONFLICT DO NOTHING
          RETURNING attempt_id`
        return rows.length
      })
    const [first, second] = await Promise.all([claim('a'), claim('b')])
    assert.equal(
      Number(first) + Number(second),
      1,
      'both connections inserted an attempt, so one organization holds two purchases',
    )
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM billing_checkout_attempts WHERE org_id = ${org.orgId}`
    assert.equal(Number(rows[0]!.n), 1)
  })

  it('two billing owners in two tabs are given one page, and once it is paid neither can open another', async () => {
    // Two owners of one organization, each in their own browser, a minute and a
    // half apart, which is past the thirty second bucket that used to hand each
    // of them a payable page. Only owners hold billing.manage, so two owners is
    // the shape this takes in a real organization.
    const { org, owner } = await freshOrg('two-tabs')
    const colleague = await signInAs(h, org, 'owner', 'colleague')
    const mark = provider.creations()
    const mine = await checkout(owner)
    h.clock.advance(90 * 1000)
    const theirs = await checkout(colleague)
    assert.equal(mine.status, 200, JSON.stringify(mine.body))
    assert.equal(theirs.status, 200, JSON.stringify(theirs.body))
    const page = sessionIdOf(mine.body)!
    assert.equal(sessionIdOf(theirs.body), page, 'the second owner was given a payable page of their own')
    assert.equal(provider.creations() - mark, 1, 'a second page was created at Stripe')

    // The first owner pays. The second tab is still on the same page, which
    // Stripe takes one payment on, and a press from it before any webhook has
    // landed is refused.
    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    const customerId = customer!.stripe_customer_id
    provider.paid(page, 'sub_two_tabs')
    provider.listed = []
    provider.objects.set(
      'sub_two_tabs',
      subscriptionObject({ id: 'sub_two_tabs', customer: customerId, status: 'active' }),
    )
    const again = await checkout(colleague)
    assert.equal(again.status, 412, `the second owner bought again: ${JSON.stringify(again.body)}`)
    assert.match(messageOf(again.body), /already completed a checkout/)
    assert.equal(provider.creations() - mark, 1)
    assert.deepEqual(await payable(customerId), [])
  })

  it('a creation whose response was lost is adopted rather than repeated', async () => {
    // Stripe committed and the answer never arrived, so a payable page exists
    // that no row names. The attempt marker in the session's metadata is how it
    // is found again, which is why the recovery needs no assumption about how
    // long an idempotency key lives.
    const { org, owner } = await freshOrg('lost')
    provider.loseResponse = true
    const lost = await checkout(owner)
    assert.equal(lost.status, 502, `a lost response was not reported: ${JSON.stringify(lost.body)}`)
    assert.match(messageOf(lost.body), /Nothing was charged/)

    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    const orphan = await payable(customer!.stripe_customer_id)
    assert.equal(orphan.length, 1, 'the lost creation left no page, so this proves nothing')
    assert.equal((await attemptOf(org.orgId))!.stripe_session_id, null)

    const retry = await checkout(owner)
    assert.equal(retry.status, 200, JSON.stringify(retry.body))
    assert.equal(sessionIdOf(retry.body), orphan[0], 'the retry did not adopt the page already open')
    assert.deepEqual(await payable(customer!.stripe_customer_id), orphan)
  })

  it('a page Stripe has expired is replaced, and only the new one is payable', async () => {
    // The buyer who closed the tab on the card form and came back the next
    // morning. Twenty four hours at Stripe expires the page; the organization
    // must be able to buy, and the dead page must not come back.
    const { org, owner } = await freshOrg('expired')
    const first = await checkout(owner)
    const abandoned = sessionIdOf(first.body)!
    await provider.expire(abandoned)

    const second = await checkout(owner)
    assert.equal(second.status, 200, JSON.stringify(second.body))
    assert.notEqual(sessionIdOf(second.body), abandoned, 'the buyer was sent back to the expired page')

    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.deepEqual(await payable(customer!.stripe_customer_id), [sessionIdOf(second.body)])
  })

  it('a press for a different plan expires the open page before opening another', async () => {
    // The buyer who opened Team, went back and chose Enterprise. Two prices
    // cannot both be payable, and the old page has to die first rather than
    // afterwards.
    const { org, owner } = await freshOrg('replan')
    const first = await checkout(owner)
    const team = sessionIdOf(first.body)!

    const second = await checkout(owner, { plan: 'enterprise' })
    assert.equal(second.status, 200, JSON.stringify(second.body))
    assert.notEqual(sessionIdOf(second.body), team)

    const [customer] = await h.admin<{ stripe_customer_id: string }[]>`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.deepEqual(
      await payable(customer!.stripe_customer_id),
      [sessionIdOf(second.body)],
      'the page for the other plan is still payable',
    )
    const expired = await provider.session(team)
    assert.equal(expired.status, 'expired')
  })

  it('a payable page from before this shipped is expired by the next press', async () => {
    // THE STATE THIS DEPLOY INHERITS. Every organization with a checkout open
    // when this shipped has a page no attempt row names, and it stays payable
    // for twenty four hours. Nothing that only reasons about its own rows can
    // see it, so the customer's open sessions are asked for and swept.
    const { org, owner, customerId } = await returningBuyer('inherited')
    const before = await provider.fetch('https://api.stripe.com/v1/checkout/sessions', {
      method: 'POST',
      headers: {
        authorization: 'Bearer sk_test_afmock',
        'content-type': 'application/x-www-form-urlencoded',
      },
      body: new URLSearchParams({
        mode: 'subscription',
        customer: customerId,
        'line_items[0][price]': 'price_team_afmock',
        success_url: 'https://app.test/plan?checkout=success',
        cancel_url: 'https://app.test/plan',
        'metadata[org_id]': org.orgId,
      }).toString(),
    })
    const stale = ((await before.json()) as { id: string }).id
    assert.deepEqual(await payable(customerId), [stale], 'the fixture did not leave a payable page')

    const pressed = await checkout(owner)
    assert.equal(pressed.status, 200, JSON.stringify(pressed.body))
    const open = await payable(customerId)
    assert.equal(open.length, 1, `two pages are payable across the deploy: ${open.join(', ')}`)
    assert.equal(open[0], sessionIdOf(pressed.body))
    assert.equal((await provider.session(stale)).status, 'expired')
  })

  it('Stripe refusing to say what is open refuses the purchase', async () => {
    // The guard that cannot ask is the guard that is absent exactly when the
    // double charge happens, so a listing that fails is a refusal and not a
    // pass. Nothing may be created under it.
    const { owner, customerId } = await returningBuyer('listing-down')
    provider.refuseListing = true
    const mark = provider.creations()
    const refused = await checkout(owner)
    provider.refuseListing = false
    assert.equal(refused.status, 502, `a page was opened unchecked: ${JSON.stringify(refused.body)}`)
    assert.match(messageOf(refused.body), /Nothing was charged/)
    assert.equal(provider.creations() - mark, 0, 'a page was opened while Stripe could not be asked')
    assert.deepEqual(await payable(customerId), [])
  })

  // -------------------------------------------------------------------------
  // Paying, and the webhook that says so
  // -------------------------------------------------------------------------

  it('a completed page refuses another purchase even before its webhook arrives', async () => {
    // The buyer is back from Stripe and the delivery carrying the subscription
    // has not landed, so the local tables know nothing. The page itself is the
    // record, and it is asked directly.
    const { org, owner, customerId } = await returningBuyer('paid-no-webhook')
    const first = await checkout(owner)
    const page = sessionIdOf(first.body)!
    provider.paid(page, 'sub_paid_no_webhook')
    // The listing is EMPTY, which is the state that makes this cell worth
    // having: Stripe's collection has not caught up with the purchase, so the
    // guard above this one passes and the only thing that knows is the page.
    provider.listed = []
    provider.objects.set(
      'sub_paid_no_webhook',
      subscriptionObject({ id: 'sub_paid_no_webhook', customer: customerId, status: 'active' }),
    )

    const mark = provider.creations()
    const again = await checkout(owner)
    assert.equal(again.status, 412, `a second purchase was allowed: ${JSON.stringify(again.body)}`)
    assert.match(messageOf(again.body), /already completed a checkout/)
    assert.equal(provider.creations() - mark, 0)
    assert.deepEqual(await payable(customerId), [], 'a payable page was left behind the refusal')

    // AND IT NEVER ARRIVES. A day on, past the twenty four hours a checkout page
    // lives and past every window this route has ever keyed on, the delivery is
    // still missing and the completed page is still the record. A fresh sign in,
    // because the first one does not last a day.
    h.clock.advance(25 * 60 * 60 * 1000)
    const tomorrow = await checkout(await signInAs(h, org, 'owner', 'tomorrow'))
    assert.equal(tomorrow.status, 412, `a purchase was allowed a day later: ${JSON.stringify(tomorrow.body)}`)
    assert.match(messageOf(tomorrow.body), /already completed a checkout/)
    assert.equal(provider.creations() - mark, 0)
  })

  it('a subscription webhook arriving after a second press leaves one page and one purchase', async () => {
    // The ordering triage asked for: the delivery for the first page arrives
    // after somebody has pressed again. The second press never opened a second
    // page, so the delivery lands on one purchase and the press after it is
    // refused.
    const { org, owner, customerId } = await returningBuyer('webhook-late')
    const first = await checkout(owner)
    const page = sessionIdOf(first.body)!
    const second = await checkout(owner)
    assert.equal(sessionIdOf(second.body), page, 'the second press opened another page')

    provider.paid(page, 'sub_webhook_late')
    await deliver('customer.subscription.created', subscriptionObject({
      id: 'sub_webhook_late', customer: customerId, status: 'active',
    }))
    const [row] = await h.admin<{ status: string }[]>`
      SELECT status FROM subscriptions WHERE org_id = ${org.orgId}`
    assert.equal(row!.status, 'active', 'the delivery did not land')

    const third = await checkout(owner)
    assert.equal(third.status, 412, `a third press was allowed: ${JSON.stringify(third.body)}`)
    assert.deepEqual(await payable(customerId), [])
  })

  it('a subscription webhook arriving before the browser returns leaves no payable page', async () => {
    // The delivery beats the redirect, which is the ordering the console's own
    // confirmation exists for. The row arrives while the page is still open, so
    // the press that follows is refused and the page it would have been paid on
    // is closed.
    const { owner, customerId } = await returningBuyer('webhook-early')
    const first = await checkout(owner)
    const page = sessionIdOf(first.body)!
    assert.deepEqual(await payable(customerId), [page])

    await deliver('customer.subscription.created', subscriptionObject({
      id: 'sub_webhook_early', customer: customerId, status: 'active',
    }))
    provider.listed = [
      subscriptionObject({ id: 'sub_webhook_early', customer: customerId, status: 'active' }),
    ]

    const again = await checkout(owner)
    assert.equal(again.status, 412, `a second purchase was allowed: ${JSON.stringify(again.body)}`)
    assert.deepEqual(
      await payable(customerId),
      [],
      'the page opened before the subscription arrived is still payable',
    )
  })

  it('a delivery Stripe retries is applied once, and the purchase it confirmed stays refused', async () => {
    // Stripe redelivers any event it did not see a 2xx for, and a timeout on
    // this side after the commit is exactly that: the row is written and the
    // same event arrives again. The retry has to be acknowledged, so Stripe
    // stops, and has to change nothing, so the organization is left with neither
    // a second row nor a state that lets it buy again.
    const { org, owner, customerId } = await returningBuyer('retried')
    const first = await checkout(owner)
    const page = sessionIdOf(first.body)!
    provider.paid(page, 'sub_retried')
    const active = subscriptionObject({ id: 'sub_retried', customer: customerId, status: 'active' })
    provider.objects.set('sub_retried', active)

    const event = `evt_retried${randomUUID().replaceAll('-', '').slice(0, 12)}`
    await deliver('customer.subscription.created', active, event)
    await deliver('customer.subscription.created', active, event)

    const [counted] = await h.admin<{ subscriptions: string; events: string }[]>`
      SELECT (SELECT count(*) FROM subscriptions WHERE org_id = ${org.orgId}) AS subscriptions,
             (SELECT count(*) FROM billing_events WHERE stripe_event_id = ${event}) AS events`
    assert.equal(Number(counted!.subscriptions), 1, 'the retry wrote a second subscription row')
    assert.equal(Number(counted!.events), 1, 'the retry was recorded as a second event')

    const again = await checkout(owner)
    assert.equal(again.status, 412, `a purchase was allowed after the retry: ${JSON.stringify(again.body)}`)
    assert.deepEqual(await payable(customerId), [])
  })

  it('a completed page whose subscription was cancelled lets the organization buy again', async () => {
    // A purchase that is over for good is the only thing that permits another
    // one. Stripe is asked about the subscription the page created rather than
    // the local row, because the local row is what a missed delivery leaves
    // wrong.
    const { owner, customerId } = await returningBuyer('came-back')
    const first = await checkout(owner)
    const page = sessionIdOf(first.body)!
    provider.paid(page, 'sub_came_back')
    const over = subscriptionObject({ id: 'sub_came_back', customer: customerId, status: 'canceled' })
    provider.listed = [over]
    provider.objects.set('sub_came_back', over)

    const again = await checkout(owner)
    // Cancelled at Stripe and cancelled in the page's own subscription: the
    // organization is allowed to come back.
    assert.equal(again.status, 200, `a returning customer could not buy: ${JSON.stringify(again.body)}`)
    const open = await payable(customerId)
    assert.deepEqual(open, [sessionIdOf(again.body)])
  })

  it('a completed page whose subscription Stripe has paused refuses, even when the listing is empty', async () => {
    // Ported from the unmerged 2026-09-04 attempt. Paused can resume, so the
    // subscription a paid page created being paused is not a purchase that is
    // over, and a listing that has not caught up must not make it look like one.
    const { owner, customerId } = await returningBuyer('paused-linked')
    const page = sessionIdOf((await checkout(owner)).body)!
    provider.paid(page, 'sub_paused_linked')
    provider.listed = []
    provider.objects.set(
      'sub_paused_linked',
      subscriptionObject({ id: 'sub_paused_linked', customer: customerId, status: 'paused' }),
    )
    const mark = provider.creations()
    const again = await checkout(owner)
    assert.equal(again.status, 412, `a purchase was sold over a paused subscription: ${JSON.stringify(again.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it("a cancelled subscription belonging to another customer does not free this organization's paid page", async () => {
    const { owner, customerId } = await returningBuyer('other-customer')
    const page = sessionIdOf((await checkout(owner)).body)!
    provider.paid(page, 'sub_other_customer')
    provider.listed = []
    provider.objects.set(
      'sub_other_customer',
      subscriptionObject({ id: 'sub_other_customer', customer: `${customerId}x`, status: 'canceled' }),
    )
    const mark = provider.creations()
    const again = await checkout(owner)
    assert.equal(again.status, 412, `another customer's cancellation freed this page: ${JSON.stringify(again.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it("a different subscription returned for the page's subscription does not free the paid page", async () => {
    const { owner, customerId } = await returningBuyer('other-subscription')
    const page = sessionIdOf((await checkout(owner)).body)!
    provider.paid(page, 'sub_asked_about')
    provider.listed = []
    provider.objects.set(
      'sub_asked_about',
      subscriptionObject({ id: 'sub_something_else', customer: customerId, status: 'canceled' }),
    )
    const mark = provider.creations()
    const again = await checkout(owner)
    assert.equal(again.status, 412, `an answer about another subscription freed this page: ${JSON.stringify(again.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it('a different session returned for the recorded page is refused, not acted on', async () => {
    const { owner, customerId } = await returningBuyer('other-session')
    const page = sessionIdOf((await checkout(owner)).body)!
    provider.sessionReads.set(page, {
      id: 'cs_someone_else', object: 'checkout.session', customer: customerId,
      status: 'expired', url: null, subscription: null, metadata: {},
    })
    const mark = provider.creations()
    const again = await checkout(owner)
    assert.notEqual(again.status, 200, `a page was opened on another session's answer: ${JSON.stringify(again.body)}`)
    assert.equal(provider.creations() - mark, 0, 'a new page was created from an answer about a different session')
    assert.deepEqual(await payable(customerId), [page], 'the recorded page stopped being the only payable one')
  })

  it('a recorded page Stripe no longer knows is refused for reconciliation, not replaced', async () => {
    // Ported, and a change of direction: this used to be replaced. A 404 is also
    // what a rotated account key answers, so the page may still be payable
    // where it was opened, and a replacement would be the second payable page.
    const { owner } = await returningBuyer('forgotten')
    const page = sessionIdOf((await checkout(owner)).body)!
    provider.sessionReads.set(page, null)
    const mark = provider.creations()
    const again = await checkout(owner)
    assert.equal(again.status, 412, `a replacement was opened: ${JSON.stringify(again.body)}`)
    assert.match(messageOf(again.body), /Refresh from Stripe/)
    assert.equal(provider.creations() - mark, 0)
  })

  it('Refresh from Stripe clears a recorded page Stripe no longer knows, and the next press opens one', async () => {
    // The path the refusal above names, driven end to end.
    const { owner } = await returningBuyer('refresh-clears')
    const page = sessionIdOf((await checkout(owner)).body)!
    provider.sessionReads.set(page, null)
    assert.equal((await checkout(owner)).status, 412, 'the forgotten page was not refused first')
    const refreshed = await callProcedure(h, owner, 'subscriptions.reconcile', 'mutation', {})
    assert.equal(refreshed.status, 200, JSON.stringify(refreshed.body))
    const mark = provider.creations()
    const again = await checkout(owner)
    assert.equal(again.status, 200, `Refresh from Stripe did not clear the forgotten page: ${JSON.stringify(again.body)}`)
    assert.equal(provider.creations() - mark, 1)
  })

  it('a Stripe the control plane cannot reach tells the buyer nothing was charged', async () => {
    // The sentence the dogfood workflow an-operator-who-is-also-a-customer-can-start-checkout
    // requires in antifailure.yaml, asserted where it is produced. A connection
    // that got no answer is a StripeError, so the route answers a gateway
    // refusal a buyer can read rather than the fixed internal error sentence.
    const { org, owner } = await freshOrg('unreachable')
    provider.unreachableCustomers = true
    const mark = provider.creations()
    const result = await checkout(owner)
    provider.unreachableCustomers = false
    assert.equal(result.status, 502, JSON.stringify(result.body))
    assert.equal(messageOf(result.body), 'Stripe would not create a customer for this organization. Nothing was charged and nothing was changed.')
    assert.equal(provider.creations() - mark, 0)
    const rows = await h.admin`SELECT 1 FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.equal(rows.length, 0, 'a customer row was written for a customer Stripe never created')
  })

  it('two presses while Stripe cannot be reached refuse the same way and leave nothing behind', async () => {
    // The ordering the dogfood runner took on the plan page: Subscribe, then
    // Subscribe again, with api.stripe.com refused. The second press must meet
    // the same refusal as the first, not a checkout the first half recorded.
    const { org, owner } = await freshOrg('press-twice-unreachable')
    provider.unreachableCustomers = true
    const mark = provider.creations()
    const first = await checkout(owner)
    const second = await checkout(owner)
    provider.unreachableCustomers = false
    for (const [label, result] of [['first', first], ['second', second]] as const) {
      assert.equal(result.status, 502, `${label} press: ${JSON.stringify(result.body)}`)
      assert.equal(messageOf(result.body), 'Stripe would not create a customer for this organization. Nothing was charged and nothing was changed.', `${label} press showed another message`)
    }
    assert.equal(provider.creations() - mark, 0)
    const customers = await h.admin`SELECT 1 FROM billing_customers WHERE org_id = ${org.orgId}`
    assert.equal(customers.length, 0, 'a customer row was written for a customer Stripe never created')
    assert.equal(await attemptOf(org.orgId), null, 'a purchase attempt was claimed before a customer existed')
  })

  // -------------------------------------------------------------------------
  // The strict subscription read the guard trusts
  // -------------------------------------------------------------------------

  it('a live subscription on the second page of the listing refuses the purchase', async () => {
    // Made in the billing portal, so no checkout page records it, and past the
    // first page, which is all the screens' listing reads.
    const { owner, customerId } = await returningBuyer('page-two')
    provider.listedPages = [
      [subscriptionObject({ id: 'sub_page_one', customer: customerId, status: 'canceled' })],
      [subscriptionObject({ id: 'sub_page_two', customer: customerId, status: 'active' })],
    ]
    const mark = provider.creations()
    const result = await checkout(owner)
    assert.equal(result.status, 412, `a subscription on page two was not seen: ${JSON.stringify(result.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it('an unreadable subscription of this customer refuses the purchase rather than being skipped', async () => {
    const { owner, customerId } = await returningBuyer('unreadable')
    provider.listed = [{ object: 'subscription', customer: customerId, status: 'active' }]
    const mark = provider.creations()
    const result = await checkout(owner)
    assert.notEqual(result.status, 200, `an unreadable subscription read as none: ${JSON.stringify(result.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it('a subscription listing that answers 404 refuses the purchase', async () => {
    const { owner } = await returningBuyer('listing-404')
    provider.listed404 = true
    const mark = provider.creations()
    const result = await checkout(owner)
    assert.notEqual(result.status, 200, `a 404 listing read as no subscription: ${JSON.stringify(result.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it('a subscription listing that fails part way through its pages refuses the purchase', async () => {
    const { owner, customerId } = await returningBuyer('mid-page')
    provider.listedPages = [
      [subscriptionObject({ id: 'sub_mid_one', customer: customerId, status: 'canceled' })],
      [subscriptionObject({ id: 'sub_mid_two', customer: customerId, status: 'active' })],
    ]
    provider.failOnPage = 1
    const mark = provider.creations()
    const result = await checkout(owner)
    assert.notEqual(result.status, 200, `a half read listing authorized a purchase: ${JSON.stringify(result.body)}`)
    assert.equal(provider.creations() - mark, 0)
  })

  it('the listing the screens render still skips an unreadable row and shows the rest', async () => {
    const { config } = await stripeAgainstMockPack()
    const client = new RealStripeClient({
      ...config,
      fetch: async () =>
        new Response(JSON.stringify({
          object: 'list', has_more: false,
          data: [
            { object: 'subscription', customer: 'cus_screen', status: 'active' },
            subscriptionObject({ id: 'sub_readable', customer: 'cus_screen', status: 'active' }),
          ],
        }), { status: 200, headers: { 'content-type': 'application/json' } }),
    })
    const shown = await client.listSubscriptions('cus_screen', 10)
    assert.deepEqual(shown.map((s) => s.id), ['sub_readable'], 'one bad row blanked the screen')
  })

  // -------------------------------------------------------------------------
  // Opened while a subscription already exists
  // -------------------------------------------------------------------------

  it('a paused subscription at Stripe refuses a second purchase, because it can resume', async () => {
    // THE SECOND DEFECT. `paused` was in neither list the route consulted, so
    // this returned 200 and a session id against main.
    const { owner, customerId } = await returningBuyer('paused-remote')
    provider.listed = [
      subscriptionObject({ id: 'sub_paused_remote', customer: customerId, status: 'paused' }),
    ]
    const mark = provider.creations()
    const result = await checkout(owner)
    assert.equal(
      result.status,
      412,
      `a second subscription was sold over a paused one: ${JSON.stringify(result.body)}`,
    )
    assert.equal(errorCode(result.body), 'PRECONDITION_FAILED')
    assert.match(messageOf(result.body), /paused/, 'the refusal does not name the paused state')
    assert.match(messageOf(result.body), /card/, 'the refusal does not say how to resume it')
    assert.equal(provider.creations() - mark, 0)
  })

  it('a paused subscription this database already holds refuses a second purchase', async () => {
    const { org, owner, customerId } = await returningBuyer('paused-local')
    await h.admin`
      INSERT INTO subscriptions (org_id, stripe_subscription_id, stripe_customer_id, plan, status,
                                 price_id, quantity, last_event_at)
      VALUES (${org.orgId}, 'sub_paused_local', ${customerId}, 'team',
              'paused', 'price_team_afmock', 1, now())`
    const result = await checkout(owner)
    assert.equal(
      result.status,
      412,
      `a second subscription was sold over a paused row: ${JSON.stringify(result.body)}`,
    )
    assert.match(messageOf(result.body), /paused/)
  })

  it('a subscription cancelling at period end still refuses a second purchase', async () => {
    // Stripe leaves the status `active` until the period actually ends, and the
    // customer keeps what they paid for until then. Selling them a second plan
    // on top of it would charge twice for the same month.
    const { owner, customerId } = await returningBuyer('ending')
    provider.listed = [
      subscriptionObject({
        id: 'sub_ending', customer: customerId, status: 'active', cancelAtPeriodEnd: true,
      }),
    ]
    const result = await checkout(owner)
    assert.equal(result.status, 412, `a second purchase was allowed: ${JSON.stringify(result.body)}`)
  })

  it('every status that counts as live refuses a purchase, so the two lists cannot drift', async () => {
    // THE GATE FOR HOW `paused` WAS MISSED. LIVE_STATUSES said which statuses
    // block a purchase and refusalFromStripe decided it again in its own words,
    // and a status could be added to one and forgotten in the other with
    // nothing failing. Now the lists have to agree.
    const now = new Date('2026-09-12T00:00:00.000Z')
    const subscription = (status: string): StripeSubscription => ({
      id: `sub_${status}`,
      customerId: 'cus_drift',
      createdAt: now,
      itemId: 'si_drift',
      status,
      priceId: 'price_team_afmock',
      quantity: 1,
      currentPeriodStart: now,
      currentPeriodEnd: now,
      cancelAtPeriodEnd: false,
      canceledAt: null,
    })
    assert.ok(LIVE_STATUSES.includes('paused'), 'paused is not counted as live')
    for (const status of LIVE_STATUSES) {
      assert.ok(
        refusalFromStripe([subscription(status)], now) !== null,
        `a ${status} subscription at Stripe does not refuse a second purchase`,
      )
    }
    for (const status of TERMINAL_STATUSES) {
      assert.ok(
        !LIVE_STATUSES.includes(status),
        `${status} is in both the live and the terminal list, so a purchase both refuses and replaces`,
      )
      assert.equal(
        refusalFromStripe([subscription(status)], now),
        null,
        `a ${status} subscription refuses a purchase, so a customer who left cannot come back`,
      )
    }
  })

  it('the open session listing passes over other customers and refuses an unreadable page of this one', async () => {
    // Strict about this customer's pages and indifferent to everybody else's. A
    // session for another customer cannot be this customer's second charge, so
    // its shape must not be able to refuse this purchase: that is one bad
    // element blanking the feature. A page of THIS customer's that cannot be
    // read is the opposite case, because it may be payable, so it stops the
    // purchase.
    const { config } = await stripeAgainstMockPack()
    const listing = (data: unknown[]) =>
      new RealStripeClient({
        ...config,
        fetch: async () =>
          new Response(JSON.stringify({ object: 'list', data, has_more: false }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          }),
      })
    const mine = {
      id: 'cs_mine', object: 'checkout.session', customer: 'cus_mine', status: 'open',
      url: 'https://checkout.stripe.com/c/pay/cs_mine',
    }
    const found = await listing([
      { id: 'cs_guest', object: 'checkout.session', customer: null, status: 'open', url: null },
      { id: 'cs_other', object: 'checkout.session', customer: 'cus_other', status: 'mystery' },
      mine,
    ]).listOpenCheckoutSessions('cus_mine')
    assert.deepEqual(found.map((s) => s.id), ['cs_mine'])

    await assert.rejects(
      listing([{ ...mine, status: 'mystery' }]).listOpenCheckoutSessions('cus_mine'),
      /cannot be verified/,
      'an unreadable page of this customer was treated as not there',
    )
  })

  // -------------------------------------------------------------------------
  // The rows this leaves behind
  // -------------------------------------------------------------------------

  it('deleting the organization takes its purchase attempt with it', async () => {
    // The attempt row is reachable only through the organization, and the app
    // role holds no DELETE on it, so the cascade is the only thing that removes
    // it. A table that survived its organization would keep a Stripe customer
    // identifier for a company that no longer exists here.
    const { org, owner } = await freshOrg('cascade')
    assert.equal((await checkout(owner)).status, 200)
    assert.ok(await attemptOf(org.orgId), 'no attempt row to delete')
    await h.admin`DELETE FROM organizations WHERE id = ${org.orgId}`
    assert.equal(await attemptOf(org.orgId), null, 'the attempt row outlived its organization')
  })
})
