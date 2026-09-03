// The cross-site guard on operator mutations, over real HTTP, under BOTH
// cookie configurations.
//
// A separate file from adminrouters.test.ts because it is a different layer:
// that one asks whether a route does the right thing for the right operator,
// with `createCaller` and no transport at all. This one asks whether the
// transport lets the request reach the route, and the only way to ask that is
// to make an actual request with actual headers.
//
// WHY IT IS PARAMETERISED, which is the whole reason this file was rewritten.
//
// `adminSessionCookie` writes `__Host-af_admin_session` when the cookie is
// Secure and the bare `af_admin_session` when it is not. `startApi` defaults
// `secureCookies` to FALSE. So this suite used to build the bare name by hand
// against a plain HTTP server, which is the one configuration nobody ships.
//
// Under that configuration the middleware's `readCookie(cookie, 'af_admin_session')`
// found the cookie and the guard ran, and five assertions passed. Under Secure
// it found nothing, skipped the block entirely, and the request reached a fully
// authenticated mutation with no origin check and no token check. The tRPC
// context resolved the operator regardless, because it reads the cookie with
// `readAdminSessionCookie`, which knows both names.
//
// It was found by driving a real browser at a real control plane and watching a
// revoke succeed with no `x-antifailure-admin-csrf` header, then reproducing it
// with curl. Measured independently from the other direction against a server
// booted with Secure cookies: a POST to `/trpc/admin.flags.kill` with a valid
// operator cookie and NO token answered 404 from the route rather than 403 from
// the gate, the same POST declaring `origin: https://evil.example` and
// `sec-fetch-site: cross-site` also answered 404, and the same POST with no
// cookie at all answered 401. The 404 against the 403 is the proof: a refusal
// by the gate does not look like a rejection by routing, and the no-cookie
// control rules out the route simply being broken.
//
// So the questions are asked twice, once per configuration, with the cookie
// name each configuration actually produces. Fixing the middleware without this
// leaves the next person who reaches for `readCookie` a green suite and a
// portal with no cross-site protection.
//
// WHAT THE EXPOSURE WAS, stated precisely, because the obvious version
// overshoots. The operator cookie is `SameSite=Strict` in both branches, so a
// browser sends it on NO cross-site request and an attacker page on another
// site cannot reach a mutation: its request carries no cookie and is refused by
// authentication. But SameSite is scoped to the registrable DOMAIN rather than
// to the origin, which the comment on `adminSessionCookie` says itself, so
// anything under our own site is same-site with the console and its requests DO
// carry the cookie. `__Host-` stops a subdomain PLANTING that cookie; it does
// not stop one SENDING it. The origin check is the layer that closes exactly
// that gap, its author clearly knew so, and it had never run.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { available, startApi, type ApiHarness } from './harness.ts'

const hasDatabase = await available()

/**
 * The whole suite, against one cookie configuration.
 *
 * `secureCookies` decides the name the server writes, so the fixture builds the
 * name that configuration would really send rather than a name chosen here.
 * That is the point: a fixture that picks its own name can only ever test the
 * configuration it picked.
 */
function guardUnder(secureCookies: boolean) {
  const label = secureCookies
    ? 'a Secure deployment, where the cookie is __Host- prefixed'
    : 'a plain HTTP deployment, where the cookie carries the bare name'

  describe(
    `an operator mutation over HTTP needs the cross-site token: ${label}`,
    { skip: hasDatabase ? false : 'no database' },
    () => {
      let h: ApiHarness
      let cookie: string
      let bareCookie: string
      let csrf: string

      before(async () => {
        h = await startApi({ secureCookies })

        // The session is created DIRECTLY rather than by signing in over HTTP,
        // and that is not a shortcut. `POST /v1/admin/signin` is rate limited to
        // half a request per second keyed by address, deliberately, because what
        // is behind it is every tenant on the instance. In a full run this file
        // follows admin-signin-route.test.ts, which spends that budget, and a
        // suite that competes with another for a rate limit is a suite that
        // fails on a busy machine and passes on a quiet one.
        //
        // What is under test here is the transport guard, and it reads the
        // cookie and the header. Neither cares how the row was made.
        const [row] = await h.admin<{ id: string }[]>`
          INSERT INTO admin_users (email, name, role)
          VALUES (${`csrf-${randomUUID().slice(0, 8)}@example.test`}, 'CSRF operator', 'owner')
          RETURNING id`
        const token = randomBytes(32).toString('base64url')
        await h.admin`
          INSERT INTO admin_sessions (token_hash, admin_user_id, expires_at)
          VALUES (${createHash('sha256').update(token).digest()}, ${row!.id},
                  ${new Date(Date.now() + 3_600_000).toISOString()})`
        // The name this configuration writes, so the request looks like the one
        // a browser on that deployment would make.
        bareCookie = `af_admin_session=${token}`
        cookie = secureCookies ? `__Host-${bareCookie}` : bareCookie

        // Through the endpoint, because the endpoint existing and returning a
        // usable token is half of what makes the guard satisfiable: without it
        // the portal could mutate exactly once per sign-in and fail on the first
        // refresh, which is a guard that looks like it works.
        const session = await h.fetch('/v1/admin/session', { headers: { cookie } })
        assert.equal(session.status, 200)
        const body = (await session.json()) as { signedIn: boolean; csrfToken: string }
        assert.equal(body.signedIn, true)
        csrf = body.csrfToken
        assert.ok(csrf && csrf.length > 20, 'the operator session returned no CSRF token')
      })

      after(async () => {
        // This suite's own rows only. A `DELETE FROM admin_audit_entries` with
        // no WHERE waits on every session row whose impersonation_audit_seq
        // points into the table, which in a full run means whatever an earlier
        // suite left behind, and the wait is indistinguishable from a hung test.
        await h.admin`DELETE FROM admin_sessions WHERE admin_user_id IN (
          SELECT id FROM admin_users WHERE email LIKE 'csrf-%')`
        await h.admin`DELETE FROM admin_audit_entries WHERE actor_label LIKE 'csrf-%'`
        await h.admin`DELETE FROM admin_users WHERE email LIKE 'csrf-%'`
        await h.close()
      })

      async function mutateAs(headers: Record<string, string>, as: () => string = () => cookie) {
        return h.fetch('/trpc/admin.flags.kill', {
          method: 'POST',
          headers: { 'content-type': 'application/json', cookie: as(), ...headers },
          body: JSON.stringify({ key: 'nothing.here', reason: 'a reason long enough' }),
        })
      }

      it('refuses a mutation with no token', async () => {
        const refused = await mutateAs({})
        assert.equal(
          refused.status,
          403,
          'an operator cookie reached a mutation with no cross-site token, so the gate did not run',
        )
        assert.match(await refused.text(), /x-antifailure-admin-csrf/)
      })

      it('refuses a mutation with the wrong token', async () => {
        const refused = await mutateAs({ 'x-antifailure-admin-csrf': 'not-the-token' })
        assert.equal(refused.status, 403)
      })

      it('refuses a mutation that declares another site as its origin', async () => {
        // The origin check runs BEFORE the token and fails open for a request
        // that declares nothing; one that declares a cross-site origin is
        // refused without its token being looked at.
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
        // The transport passes and the ROUTE refuses: there is no such flag.
        // That is the assertion, and it is also why a 404 here is not clearance
        // in the test above: the gate refusing looks like 403 and routing
        // rejecting looks like 404, and the two must not be confused.
        assert.equal(allowed.status, 404)
        assert.match(await allowed.text(), /no flag called nothing.here/)
      })

      it('a QUERY needs no token, or the portal could not render before it had one', async () => {
        const read = await h.fetch('/trpc/admin.flags.list', { headers: { cookie } })
        assert.equal(read.status, 200)
      })

      if (secureCookies) {
        it('still guards a bare-name cookie issued before Secure was turned on', async () => {
          // `readAdminSessionCookie` accepts both names on purpose: a cookie
          // issued before somebody enabled Secure is still a live session, and
          // refusing it would sign every operator out on deploy. So the guard
          // has to cover that one too, or turning Secure on would open the hole
          // for exactly the sessions that predate the change.
          const refused = await mutateAs({}, () => bareCookie)
          assert.equal(
            refused.status,
            403,
            'a session that predates Secure reached a mutation with no cross-site token',
          )
        })
      }
    },
  )
}

// Both configurations, and the second one is the one that ships.
guardUnder(false)
guardUnder(true)
