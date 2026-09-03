// The fleet and firewall surfaces, against rows that make each claim provable.
//
// Two things are asserted harder than the rest, because both are conditions
// this console exists to stop somebody misreading:
//
//   A teardown request with no route was RECORDED and never DISPATCHED, and
//   must never read as a teardown that is on its way.
//
//   A sandbox rule with no credential forwards whatever the application sent.
//   Every instance is failing, from the first one.

import assert from 'node:assert/strict'
import test, { after, before, describe } from 'node:test'
import type { Db } from '@antifailure/db'
import { available, seedOrg, startApi, type ApiHarness, type Org } from './harness.ts'
import {
  fleetBlastRadius,
  requestFleetTeardown,
  standingOf,
  teardownLedger,
  twins,
} from '../src/admin/fleet.ts'
import { findings, forwardsTheLiveCredential, summary } from '../src/admin/firewall.ts'

const NOW = new Date('2026-03-01T12:00:00.000Z')

describe('the fleet and the firewall', { concurrency: 1 }, async () => {
  if (!(await available())) {
    test('skipped: no database', { skip: true }, () => {})
    return
  }

  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'fleet')
  })
  after(async () => {
    await h.close()
  })

  const scoped = <T>(fn: (db: Db) => Promise<T>): Promise<T> =>
    h.pool.withTenant({ orgId: org.orgId }, fn)

  test('a live twin is listed, a torn down one is not, and overdue is its own scope', async () => {
    const live = await scoped((db) => twins(db, NOW))
    assert.equal(live.length, 1)
    assert.equal(live[0]!.envId, org.envId)
    assert.equal(live[0]!.overdue, false)
    assert.equal(live[0]!.teardownPending, false)
    assert.equal(live[0]!.orgSlug, org.slug)
    assert.equal(live[0]!.repository, org.repository)

    assert.equal((await scoped((db) => twins(db, NOW, { scope: 'overdue' }))).length, 0)

    await h.admin`
      UPDATE environments SET expires_at = ${new Date(NOW.getTime() - 60_000)}
      WHERE env_id = ${org.envId}`
    const overdue = await scoped((db) => twins(db, NOW, { scope: 'overdue' }))
    assert.equal(overdue.length, 1)
    assert.equal(overdue[0]!.overdue, true)

    await h.admin`UPDATE environments SET state = 'torn_down' WHERE env_id = ${org.envId}`
    assert.equal((await scoped((db) => twins(db, NOW))).length, 0, 'live excludes torn down')
    assert.equal((await scoped((db) => twins(db, NOW, { scope: 'all' }))).length, 1)

    await h.admin`
      UPDATE environments SET state = 'running', expires_at = NULL WHERE env_id = ${org.envId}`
  })

  test('recorded with nothing to reach never reads as dispatched', async () => {
    // The predicate on its own first, because this is the distinction the
    // whole module exists for and it must hold for every combination, not
    // just the ones a fixture happens to produce.
    const base = { state: 'pending', attempts: 0, lease_holder: null }
    assert.equal(
      standingOf({ ...base, env_id: null, workflow_run_id: null }),
      'nothing-to-reach',
    )
    assert.equal(
      standingOf({ ...base, env_id: 'e1', workflow_run_id: null }),
      'waiting-to-dispatch',
      'an environment id is a route: the engine can be asked by it',
    )
    assert.equal(
      standingOf({ ...base, env_id: 'e1', workflow_run_id: '99', attempts: 1 }),
      'dispatched-unconfirmed',
      'a pass has asked. Accepted is not confirmed',
    )
    assert.equal(
      standingOf({ ...base, state: 'leased', env_id: 'e1', workflow_run_id: '99', lease_holder: 'cp' }),
      'dispatched-unconfirmed',
    )
    // A leased row with no route is still a row with no route. The sweeper
    // claimed it, found nothing to ask, and will put it back.
    assert.equal(
      standingOf({ ...base, state: 'leased', env_id: null, workflow_run_id: null, attempts: 3 }),
      'nothing-to-reach',
      'claiming a row that has nothing to reach did not dispatch anything',
    )
    assert.equal(
      standingOf({ ...base, state: 'acknowledged', env_id: 'e1', workflow_run_id: '99' }),
      'confirmed',
    )
    assert.equal(
      standingOf({ ...base, state: 'abandoned', env_id: 'e1', workflow_run_id: '99', attempts: 5 }),
      'abandoned',
    )

    // And through the query, so the row shape and the predicate agree.
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO teardown_requests (org_id, env_id, reason, state, requested_at, updated_at)
      VALUES (${org.orgId}, NULL, 'nothing to reach', 'pending', ${NOW}, ${NOW})
      RETURNING id`
    const ledger = await scoped((db) => teardownLedger(db, NOW))
    const found = ledger.find((l) => l.id === row!.id)
    assert.ok(found)
    assert.equal(found.standing, 'nothing-to-reach')
    assert.match(found.route, /no cancel was ever sent/)

    await h.admin`DELETE FROM teardown_requests WHERE id = ${row!.id}`
  })

  test('an expired lease is shown as expired rather than as held', async () => {
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO teardown_requests (org_id, env_id, reason, state, attempts, lease_holder,
                                     leased_until, requested_at, updated_at)
      VALUES (${org.orgId}, ${org.envId}, 'stuck', 'leased', 2, 'control-plane',
              ${new Date(NOW.getTime() - 60_000)}, ${NOW}, ${NOW})
      RETURNING id`
    const [held] = await scoped((db) => teardownLedger(db, NOW, { open: true }))
    assert.equal(held!.leaseExpired, true, 'the lease ran out and no pass has taken it back')
    assert.equal(held!.leaseHolder, 'control-plane')
    assert.equal(held!.standing, 'dispatched-unconfirmed')

    // open: false is the default and must include the finished rows too.
    await h.admin`UPDATE teardown_requests SET state = 'acknowledged' WHERE id = ${row!.id}`
    assert.equal((await scoped((db) => teardownLedger(db, NOW, { open: true }))).length, 0)
    assert.equal((await scoped((db) => teardownLedger(db, NOW))).length, 1)

    await h.admin`DELETE FROM teardown_requests WHERE id = ${row!.id}`
  })

  test('the blast radius is counted, and a second press changes nothing', async () => {
    const before = await scoped((db) => fleetBlastRadius(db, { orgId: org.orgId }))
    assert.equal(before.environments, 1)
    assert.equal(before.organizations, 1)
    assert.equal(before.alreadyRequested, 0)

    const first = await scoped((db) =>
      requestFleetTeardown(db, NOW, { userId: null }, 'an incident', { orgId: org.orgId }),
    )
    assert.equal(first.length, 1)
    assert.equal(first[0]!.recorded, true)
    assert.equal(
      first[0]!.reachable,
      false,
      'this environment has no pull request generation, so no workflow run to cancel',
    )

    // The radius now says one environment already has a request, so the number
    // an operator confirms is the number that would actually change.
    const after = await scoped((db) => fleetBlastRadius(db, { orgId: org.orgId }))
    assert.equal(after.environments, 1)
    assert.equal(after.alreadyRequested, 1)

    const second = await scoped((db) =>
      requestFleetTeardown(db, NOW, { userId: null }, 'again', { orgId: org.orgId }),
    )
    assert.equal(second[0]!.recorded, false, 'a second press is not a second instruction')
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM teardown_requests WHERE org_id = ${org.orgId}`
    assert.equal(Number(rows[0]!.n), 1, 'one live request per environment')

    // And the twin now says a teardown is pending, so the fleet list and the
    // ledger cannot disagree about it.
    const [twin] = await scoped((db) => twins(db, NOW))
    assert.equal(twin!.teardownPending, true)

    await h.admin`DELETE FROM teardown_requests WHERE org_id = ${org.orgId}`
  })

  test('a sandbox rule with no credential is failing, from the first one', async () => {
    // The predicate, exhaustively. Empty string and null are the same
    // condition because applySandbox compares against the empty string.
    assert.equal(forwardsTheLiveCredential({ mode: 'sandbox', credential: null }), true)
    assert.equal(forwardsTheLiveCredential({ mode: 'sandbox', credential: '' }), true)
    assert.equal(forwardsTheLiveCredential({ mode: 'sandbox', credential: '   ' }), true)
    assert.equal(forwardsTheLiveCredential({ mode: 'SANDBOX', credential: null }), true)
    assert.equal(forwardsTheLiveCredential({ mode: 'sandbox', credential: 'sk_test_x' }), false)
    assert.equal(
      forwardsTheLiveCredential({ mode: 'block', credential: null }),
      false,
      'block substitutes nothing and promises nothing, so it cannot fail to substitute',
    )
    assert.equal(forwardsTheLiveCredential({ mode: 'allow', credential: null }), false)

    const rule = async (mode: string, credential: string | null, approved: boolean) => {
      await h.admin`DELETE FROM network_rules WHERE org_id = ${org.orgId}`
      await h.admin`
        INSERT INTO network_rules (org_id, host, mode, credential, approved_at)
        VALUES (${org.orgId}, 'api.stripe.com', ${mode}, ${credential},
                ${approved ? NOW : null})`
    }

    await rule('sandbox', 'sk_test_ok', true)
    let f = await scoped(findings)
    assert.equal(f.length, 0, 'a working sandbox rule is not a finding')
    assert.equal((await scoped(summary)).forwardingLiveCredentials, 0)

    await rule('sandbox', null, true)
    f = await scoped(findings)
    assert.equal(f.length, 1)
    assert.equal(f[0]!.kind, 'sandbox-without-credential')
    assert.equal(f[0]!.severity, 'failing', 'there is no acceptable quantity of this')
    assert.match(f[0]!.says, /substituted is false/)
    assert.equal((await scoped(summary)).forwardingLiveCredentials, 1)

    // Unapproved AND unsubstitutable reports both. Fixing the credential must
    // not silently drop the approval finding off the page.
    await rule('sandbox', null, false)
    f = await scoped(findings)
    assert.deepEqual(
      f.map((x) => x.kind).sort(),
      ['never-approved', 'sandbox-without-credential'],
    )
    assert.equal(f[0]!.kind, 'sandbox-without-credential', 'the always-wrong one sorts first')

    // An approved allow is a review finding, not a failure.
    await rule('allow', null, true)
    f = await scoped(findings)
    assert.equal(f.length, 1)
    assert.equal(f[0]!.kind, 'allow')
    assert.equal(f[0]!.severity, 'review')

    // An unapproved rule is inert, whatever its mode says.
    await rule('allow', null, false)
    f = await scoped(findings)
    assert.equal(f.length, 1)
    assert.equal(f[0]!.kind, 'never-approved')
    assert.match(f[0]!.says, /inert/)

    const s = await scoped(summary)
    assert.equal(s.rules, 1)
    assert.equal(s.neverApproved, 1)
    assert.equal(s.allowed, 0, 'an unapproved allow is not an allow that is in force')

    await h.admin`DELETE FROM network_rules WHERE org_id = ${org.orgId}`
  })
})
