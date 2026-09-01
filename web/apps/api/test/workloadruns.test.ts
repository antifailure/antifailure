// The orderings a workload run goes through, one test per cell.
//
// Testing that the system lands in the right state for the happy ordering is
// not testing the system. Real callers hit the other orderings within days, and
// the ones here are not hypothetical: an engine's events go through a sink that
// batches for five seconds and spools to disk across process boundaries, so a
// finish genuinely can be ingested before the start it followed, and a report
// genuinely can arrive after the control plane has given up waiting.
//
// THE TABLE. Every cell has a test below and the test is named for the cell.
//
//   requested then callback        the ordinary case
//   callback then run row          a report naming a run this control plane
//                                  does not have
//   callback with no row           the same, from another organization
//   row with no callback           the deadline ends it as abandoned
//   duplicate callback             the second changes nothing
//   concurrent starts              the database decides, not a read
//   the same request twice         one run, not two
//   cancel during execution        a durable command, and the run waits
//   cancel before anything claimed it   settled here and now
//   cancel after completion        refused, and nothing is changed
//   cancel racing completion       the completion wins and the command is
//                                  superseded rather than failed
//   retry of a superseded run      refused, naming the successor
//   retry of a run still going     refused
//   timeout with no terminal event abandoned, and a later report is a note
//
// And two that are not orderings but are the same class of defect: a malformed
// element inside a report must not discard the report, and a to-one that
// arrives as a to-many must not be read as a list.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import { RUN_DEADLINE_MS, RUN_LEASE_MS } from '../src/workloads/store.ts'
import {
  available, startApi, seedOrg, signInAs, callProcedure, errorCode, dropOrg,
  type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

type Answer = { status: number; body: any }

describe('a workload run', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let other: Org
  let owner: SignedIn
  let token: string
  /** A second engine in the SAME organization, which is what a lease takeover
   *  needs: two engines polling one environment. `otherToken` belongs to
   *  another organization and can never see these runs at all. */
  let secondToken: string
  let otherToken: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'runs')
    other = await seedOrg(h.admin, 'runs-other')
    owner = await signInAs(h, org, 'owner')

    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${Math.floor(Math.random() * 1e9)}, ${org.slug}, 'Organization')`
    h.github.addWorkflow(org.repository, 'antifailure.yml')

    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_run')`
    secondToken = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci-2', ${createHash('sha256').update(secondToken).digest()}, 'aft_2nd')`
    otherToken = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${other.orgId}, 'ci', ${createHash('sha256').update(otherToken).digest()}, 'aft_oth')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await dropOrg(h.admin, other.orgId)
    await h.close()
  })

  // -------------------------------------------------------------------------
  // Fixtures
  // -------------------------------------------------------------------------

  /**
   * A workload of one kind, with one version, and an environment of its own.
   *
   * An environment per test rather than one shared one, and it is not tidiness.
   * An engine claims the oldest run waiting for an ENVIRONMENT, so a test that
   * shared one with the test before it would claim that test's leftover run and
   * assert against the wrong row. That is an ordering bug in the suite, and the
   * suite is about orderings, so it has to be right here first.
   */
  async function define(kind: string, body: unknown): Promise<{ slug: string; envId: string }> {
    const slug = `r-${randomUUID().slice(0, 8)}`
    const made: Answer = await callProcedure(h, owner, 'workloads.create', 'mutation', {
      repository: org.repository,
      slug,
      name: slug,
      kind,
      body,
    })
    assert.equal(made.status, 200, JSON.stringify(made.body))

    const envId = `env-${slug}`
    await h.admin`
      INSERT INTO environments (org_id, repository_id, env_id, branch, state)
      VALUES (${org.orgId}, ${org.repoId}, ${envId}, 'main', 'running')`
    return { slug, envId }
  }

  async function start(
    target: { slug: string; envId: string },
    over: Record<string, unknown> = {},
  ): Promise<Answer> {
    return callProcedure(h, owner, 'workloads.start', 'mutation', {
      slug: target.slug, envId: target.envId, ...over,
    })
  }

  /** Sends events the way an engine does: over HTTP, with a bearer token. */
  async function send(events: unknown[], bearer = token): Promise<{ status: number; body: any }> {
    const res = await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${bearer}`, 'content-type': 'application/json' },
      body: JSON.stringify({ events }),
    })
    return { status: res.status, body: await res.json() }
  }

  function event(over: Record<string, unknown>): Record<string, unknown> {
    return {
      id: randomUUID(),
      sequence: 1,
      occurredAt: h.clock.now().toISOString(),
      ...over,
    }
  }

  async function stateOf(runId: string): Promise<{ state: string; verdict: string | null; detail: string | null }> {
    const rows = await h.admin<{ state: string; verdict: string | null; detail: string | null }[]>`
      SELECT state::text AS state, verdict::text AS verdict, detail
      FROM workload_runs WHERE id = ${runId}`
    return rows[0]!
  }

  /** What the row records about the lease, which is what tells a run somebody
   *  took from a run whose engine died. */
  async function leaseOf(runId: string): Promise<{
    holder: string | null; takeovers: number; lostAt: Date | null
    unheld: number; unheldAt: Date | null
  }> {
    const rows = await h.admin<{
      lease_holder: string | null; lease_takeovers: number; lease_lost_at: Date | null
      unheld_reports: number; unheld_report_at: Date | null
    }[]>`
      SELECT lease_holder, lease_takeovers, lease_lost_at, unheld_reports, unheld_report_at
      FROM workload_runs WHERE id = ${runId}`
    const r = rows[0]!
    return {
      holder: r.lease_holder, takeovers: Number(r.lease_takeovers), lostAt: r.lease_lost_at,
      unheld: Number(r.unheld_reports), unheldAt: r.unheld_report_at,
    }
  }

  /** Claims a run the way an engine on a runner does. */
  async function claim(envId: string, bearer = token): Promise<{ status: number; body: any }> {
    const res = await h.fetch('/v1/workloads/claim', {
      method: 'POST',
      headers: { authorization: `Bearer ${bearer}`, 'content-type': 'application/json' },
      body: JSON.stringify({ envId }),
    })
    return { status: res.status, body: await res.json() }
  }

  // -------------------------------------------------------------------------
  // Starting
  // -------------------------------------------------------------------------

  it('starts a run, records it, and dispatches the flags the command has', async () => {
    const target = await define('observed_load', { durationSeconds: 90, scale: 0.5 })
    const before = h.github.dispatches.length
    const started = await start(target)
    assert.equal(started.status, 200, JSON.stringify(started.body))
    assert.equal(started.body.result.data.dispatched, true)

    const dispatch = h.github.dispatches[before]!
    assert.equal(dispatch.repository, org.repository)
    assert.equal(dispatch.ref, 'main')
    assert.deepEqual(dispatch.inputs, {
      command: 'load', workflows: '', duration: '90s', scale: '0.5',
    })

    const state = await stateOf(started.body.result.data.runId)
    // Requested, not running. What exists after this call is a queued Actions
    // run and a row saying somebody asked for one.
    assert.equal(state.state, 'requested')
  })

  it('ordering: the same request twice is one run, not two', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const key = `key-${randomUUID().slice(0, 8)}`
    const first = await start(target, { requestKey: key })
    const second = await start(target, { requestKey: key })
    assert.equal(first.body.result.data.runId, second.body.result.data.runId)
    assert.equal(second.body.result.data.dispatched, false)
    assert.match(second.body.result.data.note, /had already been made/)
  })

  it('ordering: concurrent starts on one definition, and the database decides', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    // Two genuinely different requests fired together. A read followed by a
    // decision would let both through: both would see no live run.
    const [a, b] = await Promise.all([start(target), start(target)])
    const outcomes = [a, b].map((r) => (r.status === 200 ? 'started' : errorCode(r.body)))
    assert.deepEqual(
      outcomes.slice().sort(),
      ['PRECONDITION_FAILED', 'started'],
      `expected exactly one to win, got ${JSON.stringify(outcomes)}: ${JSON.stringify([a.body, b.body]).slice(0, 900)}`,
    )
    const refused = a.status === 200 ? b : a
    assert.match(JSON.stringify(refused.body), /already .* against that environment/)

    const live = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM workload_runs wr
      JOIN workloads w ON w.id = wr.workload_id
      WHERE w.slug = ${target.slug} AND wr.state IN ('requested', 'accepted', 'running')`
    assert.equal(Number(live[0]!.n), 1)
  })

  it('refuses a workload run against an environment of another repository', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const [repo] = await h.admin<{ id: string }[]>`
      INSERT INTO repositories (org_id, full_name) VALUES (${org.orgId}, ${`${org.slug}/other`})
      RETURNING id`
    const strangerEnv = `env-other-${randomUUID().slice(0, 6)}`
    await h.admin`
      INSERT INTO environments (org_id, repository_id, env_id, branch, state)
      VALUES (${org.orgId}, ${repo!.id}, ${strangerEnv}, 'main', 'running')`
    const refused = await start(target, { envId: strangerEnv })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(refused.body), /measures\\nnothing|measures nothing/)
  })

  // -------------------------------------------------------------------------
  // The engine picking work up
  // -------------------------------------------------------------------------

  it('hands the waiting run to an engine that asks, with its compiled version', async () => {
    const target = await define('http_scenario', { select: ['checkout'], seed: 3 })
    const started = await start(target).catch(() => null)
    // A scenario needs the newer workflow, so the dispatch is refused by the
    // fake and the route says so. The run is still recorded, which is the whole
    // point: it is claimable by an engine somebody starts by hand.
    const runs = await h.admin<{ id: string }[]>`
      SELECT wr.id FROM workload_runs wr JOIN workloads w ON w.id = wr.workload_id
      WHERE w.slug = ${target.slug}`
    assert.equal(runs.length, 1, 'the run was not recorded when its dispatch was refused')
    void started

    const claimed = await claim(target.envId)
    assert.equal(claimed.status, 200)
    assert.equal(claimed.body.run.runId, runs[0]!.id)
    assert.equal(claimed.body.run.kind, 'http_scenario')
    assert.deepEqual(claimed.body.run.body, { select: ['checkout'], seed: 3 })
    assert.equal((await stateOf(runs[0]!.id)).state, 'accepted')

    // And the second claim finds nothing waiting, rather than handing the same
    // run to a second engine.
    const again = await claim(target.envId)
    assert.equal(again.body.run, null)
  })

  it('answers 200 with a null run when nothing is waiting, not 204', async () => {
    // A poller that reads a status code and not a body cannot tell "nothing
    // waiting" from "something went wrong with the shape".
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const empty = await claim(target.envId)
    assert.equal(empty.status, 200)
    assert.equal(empty.body.run, null)
  })

  it('will not hand one organization a run belonging to another', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    await start(target)
    const stranger = await claim(target.envId, otherToken)
    // The same answer as an environment that does not exist, because telling
    // them apart is a way to ask what another organization has.
    assert.equal(stranger.status, 404)
  })

  // -------------------------------------------------------------------------
  // The orderings a report can arrive in
  // -------------------------------------------------------------------------

  it('ordering: requested, then started, then finished', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId

    const startedAt = await send([
      event({ type: 'workload.started', sequence: 5, payload: { workload_run_id: runId } }),
    ])
    assert.equal(startedAt.status, 202)
    assert.equal((await stateOf(runId)).state, 'running')

    const finished = await send([
      event({
        type: 'workload.finished',
        sequence: 9,
        payload: {
          workload_run_id: runId,
          kind: 'observed_load',
          outcome: 'succeeded',
          verdict: 'pass',
          result: {
            requests: 1200, failures: 3, error_rate: 0.0025, achieved_rate: 40, target_rate: 40,
            duration_ms: 30000, source: 'otlp',
            p50_ms: 12, p90_ms: 40, p95_ms: 61, p99_ms: 120, max_ms: 900,
          },
          routes: [
            { route: 'GET /checkout', sent: 600, errors: 1, latency: { p95_ms: 61 }, baseline_p95_ms: 40, p95_increase: 1.5, has_baseline: true },
            { route: 'GET /', sent: 600, errors: 2, latency: { p95_ms: 20 } },
          ],
          thresholds: [{ name: 'checkout stays quick', measure: 'p95_below_ms', threshold: 200, observed: 61, verdict: 'pass' }],
          evidence: [{ kind: 'report', locator: '/home/runner/report.md' }],
        },
      }),
    ])
    assert.equal(finished.status, 202)

    const state = await stateOf(runId)
    assert.equal(state.state, 'succeeded')
    // State says the work happened; verdict says what it found. Two columns,
    // deliberately, because collapsing them is how an exit code over work that
    // never happened reads as a pass.
    assert.equal(state.verdict, 'pass')

    const inspected: Answer = await callProcedure(h, owner, 'workloads.inspect', 'query', { runId })
    const data = inspected.body.result.data
    assert.equal(Number(data.result.requests), 1200)
    assert.equal(data.routes.length, 2)
    // No baseline and no change are different answers, and the second route had
    // nothing to compare with.
    assert.equal(data.routes[1].baseline_p95_ms, null)
    assert.equal(data.routes[1].p95_increase, null)
    assert.equal(data.thresholds.length, 1)
    // Nothing uploaded it, so it is not claimed to be fetchable.
    assert.equal(data.evidence[0].availability, 'runner_local')
    // A to-one is an object or null, and a to-many is an array.
    assert.ok(!Array.isArray(data.result))
    assert.ok(Array.isArray(data.routes))
  })

  it('ordering: finished before started, and the start does not move it back', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId

    await send([
      event({
        type: 'workload.finished',
        sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'browser_workflow', verdict: 'fail',
          result: { workflows: 3, workflows_passed: 2, workflows_failed: 1, duration_ms: 4000 },
        },
      }),
    ])
    assert.equal((await stateOf(runId)).state, 'succeeded')

    const late = await send([
      event({ type: 'workload.started', sequence: 5, payload: { workload_run_id: runId } }),
    ])
    assert.equal(late.status, 202)
    assert.match(late.body.outcomes[0].note, /already succeeded/)
    assert.equal((await stateOf(runId)).state, 'succeeded')
  })

  it('ordering: a duplicate finish changes nothing and writes no second result', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    const report = {
      workload_run_id: runId, kind: 'browser_workflow', verdict: 'pass',
      result: { workflows: 1, duration_ms: 1000 },
      routes: [{ route: 'GET /', sent: 1 }],
    }

    await send([event({ type: 'workload.finished', sequence: 9, payload: report })])
    // A different event identifier carrying the same report, which is what a
    // regenerated resend looks like: the events table cannot deduplicate it.
    const second = await send([event({ type: 'workload.finished', sequence: 10, payload: report })])
    assert.equal(second.body.outcomes[0].status, 'accepted')
    assert.match(second.body.outcomes[0].note, /already succeeded/)

    const results = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(Number(results[0]!.n), 1)
    const routes = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM workload_route_metrics WHERE workload_run_id = ${runId}`
    assert.equal(Number(routes[0]!.n), 1)
  })

  it('ordering: a report for a run this control plane does not have is stored and said out loud', async () => {
    const answer = await send([
      event({
        type: 'workload.finished',
        payload: { workload_run_id: randomUUID(), kind: 'observed_load', result: { requests: 1 } },
      }),
    ])
    assert.equal(answer.status, 202)
    assert.equal(answer.body.accepted, 1)
    assert.equal(answer.body.unprojected, 1)
    assert.match(answer.body.outcomes[0].note, /not a workload run this control plane has/)
  })

  it('ordering: a report from the wrong organization reaches nothing', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    // Sent with the OTHER organization's token. Row-level security makes the
    // run invisible, so it gets the same answer as a run that does not exist.
    const answer = await send(
      [
        {
          id: randomUUID(),
          type: 'workload.finished',
          envId: other.envId,
          sequence: 1,
          occurredAt: h.clock.now().toISOString(),
          payload: { workload_run_id: runId, kind: 'browser_workflow', result: { workflows: 1 } },
        },
      ],
      otherToken,
    )
    assert.equal(answer.body.unprojected, 1)
    assert.equal((await stateOf(runId)).state, 'requested')
  })

  it('refuses a report that says it is a different kind from the run it names', async () => {
    // Not coerced. Writing the database's kind with the payload's numbers would
    // produce a row that satisfies every constraint and means something else.
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    const answer = await send([
      event({
        type: 'workload.finished',
        payload: { workload_run_id: runId, kind: 'browser_workflow', result: { workflows: 1 } },
      }),
    ])
    assert.match(answer.body.outcomes[0].note, /says it is a browser_workflow result/)
    assert.equal((await stateOf(runId)).state, 'requested')
  })

  it('one bad route measurement does not discard the report', async () => {
    // The failure this guards is a decoder that refuses a whole collection over
    // one surprising element, which reads as "the run measured nothing" rather
    // than as "the decoder refused".
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    const answer = await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId,
          kind: 'observed_load',
          verdict: 'pass',
          result: { requests: 10, p95_ms: 5 },
          routes: [
            { route: 'GET /ok', sent: 5 },
            { route: 42, sent: 'lots' },
            { sent: 5 },
            { route: 'GET /also-ok', sent: 5 },
          ],
          // A to-one where a to-many belongs. Counted as one skip and said in
          // the note rather than silently reported as no thresholds at all.
          thresholds: { name: 'not a list' },
        },
      }),
    ])
    assert.equal(answer.body.outcomes[0].status, 'accepted')
    assert.match(answer.body.outcomes[0].note, /2 route measurements, 1 thresholds/)

    const routes = await h.admin<{ route: string }[]>`
      SELECT route FROM workload_route_metrics WHERE workload_run_id = ${runId} ORDER BY position`
    assert.deepEqual(routes.map((r) => r.route), ['GET /ok', 'GET /also-ok'])
    const result = await h.admin<{ requests: number }[]>`
      SELECT requests FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(Number(result[0]!.requests), 10)
  })

  it('will not record evidence as uploaded when nothing can verify it', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'browser_workflow', result: { workflows: 1 },
          evidence: [
            { kind: 'trace', availability: 'uploaded', locator: 's3://b/t.zip' },
            { kind: 'video', availability: 'uploaded', locator: 's3://b/v.webm', sha256: 'a'.repeat(64) },
            { kind: 'screenshot', locator: '/home/runner/s.png' },
          ],
        },
      }),
    ])
    const evidence = await h.admin<{ kind: string; availability: string }[]>`
      SELECT kind, availability::text AS availability FROM workload_evidence
      WHERE workload_run_id = ${runId} ORDER BY kind`
    // Mapped to plain objects, because the driver returns a Result array with
    // its own properties and a deep comparison against a literal fails on those
    // rather than on the rows.
    assert.deepEqual(
      evidence.map((e) => ({ kind: e.kind, availability: e.availability })),
      [
        { kind: 'screenshot', availability: 'runner_local' },
        { kind: 'video', availability: 'uploaded' },
      ],
    )
  })

  // -------------------------------------------------------------------------
  // The deadline
  // -------------------------------------------------------------------------

  it('keeps two scenarios that sent the same route, and their own assertions', async () => {
    // Measured on a real two scenario run: both send GET /health, and two p95
    // values do not average into a p95, so a key of (run, route) would refuse
    // the second row and the console would show one scenario's numbers under
    // both names. Same for an assertion both scenarios call nothing_failed.
    const target = await define('http_scenario', { select: ['browse', 'checkout'] })
    await start(target).catch(() => null)
    const runId = (await h.admin<{ id: string }[]>`
      SELECT wr.id FROM workload_runs wr JOIN workloads w ON w.id = wr.workload_id
      WHERE w.slug = ${target.slug}`)[0]!.id

    await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'http_scenario', verdict: 'pass',
          result: { requests: 8, sessions: 2, p95_ms: 20 },
          routes: [
            { scenario: 'browse', route: 'GET /health', sent: 4, latency: { p95_ms: 12 } },
            { scenario: 'checkout', route: 'GET /health', sent: 4, latency: { p95_ms: 31 } },
          ],
          thresholds: [
            { scenario: 'browse', name: 'nothing_failed', measure: 'every_request_succeeded', verdict: 'pass' },
            { scenario: 'checkout', name: 'nothing_failed', measure: 'every_request_succeeded', verdict: 'fail' },
          ],
        },
      }),
    ])

    const routes = await h.admin<{ scenario: string; p95_ms: number }[]>`
      SELECT scenario, p95_ms FROM workload_route_metrics
      WHERE workload_run_id = ${runId} ORDER BY scenario`
    assert.deepEqual(
      routes.map((r) => [r.scenario, Number(r.p95_ms)]),
      [['browse', 12], ['checkout', 31]],
    )
    const asserted = await h.admin<{ scenario: string; value: string }[]>`
      SELECT scenario, value::text AS value FROM workload_threshold_verdicts
      WHERE workload_run_id = ${runId} ORDER BY scenario`
    assert.deepEqual(
      asserted.map((a) => [a.scenario, a.value]),
      [['browse', 'pass'], ['checkout', 'fail']],
    )
  })

  it('records all five workflow counts, so a run that did nothing is not a run with no failures', async () => {
    // A real `af test` against examples/go-api returned 0 passed, 0 failed, 0
    // flaky, 0 blocked and 1 unverified, because the persona could not be
    // created. With passed and failed alone that renders as a clean run.
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'browser_workflow', verdict: 'unverified',
          result: {
            workflows: 1, workflows_passed: 0, workflows_failed: 0,
            workflows_flaky: 0, workflows_blocked: 0, workflows_unverified: 1,
          },
          reproduce: { command: 'af test --only sign-up', manifest_digest: 'a'.repeat(64) },
        },
      }),
    ])
    const [row] = await h.admin<Record<string, string>[]>`
      SELECT workflows, workflows_passed, workflows_failed, workflows_flaky,
             workflows_blocked, workflows_unverified
      FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.deepEqual(
      [row!.workflows, row!.workflows_passed, row!.workflows_failed,
       row!.workflows_flaky, row!.workflows_blocked, row!.workflows_unverified].map(Number),
      [1, 0, 0, 0, 0, 1],
    )
    assert.equal((await stateOf(runId)).verdict, 'unverified')

    // The command the engine reported, stored rather than rebuilt.
    const inspected: Answer = await callProcedure(h, owner, 'workloads.inspect', 'query', { runId })
    assert.equal(inspected.body.result.data.run.reproduce_command, 'af test --only sign-up')
    assert.equal(inspected.body.result.data.run.manifest_digest, 'a'.repeat(64))
  })

  it('keeps failures by reason rather than a total, and says nothing when nobody said', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'observed_load', verdict: 'fail',
          result: {
            requests: 10, failures: 3, error_rate: 0.3, p95_ms: 40,
            errors: { timeout: 2, '503': 1, 42: 'not a count' },
          },
        },
      }),
    ])
    const [row] = await h.admin<{ error_reasons: Record<string, number>; p50_ms: number | null }[]>`
      SELECT error_reasons, p50_ms FROM workload_run_results WHERE workload_run_id = ${runId}`
    // A count alone loses the only part that tells somebody what to fix.
    assert.deepEqual(row!.error_reasons, { timeout: 2, '503': 1 })
    // And a percentile nobody reported stays null rather than becoming a zero
    // that nothing downstream can tell from a real zero.
    assert.equal(row!.p50_ms, null)
  })

  it('records a run that hit its own timeout as timed_out, not as succeeded', async () => {
    // The enum had a value nothing could reach. Reading only `outcome` recorded
    // a run that passed its own --timeout as SUCCEEDED, which is a green answer
    // over work that was interrupted.
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'browser_workflow',
          state: 'timed_out', outcome: 'failed', verdict: 'unverified',
          result: { workflows: 1, workflows_unverified: 1 },
        },
      }),
    ])
    assert.equal((await stateOf(runId)).state, 'timed_out')
  })

  it('tells an engine on its heartbeat that a cancel is waiting', async () => {
    // So a cancel arrives on a request the engine was making anyway, rather
    // than a minute later on a poll that takes a lease on unrelated commands.
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId)

    const beat = async () => {
      const res = await h.fetch(`/v1/workloads/runs/${runId}/heartbeat`, {
        method: 'POST',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        body: '{}',
      })
      return res.json() as Promise<{ held: boolean; cancelRequested: boolean }>
    }
    assert.deepEqual(await beat(), { held: true, cancelRequested: false })
    await callProcedure(h, owner, 'workloads.cancel', 'mutation', { runId })
    assert.deepEqual(await beat(), { held: true, cancelRequested: true })
  })

  it('reads the projected result document, not the engine internal load type', async () => {
    // The seam neither suite crossed. The decoder read `sent`, `rate` and a
    // nested `overall`, which are internal/load.Result's names, and the wire
    // carries the projected document: `requests`, `achieved_rate` and flat
    // percentiles. It failed silently, because requests falls back to zero for
    // the CHECK, so a run that sent 1200 requests was stored as one that sent
    // none while every route row decoded perfectly.
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'observed_load', verdict: 'pass',
          result: {
            requests: 1200, failures: 3, error_rate: 0.0025,
            target_rate: 40, achieved_rate: 38.5,
            p50_ms: 12, p90_ms: 40, p95_ms: 61, p99_ms: 120, max_ms: 900,
            duration_ms: 30000, source: 'otlp',
          },
        },
      }),
    ])
    const [row] = await h.admin<Record<string, string | null>[]>`
      SELECT requests, achieved_rate, target_rate, p50_ms, p95_ms, max_ms
      FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(Number(row!.requests), 1200, 'the projected request count was read as zero')
    assert.equal(Number(row!.achieved_rate), 38.5)
    assert.equal(Number(row!.target_rate), 40)
    assert.equal(Number(row!.p50_ms), 12)
    assert.equal(Number(row!.p95_ms), 61)
    assert.equal(Number(row!.max_ms), 900)
  })

  it('does NOT read the engine native spelling, and that is deliberate', async () => {
    // This replaced a test asserting the opposite, and the premise it rested on
    // is false rather than merely arguable. It said accepting `sent`, `rate` and
    // a nested `overall` protects an older engine from being silently zeroed.
    // There is no such engine. No engine ever emitted `workload.finished` at
    // all before the release that introduced the result document, which is the
    // gap the emitters work closed, and `hostedPayload` deletes `native` from
    // the payload before it goes out, so the native spelling has never once
    // been on this wire and cannot be.
    //
    // Tolerance is right for a VALUE arriving in an unexpected form: `num`
    // takes a number written as a string, because that is a real thing a JSON
    // encoder in another language does. It is wrong for a NAME, because the
    // name is the contract. A decoder that reads both keeps working while the
    // two ends disagree, which is exactly how a run that sent twelve hundred
    // requests recorded as having sent none for as long as it did.
    //
    // And it is checkable rather than a matter of taste:
    // engine/internal/controlplane/report_shape_test.go reads this decoder's
    // source and fails on any name the engine does not send. Accepting both
    // leaves that gate RED after a merge, naming the three.
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    const answer = await send([
      event({
        type: 'workload.finished',
        payload: {
          workload_run_id: runId, kind: 'observed_load', verdict: 'pass',
          result: { sent: 77, rate: 9.5, overall: { p95_ms: 33 } },
        },
      }),
    ])
    assert.equal(answer.status, 202)

    const [row] = await h.admin<Record<string, string | null>[]>`
      SELECT requests, achieved_rate, p95_ms FROM workload_run_results
      WHERE workload_run_id = ${runId}`
    // Zero rather than 77, and null rather than the two numbers. The run is
    // still recorded, because refusing the row would lose the rest of the
    // report with it, and the zero is the CHECK's requirement showing through.
    assert.equal(Number(row!.requests), 0)
    assert.equal(row!.achieved_rate, null)
    assert.equal(row!.p95_ms, null)
  })

  it('ordering: a run nobody ever reports on is abandoned, not failed', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId

    h.clock.advance(RUN_DEADLINE_MS + 1000)
    // Resolved by the next thing this organization does, which is the shape
    // that works: a sweep with no tenant set matches nothing, because every
    // policy on this table keys on current_org().
    const listed: Answer = await callProcedure(h, owner, 'workloads.runs', 'query', { limit: 5 })
    assert.equal(listed.status, 200)

    const state = await stateOf(runId)
    assert.equal(state.state, 'abandoned')
    // Abandoned says the control plane never heard. Failed would say the engine
    // reported a failure, and those are different things to act on.
    assert.match(state.detail!, /may have run/)
  })

  it('ordering: a report that arrives after the deadline is a note, not a resurrection', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    h.clock.advance(RUN_DEADLINE_MS + 1000)
    await callProcedure(h, owner, 'workloads.runs', 'query', { limit: 5 })
    assert.equal((await stateOf(runId)).state, 'abandoned')

    const late = await send([
      event({
        type: 'workload.finished',
        sequence: 9,
        payload: { workload_run_id: runId, kind: 'browser_workflow', verdict: 'pass', result: { workflows: 1 } },
      }),
    ])
    assert.match(late.body.outcomes[0].note, /already abandoned/)
    assert.equal((await stateOf(runId)).state, 'abandoned')
    const results = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(Number(results[0]!.n), 0, 'a report about an abandoned run wrote a result anyway')
  })

  it('a heartbeat keeps a run alive, and a stale lease does not', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    const claimed = await claim(target.envId)
    assert.equal(claimed.body.run.runId, runId)

    // Most of the way to the deadline, then a heartbeat, then the rest.
    h.clock.advance(RUN_DEADLINE_MS - 60_000)
    const beat = await h.fetch(`/v1/workloads/runs/${runId}/heartbeat`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: '{}',
    })
    assert.equal(beat.status, 200)

    h.clock.advance(120_000)
    await callProcedure(h, owner, 'workloads.runs', 'query', { limit: 5 })
    assert.equal((await stateOf(runId)).state, 'accepted', 'a heartbeat did not push the deadline')
  })

  it('tells an engine that lost its lease, rather than letting it work on', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    const stranger = await h.fetch(`/v1/workloads/runs/${runId}/heartbeat`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: '{}',
    })
    // Never claimed, so this token does not hold it. 409 and a sentence, so the
    // engine stops rather than reporting at the end into nothing.
    assert.equal(stranger.status, 409)
    assert.match(JSON.stringify(await stranger.json()), /not held by this token/)
  })

  // -------------------------------------------------------------------------
  // A lost lease, one test per ordering
  //
  // A lease is fifteen minutes and a heartbeat extends it. Miss enough of them
  // and a SECOND engine polling the same environment claims the run and starts
  // doing the work. The statement that ends a run used to be gated on the run's
  // STATE alone, so the FIRST engine's terminal event ended it, and the second
  // engine's report then arrived against a terminal row and was refused as a
  // note. The measurements of the engine that actually did the work were
  // destroyed by the engine that had lost it.
  //
  // The engine side closed the near end: an engine told 409 by its heartbeat
  // stops reporting. This is the far end, which the engine cannot reach, because
  // an engine that never got an ANSWER to a heartbeat never learns it lost
  // anything. Every cell below is an arrival order, not a variation on one.
  //
  //   second claims, then first reports  the first is refused; the second's
  //                                      report still lands, whole
  //   lease expires, nobody reclaims     the original engine still reports. An
  //                                      expired lease is not a lost one
  //   terminal, then the lease expires   already terminal; nothing to take
  //   both engines report                exactly one ends it, and it is the
  //                                      holder, in either arrival order
  //   neither reports                    abandoned, saying it changed hands
  //   nobody ever claimed it             any token may end it, and a run nobody
  //                                      claimed says so when it is abandoned
  // -------------------------------------------------------------------------

  it('ordering: a second engine claims, then the first reports, and the second is not lost', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId

    const first = await claim(target.envId, token)
    assert.equal(first.body.run.runId, runId)

    // The first engine goes quiet for longer than a lease. Nothing here says it
    // died: a runner behind a flaky network misses heartbeats and keeps working.
    h.clock.advance(RUN_LEASE_MS + 60_000)
    const second = await claim(target.envId, secondToken)
    assert.equal(second.body.run?.runId, runId, 'the second engine did not take the expired lease')

    const took = await leaseOf(runId)
    assert.equal(
      took.takeovers, 1,
      'the lease moved to a different engine and nothing recorded it. An abandoned run that ' +
      'changed hands then reads exactly like one whose only engine died.',
    )
    assert.ok(took.lostAt, 'a takeover was counted with no time on it')

    // Now the first engine finishes and says so. It has NOT been told it lost
    // anything, because it never got an answer to a heartbeat.
    const refused = await send([
      event({
        type: 'workload.cancelled', sequence: 4,
        payload: {
          workload_run_id: runId, kind: 'observed_load', outcome: 'failed',
          detail: 'the first engine stopped', result: { requests: 1 },
        },
      }),
    ], token)
    assert.equal(refused.status, 202)

    // The state FIRST, and the note after, because this assertion is the whole
    // defect and the note is a courtesy. A test that checked the note first
    // would die on an undefined string and say so instead of saying that a run
    // somebody else is running was just ended.
    const still = await stateOf(runId)
    assert.equal(
      still.state, 'accepted',
      'AN ENGINE THAT NO LONGER HOLDS THIS RUN JUST ENDED IT. The engine that took the lease ' +
      'may be running the work right now, and its report will arrive against a terminal row and ' +
      'be refused as a note. Its measurements are gone. This is the ordering the lease check on ' +
      'the terminal statement exists to refuse.',
    )
    assert.equal(still.detail, null, 'a refused report wrote its detail onto the run anyway')

    // Stored and refused, not rejected. The event is kept whole; what it may not
    // do is end a run somebody else is running.
    assert.equal(refused.body.outcomes[0].status, 'accepted')
    assert.match(
      String(refused.body.outcomes[0].note),
      /held by another engine/,
      `the refusal was not explained to the sender: ${JSON.stringify(refused.body.outcomes[0])}`,
    )

    const counted = await leaseOf(runId)
    assert.equal(
      counted.unheld, 1,
      'a terminal event from an engine that does not hold the run was refused and NOT counted. ' +
      'Without the count, an abandoned run that changed hands is indistinguishable from one ' +
      'whose only engine died, which is the whole reason this column exists.',
    )
    assert.ok(counted.unheldAt, 'a refused report was counted with no time on it')

    // AND THE POINT OF ALL OF IT: the engine that actually holds the run reports,
    // and every measurement lands.
    const landed = await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'observed_load', outcome: 'succeeded', verdict: 'pass',
          result: { requests: 1200, failures: 3, achieved_rate: 40, p95_ms: 61 },
          routes: [{ route: 'GET /checkout', sent: 1200, errors: 3, p95_ms: 61 }],
        },
      }),
    ], secondToken)
    assert.equal(landed.status, 202)
    assert.equal(landed.body.outcomes[0].note, undefined, JSON.stringify(landed.body.outcomes[0]))

    const done = await stateOf(runId)
    assert.equal(done.state, 'succeeded')
    assert.equal(done.verdict, 'pass')
    const [result] = await h.admin<{ requests: number; p95_ms: number }[]>`
      SELECT requests, p95_ms FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(
      Number(result!.requests), 1200,
      'THE MEASUREMENTS OF THE ENGINE THAT ACTUALLY RAN THE WORK WERE LOST. This is the data ' +
      'loss the lease check exists to prevent: the engine that lost the run ended it, and the ' +
      'engine that had it reported into a terminal row.',
    )
    assert.equal(Number(result!.p95_ms), 61)
    const routes = await h.admin<{ route: string }[]>`
      SELECT route FROM workload_route_metrics WHERE workload_run_id = ${runId}`
    assert.equal(routes.length, 1)
    // The refusal is still on the row afterwards. It is a fact about what
    // happened to this run, not a transient.
    assert.equal((await leaseOf(runId)).unheld, 1)

    // AND A CONSOLE CAN READ ALL OF IT. Columns a console cannot reach answer
    // nothing: the whole point of these four is that somebody looking at a run
    // can tell it changed hands, and a fact that stops at the table is a fact
    // nobody has. Asserted here rather than assumed from `runColumns`.
    const inspected: Answer = await callProcedure(h, owner, 'workloads.inspect', 'query', { runId })
    assert.equal(inspected.status, 200, JSON.stringify(inspected.body))
    const seen = inspected.body.result.data.run
    assert.equal(Number(seen.lease_takeovers), 1, JSON.stringify(seen))
    assert.ok(seen.lease_lost_at, 'the console cannot see when the lease was taken')
    assert.equal(Number(seen.unheld_reports), 1, JSON.stringify(seen))
    assert.ok(seen.unheld_report_at, 'the console cannot see when the refused report arrived')
  })

  it('ordering: a lease that expired and nobody took is still the holder\'s to report on', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId, token)

    // Past the lease and nowhere near the deadline. NOBODY reclaims it.
    h.clock.advance(RUN_LEASE_MS + 60_000)

    const reported = await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'observed_load', outcome: 'succeeded', verdict: 'pass',
          result: { requests: 700, p95_ms: 12 },
        },
      }),
    ], token)
    assert.equal(reported.body.outcomes[0].note, undefined, JSON.stringify(reported.body.outcomes[0]))
    assert.equal((await stateOf(runId)).state, 'succeeded')

    // An expired lease is not a lost one, and this is the assertion that keeps
    // the guard from being too strict. Until another engine actually takes the
    // run, the original engine's word is still the only word there will be, and
    // refusing it here would abandon runs that finished.
    const lease = await leaseOf(runId)
    assert.equal(lease.takeovers, 0)
    assert.equal(lease.unheld, 0)
    const [result] = await h.admin<{ requests: number }[]>`
      SELECT requests FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(Number(result!.requests), 700)
  })

  it('ordering: the terminal event arrives first, and then there is no lease left to take', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId, token)

    await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'observed_load', outcome: 'succeeded', verdict: 'pass',
          result: { requests: 90 },
        },
      }),
    ], token)
    assert.equal((await stateOf(runId)).state, 'succeeded')
    // The terminal statement clears the lease with the state, so there is
    // nothing for a second engine to find.
    assert.equal((await leaseOf(runId)).holder, null)

    h.clock.advance(RUN_LEASE_MS + 60_000)
    const second = await claim(target.envId, secondToken)
    assert.equal(second.body.run, null, 'a finished run was handed to a second engine')
    assert.equal((await leaseOf(runId)).takeovers, 0)
  })

  it('ordering: both engines report, and only the one holding the run ends it', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId, token)
    h.clock.advance(RUN_LEASE_MS + 60_000)
    await claim(target.envId, secondToken)

    // The holder first this time, which is the other arrival order from the
    // test above. The first engine's report is then late AND unheld, and it must
    // still change nothing.
    const holder = await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'observed_load', outcome: 'succeeded', verdict: 'pass',
          result: { requests: 1200 },
        },
      }),
    ], secondToken)
    assert.equal(holder.body.outcomes[0].note, undefined, JSON.stringify(holder.body.outcomes[0]))

    const loser = await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'observed_load', outcome: 'failed', verdict: 'fail',
          detail: 'the first engine stopped', result: { requests: 4 },
        },
      }),
    ], token)
    // Refused for being late rather than for the lease, because the terminal
    // statement already cleared the holder. Either sentence is correct here and
    // the assertion is on what was NOT changed.
    assert.match(loser.body.outcomes[0].note, /already succeeded|held by another engine/)

    const state = await stateOf(runId)
    assert.equal(state.state, 'succeeded')
    assert.equal(state.verdict, 'pass')
    const rows = await h.admin<{ requests: number }[]>`
      SELECT requests FROM workload_run_results WHERE workload_run_id = ${runId}`
    assert.equal(rows.length, 1, 'a second report wrote a second result row')
    assert.equal(Number(rows[0]!.requests), 1200)
  })

  it('ordering: neither engine reports, and the abandoned run says it changed hands', async () => {
    const target = await define('observed_load', { durationSeconds: 30 })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId, token)
    h.clock.advance(RUN_LEASE_MS + 60_000)
    await claim(target.envId, secondToken)

    h.clock.advance(RUN_DEADLINE_MS + 60_000)
    await callProcedure(h, owner, 'workloads.runs', 'query', { limit: 5 })

    const state = await stateOf(runId)
    assert.equal(state.state, 'abandoned')
    // NOT the generic sentence. Since an engine that lost a run stops reporting,
    // a run somebody took and a run whose engine died are both silence at the
    // deadline, and this row is the only thing that separates them.
    assert.match(
      String(state.detail), /changed hands/,
      'an abandoned run that changed hands is telling the reader the same thing as one whose ' +
      'only engine died, which is the distinction the engine side can no longer make.',
    )
    assert.equal((await leaseOf(runId)).takeovers, 1)
  })

  it('ordering: a run nobody ever claimed may be ended by the engine that has it, and says so if not', async () => {
    // The no-holder arm, and it is deliberate rather than lax: a run handed to
    // an engine by --run-id is never claimed, and an engine's spool can land the
    // terminal event before the claim that was supposed to precede it. Refusing
    // those would abandon runs that finished.
    const ran = await define('observed_load', { durationSeconds: 30 })
    const ranId = (await start(ran)).body.result.data.runId
    assert.equal((await leaseOf(ranId)).holder, null)
    const reported = await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: ranId, kind: 'observed_load', outcome: 'succeeded', verdict: 'pass',
          result: { requests: 55 },
        },
      }),
    ], secondToken)
    assert.equal(reported.body.outcomes[0].note, undefined, JSON.stringify(reported.body.outcomes[0]))
    assert.equal((await stateOf(ranId)).state, 'succeeded')
    assert.equal((await leaseOf(ranId)).unheld, 0)

    // And the same run left alone is abandoned saying nobody took it, which is a
    // different fault to chase from an engine that took it and died: this one is
    // the dispatch, not the runner.
    const idle = await define('observed_load', { durationSeconds: 30 })
    const idleId = (await start(idle)).body.result.data.runId
    h.clock.advance(RUN_DEADLINE_MS + 60_000)
    await callProcedure(h, owner, 'workloads.runs', 'query', { limit: 5 })
    const state = await stateOf(idleId)
    assert.equal(state.state, 'abandoned')
    assert.match(String(state.detail), /No engine ever claimed this run/)
  })

  // -------------------------------------------------------------------------
  // Cancelling
  // -------------------------------------------------------------------------

  it('ordering: cancel before anything claimed it is settled here and now', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    const cancelled: Answer = await callProcedure(h, owner, 'workloads.cancel', 'mutation', {
      runId, reason: 'wrong branch',
    })
    assert.equal(cancelled.status, 200, JSON.stringify(cancelled.body))
    // No engine has it, so no engine will ever report on it. Waiting for a
    // terminal event would leave it open until its deadline.
    assert.equal(cancelled.body.result.data.stopped, true)
    assert.equal(cancelled.body.result.data.commandId, null)
    assert.equal((await stateOf(runId)).state, 'cancelled')
  })

  it('ordering: cancel during execution writes a durable command and waits', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId)

    const cancelled: Answer = await callProcedure(h, owner, 'workloads.cancel', 'mutation', { runId })
    assert.equal(cancelled.body.result.data.stopped, false)
    const commandId = cancelled.body.result.data.commandId
    assert.ok(commandId, 'a cancel of a claimed run left nothing for a runtime to act on')
    // The run is not cancelled yet, and saying it was would be the same lie the
    // old teardown told.
    assert.equal((await stateOf(runId)).state, 'accepted')

    const command = await h.admin<{ state: string }[]>`
      SELECT state::text AS state FROM runtime_commands WHERE id = ${commandId}`
    assert.equal(command[0]!.state, 'pending')

    // And the engine reporting that it stopped is what closes it.
    await send([
      event({ type: 'workload.cancelled', sequence: 7, payload: { workload_run_id: runId } }),
    ])
    assert.equal((await stateOf(runId)).state, 'cancelled')
    const settled = await h.admin<{ state: string; outcome: string | null }[]>`
      SELECT state::text AS state, outcome FROM runtime_commands WHERE id = ${commandId}`
    assert.equal(settled[0]!.state, 'acknowledged')
    assert.equal(settled[0]!.outcome, 'done')
  })

  it('ordering: a cancel that loses the race is superseded, not failed', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId)
    const cancelled: Answer = await callProcedure(h, owner, 'workloads.cancel', 'mutation', { runId })
    const commandId = cancelled.body.result.data.commandId

    // The engine finished the work before the cancel reached it. Nothing went
    // wrong, and nobody should be shown a failed command for a run that simply
    // finished first.
    await send([
      event({
        type: 'workload.finished',
        sequence: 9,
        payload: { workload_run_id: runId, kind: 'browser_workflow', verdict: 'pass', result: { workflows: 1 } },
      }),
    ])
    assert.equal((await stateOf(runId)).state, 'succeeded')
    const settled = await h.admin<{ state: string; detail: string | null }[]>`
      SELECT state::text AS state, detail FROM runtime_commands WHERE id = ${commandId}`
    assert.equal(settled[0]!.state, 'superseded')
    assert.match(settled[0]!.detail!, /finished as succeeded before the cancel/)
  })

  it('ordering: cancel after completion is refused and changes nothing', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: { workload_run_id: runId, kind: 'browser_workflow', verdict: 'pass', result: { workflows: 1 } },
      }),
    ])
    const refused: Answer = await callProcedure(h, owner, 'workloads.cancel', 'mutation', { runId })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(refused.body), /already finished/)
    const state = await stateOf(runId)
    assert.equal(state.state, 'succeeded')
    assert.equal(state.verdict, 'pass')
  })

  it('ordering: cancelling twice joins the first request rather than queueing a second', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await claim(target.envId)
    const first: Answer = await callProcedure(h, owner, 'workloads.cancel', 'mutation', { runId })
    const second: Answer = await callProcedure(h, owner, 'workloads.cancel', 'mutation', { runId })
    assert.equal(second.status, 200, JSON.stringify(second.body))
    assert.equal(second.body.result.data.alreadyRequested, true)
    assert.equal(second.body.result.data.commandId, first.body.result.data.commandId)

    const commands = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM runtime_commands
      WHERE kind = 'workload.cancel' AND workload_run_id = ${runId}`
    assert.equal(Number(commands[0]!.n), 1)
  })

  // -------------------------------------------------------------------------
  // Retrying
  // -------------------------------------------------------------------------

  it('ordering: a retry runs the same version, and marks the original superseded', async () => {
    const target = await define('observed_load', { durationSeconds: 30, scale: 1 })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: {
          workload_run_id: runId, kind: 'observed_load', verdict: 'fail',
          result: { requests: 5, p95_ms: 900 },
        },
      }),
    ])

    // A newer version exists by the time somebody retries. A retry answers
    // "was that a fluke", and answering it with a definition somebody edited in
    // the meantime answers a different question while looking like it answered
    // this one.
    await callProcedure(h, owner, 'workloads.addVersion', 'mutation', {
      slug: target.slug, body: { durationSeconds: 600, scale: 4 },
    })

    const retried: Answer = await callProcedure(h, owner, 'workloads.retry', 'mutation', { runId })
    assert.equal(retried.status, 200, JSON.stringify(retried.body))
    const newRunId = retried.body.result.data.runId

    const versions = await h.admin<{ version: number; attempt: number; retry_of: string }[]>`
      SELECT v.version, wr.attempt, wr.retry_of FROM workload_runs wr
      JOIN workload_versions v ON v.id = wr.workload_version_id WHERE wr.id = ${newRunId}`
    assert.equal(Number(versions[0]!.version), 1, 'the retry ran a version the original never used')
    assert.equal(Number(versions[0]!.attempt), 2)
    assert.equal(versions[0]!.retry_of, runId)

    const original = await h.admin<{ superseded_by: string }[]>`
      SELECT superseded_by FROM workload_runs WHERE id = ${runId}`
    assert.equal(original[0]!.superseded_by, newRunId)
  })

  it('ordering: retrying a superseded run is refused, naming its successor', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: { workload_run_id: runId, kind: 'browser_workflow', verdict: 'fail', result: { workflows: 1 } },
      }),
    ])
    const first: Answer = await callProcedure(h, owner, 'workloads.retry', 'mutation', { runId })
    const successor = first.body.result.data.runId

    // Finish the successor so the live-run index is not what refuses this.
    await send([
      event({
        type: 'workload.finished', sequence: 9,
        payload: { workload_run_id: successor, kind: 'browser_workflow', verdict: 'fail', result: { workflows: 1 } },
      }),
    ])

    const again: Answer = await callProcedure(h, owner, 'workloads.retry', 'mutation', { runId })
    assert.equal(errorCode(again.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(again.body), new RegExp(successor))
  })

  it('ordering: retrying a run that is still going is refused', async () => {
    const target = await define('browser_workflow', { select: ['sign-up'] })
    const runId = (await start(target)).body.result.data.runId
    const refused: Answer = await callProcedure(h, owner, 'workloads.retry', 'mutation', { runId })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(refused.body), /still requested/)
  })
})
