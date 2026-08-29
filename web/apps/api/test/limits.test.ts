// Every public endpoint has a declared limit, and the list is checked against
// the server's own route table rather than against itself.
//
// Phase 14.7's exit criterion. A test that reads the same list the middleware
// reads proves only that the list equals itself. This asks Hono what it is
// serving and asks the router what procedures exist, and fails on anything the
// list does not name.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import {
  ENDPOINT_LIMITS, checkQuota, limitFor, bucketFor, PLAN_QUOTAS, DEFAULT_PLAN,
} from '../src/limits.ts'
import { listProcedures } from '../src/openapi.ts'
import { RateLimiter } from '../src/ratelimit.ts'
import { FakeClock } from '../src/clock.ts'
import {
  available, startApi, seedOrg, dropOrg, signInAs, type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

describe('the limit catalog', () => {
  it('names a rate, a burst, a key, and a reason for every endpoint', () => {
    for (const [endpoint, limit] of Object.entries(ENDPOINT_LIMITS)) {
      assert.ok(limit.rate > 0, `${endpoint} has a rate of ${limit.rate}`)
      assert.ok(limit.burst >= limit.rate, `${endpoint} bursts smaller than it sustains`)
      assert.ok(
        ['ip', 'token', 'org'].includes(limit.key),
        `${endpoint} is keyed on ${limit.key}`,
      )
      // The reason is what somebody reads before raising the number. Without it
      // every limit eventually becomes whatever the loudest complaint asked for.
      assert.ok(
        limit.reason.length > 30,
        `${endpoint} has no reason for its number, so nobody can judge a request to raise it`,
      )
    }
  })

  it('keys sign-in on the address, because that is all an attacker has', () => {
    for (const endpoint of ['GET /auth/github', 'GET /auth/github/callback']) {
      assert.equal(
        ENDPOINT_LIMITS[endpoint]!.key,
        'ip',
        `${endpoint} is keyed on something an unauthenticated caller does not have`,
      )
    }
  })

  it('keys the ingestion path on the token, so one engine cannot fill the queue', () => {
    assert.equal(ENDPOINT_LIMITS['POST /v1/events']!.key, 'token')
  })

  it('resolves a tRPC path through the wildcard and nothing else through it', () => {
    assert.ok(limitFor('GET', '/trpc/environments.list'))
    assert.ok(limitFor('POST', '/trpc/environments.teardown'))
    assert.equal(limitFor('GET', '/something-new'), undefined)
  })

  it('resolves a parameterised path to its pattern', () => {
    // The catalog holds patterns and a request carries a concrete path.
    // Comparing them as strings finds nothing, which is how a declared endpoint
    // ends up answering 500.
    assert.ok(limitFor('GET', '/v1/environments/af-branch-1'))
    // One segment, not several. A parameter must not swallow a deeper path
    // that nobody has declared a limit for.
    assert.equal(limitFor('GET', '/v1/environments/af-1/artifacts'), undefined)
    assert.equal(limitFor('POST', '/v1/environments/af-1'), undefined)
  })

  it('buckets fall back to the address when the key is not available', () => {
    const limit = ENDPOINT_LIMITS['POST /v1/events']!
    // An unauthenticated caller on a token-keyed endpoint must not share one
    // global bucket with every other unauthenticated caller.
    const a = bucketFor(limit, { ip: '10.0.0.1' })
    const b = bucketFor(limit, { ip: '10.0.0.2' })
    assert.notEqual(a, b)
    assert.notEqual(bucketFor(limit, { ip: '10.0.0.1', token: 'aft_x' }), a)
  })
})

describe('every endpoint the server serves is in the catalog', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  before(async () => {
    h = await startApi()
  })
  after(async () => {
    await h.close()
  })

  it('has a declared limit for every plain HTTP route', async () => {
    // Hono's own route table, so a route added and forgotten shows up here
    // rather than being served unbounded.
    const routes = (h.app.routes as { method: string; path: string }[])
      .filter((r) => r.method !== 'ALL' && !r.path.endsWith('/*'))
      .map((r) => `${r.method} ${r.path}`)

    // Through limitFor rather than against ENDPOINT_LIMITS directly, because
    // the catalog is no longer the only place a limit can be declared: a route
    // another edition registers carries its own. The property is unchanged and
    // the coverage is wider, which is the point: an extension route mounted
    // without a limit would be served 500 forever, and this is where that
    // shows up.
    const undeclared = [...new Set(routes)].filter((r) => {
      const space = r.indexOf(' ')
      return !limitFor(r.slice(0, space), r.slice(space + 1))
    })
    assert.deepEqual(
      undeclared,
      [],
      `these endpoints have no declared rate limit:\n  ${undeclared.join('\n  ')}\n` +
        'Add each to ENDPOINT_LIMITS with the reason for the number.',
    )
  })

  it('has a declared limit for every tRPC procedure', () => {
    const missing = listProcedures().filter(
      ({ path, type }) => !limitFor(type === 'query' ? 'GET' : 'POST', `/trpc/${path}`),
    )
    assert.deepEqual(missing.map((m) => m.path), [])
  })

  it('refuses an endpoint with no declared limit rather than serving it unbounded', async () => {
    // The safe direction. An endpoint nobody remembered to limit is the one
    // that has never been load tested, so answering with an error is a bug
    // report and leaving it open is an outage.
    const res = await h.fetch('/some-endpoint-nobody-declared')
    assert.equal(res.status, 500)
    const body = (await res.json()) as { error: string }
    assert.match(body.error, /no declared rate limit/)
  })

  it('answers a burst with 429 and a Retry-After, then lets it through after the wait', async () => {
    let refused: Response | null = null
    for (let i = 0; i < 200 && !refused; i += 1) {
      const res = await h.fetch('/auth/session')
      if (res.status === 429) refused = res
    }
    assert.ok(refused, 'the declared limit on /auth/session never refused anything')
    const retryAfter = Number(refused.headers.get('retry-after'))
    assert.ok(retryAfter >= 1, `Retry-After was ${retryAfter}, which means retry at once`)

    h.clock.advance(retryAfter * 1000 + 1000)
    const after = await h.fetch('/auth/session')
    assert.notEqual(after.status, 429, 'waiting the stated time did not help')
  })
})

describe('the token bucket', () => {
  it('refills at the stated rate and no faster', () => {
    const clock = new FakeClock()
    const limiter = new RateLimiter(clock, { rate: 2, burst: 4 })

    for (let i = 0; i < 4; i += 1) {
      assert.equal(limiter.take('k').allowed, true, `token ${i} should be in the burst`)
    }
    assert.equal(limiter.take('k').allowed, false, 'the burst was larger than declared')

    clock.advance(500) // one token at two per second
    assert.equal(limiter.take('k').allowed, true)
    assert.equal(limiter.take('k').allowed, false)
  })

  it('never accumulates more than the burst however long it idles', () => {
    const clock = new FakeClock()
    const limiter = new RateLimiter(clock, { rate: 10, burst: 5 })
    clock.advance(60 * 60 * 1000)

    for (let i = 0; i < 5; i += 1) {
      assert.equal(limiter.take('k').allowed, true)
    }
    assert.equal(
      limiter.take('k').allowed,
      false,
      'an hour of idling banked more than the burst, so a limit is a queue',
    )
  })

  it('keeps one caller’s bucket separate from another’s', () => {
    const clock = new FakeClock()
    const limiter = new RateLimiter(clock, { rate: 1, burst: 1 })
    assert.equal(limiter.take('a').allowed, true)
    assert.equal(limiter.take('a').allowed, false)
    assert.equal(limiter.take('b').allowed, true, 'one caller exhausted another caller’s budget')
  })

  it('drops idle buckets so a limiter keyed by token does not grow forever', () => {
    const clock = new FakeClock()
    const limiter = new RateLimiter(clock, { rate: 1, burst: 1 }, 60_000)
    for (let i = 0; i < 100; i += 1) limiter.take(`token-${i}`)
    assert.equal(limiter.size, 100)

    clock.advance(61_000)
    assert.equal(limiter.sweep(), 100)
    assert.equal(limiter.size, 0)
  })

  it('a cost larger than the burst is refused rather than waited for forever', () => {
    const clock = new FakeClock()
    const limiter = new RateLimiter(clock, { rate: 1, burst: 5 })
    const verdict = limiter.take('k', 50)
    assert.equal(verdict.allowed, false)
    assert.ok(verdict.retryAfterSeconds > 0)
  })
})

describe('quotas', () => {
  it('refuse the next creation and never remove anything', () => {
    const verdict = checkQuota('free', 'environments', 3)
    assert.equal(verdict.allowed, false)
    assert.equal(verdict.limit, PLAN_QUOTAS.free!.environments)
    // Tearing down running environments because a plan changed is not a
    // behaviour any product should have, and the message says so.
    assert.match(verdict.reason, /Nothing that already exists was removed/)
    assert.match(verdict.reason, /Tear one down, or change the plan/)
  })

  it('allow up to the limit', () => {
    assert.equal(checkQuota('free', 'environments', 2).allowed, true)
    assert.equal(checkQuota('team', 'environments', 24).allowed, true)
    assert.equal(checkQuota('team', 'environments', 25).allowed, false)
  })

  it('fall back to the smallest plan for a plan nobody recognises', () => {
    // A plan name that arrived from a database row somebody edited by hand. The
    // safe direction is the smallest quota, not the largest.
    const unknown = checkQuota('platinum-plus', 'environments', 3)
    assert.equal(unknown.allowed, false)
    assert.equal(unknown.limit, PLAN_QUOTAS[DEFAULT_PLAN]!.environments)
  })

  it('every plan has a quota for every resource', () => {
    for (const [plan, quota] of Object.entries(PLAN_QUOTAS)) {
      for (const resource of ['environments', 'goldens', 'artifactGigabytes'] as const) {
        assert.ok(
          quota[resource] > 0,
          `the ${plan} plan has no ${resource} quota, so it is unbounded`,
        )
      }
    }
  })

  it('a larger plan is never smaller in any dimension', () => {
    // A plan that costs more and permits less in some dimension is a plan
    // somebody will hit in production and nobody will believe.
    const order = ['free', 'team', 'enterprise']
    for (let i = 1; i < order.length; i += 1) {
      const smaller = PLAN_QUOTAS[order[i - 1]!]!
      const larger = PLAN_QUOTAS[order[i]!]!
      for (const resource of ['environments', 'goldens', 'artifactGigabytes'] as const) {
        assert.ok(
          larger[resource] >= smaller[resource],
          `${order[i]} permits fewer ${resource} than ${order[i - 1]}`,
        )
      }
    }
  })
})

describe('the kill switch', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'killswitch')
    token = `aft_${randomUUID().replace(/-/g, '')}${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_ks')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function sendEvent(): Promise<Response> {
    return h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({
        events: [
          {
            id: randomUUID(),
            type: 'environment.ready',
            envId: org.envId,
            sequence: 1,
            occurredAt: h.clock.now().toISOString(),
          },
        ],
      }),
    })
  }

  it('a suspended organization cannot send events, and is told why', async () => {
    assert.equal((await sendEvent()).status, 202, 'the fixture could not send before suspension')

    const owner = await signInAs(h, org, 'owner', 'incident')
    const suspended = await h.fetch('/trpc/org.suspend', {
      method: 'POST',
      headers: {
        cookie: owner.cookie,
        'x-antifailure-csrf': owner.csrfToken,
        'content-type': 'application/json',
      },
      body: JSON.stringify({ reason: 'runaway environment creation' }),
    })
    assert.equal(suspended.status, 200)

    const refused = await sendEvent()
    // 403 and not 401. The token is fine and the organization is stopped, and
    // somebody debugging at two in the morning needs to know which.
    assert.equal(refused.status, 403)
    const body = (await refused.json()) as { error: string; retryAfterSeconds: number }
    assert.match(body.error, /runaway environment creation/)
    assert.ok(body.retryAfterSeconds > 0, 'a suspended engine was not told when to try again')
  })

  it('reading is still permitted while suspended', async () => {
    // A suspension stops new work. Taking away the ability to see what is
    // already running is the opposite of what an incident needs.
    const res = await h.fetch(`/v1/environments/${org.envId}`, {
      headers: { authorization: `Bearer ${token}` },
    })
    assert.equal(res.status, 200)
  })

  it('nothing running was torn down', async () => {
    const rows = await h.admin<{ state: string }[]>`
      SELECT state::text AS state FROM environments WHERE org_id = ${org.orgId}`
    assert.ok(rows.length > 0)
    for (const row of rows) {
      assert.notEqual(
        row.state,
        'torn_down',
        'suspending an organization destroyed the evidence the incident was about',
      )
    }
  })

  it('suspending and resuming are both audited', async () => {
    const rows = await h.admin<{ action: string; detail: any }[]>`
      SELECT action, detail FROM audit_entries
      WHERE org_id = ${org.orgId} ORDER BY seq ASC`
    const actions = rows.map((r) => r.action)
    assert.ok(actions.includes('organization.suspended'), 'a suspension was not audited')
    const entry = rows.find((r) => r.action === 'organization.suspended')!
    assert.match(String(entry.detail.reason), /runaway/)
  })

  it('resuming lets events through again', async () => {
    const owner = await signInAs(h, org, 'owner', 'resumer')
    const resumed = await h.fetch('/trpc/org.resume', {
      method: 'POST',
      headers: {
        cookie: owner.cookie,
        'x-antifailure-csrf': owner.csrfToken,
        'content-type': 'application/json',
      },
      body: JSON.stringify({}),
    })
    assert.equal(resumed.status, 200)
    assert.equal((await sendEvent()).status, 202)

    const rows = await h.admin<{ action: string }[]>`
      SELECT action FROM audit_entries WHERE org_id = ${org.orgId} AND action = 'organization.resumed'`
    assert.equal(rows.length, 1)
  })

  it('org.status reports the plan and what is left of each quota', async () => {
    const owner = await signInAs(h, org, 'viewer', 'watcher')
    const res = await h.fetch(
      `/trpc/org.status?input=${encodeURIComponent(JSON.stringify({}))}`,
      { headers: { cookie: owner.cookie } },
    )
    assert.equal(res.status, 200)
    const body = (await res.json()) as { result: { data: any } }
    const status = body.result.data
    assert.equal(status.suspended, false)
    assert.equal(status.plan, 'free')
    assert.ok(status.quotas.environments.limit > 0)
    assert.equal(typeof status.quotas.environments.allowed, 'boolean')
  })
})
