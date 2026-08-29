// Readiness, and the reason it is not /health.
//
// The first deploy of this application to Azure answered GET /health with 200
// while every endpoint that touched a table returned 500. The managed Postgres
// had refused CREATE EXTENSION pgcrypto, so migration 0001 rolled back and the
// database had no schema; the process was running perfectly and could not
// serve a single request. Nothing being monitored could tell.
//
// /health is still a static literal and should stay one: it answers "is this
// process alive" for a liveness probe, and a liveness probe that restarts a
// container because the database is slow turns a database blip into an outage.
//
// /readyz answers the other question, and these tests hold it to the standard
// that matters: it has to be ABLE to fail. The unhappy path is asserted first
// because a readiness check that cannot return anything but 200 is the bug this
// endpoint exists to fix, wearing a different name.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from '../src/server.ts'
import { FakeClock } from '../src/clock.ts'
import { FakeGitHub } from '../src/auth/fakegithub.ts'
import { available, startApi, type ApiHarness } from './harness.ts'
import type { Pool } from '@antifailure/db'

/**
 * A pool whose every transaction rejects.
 *
 * This is what an unreachable database, a refused password and a database with
 * no schema all look like from inside the application, and all three have
 * happened to this deployment.
 */
function brokenPool(message: string): Pool {
  const fail = async (): Promise<never> => {
    throw new Error(message)
  }
  return {
    withTenant: fail,
    withoutTenant: fail,
    sql: null as never,
    close: async () => {},
  } as unknown as Pool
}

describe('readiness when the database will not answer', () => {
  // No real Postgres needed: the whole point is that the database is broken.
  const clock = new FakeClock()
  const { app } = createServer({
    pool: brokenPool('password authentication failed for user "af_app"'),
    github: new FakeGitHub(clock),
    clock,
    secureCookies: false,
  })
  const get = (path: string) => app.fetch(new Request(`http://api.test${path}`))

  test('answers 503, not 200 and not 500', async () => {
    const res = await get('/readyz')
    // 503 specifically. 500 reads as "this endpoint is broken" and gets
    // retried; 503 is "not ready for traffic", which is what a load balancer
    // and the deploy gate in cd.yml both act on.
    assert.equal(res.status, 503)
  })

  test('names the cause, because the operator has no other way to see it', async () => {
    const body = (await (await get('/readyz')).json()) as { ready: boolean; reason?: string }
    assert.equal(body.ready, false)
    assert.match(String(body.reason), /password authentication failed/)
  })

  test('reports the build even when it is not ready', async () => {
    // A rollback decision needs to know which build is answering. A 503 with no
    // build attached cannot distinguish "the new revision is bad" from "the old
    // one never went away".
    const body = (await (await get('/readyz')).json()) as { version: string; commit: string }
    assert.equal(typeof body.version, 'string')
    assert.equal(typeof body.commit, 'string')
  })

  test('liveness still answers 200, because the process is alive', async () => {
    // The distinction, asserted. If this ever starts failing with the database,
    // a slow Postgres becomes a restart loop.
    const res = await get('/health')
    assert.equal(res.status, 200)
    assert.deepEqual(await res.json(), { ok: true })
  })
})

describe('readiness against a real database', { skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let api: ApiHarness

  before(async () => {
    api = await startApi()
  })
  after(async () => {
    await api.close()
  })

  test('answers 200 when the database is reachable', async () => {
    // The negative control for the suite above. Without it, an endpoint that
    // returned 503 unconditionally would pass every test in this file.
    const res = await api.fetch('/readyz')
    assert.equal(res.status, 200)
    const body = (await res.json()) as { ready: boolean }
    assert.equal(body.ready, true)
  })
})
