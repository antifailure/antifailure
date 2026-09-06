// The PostHog proxy, held to the two things that make it safe to run.
//
// It forwards, which is the easy half and the half a smoke test would cover.
// And it forwards to ONE place, which is the half that matters and the half
// nothing would notice going wrong: a forwarder that quietly follows a redirect
// off our infrastructure, or that treats a caller's string as part of the
// destination, looks exactly like a working proxy for as long as nobody points
// it at itself.
//
// So most of what is below is refusals, and every refusal is asserted twice:
// the status the caller gets, AND that no request left this process. The second
// half is the one worth having. A test that only reads the status passes on a
// proxy that fetches somebody else's server first and refuses afterwards, which
// is the whole of the damage already done.
//
// No database and no network. createServer builds the route table from a pool
// it never reaches on these paths, and every upstream is a fake handed in
// through fetchImpl, which is the seam providers/proxy.ts already uses. The
// live forward was proved separately against the real hosts; a suite that
// depends on a vendor being up is a suite that goes red for reasons that are
// not the code.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import type { Pool } from '@antifailure/db'
import { createServer } from '../src/server.ts'
import type { GitHubClient } from '../src/auth/github.ts'
import { ENDPOINT_LIMITS } from '../src/limits.ts'
import {
  POSTHOG_MOUNT,
  POSTHOG_PROXIED,
  POSTHOG_REGIONS,
  PostHogProxyRefused,
  forwardToPostHog,
  postHogRegionFrom,
  postHogSummary,
  proxiedPathFor,
} from '../src/analytics/posthog.ts'

const SITE = 'https://antifailure.dev'
const US = POSTHOG_REGIONS.us

/** Every call the proxy made, and what it made it with. The point of recording
 *  the whole call rather than a count is that "it refused" and "it refused
 *  after asking somebody" are different answers and a count cannot tell them
 *  apart. */
interface Sent {
  url: string
  method: string
  headers: Record<string, string>
  body: string
  /** What the client was told to do about a redirect. Recorded because a stub
   *  cannot follow one, so without this the test would pass on a proxy that had
   *  left the default in place and WOULD follow one in production. */
  redirect: string | undefined
}

function fakeUpstream(reply?: (url: URL) => Response) {
  const sent: Sent[] = []
  const fetchImpl = (async (input: unknown, init?: RequestInit) => {
    const url = new URL(String(input))
    const headers: Record<string, string> = {}
    for (const [k, v] of Object.entries((init?.headers ?? {}) as Record<string, string>)) {
      headers[k.toLowerCase()] = v
    }
    const raw = init?.body
    sent.push({
      url: url.toString(),
      method: init?.method ?? 'GET',
      headers,
      body: raw instanceof ArrayBuffer ? new TextDecoder().decode(raw) : String(raw ?? ''),
      redirect: init?.redirect,
    })
    return (
      reply?.(url) ??
      new Response('{"status":"Ok"}', { status: 200, headers: { 'content-type': 'application/json' } })
    )
  }) as unknown as typeof fetch
  return { sent, fetchImpl }
}

function serverWith(fetchImpl?: typeof fetch) {
  return createServer({
    pool: {} as unknown as Pool,
    github: {} as unknown as GitHubClient,
    siteOrigins: [SITE],
    postHog: { bases: US, ...(fetchImpl ? { fetchImpl } : {}) },
  })
}

/** One request at the mount, from the site, the way a browser makes it. */
function call(
  app: { fetch: (r: Request) => Response | Promise<Response> },
  method: string,
  path: string,
  init: { body?: string; headers?: Record<string, string> } = {},
) {
  const headers: Record<string, string> = { origin: SITE, ...(init.headers ?? {}) }
  if (method === 'POST' && !headers['content-type']) headers['content-type'] = 'application/json'
  return app.fetch(
    new Request(`https://app.antifailure.dev${path}`, {
      method,
      headers,
      ...(init.body === undefined ? {} : { body: init.body }),
    }),
  )
}

describe('the PostHog proxy forwards', () => {
  it('sends an allowlisted event to the ingestion host, body and content type intact', async () => {
    const { sent, fetchImpl } = fakeUpstream()
    const { app } = serverWith(fetchImpl)
    const body = '{"api_key":"phc_public","event":"$pageview"}'
    const res = await call(app, 'POST', `${POSTHOG_MOUNT}/i/v0/e/?ip=0`, { body })

    assert.equal(res.status, 200)
    assert.equal(sent.length, 1, 'the request did not reach the upstream at all')
    assert.equal(sent[0]!.url, `${US.ingestion}/i/v0/e/?ip=0`)
    assert.equal(sent[0]!.method, 'POST')
    assert.equal(sent[0]!.body, body, 'the body was altered on the way through')
    assert.equal(sent[0]!.headers['content-type'], 'application/json')
    assert.equal(await res.text(), '{"status":"Ok"}')
  })

  it('sends a bundle to the ASSETS host and not to the ingestion one', async () => {
    // The two upstreams wired the right way round. Getting this backwards is
    // invisible in review, answers 200 from a host that exists, and serves
    // nothing: posthog-js would load its recorder from an ingestion endpoint.
    const { sent, fetchImpl } = fakeUpstream()
    const { app } = serverWith(fetchImpl)
    await call(app, 'GET', `${POSTHOG_MOUNT}/static/recorder.js?v=1.427.2`)
    assert.equal(sent.length, 1)
    assert.equal(sent[0]!.url, `${US.assets}/static/recorder.js?v=1.427.2`)
    assert.notEqual(new URL(sent[0]!.url).host, new URL(US.ingestion).host)
  })

  it('answers the preflight the browser sends before an event, for the site and nobody else', async () => {
    const { app } = serverWith(fakeUpstream().fetchImpl)
    const allowed = await call(app, 'OPTIONS', `${POSTHOG_MOUNT}/i/v0/e/`)
    assert.equal(allowed.status, 204)
    assert.equal(allowed.headers.get('access-control-allow-origin'), SITE)
    assert.equal(allowed.headers.get('vary'), 'origin')

    const stranger = await call(app, 'OPTIONS', `${POSTHOG_MOUNT}/i/v0/e/`, {
      headers: { origin: 'https://evil.example' },
    })
    assert.equal(stranger.status, 403)
    assert.equal(stranger.headers.get('access-control-allow-origin'), null)
  })

  it('never echoes a wildcard, whatever origin asks', async () => {
    const { app } = serverWith(fakeUpstream().fetchImpl)
    for (const origin of [SITE, 'https://evil.example', 'null']) {
      const res = await call(app, 'POST', `${POSTHOG_MOUNT}/e/`, {
        body: '{}',
        headers: { origin },
      })
      assert.notEqual(res.headers.get('access-control-allow-origin'), '*')
    }
  })
})

describe('the PostHog proxy carries nothing of ours upstream', () => {
  it('forwards no cookie, no authorization and not the reader\'s address', async () => {
    // The failure this is against: a proxy that copies the incoming headers
    // through. The browser sends this control plane's OWN session cookie with
    // any request to this origin, because a cookie goes to its domain whether
    // the page meant it to or not. Copying the headers hands that session to
    // an analytics vendor.
    const { sent, fetchImpl } = fakeUpstream()
    const { app } = serverWith(fetchImpl)
    await call(app, 'POST', `${POSTHOG_MOUNT}/e/`, {
      body: '{}',
      headers: {
        cookie: 'af_session=this-must-never-leave',
        authorization: 'Bearer aft_this-must-never-leave',
        'x-forwarded-for': '203.0.113.9',
      },
    })
    assert.equal(sent.length, 1)
    const names = Object.keys(sent[0]!.headers).sort()
    assert.deepEqual(names, ['content-type'], `the proxy sent ${names.join(', ')} upstream`)
    const asText = JSON.stringify(sent[0]!.headers)
    assert.doesNotMatch(asText, /this-must-never-leave/)
    assert.doesNotMatch(asText, /203\.0\.113\.9/)
  })

  it('does not set a cookie of the upstream\'s on our own domain', async () => {
    // PostHog answers some calls with a cookie. Passed back, it would be set on
    // app.antifailure.dev and would then ride along on every authenticated
    // request this control plane serves.
    const { fetchImpl } = fakeUpstream(
      () =>
        new Response('{}', {
          status: 200,
          headers: { 'content-type': 'application/json', 'set-cookie': 'ph_id=1; Path=/' },
        }),
    )
    const { app } = serverWith(fetchImpl)
    const res = await call(app, 'POST', `${POSTHOG_MOUNT}/e/`, { body: '{}' })
    assert.equal(res.status, 200)
    assert.equal(res.headers.get('set-cookie'), null)
  })
})

describe('the PostHog proxy cannot be steered somewhere else', () => {
  /** Every one of these asserts the refusal AND that nothing left the process.
   *  A proxy that fetches first and refuses afterwards has already done the
   *  damage, and the status alone cannot tell the two apart. */
  const steering: [string, string, string][] = [
    [
      'a PostHog path that is not on the allowlist',
      'GET',
      `${POSTHOG_MOUNT}/api/surveys/?token=phc_public`,
    ],
    ['another vendor area of the same host', 'POST', `${POSTHOG_MOUNT}/api/projects/1/query/`],
    ['a protocol relative host', 'POST', `${POSTHOG_MOUNT}//evil.example/e/`],
    ['an empty segment where a name belongs', 'GET', `${POSTHOG_MOUNT}/static//`],
    ['a method the path does not serve', 'GET', `${POSTHOG_MOUNT}/e/`],
    ['the mount itself', 'GET', POSTHOG_MOUNT],
  ]

  for (const [what, method, path] of steering) {
    it(`refuses ${what}, without asking anybody`, async () => {
      const { sent, fetchImpl } = fakeUpstream()
      const { app } = serverWith(fetchImpl)
      const res = await call(app, method, path, method === 'POST' ? { body: '{}' } : {})
      assert.equal(res.status, 404, `${method} ${path} was answered ${res.status}`)
      assert.deepEqual(
        sent.map((s) => s.url),
        [],
        `${method} ${path} was refused only after a request had already left for ${sent.map((s) => s.url).join(', ')}`,
      )
    })
  }

  it('cannot be walked out of the mount with a traversal, because the URL collapses first', async () => {
    // WORTH A TEST OF ITS OWN RATHER THAN A LINE IN THE LOOP ABOVE, because the
    // answer is not the 404 the others give and the reason is instructive.
    // `/ph/static/../../evil` never reaches this mount at all: URL parsing
    // resolves the dot segments before anything routes, so the server is asked
    // for `/evil`. That is a stronger property than a refusal, and it is also
    // one this code does not own, so it is asserted rather than assumed.
    assert.equal(new URL(`${POSTHOG_MOUNT}/static/../../evil`, 'https://x.test').pathname, '/evil')

    const { sent, fetchImpl } = fakeUpstream()
    const { app } = serverWith(fetchImpl)
    const res = await call(app, 'GET', `${POSTHOG_MOUNT}/static/../../evil`)
    assert.notEqual(res.status, 200, 'a traversal was served something')
    assert.deepEqual(sent.map((x) => x.url), [], 'a traversal reached an upstream')
  })

  it('answers an unlisted path under the mount as an API 404 and not with the console', async () => {
    // The trap this is against is one line in limits.ts. consoleClass is the
    // last resort in limitFor and answers every unmatched GET with the
    // console's own limit, which means the console's HTML. Without the mount
    // in API_PREFIXES, a GET to a PostHog path this deliberately refuses would
    // be answered 200 with an application page.
    const { app } = serverWith(fakeUpstream().fetchImpl)
    const res = await call(app, 'GET', `${POSTHOG_MOUNT}/api/surveys/`)
    assert.equal(res.status, 404)
    assert.match(res.headers.get('content-type') ?? '', /json/)
    assert.doesNotMatch(await res.text(), /<html/i)
  })

  it('keeps an encoded traversal inside the upstream, path and origin both', async () => {
    // %2F is not a separator, so this IS one segment and the allowlist matches
    // it. That is correct and it is why the origin has to be guaranteed by
    // construction rather than by the shape of the path.
    const { sent, fetchImpl } = fakeUpstream()
    const { app } = serverWith(fetchImpl)
    await call(app, 'GET', `${POSTHOG_MOUNT}/static/..%2F..%2Fevil.js`)
    assert.equal(sent.length, 1)
    assert.equal(new URL(sent[0]!.url).origin, US.assets)
    assert.ok(
      new URL(sent[0]!.url).pathname.startsWith('/static/'),
      `the request left for ${sent[0]!.url}`,
    )
  })

  it('cannot be pointed at another host by a header', async () => {
    const { sent, fetchImpl } = fakeUpstream()
    const { app } = serverWith(fetchImpl)
    await call(app, 'POST', `${POSTHOG_MOUNT}/e/`, {
      body: '{}',
      headers: {
        'x-forwarded-host': 'evil.example',
        'x-posthog-host': 'evil.example',
      },
    })
    assert.equal(sent.length, 1)
    assert.equal(new URL(sent[0]!.url).origin, US.ingestion)
  })

  it('refuses a redirect from the upstream rather than following it', async () => {
    // fetch follows redirects by default, so without redirect: 'manual' an
    // upstream answering 302 would make this process issue a SECOND request,
    // with the body it was given, to a host that is nowhere in the region set.
    // That is the open forwarder this is supposed not to be, reached without
    // touching anything on our side.
    const { sent, fetchImpl } = fakeUpstream(
      () =>
        new Response(null, {
          status: 302,
          headers: { location: 'https://evil.example/collect' },
        }),
    )
    const { app } = serverWith(fetchImpl)
    const res = await call(app, 'POST', `${POSTHOG_MOUNT}/e/`, { body: '{"secret":"body"}' })

    assert.equal(res.status, 502)
    assert.equal(res.headers.get('location'), null, 'the browser was handed the redirect instead')
    // The half a stub cannot demonstrate. This fake returns the 302 rather than
    // chasing it, so the refusal below would pass even with fetch left on its
    // default. Asserting the option is what covers production, where a real
    // client WOULD chase it before this code ever saw a status.
    assert.equal(sent[0]!.redirect, 'manual', 'fetch was left free to follow the redirect itself')
    // Exactly one request, to the configured host. A second one, to anywhere,
    // is the redirect having been followed, and the body going with it is the
    // damage. Asserted on the requests that did NOT go to the upstream, because
    // the one that did legitimately carries the body it was given.
    const elsewhere = sent.filter((x) => new URL(x.url).origin !== US.ingestion)
    assert.deepEqual(elsewhere.map((x) => x.url), [], 'the redirect was followed')
    assert.equal(sent.length, 1, `${sent.length} requests left for one call`)
    assert.doesNotMatch(
      elsewhere.map((x) => x.body).join(''),
      /secret/,
      'the body was sent on to the redirect target',
    )
  })

  it('builds the upstream URL the one way that cannot be escaped', () => {
    // THE NEGATIVE CONTROL, and the reason the construction above is not
    // arbitrary. `new URL(path, base)` is what everybody writes first, and a
    // value beginning `//` makes it discard the base entirely and silently.
    assert.equal(
      new URL('//evil.example/x', US.ingestion).origin,
      'https://evil.example',
      'the resolving form stopped being dangerous, so the reason for the other one is gone',
    )
    const assigned = new URL(US.ingestion)
    assigned.pathname = '//evil.example/x'
    assert.equal(assigned.origin, US.ingestion, 'assigning a pathname changed the origin')
  })

  it('says no from the deciding function, not only from a route that calls it', async () => {
    // THE REASON THIS EXISTS, found by mutation rather than by reasoning.
    // Loosening the allowlist so that it matched EVERY path left every route
    // test above green, because Hono refuses an unregistered path before any
    // handler runs. So those tests prove the ROUTER says no; they say nothing
    // about the allowlist, and the allowlist is the thing that decides what a
    // registered handler is allowed to forward. Two mechanisms, neither
    // subsuming the other, so both are driven.
    for (const [method, path] of [
      ['GET', '/api/surveys/'],
      ['GET', '/api/web_experiments/'],
      ['POST', '/api/projects/1/query/'],
      ['POST', '//evil.example/e/'],
      ['GET', '/static//'],
      ['GET', '/static/'],
      ['GET', '/array//config.js'],
      ['GET', '/'],
      ['GET', ''],
      ['DELETE', '/e/'],
      ['GET', '/e/'],
      ['POST', '/static/recorder.js'],
    ] as const) {
      assert.equal(
        proxiedPathFor(method, path),
        null,
        `the allowlist admits ${method} ${path}`,
      )
    }
    assert.notEqual(proxiedPathFor('POST', '/i/v0/e/'), null)
    assert.notEqual(proxiedPathFor('GET', '/static/recorder.js'), null)
    assert.notEqual(proxiedPathFor('GET', '/static/1.427.2/recorder.js'), null)
    assert.notEqual(proxiedPathFor('GET', '/array/phc_public/config.js'), null)
    await assert.rejects(
      () => forwardToPostHog({ bases: US }, { method: 'GET', subPath: '/nope', search: '' }),
      PostHogProxyRefused,
      'the refusal is async, so a synchronous assertion here would pass on a proxy that forwarded',
    )
  })
})

describe('the PostHog proxy is off unless it is configured', () => {
  it('mounts nothing at all when no region is set', async () => {
    const { app } = createServer({
      pool: {} as unknown as Pool,
      github: {} as unknown as GitHubClient,
      siteOrigins: [SITE],
    })
    for (const entry of POSTHOG_PROXIED) {
      const path = POSTHOG_MOUNT + entry.route.replace(/:[^/]+/g, 'x')
      const res = await call(app, entry.method, path, entry.method === 'POST' ? { body: '{}' } : {})
      assert.equal(res.status, 404, `${entry.method} ${path} is served on a deployment with no region`)
    }
  })

  it('takes the two regions, refuses anything else, and says which it is', () => {
    assert.equal(postHogRegionFrom('us'), 'us')
    assert.equal(postHogRegionFrom(' EU '), 'eu')
    assert.equal(postHogRegionFrom(undefined), null)
    assert.equal(postHogRegionFrom('  '), null)
    // Not a host, not a URL, not a project. There is no value of this variable
    // that names somewhere to forward to.
    for (const bad of ['https://us.i.posthog.com', 'usa', 'eu-central', 'evil.example']) {
      assert.throws(() => postHogRegionFrom(bad), /AF_POSTHOG_REGION/, bad)
    }
    assert.match(postHogSummary(null), /not mounted/)
    assert.match(postHogSummary('us'), /us\.i\.posthog\.com/)
  })

  it('bounds every path it forwards, on the only key a reader has', () => {
    // The rate limit gate refuses a served route with no declared limit, so a
    // missing entry is a 500 rather than an unbounded endpoint. This says the
    // entries are the right SHAPE: keyed on the address, because a reader of
    // the marketing site has no token and no organization.
    for (const entry of POSTHOG_PROXIED) {
      for (const method of entry.method === 'POST' ? ['POST', 'OPTIONS'] : ['GET']) {
        const limit = ENDPOINT_LIMITS[`${method} ${POSTHOG_MOUNT}${entry.route}`]
        assert.ok(limit, `${method} ${POSTHOG_MOUNT}${entry.route} has no declared limit`)
        assert.equal(limit.key, 'ip', `${method} ${entry.route} is keyed on something a reader lacks`)
      }
    }
  })

  it('keeps both capture endpoints, because the remote config chooses between them', () => {
    // The one that would have been missed. posthog-js posts to /e/ by default
    // and overrides it from analytics.endpoint in the remote config; this
    // project's config returns /i/v0/e/. Allowlisting only the documented
    // default forwards the flag call, serves the script, looks healthy, and
    // 404s every event.
    assert.notEqual(proxiedPathFor('POST', '/e/'), null)
    assert.notEqual(proxiedPathFor('POST', '/i/v0/e/'), null)
  })
})
