import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { startApi, type ApiHarness } from './harness.ts'

/**
 * Every route a page on the marketing site calls, from every hostname the site
 * is served on.
 *
 * THE FAILURE THIS FILE EXISTS FOR. The marketing site answers on
 * antifailure.dev and on www.antifailure.dev: two custom domains on one Azure
 * Static Web App, both Ready, both serving every page, and neither redirecting
 * to the other, because a Static Web Apps route rule matches on PATH and the
 * schema has no hostname condition at all. site_origin held one value, the
 * apex. So a visitor who typed www, or followed an old link, sent
 * `origin: https://www.antifailure.dev` and the control plane refused every
 * call the page made with a 403: the analytics beacon, the enterprise contact
 * form, the careers application form. It was found on a phone, by a person, on
 * the live site, while every check anybody had run was green, because they all
 * asked the apex and the apex was perfect.
 *
 * WHY IT IS ONE FILE ACROSS THREE ROUTES rather than three assertions in three
 * suites. The bug was not in a route. It was that the comparison lived in four
 * places, written four times, against a value that was singular, and each of
 * those suites tested its own route from its own single origin and passed. The
 * property that was false is a property of the SET: every route, every
 * hostname. A suite that cannot state that property cannot notice it is false.
 *
 * The apex is deliberately the SECOND entry in the allowlist here. If any route
 * ever echoes "the configured origin" rather than "the entry that matched", a
 * request from the apex gets www's header back, the browser compares it against
 * its own origin and refuses, and the response still carries a header that a
 * check looking only for presence would call a pass.
 */
const WWW = 'https://www.antifailure.test'
const APEX = 'https://antifailure.test'
const BOTH = [WWW, APEX]

/** The routes a browser on the site calls cross origin. Each carries what a
 *  visitor experiences when it is refused, because a refused beacon is
 *  invisible and a refused form is a person being told the server is down. */
const ROUTES = [
  { path: '/v1/site/events', visible: 'the analytics beacon, silently' },
  { path: '/v1/leads', visible: 'the enterprise contact form, as "Could not reach the server"' },
  { path: '/v1/applications', visible: 'the careers application form, on a form just filled in' },
] as const

const hasDb = Boolean(process.env.AF_TEST_DATABASE_URL)

describe('every cross origin route answers every hostname the site is served on', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let caller = 0

  const preflight = (path: string, origin: string) =>
    h.fetch(path, {
      method: 'OPTIONS',
      headers: {
        origin,
        'access-control-request-method': 'POST',
        'access-control-request-headers': 'content-type',
        'x-forwarded-for': `198.51.100.${++caller}`,
      },
    })

  before(async () => {
    h = await startApi({ siteOrigins: BOTH })
  })
  after(async () => {
    await h.close()
  })

  for (const origin of BOTH) {
    for (const route of ROUTES) {
      it(`answers the preflight ${route.path} gets from ${origin}`, async () => {
        const res = await preflight(route.path, origin)
        assert.ok(
          res.status >= 200 && res.status < 300,
          `${route.path} answered ${res.status} to a preflight from ${origin}, which breaks ${route.visible}`,
        )
        // The ONE origin that matched, not the first entry and not a list. A
        // browser compares this against its own origin, so echoing the wrong
        // allowed origin refuses the request while looking configured, and a
        // header that carried two would be refused outright.
        assert.equal(
          res.headers.get('access-control-allow-origin'),
          origin,
          `${route.path} echoed the wrong origin to ${origin}`,
        )
        // Without this a shared cache can serve the header it built for one
        // hostname to a request that arrived on the other. It was a precaution
        // with one allowed origin. With two it is load bearing.
        assert.equal(res.headers.get('vary'), 'origin', `${route.path} does not vary on origin`)
        // Never. Credentials on an unauthenticated cross origin write is the
        // combination that would make these forgeable on somebody's behalf, and
        // it is absent rather than false so no header can be misread.
        assert.equal(res.headers.get('access-control-allow-credentials'), null)
      })
    }
  }

  it('lets a real post through from the www hostname, not just the preflight', async () => {
    // A preflight that passes and a POST that does not is the same outage with
    // a later symptom, and the two are answered by different handlers on every
    // one of these routes.
    const lead = await h.fetch('/v1/leads', {
      method: 'POST',
      headers: { origin: WWW, 'content-type': 'application/json', 'x-forwarded-for': `198.51.100.${++caller}` },
      body: JSON.stringify({
        name: 'Ada Lovelace',
        email: 'ada@example.test',
        company: 'Analytical Engines',
        seats: 40,
        message: 'seats, single sign-on',
        source: 'contact',
      }),
    })
    assert.equal(lead.status, 201)
    assert.equal(lead.headers.get('access-control-allow-origin'), WWW)
    const written = (await lead.json()) as { id: string }
    await h.admin`DELETE FROM enterprise_leads WHERE id = ${written.id}::uuid`

    const application = await h.fetch('/v1/applications', {
      method: 'POST',
      headers: { origin: WWW, 'content-type': 'application/json', 'x-forwarded-for': `198.51.100.${++caller}` },
      body: JSON.stringify({
        submissionId: randomUUID(),
        name: 'Ada',
        email: 'ada@example.test',
        role: 'founding_engineer',
        projectUrl: '',
        why: 'Built a compiler.',
        compensationAcknowledged: true,
        website: '',
      }),
    })
    assert.equal(application.status, 201)
    assert.equal(application.headers.get('access-control-allow-origin'), WWW)
    const recorded = (await application.json()) as { id: string }
    await h.admin`DELETE FROM recruitment_applications WHERE id = ${recorded.id}`

    const beacon = await h.fetch('/v1/site/events', {
      method: 'POST',
      headers: { origin: WWW, 'content-type': 'application/json', 'x-forwarded-for': `198.51.100.${++caller}` },
      body: JSON.stringify({ events: [] }),
    })
    assert.ok(beacon.status < 400, `the beacon answered ${beacon.status} from ${WWW}`)
    assert.equal(beacon.headers.get('access-control-allow-origin'), WWW)
  })

  it('still refuses a hostname that is not served, on every one of them', async () => {
    // Widening to a list must not have widened it to a pattern. Each of these
    // is a string somebody could mistake for the real one at a glance, which is
    // exactly why a suffix or prefix test is the mistake that survives review.
    for (const route of ROUTES) {
      for (const origin of [
        'https://evil-antifailure.test',
        'https://antifailure.test.evil.test',
        'https://www.antifailure.test.evil.test',
        'http://antifailure.test',
      ]) {
        const res = await preflight(route.path, origin)
        assert.equal(
          res.headers.get('access-control-allow-origin'),
          null,
          `${route.path} handed an allow header to ${origin}`,
        )
      }
    }
  })
})

describe('a control plane with no site origin configured', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let caller = 0
  before(async () => {
    h = await startApi({ siteOrigins: [] })
  })
  after(async () => {
    await h.close()
  })

  it('refuses every one of them rather than reflecting what arrived', async () => {
    // The refusing default, asserted rather than assumed. An empty list must
    // not read as "no restriction", which is what an allowlist implemented as
    // "match anything when the list is empty" would do, and that mistake is one
    // character away from the code that is there.
    for (const route of ROUTES) {
      const res = await h.fetch(route.path, {
        method: 'OPTIONS',
        headers: { origin: APEX, 'access-control-request-method': 'POST', 'x-forwarded-for': `203.0.113.${++caller}` },
      })
      assert.equal(res.status, 403, `${route.path} answered ${res.status} with nothing configured`)
      assert.equal(res.headers.get('access-control-allow-origin'), null)
    }
  })
})
