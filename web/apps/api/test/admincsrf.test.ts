// The cross-site guard on operator mutations, over real HTTP.
//
// A separate file from adminrouters.test.ts because it is a different layer:
// that one asks whether a route does the right thing for the right operator,
// with `createCaller` and no transport at all. This one asks whether the
// transport lets the request reach the route, and the only way to ask that is
// to make an actual request with actual headers.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { available, startApi, type ApiHarness } from './harness.ts'

const hasDatabase = await available()

describe(
  'an operator mutation over HTTP needs the cross-site token',
  { skip: hasDatabase ? false : 'no database' },
  () => {
  let h: ApiHarness
  let cookie: string
  let csrf: string

  before(async () => {
    h = await startApi()

    // The session is created DIRECTLY rather than by signing in over HTTP, and
    // that is not a shortcut. `POST /v1/admin/signin` is rate limited to half a
    // request per second keyed by address, deliberately, because what is behind
    // it is every tenant on the instance. In a full run this file follows
    // admin-signin-route.test.ts, which spends that budget, and a suite that
    // competes with another for a rate limit is a suite that fails on a busy
    // machine and passes on a quiet one.
    //
    // What is under test here is the transport guard, and it reads the cookie
    // and the header. Neither cares how the row was made.
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`csrf-${randomUUID().slice(0, 8)}@example.test`}, 'CSRF operator', 'owner')
      RETURNING id`
    const token = randomBytes(32).toString('base64url')
    await h.admin`
      INSERT INTO admin_sessions (token_hash, admin_user_id, expires_at)
      VALUES (${createHash('sha256').update(token).digest()}, ${row!.id},
              ${new Date(Date.now() + 3_600_000).toISOString()})`
    cookie = `af_admin_session=${token}`

    // Through the endpoint, because the endpoint existing and returning a
    // usable token is half of what makes the guard satisfiable: without it the
    // portal could mutate exactly once per sign-in and fail on the first
    // refresh, which is a guard that looks like it works.
    const session = await h.fetch('/v1/admin/session', { headers: { cookie } })
    assert.equal(session.status, 200)
    const body = (await session.json()) as { signedIn: boolean; csrfToken: string }
    assert.equal(body.signedIn, true)
    csrf = body.csrfToken
    assert.ok(csrf && csrf.length > 20, 'the operator session returned no CSRF token')
  })

  after(async () => {
    // This suite's own rows only. A `DELETE FROM admin_audit_entries` with no
    // WHERE waits on every session row whose impersonation_audit_seq points
    // into the table, which in a full run means whatever an earlier suite left
    // behind, and the wait is indistinguishable from a hung test.
    await h.admin`DELETE FROM admin_sessions WHERE admin_user_id IN (
      SELECT id FROM admin_users WHERE email LIKE 'csrf-%')`
    await h.admin`DELETE FROM admin_audit_entries WHERE actor_label LIKE 'csrf-%'`
    await h.admin`DELETE FROM admin_users WHERE email LIKE 'csrf-%'`
    await h.close()
  })

  async function mutateAs(headers: Record<string, string>) {
    return h.fetch('/trpc/admin.flags.kill', {
      method: 'POST',
      headers: { 'content-type': 'application/json', cookie, ...headers },
      body: JSON.stringify({ key: 'nothing.here', reason: 'a reason long enough' }),
    })
  }

  it('refuses a mutation with no token', async () => {
    const refused = await mutateAs({})
    assert.equal(refused.status, 403)
    assert.match(await refused.text(), /x-antifailure-admin-csrf/)
  })

  it('refuses a mutation with the wrong token', async () => {
    const refused = await mutateAs({ 'x-antifailure-admin-csrf': 'not-the-token' })
    assert.equal(refused.status, 403)
  })

  it('refuses a mutation that declares another site as its origin', async () => {
    // The origin check runs BEFORE the token and fails open for a request that
    // declares nothing; one that declares a cross-site origin is refused
    // without its token being looked at.
    const refused = await mutateAs({
      'x-antifailure-admin-csrf': csrf,
      'sec-fetch-site': 'cross-site',
      origin: 'https://not-this-product.example',
    })
    assert.equal(refused.status, 403)
    assert.match(await refused.text(), /came from another site/)
  })

  it('lets the real token through to the route, which then answers on its own terms', async () => {
    const allowed = await mutateAs({ 'x-antifailure-admin-csrf': csrf })
    // 200 from the transport, and the ROUTE refuses: there is no such flag.
    // That is the assertion: the guard stopped guarding and the procedure took
    // over, which is exactly one layer doing exactly one job.
    assert.equal(allowed.status, 404)
    assert.match(await allowed.text(), /no flag called nothing.here/)
  })

  it('a QUERY needs no token, or the portal could not render before it had one', async () => {
    const read = await h.fetch('/trpc/admin.flags.list', { headers: { cookie } })
    assert.equal(read.status, 200)
  })

  describe('the cookie NAME a real browser sends', () => {
    // THE HOLE THIS BLOCK EXISTS FOR, found by driving an actual browser at an
    // actual server rather than by reading the middleware.
    //
    // adminSessionCookie writes `__Host-af_admin_session` whenever the cookie
    // is Secure, and Secure is every deployment that matters. The guard read
    // the BARE name, so in production it found no cookie, skipped the whole
    // check, and let the request through; the tRPC context then resolved the
    // operator anyway through readAdminSessionCookie, which knows both names.
    // Every assertion above passed throughout, because this test server speaks
    // plain HTTP and the fixture above builds the bare name by hand.
    //
    // So the fix is one function call, and this is the instrument that can say
    // no to it: the same four questions, asked with the name a browser actually
    // sends. Without it the next person to write `readCookie` here gets a green
    // suite and a portal with no cross-site protection.
    const prefixed = () => `__Host-${cookie}`

    it('refuses a mutation with no token, under the prefixed name too', async () => {
      const refused = await h.fetch('/trpc/admin.flags.kill', {
        method: 'POST',
        headers: { 'content-type': 'application/json', cookie: prefixed() },
        body: JSON.stringify({ key: 'nothing.here', reason: 'a reason long enough' }),
      })
      assert.equal(
        refused.status,
        403,
        'a prefixed operator cookie reached a mutation with no cross-site token',
      )
      assert.match(await refused.text(), /x-antifailure-admin-csrf/)
    })

    it('lets the real token through under the prefixed name, so the fix is not a blanket refusal', async () => {
      // The other half. A guard that refuses every prefixed cookie would also
      // make this test pass if it only asserted the 403 above, and it would
      // break the portal for every operator in production.
      const allowed = await h.fetch('/trpc/admin.flags.kill', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          cookie: prefixed(),
          'x-antifailure-admin-csrf': csrf,
        },
        body: JSON.stringify({ key: 'nothing.here', reason: 'a reason long enough' }),
      })
      assert.equal(allowed.status, 404)
      assert.match(await allowed.text(), /no flag called nothing.here/)
    })
  })
  },
)
