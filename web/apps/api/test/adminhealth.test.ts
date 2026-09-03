// The health checks, against rows that make each one go red.
//
// Every check here is asserted twice: once on a clean installation, where it
// must be ok, and once against rows chosen to breach exactly its threshold.
// A check only ever verified in its green state is a check nobody has proved
// can turn red, and a health page whose rows cannot turn red is decoration.

import assert from 'node:assert/strict'
import test, { after, before, describe } from 'node:test'
import type { Db } from '@antifailure/db'
import { available, seedOrg, startApi, type ApiHarness, type Org } from './harness.ts'
import {
  healthChecks,
  ingestionLag,
  leakedEnvironments,
  stalledGenerations,
  suspendedOrganizations,
  teardownBacklog,
  unhandledDeliveries,
  worst,
  type Check,
} from '../src/admin/health.ts'

const NOW = new Date('2026-03-01T12:00:00.000Z')

describe('the health page can go red', { concurrency: 1 }, async () => {
  if (!(await available())) {
    test('skipped: no database', { skip: true }, () => {})
    return
  }

  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'health')
  })
  after(async () => {
    await h.close()
  })

  /** Runs a check on a connection scoped to the seeded organization. */
  const scoped = <T>(fn: (db: Db) => Promise<T>): Promise<T> =>
    h.pool.withTenant({ orgId: org.orgId }, fn)

  const find = (checks: Check[], id: string): Check => {
    const found = checks.find((c) => c.id === id)
    assert.ok(found, `no check named ${id}`)
    return found
  }

  test('an environment inside its expiry is not a leak, and one past it is', async () => {
    // seedOrg leaves a running environment with no expiry at all. An
    // environment that was never given a lifetime cannot have outlived one,
    // and counting it would make the row red on every installation forever.
    const clean = await scoped((db) => leakedEnvironments(db, NOW))
    assert.equal(clean.verdict, 'ok')
    assert.equal(clean.value, 0)

    await h.admin`
      UPDATE environments SET expires_at = ${new Date(NOW.getTime() + 3600_000)}
      WHERE env_id = ${org.envId}`
    const inside = await scoped((db) => leakedEnvironments(db, NOW))
    assert.equal(inside.verdict, 'ok', 'an expiry in the future is not past')

    await h.admin`
      UPDATE environments SET expires_at = ${new Date(NOW.getTime() - 3600_000)}
      WHERE env_id = ${org.envId}`
    const leaked = await scoped((db) => leakedEnvironments(db, NOW))
    assert.equal(leaked.verdict, 'degraded', 'one past its expiry is worth saying')
    assert.equal(leaked.value, 1)
    assert.ok(leaked.remedy, 'a red row without a remedy is a row nobody can act on')

    // Torn down is the end state, not a leak, whatever the expiry says.
    await h.admin`UPDATE environments SET state = 'torn_down' WHERE env_id = ${org.envId}`
    const gone = await scoped((db) => leakedEnvironments(db, NOW))
    assert.equal(gone.verdict, 'ok')

    await h.admin`
      UPDATE environments SET state = 'running', expires_at = NULL WHERE env_id = ${org.envId}`
  })

  test('a teardown waiting a minute is fine and one waiting an hour is failing', async () => {
    const clean = await scoped((db) => teardownBacklog(db, NOW))
    assert.equal(find(clean, 'teardown-waiting').verdict, 'ok')
    assert.equal(find(clean, 'teardown-abandoned').verdict, 'ok')

    const [fresh] = await h.admin<{ id: string }[]>`
      INSERT INTO teardown_requests (org_id, env_id, reason, state, requested_at, updated_at)
      VALUES (${org.orgId}, ${org.envId}, 'test', 'pending',
              ${new Date(NOW.getTime() - 60_000)}, ${NOW})
      RETURNING id`
    const waiting = await scoped((db) => teardownBacklog(db, NOW))
    assert.equal(find(waiting, 'teardown-waiting').value, 1)
    assert.equal(
      find(waiting, 'teardown-waiting').verdict,
      'ok',
      'asked for a minute ago is waiting, not stuck',
    )

    // The count did not change. The AGE did, and the age is what decides.
    await h.admin`
      UPDATE teardown_requests SET requested_at = ${new Date(NOW.getTime() - 3 * 3600_000)}
      WHERE id = ${fresh!.id}`
    const stuck = await scoped((db) => teardownBacklog(db, NOW))
    assert.equal(find(stuck, 'teardown-waiting').value, 1, 'same count')
    assert.equal(find(stuck, 'teardown-waiting').verdict, 'failing', 'different age')
    assert.match(find(stuck, 'teardown-waiting').detail, /180 minutes/)

    // Abandoned has no tolerable quantity: one is failing.
    await h.admin`UPDATE teardown_requests SET state = 'abandoned' WHERE id = ${fresh!.id}`
    const abandoned = await scoped((db) => teardownBacklog(db, NOW))
    assert.equal(find(abandoned, 'teardown-waiting').verdict, 'ok', 'nothing is waiting now')
    assert.equal(find(abandoned, 'teardown-abandoned').verdict, 'failing')
    assert.equal(find(abandoned, 'teardown-abandoned').value, 1)

    await h.admin`DELETE FROM teardown_requests WHERE id = ${fresh!.id}`
  })

  test('no events is not an outage, a late event is, and a clock ahead is its own answer', async () => {
    const quiet = await scoped((db) => ingestionLag(db))
    assert.equal(quiet.verdict, 'ok', 'a fresh installation is not a broken one')
    assert.match(quiet.detail, /No events/)

    const insert = async (occurred: Date, received: Date) => {
      await h.admin`DELETE FROM events WHERE org_id = ${org.orgId}`
      await h.admin`
        INSERT INTO events (org_id, idempotency_key, type, occurred_at, received_at)
        VALUES (${org.orgId}, ${`k-${received.getTime()}`}, 'run.started', ${occurred}, ${received})`
    }

    await insert(new Date(NOW.getTime() - 5_000), NOW)
    assert.equal((await scoped(ingestionLag)).verdict, 'ok', 'five seconds is ordinary')

    await insert(new Date(NOW.getTime() - 20 * 60_000), NOW)
    const late = await scoped(ingestionLag)
    assert.equal(late.verdict, 'degraded')
    assert.equal(late.value, 1200)

    await insert(new Date(NOW.getTime() - 2 * 3600_000), NOW)
    assert.equal((await scoped(ingestionLag)).verdict, 'failing')

    // An engine whose clock is ahead reports a NEGATIVE gap. Taking the
    // absolute value would read it as ordinary and hide the cause of every
    // out of order run view that follows.
    await insert(new Date(NOW.getTime() + 30_000), NOW)
    const skewed = await scoped(ingestionLag)
    assert.equal(skewed.verdict, 'degraded')
    assert.match(skewed.detail, /clock is ahead/)

    await h.admin`DELETE FROM events WHERE org_id = ${org.orgId}`
  })

  test('a delivery in flight is not unhandled, and one from an hour ago is', async () => {
    const clean = await scoped((db) => unhandledDeliveries(db, NOW))
    assert.equal(clean.verdict, 'ok')

    // Two minutes old and unhandled is a request in flight. Counting it would
    // make the row flicker red under ordinary load.
    await h.admin`
      INSERT INTO github_deliveries (delivery_id, org_id, account_login, event, received_at)
      VALUES ('d-inflight', ${org.orgId}, ${org.slug}, 'pull_request',
              ${new Date(NOW.getTime() - 2 * 60_000)})`
    assert.equal((await scoped((db) => unhandledDeliveries(db, NOW))).verdict, 'ok')

    await h.admin`
      INSERT INTO github_deliveries (delivery_id, org_id, account_login, event, received_at)
      VALUES ('d-dropped', ${org.orgId}, ${org.slug}, 'pull_request',
              ${new Date(NOW.getTime() - 3600_000)})`
    const dropped = await scoped((db) => unhandledDeliveries(db, NOW))
    assert.equal(dropped.verdict, 'degraded')
    assert.equal(dropped.value, 1)

    // Handled is handled, however long it took.
    await h.admin`UPDATE github_deliveries SET handled_at = ${NOW} WHERE delivery_id = 'd-dropped'`
    assert.equal((await scoped((db) => unhandledDeliveries(db, NOW))).verdict, 'ok')

    await h.admin`DELETE FROM github_deliveries WHERE delivery_id IN ('d-inflight', 'd-dropped')`
  })

  test('a check inside its deadline is fine and one past it is stalled', async () => {
    const [pr] = await h.admin<{ id: string }[]>`
      INSERT INTO pull_requests (org_id, repository_id, number, head_sha, head_ref, base_ref,
                                 head_repository)
      VALUES (${org.orgId}, ${org.repoId}, 1, 'abc', 'feature', 'main', ${org.repository})
      RETURNING id`

    const gen = async (state: string, deadline: Date) => {
      await h.admin`DELETE FROM pr_generations WHERE pull_request_id = ${pr!.id}`
      await h.admin`
        INSERT INTO pr_generations (org_id, pull_request_id, head_sha, state, deadline_at)
        VALUES (${org.orgId}, ${pr!.id}, 'abc', ${state}::pr_generation_state, ${deadline})`
    }

    await gen('running', new Date(NOW.getTime() + 600_000))
    assert.equal((await scoped((db) => stalledGenerations(db, NOW))).verdict, 'ok')

    await gen('running', new Date(NOW.getTime() - 600_000))
    const stalled = await scoped((db) => stalledGenerations(db, NOW))
    assert.equal(stalled.verdict, 'degraded')
    assert.equal(stalled.value, 1)

    // A finished generation past its deadline finished late. It is not stuck,
    // and nothing is sitting on a pull request because of it.
    await gen('passed', new Date(NOW.getTime() - 600_000))
    assert.equal((await scoped((db) => stalledGenerations(db, NOW))).verdict, 'ok')

    await h.admin`DELETE FROM pr_generations WHERE pull_request_id = ${pr!.id}`
    await h.admin`DELETE FROM pull_requests WHERE id = ${pr!.id}`
  })

  test('a suspension is reported and never worse than degraded', async () => {
    assert.equal((await scoped(suspendedOrganizations)).verdict, 'ok')
    await h.admin`
      UPDATE organizations SET suspended_at = ${NOW}, suspended_reason = 'an incident'
      WHERE id = ${org.orgId}`
    const suspended = await scoped(suspendedOrganizations)
    assert.equal(suspended.verdict, 'degraded', 'a suspension is information, not a fault')
    assert.equal(suspended.value, 1)
    await h.admin`
      UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL WHERE id = ${org.orgId}`
  })

  test('every check carries a remedy when it is red and none when it is green', async () => {
    const checks = await scoped((db) => healthChecks(db, NOW))
    assert.equal(worst(checks), 'ok', 'the seeded organization is clean by now')
    for (const c of checks) {
      assert.equal(c.remedy, null, `${c.id} is ok and should offer nothing to do`)
      assert.ok(c.detail.length > 0, `${c.id} has no detail`)
      assert.ok(c.unit.length > 0, `${c.id} has no unit`)
    }

    // Now make one of them fail and prove the summary follows the worst row
    // rather than being stored beside it.
    await h.admin`
      INSERT INTO teardown_requests (org_id, env_id, reason, state, requested_at, updated_at)
      VALUES (${org.orgId}, ${org.envId}, 'test', 'abandoned', ${NOW}, ${NOW})`
    const red = await scoped((db) => healthChecks(db, NOW))
    assert.equal(worst(red), 'failing')
    for (const c of red.filter((x) => x.verdict !== 'ok')) {
      assert.ok(c.remedy, `${c.id} went red without saying what to do about it`)
    }
    await h.admin`DELETE FROM teardown_requests WHERE org_id = ${org.orgId}`
  })
})
