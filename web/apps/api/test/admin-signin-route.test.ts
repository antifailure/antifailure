// The operator sign-in ROUTES, tested through HTTP rather than by calling the
// functions underneath them.
//
// WHY THIS FILE EXISTS AT ALL. admin-boundary.test.ts already proves adminSignIn
// works, and it proves it by calling adminSignIn. That is the right test of the
// function and it is not a test of the PRODUCT: before these routes existed,
// every one of those assertions passed while the operator portal could not be
// signed into by anybody, because nothing in src/ ever called the function or
// set the cookie. ctx.admin was null on every request and all twelve admin
// procedures answered UNAUTHORIZED.
//
// So this file asks the only question that distinguishes those two worlds: does
// a browser that POSTs a correct password end up able to reach an admin route.
// It goes through fetch, it keeps the Set-Cookie, and it sends it back.
//
// THE COOKIE NAME IS THE POINT OF THE LAST TEST. adminSessionCookie writes
// `__Host-af_admin_session` when Secure and the bare name otherwise, so a reader
// that knows only the bare name works in development and never in production.
// That is a defect with no error in it: sign in succeeds, returns 200 with a
// Set-Cookie, and the next request is anonymous again. secureCookies is forced
// on for that test so the prefixed name is the one actually exercised.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { hashPassword } from '../src/admin/session.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

const hasDb = await available()

/** The cookie a Set-Cookie header is offering, name included, ready to send
 *  straight back. Deliberately NOT parsed into a name and a value the test
 *  then reassembles: reassembling is how a test stops exercising the name the
 *  server actually chose, which is the thing under test here. */
function cookieFrom(res: Response): string | null {
  const header = res.headers.get('set-cookie')
  if (!header) return null
  return header.split(';')[0]!
}


/**
 * Move the server's clock to real time before signing anybody in.
 *
 * THIS IS NOT TEST HOUSEKEEPING, it is a real coupling worth knowing about. An
 * operator session's lifetime is enforced in TWO places by TWO clocks: the
 * application computes expires_at from its own clock, and the RLS predicate
 * behind current_admin_user() compares that column against the DATABASE's
 * now(). The harness's FakeClock starts at 2026-01-01, so a session it issues
 * is born already expired from the database's point of view, current_admin_user()
 * resolves to nothing, and the sign-in audit append is refused by row-level
 * security.
 *
 * The failure names none of that. It surfaces as
 * `new row violates row-level security policy for table "admin_audit_entries"`
 * on the SUCCESS path, while every wrong-password test still passes, because
 * those never get far enough to write a session. It cost an hour here and the
 * same shape would cost a night in production, where the two clocks are the app
 * server's and the database's and skew between them is ordinary.
 *
 * admin-boundary.test.ts does not hit this because it calls adminSignIn with
 * `new Date()` directly rather than through the server's injected clock.
 */
/**
 * Headers that put each caller in its own rate-limit bucket.
 *
 * The sign-in route is limited on clientKey(x-forwarded-for, user-agent), and
 * every request from this suite arrives with neither, so without this the whole
 * FILE shares one token bucket: the four parallel attempts in the refusal test
 * drain it and a later test gets 429, which reads exactly like an
 * authentication failure and sent me looking at the gate instead of the
 * limiter. Distinct callers is also the honest shape, since these are meant to
 * be different people signing in.
 *
 * x-forwarded-for rather than user-agent: `new Request()` drops a user-agent
 * set through init.headers, so keying on that silently changed nothing and the
 * bucket stayed shared. Measured by the 429 not moving.
 */
let caller = 0
function asNewCaller(): Record<string, string> {
  caller += 1
  return { 'content-type': 'application/json', 'x-forwarded-for': `203.0.113.${caller % 250}` }
}

function useRealTime(h: ApiHarness): void {
  h.clock.advance(Date.now() - h.clock.now().getTime())
}

async function seedOperator(
  h: ApiHarness,
  password: string,
  opts: { suspended?: boolean; withPassword?: boolean } = {},
): Promise<string> {
  const email = `operator-${randomUUID().slice(0, 8)}@example.test`
  const creds = opts.withPassword === false ? null : await hashPassword(password)
  await h.admin`
    INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at, suspended_at)
    VALUES (${email}, 'Route Operator', 'super_admin',
            ${creds?.hash ?? null}, ${creds?.salt ?? null},
            ${creds ? new Date().toISOString() : null},
            ${opts.suspended ? new Date().toISOString() : null})`
  return email
}

describe(
  'the operator sign-in routes',
  { skip: hasDb ? false : 'no database' },
  () => {
    let h: ApiHarness
    const password = 'a-provisioned-password-nobody-shipped'

    before(async () => {
      h = await startApi()
      useRealTime(h)
    })

    after(async () => {
      await h?.close()
    })

    it('signs an operator in and the session reaches an admin route', async () => {
      const email = await seedOperator(h, password)

      const res = await h.fetch('/v1/admin/signin', {
        method: 'POST',
        headers: asNewCaller(),
        body: JSON.stringify({ email, password }),
      })
      assert.equal(res.status, 200, await res.text())
      const cookie = cookieFrom(res)
      assert.ok(cookie, 'signing in must set a cookie')

      // THE ASSERTION THAT WOULD HAVE CAUGHT THE DEAD PATH. Not "a cookie was
      // returned" but "the cookie makes an admin procedure answer". admin.me is
      // gated by adminProcedure('admin.portal.access'), so a 200 here means the
      // whole chain resolved: cookie read under the right name, session found,
      // actor built, permission checked.
      const me = await h.fetch('/trpc/admin.me', { headers: { cookie: cookie! } })
      // Read the body ONCE. A template literal in the assert message is
      // evaluated whether or not the assertion fails, so `${await me.text()}`
      // there consumes the stream and the next me.json() throws "Body is
      // unusable" on the PASSING path.
      const raw = await me.text()
      assert.equal(me.status, 200, `admin.me refused a freshly signed-in operator: ${raw}`)
      const body = JSON.parse(raw) as { result: { data: { email: string } } }
      assert.equal(body.result.data.email, email)
    })

    it('leaves an admin route unreachable without the cookie', async () => {
      // The control. Without it, the test above could pass on a gate that
      // admits everybody, and it would look identical.
      const me = await h.fetch('/trpc/admin.me')
      assert.equal(me.status, 401, 'an anonymous request must not reach an admin route')
    })

    it('refuses a wrong password, an unknown address and a suspended operator with the same words', async () => {
      // One message for all three, or the endpoint answers "which of your
      // guesses was closer". Compared to each other rather than to a literal,
      // so the wording can change without this test going stale, and so the
      // property under test is the SAMENESS.
      const email = await seedOperator(h, password)
      const suspended = await seedOperator(h, password, { suspended: true })
      const unprovisioned = await seedOperator(h, password, { withPassword: false })

      const attempts = await Promise.all(
        [
          { email, password: 'the-wrong-password' },
          { email: 'nobody@example.test', password },
          { email: suspended, password },
          { email: unprovisioned, password },
        ].map(async (creds) => {
          const res = await h.fetch('/v1/admin/signin', {
            method: 'POST',
            headers: asNewCaller(),
            body: JSON.stringify(creds),
          })
          return { status: res.status, body: (await res.json()) as { error: string } }
        }),
      )

      for (const attempt of attempts) {
        assert.equal(attempt.status, 401, 'a refused credential is 401, not 400: it was not malformed')
        assert.equal(
          attempt.body.error,
          attempts[0]!.body.error,
          'every refusal must read identically, or this endpoint enumerates operators',
        )
      }
    })

    it('signs out, and the cookie stops working', async () => {
      const email = await seedOperator(h, password)
      const signin = await h.fetch('/v1/admin/signin', {
        method: 'POST',
        headers: asNewCaller(),
        body: JSON.stringify({ email, password }),
      })
      // Checked before it is used. Without this, a sign-in that was refused
      // yields a null cookie, the next request is simply anonymous, and the
      // failure reads as "the gate rejected a valid session" when the truth is
      // that no session was ever issued.
      assert.equal(signin.status, 200, await signin.text())
      const cookie = cookieFrom(signin)!

      assert.equal(
        (await h.fetch('/trpc/admin.me', { headers: { cookie } })).status,
        200,
        'a freshly signed-in operator must reach an admin route',
      )
      const out = await h.fetch('/v1/admin/signout', { method: 'POST', headers: { cookie } })
      assert.equal(out.status, 200)
      assert.equal(
        (await h.fetch('/trpc/admin.me', { headers: { cookie } })).status,
        401,
        'the session must be revoked server-side, not merely cleared in the browser',
      )
    })
  },
)

describe(
  'the operator cookie under Secure, where the __Host- prefix applies',
  { skip: hasDb ? false : 'no database' },
  () => {
    let h: ApiHarness
    const password = 'a-provisioned-password-nobody-shipped'

    before(async () => {
      // The production shape. With secureCookies on, adminSessionCookie writes
      // `__Host-af_admin_session`, and a reader that only knows the bare name
      // finds nothing.
      h = await startApi({ secureCookies: true })
      useRealTime(h)
    })

    after(async () => {
      await h?.close()
    })

    it('writes the prefixed name and still resolves it on the next request', async () => {
      const email = await seedOperator(h, password)
      const res = await h.fetch('/v1/admin/signin', {
        method: 'POST',
        headers: asNewCaller(),
        body: JSON.stringify({ email, password }),
      })
      assert.equal(res.status, 200, await res.text())

      const header = res.headers.get('set-cookie') ?? ''
      assert.match(
        header,
        /^__Host-af_admin_session=/,
        'a Secure deployment must write the prefixed name, which is what forbids a Domain attribute',
      )
      assert.match(header, /Path=\//, '__Host- requires Path=/')
      assert.match(header, /Secure/, '__Host- requires Secure')
      assert.doesNotMatch(header, /Domain=/, '__Host- forbids Domain')

      // The half that was broken: reading it back. Before readAdminSessionCookie
      // this returned 401 in production and 200 in development, with no error
      // anywhere in between.
      const me = await h.fetch('/trpc/admin.me', { headers: { cookie: cookieFrom(res)! } })
      assert.equal(
        me.status,
        200,
        'the prefixed cookie must resolve, or operators sign in and land back on the sign-in screen forever',
      )
    })
  },
)
