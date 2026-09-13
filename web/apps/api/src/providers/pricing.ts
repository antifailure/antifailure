// What a model costs, so a budget can be a number of dollars.
//
// A cap in dollars needs tokens converted to dollars, and that conversion is
// the one part of this feature that cannot be derived from anything: it is a
// price list a provider publishes and changes. So it lives here, in one place,
// with the two rules that keep it honest.
//
// FIRST: an unpriced model is REFUSED, not charged zero. A run against a model
// this table does not know would otherwise spend a customer's money and add
// nothing to the total, which is precisely a spend cap that does not cap
// spending. Refusing names the model and says how to price it.
//
// SECOND: these defaults are a starting point an operator has to confirm.
// Prices change and this file does not know when. AF_MODEL_PRICES overrides
// them without a deploy, and `af provider list` shows what was actually
// charged so a wrong number here shows up as a number that disagrees with the
// provider's invoice rather than as a silent overrun.
//
// THIRD: every default names the page it was read from and the day it was
// read. On 2026-09-13 two of these rows were a price nobody charged any more:
// claude-sonnet-5 at 3/15 against a published 2/10, and claude-opus-5 at 15/75,
// which is the retired Opus 4.1 price, against a published 5/25. A customer's
// budget ran out at two thirds and at one third of what they had set, and
// nothing in this file said where either number had come from, so nothing
// could be checked against it. byok.test.ts refuses a default with no source.

export class PricingError extends Error {}

/** US dollars per million tokens, in and out. */
export interface Price {
  inputPerMillion: number
  outputPerMillion: number
}

/** Where a default price was read, so the number can be checked against it. */
export interface PriceSource {
  /** The provider's own published price list. */
  url: string
  /** The day that page was read, as YYYY-MM-DD. */
  fetched: string
}

/** A built-in price. An override from AF_MODEL_PRICES is the operator's own
 *  number and carries no source; a default always does. */
export interface DefaultPrice extends Price {
  source: PriceSource
}

/** Standard, uncached, non batch rates in each table. */
const ANTHROPIC_PRICING = 'https://platform.claude.com/docs/en/about-claude/pricing'
const OPENAI_PRICING = 'https://developers.openai.com/api/docs/pricing'

/**
 * The defaults.
 *
 * Deliberately short. A long list is a long list of numbers nobody checked; a
 * short one covers what this product actually asks for and makes anything else
 * an explicit decision by whoever adds it.
 */
export const DEFAULT_PRICES: Record<string, DefaultPrice> = {
  // Anthropic's page says 2/10 "is now the standard price" and that the rise to
  // 3/15 scheduled for September 1, 2026 "will not occur".
  'claude-sonnet-5': { inputPerMillion: 2, outputPerMillion: 10, source: { url: ANTHROPIC_PRICING, fetched: '2026-09-13' } },
  'claude-opus-5': { inputPerMillion: 5, outputPerMillion: 25, source: { url: ANTHROPIC_PRICING, fetched: '2026-09-13' } },
  'claude-haiku-4-5-20251001': { inputPerMillion: 1, outputPerMillion: 5, source: { url: ANTHROPIC_PRICING, fetched: '2026-09-13' } },
  'gpt-4.1': { inputPerMillion: 2, outputPerMillion: 8, source: { url: OPENAI_PRICING, fetched: '2026-09-13' } },
  'gpt-4.1-mini': { inputPerMillion: 0.4, outputPerMillion: 1.6, source: { url: OPENAI_PRICING, fetched: '2026-09-13' } },
}

/**
 * Reads an override table.
 *
 * The format is `model=input/output` separated by commas, in dollars per
 * million tokens: `claude-sonnet-5=2/10,gpt-4.1=2/8`. JSON would be tidier and
 * is worse here: this value is typed into a deployment template by a person,
 * and a JSON object inside a shell variable inside a YAML file is three layers
 * of quoting to get wrong.
 *
 * A malformed entry THROWS at start-up rather than being skipped. A skipped
 * entry is a model that silently falls back to a different price, which is the
 * same failure this whole file exists to prevent.
 */
export function pricesFrom(raw: string | undefined): Record<string, Price> {
  if (!raw || !raw.trim()) return { ...DEFAULT_PRICES }
  // Plain Price, not DefaultPrice: an entry below is the operator's own number,
  // which has no source to name.
  const prices: Record<string, Price> = { ...DEFAULT_PRICES }
  for (const entry of raw.split(',')) {
    const trimmed = entry.trim()
    if (!trimmed) continue
    const match = /^([^=]+)=([0-9]*\.?[0-9]+)\/([0-9]*\.?[0-9]+)$/.exec(trimmed)
    if (!match) {
      throw new PricingError(
        `AF_MODEL_PRICES entry ${JSON.stringify(trimmed)} is not model=input/output, ` +
          'for example claude-sonnet-5=2/10 in US dollars per million tokens.',
      )
    }
    prices[match[1]!.trim()] = {
      inputPerMillion: Number(match[2]),
      outputPerMillion: Number(match[3]),
    }
  }
  return prices
}

export interface Usage {
  inputTokens: number
  outputTokens: number
}

/**
 * What a call cost, in dollars.
 *
 * Throws for a model with no price. See the top of the file: this is the
 * difference between a cap and a suggestion.
 */
export function costOf(prices: Record<string, Price>, model: string, usage: Usage): number {
  const price = prices[model]
  if (!price) {
    throw new PricingError(
      `No price is configured for ${JSON.stringify(model)}, so what it costs cannot be ` +
        'charged against a budget and the request is refused rather than spent unmetered. ' +
        `Add it to AF_MODEL_PRICES as ${model}=<input>/<output>, in US dollars per million tokens, ` +
        "from the provider's published price list.",
    )
  }
  return (
    (usage.inputTokens / 1_000_000) * price.inputPerMillion +
    (usage.outputTokens / 1_000_000) * price.outputPerMillion
  )
}

/** Pulls usage out of whichever provider answered. */
export function usageFrom(provider: string, body: unknown): Usage | null {
  const u = (body as { usage?: Record<string, unknown> })?.usage
  if (!u) return null
  if (provider === 'anthropic') {
    const input = Number(u.input_tokens ?? 0)
    const output = Number(u.output_tokens ?? 0)
    if (!Number.isFinite(input) || !Number.isFinite(output)) return null
    return { inputTokens: input, outputTokens: output }
  }
  const input = Number(u.prompt_tokens ?? 0)
  const output = Number(u.completion_tokens ?? 0)
  if (!Number.isFinite(input) || !Number.isFinite(output)) return null
  return { inputTokens: input, outputTokens: output }
}
