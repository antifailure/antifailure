// The console's two server-side halves: the build it serves, and the JSON the
// browser needs that is not tRPC.
//
// What this file deliberately does NOT do is assert HTML. The console is a
// Next.js application with its own build and its own tests for what it
// renders; the claims here are the ones this process is responsible for --
// which file a URL maps to, which headers it carries, and who is allowed to
// change a provider key.

import { after, before, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { available, seedOrg, signInAs, startApi, type ApiHarness, type Org } from './harness.ts'

/**
 * A stand-in for the export.
 *
 * Not the real console build: making this suite depend on a Next.js build
 * would mean the API's tests could not run without one, and the claim being
 * tested here is the mapping, not the markup. What proves the real build has
 * these files is the CI step that checks console/out after building it, and
 * the image that refuses to be built without index.html.
 */
async function fakeBuild(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'af-console-'))
  await writeFile(join(dir, 'index.html'), '<!doctype html><title>index</title>')
  await writeFile(join(dir, 'runs.html'), '<!doctype html><title>runs</title>')
  await writeFile(join(dir, 'keys.html'), '<!doctype html><title>keys</title>')
  await writeFile(join(dir, '404.html'), '<!doctype html><title>not found</title>')
  await mkdir(join(dir, '_next', 'static', 'chunks'), { recursive: true })
  await writeFile(join(dir, '_next', 'static', 'chunks', 'main-abc123.js'), 'console.log(1)')
  return dir
}

const ok = await available()

describe('serving the console build', { skip: ok ? false : 'no database' }, () => {
  let h: ApiHarness
  let dir: string

  before(async () => {
    dir = await fakeBuild()
    h = await startApi({ consoleDir: dir })
  })
  after(async () => {
    await h.close()
    await rm(dir, { recursive: true, force: true })
  })

  test('/ is the application, not JSON', async () => {
    const res = await h.fetch('/')
    assert.equal(res.status, 200)
    assert.match(res.headers.get('content-type') ?? '', /text\/html/)
    assert.match(await res.text(), /<title>index<\/title>/)
  })

  test('an extensionless route maps to the file the export wrote for it', async () => {
    const res = await h.fetch('/runs')
    assert.equal(res.status, 200)
    assert.match(await res.text(), /<title>runs<\/title>/)
  })

  test('a static asset is served with its own type and cached forever', async () => {
    const res = await h.fetch('/_next/static/chunks/main-abc123.js')
    assert.equal(res.status, 200)
    assert.match(res.headers.get('content-type') ?? '', /javascript/)
    // The filename carries a content hash, so the URL can never mean anything
    // else. A page cannot say this: it is one URL for every build.
    assert.equal(res.headers.get('cache-control'), 'public, max-age=31536000, immutable')
  })

  test('a page is never cached, because it is rendered for whoever is signed in', async () => {
    assert.equal((await h.fetch('/')).headers.get('cache-control'), 'no-store')
  })

  test('an unknown path is the application’s own 404, not a blank one', async () => {
    const res = await h.fetch('/nothing-here')
    assert.equal(res.status, 404)
    assert.match(await res.text(), /<title>not found<\/title>/)
  })

  test('a path that climbs out of the build is refused', async () => {
    // Encoded as well as plain, because the two take different paths through
    // the router and only one of them is normalised on the way in.
    for (const attack of ['/../package.json', '/%2e%2e/package.json', '/_next/../../package.json']) {
      const res = await h.fetch(attack)
      const body = await res.text()
      assert.ok(!body.includes('"name"'), `${attack} served something from outside the build`)
      assert.equal(res.status, 404)
    }
  })

  test('every page carries the headers that make it safe to serve', async () => {
    const res = await h.fetch('/')
    const csp = res.headers.get('content-security-policy') ?? ''
    // Asserted whole rather than by a fragment. The bug this replaces was a
    // policy that still contained "default-src 'none'" and had lost its
    // style-src, so a test matching that fragment passed while every page in
    // a real browser rendered as unstyled text.
    assert.equal(
      csp,
      "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
        "img-src 'self' data: https://avatars.githubusercontent.com; font-src 'self'; " +
        "connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
    )
    assert.equal(res.headers.get('x-frame-options'), 'DENY')
    assert.equal(res.headers.get('x-content-type-options'), 'nosniff')
    assert.equal(res.headers.get('referrer-policy'), 'strict-origin-when-cross-origin')
  })

  test('the policy allows the stylesheet and the script the export actually ships', async () => {
    // The two directives whose absence is invisible in a status code and fatal
    // in a browser, named individually so a future edit that drops one fails
    // here rather than in somebody's tab.
    const csp = (await h.fetch('/')).headers.get('content-security-policy') ?? ''
    assert.match(csp, /style-src 'self'/)
    assert.match(csp, /script-src 'self' 'unsafe-inline'/)
    assert.match(csp, /connect-src 'self'/)
  })

  test('a second request with the same etag gets a 304 and no body', async () => {
    const first = await h.fetch('/')
    const etag = first.headers.get('etag')
    assert.ok(etag)
    const second = await h.fetch('/', { headers: { 'if-none-match': etag } })
    assert.equal(second.status, 304)
    assert.equal(await second.text(), '')
  })

  test('an API route still wins over the application', async () => {
    // The console is Hono's not-found handler rather than a wildcard route
    // precisely so that this cannot regress: every declared route, wherever it
    // is declared, is matched first.
    const res = await h.fetch('/health')
    assert.equal(res.status, 200)
    assert.match(res.headers.get('content-type') ?? '', /json/)
  })

  test('a POST to a path with no route never reaches the console at all', async () => {
    // A static file server answers GET and HEAD, so the console never claims a
    // mutating method: it is classified only for GET, which is what keeps a
    // new mutating endpoint impossible to add without a limit.
    //
    // What it answers is 404, and it used to be 500. There is no route here, so
    // nothing is wrong with the server; saying 500 told a load balancer
    // otherwise. The refusal that makes the classification worth having is
    // asserted in limits.test.ts against a route that actually exists, which is
    // the case where the answer really is this server's fault.
    const res = await h.fetch('/nothing-here', { method: 'POST' })
    assert.equal(res.status, 404)
    assert.match(res.headers.get('content-type') ?? '', /json/)
    const { error } = (await res.json()) as { error: string }
    assert.match(error, /openapi\.json/)
    assert.doesNotMatch(error, /ENDPOINT_LIMITS/)
  })

  test('a GET under a prefix the API owns answers as the API, not as a page', async () => {
    // With a console build present, this is the case that has two wrong
    // answers rather than one. Answering 500 said the server was broken.
    // Answering the console's HTML 404 page would be the right status wrapped
    // in a body no API client can read, on a path a browser never asks for.
    //
    // The split is consoleClass, the same predicate the rate limiter uses to
    // decide the same question, rather than a second copy of the prefix list.
    for (const path of ['/v1/health', '/auth/nothing', '/console/api/nothing']) {
      const res = await h.fetch(path)
      assert.equal(res.status, 404, path)
      assert.match(res.headers.get('content-type') ?? '', /json/, path)
    }
  })

  test('a page a browser asked for still gets the console 404 page', async () => {
    // The other side of the same split, so a change that sent everything to
    // the API branch fails here rather than by handing a person JSON.
    const res = await h.fetch('/nothing-here')
    assert.equal(res.status, 404)
    assert.match(res.headers.get('content-type') ?? '', /html/)
  })
})

describe('running without a console build', { skip: ok ? false : 'no database' }, () => {
  let h: ApiHarness
  before(async () => {
    h = await startApi()
  })
  after(async () => h.close())

  test('the API serves normally', async () => {
    assert.equal((await h.fetch('/health')).status, 200)
  })

  test('a page says the build is missing rather than answering a blank 404', async () => {
    // A blank 404 on every page is indistinguishable from a routing bug. This
    // is the one state where saying so out loud is worth a non-standard status.
    const res = await h.fetch('/environments')
    assert.equal(res.status, 503)
    assert.match(await res.text(), /without the console build/)
  })
})

describe('provider keys from a browser', { skip: ok ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  const sealing = Buffer.alloc(32, 7)

  before(async () => {
    h = await startApi({ sealingKey: sealing })
    org = await seedOrg(h.admin, 'console-keys')
  })
  after(async () => h.close())

  /**
   * A signed-in session, and a minute on the clock.
   *
   * The mutating endpoints here share one bucket of ten, and the harness clock
   * only moves when a test moves it. Without this the suite's own volume
   * exhausts the limiter partway through and later tests fail with 429s that
   * have nothing to do with what they are checking -- which reads exactly like
   * a real bug in whichever test happens to be last.
   */
  async function as(role: 'owner' | 'admin' | 'member' | 'viewer') {
    h.clock.advance(60_000)
    return signInAs(h, org, role)
  }

  test('signed out, nothing is readable', async () => {
    assert.equal((await h.fetch('/console/api/providers')).status, 401)
  })

  test('a viewer can see which keys are set and is told they cannot change them', async () => {
    const v = await as('viewer')
    const res = await h.fetch('/console/api/providers', { headers: { cookie: v.cookie } })
    assert.equal(res.status, 200)
    const body = (await res.json()) as { mayManage: boolean; role: string; sealing: boolean }
    assert.equal(body.mayManage, false)
    assert.equal(body.role, 'viewer')
    assert.equal(body.sealing, true)
  })

  test('a viewer with a valid CSRF token still cannot store a key', async () => {
    // The role check is not the hidden form. This request is exactly what the
    // page would send if the controls were rendered.
    const v = await as('viewer')
    const res = await h.fetch('/console/api/providers/anthropic', {
      method: 'PUT',
      headers: { cookie: v.cookie, 'x-antifailure-csrf': v.csrfToken, 'content-type': 'application/json' },
      body: JSON.stringify({ key: 'sk-ant-should-never-be-stored' }),
    })
    assert.equal(res.status, 403)
    const list = await (
      await h.fetch('/console/api/providers', { headers: { cookie: v.cookie } })
    ).json()
    assert.equal((list as { keys: unknown[] }).keys.length, 0)
  })

  test('a request with no CSRF header changes nothing', async () => {
    const o = await as('owner')
    const res = await h.fetch('/console/api/providers/anthropic', {
      method: 'PUT',
      headers: { cookie: o.cookie, 'content-type': 'application/json' },
      body: JSON.stringify({ key: 'sk-ant-no-csrf' }),
    })
    assert.equal(res.status, 403)
  })

  test('storing, then rotating, then storing the same key again', async () => {
    const o = await as('owner')
    const put = (key: string) => {
      h.clock.advance(60_000)
      return h.fetch('/console/api/providers/anthropic', {
        method: 'PUT',
        headers: { cookie: o.cookie, 'x-antifailure-csrf': o.csrfToken, 'content-type': 'application/json' },
        body: JSON.stringify({ key }),
      })
    }

    const first = (await (await put('sk-ant-aaaaaaaaaaaaaaaaaaaa1111')).json()) as {
      last4: string
      replaced: boolean
      sameAsBefore: boolean
    }
    assert.equal(first.replaced, false)
    assert.equal(first.sameAsBefore, false)
    assert.equal(first.last4, '1111')

    const second = (await (await put('sk-ant-bbbbbbbbbbbbbbbbbbbb2222')).json()) as {
      replaced: boolean
      sameAsBefore: boolean
    }
    assert.equal(second.replaced, true)
    assert.equal(second.sameAsBefore, false)

    const again = (await (await put('sk-ant-bbbbbbbbbbbbbbbbbbbb2222')).json()) as {
      sameAsBefore: boolean
    }
    assert.equal(again.sameAsBefore, true, 'rotating to the same key should say so')
  })

  test('no route returns the key, including the one that lists them', async () => {
    const o = await as('owner')
    const body = await (
      await h.fetch('/console/api/providers', { headers: { cookie: o.cookie } })
    ).text()
    assert.ok(!body.includes('sk-ant-bbbbbbbbbbbbbbbbbbbb2222'), 'the key was echoed')
    assert.ok(!body.includes('sk-ant-b'), 'a prefix of the key was echoed')
  })

  test('a blank cap is refused rather than read as zero dollars', async () => {
    // Number('') and Number(null) are both 0. A cap of zero is legitimate,
    // which is what makes inferring it dangerous: nothing looks wrong until
    // every run refuses as overspent.
    const o = await as('owner')
    for (const body of [{}, { capUsd: '' }, { capUsd: null }, { capUsd: 'x' }, { capUsd: -1 }]) {
      h.clock.advance(60_000)
      const res = await h.fetch('/console/api/providers/anthropic/budget', {
        method: 'PUT',
        headers: { cookie: o.cookie, 'x-antifailure-csrf': o.csrfToken, 'content-type': 'application/json' },
        body: JSON.stringify(body),
      })
      assert.equal(res.status, 400, `${JSON.stringify(body)} should not have set a cap`)
    }
  })

  test('a cap of zero can still be asked for on purpose', async () => {
    const o = await as('owner')
    const res = await h.fetch('/console/api/providers/openai/budget', {
      method: 'PUT',
      headers: { cookie: o.cookie, 'x-antifailure-csrf': o.csrfToken, 'content-type': 'application/json' },
      body: JSON.stringify({ capUsd: 0 }),
    })
    assert.equal(res.status, 200)
    assert.equal(((await res.json()) as { capUsd: number }).capUsd, 0)
  })

  test('an unknown provider is refused', async () => {
    const o = await as('owner')
    const res = await h.fetch('/console/api/providers/notaprovider', {
      method: 'PUT',
      headers: { cookie: o.cookie, 'x-antifailure-csrf': o.csrfToken, 'content-type': 'application/json' },
      body: JSON.stringify({ key: 'sk-whatever' }),
    })
    assert.equal(res.status, 400)
  })

  test('removing is idempotent, so a retry after a timeout is not a failure', async () => {
    const o = await as('owner')
    const del = () => {
      h.clock.advance(60_000)
      return h.fetch('/console/api/providers/anthropic', {
        method: 'DELETE',
        headers: { cookie: o.cookie, 'x-antifailure-csrf': o.csrfToken },
      })
    }
    const first = (await (await del()).json()) as { revoked: boolean }
    assert.equal(first.revoked, true)
    const second = await del()
    assert.equal(second.status, 200)
    assert.equal(((await second.json()) as { revoked: boolean }).revoked, false)
  })
})

describe('a control plane with no sealing secret', { skip: ok ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  before(async () => {
    h = await startApi({ sealingKey: null })
    org = await seedOrg(h.admin, 'console-nosealing')
  })
  after(async () => h.close())

  test('says so, rather than storing a key in the clear', async () => {
    const o = await signInAs(h, org, 'owner')
    const list = (await (
      await h.fetch('/console/api/providers', { headers: { cookie: o.cookie } })
    ).json()) as { sealing: boolean }
    assert.equal(list.sealing, false)

    const res = await h.fetch('/console/api/providers/anthropic', {
      method: 'PUT',
      headers: { cookie: o.cookie, 'x-antifailure-csrf': o.csrfToken, 'content-type': 'application/json' },
      body: JSON.stringify({ key: 'sk-ant-would-be-plaintext' }),
    })
    assert.equal(res.status, 503)
    assert.match(((await res.json()) as { error: string }).error, /AF_PROVIDER_KEY_SECRET/)
  })
})
