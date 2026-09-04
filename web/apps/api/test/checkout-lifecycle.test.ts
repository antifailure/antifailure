import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import { RealStripeClient } from '../src/billing/stripe.ts'
import { available, startApi, seedOrg, signInAs, callProcedure, dropOrg, errorCode } from './harness.ts'

const hasDatabase = await available()
const form = { plan: 'team', successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no' }

class Provider {
  sessions = new Map<string, Record<string, unknown>>()
  subscriptions = new Map<string, Record<string, unknown>>()
  keys = new Map<string, string>()
  posts: string[] = []
  loseResponse = false
  staleSubscriptionList = false
  waitForPosts = 0
  postWaiters: (() => void)[] = []
  beforeCustomer: ((id: string) => Promise<void>) | null = null

  fetch: typeof globalThis.fetch = async (input, init) => {
    const url = new URL(String(input))
    const fields = new URLSearchParams(String(init?.body ?? ''))
    const answer = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
    if (url.pathname === '/v1/customers' && init?.method === 'POST') {
      const id = `cus_${fields.get('metadata[org_id]')}`
      await this.beforeCustomer?.(id)
      return answer({ id, email: null })
    }
    if (url.pathname === '/v1/checkout/sessions' && init?.method === 'POST') {
      const key = new Headers(init.headers).get('idempotency-key') ?? randomUUID()
      this.posts.push(key)
      if (this.waitForPosts > 0) await new Promise<void>((resolve) => {
        this.postWaiters.push(resolve)
        if (this.postWaiters.length === this.waitForPosts) {
          this.waitForPosts = 0
          this.postWaiters.splice(0).forEach((release) => release())
        }
      })
      let id = this.keys.get(key)
      if (!id) {
        id = `cs_${randomUUID()}`
        this.keys.set(key, id)
        this.sessions.set(id, {
          id, customer: fields.get('customer'), status: 'open', subscription: null,
          url: `https://checkout.stripe.com/c/pay/${id}`,
          metadata: { checkout_attempt: fields.get('metadata[checkout_attempt]') },
        })
      }
      if (this.loseResponse) {
        this.loseResponse = false
        throw new Error('the response was lost after Stripe committed')
      }
      return answer(this.sessions.get(id))
    }
    if (url.pathname === '/v1/checkout/sessions') {
      return answer({ data: [...this.sessions.values()], has_more: false })
    }
    if (url.pathname.startsWith('/v1/checkout/sessions/')) {
      const session = this.sessions.get(url.pathname.split('/').at(-1)!)
      return answer(session ?? { error: { message: 'missing' } }, session ? 200 : 404)
    }
    if (url.pathname === '/v1/subscriptions') {
      return answer({ data: this.staleSubscriptionList ? [] : [...this.subscriptions.values()], has_more: false })
    }
    if (url.pathname.startsWith('/v1/subscriptions/')) {
      const subscription = this.subscriptions.get(url.pathname.split('/').at(-1)!)
      return answer(subscription ?? { error: { message: 'missing' } }, subscription ? 200 : 404)
    }
    throw new Error(`Unimplemented provider route ${url.pathname}`)
  }
}

async function fixture(run: (f: Awaited<ReturnType<typeof setup>>) => Promise<void>) {
  const f = await setup()
  try { await run(f) } finally { await dropOrg(f.h.admin, f.org.orgId); await f.h.close() }
}

async function setup() {
  const provider = new Provider()
  const config = { secretKey: 'sk_test_fixture', webhookSecret: 'whsec_fixture', prices: { team: 'price_team' }, fetch: provider.fetch }
  const h = await startApi({ stripe: { config, client: new RealStripeClient(config) } })
  const org = await seedOrg(h.admin, 'checkout')
  const owner = await signInAs(h, org, 'owner')
  const checkout = (over = {}) => callProcedure(h, owner, 'subscriptions.checkout', 'mutation', { ...form, ...over })
  const early = async (customerId: string) => {
    const body = JSON.stringify({
      id: `evt_${randomUUID()}`, type: 'customer.subscription.created', created: h.clock.now().getTime() / 1000,
      data: { object: { id: `sub_${randomUUID()}`, customer: customerId, status: 'active', items: { data: [{ price: { id: 'price_team' }, quantity: 1 }] } } },
    })
    const timestamp = Math.floor(h.clock.now().getTime() / 1000)
    const signature = createHmac('sha256', config.webhookSecret).update(`${timestamp}.${body}`).digest('hex')
    const res = await h.fetch('/webhooks/stripe', {
      method: 'POST', headers: { 'content-type': 'application/json', 'stripe-signature': `t=${timestamp},v1=${signature}` }, body,
    })
    if (res.status !== 200) throw new Error(`fixture webhook failed: ${await res.text()}`)
  }
  return { h, org, owner, provider, checkout, early }
}

describe('a checkout is one purchase attempt', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  it('simultaneous clicks return the same session', () => fixture(async ({ checkout, provider }) => {
    provider.waitForPosts = 2
    const results = await Promise.all([checkout(), checkout()])
    const id = [...provider.sessions.keys()][0]
    assert.deepEqual(results.map((r) => [r.status, (r.body as { result?: { data?: { sessionId?: string } } }).result?.data?.sessionId]), [[200, id], [200, id]])
  }))

  it('simultaneous clicks create one payable provider session', () => fixture(async ({ checkout, provider }) => {
    provider.waitForPosts = 2
    await Promise.all([checkout(), checkout()])
    assert.equal(provider.sessions.size, 1)
  }))

  it('a repeated click reopens the previous checkout instead of posting a new one', () => fixture(async ({ checkout, provider }) => {
    await checkout(); await checkout()
    assert.equal(provider.posts.length, 1)
  }))

  it('a lost response is recovered by its attempt metadata without another POST', () => fixture(async ({ checkout, provider }) => {
    provider.loseResponse = true
    await checkout()
    const result = await checkout()
    assert.deepEqual([result.status, provider.posts.length, provider.sessions.size], [200, 1, 1])
  }))

  it('a missing local session identifier is recovered after a process crash', () => fixture(async ({ checkout, provider, h, org }) => {
    await checkout()
    await h.admin`UPDATE billing_checkout_attempts SET stripe_session_id = NULL WHERE org_id = ${org.orgId}`
    await checkout()
    assert.equal(provider.posts.length, 1)
  }))

  it('an expired session permits a new attempt key', () => fixture(async ({ checkout, provider }) => {
    await checkout()
    Object.assign([...provider.sessions.values()][0]!, { status: 'expired', url: null })
    await checkout()
    assert.equal(new Set(provider.posts).size, 2)
  }))

  it('completed checkout without its subscription webhook cannot start a second purchase', () => fixture(async ({ checkout, provider }) => {
    await checkout()
    Object.assign([...provider.sessions.values()][0]!, { status: 'complete', url: null })
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('an ended linked subscription permits a returning customer to buy again', () => fixture(async ({ checkout, provider, org }) => {
    await checkout()
    const customer = `cus_${org.orgId}`
    provider.subscriptions.set('sub_ended', { id: 'sub_ended', customer, status: 'canceled', items: { data: [] } })
    Object.assign([...provider.sessions.values()][0]!, { status: 'complete', url: null, subscription: 'sub_ended' })
    await checkout()
    assert.equal(new Set(provider.posts).size, 2)
  }))

  it('a paused linked subscription cannot retire an attempt after a stale empty listing', () => fixture(async ({ checkout, provider, org }) => {
    await checkout()
    provider.subscriptions.set('sub_paused', { id: 'sub_paused', customer: `cus_${org.orgId}`, status: 'paused', items: { data: [] } })
    provider.staleSubscriptionList = true
    Object.assign([...provider.sessions.values()][0]!, { status: 'complete', url: null, subscription: 'sub_paused' })
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('a provider subscription with no local webhook refuses checkout', () => fixture(async ({ checkout, provider, org }) => {
    provider.subscriptions.set('sub_unreported', { id: 'sub_unreported', customer: `cus_${org.orgId}`, status: 'active' })
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 0])
  }))

  it('an existing provider subscription leaves no unsent checkout reservation', () => fixture(async ({ checkout, provider, org, h }) => {
    provider.subscriptions.set('sub_existing', { id: 'sub_existing', customer: `cus_${org.orgId}`, status: 'active' })
    await checkout()
    const rows = await h.admin`SELECT * FROM billing_checkout_attempts WHERE org_id = ${org.orgId}`
    assert.equal(rows.length, 0)
  }))

  it('an ended subscription belonging to another customer cannot retire this attempt', () => fixture(async ({ checkout, provider }) => {
    await checkout()
    provider.subscriptions.set('sub_foreign', { id: 'sub_foreign', customer: 'cus_foreign', status: 'canceled', items: { data: [] } })
    Object.assign([...provider.sessions.values()][0]!, { status: 'complete', url: null, subscription: 'sub_foreign' })
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('a different subscription returned for the requested identifier cannot retire this attempt', () => fixture(async ({ checkout, provider, org }) => {
    await checkout()
    provider.subscriptions.set('sub_expected', { id: 'sub_wrong', customer: `cus_${org.orgId}`, status: 'canceled', items: { data: [] } })
    Object.assign([...provider.sessions.values()][0]!, { status: 'complete', url: null, subscription: 'sub_expected' })
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('a different session returned for the requested identifier is refused', () => fixture(async ({ checkout, provider }) => {
    await checkout()
    Object.assign([...provider.sessions.values()][0]!, { id: 'cs_wrong' })
    const result = await checkout()
    assert.equal(errorCode(result.body), 'PRECONDITION_FAILED')
  }))

  it('an early webhook replayed while attaching the customer refuses checkout', () => fixture(async ({ checkout, provider, early }) => {
    provider.beforeCustomer = early
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 0])
  }))

  it('changed return URLs do not mutate the parameters of an existing attempt', () => fixture(async ({ checkout, provider }) => {
    await checkout()
    const result = await checkout({ successUrl: 'https://app.test/different' })
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('an unresolved attempt outside the idempotency window refuses a new key', () => fixture(async ({ checkout, provider, h, org }) => {
    provider.loseResponse = true
    await checkout()
    provider.sessions.clear()
    await h.admin`UPDATE billing_checkout_attempts SET created_at = created_at - interval '24 hours' WHERE org_id = ${org.orgId}`
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('a missing provider session does not authorize a replacement', () => fixture(async ({ checkout, provider }) => {
    await checkout(); provider.sessions.clear()
    const result = await checkout()
    assert.deepEqual([errorCode(result.body), provider.posts.length], ['PRECONDITION_FAILED', 1])
  }))

  it('another tenant cannot read the checkout attempt', () => fixture(async ({ checkout, h }) => {
    await checkout()
    const other = await seedOrg(h.admin, 'checkout-other')
    try {
      const rows = await h.pool.withTenant({ orgId: other.orgId }, async (db) => {
        const { sql } = await import('drizzle-orm')
        return db.execute(sql`SELECT * FROM billing_checkout_attempts`)
      })
      assert.equal(rows.length, 0)
    } finally { await dropOrg(h.admin, other.orgId) }
  }))
})

function clientWith(fetch: typeof globalThis.fetch) {
  return new RealStripeClient({ secretKey: 'sk_test_fixture', webhookSecret: 'whsec_fixture', prices: { team: 'price_team' }, fetch })
}

describe('provider state is verified before another purchase', () => {
  it('a paused subscription still blocks another purchase because it can resume', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ data: [{ id: 'sub_paused', customer: 'cus_test', status: 'paused' }], has_more: false })))
    assert.equal(await client.hasBlockingSubscription('cus_test'), true)
  })

  it('bounds the whole paginated lookup rather than only each request', async () => {
    const realNow = Date.now
    let time = 0
    let page = 0
    Date.now = () => { time += 10_000; return time }
    try {
      const client = clientWith(async () => new Response(JSON.stringify({ data: [{ id: `sub_${page++}`, customer: 'cus_test', status: 'canceled' }], has_more: true })))
      await assert.rejects(client.hasBlockingSubscription('cus_test'), /verification took too long/)
    } finally { Date.now = realNow }
  })

  it('finds an active subscription beyond the first page', async () => {
    const client = clientWith(async (input) => {
      const next = new URL(String(input)).searchParams.has('starting_after')
      return new Response(JSON.stringify({
        data: [{ id: next ? 'sub_live' : 'sub_old', customer: 'cus_test', status: next ? 'active' : 'canceled' }], has_more: !next,
      }))
    })
    assert.equal(await client.hasBlockingSubscription('cus_test'), true)
  })

  it('recovers a session beyond the first page by its exact attempt metadata', async () => {
    const client = clientWith(async (input) => {
      const next = new URL(String(input)).searchParams.get('starting_after') === 'cs_old'
      return new Response(JSON.stringify({
        data: [{ id: next ? 'cs_found' : 'cs_old', customer: 'cus_test', status: 'open', url: 'https://checkout.stripe.com/found', metadata: { checkout_attempt: next ? 'attempt' : 'other' } }], has_more: !next,
      }))
    })
    assert.equal((await client.findCheckoutAttempt('cus_test', 'attempt'))?.id, 'cs_found')
  })

  it('refuses an incomplete provider collection instead of reporting no subscriptions', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ data: [] })))
    await assert.rejects(client.hasBlockingSubscription('cus_test'), /complete billing collection/)
  })

  it('refuses an unreadable collection member before authorizing a purchase', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ data: [null], has_more: false })))
    await assert.rejects(client.hasBlockingSubscription('cus_test'), /unreadable billing record/)
  })

  it('refuses a subscription with no customer instead of treating it as another tenant', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ data: [{ id: 'sub_unknown', status: 'active' }], has_more: false })))
    await assert.rejects(client.hasBlockingSubscription('cus_test'), /no customer/)
  })

  it('does not treat an unknown subscription status as ended', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ data: [{ id: 'sub_unknown', customer: 'cus_test', status: 'new_status' }], has_more: false })))
    assert.equal(await client.hasBlockingSubscription('cus_test'), true)
  })

  it('reads a completed checkout whose URL is null', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ id: 'cs_complete', customer: 'cus_test', status: 'complete', url: null, subscription: 'sub_done' })))
    assert.equal((await client.getCheckoutSession('cs_complete'))?.status, 'complete')
  })

  it('refuses a checkout with an unknown status', async () => {
    const client = clientWith(async () => new Response(JSON.stringify({ id: 'cs_unknown', customer: 'cus_test', status: 'future', url: null })))
    await assert.rejects(client.getCheckoutSession('cs_unknown'), /state cannot be verified/)
  })
})
