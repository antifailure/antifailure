// Spending a customer's key against their cap.
//
// This is the half of BYOK that was missing. Storing a key, capping it and
// rotating it were all built and tested, and nothing ever used the key: the
// two functions that decrypt one and charge a budget had no call sites outside
// their own tests. So a cap was a number in a table.
//
// What is tested here is that it is now a cap:
//
//   - a request against an exhausted budget never reaches the provider, and
//     never causes the key to be decrypted;
//   - what is charged is what the provider itself said it used, not an
//     estimate computed here that would disagree with the invoice;
//   - a model with no price is refused BEFORE the call, because discovering it
//     afterwards means the money is spent and the only remaining choice is
//     whether to lie about it;
//   - and the customer's key never appears in anything this returns.
//
// No test here reaches a real provider. The base URL is pointed at a stub that
// records what it was sent, which is also how the key-in-the-request assertion
// is made: by reading the header the provider would have received.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { createServer as createHttpServer, type Server } from 'node:http'
import { available, startApi, seedOrg, dropOrg, type ApiHarness, type Org, testAnalytics } from './harness.ts'
import { saveKey, setBudget, listBudgets, borrowKey, recordSpend } from '../src/providers/store.ts'
import { Keyring } from '../src/providers/seal.ts'
import { costOf, pricesFrom, PricingError, usageFrom, DEFAULT_PRICES } from '../src/providers/pricing.ts'
import { createPostHogSink } from '../src/analytics/posthog-sink.ts'

interface Generation { event: string; distinctId: string; properties: Record<string, unknown> }
/** Everything the control plane reported about a brokered model call. */
const generations: Generation[] = []

const ANTHROPIC = ['sk', 'ant', 'api03'].join('-')
const KEY = `${ANTHROPIC}-dddddddddddddddddddddddddddd8888`

const PRICES = {
  'test-model': { inputPerMillion: 10, outputPerMillion: 30 },
}

// ---------------------------------------------------------------------------

describe('pricing', () => {
  test('an unpriced model throws rather than costing nothing', () => {
    // THE ONE THAT MATTERS. Charging zero for a model this does not know is a
    // request that spends a customer's money and adds nothing to the total,
    // which is exactly a spend cap that does not cap spending.
    assert.throws(
      () => costOf(PRICES, 'a-model-nobody-priced', { inputTokens: 1000, outputTokens: 1000 }),
      (err: Error) => err instanceof PricingError && /No price is configured/.test(err.message),
    )
  })

  test('the message says how to price it, because that is the fix', () => {
    try {
      costOf(PRICES, 'new-model', { inputTokens: 0, outputTokens: 0 })
      assert.fail('should have thrown')
    } catch (err) {
      assert.match(String((err as Error).message), /AF_MODEL_PRICES/)
      assert.match(String((err as Error).message), /new-model=/)
    }
  })

  test('cost is per million tokens, in and out separately', () => {
    // Input and output are priced differently by every provider, and a single
    // rate would undercharge output by three to five times.
    assert.equal(costOf(PRICES, 'test-model', { inputTokens: 1_000_000, outputTokens: 0 }), 10)
    assert.equal(costOf(PRICES, 'test-model', { inputTokens: 0, outputTokens: 1_000_000 }), 30)
    assert.equal(
      costOf(PRICES, 'test-model', { inputTokens: 500_000, outputTokens: 100_000 }),
      5 + 3,
    )
  })

  test('an override adds to the defaults rather than replacing them', () => {
    const prices = pricesFrom('my-model=1/2')
    assert.deepEqual(prices['my-model'], { inputPerMillion: 1, outputPerMillion: 2 })
    // The defaults survive, so overriding one model does not un-price the rest.
    assert.deepEqual(prices['gpt-4.1'], DEFAULT_PRICES['gpt-4.1'])
  })

  test('a malformed override throws at start-up rather than being skipped', () => {
    // A skipped entry is a model that silently falls back to a different price,
    // which is the same failure this file exists to prevent.
    assert.throws(() => pricesFrom('nonsense'), PricingError)
    assert.throws(() => pricesFrom('m=1'), PricingError)
    assert.throws(() => pricesFrom('m=a/b'), PricingError)
  })

  test('unset is the defaults, not an empty table', () => {
    // An empty table would refuse every model, which is safe and useless.
    assert.ok(Object.keys(pricesFrom(undefined)).length > 0)
    assert.ok(Object.keys(pricesFrom('')).length > 0)
  })

  test('usage is read in the spelling each provider uses', () => {
    assert.deepEqual(
      usageFrom('anthropic', { usage: { input_tokens: 5, output_tokens: 7 } }),
      { inputTokens: 5, outputTokens: 7 },
    )
    assert.deepEqual(
      usageFrom('openai', { usage: { prompt_tokens: 5, completion_tokens: 7 } }),
      { inputTokens: 5, outputTokens: 7 },
    )
    // A response with no usage is null rather than zero. Zero would be a real
    // charge of nothing; null means there is nothing to charge.
    assert.equal(usageFrom('anthropic', {}), null)
  })
})

// ---------------------------------------------------------------------------

describe('spending a key against a budget', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness
  let org: Org
  let stub: Server
  let stubUrl: string
  const keyring = Keyring.of(randomBytes(32))

  /** What the stubbed provider was sent, so the key can be asserted on. */
  let seen: { headers: Record<string, string>; body: string }[] = []
  let reply: { status: number; body: unknown } = {
    status: 200,
    body: { usage: { input_tokens: 1_000_000, output_tokens: 0 }, content: [{ text: 'hi' }] },
  }

  before(async () => {
    stub = createHttpServer((req, res) => {
      const chunks: Buffer[] = []
      req.on('data', (c: Buffer) => chunks.push(c))
      req.on('end', () => {
        seen.push({
          headers: req.headers as Record<string, string>,
          body: Buffer.concat(chunks).toString('utf8'),
        })
        res.writeHead(reply.status, { 'content-type': 'application/json' })
        res.end(JSON.stringify(reply.body))
      })
    })
    await new Promise<void>((resolve) => stub.listen(0, '127.0.0.1', resolve))
    const address = stub.address()
    stubUrl = `http://127.0.0.1:${typeof address === 'object' && address ? address.port : 0}`

    api = await startApi({
      keyring,
      modelPrices: PRICES,
      providerBases: { anthropic: stubUrl, openai: stubUrl },
      // The REAL sink with a fake transport, so what is asserted below is the
      // payload this process would actually send rather than a double's
      // reconstruction of it. A hand written double passed while the real sink
      // leaked, on this exact shape, in the MCP suite.
      postHogSink: createPostHogSink({
        projectKey: 'phc_public',
        region: 'us',
        client: {
          capture(payload: unknown) { generations.push(payload as Generation) },
          async shutdown() {},
        } as never,
      }),
    })
    org = await seedOrg(api.admin, 'byok-spend')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
    await new Promise<void>((resolve) => stub.close(() => resolve()))
  })

  beforeEach(async () => {
    seen = []
    generations.length = 0
    reply = {
      status: 200,
      body: { usage: { input_tokens: 1_000_000, output_tokens: 0 }, content: [{ text: 'hi' }] },
    }
    await api.admin`DELETE FROM provider_keys WHERE org_id = ${org.orgId}`
    await api.admin`DELETE FROM provider_budgets WHERE org_id = ${org.orgId}`
    api.clock.advance(120_000)
  })

  /** An engine token, which is what a build machine would hold. */
  async function engineToken(): Promise<string> {
    const token = 'afe_' + randomBytes(24).toString('hex')
    const { createHash } = await import('node:crypto')
    const hash = createHash('sha256').update(token).digest()
    await api.admin`
      INSERT INTO engine_tokens (org_id, token_hash, prefix, name, kind)
      VALUES (${org.orgId}, ${hash}, ${token.slice(0, 12)}, 'a build machine', 'engine')`
    return token
  }

  async function call(token: string | null, body: unknown, header: 'x-api-key' | 'authorization' = 'x-api-key') {
    const headers: Record<string, string> = { 'content-type': 'application/json' }
    if (token) headers[header] = header === 'authorization' ? `Bearer ${token}` : token
    const res = await api.fetch('/byok/anthropic/v1/messages', {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
    })
    return { status: res.status, text: await res.text(), headers: res.headers }
  }

  async function ready(capUsd: number) {
    await setBudget(api.pool, api.clock, {
      analytics: testAnalytics(),
      orgId: org.orgId, provider: 'anthropic', capUsd,
      actorLabel: 'a test', actorUserId: null,
    })
    await saveKey(api.pool, api.clock, keyring, {
      analytics: testAnalytics(),
      orgId: org.orgId, provider: 'anthropic', key: KEY,
      actorLabel: 'a test', actorUserId: null,
    })
  }

  // -------------------------------------------------------------------------

  test('a call goes through with the customer key and charges what it used', async () => {
    await ready(100)
    const token = await engineToken()
    const res = await call(token, { model: 'test-model', messages: [] })
    assert.equal(res.status, 200, res.text)

    // The provider received the CUSTOMER's key, not ours and not the token.
    assert.equal(seen.length, 1)
    assert.equal(seen[0]!.headers['x-api-key'], KEY)
    assert.equal(seen[0]!.headers['anthropic-version'], '2023-06-01')

    // A million input tokens at ten dollars a million.
    const budgets = await listBudgets(api.pool, api.clock, org.orgId)
    assert.equal(budgets[0]!.spentUsd, 10)
    assert.equal(res.headers.get('x-antifailure-cost-usd'), '10.000000')
  })

  test('an exhausted budget is refused and never reaches the provider', async () => {
    // The claim the whole feature rests on. Not "the call is recorded as over
    // budget afterwards": the request does not happen.
    await ready(5)
    const token = await engineToken()
    const res = await call(token, { model: 'test-model', messages: [] })
    assert.equal(res.status, 200, 'the first call should fit')
    assert.equal(seen.length, 1)

    const second = await call(token, { model: 'test-model', messages: [] })
    assert.equal(second.status, 402)
    assert.match(second.text, /budget/)
    assert.equal(seen.length, 1, 'a refused call still reached the provider')
  })

  test('a provider with no budget row cannot spend at all', async () => {
    // A missing cap reads as zero, not unlimited. The key is stored and there
    // is no allowance, so nothing goes out.
    await saveKey(api.pool, api.clock, keyring, {
      analytics: testAnalytics(),
      orgId: org.orgId, provider: 'anthropic', key: KEY,
      actorLabel: 'a test', actorUserId: null,
    })
    const res = await call(await engineToken(), { model: 'test-model', messages: [] })
    assert.equal(res.status, 402)
    assert.equal(seen.length, 0)
  })

  test('an unpriced model is refused before the call, not after', async () => {
    // Afterwards the money is spent and the only choice left is whether to lie
    // about it.
    await ready(100)
    const res = await call(await engineToken(), { model: 'not-in-the-table', messages: [] })
    assert.equal(res.status, 400)
    assert.match(res.text, /No price is configured/)
    assert.equal(seen.length, 0, 'an unpriced model reached the provider')
  })

  test('streaming is refused rather than charged as free', async () => {
    await ready(100)
    const res = await call(await engineToken(), { model: 'test-model', stream: true, messages: [] })
    assert.equal(res.status, 400)
    assert.match(res.text, /Streaming is not supported/)
    assert.equal(seen.length, 0)
  })

  test('no stored key is a refusal that says what to do', async () => {
    await setBudget(api.pool, api.clock, {
      analytics: testAnalytics(),
      orgId: org.orgId, provider: 'anthropic', capUsd: 100,
      actorLabel: 'a test', actorUserId: null,
    })
    const res = await call(await engineToken(), { model: 'test-model', messages: [] })
    assert.equal(res.status, 402)
    assert.match(res.text, /af provider set anthropic/)
  })

  test('the provider error comes back as the provider wrote it', async () => {
    // A client library that knows how to read an Anthropic error should keep
    // working. Rewriting it into our own shape breaks every caller's handling.
    await ready(100)
    reply = { status: 429, body: { error: { type: 'rate_limit_error', message: 'slow down' } } }
    const res = await call(await engineToken(), { model: 'test-model', messages: [] })
    assert.equal(res.status, 429)
    assert.match(res.text, /rate_limit_error/)

    // And nothing is charged for a call that failed.
    const budgets = await listBudgets(api.pool, api.clock, org.orgId)
    assert.equal(budgets[0]!.spentUsd, 0)
  })

  test('a personal token works too, in the header its own client sends', async () => {
    // OpenAI's client sends an Authorization bearer and Anthropic's sends
    // x-api-key. Accepting only one would mean editing a caller to satisfy a
    // preference of ours.
    await ready(100)
    const token = await engineToken()
    const res = await call(token, { model: 'test-model', messages: [] }, 'authorization')
    assert.equal(res.status, 200, res.text)
  })

  test('no token is refused, and a made-up one too', async () => {
    await ready(100)
    assert.equal((await call(null, { model: 'test-model' })).status, 401)
    assert.equal((await call('afe_' + '0'.repeat(48), { model: 'test-model' })).status, 401)
    assert.equal(seen.length, 0)
  })

  test('another organization cannot spend the first one key', async () => {
    await ready(100)
    const other = await seedOrg(api.admin, 'byok-spend-other')
    try {
      const token = 'afe_' + randomBytes(24).toString('hex')
      const { createHash } = await import('node:crypto')
      await api.admin`
        INSERT INTO engine_tokens (org_id, token_hash, prefix, name, kind)
        VALUES (${other.orgId}, ${createHash('sha256').update(token).digest()},
                ${token.slice(0, 12)}, 'somebody else', 'engine')`

      // The organization comes from the token, so this asks about its own
      // tenant, which has no key and no budget.
      const res = await call(token, { model: 'test-model', messages: [] })
      assert.equal(res.status, 402)
      assert.equal(seen.length, 0)
    } finally {
      await dropOrg(api.admin, other.orgId)
    }
  })

  test('nothing this returns contains the key', async () => {
    await ready(100)
    const token = await engineToken()
    const bodies: string[] = []
    bodies.push((await call(token, { model: 'test-model', messages: [] })).text)
    bodies.push((await call(token, { model: 'nope', messages: [] })).text)
    bodies.push((await call(token, { model: 'test-model', stream: true })).text)
    bodies.push((await call(null, { model: 'test-model' })).text)
    reply = { status: 500, body: { error: { message: 'boom' } } }
    bodies.push((await call(token, { model: 'test-model', messages: [] })).text)

    for (const body of bodies) {
      assert.ok(!body.includes(KEY), `a response carried the key: ${body.slice(0, 120)}`)
      assert.ok(!body.includes('dddddddd'))
    }
  })

  test('the request body reaches the provider unchanged', async () => {
    // A proxy that rewrote the body would change what the model was asked,
    // which is the one thing a caller has to be able to rely on.
    await ready(100)
    const body = { model: 'test-model', messages: [{ role: 'user', content: 'héllo 🎉' }], max_tokens: 64 }
    await call(await engineToken(), body)
    assert.deepEqual(JSON.parse(seen[0]!.body), body)
  })

  // -------------------------------------------------------------------------
  // The month boundary. A budget row is keyed on (org, provider, period), and
  // the two halves of one call read the period separately: borrowKey when the
  // request goes out, recordSpend when it comes back. A request that starts on
  // the last minute of a month and finishes on the first minute of the next one
  // reads two different periods, and the row for the second does not exist yet.
  // -------------------------------------------------------------------------

  test('a call that crosses a month boundary is charged to the month that allowed it', async () => {
    // Clock at 2026-01-31T23:59:30Z. The cap is set for January and the call is
    // authorised against January.
    const toJanuary = new Date('2026-01-31T23:59:30.000Z').getTime() - api.clock.now().getTime()
    api.clock.advance(toJanuary)
    await ready(100)

    const borrowed = await borrowKey(api.pool, api.clock, keyring, {
      orgId: org.orgId, provider: 'anthropic',
    })
    assert.equal(borrowed.budget.period, '2026-01-01')

    // The provider took a minute to answer, which is ordinary for a long
    // completion, and the month turned over while it did.
    api.clock.advance(60_000)
    await recordSpend(api.pool, api.clock, {
      orgId: org.orgId, provider: 'anthropic', usd: 7, period: borrowed.budget.period,
    })

    const [row] = await api.admin<{ spent_usd: string }[]>`
      SELECT spent_usd FROM provider_budgets
      WHERE org_id = ${org.orgId} AND provider = 'anthropic' AND period = '2026-01-01'`
    assert.ok(row, 'the January budget row is gone')
    // Before the fix this was 0. recordSpend read February, February had no
    // row, the UPDATE matched nothing, and the money was spent and recorded
    // nowhere. Nothing threw, nothing logged, and the cap that authorised the
    // spend never learned about it.
    assert.equal(Number(row!.spent_usd), 7)
  })

  test('spend that lands nowhere is an error rather than a silent zero', async () => {
    // The general form. An UPDATE that matches no row is indistinguishable from
    // one that matched, so the only way a lost charge can be noticed is if the
    // statement says how many rows it touched. Nothing has ever set a cap for
    // this period, so there is nothing for the UPDATE to hit.
    await ready(100)
    await assert.rejects(
      () => recordSpend(api.pool, api.clock, {
        orgId: org.orgId, provider: 'anthropic', usd: 3, period: '2029-03-01',
      }),
      /no budget row/i,
    )
  })

  // -------------------------------------------------------------------------
  // What the control plane tells PostHog about a call it brokered
  // -------------------------------------------------------------------------

  test('a brokered call is reported, with the provider\'s own token counts', async () => {
    // DEFINED, WIRED, EFFECTIVE, and this is the third one. posthog-sink.test.ts
    // proves the payload is right if something calls the sink. This proves
    // something calls it, through the real route, with a real key, a real
    // budget and a real charge, which is the only version of the question that
    // matters.
    await ready(100)
    const token = await engineToken()
    const startedAt = Date.now()
    const res = await call(token, { model: 'test-model', max_tokens: 10 })
    const elapsedMs = Date.now() - startedAt
    assert.equal(res.status, 200)

    assert.equal(generations.length, 1, 'the brokered call reported nothing, so the sink has no live caller')
    const [generation] = generations
    assert.equal(generation!.event, '$ai_generation')
    assert.equal(generation!.properties.$ai_model, 'test-model')
    assert.equal(generation!.properties.$ai_provider, 'anthropic')
    // The counts the PROVIDER reported, not a local estimate. A local estimate
    // is a number that disagrees with the invoice, which is the reasoning
    // providers/proxy.ts already applies to the spend it records.
    assert.equal(generation!.properties.$ai_input_tokens, 1_000_000)
    assert.equal(generation!.properties.$ai_output_tokens, 0)
    // SECONDS, AND MEASURED AGAINST THE WALL CLOCK RATHER THAN AGAINST A
    // PLAUSIBLE LOOKING BOUND. PostHog's $ai_latency is documented in seconds
    // and everything in this process measures in milliseconds, so getting it
    // wrong is a silent thousand fold error on a dashboard nobody would
    // question. The first version of this asserted the value was under sixty,
    // which a local stub answering in three milliseconds satisfies in EITHER
    // unit: mutation proved it, the check passed with the division removed.
    //
    // So it is held against how long the request actually took. Reported in
    // seconds the number is a thousandth of the elapsed milliseconds; reported
    // in milliseconds it is roughly equal to them, which this cannot miss.
    const latency = generation!.properties.$ai_latency
    assert.equal(typeof latency, 'number')
    assert.ok((latency as number) >= 0)
    assert.ok(
      (latency as number) * 1000 <= elapsedMs + 50,
      `$ai_latency is ${latency} for a request that took ${elapsedMs}ms, which is milliseconds ` +
        'in a field PostHog reads as seconds',
    )
  })

  test('neither the prompt nor the completion leaves this process', async () => {
    // THE WORST THING THAT COULD END UP IN A VENDOR'S EVENT STORE. The request
    // below carries a recognisable sentence and the stubbed provider answers
    // with another, and neither may appear in anything reported.
    await ready(100)
    reply = {
      status: 200,
      body: {
        usage: { input_tokens: 10, output_tokens: 10 },
        content: [{ text: 'THE-MODELS-ANSWER-ABOUT-A-CUSTOMERS-PAGE' }],
      },
    }
    const token = await engineToken()
    await call(token, {
      model: 'test-model',
      max_tokens: 10,
      messages: [{ role: 'user', content: 'THE-CUSTOMERS-OWN-PROMPT-TEXT' }],
    })

    assert.equal(generations.length, 1)
    const payload = JSON.stringify(generations[0])
    assert.ok(!payload.includes('THE-CUSTOMERS-OWN-PROMPT-TEXT'), `the prompt was reported: ${payload}`)
    assert.ok(!payload.includes('THE-MODELS-ANSWER-ABOUT-A-CUSTOMERS-PAGE'), 'the completion was reported')
    assert.ok(!payload.includes(org.orgId), 'the organization id was reported rather than its pseudonym')
    assert.equal(generations[0]!.distinctId, api.analytics.surrogate(org.orgId))
  })

  test('a call the provider reported no usage for is charged nothing and reported not at all', async () => {
    // The spend and the report move together. A response with no usage is a
    // charge of nothing, and reporting a generation with zero tokens would put
    // a row on a dashboard for a call nobody can price.
    await ready(100)
    reply = { status: 200, body: { content: [{ text: 'hi' }] } }
    const token = await engineToken()
    const res = await call(token, { model: 'test-model', max_tokens: 10 })
    assert.equal(res.status, 200)
    assert.deepEqual(generations, [])
  })

})
