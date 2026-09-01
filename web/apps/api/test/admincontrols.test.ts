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
import { available, callProcedure, seedOrg, signInAs, startApi, type ApiHarness, type Org, type SignedIn } from './harness.ts'
import { CONTROLS, CONTROL_NAMES, controlStates, engagedReason, setControl } from '../src/admin/controls.ts'
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

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'controls')
    owner = await signInAs(h, org, 'owner')
    // An engaged control survives a restart, which is the whole point of
    // keeping it in a table. It also means a suite that does not reset
    // inherits whatever the last run left engaged, and a freeze left on by a
    // crashed run makes every later assertion here pass for the wrong reason.
    await h.admin`DELETE FROM platform_controls`
  })
  after(async () => {
    await h.close()
  })

  const engage = (name: (typeof CONTROL_NAMES)[number], reason: string) =>
    h.pool.withPlatformAdmin((db) =>
      setControl(db, h.clock.now(), name, true, { label: 'the operator' }, reason),
    )
  const release = (name: (typeof CONTROL_NAMES)[number]) =>
    h.pool.withPlatformAdmin((db) =>
      setControl(db, h.clock.now(), name, false, { label: 'the operator' }, null),
    )

  test('every declared control names a function that exists', async () => {
    // The catalog says which function refuses. If that string is stale the
    // console shows a switch whose enforcement was renamed away, which is
    // exactly the failure this file exists to make impossible.
    const sources = await Promise.all(
      ['server.ts', 'auth/signin.ts', 'routers/dispatch.ts'].map((f) => readFile(src(f), 'utf8')),
    )
    const haystack = sources.join('\n')
    for (const name of CONTROL_NAMES) {
      const def = CONTROLS[name]
      assert.ok(
        new RegExp(`function ${def.enforcedBy}\\b`).test(haystack),
        `${name} says it is enforced by ${def.enforcedBy}, and no such function exists`,
      )
      assert.ok(def.release.length > 0, `${name} does not say how to undo it`)
      assert.ok(def.effect.length > 0, `${name} does not say what it stops`)
    }
  })

  test('an ordinary connection cannot engage a switch, and does not silently fail to', async () => {
    // The point of the policy: a bug in any tenant route must not be able to
    // pause the installation. An UPDATE that matches no policy writes nothing
    // and reports success, so setControl has to notice the empty RETURNING and
    // throw rather than handing back a state nobody wrote.
    // Either answer is the guarantee holding. The policy refuses the INSERT
    // outright, which is stronger than the empty RETURNING that setControl
    // also guards against, and both prove nothing was written rather than
    // proving which mechanism caught it. Drizzle wraps the driver's error, so
    // the text is on the cause and asserting on `message` alone would pass
    // against any failure at all, including a typo in the statement.
    const refused = async (attempt: () => Promise<unknown>, why: string) => {
      let thrown: unknown
      try {
        await attempt()
      } catch (e) {
        thrown = e
      }
      assert.ok(thrown, why)
      const text = `${(thrown as Error).message} ${String((thrown as { cause?: unknown }).cause ?? '')}`
      assert.match(text, /row-level security|withPlatformAdmin/, why)
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
        h.pool.withPlatformAdmin((db) =>
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
    assert.match(String((await blocked.json()).error), /paused for maintenance: a bad deploy/)

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
