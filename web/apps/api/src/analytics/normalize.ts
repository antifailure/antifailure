// Turning what the control plane knows into what the catalog allows.
//
// Every function here takes something unbounded and returns a member of a
// closed set. That is the whole file, and putting them together means the
// mapping is one place a reviewer can read rather than a decision repeated at
// each producer, where the twelfth one gets it wrong.
//
// Note what is NOT here: the referrer, the URL and the query string. Those are
// normalized in the browser, in www/lib/analytics.ts, so the raw values never
// cross the network at all rather than crossing it and being discarded
// politely. A function here that took a referrer would mean the raw value had
// already been sent, and by then the privacy claim is about what this process
// chooses to do rather than about what it can do.

import { DURATION_BUCKETS, PLANS, RUNTIME_CLASSES } from './catalog.ts'

/**
 * A runtime string, as a class.
 *
 * The engine reports a runtime name chosen by the customer's manifest, so it is
 * free text as far as this process is concerned and could hold a project name.
 * The class is what a chart is about anyway: whether people run this on their
 * own machine, in a container, or on a cluster.
 *
 * Substring matching rather than equality, because the names the engine reports
 * are compound ("docker-compose", "k8s-staging"), and an unrecognised one is
 * `unknown` rather than being passed through. Passing it through is the exact
 * shape of leak this file exists to prevent.
 */
export function runtimeClass(runtime: unknown): (typeof RUNTIME_CLASSES)[number] {
  if (typeof runtime !== 'string') return 'unknown'
  const value = runtime.toLowerCase()
  if (value.includes('kube') || value.includes('k8s')) return 'kubernetes'
  if (value.includes('docker') || value.includes('compose') || value.includes('container')) {
    return 'docker'
  }
  if (value.includes('azure') || value.includes('aws') || value.includes('gcp') || value.includes('cloud')) {
    return 'cloud'
  }
  if (value.includes('local') || value.includes('host')) return 'local'
  return 'unknown'
}

/**
 * A duration in milliseconds, as a bucket.
 *
 * A duration to the millisecond is a fingerprint: an environment that lived for
 * 4h13m52.418s is very likely one particular environment. A bucket answers the
 * question anybody actually asks, which is whether people leave things up
 * overnight, and answers it about a population rather than about a run.
 *
 * A negative duration is `under_1m` rather than an error. It means a clock
 * disagreed, the projection already clamps that case, and refusing the event
 * would lose the teardown as well as the duration.
 */
export function durationBucket(ms: number): (typeof DURATION_BUCKETS)[number] {
  if (!Number.isFinite(ms) || ms < 60_000) return 'under_1m'
  if (ms < 5 * 60_000) return 'under_5m'
  if (ms < 30 * 60_000) return 'under_30m'
  if (ms < 2 * 3_600_000) return 'under_2h'
  if (ms < 12 * 3_600_000) return 'under_12h'
  if (ms < 24 * 3_600_000) return 'under_24h'
  return 'over_24h'
}

/** A plan name, as one of the plans that exist. An unrecognised one is `free`,
 *  because the plans are a closed set this process defines and anything else is
 *  a bug rather than a customer on a plan nobody has heard of. */
export function planName(plan: unknown): (typeof PLANS)[number] {
  return typeof plan === 'string' && (PLANS as readonly string[]).includes(plan)
    ? (plan as (typeof PLANS)[number])
    : 'free'
}

/** A Stripe subscription status, as one of the ones the catalog names. Anything
 *  else is `other`, so a status Stripe adds later is counted rather than
 *  refused, and is visible as a bar labelled other rather than as a gap. */
export function subscriptionStatus(status: unknown): string {
  const known = [
    'trialing', 'active', 'past_due', 'canceled', 'unpaid',
    'incomplete', 'incomplete_expired', 'paused',
  ]
  return typeof status === 'string' && known.includes(status) ? status : 'other'
}

/** A verdict, as one of the five the product has. Anything else is
 *  `unverified`, which is the conservative direction: a verdict this process
 *  does not recognise has not proved anything. */
export function verdictValue(value: unknown): string {
  const known = ['pass', 'fail', 'flaky', 'blocked', 'unverified']
  return typeof value === 'string' && known.includes(value) ? value : 'unverified'
}

/** A run kind, as one of the four the product has. */
export function runKind(kind: unknown): string {
  const known = ['test', 'agent', 'load', 'migration']
  return typeof kind === 'string' && known.includes(kind) ? kind : 'other'
}
