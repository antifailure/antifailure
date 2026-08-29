// The console's routes, as actual requests.
//
// console.test.ts tests the templates. This tests the ROUTES, and it exists
// because the route had a hole the templates could not have: it read a session,
// checked CSRF, and then did whatever the form said, with no check on what the
// person's role was. Any signed-in member -- including a viewer, the role that
// exists precisely to be able to look and not touch -- could store, rotate or
// remove somebody else's provider key from a browser.
//
// The hole was invisible in three places at once. The page renders the same for
// every role, so a screenshot shows nothing. The templates are unit tested
// without a session, so no test had a role to get wrong. And the CLI, written
// later, checked the role correctly, which made the console look like it must
// be doing the same.
//
// So the assertions here are about a POST arriving from something that is not
// the rendered page, because that is what a missing permission actually looks
// like: the form is hidden and the endpoint is open.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import {
  available,
  startApi,
  seedOrg,
  dropOrg,
  signInAs,
  type ApiHarness,
  type Org,
} from './harness.ts'
import type { Role } from '../src/permissions.ts'
import { appendAudit } from '@antifailure/db'
import { listKeys, listBudgets } from '../src/providers/store.ts'

const ANTHROPIC = ['sk', 'ant', 'api03'].join('-')
const KEY_ONE = `${ANTHROPIC}-aaaaaaaaaaaaaaaaaaaaaaaaaaaa1111`

describe('the console provider key page', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let api: ApiHarness
  let org: Org
  const sealingKey = randomBytes(32)

  before(async () => {
    api = await startApi({ sealingKey })
    org = await seedOrg(api.admin, 'console-keys')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  beforeEach(async () => {
    await api.admin`DELETE FROM provider_keys WHERE org_id = ${org.orgId}`
    await api.admin`DELETE FROM provider_budgets WHERE org_id = ${org.orgId}`
    // POST /console/keys allows a burst of ten and refills at one a second, and
    // the clock in these tests does not move on its own. Without this the suite
    // exhausts the bucket partway through and every later test fails with a
    // rate limit rendered as a page, which reads exactly like a broken form.
    // Letting a minute pass between a person's actions is what actually
    // happens; disabling the limiter would test a server nobody runs.
    api.clock.advance(60_000)
  })

  async function as(role: Role, label?: string) {
    return signInAs(api, org, role, label ?? `console-${role}`)
  }

  async function view(who: { cookie: string }) {
    const res = await api.fetch('/settings/keys', { headers: { cookie: who.cookie } })
    return { status: res.status, html: await res.text() }
  }

  /** Posts the form directly, which is what an attacker does. */
  async function submit(
    who: { cookie: string; csrfToken: string },
    fields: Record<string, string>,
  ) {
    const body = new URLSearchParams({ csrf: who.csrfToken, ...fields })
    const res = await api.fetch('/console/keys', {
      method: 'POST',
      headers: { cookie: who.cookie, 'content-type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    })
    return { status: res.status, html: await res.text() }
  }

  // -------------------------------------------------------------------------
  // Who may change one
  // -------------------------------------------------------------------------

  for (const role of ['viewer', 'member'] as Role[]) {
    test(`a ${role} cannot store a key by posting the form`, async () => {
      const who = await as(role)
      const res = await submit(who, { provider: 'anthropic', action: 'save', key: KEY_ONE })
      assert.match(res.html, /owners and admins/)
      // The claim that matters is not the message. It is that nothing was written.
      assert.deepEqual(await listKeys(api.pool, org.orgId), [])
    })

    test(`a ${role} cannot set a budget by posting the form`, async () => {
      const who = await as(role)
      await submit(who, { provider: 'anthropic', action: 'budget', cap: '500' })
      assert.deepEqual(await listBudgets(api.pool, api.clock, org.orgId), [])
    })

    test(`a ${role} cannot remove somebody else's key`, async () => {
      const owner = await as('owner', 'console-owner-victim')
      await submit(owner, { provider: 'anthropic', action: 'save', key: KEY_ONE })
      assert.equal((await listKeys(api.pool, org.orgId)).length, 1)

      const who = await as(role)
      await submit(who, { provider: 'anthropic', action: 'revoke' })
      // Still there. Revocation is the one that would be done maliciously: it
      // needs no key of the attacker's own and it stops every run.
      assert.equal((await listKeys(api.pool, org.orgId)).length, 1)
    })

    test(`a ${role} is shown the state and no controls`, async () => {
      const who = await as(role)
      const page = await view(who)
      assert.equal(page.status, 200)
      assert.match(page.html, /You can see these, and you cannot change them/)
      // No form posts to the endpoint they are not allowed to reach. A control
      // that cannot work is worse than no control.
      assert.ok(!page.html.includes('action="/console/keys"'))
    })
  }

  for (const role of ['owner', 'admin'] as Role[]) {
    test(`an ${role} stores, caps and removes a key`, async () => {
      const who = await as(role)
      const saved = await submit(who, { provider: 'anthropic', action: 'save', key: KEY_ONE })
      assert.equal(saved.status, 200)
      const keys = await listKeys(api.pool, org.orgId)
      assert.equal(keys.length, 1)
      assert.equal(keys[0]!.last4, '1111')
      // The page it renders back says the last four and never the key.
      assert.match(saved.html, /1111/)
      assert.ok(!saved.html.includes(KEY_ONE))

      await submit(who, { provider: 'anthropic', action: 'budget', cap: '40' })
      const budgets = await listBudgets(api.pool, api.clock, org.orgId)
      assert.equal(budgets[0]!.capUsd, 40)

      await submit(who, { provider: 'anthropic', action: 'revoke' })
      assert.deepEqual(await listKeys(api.pool, org.orgId), [])
    })

    test(`an ${role} sees the controls`, async () => {
      const page = await view(await as(role))
      assert.ok(page.html.includes('action="/console/keys"'))
      assert.ok(!page.html.includes('You can see these, and you cannot change them'))
    })
  }

  test('the role check happens even with a valid CSRF token', async () => {
    // Stated separately because the two are easy to conflate. CSRF proves the
    // request came from this person. It says nothing about whether this person
    // is allowed to make it, and a page that only checked CSRF would refuse a
    // forged request from an owner while accepting a genuine one from a viewer.
    const viewer = await as('viewer', 'console-csrf-viewer')
    const res = await submit(viewer, { provider: 'anthropic', action: 'save', key: KEY_ONE })
    assert.match(res.html, /owners and admins/)
    assert.deepEqual(await listKeys(api.pool, org.orgId), [])
  })

  // -------------------------------------------------------------------------
  // The blank cap
  // -------------------------------------------------------------------------

  test('submitting the budget form with the field left blank does not set a cap of zero', async () => {
    // Number('') is 0, so a coercing read turns an empty field into a cap of
    // zero dollars. Zero is a legitimate cap, which is exactly what makes this
    // dangerous: the page says the cap is set, and every run afterwards is
    // refused for having no allowance.
    const owner = await as('owner', 'console-blank-cap')
    await submit(owner, { provider: 'anthropic', action: 'budget', cap: '40' })

    const res = await submit(owner, { provider: 'anthropic', action: 'budget', cap: '' })
    assert.match(res.html, /That is not a cap/)
    const budgets = await listBudgets(api.pool, api.clock, org.orgId)
    assert.equal(budgets[0]!.capUsd, 40, 'the cap that was set must survive a blank submission')
  })

  test('a cap of zero can still be asked for on purpose', async () => {
    // The negative control for the test above. A checker that refused every
    // falsy value would pass it and would have taken away the only way to say
    // "spend nothing on this provider".
    const owner = await as('owner', 'console-zero-cap')
    await submit(owner, { provider: 'anthropic', action: 'budget', cap: '0' })
    const budgets = await listBudgets(api.pool, api.clock, org.orgId)
    assert.equal(budgets.length, 1)
    assert.equal(budgets[0]!.capUsd, 0)
  })

  test('a negative cap is refused', async () => {
    const owner = await as('owner', 'console-neg-cap')
    const res = await submit(owner, { provider: 'anthropic', action: 'budget', cap: '-5' })
    assert.match(res.html, /That is not a cap/)
    assert.deepEqual(await listBudgets(api.pool, api.clock, org.orgId), [])
  })

  // -------------------------------------------------------------------------
  // The rest of the form's edges
  // -------------------------------------------------------------------------

  test('a request with no CSRF token changes nothing', async () => {
    const owner = await as('owner', 'console-nocsrf')
    const res = await api.fetch('/console/keys', {
      method: 'POST',
      headers: { cookie: owner.cookie, 'content-type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ provider: 'anthropic', action: 'save', key: KEY_ONE }).toString(),
    })
    assert.match(await res.text(), /could not be trusted/)
    assert.deepEqual(await listKeys(api.pool, org.orgId), [])
  })

  test('signed out, the page asks for a sign-in rather than rendering an empty one', async () => {
    const res = await api.fetch('/settings/keys')
    assert.equal(res.status, 401)
    const body = await res.text()
    assert.match(body, /sign in/i)
    assert.ok(!body.includes('Provider keys'))
  })

  test('the endpoint is rate limited, and says so as a page rather than a blank', async () => {
    // Asserted rather than assumed, because the suite above works around it and
    // a workaround that quietly stopped being needed would mean the limit had
    // been dropped. Eleven posts against a burst of ten.
    const owner = await as('owner', 'console-ratelimit')
    let refused = false
    for (let i = 0; i < 12 && !refused; i++) {
      refused = (await submit(owner, { provider: 'anthropic', action: 'budget', cap: '1' })).status === 429
    }
    assert.ok(refused, 'a burst past the limit must be refused')
  })

  test('a key pasted for the wrong provider is refused and not echoed back', async () => {
    const owner = await as('owner', 'console-wrong-provider')
    const res = await submit(owner, { provider: 'openai', action: 'save', key: KEY_ONE })
    assert.match(res.html, /Anthropic key/)
    assert.ok(!res.html.includes(KEY_ONE))
    assert.deepEqual(await listKeys(api.pool, org.orgId), [])
  })
})

// ---------------------------------------------------------------------------

describe('every console page answers with a page', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  // The test that would have caught [object Response] on the first run.
  //
  // Nothing here asserts what a page SAYS -- the templates are tested for that,
  // and a screenshot judges the rest better than an assertion can. What is
  // asserted is that a request to each route produces a document: the right
  // status, HTML, a doctype, a title, the navigation, and enough of it that a
  // stub cannot pass. Those are the properties a route can lose without any
  // template test noticing, because a template test never goes through a route.

  let api: ApiHarness
  let org: Org
  let owner: { cookie: string; csrfToken: string }

  before(async () => {
    api = await startApi({ sealingKey: randomBytes(32) })
    org = await seedOrg(api.admin, 'console-pages')
    owner = await signInAs(api, org, 'owner', 'console-pages-owner')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  /** Every console GET the server declares, with parameters filled in. */
  function consolePaths(): string[] {
    // /metrics sits with /health and /readyz: an operational endpoint, not a
    // console page. It serves text/plain to a scraper and has no session, so
    // every assertion below about documents and sign-in is the wrong question
    // to ask of it.
    const skip = ['/v1', '/auth', '/trpc', '/health', '/readyz', '/metrics', '/openapi.json', '/console']
    const paths = new Set<string>()
    for (const route of api.app.routes) {
      if (route.method !== 'GET') continue
      if (skip.some((prefix) => route.path.startsWith(prefix))) continue
      if (route.path === '/*' || route.path.includes('*')) continue
      paths.add(
        route.path
          .replace(':envId', org.envId)
          // A run that does not exist: the route still has to answer with a
          // page, and "not found" rendered as a page is the correct answer.
          .replace(':runId', '00000000-0000-0000-0000-000000000000'),
      )
    }
    return [...paths].sort()
  }

  test('the route table has the pages this suite thinks it has', () => {
    // A guard on the guard. If somebody adds a page, this fails and they add it
    // here; if somebody deletes one, the loop below would otherwise pass by
    // testing nothing.
    const paths = consolePaths()
    assert.ok(paths.length >= 9, `expected the console's pages, found ${paths.join(', ')}`)
    for (const expected of ['/environments', '/runs', '/masking', '/network', '/audit', '/settings/keys', '/settings/members', '/device']) {
      assert.ok(paths.includes(expected), `${expected} is missing from the route table`)
    }
  })

  test('each one is a real document for somebody signed in', async () => {
    for (const path of consolePaths()) {
      const res = await api.fetch(path, { headers: { cookie: owner.cookie } })
      const body = await res.text()
      const where = `${path} -> ${res.status}`

      // 200 or a redirect between pages. Anything else is a page that is down.
      assert.ok([200, 302, 404].includes(res.status), `${where}: not a page`)
      if (res.status === 302) continue

      assert.match(res.headers.get('content-type') ?? '', /text\/html/, where)
      assert.match(body, /^<!doctype html>/i, where)
      assert.match(body, /<title>[^<]+<\/title>/, `${where}: no title`)
      // The literal failure this suite was written for. It is 17 characters and
      // a 200, so only a length check or this line catches it.
      assert.ok(!body.includes('[object '), `${where}: a value was stringified into the page`)
      assert.ok(body.length > 500, `${where}: ${body.length} bytes is not a page`)
      // The stylesheet, because a page served without it is the "unstyled" case
      // and looks like a different bug entirely.
      assert.match(body, /console\.css/, `${where}: no stylesheet`)
    }
  })

  test('each one refuses somebody signed out, without leaking what is on it', async () => {
    for (const path of consolePaths()) {
      const res = await api.fetch(path)
      const body = await res.text()
      const where = `${path} -> ${res.status}`
      assert.ok([200, 302, 401].includes(res.status), where)
      if (res.status === 200 || res.status === 401) {
        assert.match(body, /sign in/i, `${where}: not the sign-in page`)
      }
      assert.ok(!body.includes(org.repository), `${where}: leaked a repository name`)
      assert.ok(!body.includes(org.envId), `${where}: leaked an environment id`)
    }
  })

  test('every page carries the headers that make it safe to serve', async () => {
    for (const path of consolePaths()) {
      const res = await api.fetch(path, { headers: { cookie: owner.cookie } })
      if (res.status === 302) continue
      const where = `${path}`
      // no-store, because a page rendered for one session must never be served
      // to another out of a shared cache.
      assert.match(res.headers.get('cache-control') ?? '', /no-store/, where)
      assert.equal(res.headers.get('x-frame-options'), 'DENY', where)
      assert.match(res.headers.get('content-security-policy') ?? '', /default-src 'none'/, where)
    }
  })
})

// ---------------------------------------------------------------------------

describe('the evidence pages with rows in them', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  // A page whose query names a column that does not exist fails on an EMPTY
  // table, because Postgres parses before it looks. So the suite above -- which
  // runs against an organization with no goldens and no rules -- proves the
  // queries parse and nothing more. These put real rows in and read the
  // rendered numbers back, which is what catches the other half: a query that
  // parses and reads the wrong field, or reads the right one at the wrong
  // depth in a jsonb document.

  let api: ApiHarness
  let org: Org
  let owner: { cookie: string }

  before(async () => {
    api = await startApi()
    org = await seedOrg(api.admin, 'console-evidence')
    owner = await signInAs(api, org, 'owner', 'console-evidence-owner')

    // The attestation is the shape engine/internal/verify/scan.go signs, nested
    // exactly as it nests it. Flattening it here would make the test agree with
    // a query that reads the wrong depth.
    const attestation = {
      golden: 'g-2026-08-01',
      rules_hash: 'abc123',
      public_key: 'not-a-key',
      signature: 'not-a-signature',
      report: {
        scanner: 'af-verify/0.1.0',
        started_at: '2026-08-01T09:00:00Z',
        finished_at: '2026-08-01T09:04:31Z',
        tables: 42,
        columns: 317,
        rows_sampled: 91234,
        sample_size: 500,
        findings: [],
      },
    }
    // sql.json, not JSON.stringify. The driver serialises an object for a jsonb
    // parameter already, so stringifying first stores a jsonb STRING containing
    // the document rather than the document -- valid jsonb, and every path read
    // against it returns null. Which is what this fixture did on its first run,
    // and it is the same mistake the page itself could make.
    await api.admin`
      INSERT INTO golden_versions (org_id, repository_id, version, verified, attestation, size_bytes)
      VALUES (${org.orgId}, ${org.repoId}, 'g-2026-08-01', true,
              ${api.admin.json(attestation)}::jsonb, 1048576)`
    await api.admin`
      INSERT INTO golden_versions (org_id, repository_id, version, verified, attestation)
      VALUES (${org.orgId}, ${org.repoId}, 'g-2026-07-30', false, NULL)`

    await api.admin`
      INSERT INTO network_rules (org_id, repository_id, host, mode, paths, methods, position)
      VALUES (${org.orgId}, ${org.repoId}, 'api.stripe.com', 'record',
              ARRAY['/v1/charges'], ARRAY['POST'], 0)`
    await api.admin`
      INSERT INTO network_rules (org_id, repository_id, host, mode, position)
      VALUES (${org.orgId}, NULL, 'events.example.test', 'deny', 1)`
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  async function pageAt(path: string) {
    const res = await api.fetch(path, { headers: { cookie: owner.cookie } })
    assert.equal(res.status, 200, path)
    return res.text()
  }

  test('the fixture stored a jsonb document, not a jsonb string', async () => {
    // The guard on the guard. A double-encoded attestation is still valid
    // jsonb, so every assertion below would fail with an em dash and look like
    // a broken query rather than a broken fixture.
    const [row] = await api.admin<{ t: string }[]>`
      SELECT jsonb_typeof(attestation) AS t FROM golden_versions WHERE version = 'g-2026-08-01'`
    assert.equal(row!.t, 'object')
  })

  test('the masking page reads the attestation, not just the row', async () => {
    const body = await pageAt('/masking')
    assert.match(body, /g-2026-08-01/)
    assert.match(body, /g-2026-07-30/)
    // The numbers come out of the nested report. A query reading one level too
    // shallow returns null for every one of these and renders an em dash.
    for (const n of ['42', '317', '91234']) {
      assert.ok(body.includes(n), `the rendered page is missing ${n} from the attestation`)
    }
    // One verified, one not, and the counters at the top agree with the table.
    assert.match(body, /verified/)
    assert.match(body, /unverified/)
  })

  test('a golden with no attestation renders beside one that has it', async () => {
    // The row that would throw if the jsonb reads were not null-tolerant. A
    // golden is inserted before it is scanned, so this is the normal state for
    // the newest one, not an edge case.
    const body = await pageAt('/masking')
    assert.match(body, /g-2026-07-30/)
    assert.ok(!body.includes('[object '))
    assert.ok(!body.toLowerCase().includes('null</td>'))
  })

  test('the network page renders paths and methods from their arrays', async () => {
    const body = await pageAt('/network')
    assert.match(body, /api\.stripe\.com/)
    assert.match(body, /\/v1\/charges/)
    assert.match(body, /POST/)
    assert.match(body, /events\.example\.test/)
    // The rule with no paths and no methods says what applies rather than
    // rendering an empty cell that reads as missing data.
    assert.match(body, /any/)
    // Postgres returns a text[] as a JS array; a driver or a cast that turned
    // it into "{/v1/charges}" would render the braces.
    assert.ok(!body.includes('{/v1/charges}'), 'a text[] was rendered as its Postgres literal')
    assert.ok(!body.includes('[object '))
  })

  test('a rule that applies to every repository says so', async () => {
    const body = await pageAt('/network')
    assert.match(body, />all</)
  })
})

// ---------------------------------------------------------------------------

describe('the console with a populated organization', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  // The console has never had anything in it.
  //
  // There is no GitHub App yet, so no installation, so no repository, so no
  // runs and no artifacts, and every page on the live site renders its empty
  // state. That is exactly the condition under which a page that cannot render
  // a ROW looks perfectly healthy: the query runs, the table is empty, the
  // empty state appears, and nothing has exercised the loop that draws data.
  //
  // So this seeds one of everything and reads the rendered values back. It is
  // written before the console is populated on purpose. The alternative is
  // finding these the day a real repository is connected, in front of somebody.

  let api: ApiHarness
  let org: Org
  let owner: { cookie: string }
  let runId: string

  before(async () => {
    api = await startApi()
    org = await seedOrg(api.admin, 'console-full')
    owner = await signInAs(api, org, 'owner', 'console-full-owner')

    const [env] = await api.admin<{ id: string }[]>`
      SELECT id FROM environments WHERE org_id = ${org.orgId} LIMIT 1`

    const [run] = await api.admin<{ id: string }[]>`
      INSERT INTO runs (org_id, environment_id, kind, state, started_at, finished_at)
      VALUES (${org.orgId}, ${env!.id}, 'test', 'complete',
              now() - interval '4 minutes', now())
      RETURNING id`
    runId = run!.id

    // Two verdicts with different values, because a page that renders one
    // state correctly and hard-codes the chip would pass with only one.
    await api.admin`
      INSERT INTO verdicts (org_id, run_id, workflow, persona, value, summary, steps, duration_ms)
      VALUES (${org.orgId}, ${runId}, 'checkout-flow', 'returning-customer', 'pass',
              'The cart survived a reload', 12, 41000),
             (${org.orgId}, ${runId}, 'password-reset', NULL, 'fail',
              'The reset link 404s on the second click', 7, 9000)`

    // One retained artifact and one whose bytes retention removed. The second
    // is the interesting row: it exists so a timeline can say "not retained"
    // rather than rendering a gap that reads as a bug.
    await api.admin`
      INSERT INTO artifacts (org_id, run_id, kind, step, storage_key, content_type, size_bytes, sha256, retained)
      VALUES (${org.orgId}, ${runId}, 'screenshot', 3, 'runs/x/step-3.png', 'image/png', 184320,
              'aa11bb22cc33dd44ee55ff6600112233445566778899aabbccddeeff00112233', true),
             (${org.orgId}, ${runId}, 'video', 9, 'runs/x/run.webm', 'video/webm', 9437184,
              'bb22cc33dd44ee55ff6600112233445566778899aabbccddeeff00112233aa11', false)`
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  async function pageAt(path: string) {
    const res = await api.fetch(path, { headers: { cookie: owner.cookie } })
    assert.equal(res.status, 200, `${path} answered ${res.status}`)
    const body = await res.text()
    // Every page, every time. A stringified object is a 200 with a plausible
    // length, so nothing else catches it.
    assert.ok(!body.includes('[object '), `${path} stringified a value into the page`)
    return body
  }

  test('the environments page shows the environment rather than its empty state', async () => {
    const body = await pageAt('/environments')
    assert.ok(body.includes(org.envId), 'the environment id is not on the page')
    assert.ok(body.includes(org.repository), 'the repository is not on the page')
    assert.match(body, /main/)
    assert.ok(!body.includes('No environments'), 'the empty state rendered with a row present')
  })

  test('the environment page shows one environment', async () => {
    const body = await pageAt(`/environments/${org.envId}`)
    assert.ok(body.includes(org.envId))
    assert.ok(body.includes(org.repository))
  })

  test('the runs page shows the run and both verdicts', async () => {
    const body = await pageAt('/runs')
    assert.ok(body.includes(runId.slice(0, 8)) || body.includes(runId),
      'the run is not identifiable on the page')
    assert.ok(!body.includes('No runs'), 'the empty state rendered with a run present')
  })

  test('the run page shows verdicts, their summaries and their artifacts', async () => {
    const body = await pageAt(`/runs/${runId}`)
    assert.ok(body.includes('checkout-flow'), 'a workflow name is missing')
    assert.ok(body.includes('password-reset'), 'the second workflow is missing')
    assert.ok(body.includes('The cart survived a reload'), 'a verdict summary is missing')
    // Both values render, so the chip is driven by the row rather than fixed.
    assert.match(body, /pass/)
    assert.match(body, /fail/)
    // Persona, steps and duration, because a verdict that says "fail" and
    // cannot say what failed or how far it got is a row with no use.
    assert.ok(body.includes('returning-customer'), 'the persona is missing')
    assert.ok(body.includes('12'), 'the step count is missing')
    assert.ok(body.includes('41.0s'), 'the duration is missing')
    // A verdict with no persona says who it ran as rather than leaving a blank
    // cell, which reads as missing data.
    assert.ok(body.includes('anybody'))

    // The artifacts, including the one whose bytes are gone. A page that
    // dropped it would show a run with a hole in its timeline.
    assert.ok(body.includes('screenshot'), 'the screenshot artifact is missing')
    assert.ok(body.includes('video'), 'the video artifact is missing')
    assert.ok(body.includes('runs/x/step-3.png'), 'the storage key is missing')
    // Sizes read as sizes. 184320 rendered raw is a number nobody reads as
    // 180 kilobytes, and a bigint arrives from the driver as a string.
    assert.ok(body.includes('180 kB'), 'the artifact size is not human readable')
    assert.ok(body.includes('not retained'),
      'an artifact whose bytes retention removed rendered as a gap')
    assert.ok(!body.includes('9437184'), 'a raw byte count reached the page')
  })

  test('a run that does not exist is a page, not a 500', async () => {
    const res = await api.fetch('/runs/00000000-0000-0000-0000-000000000000', {
      headers: { cookie: owner.cookie },
    })
    assert.ok([200, 404].includes(res.status), `answered ${res.status}`)
    const body = await res.text()
    assert.match(body, /^<!doctype html>/i)
    assert.ok(!body.includes('[object '))
  })

  test('a run id that is not a uuid is refused as a page too', async () => {
    // The shape that turns a missing row into a 500: Postgres rejects
    // 'not-a-uuid'::uuid with a type error rather than returning nothing, so
    // the not-found path is never reached.
    const res = await api.fetch('/runs/not-a-uuid', { headers: { cookie: owner.cookie } })
    assert.ok([200, 400, 404].includes(res.status), `answered ${res.status}`)
    assert.match(await res.text(), /^<!doctype html>/i)
  })

  test('the members page shows the member', async () => {
    const body = await pageAt('/settings/members')
    assert.match(body, /console-full-owner/)
    assert.match(body, /owner/)
  })

  test('the audit page shows an entry', async () => {
    // Written through appendAudit rather than inserted, because audit_entries
    // is append-only with a hash chain and a raw INSERT would have to invent
    // an entry_hash. Using the real function also means this exercises the
    // shape the application actually writes.
    await api.pool.withTenant({ orgId: org.orgId }, async (db) => {
      await appendAudit(db, {
        orgId: org.orgId,
        actorLabel: 'console-full-owner',
        action: 'provider_key.stored',
        targetType: 'provider_key',
        targetId: 'anthropic',
        origin: 'cli',
        detail: { provider: 'anthropic', last4: '4321' },
        occurredAt: new Date(),
      })
    })

    const body = await pageAt('/audit')
    assert.ok(body.includes('provider_key.stored'), 'the action is not on the page')
    assert.ok(body.includes('console-full-owner'), 'the actor is not on the page')
    assert.ok(!body.includes('No entries'), 'the empty state rendered with an entry present')
  })
})
