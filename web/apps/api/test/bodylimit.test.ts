// How much body a request may carry, and where that is decided.
//
// The claim under test is an ordering, not a number: a body over the limit is
// refused BEFORE the handler reads it, so a stranger with no secret cannot
// make this process buffer a body of any size on the way to a 401. The number
// matters second, and only in one direction: everything GitHub, Stripe and
// the console legitimately send still gets through, with the signature still
// verifying over the bytes the middleware handed on.
//
// Two paths reach the refusal and both are exercised: a client that declares
// content-length is refused from the header without a byte read, and a client
// that streams is refused at the byte where the count crosses the limit. The
// in-process fetch used here attaches no content-length, so a plain string
// body takes the streaming path, and the header path is taken by setting the
// header by hand.

import { test, describe, before, after, mock } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import {
  BODY_LIMITS,
  DEFAULT_BODY_BYTES,
  ENDPOINT_LIMITS,
  bodyLimitFor,
} from '../src/limits.ts'
import { POSTHOG_MOUNT } from '../src/analytics/posthog.ts'
import {
  available,
  dropOrg,
  startApi,
  stripeAgainstMockPack,
  type ApiHarness,
} from './harness.ts'
import type { Billing } from '../src/billing/index.ts'

const SECRET = 'a-body-limit-webhook-secret'
// Read with a fallback rather than a non-null assertion, so a catalog key that
// went missing fails the test that names it instead of throwing while this
// file loads, which would read as one failure with no test name on it.
const GITHUB_LIMIT = BODY_LIMITS['POST /webhooks/github']?.maxBytes ?? -1
const STRIPE_LIMIT = BODY_LIMITS['POST /webhooks/stripe']?.maxBytes ?? -1

function githubSignature(body: string, secret = SECRET): string {
  return 'sha256=' + createHmac('sha256', secret).update(body, 'utf8').digest('hex')
}

function stripeSignature(secret: string, body: string, at: Date): string {
  const t = Math.floor(at.getTime() / 1000)
  const mac = createHmac('sha256', secret).update(`${t}.${body}`, 'utf8').digest('hex')
  return `t=${t},v1=${mac}`
}

/** A JSON body of at least `bytes` bytes that still parses: one object with a
 *  padding field, so a handler that did run would not refuse it as not JSON
 *  and hide the ordering under a 400. */
function padded(fields: Record<string, unknown>, bytes: number): string {
  const base = JSON.stringify({ ...fields, padding: '' })
  return JSON.stringify({ ...fields, padding: 'x'.repeat(Math.max(0, bytes - base.length + 1)) })
}

// ---------------------------------------------------------------------------

describe('the body limit catalog', () => {
  test('every named endpoint is one the rate catalog also names', () => {
    // A body limit for a route that does not exist is a number nobody will
    // ever read, and a typo in the key is exactly that. The rate catalog is
    // checked against the server's route table by limits.test.ts, so a key
    // present there is a route that is served.
    for (const key of Object.keys(BODY_LIMITS)) {
      assert.ok(key in ENDPOINT_LIMITS, `${key} has a body limit and no rate limit`)
    }
  })

  test('every entry carries a positive number and a reason', () => {
    for (const [key, limit] of Object.entries(BODY_LIMITS)) {
      assert.ok(limit.maxBytes > 0, `${key} admits nothing`)
      assert.ok(limit.reason.length > 40, `${key} does not say why`)
    }
  })

  test('the webhooks are bounded to what their senders send', () => {
    // GitHub drops a delivery over 25 MB and the largest honest one this App
    // subscribes to is an installation listing tens of thousands of
    // repositories. Stripe events are tens of kilobytes. Both sit inside the
    // limit and both limits sit inside the default's neighbourhood rather than
    // being the transport's ceiling.
    assert.equal(GITHUB_LIMIT, 5 * 1024 * 1024)
    assert.equal(STRIPE_LIMIT, 512 * 1024)
    assert.ok(STRIPE_LIMIT < DEFAULT_BODY_BYTES)
  })

  test('resolves an unnamed route to the default and a named one to its number', () => {
    assert.equal(bodyLimitFor('POST', '/trpc/environments.create'), DEFAULT_BODY_BYTES)
    assert.equal(bodyLimitFor('POST', '/v1/events'), DEFAULT_BODY_BYTES)
    assert.equal(bodyLimitFor('POST', '/webhooks/github'), GITHUB_LIMIT)
    assert.equal(bodyLimitFor('POST', '/webhooks/stripe'), STRIPE_LIMIT)
    assert.equal(bodyLimitFor('POST', '/byok/anthropic/v1/messages'), 32 * 1024 * 1024)
  })

  test('the PostHog proxy keeps the number beside its route', () => {
    // The allowlist in analytics/posthog.ts declares a body size per path and
    // used to apply it per route. Now it flows through the catalog, and this
    // is the entry that would have been clamped to the default if it did not:
    // session recording accepts two megabytes.
    assert.equal(bodyLimitFor('POST', `${POSTHOG_MOUNT}/s/`), 2 * 1024 * 1024)
    assert.equal(bodyLimitFor('POST', `${POSTHOG_MOUNT}/flags/`), 64 * 1024)
  })

  test('the MCP mount bounds its own body and is left alone', () => {
    assert.equal(bodyLimitFor('POST', '/mcp'), null)
    assert.equal(bodyLimitFor('POST', '/auth/mcp/token'), null)
  })
})

// ---------------------------------------------------------------------------

describe('bodies over the limit', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness
  let billing: Billing
  const run = randomUUID().slice(0, 8)

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    billing = stripe.billing
    api = await startApi({ githubWebhookSecret: SECRET, stripe: billing })
  })
  after(async () => {
    const rows = await api.admin<{ id: string }[]>`
      SELECT id FROM organizations WHERE github_login = 'body-limit-org'`
    for (const row of rows) await dropOrg(api.admin, row.id)
    await api.admin`DELETE FROM github_deliveries WHERE delivery_id LIKE ${'body-limit-' + run + '-%'}`
    await api.close()
  })

  async function github(body: string, headers: Record<string, string>, deliveryId: string) {
    return api.fetch('/webhooks/github', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-github-event': 'ping',
        'x-github-delivery': `body-limit-${run}-${deliveryId}`,
        ...headers,
      },
      body,
    })
  }

  /** Whether the GitHub handler got as far as writing the delivery to its
   *  ledger, which is the first thing it does after the signature verifies. A
   *  row here means verification ran and passed; no row means the handler
   *  never reached it. */
  async function ledgered(deliveryId: string): Promise<boolean> {
    const rows = await api.admin<{ n: string }[]>`
      SELECT count(*)::text AS n FROM github_deliveries
      WHERE delivery_id = ${`body-limit-${run}-${deliveryId}`}`
    return rows[0]!.n !== '0'
  }

  test('a GitHub delivery over the limit is refused before the signature is checked', async () => {
    // The signature is VALID over this body. If the handler ran, it would
    // verify, claim the delivery and answer 200 for the ping; the only way
    // this answers 413 is the middleware refusing it first. And the ledger
    // proves the handler never ran, which is the property that matters: the
    // body was not buffered on the way to a decision.
    const body = padded({ zen: 'oversized' }, GITHUB_LIMIT + 1)
    const res = await github(body, { 'x-hub-signature-256': githubSignature(body) }, 'over-valid')
    assert.equal(res.status, 413)
    assert.deepEqual(await res.json(), {
      error: `This request is larger than ${GITHUB_LIMIT} bytes.`,
    })
    assert.equal(await ledgered('over-valid'), false, 'the handler ran and claimed the delivery')
  })

  test('an unsigned GitHub delivery over the limit answers 413, not 401', async () => {
    // A 401 here would mean the handler read the whole body and then
    // refused it, which is the defect: the answer would be right and the
    // buffering would already have happened.
    const body = padded({ zen: 'oversized and unsigned' }, GITHUB_LIMIT + 1)
    const res = await github(body, { 'x-hub-signature-256': 'sha256=' + '0'.repeat(64) }, 'over-unsigned')
    assert.equal(res.status, 413)
  })

  test('a declared content-length over the limit is refused without reading a byte', async () => {
    // The header path. The body here is tiny; only the header says otherwise,
    // and the middleware believes the header because a real server enforces
    // it. The handler, had it run, would have found a valid ping.
    const body = JSON.stringify({ zen: 'small' })
    const res = await github(
      body,
      {
        'x-hub-signature-256': githubSignature(body),
        'content-length': String(GITHUB_LIMIT + 1),
      },
      'header-over',
    )
    assert.equal(res.status, 413)
    assert.equal(await ledgered('header-over'), false)
  })

  test('a GitHub delivery under the limit still verifies over the raw bytes', async () => {
    // The middleware re-wraps a streamed body to hand it on. The signature is
    // over the original bytes, with whitespace that a re-serialisation would
    // drop, so a 200 here means the handler saw exactly what was sent.
    const body = '{ "zen":   "spaced out" ,"hook_id": 1 }'
    const res = await github(body, { 'x-hub-signature-256': githubSignature(body) }, 'under')
    assert.equal(res.status, 200, await res.text())
    assert.equal(await ledgered('under'), true)
  })

  test('a Stripe delivery over the limit is refused before the signature is checked', async () => {
    // The Stripe handler logs every signature failure with one fixed prefix.
    // That line is the instrument: a refusal that ran the verifier over an
    // oversized body would have written it, and a refusal that did not would
    // not.
    const warned: string[] = []
    const spy = mock.method(console, 'warn', (...args: unknown[]) => {
      warned.push(args.map(String).join(' '))
    })
    try {
      const body = padded({ id: 'evt_oversized', object: 'event', type: 'ping' }, STRIPE_LIMIT + 1)
      const res = await api.fetch('/webhooks/stripe', {
        method: 'POST',
        headers: { 'stripe-signature': 't=1,v1=' + '0'.repeat(64) },
        body,
      })
      assert.equal(res.status, 413)
      assert.deepEqual(await res.json(), {
        error: `This request is larger than ${STRIPE_LIMIT} bytes.`,
      })
      assert.equal(
        warned.filter((line) => line.startsWith('stripe webhook refused')).length,
        0,
        'the signature verifier ran over an oversized body',
      )
    } finally {
      spy.mock.restore()
    }
  })

  test('a signed Stripe delivery under the limit still passes verification', async () => {
    const body = JSON.stringify({
      id: `evt_${run}bodylimit`,
      object: 'event',
      api_version: '2024-06-20',
      created: Math.floor(api.clock.now().getTime() / 1000),
      livemode: false,
      type: 'ping',
      data: { object: {} },
    })
    const res = await api.fetch('/webhooks/stripe', {
      method: 'POST',
      headers: {
        'stripe-signature': stripeSignature(billing.config.webhookSecret, body, api.clock.now()),
      },
      body,
    })
    assert.notEqual(res.status, 413, await res.text())
    assert.notEqual(res.status, 401, 'the middleware handed the handler different bytes')
  })

  test('a tRPC mutation over the default is refused before authentication', async () => {
    // No session, no CSRF header: with the limit in the wrong place this
    // would answer 401 or 403 after buffering the whole body.
    const body = padded({ json: {} }, DEFAULT_BODY_BYTES + 1)
    const res = await api.fetch('/trpc/environments.create', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body,
    })
    assert.equal(res.status, 413)
  })

  test('the bring-your-own-key proxy admits a body the default would refuse', async () => {
    // Two megabytes, which is over the default and well under the provider's
    // ceiling. Refused by the HANDLER rather than for size: this harness holds
    // no sealing key, so the route answers 503 naming it, and a harness with
    // one would answer 401 for want of a token. Either is the handler
    // speaking; 413 would be the default winning over the exception.
    const res = await api.fetch('/byok/anthropic/v1/messages', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'content-length': String(2 * 1024 * 1024) },
      body: '{}',
    })
    const text = await res.text()
    assert.notEqual(res.status, 413, text)
    assert.ok([401, 503].includes(res.status), `${res.status} ${text}`)
  })

  test('and refuses one over the provider ceiling', async () => {
    const res = await api.fetch('/byok/anthropic/v1/messages', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'content-length': String(32 * 1024 * 1024 + 1),
      },
      body: '{}',
    })
    assert.equal(res.status, 413)
  })

  test('a request with no body is not touched', async () => {
    const res = await api.fetch('/health')
    assert.equal(res.status, 200)
  })
})
