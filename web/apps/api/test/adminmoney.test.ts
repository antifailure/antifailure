// Proving that a refund happens once.
//
// The simulator below is not a stub that returns a canned refund. It
// implements Stripe's actual idempotency contract, because that contract is
// half of the guarantee under test and a double that ignored it would let
// every one of these tests pass against code that has no guarantee at all:
//
//   - A repeated key with the SAME parameters replays the first response and
//     creates nothing.
//   - A repeated key with DIFFERENT parameters is refused, 400
//     idempotency_key_in_use.
//   - A request with no key at all creates a new object every time. That is
//     what makes the "without a key it would have refunded twice" test mean
//     something rather than being an assertion about the simulator.
//
// It also COUNTS, per path, so every test here can assert on how many times
// Stripe was actually reached rather than on what came back. A design that
// returned the right answer while calling the provider twice would pass every
// value assertion and fail the only one that matters.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createPool, sql, type Db, type Pool } from '@antifailure/db'
import { RealStripeClient } from '../src/billing/stripe.ts'
import type { StripeConfig } from '../src/billing/plans.ts'
import {
  cancelSubscription,
  changePlan,
  creditCustomer,
  extendTrial,
  money,
  refundCharge,
  retryPayment,
} from '../src/admin/money.ts'
import { IN_FLIGHT_GRACE_MS, fingerprint, keyFor } from '../src/admin/ledger.ts'
import { available, adminUrl, dropOrg, seedOrg, startApi, type ApiHarness, type Org } from './harness.ts'

// ---------------------------------------------------------------------------
// The simulator
// ---------------------------------------------------------------------------

interface Recorded { status: number; body: string }

class StripeSim {
  /** Every request, in order, as `METHOD /path`. */
  readonly calls: string[] = []
  /** Keyed by idempotency key: the answer already given, and the fingerprint
   *  of the request it was given for. */
  private readonly keys = new Map<string, { body: string; print: string; status: number }>()
  private seq = 0
  /** Objects that exist, so a second CREATE is visible as a second object
   *  rather than only as a second call. */
  readonly refunds: { id: string; charge: string; amount: number }[] = []
  readonly credits: { id: string; customer: string; amount: number }[] = []
  /** Set to fail the next N create calls, for the failure and recovery tests. */
  failNext = 0
  /** Set to hold the next create open until released, for the concurrency test. */
  private gate: Promise<void> | null = null
  private release: (() => void) | null = null

  openGate(): void {
    this.gate = new Promise((resolve) => {
      this.release = resolve
    })
  }

  closeGate(): void {
    this.release?.()
    this.gate = null
    this.release = null
  }

  readonly fetch: typeof globalThis.fetch = async (input, init): Promise<Response> => {
    const url = new URL(typeof input === 'string' ? input : (input as URL).toString())
    const method = init?.method ?? 'GET'
    const path = url.pathname
    this.calls.push(`${method} ${path}`)

    const body = typeof init?.body === 'string' ? init.body : ''
    const key = headerOf(init, 'idempotency-key')

    if (method === 'POST' && key) {
      const print = `${path}|${body}`
      const held = this.keys.get(key)
      if (held) {
        if (held.print !== print) {
          // Stripe's own refusal. Reproduced so that the ledger's fingerprint
          // check is not the ONLY thing standing between a reused key and a
          // wrong answer.
          const refusal = json(400, {
            error: {
              type: 'idempotency_error',
              code: 'idempotency_key_in_use',
              message: 'Keys for idempotent requests can only be used with the same parameters.',
            },
          })
          return new Response(refusal.body, {
            status: refusal.status,
            headers: { 'content-type': 'application/json' },
          })
        }
        // The replay. Nothing new is created; the first answer comes back.
        return new Response(held.body, {
          status: held.status,
          headers: { 'content-type': 'application/json' },
        })
      }
      if (this.gate) await this.gate
      const made = this.create(path, body)
      this.keys.set(key, { body: made.body, print, status: made.status })
      return new Response(made.body, {
        status: made.status,
        headers: { 'content-type': 'application/json' },
      })
    }

    if (method === 'POST') {
      if (this.gate) await this.gate
      const made = this.create(path, body)
      return new Response(made.body, {
        status: made.status,
        headers: { 'content-type': 'application/json' },
      })
    }

    return this.read(path)
  }

  private create(path: string, body: string): Recorded {
    if (this.failNext > 0) {
      this.failNext -= 1
      return json(402, {
        error: { type: 'card_error', code: 'card_declined', message: 'Your card was declined.' },
      })
    }
    const form = new URLSearchParams(body)
    this.seq += 1
    if (path === '/v1/refunds') {
      const id = `re_${this.seq}`
      const charge = form.get('charge') ?? ''
      const amount = Number(form.get('amount') ?? 5000)
      this.refunds.push({ id, charge, amount })
      return json(200, {
        id, object: 'refund', charge, payment_intent: null,
        amount, currency: 'usd', status: 'succeeded', reason: form.get('reason'),
      })
    }
    if (/^\/v1\/customers\/[^/]+\/balance_transactions$/.test(path)) {
      const id = `cbtxn_${this.seq}`
      const customer = path.split('/')[3]!
      const amount = Number(form.get('amount') ?? 0)
      this.credits.push({ id, customer, amount })
      return json(200, {
        id, object: 'customer_balance_transaction', customer, amount,
        currency: form.get('currency') ?? 'usd',
        ending_balance: this.credits
          .filter((c) => c.customer === customer)
          .reduce((n, c) => n + c.amount, 0),
        description: form.get('description'),
      })
    }
    if (/^\/v1\/subscriptions\/[^/]+$/.test(path)) {
      return json(200, subscription(path.split('/')[3]!, {
        price: form.get('items[0][price]'),
        cancel: form.get('cancel_at_period_end') === 'true',
        trialEnd: form.get('trial_end'),
      }))
    }
    if (/^\/v1\/invoices\/[^/]+\/pay$/.test(path)) {
      const id = path.split('/')[3]!
      return json(200, {
        id, object: 'invoice', customer: 'cus_1', subscription: 'sub_1', number: 'AF-1',
        status: 'paid', amount_due: 5000, amount_paid: 5000, currency: 'usd',
      })
    }
    return json(404, { error: { message: `no route for ${path}` } })
  }

  private read(path: string): Response {
    if (/^\/v1\/charges\/[^/]+$/.test(path)) {
      const id = path.split('/')[3]!
      const refunded = this.refunds.filter((r) => r.charge === id).reduce((n, r) => n + r.amount, 0)
      return new Response(JSON.stringify({
        id, object: 'charge', amount: 5000, amount_refunded: refunded, currency: 'usd',
        status: 'succeeded', paid: true, refunded: refunded >= 5000, disputed: false,
        created: 1767225600, invoice: 'in_1', failure_message: null,
      }), { status: 200, headers: { 'content-type': 'application/json' } })
    }
    if (/^\/v1\/customers\/[^/]+$/.test(path)) {
      const id = path.split('/')[3]!
      const balance = this.credits.filter((c) => c.customer === id).reduce((n, c) => n + c.amount, 0)
      return new Response(JSON.stringify({
        id, object: 'customer', email: 'finance@example.test', balance,
        currency: 'usd', delinquent: false, discount: null,
      }), { status: 200, headers: { 'content-type': 'application/json' } })
    }
    if (/^\/v1\/subscriptions\/[^/]+$/.test(path)) {
      return new Response(JSON.stringify(subscription(path.split('/')[3]!, {})), {
        status: 200, headers: { 'content-type': 'application/json' },
      })
    }
    if (/^\/v1\/invoices\/[^/]+$/.test(path)) {
      const id = path.split('/')[3]!
      return new Response(JSON.stringify({
        id, object: 'invoice', customer: 'cus_1', subscription: 'sub_1', number: 'AF-1',
        status: 'open', amount_due: 5000, amount_paid: 0, currency: 'usd',
      }), { status: 200, headers: { 'content-type': 'application/json' } })
    }
    return new Response('{}', { status: 404, headers: { 'content-type': 'application/json' } })
  }

  countOf(call: string): number {
    return this.calls.filter((c) => c === call).length
  }
}

function subscription(id: string, over: { price?: string | null; cancel?: boolean; trialEnd?: string | null }) {
  return {
    id, object: 'subscription', customer: 'cus_1',
    status: over.trialEnd ? 'trialing' : 'active',
    items: { data: [{ id: 'si_1', price: { id: over.price ?? 'price_team' }, quantity: 3 }] },
    current_period_start: 1767225600,
    current_period_end: over.trialEnd ? Number(over.trialEnd) : 1769904000,
    cancel_at_period_end: over.cancel ?? false,
    canceled_at: null,
  }
}

function json(status: number, body: unknown): Recorded {
  return { status, body: JSON.stringify(body) }
}

function headerOf(init: RequestInit | undefined, name: string): string | null {
  const h = init?.headers as Record<string, string> | undefined
  if (!h) return null
  for (const [k, v] of Object.entries(h)) if (k.toLowerCase() === name) return v
  return null
}

// ---------------------------------------------------------------------------

describe('money moves once', async () => {
  if (!(await available())) {
    it('skipped: no database', () => {})
    return
  }

  let h: ApiHarness
  let org: Org
  let adminPool: Pool
  let sim: StripeSim

  const withAdmin = <R,>(fn: (db: Db) => Promise<R>) => adminPool.withoutTenant(fn)

  function ctx(now = new Date('2026-03-01T12:00:00Z')) {
    const config: StripeConfig = {
      secretKey: 'sk_test', webhookSecret: 'whsec',
      prices: { team: 'price_team', enterprise: 'price_enterprise' },
      apiBase: 'https://stripe.test', fetch: sim.fetch,
    }
    return {
      stripe: { client: new RealStripeClient(config), config },
      withAdmin,
      now,
      actorUserId: null,
      actorLabel: 'ops@antifailure.test',
    }
  }

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'money')
    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'admin-test-password'`)
    const u = new URL(adminUrl)
    u.username = 'antifailure_admin'
    u.password = 'admin-test-password'
    adminPool = createPool({ url: u.toString(), max: 6, connectTimeoutSeconds: 30 })
  })

  after(async () => {
    await h.admin`DELETE FROM admin_operations WHERE org_id = ${org.orgId}`
    await dropOrg(h.admin, org.orgId)
    await adminPool.close()
    await h.close()
  })

  async function reset() {
    sim = new StripeSim()
    await h.admin`DELETE FROM admin_operations WHERE org_id = ${org.orgId}`
    await h.admin`DELETE FROM audit_entries WHERE org_id = ${org.orgId}`
  }

  async function ledger() {
    return h.admin<{
      idempotency_key: string; state: string; action: string
      amount_minor: string | null; currency: string | null
      provider_object_id: string | null; before_state: unknown; after_state: unknown
      error_message: string | null; error_answered: boolean | null
    }[]>`SELECT * FROM admin_operations WHERE org_id = ${org.orgId}
         -- By key rather than by started_at: this suite runs on a fixed clock,
         -- so two attempts of one intent share a timestamp and ordering by it
         -- is a coin toss. The attempt suffix sorts after the bare key.
         ORDER BY idempotency_key`
  }

  // -------------------------------------------------------------------------

  it('a repeated refund is one call to Stripe, one refund, and one audit entry', async () => {
    await reset()
    const intent = { orgId: org.orgId, reason: 'Duplicate charge, AF-901', idempotencyKey: 'af-form-1' }

    const first = await refundCharge(ctx(), { ...intent, chargeId: 'ch_1', amountMinor: 2500 })
    const second = await refundCharge(ctx(), { ...intent, chargeId: 'ch_1', amountMinor: 2500 })

    assert.equal(first.replayed, false)
    assert.equal(second.replayed, true, 'the second press was treated as a new refund')
    assert.equal(
      sim.countOf('POST /v1/refunds'), 1,
      `Stripe was asked to refund ${sim.countOf('POST /v1/refunds')} times`,
    )
    assert.equal(sim.refunds.length, 1, 'a second refund object exists at the provider')
    assert.equal(first.providerObjectId, second.providerObjectId)

    const rows = await ledger()
    assert.equal(rows.length, 1, 'the ledger holds more than one row for one operation')
    assert.equal(rows[0]!.state, 'succeeded')
    assert.equal(Number(rows[0]!.amount_minor), 2500)
    assert.equal(rows[0]!.currency, 'usd')
    // The before state is Stripe's, read before the call, so the entry can be
    // reviewed without asking Stripe what things looked like a month ago.
    assert.equal((rows[0]!.before_state as { amountRefundedMinor: number }).amountRefundedMinor, 0)

    const entries = await h.admin<{ action: string; detail: Record<string, unknown> }[]>`
      SELECT action, detail FROM audit_entries WHERE org_id = ${org.orgId} AND action = 'billing.refunded'`
    assert.equal(entries.length, 1, 'the replay wrote a second audit entry')
    assert.equal(entries[0]!.detail.reason, 'Duplicate charge, AF-901')
    assert.equal(entries[0]!.detail.idempotencyKey, 'af-form-1')
  })

  it('two presses at the same instant produce one refund, not two', async () => {
    await reset()
    const intent = {
      orgId: org.orgId, chargeId: 'ch_2', amountMinor: 1000,
      reason: 'Double click, AF-902', idempotencyKey: 'af-form-2',
    }
    // The gate holds the provider open so both requests are genuinely in
    // flight at once. Without it the first finishes before the second starts
    // and this test proves nothing that the previous one did not.
    sim.openGate()
    const both = Promise.allSettled([refundCharge(ctx(), intent), refundCharge(ctx(), intent)])
    await new Promise((r) => setTimeout(r, 120))
    sim.closeGate()
    const [a, b] = await both

    const ok = [a, b].filter((r) => r.status === 'fulfilled')
    const refused = [a, b].filter((r) => r.status === 'rejected')
    assert.equal(ok.length, 1, 'both concurrent presses were served')
    assert.equal(refused.length, 1)
    assert.match(
      String((refused[0] as PromiseRejectedResult).reason.message),
      /already in progress/,
      'the losing press was refused for the wrong reason',
    )
    assert.equal(sim.countOf('POST /v1/refunds'), 1)
    assert.equal(sim.refunds.length, 1)
    assert.equal((await ledger()).length, 1)
  })

  it('the same key with a different amount is refused before anything is sent', async () => {
    await reset()
    const base = { orgId: org.orgId, chargeId: 'ch_3', reason: 'AF-903', idempotencyKey: 'af-form-3' }
    await refundCharge(ctx(), { ...base, amountMinor: 1000 })
    const sent = sim.countOf('POST /v1/refunds')

    await assert.rejects(
      () => refundCharge(ctx(), { ...base, amountMinor: 5000 }),
      /already been used for a different request/,
    )
    // The important half. A refusal AFTER the network call would have already
    // refunded the wrong amount or been refused by Stripe with a message
    // nobody here wrote.
    assert.equal(sim.countOf('POST /v1/refunds'), sent, 'the refused retry still reached Stripe')
    assert.equal(sim.refunds.length, 1)
  })

  it('a refund with no idempotency key from the client still collapses a double click', async () => {
    await reset()
    const intent = { orgId: org.orgId, chargeId: 'ch_4', amountMinor: 700, reason: 'AF-904' }
    await refundCharge(ctx(), intent)
    const again = await refundCharge(ctx(), intent)
    assert.equal(again.replayed, true)
    assert.equal(sim.refunds.length, 1)
    // ... and a DIFFERENT amount is a different operation rather than a silent
    // replay of the first, because the amount is in the fingerprint.
    await refundCharge(ctx(), { ...intent, amountMinor: 300 })
    assert.equal(sim.refunds.length, 2)
    assert.equal((await ledger()).length, 2)
  })

  it('without the key the provider would have refunded twice, which is what the key prevents', async () => {
    await reset()
    // Straight at the client, bypassing the ledger, to show the simulator is
    // not the thing making the tests above pass.
    const config: StripeConfig = {
      secretKey: 'sk', webhookSecret: 'w',
      prices: { team: 'price_team', enterprise: 'price_enterprise' },
      apiBase: 'https://stripe.test', fetch: sim.fetch,
    }
    const client = new RealStripeClient(config)
    await client.refund({ chargeId: 'ch_5', amountMinor: 100 }, 'k-1')
    await client.refund({ chargeId: 'ch_5', amountMinor: 100 }, 'k-1')
    assert.equal(sim.refunds.length, 1, 'the same key created a second refund')

    await client.refund({ chargeId: 'ch_5', amountMinor: 100 }, 'k-2')
    assert.equal(sim.refunds.length, 2, 'a different key did not create a second refund')
  })

  it('a crash between the claim and the answer converges on one refund', async () => {
    await reset()
    const intent = {
      orgId: org.orgId, chargeId: 'ch_6', amountMinor: 4200,
      reason: 'AF-905', idempotencyKey: 'af-form-6',
    }
    // The first attempt reaches Stripe and dies before settling: the ledger
    // row is left in_flight and the refund exists at the provider. This is the
    // window the ledger CANNOT close on its own, and the one the key closes.
    const key = keyFor({
      action: 'billing.refunded', orgId: org.orgId, targetType: 'charge',
      targetId: 'ch_6', actorUserId: null, actorLabel: 'ops', reason: 'AF-905',
      params: { chargeId: 'ch_6', amountMinor: 4200, category: null },
      idempotencyKey: 'af-form-6',
    })
    const client = new RealStripeClient({
      secretKey: 'sk', webhookSecret: 'w',
      prices: { team: 'price_team', enterprise: 'price_enterprise' },
      apiBase: 'https://stripe.test', fetch: sim.fetch,
    })
    await withAdmin(async (db) => {
      await db.execute(sql`
        INSERT INTO admin_operations (
          idempotency_key, action, org_id, target_type, target_id, actor_label, reason,
          request, request_fingerprint, state, started_at)
        VALUES (${key}, 'billing.refunded', ${org.orgId}, 'charge', 'ch_6', 'ops', 'AF-905',
                ${JSON.stringify({ chargeId: 'ch_6', amountMinor: 4200, category: null })}::jsonb,
                ${fingerprint({
                  action: 'billing.refunded', orgId: org.orgId, targetId: 'ch_6',
                  params: { chargeId: 'ch_6', amountMinor: 4200, category: null },
                })},
                'in_flight', ${'2026-03-01T11:00:00Z'})`)
    })
    await client.refund({ chargeId: 'ch_6', amountMinor: 4200 }, key)
    assert.equal(sim.refunds.length, 1, 'the interrupted attempt did not reach the provider')

    // Now the retry, well past the grace period, through the ordinary path.
    const recovered = await refundCharge(
      ctx(new Date(Date.parse('2026-03-01T11:00:00Z') + IN_FLIGHT_GRACE_MS + 1000)),
      intent,
    )
    assert.equal(
      sim.refunds.length, 1,
      'the recovery created a SECOND refund; the key was not reused',
    )
    assert.equal(recovered.providerObjectId, sim.refunds[0]!.id)
    const rows = await ledger()
    assert.equal(rows.length, 1)
    assert.equal(rows[0]!.state, 'succeeded')
  })

  it('an in-flight operation inside the grace period is refused rather than retried', async () => {
    await reset()
    const intent = {
      orgId: org.orgId, chargeId: 'ch_7', amountMinor: 100,
      reason: 'AF-906', idempotencyKey: 'af-form-7',
    }
    sim.openGate()
    const running = refundCharge(ctx(), intent).catch(() => null)
    await new Promise((r) => setTimeout(r, 80))
    await assert.rejects(() => refundCharge(ctx(), intent), /already in progress/)
    sim.closeGate()
    await running
    assert.equal(sim.refunds.length, 1)
  })

  it('a provider REFUSAL is recorded, and the retry gets a key of its own', async () => {
    await reset()
    const intent = {
      orgId: org.orgId, invoiceId: 'in_9',
      reason: 'Customer fixed their card, AF-907', idempotencyKey: 'af-form-9',
    }
    sim.failNext = 1
    await assert.rejects(() => retryPayment(ctx(), intent), /card was declined/i)

    const failed = await ledger()
    assert.equal(failed.length, 1)
    assert.equal(failed[0]!.state, 'failed')
    assert.match(String(failed[0]!.error_message), /declined/i)
    // A failure writes no audit entry, because nothing happened to the
    // customer's money and a log that records attempts as events is a log
    // nobody can read.
    const noEntry = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'billing.payment_retried'`
    assert.equal(Number(noEntry[0]!.n), 0)

    // The provider ANSWERED with a refusal, so that attempt definitively did
    // nothing and the retry is a new request with a key of its own. Reusing the
    // key here is the defect this test was written to catch: the simulator, like
    // Stripe, replays its own 402 forever, so a customer who has since fixed
    // their card could never pay.
    const ok = await retryPayment(ctx(), intent)
    assert.equal(ok.replayed, false)
    const settled = await ledger()
    assert.equal(settled.length, 2, 'the retry did not get a key of its own')
    assert.equal(settled[0]!.state, 'failed')
    assert.equal(settled[0]!.error_answered, true)
    assert.equal(settled[1]!.state, 'succeeded')
    assert.equal(settled[1]!.idempotency_key, `${settled[0]!.idempotency_key}.a2`)
    // One audit entry, for the attempt that actually did something.
    const entries = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'billing.payment_retried'`
    assert.equal(Number(entries[0]!.n), 1)
  })

  it('a retry after a failure with NO answer reuses the key rather than minting one', async () => {
    await reset()
    const intent = {
      orgId: org.orgId, invoiceId: 'in_10',
      reason: 'Timed out, AF-913', idempotencyKey: 'af-form-10',
    }
    // A transport failure: nothing came back, so the request may have been
    // executed and the response lost. Minting a new key here would charge the
    // customer twice.
    const before = ctx()
    await assert.rejects(
      () => retryPayment(
        { ...before, stripe: { ...before.stripe, client: new RealStripeClient({
          secretKey: 'sk', webhookSecret: 'w',
          prices: { team: 'price_team', enterprise: 'price_enterprise' },
          apiBase: 'https://stripe.test',
          fetch: (async (i: unknown, init: unknown) => {
            const path = new URL(String(i)).pathname
            if (path.endsWith('/pay')) throw new TypeError('socket hang up')
            return sim.fetch(i as string, init as RequestInit)
          }) as typeof globalThis.fetch,
        }) } },
        intent,
      ),
      /socket hang up/,
    )
    const failed = await ledger()
    assert.equal(failed[0]!.state, 'failed')
    assert.equal(
      failed[0]!.error_answered, false,
      'a transport failure was recorded as a provider refusal, which would allow a second charge',
    )

    await retryPayment(ctx(), intent)
    const settled = await ledger()
    assert.equal(settled.length, 1, 'the ambiguous retry minted a second key')
    assert.equal(settled[0]!.state, 'succeeded')
  })

  it('credit is applied as credit and not as a charge', async () => {
    await reset()
    const run = await creditCustomer(ctx(), {
      orgId: org.orgId, customerId: 'cus_1', amountMinor: 2500, currency: 'usd',
      reason: 'Goodwill for the March incident, AF-908',
    })
    // Stripe's sign convention, asserted rather than assumed. A positive
    // amount here would be a bill sent to somebody being apologised to.
    assert.equal(sim.credits[0]!.amount, -2500)
    assert.equal(run.result.endingBalance, -2500)
    const rows = await ledger()
    // The ledger records what the operator moved, positive, beside its
    // currency, so a month's credits can be summed without knowing Stripe's
    // sign convention.
    assert.equal(Number(rows[0]!.amount_minor), 2500)
    assert.equal(rows[0]!.currency, 'usd')
  })

  it('a plan change names the subscription item, so nobody is billed for two plans', async () => {
    await reset()
    await changePlan(ctx(), {
      orgId: org.orgId, subscriptionId: 'sub_1', plan: 'enterprise', prorate: false,
      reason: 'Upgraded on the annual contract, AF-909',
    })
    const rows = await ledger()
    assert.equal((rows[0]!.before_state as { priceId: string }).priceId, 'price_team')
    assert.equal((rows[0]!.after_state as { priceId: string }).priceId, 'price_enterprise')
  })

  it('changing to the plan somebody is already on is refused rather than recorded', async () => {
    await reset()
    await assert.rejects(
      () => changePlan(ctx(), {
        orgId: org.orgId, subscriptionId: 'sub_1', plan: 'team', prorate: false,
        reason: 'AF-910',
      }),
      /already on the team plan/,
    )
    assert.equal((await ledger()).length, 0, 'a no-op was written to the money ledger')
    assert.equal(sim.countOf('POST /v1/subscriptions/sub_1'), 0)
  })

  it('a trial cannot be extended into the past', async () => {
    await reset()
    await assert.rejects(
      () => extendTrial(ctx(), {
        orgId: org.orgId, subscriptionId: 'sub_1',
        until: new Date('2020-01-01T00:00:00Z'), reason: 'AF-911',
      }),
      /date in the future/,
    )
    assert.equal((await ledger()).length, 0)
  })

  it('cancelling ends the paid period rather than taking it away', async () => {
    await reset()
    const run = await cancelSubscription(ctx(), {
      orgId: org.orgId, subscriptionId: 'sub_1', reason: 'Customer left, AF-912',
    })
    assert.equal(run.result.subscription.cancelAtPeriodEnd, true)
    // Never a DELETE. Taking away a month somebody has paid for is a refund
    // question, asked and answered separately.
    assert.equal(sim.countOf('DELETE /v1/subscriptions/sub_1'), 0)
  })

  it('an operation with no reason is refused', async () => {
    await reset()
    await assert.rejects(
      () => refundCharge(ctx(), { orgId: org.orgId, chargeId: 'ch_x', reason: '   ' }),
      /needs a reason/,
    )
    assert.equal(sim.countOf('POST /v1/refunds'), 0)
  })

  it('the tenant role cannot read or write the money ledger', async () => {
    let thrown: unknown
    try {
      await h.pool.withTenant({ orgId: org.orgId }, async (db) => {
        await db.execute(sql`SELECT count(*) FROM admin_operations`)
      })
    } catch (e) { thrown = e }
    const said = `${(thrown as Error)?.message} ${(thrown as { cause?: Error })?.cause?.message ?? ''}`
    assert.match(said, /permission denied/i, said)
  })
})

describe('money is never rendered without its currency', () => {
  it('formats minor units into the currency they are in', () => {
    assert.equal(money(2500, 'usd'), '$25.00')
    assert.equal(money(2500, 'eur'), '€25.00')
    // Zero-decimal. Dividing by a hundred here would show 25 yen as a
    // quarter of a yen, which is the kind of defect that reaches a newspaper.
    assert.equal(money(2500, 'jpy'), '¥2,500')
  })

  it('says the code rather than throwing for a currency Intl has not heard of', () => {
    assert.match(money(1000, 'zzz'), /ZZZ/)
  })
})
