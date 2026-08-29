// The route extension point, and the three ways adding one could go wrong.
//
// The test worth writing here is not "a registered route is served". It is
// that a registered route is served *bounded*: the server refuses any path
// limitFor cannot answer for, so a route mounted without its limit reaching
// the limiter is an endpoint that returns 500 forever. That is the exact shape
// of failure this repository has shipped six times, where every piece exists,
// the compiler is happy, and the feature could never have worked. So the first
// case below asserts a body, not a status, and the second asserts that the
// declared numbers are the numbers actually enforced.
//
// No database. Nothing here touches one.

import { afterEach, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from '../src/server.ts'
import { limitFor, type EndpointLimit } from '../src/limits.ts'
import {
  clearExtensions,
  registerExtension,
  registeredExtensions,
  ExtensionRefused,
} from '../src/extensions.ts'
import { FakeClock } from '../src/clock.ts'
import type { Pool } from '@antifailure/db'
import type { GitHubClient } from '../src/auth/github.ts'

const limit: EndpointLimit = {
  rate: 1,
  burst: 3,
  key: 'ip',
  reason: 'A test route. Three at once, so the fourth proves the limiter reached it.',
}

/** The server needs a pool and a GitHub client to be constructed. Neither is
 *  reached by any route this file exercises, and a stub that throws on use is
 *  better than a real one: if a case here ever does touch the database, it
 *  fails loudly instead of quietly depending on one. */
function stubs() {
  const die = () => {
    throw new Error('an extension route test reached the database')
  }
  return {
    pool: { withTenant: die, withoutTenant: die, sql: die, close: async () => {} } as unknown as Pool,
    github: {} as GitHubClient,
  }
}

function serve(clock = new FakeClock()) {
  const { pool, github } = stubs()
  const { app } = createServer({ pool, github, clock, secureCookies: false })
  return app
}

describe('the route extension point', () => {
  afterEach(() => clearExtensions())

  it('serves a registered route, bounded by the limit it declared', async () => {
    registerExtension({
      name: 'test-sso',
      routes: [
        {
          method: 'GET',
          path: '/ext/hello/:name',
          limit,
          handler: (c) => c.json({ hello: c.req.param('name') }),
        },
      ],
    })

    const app = serve()
    const res = await app.request('http://test/ext/hello/ada')

    // The body, not the status. A 500 from the "this endpoint has no declared
    // rate limit" branch is still a 500, and asserting only on 200 would let
    // the interesting failure through as a different-looking pass.
    assert.equal(res.status, 200)
    assert.deepEqual(await res.json(), { hello: 'ada' })
  })

  it('the limiter finds the route without it being in the catalog', () => {
    registerExtension({
      name: 'test-sso',
      routes: [{ method: 'POST', path: '/ext/acs/:handle', limit, handler: (c) => c.json({}) }],
    })
    // Straight at limitFor, with a concrete path rather than the pattern,
    // because that is what a request carries.
    const found = limitFor('POST', '/ext/acs/abc123')
    assert.equal(found?.reason, limit.reason)
    assert.equal(limitFor('POST', '/ext/acs'), undefined, 'a pattern segment matched nothing')
    // Not "undefined": a GET the API does not claim falls to the console's
    // static class, which is what serves its pages. What matters here is that
    // it is not the extension's limit, because the extension declared POST.
    assert.notEqual(
      limitFor('GET', '/ext/acs/abc123')?.reason,
      limit.reason,
      'the method was ignored',
    )
  })

  it('enforces the declared numbers rather than a default', async () => {
    registerExtension({
      name: 'test-sso',
      routes: [{ method: 'GET', path: '/ext/burst', limit, handler: (c) => c.json({ ok: true }) }],
    })
    const app = serve()

    const codes: number[] = []
    for (let i = 0; i < 5; i += 1) {
      const res = await app.request('http://test/ext/burst', {
        headers: { 'x-forwarded-for': '198.51.100.7' },
      })
      codes.push(res.status)
    }
    // A burst of three, then refusals. If the limiter were not reached, or
    // were reached with somebody else's numbers, this would be five 200s.
    assert.deepEqual(codes, [200, 200, 200, 429, 429])
  })

  it('refuses a path the server owns, so an extension cannot shadow sign-in', () => {
    for (const path of ['/auth/session', '/v1/events', '/trpc/anything', '/health']) {
      assert.throws(
        () =>
          registerExtension({
            name: `shadow-${path}`,
            routes: [{ method: 'GET', path, limit, handler: (c) => c.json({}) }],
          }),
        ExtensionRefused,
        `${path} was accepted, which would let an extension receive session cookies`,
      )
    }
    assert.equal(registeredExtensions().length, 0)
  })

  it('refuses a wildcard, a missing limit, and a limit with no reason', () => {
    const cases: { why: string; route: Record<string, unknown> }[] = [
      { why: 'wildcard', route: { method: 'GET', path: '/ext/*', limit } },
      { why: 'no limit', route: { method: 'GET', path: '/ext/a', limit: undefined } },
      {
        why: 'no reason',
        route: { method: 'GET', path: '/ext/b', limit: { ...limit, reason: '' } },
      },
      {
        why: 'zero rate',
        route: { method: 'GET', path: '/ext/c', limit: { ...limit, rate: 0 } },
      },
      { why: 'relative path', route: { method: 'GET', path: 'ext/d', limit } },
    ]
    for (const { why, route } of cases) {
      assert.throws(
        () =>
          registerExtension({
            name: `bad-${why}`,
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            routes: [{ ...route, handler: (c: never) => c } as never],
          }),
        ExtensionRefused,
        `a route with ${why} was accepted`,
      )
    }
  })

  it('refuses a second extension with the same name, or the same path', () => {
    const routes = [{ method: 'GET' as const, path: '/ext/one', limit, handler: (c: never) => c }]
    registerExtension({ name: 'first', routes: routes as never })
    assert.throws(() => registerExtension({ name: 'first', routes: [] }), ExtensionRefused)
    assert.throws(
      () => registerExtension({ name: 'second', routes: routes as never }),
      ExtensionRefused,
    )
    assert.equal(registeredExtensions().length, 1)
  })

  it('leaves nothing half-registered when one route in a batch is refused', () => {
    assert.throws(
      () =>
        registerExtension({
          name: 'partial',
          routes: [
            { method: 'GET', path: '/ext/fine', limit, handler: (c) => c.json({}) },
            { method: 'GET', path: '/auth/steal', limit, handler: (c) => c.json({}) },
          ],
        }),
      ExtensionRefused,
    )
    assert.equal(registeredExtensions().length, 0)
    assert.notEqual(
      limitFor('GET', '/ext/fine')?.reason,
      limit.reason,
      'the acceptable half of a refused extension was registered anyway',
    )
  })
})
