import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import type postgres from 'postgres'
import type { SQL } from 'drizzle-orm'
import { PgDialect } from 'drizzle-orm/pg-core'
import type { Db, Pool } from '@antifailure/db'
import { handleStripeDelivery, recomputePlan, writeInvoice, writeSubscription } from '../src/billing/webhook.ts'
import { attachCustomer, readBillingState } from '../src/billing/store.ts'
import { invoiceOf, subscriptionOf } from '../src/billing/stripe.ts'
import { available, startApi, seedOrg, dropOrg, stripeAgainstMockPack, type ApiHarness } from './harness.ts'
import type { StripeConfig } from '../src/billing/plans.ts'

const hasDatabase = await available()

describe('billing across subscription arrival orders', { skip: !hasDatabase }, () => {
  let h: ApiHarness
  let config: StripeConfig
  const orgIds: string[] = []
  before(async () => {
    const stripe = await stripeAgainstMockPack()
    config = stripe.config
    h = await startApi({ stripe: stripe.billing })
  })
  after(async () => {
    for (const orgId of orgIds) await dropOrg(h.admin, orgId)
    await h.close()
  })

  async function fixture(attached = true) {
    const org = await seedOrg(h.admin, 'billing-order')
    orgIds.push(org.orgId)
    const customer = `cus_${randomUUID()}`
    if (attached) await h.admin`INSERT INTO billing_customers (org_id, stripe_customer_id) VALUES (${org.orgId}, ${customer})`
    return { orgId: org.orgId, customer }
  }

  function object(customer: string, id: string, created: number, plan = 'team', status = 'active') {
    return {
      id, customer, created, status,
      items: { data: [{ id: 'si_order', price: `price_${plan}_afmock`, quantity: 1 }] },
    }
  }

  async function deliver(body: ReturnType<typeof object>) {
    await handleStripeDelivery(h.pool, h.clock, config, {
      id: `evt_${randomUUID()}`, type: 'customer.subscription.updated',
      created: h.clock.now(), object: body,
    }, h.analytics)
  }

  async function plan(orgId: string) {
    const [row] = await h.admin`SELECT plan FROM organizations WHERE id = ${orgId}`
    return row!.plan
  }

  for (const reverse of [false, true]) {
    it(`keeps the newest entitling subscription when arrival is ${reverse ? 'new then old' : 'old then new'}`, async () => {
      const f = await fixture()
      const bodies = [object(f.customer, `sub_${randomUUID()}`, 100, 'team'), object(f.customer, `sub_${randomUUID()}`, 200, 'enterprise')]
      for (const body of reverse ? bodies.reverse() : bodies) await deliver(body)
      assert.equal(await plan(f.orgId), 'enterprise')
    })
  }

  it('a newer canceled subscription cannot remove an older active entitlement', async () => {
    const f = await fixture()
    await deliver(object(f.customer, `sub_${randomUUID()}`, 100, 'enterprise'))
    await deliver(object(f.customer, `sub_${randomUUID()}`, 200, 'team', 'canceled'))
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('the current subscription is the live one rather than a newer ended purchase', async () => {
    const f = await fixture()
    const live = `sub_${randomUUID()}`
    await deliver(object(f.customer, live, 100))
    await deliver(object(f.customer, `sub_${randomUUID()}`, 200, 'enterprise', 'canceled'))
    const state = await h.pool.withTenant({ orgId: f.orgId }, db => readBillingState(db, f.orgId))
    assert.equal(state.subscription?.id, live)
  })

  it('equal provider creation times have a stable provider identifier tie break', async () => {
    const f = await fixture()
    const prefix = randomUUID()
    await deliver(object(f.customer, `sub_z_${prefix}`, 100, 'enterprise'))
    await deliver(object(f.customer, `sub_a_${prefix}`, 100, 'team'))
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('a newly observed unknown price does not downgrade an existing paid organization', async () => {
    const f = await fixture()
    await h.admin`UPDATE organizations SET plan = 'enterprise' WHERE id = ${f.orgId}`
    await deliver(object(f.customer, `sub_${randomUUID()}`, 100, 'unknown'))
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('a newer unknown price cannot hide an older known active plan', async () => {
    const f = await fixture()
    await deliver(object(f.customer, `sub_${randomUUID()}`, 200, 'unknown'))
    await deliver(object(f.customer, `sub_${randomUUID()}`, 100, 'enterprise'))
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('an older unknown active purchase is preserved behind a newer ended purchase', async () => {
    const f = await fixture()
    await h.admin`UPDATE organizations SET plan = 'enterprise' WHERE id = ${f.orgId}`
    await deliver(object(f.customer, `sub_${randomUUID()}`, 100, 'unknown'))
    await deliver(object(f.customer, `sub_${randomUUID()}`, 200, 'team', 'canceled'))
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('forced overlap of initial attachment and webhook resolves the paid purchase', async () => {
    const f = await fixture(false)
    const inserted = barrier()
    const continueWebhook = barrier()
    const continueAttach = barrier()
    let pendingRead = false
    const watched: Pool = {
      ...h.pool,
      withStripeCustomer: (customer, fn) => h.pool.withStripeCustomer(customer, db =>
        fn(afterQuery(db, async statement => {
          if (statement.includes('INSERT INTO billing_events')) {
            inserted.release()
            await continueWebhook.promise
          }
        }))),
    }
    const webhook = handleStripeDelivery(watched, h.clock, config, {
      id: `evt_${randomUUID()}`, type: 'customer.subscription.created', created: h.clock.now(),
      object: object(f.customer, `sub_${randomUUID()}`, 100, 'enterprise'),
    }, h.analytics)
    await inserted.promise
    const attachment = h.pool.withTenant({ orgId: f.orgId }, db => attachCustomer(
      afterQuery(db, async statement => {
        if (statement.includes('FROM billing_events')) {
          pendingRead = true
          await continueAttach.promise
        }
      }), h.clock, config, f.orgId, { id: f.customer, email: null }, h.analytics,
    ))
    try {
      // With serialization, attachment waits on the customer lock. Without
      // either call site, its pending SELECT sees no committed webhook row.
      for (let i = 0; i < 200 && !pendingRead; i++) {
        const waits = await h.admin`SELECT pid FROM pg_stat_activity WHERE datname = current_database() AND usename = 'antifailure_app' AND wait_event = 'advisory'`
        if (waits.length) break
        await new Promise(resolve => setTimeout(resolve, 5))
        if (i === 199) throw new Error('Attachment reached neither the lock nor its pending-event read')
      }
      continueWebhook.release()
      await webhook
    } finally {
      continueWebhook.release()
      continueAttach.release()
      await Promise.all([webhook, attachment])
    }
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('a later delivery repairs the creation time stored by older application code', async () => {
    const f = await fixture()
    const body = object(f.customer, `sub_${randomUUID()}`, 100)
    await deliver(body)
    await h.admin`UPDATE subscriptions SET created_at = '2026-01-01', last_event_at = '2025-01-01' WHERE stripe_subscription_id = ${body.id}`
    await deliver(body)
    const [row] = await h.admin`SELECT extract(epoch FROM created_at)::int AS seconds FROM subscriptions WHERE stripe_subscription_id = ${body.id}`
    assert.equal(row!.seconds, 100)
  })

  it('a payload without provider creation preserves the previously known creation time', async () => {
    const f = await fixture()
    const body = object(f.customer, `sub_${randomUUID()}`, 100)
    await deliver(body)
    await h.admin`UPDATE subscriptions SET last_event_at = '2025-01-01' WHERE stripe_subscription_id = ${body.id}`
    await deliver({ ...body, created: undefined } as unknown as ReturnType<typeof object>)
    const [row] = await h.admin`SELECT extract(epoch FROM created_at)::int AS seconds FROM subscriptions WHERE stripe_subscription_id = ${body.id}`
    assert.equal(row!.seconds, 100)
  })

  it('recomputing waits for the organization lock before reading the deciding subscription', async () => {
    const f = await fixture()
    const body = object(f.customer, `sub_${randomUUID()}`, 100)
    await deliver(body)
    let release!: () => void
    let locked!: () => void
    const lockReady = new Promise<void>(resolve => { locked = resolve })
    const mayCommit = new Promise<void>(resolve => { release = resolve })
    const writer = h.admin.begin(async tx => {
      await tx`SELECT id FROM organizations WHERE id = ${f.orgId} FOR UPDATE`
      await tx`UPDATE subscriptions SET plan = 'enterprise' WHERE stripe_subscription_id = ${body.id}`
      locked()
      await mayCommit
    })
    await lockReady
    const reader = h.pool.withTenant({ orgId: f.orgId }, db => recomputePlan(db, h.clock, f.orgId, h.analytics))
    try {
      await waitForBlockedQuery()
    } finally { release() }
    await Promise.all([writer, reader])
    assert.equal(await plan(f.orgId), 'enterprise')
  })

  it('subscription writes wait for the organization before holding any subscription row lock', async () => {
    const f = await fixture()
    const body = object(f.customer, `sub_${randomUUID()}`, 100)
    await deliver(body)
    await h.admin`UPDATE subscriptions SET last_event_at = '2025-01-01' WHERE stripe_subscription_id = ${body.id}`
    let release!: () => void
    let locked!: () => void
    const lockReady = new Promise<void>(resolve => { locked = resolve })
    const mayInspect = new Promise<void>(resolve => { release = resolve })
    const holder = h.admin.begin(async tx => {
      await tx`SELECT id FROM organizations WHERE id = ${f.orgId} FOR UPDATE`
      locked()
      await mayInspect
      await tx`SELECT id FROM subscriptions WHERE stripe_subscription_id = ${body.id} FOR UPDATE NOWAIT`
    })
    await lockReady
    const writer = h.pool.withTenant({ orgId: f.orgId }, async db => {
      await writeSubscription(db, h.clock, config, f.orgId, subscriptionOf(body), h.clock.now(), h.analytics)
      await recomputePlan(db, h.clock, f.orgId, h.analytics)
    })
    try { await waitForBlockedQuery() } finally { release() }
    await assert.doesNotReject(Promise.all([holder, writer]))
  })

  it('invoice repair takes the organization before an existing invoice lock', async () => {
    const f = await fixture()
    const id = `in_${randomUUID()}`
    await h.admin`INSERT INTO invoices (org_id, stripe_invoice_id, stripe_customer_id, status, last_event_at) VALUES (${f.orgId}, ${id}, ${f.customer}, 'open', '2025-01-01')`
    await assert.doesNotReject(holdingOrganization(f.orgId, async tx => {
      await tx`SELECT id FROM invoices WHERE stripe_invoice_id = ${id} FOR UPDATE NOWAIT`
    }, () => h.pool.withTenant({ orgId: f.orgId }, async db => {
      await writeInvoice(db, h.clock, f.orgId, invoiceOf({ id, customer: f.customer, status: 'paid' })!, h.clock.now())
      await recomputePlan(db, h.clock, f.orgId, h.analytics)
    })))
  })

  it('a payment-method delivery takes the organization before its entity lock and event foreign key', async () => {
    const f = await fixture()
    const id = `pm_${randomUUID()}`
    await h.admin`INSERT INTO payment_methods (org_id, stripe_payment_method_id, stripe_customer_id, last_event_at) VALUES (${f.orgId}, ${id}, ${f.customer}, '2025-01-01')`
    await assert.doesNotReject(holdingOrganization(f.orgId, async tx => {
      await tx`SELECT id FROM payment_methods WHERE stripe_payment_method_id = ${id} FOR UPDATE NOWAIT`
    }, () => handleStripeDelivery(h.pool, h.clock, config, {
      id: `evt_${randomUUID()}`, type: 'payment_method.detached', created: h.clock.now(), object: { id, customer: f.customer },
    }, h.analytics)))
  })

  async function holdingOrganization(orgId: string, inspect: (tx: postgres.TransactionSql) => Promise<void>, operation: () => Promise<unknown>) {
    const locked = barrier()
    const inspectNow = barrier()
    const holder = h.admin.begin(async tx => {
      await tx`SELECT id FROM organizations WHERE id = ${orgId} FOR UPDATE`
      locked.release()
      await inspectNow.promise
      await inspect(tx)
    })
    await locked.promise
    const writer = operation()
    try { await waitForBlockedQuery() } finally { inspectNow.release() }
    await Promise.all([holder, writer])
  }

  function barrier() {
    let release!: () => void
    const promise = new Promise<void>(resolve => { release = resolve })
    return { promise, release }
  }

  function afterQuery(db: Db, after: (statement: string) => Promise<void>): Db {
    return new Proxy(db, {
      get(target, property, receiver) {
        if (property === 'execute') return async (query: SQL) => {
          const result = await target.execute(query)
          await after(new PgDialect().sqlToQuery(query).sql)
          return result
        }
        const value = Reflect.get(target, property, receiver)
        return typeof value === 'function' ? value.bind(target) : value
      },
    })
  }

  async function waitForBlockedQuery() {
    for (let attempt = 0; attempt < 200; attempt++) {
      const rows = await h.admin`SELECT pid FROM pg_stat_activity WHERE datname = current_database() AND usename = 'antifailure_app' AND wait_event_type = 'Lock'`
      if (rows.length) return
      await new Promise(resolve => setTimeout(resolve, 5))
    }
    throw new Error('The contender never reached the real PostgreSQL lock')
  }
})
