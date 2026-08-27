import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  Counter,
  Gauge,
  Histogram,
  Registry,
  createMetrics,
  routeLabel,
  statusClass,
} from '../src/metrics.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, '../../../..')
const rulesPath = path.join(repoRoot, 'observability/alerts/antifailure.rules.yml')
const dashboardPath = path.join(repoRoot, 'observability/dashboards/control-plane.json')

// ---------------------------------------------------------------------------
// The drift check, which is the reason the rules and the dashboard live in this
// repository rather than in a Grafana someone clicked together.
//
// An alert on a metric that does not exist fires never. A panel querying a
// metric that does not exist draws an empty graph, and an empty graph reads as
// a quiet system rather than as a broken dashboard. Both failures are silent
// and both are only discovered by the incident they were written for. So the
// names are checked against the registry that actually produces them.
//
// This is the same guard as the one over the engine's control plane event
// vocabulary, and for the same reason: two artefacts that have to agree, in two
// formats, with nothing in either able to notice when they stop.
// ---------------------------------------------------------------------------

/** Every af_-prefixed identifier in a file, with the suffixes Prometheus adds to
 *  a histogram removed, since those are derived rather than declared. */
function metricNamesIn(text: string): Set<string> {
  const found = new Set<string>()
  for (const match of text.matchAll(/\baf_[a-z0-9_]+/g)) {
    found.add(match[0].replace(/_(bucket|sum|count)$/, ''))
  }
  return found
}

function exported(): Set<string> {
  return new Set(createMetrics('test').registry.names())
}

test('every metric an alert rule names is one the control plane exports', () => {
  const rules = readFileSync(rulesPath, 'utf8')
  const referenced = metricNamesIn(rules)
  assert.ok(referenced.size >= 6, `parsed only ${referenced.size} metric names out of the rules file`)

  const have = exported()
  const missing = [...referenced].filter((n) => !have.has(n)).sort()
  assert.deepEqual(
    missing,
    [],
    `observability/alerts/antifailure.rules.yml alerts on metrics nothing exports: ${missing.join(', ')}. ` +
      `An alert on a metric that does not exist fires never, and you find out during the outage it was written for.`,
  )
})

test('every metric a dashboard panel queries is one the control plane exports', () => {
  const dashboard = readFileSync(dashboardPath, 'utf8')
  const referenced = metricNamesIn(dashboard)
  assert.ok(referenced.size >= 6, `parsed only ${referenced.size} metric names out of the dashboard`)

  const have = exported()
  const missing = [...referenced].filter((n) => !have.has(n)).sort()
  assert.deepEqual(
    missing,
    [],
    `observability/dashboards/control-plane.json queries metrics nothing exports: ${missing.join(', ')}. ` +
      `A panel on a metric that does not exist draws an empty graph, which reads as a quiet system.`,
  )
})

test('the dashboard is valid JSON with a stable uid and a panel per objective', () => {
  const dashboard = JSON.parse(readFileSync(dashboardPath, 'utf8'))
  assert.equal(dashboard.uid, 'antifailure-control-plane', 'the uid is what makes a re-import an update')
  assert.ok(Array.isArray(dashboard.panels) && dashboard.panels.length > 5)
  for (const panel of dashboard.panels) {
    assert.ok(panel.title, `a panel with no title: ${JSON.stringify(panel).slice(0, 80)}`)
    assert.ok(panel.gridPos, `panel ${panel.title} has no position`)
    if (panel.type === 'row') continue
    assert.ok(
      Array.isArray(panel.targets) && panel.targets.length > 0,
      `panel ${panel.title} queries nothing`,
    )
    for (const target of panel.targets) {
      assert.ok(target.expr, `panel ${panel.title} has a target with no expression`)
    }
  }
})

test('every alert names a runbook, because an alert with no next step is a wake-up call', () => {
  const rules = readFileSync(rulesPath, 'utf8')
  const alerts = [...rules.matchAll(/- alert: (\w+)/g)].map((m) => m[1])
  assert.ok(alerts.length >= 6, `found only ${alerts.length} alerts`)

  // Split on the alert boundary so each block is checked on its own rather than
  // the file being checked as a whole, which would pass on one runbook.
  const blocks = rules.split(/- alert: /).slice(1)
  assert.equal(blocks.length, alerts.length)
  for (const [i, block] of blocks.entries()) {
    assert.match(
      block,
      /runbook: https:\/\/antifailure\.dev\/docs\/self-hosting\/operations/,
      `${alerts[i]} has no runbook link`,
    )
    assert.match(block, /severity: (page|ticket)/, `${alerts[i]} has no severity`)
    assert.match(block, /summary:/, `${alerts[i]} has no summary`)
  }
})

// ---------------------------------------------------------------------------
// The exposition format. Getting it wrong is not a crash, it is a scrape that
// silently drops the malformed lines, so the format is asserted rather than
// eyeballed.
// ---------------------------------------------------------------------------

test('a counter renders in the text exposition format', () => {
  const c = new Counter('af_test_total', 'A counter.')
  c.inc({ outcome: 'accepted' })
  c.inc({ outcome: 'accepted' })
  c.inc({ outcome: 'rejected' }, 3)

  const out = c.render()
  assert.match(out, /^# HELP af_test_total A counter\.$/m)
  assert.match(out, /^# TYPE af_test_total counter$/m)
  assert.match(out, /^af_test_total\{outcome="accepted"\} 2$/m)
  assert.match(out, /^af_test_total\{outcome="rejected"\} 3$/m)
})

test('label values and help text are escaped, so one odd character cannot break a scrape', () => {
  const c = new Counter('af_test_total', 'Quotes " and a\nnewline.')
  c.inc({ route: 'GET /a"b\\c', note: 'line\nbreak' })

  const out = c.render()
  assert.match(out, /# HELP af_test_total Quotes " and a\\nnewline\./)
  assert.ok(out.includes('route="GET /a\\"b\\\\c"'), out)
  assert.ok(out.includes('note="line\\nbreak"'), out)
})

test('histogram buckets are cumulative, which is the only shape histogram_quantile can read', () => {
  const h = new Histogram('af_test_seconds', 'A histogram.', [1, 5, 10])
  h.observe(0.5)
  h.observe(3)
  h.observe(7)
  h.observe(100)

  const out = h.render()
  assert.match(out, /^af_test_seconds_bucket\{le="1.0"\} 1$/m)
  assert.match(out, /^af_test_seconds_bucket\{le="5.0"\} 2$/m, 'le="5" must include the 0.5 and the 3')
  assert.match(out, /^af_test_seconds_bucket\{le="10.0"\} 3$/m)
  assert.match(out, /^af_test_seconds_bucket\{le="\+Inf"\} 4$/m, '+Inf is every observation')
  assert.match(out, /^af_test_seconds_sum 110.5$/m)
  assert.match(out, /^af_test_seconds_count 4$/m)
})

test('a bucket boundary is inclusive, because le means less than or equal', () => {
  const h = new Histogram('af_test_seconds', 'A histogram.', [1])
  h.observe(1)
  assert.match(h.render(), /^af_test_seconds_bucket\{le="1.0"\} 1$/m)
})

test('bounds are sorted even when they are declared out of order', () => {
  const h = new Histogram('af_test_seconds', 'A histogram.', [10, 1, 5])
  h.observe(3)
  const lines = h
    .render()
    .split('\n')
    .filter((l) => l.includes('_bucket') && !l.includes('+Inf'))
  assert.deepEqual(
    lines.map((l) => l.match(/le="([^"]+)"/)![1]),
    ['1.0', '5.0', '10.0'],
  )
})

test('a gauge is set rather than accumulated', () => {
  const g = new Gauge('af_test_info', 'A gauge.')
  g.set(1, { version: 'v0.1.1' })
  g.set(1, { version: 'v0.1.1' })
  const lines = g.render().split('\n').filter((l) => l.startsWith('af_test_info'))
  assert.equal(lines.length, 1, 'setting twice is one series, not two')
  assert.match(lines[0], /af_test_info\{version="v0\.1\.1"\} 1/)
})

test('the registry renders every metric and ends with a newline', () => {
  const r = new Registry()
  r.register(new Counter('af_a_total', 'a'))
  r.register(new Gauge('af_b', 'b'))
  const out = r.render()
  assert.ok(out.includes('af_a_total'))
  assert.ok(out.includes('af_b'))
  assert.ok(out.endsWith('\n'), 'the format requires a trailing newline')
})

test('two servers do not share counters', () => {
  const first = createMetrics('test')
  const second = createMetrics('test')
  first.httpRequests.inc({ route: 'GET /health', status_class: '2xx' })
  assert.equal(first.httpRequests.get({ route: 'GET /health', status_class: '2xx' }), 1)
  assert.equal(
    second.httpRequests.get({ route: 'GET /health', status_class: '2xx' }),
    0,
    'module level counters would make one test pass because of another',
  )
})

// ---------------------------------------------------------------------------
// Label cardinality. A metrics endpoint that grows with the requests it serves
// is the largest object in the process by the end of the week.
// ---------------------------------------------------------------------------

test('an undeclared path becomes one bucket rather than one series per path', () => {
  const known = (key: string) => key === 'GET /health'
  assert.equal(routeLabel('get', '/health', known), 'GET /health')
  assert.equal(routeLabel('GET', '/v1/environments/9f3c1a', known), 'GET other')
  assert.equal(routeLabel('GET', '/v1/environments/22b0ff', known), 'GET other')
})

test('statuses collapse to a class', () => {
  assert.equal(statusClass(200), '2xx')
  assert.equal(statusClass(204), '2xx')
  assert.equal(statusClass(302), '3xx')
  assert.equal(statusClass(404), '4xx')
  assert.equal(statusClass(429), '4xx')
  assert.equal(statusClass(500), '5xx')
  assert.equal(statusClass(503), '5xx')
})

// A guard on the guards: if the extraction found nothing, every drift test
// above would pass while checking nothing at all.
test('the metric name extraction actually finds names', () => {
  const found = metricNamesIn('rate(af_http_requests_total[5m]) af_environment_ready_seconds_bucket')
  assert.deepEqual([...found].sort(), ['af_environment_ready_seconds', 'af_http_requests_total'])
})
