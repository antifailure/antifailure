// The emergency switches, broken on purpose.
//
// A control that is declared and not enforced is the worst artefact this
// product can ship: the operator engages it, believes the installation is
// paused, and stops looking for the problem. So every control in the catalog
// is driven here through the path a real request takes, and the catalog itself
// is checked against the source so a rename cannot leave a pointer stale.
//
// The write policy is attacked from an ordinary connection first, because that
// is the guarantee migration 0029 makes and an UPDATE that matches no policy
// reports success.

import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test, { after, before, describe } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createPool, type Pool } from '@antifailure/db'
import { adminUrl, available, callProcedure, seedOrg, signInAs, startApi, type ApiHarness, type Org, type SignedIn } from './harness.ts'
import { CONTROLS, CONTROL_NAMES, controlStates, engagedReason, setControl } from '../src/admin/controls.ts'
import { maintenanceExemptions } from '../src/server.ts'
import { refuseNewAccounts } from '../src/auth/signin.ts'

const src = (rel: string) => fileURLToPath(new URL(`../src/${rel}`, import.meta.url))

describe('the emergency switches actually stop things', { concurrency: 1 }, async () => {
  if (!(await available())) {
    test('skipped: no database', { skip: true }, () => {})
    return
  }

  let h: ApiHarness
  let org: Org
  let owner: SignedIn
  /**
   * A pool connecting as antifailure_admin, which is what `ctx.adminDb` will
   * be once the admin portal's pool lands. Built here rather than imported so
   * that this suite proves the GRANT in migration 0031 is what admits the
   * write, rather than proving that some helper exists.
   */
  let operator: Pool

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'controls')
    owner = await signInAs(h, org, 'owner')
    // An engaged control survives a restart, which is the whole point of
    // keeping it in a table. It also means a suite that does not reset
    // inherits whatever the last run left engaged, and a freeze left on by a
    // crashed run makes every later assertion here pass for the wrong reason.
    await h.admin`DELETE FROM platform_controls`
    // The operator role is created NOLOGIN by the migration, exactly as
    // antifailure_app is by 0001, so a password has to be set deliberately.
    // The harness does the same thing for the application role.
    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'operator-test-password'`)
    const url = new URL(adminUrl)
    url.username = 'antifailure_admin'
    url.password = 'operator-test-password'
    operator = createPool({ url: url.toString(), max: 2 })
  })
  after(async () => {
    await operator.close()
    await h.close()
  })

  const engage = (name: (typeof CONTROL_NAMES)[number], reason: string) =>
    operator.withoutTenant((db) =>
      setControl(db, h.clock.now(), name, true, { label: 'the operator' }, reason),
    )
  const release = (name: (typeof CONTROL_NAMES)[number]) =>
    operator.withoutTenant((db) =>
      setControl(db, h.clock.now(), name, false, { label: 'the operator' }, null),
    )

  test('every switch names its enforcement, and that enforcement is CALLED', async () => {
    // Three separate claims, because passing the first two while failing the
    // third is exactly what a dead kill switch looks like: the function
    // exists, the catalog points at it, and nothing invokes it.
    const searched = ['server.ts', 'auth/signin.ts', 'routers/dispatch.ts']
    const all = await Promise.all(searched.map((f) => readFile(src(f), 'utf8')))

    for (const name of CONTROL_NAMES) {
      const def = CONTROLS[name]
      const [file, symbol] = def.enforcedBy.split(':')
      assert.ok(file && symbol, `${name}.enforcedBy is not file:symbol`)

      // 1. That exact file declares that exact symbol. The file matters as
      //    well as the name: a bare name proves only that SOME file declares
      //    one, and enforcement moving into a module nothing calls would still
      //    satisfy it.
      const source = await readFile(src(file), 'utf8')
      assert.match(
        source,
        new RegExp(`function ${symbol}\\b`),
        `${name} says ${def.enforcedBy} refuses it, and that file declares no such function`,
      )

      // 2. Something CALLS it. A function with zero call sites is a dead kill
      //    switch: it looks like a control and does nothing, which is worse
      //    than having no control, because the operator who engaged it stops
      //    looking for the problem.
      const callSites = all
        .join('\n')
        .split('\n')
        .filter((line) => line.includes(`${symbol}(`) && !line.includes(`function ${symbol}(`))
      assert.ok(
        callSites.length > 0,
        `${name} is enforced by ${def.enforcedBy}, which has ZERO call sites. The switch would ` +
          `flip and nothing would stop.`,
      )

      // 3. It says what it stops and how to undo it. A control an operator
      //    cannot reverse is an outage this product caused.
      assert.ok(def.effect.length > 0, `${name} does not say what it stops`)
      assert.ok(def.release.length > 0, `${name} does not say how to undo it`)
    }
  })

  test('an ordinary connection cannot engage a switch, and does not silently fail to', async () => {
    // The point of the policy: a bug in any tenant route must not be able to
    // pause the installation. An UPDATE that matches no policy writes nothing
    // and reports success, so setControl has to notice the empty RETURNING and
    // throw rather than handing back a state nobody wrote.
    // The application role is granted SELECT on platform_controls and nothing
    // else, so this is a MISSING PRIVILEGE rather than a false predicate: the
    // statement raises instead of matching no rows and reporting success.
    // Drizzle wraps the driver's error, so the text is on the cause and
    // asserting on `message` alone would pass against any failure at all.
    const refused = async (attempt: () => Promise<unknown>, why: string) => {
      let thrown: unknown
      try {
        await attempt()
      } catch (e) {
        thrown = e
      }
      assert.ok(thrown, why)
      const text = `${(thrown as Error).message} ${String((thrown as { cause?: unknown }).cause ?? '')}`
      // `permission denied` is the grant refusing, which is the guarantee
      // migration 0031 makes. The other two are the older shapes and are kept
      // so this assertion cannot pass against a typo in the statement.
      assert.match(text, /permission denied|row-level security|adminDb/, why)
    }
    await refused(
      () =>
        h.pool.withTenant({ orgId: org.orgId }, (db) =>
          setControl(db, h.clock.now(), 'maintenance', true, { label: 'a tenant' }, 'try it'),
        ),
      'a tenant connection wrote platform_controls',
    )
    await refused(
      () =>
        h.pool.withoutTenant((db) =>
          setControl(db, h.clock.now(), 'maintenance', true, { label: 'nobody' }, 'try it'),
        ),
      'an untenanted connection wrote platform_controls',
    )
    // And nothing was written by either attempt.
    const rows = await h.admin<{ n: string }[]>`SELECT count(*) AS n FROM platform_controls`
    assert.equal(Number(rows[0]!.n), 0)
  })

  test('engaging needs a reason, and reading works from an ordinary connection', async () => {
    await assert.rejects(
      () =>
        operator.withoutTenant((db) =>
          setControl(db, h.clock.now(), 'maintenance', true, { label: 'the operator' }, '  '),
        ),
      /needs a reason/,
    )

    await engage('maintenance', 'a bad deploy')
    // The read policy is open to the application role on purpose: every request
    // has to be able to learn the installation is paused, including one with no
    // organization yet.
    assert.equal(
      await h.pool.withoutTenant((db) => engagedReason(db, 'maintenance')),
      'a bad deploy',
    )
    assert.equal(
      await h.pool.withTenant({ orgId: org.orgId }, (db) => engagedReason(db, 'maintenance')),
      'a bad deploy',
    )
    const states = await h.pool.withoutTenant(controlStates)
    assert.equal(states.length, CONTROL_NAMES.length, 'every control is listed, engaged or not')
    const m = states.find((s) => s.name === 'maintenance')!
    assert.equal(m.engaged, true)
    assert.equal(m.engagedBy, 'the operator')
    assert.equal(states.find((s) => s.name === 'signups')!.engaged, false)

    await release('maintenance')
    assert.equal(await h.pool.withoutTenant((db) => engagedReason(db, 'maintenance')), null)
    // Released, not deleted: the row still says who last touched it.
    const rows = await h.admin<{ engaged_by: string }[]>`
      SELECT engaged_by FROM platform_controls WHERE name = 'maintenance'`
    assert.equal(rows[0]!.engaged_by, 'the operator')
  })

  test('maintenance refuses writes, keeps reads, and leaves the way back open', async () => {
    const post = (path: string) =>
      h.fetch(path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' })

    // Before: a mutation is refused for its own reasons, never with a 503.
    const before = await post('/trpc/org.suspend')
    assert.notEqual(before.status, 503)

    await engage('maintenance', 'a bad deploy')

    const blocked = await post('/trpc/org.suspend')
    assert.equal(blocked.status, 503, 'a mutation was allowed through maintenance')
    const body = (await blocked.json()) as { error?: string }
    assert.match(String(body.error), /paused for maintenance: a bad deploy/)

    // Reads keep working. A pause where nobody can see the state is an outage.
    assert.equal((await h.fetch('/health')).status, 200)
    assert.equal((await h.fetch('/auth/session')).status, 200)

    // The way back. If sign-in were refused, the operator who engaged this
    // could not authenticate to release it.
    assert.notEqual((await post('/auth/device/code')).status, 503)
    // Engines keep reporting: refusing ingestion loses the record of work that
    // ran anyway, it does not pause anything.
    assert.notEqual((await post('/v1/events')).status, 503)
    // And the surface that owns the switch stays reachable.
    assert.notEqual((await post('/trpc/admin.controls.set')).status, 503)

    await release('maintenance')
    assert.notEqual((await post('/trpc/org.suspend')).status, 503, 'release did not take effect')
  })


  test('an operator can sign in COLD while maintenance is engaged', async () => {
    // The lockout this exists to prevent: engage maintenance, and the person
    // who engaged it cannot authenticate to release it. Driven as a real
    // sign-in through the real routes rather than by reading the route table
    // and agreeing with myself, because the whole risk is that the prefix
    // match and the actual route differ.
    await engage('maintenance', 'a bad deploy')

    const login = `operator-${Math.floor(Math.random() * 1e6)}`
    h.github.addUser({
      id: Math.floor(Math.random() * 1e9),
      login,
      email: `${login}@example.test`,
      name: login,
    })
    h.github.addOrganization(login, { id: 1, login: org.slug })

    // Cold: no cookie, no session, nothing carried over.
    const start = await h.fetch('/auth/github')
    assert.equal(start.status, 302, 'starting a sign-in was refused during maintenance')
    const state = new URL(start.headers.get('location')!).searchParams.get('state')!
    const code = h.github.approve(login)

    const done = await h.fetch(`/auth/github/callback?code=${code}&state=${state}`)
    assert.equal(done.status, 302, 'completing a sign-in was refused during maintenance')
    const cookie = done.headers.get('set-cookie')!
    assert.match(cookie, /af_session=/, 'no session was issued during maintenance')

    // And the session actually works, which is the thing that matters: a
    // cookie that is refused on its first use is not a sign-in.
    const session = await h.fetch('/auth/session', {
      headers: { cookie: cookie.split(';')[0]! },
    })
    const who = (await session.json()) as { signedIn: boolean }
    assert.equal(who.signedIn, true, 'the session issued during maintenance did not resolve')

    // THE OPERATOR'S OWN SIGN-IN, driven rather than reasoned about. This is
    // the route the route-table test caught living at /v1/admin/signin,
    // outside the /admin/ prefix, which would have meant the person who
    // engaged maintenance could not authenticate to release it. A 401 for bad
    // credentials is a PASS here: it proves the request reached the handler.
    // A 503 is the lockout.
    const operatorSignIn = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ email: 'nobody@example.test', password: 'wrong' }),
    })
    assert.notEqual(
      operatorSignIn.status,
      503,
      'operator sign-in was refused by maintenance mode, which is the lockout',
    )

    // The terminal path too, which is a POST and therefore actually passes
    // through the middleware rather than being waved through as a GET.
    const device = await h.fetch('/auth/device/code', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ clientLabel: 'a terminal' }),
    })
    assert.notEqual(device.status, 503, 'a terminal sign-in was refused during maintenance')

    await release('maintenance')
  })

  test('no route the server actually serves can be locked away by maintenance', async () => {
    // Walks the REAL route table rather than a list somebody remembered to
    // write down. The failure guarded against is not a wrong exemption, it is
    // a route added later, outside every prefix, that turns out to be the one
    // an operator needs to turn maintenance off.
    //
    // Hono exposes its routes on the app; each entry carries a method and a
    // path. GET is never refused, so only the rest matter.
    const routes = (h.app as unknown as { routes: { method: string; path: string }[] }).routes
    assert.ok(routes.length > 0, 'could not read the route table')

    const exempt = (path: string) => maintenanceExemptions.some((p) => path.startsWith(p))
    const mustBeReachable = routes.filter(
      (r) =>
        !['GET', 'HEAD', 'OPTIONS', 'ALL'].includes(r.method.toUpperCase()) &&
        // Anything that is how somebody proves who they are, in any spelling
        // the portal might land on.
        /auth|signin|sign-in|login|session|device|admin/i.test(r.path),
    )
    assert.ok(mustBeReachable.length > 0, 'found no sign-in routes, so this test proves nothing')
    for (const r of mustBeReachable) {
      assert.ok(
        exempt(r.path),
        `${r.method} ${r.path} is how somebody signs in and maintenance mode refuses it. ` +
          `Whoever engaged maintenance cannot authenticate to release it. Add its prefix to ` +
          `maintenanceExemptions in server.ts.`,
      )
    }
  })

  test('pausing sign-ups stops a new account and never an existing one', async () => {
    const [known] = await h.admin<{ github_id: string }[]>`
      SELECT github_id FROM users LIMIT 1`
    assert.ok(known, 'signInAs created a user to test against')
    const knownId = Number(known.github_id)
    const strangerId = knownId + 1

    // Open: neither is refused.
    await refuseNewAccounts(h.pool, knownId)
    await refuseNewAccounts(h.pool, strangerId)

    await engage('signups', 'an abuse wave')
    // The whole point of the switch: somebody mid-task is not locked out.
    await refuseNewAccounts(h.pool, knownId)
    await assert.rejects(
      () => refuseNewAccounts(h.pool, strangerId),
      /New sign-ups are paused/,
      'a stranger got in while sign-ups were paused',
    )
    await release('signups')
    await refuseNewAccounts(h.pool, strangerId)
  })

  test('freezing runs refuses every verb that starts work, and nothing else', async () => {
    const up = () =>
      callProcedure(h, owner, 'environments.create', 'mutation', {
        repository: org.repository,
        workflow: 'antifailure.yml',
      })
    const agents = () =>
      callProcedure(h, owner, 'agents.run', 'mutation', {
        envId: org.envId,
        workflow: 'antifailure.yml',
      })
    const load = () =>
      callProcedure(h, owner, 'load.run', 'mutation', {
        envId: org.envId,
        workflow: 'antifailure.yml',
      })

    // Before the freeze each of these fails for its own reason, which is not
    // this one. Asserting that first is what makes the assertion below mean
    // something rather than matching a refusal that was already there.
    for (const call of [up, agents, load]) {
      const r = await call()
      const message = JSON.stringify(r.body)
      assert.doesNotMatch(message, /paused on this installation/, 'frozen before it was frozen')
    }

    await engage('new_runs', 'the runtime is down')
    for (const [label, call] of [['environments.create', up], ['agents.run', agents], ['load.run', load]] as const) {
      const r = await call()
      assert.match(
        JSON.stringify(r.body),
        /New runs and environments are paused on this installation: the runtime is down/,
        `${label} started work while runs were frozen`,
      )
    }

    // Reading is untouched. The freeze stops work starting; it does not blind
    // the people who have work already running.
    const listed = await callProcedure(h, owner, 'environments.list', 'query', {})
    assert.equal(listed.status, 200, 'the freeze blocked a read')

    // Teardown is untouched too, deliberately: an operator freezing runs during
    // an incident must still be able to stop what is running.
    const down = await callProcedure(h, owner, 'environments.teardown', 'mutation', {
      envId: org.envId,
    })
    assert.equal(down.status, 200, 'the freeze blocked a teardown')

    await release('new_runs')
    const after = await up()
    assert.doesNotMatch(JSON.stringify(after.body), /paused on this installation/)

    await h.admin`DELETE FROM teardown_requests WHERE org_id = ${org.orgId}`
  })
})
