// The metrics endpoint, proved wired rather than written.
//
// metrics.ts can be at a hundred percent coverage while nothing in the server
// ever calls it, which is the failure this repository has now hit in three
// separate subsystems: an event bus with no sinks, a control plane sink with no
// constructor call, a journal with no replay. So every assertion here goes
// through a real HTTP request to the real app and reads the real scrape.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomUUID } from 'node:crypto'
import { URL as DR_URL, start as startPostgres } from './pgcontainer.ts'

// Against this suite's own Postgres rather than the shared development
// container, for the reason the drill suite gives at length: a run that takes
// longer than a few seconds is a run during which somebody else recreates the
// shared one, and the failure arrives as ECONNRESET in the middle of an
// assertion about metrics. The environment variable is set before the harness
// module is loaded, which is what the dynamic import below is for; a static
// import would be evaluated first and would read the old value.
const hasDatabase = await startPostgres()
if (hasDatabase) process.env.AF_TEST_DATABASE_URL = DR_URL

const { dropOrg, seedOrg, startApi } = await import('./harness.ts')
type ApiHarness = Awaited<ReturnType<typeof startApi>>
type Org = Awaited<ReturnType<typeof seedOrg>>

describe('metrics', { skip: hasDatabase ? false : 'no Docker daemon to stand a database up in' }, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'metrics')
    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_met')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function scrape(): Promise<string> {
    const res = await h.fetch('/metrics')
    assert.equal(res.status, 200, 'the endpoint is not served')
    assert.match(
      res.headers.get('content-type') ?? '',
      /text\/plain/,
      'Prometheus needs the text exposition content type',
    )
    return res.text()
  }

  it('is served at all, which the declared rate limit decides', async () => {
    // limits.ts refuses to serve any path limitFor does not answer for, with a
    // 500 that says so. An endpoint added without a limit is therefore an
    // endpoint that can never serve, and this is what catches that.
    const body = await scrape()
    assert.match(body, /# TYPE af_control_plane_info gauge/)
    assert.match(body, /af_control_plane_info\{version="[^"]*"\} 1/)
  })

  it('counts a request that actually happened', async () => {
    await h.fetch('/health')
    const body = await scrape()
    assert.match(
      body,
      /af_http_requests_total\{route="GET \/health",status_class="2xx"\} [1-9]/,
      'a request was served and nothing counted it',
    )
    assert.match(body, /af_http_request_seconds_bucket\{le="\+Inf",route="GET \/health"\}/)
  })

  it('collapses an undeclared path into one series rather than one per path', async () => {
    await h.fetch(`/v1/environments/${randomUUID()}`)
    await h.fetch(`/v1/environments/${randomUUID()}`)
    const body = await scrape()
    const series = body
      .split('\n')
      .filter((l) => l.startsWith('af_http_requests_total{') && l.includes('other'))
    assert.equal(
      series.length,
      1,
      'two different paths produced two series, so a metrics endpoint grows with the ' +
        'requests it serves and becomes the largest object in the process',
    )
  })

  it('counts the events an engine sent, by what happened to each', async () => {
    const id = randomUUID()
    const send = (events: unknown[]) =>
      h.fetch('/v1/events', {
        method: 'POST',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        body: JSON.stringify({ events }),
      })

    const event = {
      id,
      type: 'environment.ready',
      envId: org.envId,
      sequence: 41,
      occurredAt: h.clock.now().toISOString(),
      payload: { seconds: 93.5 },
    }
    assert.equal((await send([event])).status, 202)
    // The same event again. A resend after a timeout carries the same
    // identifier, and a duplicate is healthy rather than a fault, so it is
    // counted separately rather than as an acceptance.
    assert.equal((await send([event])).status, 202)

    const body = await scrape()
    assert.match(body, /af_ingest_events_total\{outcome="accepted"\} [1-9]/)
    assert.match(body, /af_ingest_events_total\{outcome="duplicate"\} [1-9]/)
    assert.match(
      body,
      /af_environment_outcomes_total\{outcome="ready"\} [1-9]/,
      'the environment creation success objective is measured from this counter',
    )
    assert.match(
      body,
      /af_environment_transitions_total\{to_state="ready"\} [1-9]/,
    )
  })

  it('measures time to preview from the number the engine reported', async () => {
    await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({
        events: [{
          id: randomUUID(), type: 'environment.ready', envId: org.envId, sequence: 60,
          occurredAt: h.clock.now().toISOString(), payload: { seconds: 93.5 },
        }],
      }),
    })

    const body = await scrape()
    // The objective is a p95 under eight minutes, so there is a bucket boundary
    // at 480 exactly: a quantile estimated across a bucket that straddles the
    // target is an estimate of whether the target was met.
    assert.match(body, /af_environment_ready_seconds_bucket\{le="480.0"\} [1-9]/)
    assert.match(
      body,
      /af_environment_ready_seconds_bucket\{le="60.0"\} 0/,
      'a 93 second environment was counted in the under-a-minute bucket',
    )
  })

  it('counts a failure with the error code rather than the sentence', async () => {
    await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({
        events: [{
          id: randomUUID(), type: 'environment.failed', envId: org.envId, sequence: 70,
          occurredAt: h.clock.now().toISOString(),
          payload: { code: 'AF-DB-001', detail: 'a sentence written for a terminal' },
        }],
      }),
    })

    const body = await scrape()
    assert.match(
      body,
      /af_environment_outcomes_total\{code="AF-DB-001",outcome="failed"\} [1-9]/,
      'a dashboard can group by AF-DB-001; it cannot group by a sentence',
    )
    assert.doesNotMatch(body, /a sentence written for a terminal/,
      'the detail reached a metric label, which is unbounded cardinality and possibly a secret')
  })

  it('counts a refusal, because a metric that only sees successes cannot see a limit set too low', async () => {
    // The sign-in limit is the tightest on the server: one per second with a
    // burst of twenty, keyed by address.
    let refused = false
    for (let i = 0; i < 40 && !refused; i++) {
      const res = await h.fetch('/auth/github')
      if (res.status === 429) refused = true
    }
    assert.ok(refused, 'the rate limit did not engage, so this test proves nothing')

    const body = await scrape()
    assert.match(body, /af_rate_limited_total\{route="GET \/auth\/github"\} [1-9]/)
    assert.match(
      body,
      /af_http_requests_total\{route="GET \/auth\/github",status_class="4xx"\} [1-9]/,
      'a refused request was not counted at all, so an outage and a quiet afternoon look alike',
    )
  })
})
