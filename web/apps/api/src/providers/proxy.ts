// Model calls, made with the customer's key, from here rather than from their
// machine.
//
// THE POINT. A budget is only a spend cap if it is checked at the moment money
// would be spent, and the only place that can happen is where the request to
// the provider is made. Storing a key and letting a build machine spend it is a
// cap on nothing: the machine has the key, the machine makes the call, and this
// process finds out afterwards if it finds out at all.
//
// So the key never leaves this process. `borrowKey` checks the budget BEFORE it
// decrypts, the plaintext exists for the length of one outbound request, and
// what is recorded afterwards is the usage the provider itself reported.
//
// WHY THE PATHS LOOK LIKE THIS. Both callers -- runner/src/model.ts and
// engine/cmd/af-proxy/synth.go -- build their URL by concatenating a base onto
// the provider's own path, and both take that base from an environment
// variable. So a control plane that answers on `<base>/v1/messages` needs no
// change in either of them: point ANTHROPIC_BASE_URL here, put an Antifailure
// token in ANTHROPIC_API_KEY, and the same code that called Anthropic directly
// now calls Anthropic through a budget.
//
// Nothing here logs a body. A prompt is the page a customer's application
// rendered, and a response is what a model said about it; neither belongs in
// this system's logs, which is the same rule the rest of the product follows
// about production data.

import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import { borrowKey, recordSpend, ProviderKeyError } from './store.ts'
import { costOf, usageFrom, PricingError, type Price } from './pricing.ts'
import type { Provider } from './seal.ts'

export class ProxyError extends Error {
  readonly status: number
  constructor(message: string, status = 400) {
    super(message)
    this.status = status
  }
}

/** Where each provider lives, and what it calls its authorization header. */
const PROVIDERS: Record<Provider, { base: string; path: string; auth: (key: string) => Record<string, string> }> = {
  anthropic: {
    base: 'https://api.anthropic.com',
    path: '/v1/messages',
    auth: (key) => ({ 'x-api-key': key, 'anthropic-version': '2023-06-01' }),
  },
  openai: {
    base: 'https://api.openai.com',
    path: '/v1/chat/completions',
    auth: (key) => ({ authorization: `Bearer ${key}` }),
  },
}

export interface ProxyOptions {
  pool: Pool
  clock: Clock
  sealingKey: Buffer
  prices: Record<string, Price>
  /** Overridden in tests. */
  fetchImpl?: typeof fetch
  /** Overridden in tests, so nothing here reaches a real provider. */
  bases?: Partial<Record<Provider, string>>
}

export interface ProxyResult {
  status: number
  body: string
  /** What this call cost, already charged. Null when the provider reported no
   *  usage, which happens on an error response. */
  costUsd: number | null
  model: string
}

/**
 * Forwards one request, with the budget checked on the way in and the spend
 * recorded on the way out.
 *
 * Streaming is refused rather than passed through. Not an oversight: neither
 * caller in this repository streams, and a pass-through that could not read the
 * usage out of the stream would record no spend, which is a request that
 * costs money and charges nothing. Refusing says so; supporting it properly
 * means parsing the provider's event stream, which is worth doing when
 * something actually streams.
 */
export async function forward(
  options: ProxyOptions,
  provider: Provider,
  orgId: string,
  requestBody: string,
): Promise<ProxyResult> {
  const spec = PROVIDERS[provider]
  if (!spec) throw new ProxyError(`Unknown provider ${provider}.`, 404)

  let parsed: { model?: unknown; stream?: unknown; max_tokens?: unknown }
  try {
    parsed = JSON.parse(requestBody) as typeof parsed
  } catch {
    throw new ProxyError('The request body is not JSON.', 400)
  }
  const model = typeof parsed.model === 'string' ? parsed.model : ''
  if (!model) throw new ProxyError('The request body names no model.', 400)
  if (parsed.stream === true) {
    throw new ProxyError(
      'Streaming is not supported through a budgeted key yet. A streamed response ' +
        'whose usage this cannot read would cost money and record no spend, so it is ' +
        'refused rather than charged as free. Send the request without "stream": true.',
      400,
    )
  }

  // The price is looked up BEFORE the call. A model this cannot price must not
  // reach the provider at all: discovering it afterwards means the money is
  // already spent and the only choice left is whether to lie about it.
  costOf(options.prices, model, { inputTokens: 0, outputTokens: 0 })

  // Budget first, and the decrypt happens inside. A run with no allowance never
  // causes the key to exist in this process's memory.
  const borrowed = await borrowKey(options.pool, options.clock, options.sealingKey, {
    orgId,
    provider,
  })

  const doFetch = options.fetchImpl ?? fetch
  const base = options.bases?.[provider] ?? spec.base
  const res = await doFetch(base + spec.path, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      accept: 'application/json',
      ...spec.auth(borrowed.key),
    },
    body: requestBody,
  })

  const body = await res.text()

  // Usage comes from the provider's own answer rather than from counting
  // tokens here. A local estimate is a number that disagrees with the invoice,
  // and the whole reason to record spend is to agree with the invoice.
  let costUsd: number | null = null
  if (res.ok) {
    let usage = null
    try {
      usage = usageFrom(provider, JSON.parse(body))
    } catch {
      // A 200 whose body is not JSON. Nothing to charge and nothing to fix
      // here; the caller gets the body as it arrived.
    }
    if (usage) {
      costUsd = costOf(options.prices, model, usage)
      // Recorded even when it is tiny, and recorded after the call rather than
      // before, because what is charged has to be what was used. A failure to
      // record here would let a customer spend past the cap one request at a
      // time, so it is not swallowed.
      await recordSpend(options.pool, options.clock, { orgId, provider, usd: costUsd })
    }
  }

  return { status: res.status, body, costUsd, model }
}

export { ProviderKeyError, PricingError }
