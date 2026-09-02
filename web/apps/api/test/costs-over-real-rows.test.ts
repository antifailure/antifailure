// The caps, over rows the ingestion path actually created.
//
// costs.test.ts proves the arithmetic, which is the right thing to prove about
// a pure function and is not enough on its own. Both numbers the arithmetic
// consumes come from SQL over the environments table, and until the projection
// created rows that table was empty in production, so every cap computed zero
// and a cap that computes zero is a cap that can never trip. An arithmetic
// test cannot see that: it is handed the usage.
//
// So nothing here inserts an environment. Every row these sums run over was
// brought into existence by an event arriving at /v1/events, which is the only
// path a real engine has, and the assertion that matters is that the number is
// not zero and that the cap refuses when it should.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'

import { capsFor, checkCostCap, costAttribution, environmentHoursSince } from '../src/costs.ts'
import {
  available, startApi, seedOrg, dropOrg, signInAs, callProcedure,
  type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

const HOUR = 60 * 60 * 1000

describe('the cost caps over real environments', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'costs-real')
    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_test')`
    // seedOrg brings its own environment, and this suite is about what the
    // ingestion path creates. Removing it means every hour counted below was
    // reported by an engine.
    await h.admin`DELETE FROM environments WHERE org_id = ${org.orgId}`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function send(events: unknown[]): Promise<any> {
    const res = await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ events }),
    })
    return res.json()
  }

  function event(
    type: string, envId: string, sequence: number, at: Date, branch = 'main',
  ): Record<string, unknown> {
    return {
      id: randomUUID(),
      type,
      envId,
      sequence,
      occurredAt: at.toISOString(),
      payload: { repository: org.repository, branch, ttl_seconds: 24 * 3600 },
    }
  }

  /** Reports one environment's whole life, as an engine does. */
  async function ran(label: string, createdAt: Date, tornDownAt: Date | null): Promise<void> {
    const id = `env-${label}-${randomUUID().slice(0, 8)}`
    await send([event('environment.creating', id, 1, createdAt, label)])
    await send([event('environment.ready', id, 2, new Date(createdAt.getTime() + 60_000), label)])
    if (tornDownAt) {
      await send([event('environment.torn_down', id, 3, tornDownAt, label)])
    }
  }

  it('counts hours that are not zero, which is the whole difference from an empty table', async () => {
    const now = h.clock.now()
    // Six hours, finished. Two hours, still running and counted to now. Ten
    // hours ending before the window opened, which contributes nothing.
    await ran('finished', new Date(now.getTime() - 10 * HOUR), new Date(now.getTime() - 4 * HOUR))
    await ran('running', new Date(now.getTime() - 2 * HOUR), null)
    await ran('old', new Date(now.getTime() - 40 * HOUR), new Date(now.getTime() - 30 * HOUR))

    const used = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
      environmentHoursSince(db, org.orgId, new Date(now.getTime() - 24 * HOUR), now))

    assert.notEqual(used, 0, 'the cap is summing over an empty table, so it can never trip')
    assert.equal(Math.round(used * 100) / 100, 8)
  })

  it('attributes those hours to the repository and branch that spent them', async () => {
    const now = h.clock.now()
    const lines = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
      costAttribution(db, org.orgId, new Date(now.getTime() - 24 * HOUR), now))

    assert.ok(lines.length >= 2, 'the bill has no lines, so nobody can act on the number')
    const worst = lines[0]!
    assert.equal(worst.repository, org.repository)
    assert.equal(worst.branch, 'finished', 'the most expensive line should be the six hour one')
    assert.equal(worst.hours, 6)
    assert.notEqual(worst.tornDownAt, null)
    // The still running one is counted up to now rather than left out, which
    // is the shape of runaway the cap exists to catch.
    const running = lines.find((l) => l.branch === 'running')!
    assert.equal(running.tornDownAt, null)
    assert.equal(running.hours, 2)
  })

  it('trips the per-day cap on rows an engine reported, rather than only in arithmetic', async () => {
    // The free plan allows 72 environment-hours in a rolling day. Three
    // environments held for a day each, reported entirely over /v1/events,
    // spend exactly that.
    const capped = await seedOrg(h.admin, 'costs-capped')
    try {
      await h.admin`DELETE FROM environments WHERE org_id = ${capped.orgId}`
      const cappedToken = `aft_${randomUUID().replace(/-/g, '')}`
      await h.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
        VALUES (${capped.orgId}, 'ci',
                ${createHash('sha256').update(cappedToken).digest()}, 'aft_capped')`

      const now = h.clock.now()
      const caps = capsFor('free')

      const before = await h.pool.withTenant({ orgId: capped.orgId }, (db) =>
        environmentHoursSince(db, capped.orgId, new Date(now.getTime() - 24 * HOUR), now))
      assert.equal(before, 0, 'a fresh organization has spent nothing')
      assert.equal(checkCostCap('free', 24, before).allowed, true,
        'an ordinary run must be admitted before anything has been spent')

      for (let i = 0; i < 3; i++) {
        const id = `env-runaway-${i}-${randomUUID().slice(0, 8)}`
        const events = [
          {
            id: randomUUID(), type: 'environment.creating', envId: id, sequence: 1,
            occurredAt: new Date(now.getTime() - 24 * HOUR).toISOString(),
            payload: { repository: capped.repository, branch: `loop-${i}`, ttl_seconds: 24 * 3600 },
          },
        ]
        const res = await h.fetch('/v1/events', {
          method: 'POST',
          headers: { authorization: `Bearer ${cappedToken}`, 'content-type': 'application/json' },
          body: JSON.stringify({ events }),
        })
        const body = (await res.json()) as { accepted: number }
        assert.equal(body.accepted, 1)
      }

      const used = await h.pool.withTenant({ orgId: capped.orgId }, (db) =>
        environmentHoursSince(db, capped.orgId, new Date(now.getTime() - 24 * HOUR), now))
      assert.equal(Math.round(used), caps.perDayHours,
        `three environments held for a day is ${caps.perDayHours} hours and the table says ${used}`)

      const verdict = checkCostCap('free', 1, used)
      assert.equal(verdict.allowed, false, 'the cap did not trip on a day that was fully spent')
      assert.equal(verdict.kind, 'per-day')
      assert.ok(verdict.reason.includes('72 hours'))
    } finally {
      await dropOrg(h.admin, capped.orgId)
    }
  })

  // ---------------------------------------------------------------------------
  // The whole chain, in one call
  // ---------------------------------------------------------------------------

  it('answers environments.costs with a number that is not zero, from events alone', async () => {
    // The one assertion that covers every link: an engine posts to /v1/events,
    // the projection creates a row, the cost SQL sums it, and the procedure a
    // person actually calls returns it. Every one of those was in place before
    // and the answer was still zero, because the table in the middle was never
    // filled and nothing between the two ends could tell.
    const viewer = await signInAs(h, org, 'admin')
    const res = await callProcedure(h, viewer, 'environments.costs', 'query', { hours: 24 })
    assert.equal(res.status, 200)

    const data = (res.body as {
      result: {
        data: {
          plan: string
          usedHours: number
          remainingDayHours: number
          caps: { perDayHours: number }
          environments: Array<{ repository: string; branch: string; hours: number }>
        }
      }
    }).result.data

    assert.notEqual(data.usedHours, 0, 'environments.costs still answers zero for every caller')
    assert.equal(data.usedHours, 8)
    assert.equal(data.remainingDayHours, data.caps.perDayHours - 8)
    assert.ok(data.environments.length >= 2, 'a total with no lines is a number nobody can act on')
    assert.equal(data.environments[0]!.branch, 'finished')
    assert.equal(data.environments[0]!.repository, org.repository)
  })

  it('bills the build even when the creating event never arrived, through the same procedure', async () => {
    // The failure this guards is a number that is wrong and looks right. If
    // created_at came from whichever event arrived first, an environment whose
    // creating event was dropped from the spool would bill from AFTER its
    // build, and a cold build is the expensive part of a run.
    const lost = await seedOrg(h.admin, 'costs-lost-creating')
    try {
      await h.admin`DELETE FROM environments WHERE org_id = ${lost.orgId}`
      const lostToken = `aft_${randomUUID().replace(/-/g, '')}`
      await h.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
        VALUES (${lost.orgId}, 'ci',
                ${createHash('sha256').update(lostToken).digest()}, 'aft_lost')`

      const now = h.clock.now()
      const cameUp = new Date(now.getTime() - 5 * HOUR)
      // Half an hour of build before it was ready, and no creating event ever
      // sent. The bill has to be five hours, not four and a half.
      const readyAt = new Date(cameUp.getTime() + 0.5 * HOUR)

      const res = await h.fetch('/v1/events', {
        method: 'POST',
        headers: { authorization: `Bearer ${lostToken}`, 'content-type': 'application/json' },
        body: JSON.stringify({
          events: [{
            id: randomUUID(), type: 'environment.ready',
            envId: `env-lost-${randomUUID().slice(0, 8)}`, sequence: 2,
            occurredAt: readyAt.toISOString(),
            payload: {
              repository: lost.repository, branch: 'cold-build',
              ttl_seconds: 24 * 3600, started_at: cameUp.toISOString(), seconds: 1800,
            },
          }],
        }),
      })
      assert.equal(((await res.json()) as { accepted: number }).accepted, 1)

      const used = await h.pool.withTenant({ orgId: lost.orgId }, (db) =>
        environmentHoursSince(db, lost.orgId, new Date(now.getTime() - 24 * HOUR), now))
      assert.equal(Math.round(used * 100) / 100, 5,
        `the build was not billed: the sum says ${used} where it should say 5`)
    } finally {
      await dropOrg(h.admin, lost.orgId)
    }
  })
})
