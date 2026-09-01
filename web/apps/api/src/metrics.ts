// What the control plane exposes to Prometheus, and why it is only ever
// counters the process kept itself.
//
// The obvious design is to query the tables on every scrape: environments
// grouped by state, runs grouped by verdict. It is the wrong one here, for a
// reason specific to this control plane. Tenancy is enforced by row level
// security rather than by a WHERE clause, so a query that aggregates across
// every organization needs a role that can read every organization's rows.
// Creating one in order to draw a graph would put the strongest read in the
// system on the least important path, and it would sit there being scraped
// every fifteen seconds forever. A counter incremented where the work happens
// costs nothing, crosses no tenant boundary, and answers the same questions.
//
// So the numbers here are counters and histograms over things this process did,
// plus one build-info gauge. Several replicas each expose their own and
// Prometheus sums them, which is the normal shape and the one the alert rules
// in observability/alerts are written against.
//
// The SLOs in the plan are what this exists to serve, and each one is named
// against the metric that measures it in observability/alerts/antifailure.rules.yml:
//
//   environment creation success   99.5 percent   af_environment_outcomes_total
//   time to preview                p95 under 8m   af_environment_ready_seconds
//   API availability               99.9 percent   af_http_requests_total
//   ingestion loss                 zero           af_ingest_events_total{outcome="rejected"}
//
// A rule that names a metric this file does not export would fire never, which
// is worse than an alert that fires wrongly because nobody finds out. There is
// a test that reads the rules and the dashboard and fails on any metric name
// neither this file nor the registry produces.

/** A label set. Values are escaped on the way out; names are not, so they are
 *  written by this file rather than taken from a request. */
export type Labels = Record<string, string>

interface Series {
  labels: Labels
  value: number
}

function labelKey(labels: Labels): string {
  const names = Object.keys(labels).sort()
  return names.map((n) => `${n}\u0000${labels[n]}`).join('\u0001')
}

/** Escapes a label value for the text exposition format. Backslash, newline and
 *  double quote, in that order, because escaping the backslash second would
 *  escape the backslashes the other two just introduced. */
function escapeLabelValue(v: string): string {
  return v.replace(/\\/g, '\\\\').replace(/\n/g, '\\n').replace(/"/g, '\\"')
}

function escapeHelp(v: string): string {
  return v.replace(/\\/g, '\\\\').replace(/\n/g, '\\n')
}

function renderLabels(labels: Labels, extra?: Labels): string {
  const all = { ...labels, ...(extra ?? {}) }
  const names = Object.keys(all).sort()
  if (names.length === 0) return ''
  return `{${names.map((n) => `${n}="${escapeLabelValue(all[n] ?? '')}"`).join(',')}}`
}

/** A number that only goes up. */
export class Counter {
  readonly name: string
  readonly help: string
  private readonly series = new Map<string, Series>()

  constructor(name: string, help: string) {
    this.name = name
    this.help = help
  }

  inc(labels: Labels = {}, by = 1): void {
    const key = labelKey(labels)
    const existing = this.series.get(key)
    if (existing) {
      existing.value += by
      return
    }
    this.series.set(key, { labels, value: by })
  }

  /** For tests and for the drift check. */
  get(labels: Labels = {}): number {
    return this.series.get(labelKey(labels))?.value ?? 0
  }

  render(): string {
    const lines = [`# HELP ${this.name} ${escapeHelp(this.help)}`, `# TYPE ${this.name} counter`]
    for (const s of this.series.values()) {
      lines.push(`${this.name}${renderLabels(s.labels)} ${s.value}`)
    }
    return lines.join('\n')
  }
}

/** A number that goes both ways. */
export class Gauge {
  readonly name: string
  readonly help: string
  private readonly series = new Map<string, Series>()

  constructor(name: string, help: string) {
    this.name = name
    this.help = help
  }

  set(value: number, labels: Labels = {}): void {
    this.series.set(labelKey(labels), { labels, value })
  }

  get(labels: Labels = {}): number {
    return this.series.get(labelKey(labels))?.value ?? 0
  }

  render(): string {
    const lines = [`# HELP ${this.name} ${escapeHelp(this.help)}`, `# TYPE ${this.name} gauge`]
    for (const s of this.series.values()) {
      lines.push(`${this.name}${renderLabels(s.labels)} ${s.value}`)
    }
    return lines.join('\n')
  }
}

interface Bucketed {
  labels: Labels
  counts: number[]
  sum: number
  count: number
}

/**
 * A cumulative histogram, which is the only shape histogram_quantile can read.
 *
 * The buckets are chosen per metric rather than shared, because a default set
 * spanning milliseconds to minutes puts every environment creation in the last
 * bucket and every HTTP request in the first, and a quantile computed from a
 * bucket everything falls into is a quantile made up out of its boundaries.
 */
export class Histogram {
  readonly name: string
  readonly help: string
  readonly bounds: number[]
  private readonly series = new Map<string, Bucketed>()

  constructor(name: string, help: string, bounds: number[]) {
    this.name = name
    this.help = help
    this.bounds = [...bounds].sort((a, b) => a - b)
  }

  observe(value: number, labels: Labels = {}): void {
    const key = labelKey(labels)
    let s = this.series.get(key)
    if (!s) {
      s = { labels, counts: new Array(this.bounds.length).fill(0), sum: 0, count: 0 }
      this.series.set(key, s)
    }
    s.sum += value
    s.count += 1
    for (let i = 0; i < this.bounds.length; i++) {
      if (value <= (this.bounds[i] ?? Infinity)) s.counts[i] = (s.counts[i] ?? 0) + 1
    }
  }

  render(): string {
    const lines = [`# HELP ${this.name} ${escapeHelp(this.help)}`, `# TYPE ${this.name} histogram`]
    for (const s of this.series.values()) {
      // The stored counts are already cumulative: observe increments every
      // bucket whose bound is at or above the value, which is what le means.
      for (let i = 0; i < this.bounds.length; i++) {
        lines.push(
          `${this.name}_bucket${renderLabels(s.labels, { le: formatBound(this.bounds[i] ?? 0) })} ${s.counts[i] ?? 0}`,
        )
      }
      lines.push(`${this.name}_bucket${renderLabels(s.labels, { le: '+Inf' })} ${s.count}`)
      lines.push(`${this.name}_sum${renderLabels(s.labels)} ${s.sum}`)
      lines.push(`${this.name}_count${renderLabels(s.labels)} ${s.count}`)
    }
    return lines.join('\n')
  }
}

function formatBound(b: number): string {
  return Number.isInteger(b) ? b.toFixed(1) : String(b)
}

type Metric = Counter | Gauge | Histogram

/** Everything one process exposes. */
export class Registry {
  private readonly metrics: Metric[] = []

  register<T extends Metric>(m: T): T {
    this.metrics.push(m)
    return m
  }

  names(): string[] {
    return this.metrics.map((m) => m.name)
  }

  /** The text exposition format, ending in a newline as the format requires. */
  render(): string {
    return this.metrics.map((m) => m.render()).join('\n') + '\n'
  }
}

/**
 * The control plane's metrics.
 *
 * Built per server rather than as module state, so that two servers in one test
 * process do not share counters and a test cannot pass because of what an
 * earlier one did.
 */
import { consoleClass } from './limits.ts'

export interface ControlPlaneMetrics {
  registry: Registry
  info: Gauge
  httpRequests: Counter
  httpDuration: Histogram
  rateLimited: Counter
  ingestEvents: Counter
  ingestBatches: Counter
  environmentOutcomes: Counter
  environmentReadySeconds: Histogram
  environmentTransitions: Counter
  runVerdicts: Counter
}

export function createMetrics(version = 'dev'): ControlPlaneMetrics {
  const registry = new Registry()

  const info = registry.register(
    new Gauge('af_control_plane_info', 'Always 1. The labels carry the build, so a dashboard can say which version produced a number.'),
  )
  info.set(1, { version })

  const metrics: ControlPlaneMetrics = {
    registry,
    info,

    httpRequests: registry.register(
      new Counter(
        'af_http_requests_total',
        'Requests served, by route and status class. The availability SLO is the ratio of 5xx to the whole.',
      ),
    ),
    httpDuration: registry.register(
      new Histogram(
        'af_http_request_seconds',
        'How long the control plane took to answer.',
        // A control plane request that takes a minute has already failed the
        // request, and the statement timeout is fifteen seconds, so the top
        // bucket is above that and no higher.
        [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30],
      ),
    ),
    rateLimited: registry.register(
      new Counter(
        'af_rate_limited_total',
        'Requests refused with 429, by route. A rate limit nobody can see is a rate limit nobody knows is misconfigured.',
      ),
    ),

    ingestEvents: registry.register(
      new Counter(
        'af_ingest_events_total',
        'Engine events by what happened to them: accepted, duplicate, rejected, or unprojected. '
        + 'The ingestion loss SLO is that rejected stays at zero. Unprojected is a subset of accepted: '
        + 'stored, and applied to no environment row because the sender did not say which repository the '
        + 'environment belongs to, which is an engine older than the release that started reporting it.',
      ),
    ),
    ingestBatches: registry.register(
      new Counter('af_ingest_batches_total', 'Ingestion requests, by outcome.'),
    ),

    environmentOutcomes: registry.register(
      new Counter(
        'af_environment_outcomes_total',
        'Environments that reached a terminal creation outcome: ready or failed. The creation success SLO is the ratio.',
      ),
    ),
    environmentReadySeconds: registry.register(
      new Histogram(
        'af_environment_ready_seconds',
        'Time from an environment being asked for to it being reachable, as reported by the engine that built it.',
        // The SLO is p95 under eight minutes, so there is a boundary at 480
        // exactly. A quantile estimated across a bucket that straddles the
        // target is an estimate of whether the target was met.
        [10, 30, 60, 120, 240, 480, 900, 1800],
      ),
    ),
    environmentTransitions: registry.register(
      new Counter(
        'af_environment_transitions_total',
        'Environment state changes, by the state entered. Counted rather than gauged: a counter needs no query across tenants, which row level security would refuse anyway.',
      ),
    ),
    runVerdicts: registry.register(
      new Counter('af_run_verdicts_total', 'Agent verdicts recorded, by verdict.'),
    ),
  }
  return metrics
}

/**
 * Reduces a path to something bounded before it becomes a label.
 *
 * A label whose value is a raw path is an unbounded label, and an unbounded
 * label is how a metrics endpoint becomes the largest thing in the process. The
 * bounded set is the routes the server declares in ENDPOINT_LIMITS, so the
 * label is the DECLARED KEY that matched, never the path that matched it.
 *
 * That distinction is the whole function, and getting it wrong is not obvious.
 * The first version asked "is this path declared?" and, when the answer was
 * yes, used the path. `GET /v1/environments/:envId` is declared, so every
 * environment identifier anybody ever fetched became its own series while the
 * code looked like it was bounding them. The test that two different paths
 * collapse into one series is what found it, twice: once against the wrong
 * comparison operator, and once against this.
 */
export function routeLabel(method: string, path: string, declared: readonly string[]): string {
  const verb = method.toUpperCase()

  const exact = `${verb} ${path}`
  if (declared.includes(exact)) return exact

  // The one real wildcard. Every tRPC procedure is one route as far as a
  // rate limit is concerned, and so it is here.
  if (path.startsWith('/trpc/')) {
    const wildcard = `${verb} /trpc/*`
    if (declared.includes(wildcard)) return wildcard
  }

  // The console's files, classified exactly as the rate limiter classifies
  // them, by calling the same function rather than keeping a second copy of
  // the rule. A metric that disagrees with the limiter about which class a
  // request is in cannot be used to tune the limiter.
  const asConsole = consoleClass(verb, path)
  if (asConsole && declared.includes(asConsole)) return asConsole

  const segments = path.split('/')
  for (const key of declared) {
    const space = key.indexOf(' ')
    if (key.slice(0, space) !== verb) continue
    const pattern = key.slice(space + 1).split('/')
    if (pattern.length !== segments.length) continue
    if (pattern.every((part, i) => part.startsWith(':') || part === segments[i])) {
      return key
    }
  }

  // Everything the server does not declare, in one series. A 404 storm from a
  // scanner is one line here rather than one line per URL it tried.
  return `${verb} other`
}

/** 2xx, 4xx, 5xx rather than every code, for the same bounding reason. */
export function statusClass(status: number): string {
  if (status >= 500) return '5xx'
  if (status >= 400) return '4xx'
  if (status >= 300) return '3xx'
  return '2xx'
}
