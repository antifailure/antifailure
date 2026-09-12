// Reading the sink an installation has actually configured.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The rule is `ee/engine/auditsink/configure.go`'s and it is the same rule for
// the same reason. Somebody who writes AF_AUDIT_STREAM_SINK=splunk has said
// that every privileged action in this control plane must reach their SIEM.
// Starting anyway with the sink unbuilt means nothing is forwarded and nothing
// says so, which is indistinguishable from a quiet week. That is not a degraded
// feature; it is a compliance control reporting itself as held while holding
// nothing, which is the exact defect this whole file exists downstream of.
//
// So a named sink that cannot be built REFUSES, with the variable that is
// missing named in the message. An unset variable builds nothing and says so
// once, which is the ordinary case. Nothing is ever detected: a control plane
// that happens to carry a webhook URL for something unrelated must not decide
// on its own to start posting an organization's audit trail to it.
//
// WHAT IS NOT HERE, SAID PLAINLY RATHER THAN IMPLIED BY ITS ABSENCE.
// `ObjectStoreSink` exists in sinks.ts and cannot be built from the
// environment, because it takes a `put` callback and this side of the product
// has no S3 or Blob signer to supply one. It is reachable by an embedder that
// passes a Sink directly and it is not reachable from a container's
// configuration, and `Known()` below lists only what an operator can actually
// turn on. A name accepted here that then wrote nowhere would be the same
// failure one layer along.

import {
  EventHubsSink,
  SplunkSink,
  WebhookSink,
  type Fetcher,
} from './sinks.ts'
import type { Sink } from './sink.ts'

/** Names the variable that chooses the destination. */
export const SinkEnv = 'AF_AUDIT_STREAM_SINK'

/** The variables each sink reads. Constants rather than inline strings so that
 *  the message listing what is missing and the code reading it cannot drift. */
export const KeyEnv = 'AF_AUDIT_STREAM_KEY'
export const IntervalEnv = 'AF_AUDIT_STREAM_INTERVAL_MS'
export const BatchEnv = 'AF_AUDIT_STREAM_BATCH'
export const DeliveryBatchEnv = 'AF_AUDIT_STREAM_DELIVERY_BATCH'

export const SplunkUrlEnv = 'AF_AUDIT_STREAM_SPLUNK_URL'
export const SplunkTokenEnv = 'AF_AUDIT_STREAM_SPLUNK_TOKEN'
export const SplunkIndexEnv = 'AF_AUDIT_STREAM_SPLUNK_INDEX'
export const SplunkSourcetypeEnv = 'AF_AUDIT_STREAM_SPLUNK_SOURCETYPE'

export const EventHubsUrlEnv = 'AF_AUDIT_STREAM_EVENT_HUBS_URL'
export const EventHubsAuthorizationEnv = 'AF_AUDIT_STREAM_EVENT_HUBS_AUTHORIZATION'

export const WebhookUrlEnv = 'AF_AUDIT_STREAM_WEBHOOK_URL'
export const WebhookSecretEnv = 'AF_AUDIT_STREAM_WEBHOOK_SECRET'

/** The destinations an operator can name. Sorted, for the message that lists
 *  them when somebody names one that does not exist. */
export function known(): string[] {
  return ['event_hubs', 'splunk', 'webhook']
}

/** A configuration mistake, separated from every other error so the caller can
 *  refuse to start rather than log and continue. */
export class SinkRefused extends Error {}

export interface StreamConfig {
  sink: Sink
  key: string
  intervalMs: number
  /** Entries one pass reads. */
  batchSize: number
  /** Entries one delivery carries. A different question from the one above: a
   *  pass size is how fast the forwarder catches up and a delivery size is what
   *  one request to somebody else's collector may hold. Defaults to the pass
   *  size, so an operator who does not care sets one number. */
  deliveryBatchSize: number
}

/** How often a pass runs when nothing says otherwise. Ten seconds, because an
 *  audit stream is read during an incident and five minutes of lag is five
 *  minutes of a security team seeing nothing after something started. */
export const DEFAULT_INTERVAL_MS = 10_000

/** How many entries one pass reads when nothing says otherwise. */
export const DEFAULT_BATCH = 500

/**
 * Builds the configured sink, or null when none is configured.
 *
 * `fetch` is a parameter for the reason sinks.ts gives about its own: it is
 * what lets an operator route through their own proxy or add a header their
 * gateway requires, and it is what lets a test hold a real receiver.
 */
export function fromEnvironment(
  env: NodeJS.ProcessEnv,
  fetcher: Fetcher = (url, init) => fetch(url, init),
): StreamConfig | null {
  const name = (env[SinkEnv] ?? '').trim().toLowerCase()
  if (name === '') return null

  if (!known().includes(name)) {
    throw new SinkRefused(
      `${SinkEnv} names ${name} and this build has no such sink. It knows ${known().join(', ')}. ` +
        'An object store sink exists in the code and cannot be configured from the environment, ' +
        'because it needs a signer this side of the product does not carry.',
    )
  }

  const key = (env[KeyEnv] ?? '').trim()
  if (key === '') {
    // Required rather than defaulted. The manifest is what lets an auditor
    // check a batch without asking this control plane anything, and a manifest
    // signed under a key everybody knows is a decoration rather than evidence.
    throw new SinkRefused(
      `${SinkEnv} is set to ${name} and ${KeyEnv} is not. Batch manifests are signed under it, ` +
        'and a signature under a key nobody chose proves nothing. Generate one with ' +
        '`openssl rand -base64 32`.',
    )
  }

  return { sink: build(name, env, fetcher), key, ...scheduleFromEnvironment(env) }
}

export interface Schedule {
  intervalMs: number
  batchSize: number
  deliveryBatchSize: number
}

/**
 * How often and how much, whether or not the installation has a sink of its own.
 *
 * Separate from `fromEnvironment` because an installation whose organizations
 * choose their own destinations runs a forwarder with no installation sink at
 * all, and the operator's interval and batch settings must still apply to it.
 * Refuses a malformed number for the reason `positive` gives.
 */
export function scheduleFromEnvironment(env: NodeJS.ProcessEnv): Schedule {
  const batchSize = positive(env, BatchEnv, DEFAULT_BATCH)
  return {
    intervalMs: positive(env, IntervalEnv, DEFAULT_INTERVAL_MS),
    batchSize,
    deliveryBatchSize: positive(env, DeliveryBatchEnv, batchSize),
  }
}

function build(name: string, env: NodeJS.ProcessEnv, fetcher: Fetcher): Sink {
  switch (name) {
    case 'splunk':
      return new SplunkSink({
        url: required(env, SplunkUrlEnv, name),
        token: required(env, SplunkTokenEnv, name),
        fetch: fetcher,
        ...(env[SplunkIndexEnv] ? { index: env[SplunkIndexEnv] } : {}),
        ...(env[SplunkSourcetypeEnv] ? { sourcetype: env[SplunkSourcetypeEnv] } : {}),
      })
    case 'event_hubs':
      return new EventHubsSink({
        url: required(env, EventHubsUrlEnv, name),
        authorization: required(env, EventHubsAuthorizationEnv, name),
        fetch: fetcher,
      })
    default:
      return new WebhookSink({
        url: required(env, WebhookUrlEnv, name),
        secret: required(env, WebhookSecretEnv, name),
        fetch: fetcher,
      })
  }
}

function required(env: NodeJS.ProcessEnv, variable: string, sink: string): string {
  const value = (env[variable] ?? '').trim()
  if (value === '') {
    throw new SinkRefused(
      `${SinkEnv} is set to ${sink} and ${variable} is not set. Refusing to start rather than ` +
        'forwarding nothing quietly.',
    )
  }
  return value
}

/** A whole number above zero, or the default. Refuses anything else rather than
 *  falling back, because a typo that silently becomes ten seconds is a
 *  configuration an operator believes they changed. */
function positive(env: NodeJS.ProcessEnv, variable: string, fallback: number): number {
  const raw = (env[variable] ?? '').trim()
  if (raw === '') return fallback
  const value = Number(raw)
  if (!Number.isInteger(value) || value <= 0) {
    throw new SinkRefused(
      `${variable} is ${JSON.stringify(raw)}; it has to be a whole number above zero.`,
    )
  }
  return value
}
