// The golden, run, verdict and egress projections, end to end.
//
// The same suite shape as projection.test.ts and for the same reason it says in
// its own header: nothing here seeds the row it is asserting on. Every golden,
// run and verdict has to have been brought into existence by an event arriving
// at /v1/events, which is the only path a real engine has, and every assertion
// that matters ends at the procedure the console actually calls rather than at
// the table.
//
// That last part is the point of the file. A test that stops at the row proves
// the projector runs; it does not prove the page fills. Three of these tables
// were read by a router and written by nobody, so the console's runs list, its
// verdicts, the goldens quota and the compliance pack's masking control were
// empty for every real customer and full in development, where the staging
// seeder writes all of them. So each case here goes event in, row out, and then
// asks the router the page asks.
//
// The orderings are enumerated rather than assumed, because the sink drops the
// OLDEST events of a failed batch: run.finished before run.started, and a
// verdict before either, are the ordinary consequence of one outage rather than
// hypotheses.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import {
  available, startApi, seedOrg, dropOrg, signInAs, callProcedure,
  type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

describe('the golden, run and verdict projections', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let token: string
  let admin: SignedIn
  let owner: SignedIn

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'projrun')
    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_test')`
    admin = await signInAs(h, org, 'admin')
    // billing.manage is an owner's permission, and the goldens quota is behind it.
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function send(events: unknown[]): Promise<{ status: number; body: any }> {
    const res = await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ events }),
    })
    return { status: res.status, body: await res.json() }
  }

  /** An event as the engine now sends it: the identity travels on every one,
   *  not only on the first, because the first is the one an overflowing spool
   *  drops. */
  function event(
    type: string,
    envId: string,
    sequence: number,
    payload: Record<string, unknown> = {},
  ): Record<string, unknown> {
    return {
      id: randomUUID(),
      type,
      envId,
      sequence,
      occurredAt: h.clock.now().toISOString(),
      payload: { repository: org.repository, branch: 'feature/a-branch', ...payload },
    }
  }

  function envId(label: string): string {
    return `env-${label}-${randomUUID().slice(0, 8)}`
  }

  /** Everything accepted and nothing left unprojected. An event that is stored
   *  and changes nothing is reported rather than dropped, so a batch that comes
   *  back clean on this line is a batch that reached a table. */
  function projected(body: any, count: number): void {
    assert.equal(body.accepted, count, JSON.stringify(body.outcomes))
    assert.equal(body.rejected, 0, JSON.stringify(body.outcomes))
    assert.equal(body.unprojected, 0, JSON.stringify(body.outcomes))
  }

  /** What a tRPC query answered with. The envelope is result.data, and a test
   *  that reads body directly asserts against the envelope rather than the
   *  answer, which passes for the wrong reason. */
  function data(res: { status: number; body: unknown }): any {
    assert.equal(res.status, 200, JSON.stringify(res.body))
    return (res.body as { result: { data: unknown } }).result.data
  }

  async function goldens(): Promise<any[]> {
    return h.admin`
      SELECT g.version, g.verified, g.rules_digest, g.size_bytes, g.attestation, g.created_at
      FROM golden_versions g WHERE g.org_id = ${org.orgId} ORDER BY g.version`
  }

  // -------------------------------------------------------------------------
  // golden.published
  // -------------------------------------------------------------------------

  it('a published golden becomes a row the masking page and the plan page can read', async () => {
    const version = `gv_${randomUUID().slice(0, 8)}`
    const attestation = {
      report: { scanner: 'af-verify', tables: 12, columns: 41, rows_sampled: 2000, findings: [] },
      golden: version,
      rules_hash: 'abc123',
      public_key: 'k',
      signature: 's',
    }
    const { status, body } = await send([
      event('golden.published', envId('golden'), 1, {
        version,
        verified: true,
        rules_digest: 'abc123',
        size_bytes: 4_194_304,
        // Serialised twice on the wire, because an engine event field is a
        // string and an attestation is a document. The projector parses it, so
        // the column holds jsonb rather than a quoted string that every query
        // would have to unwrap.
        attestation: JSON.stringify(attestation),
      }),
    ])
    assert.equal(status, 202, 'ingestion accepts with 202')
    projected(body, 1)

    const rows = (await goldens()).filter((g) => g.version === version)
    assert.equal(rows.length, 1, 'the golden did not reach golden_versions')
    assert.equal(rows[0].verified, true)
    assert.equal(rows[0].rules_digest, 'abc123')
    assert.equal(String(rows[0].size_bytes), '4194304')
    assert.equal(
      rows[0].attestation.report.columns, 41,
      'the attestation is not a document the compliance pack can read',
    )

    // The console's masking page, which is the reader that reported nothing.
    const shown = (data(await callProcedure(h, admin, 'masking.attestations', 'query', {
      repository: org.repository,
    })) as any[]).filter((a) => a.version === version)
    assert.equal(shown.length, 1, 'the golden is in the table and not on the page')
    assert.equal(shown[0].verified, true)

    // The plan page's goldens quota, which was a count over a table only
    // fixtures wrote to.
    const plan = data(await callProcedure(h, owner, 'billing.get', 'query', undefined))
    assert.ok(
      Number(plan.holding.goldens) >= 1,
      `the goldens quota still reads ${plan.holding.goldens}`,
    )
  })

  it('a second announcement of the same golden updates one row rather than making two', async () => {
    const version = `gv_${randomUUID().slice(0, 8)}`
    const id = envId('again')
    // `af up` announces the golden it branched on every single run, so this is
    // the ordinary case rather than an edge one.
    projected((await send([
      event('golden.published', id, 1, { version, verified: true, size_bytes: 100 }),
    ])).body, 1)
    projected((await send([
      event('golden.published', envId('other'), 1, { version, verified: true }),
    ])).body, 1)

    const rows = (await goldens()).filter((g) => g.version === version)
    assert.equal(rows.length, 1, 'the same golden was recorded twice')
    // A sparse announcement, from the pinned or already-verified path, must not
    // erase what the refresh that built it sent.
    assert.equal(String(rows[0].size_bytes), '100')
  })

  it('a golden with no repository is stored, counted and said out loud', async () => {
    const { body } = await send([
      {
        id: randomUUID(),
        type: 'golden.published',
        envId: envId('norepo'),
        sequence: 1,
        occurredAt: h.clock.now().toISOString(),
        payload: { version: 'gv_orphan', verified: true },
      },
    ])
    assert.equal(body.accepted, 1)
    assert.equal(body.unprojected, 1, 'an event that changed nothing was reported as if it had')
    assert.match(String(body.outcomes[0].note), /repository/)
  })

  // -------------------------------------------------------------------------
  // run.started, run.finished, verdict.recorded, and the orderings
  // -------------------------------------------------------------------------

  async function runsFor(id: string): Promise<any[]> {
    return h.admin`
      SELECT r.id, r.kind, r.state::text AS state, r.started_at, r.finished_at, r.last_sequence
      FROM runs r JOIN environments e ON e.id = r.environment_id
      WHERE r.org_id = ${org.orgId} AND e.env_id = ${id}`
  }

  it('ordering: started then finished, the one everybody tests', async () => {
    const id = envId('run')
    const runId = `run_${randomUUID().slice(0, 8)}`
    projected((await send([
      event('run.started', id, 1, { run_id: runId, kind: 'workflows', workflows: 2 }),
    ])).body, 1)

    let rows = await runsFor(id)
    assert.equal(rows.length, 1)
    assert.equal(rows[0].state, 'running')

    h.clock.advance(5_000)
    projected((await send([
      event('run.finished', id, 2, { run_id: runId, state: 'complete', passed: 2 }),
    ])).body, 1)

    rows = await runsFor(id)
    assert.equal(rows.length, 1, 'run.finished made a second run rather than advancing the first')
    assert.equal(rows[0].state, 'complete')
    assert.ok(rows[0].started_at, 'the run has no start')
    assert.ok(rows[0].finished_at, 'the run has no finish')
    assert.ok(
      rows[0].finished_at.getTime() >= rows[0].started_at.getTime(),
      'the run finished before it began',
    )
  })

  it('ordering: finished before started, which is what an overflowing spool produces', async () => {
    const id = envId('reversed')
    const runId = `run_${randomUUID().slice(0, 8)}`
    h.clock.advance(1_000)
    const finishedAt = h.clock.now()
    projected((await send([
      event('run.finished', id, 2, { run_id: runId, state: 'failed' }),
    ])).body, 1)
    // The lower sequence, arriving second, must not drag the state backwards.
    projected((await send([
      event('run.started', id, 1, { run_id: runId }),
    ])).body, 1)

    const rows = await runsFor(id)
    assert.equal(rows.length, 1, 'the two events made two runs')
    assert.equal(rows[0].state, 'failed', 'a late run.started overwrote a finished run')
    assert.equal(rows[0].finished_at.getTime(), finishedAt.getTime())
  })

  it('ordering: both in one batch', async () => {
    const id = envId('batch')
    const runId = `run_${randomUUID().slice(0, 8)}`
    const { body } = await send([
      event('run.started', id, 1, { run_id: runId }),
      event('run.finished', id, 2, { run_id: runId, state: 'complete' }),
    ])
    projected(body, 2)
    const rows = await runsFor(id)
    assert.equal(rows.length, 1)
    assert.equal(rows[0].state, 'complete')
  })

  it('ordering: the same event twice is one run, not two', async () => {
    const id = envId('retry')
    const runId = `run_${randomUUID().slice(0, 8)}`
    const e = event('run.started', id, 1, { run_id: runId })
    projected((await send([e])).body, 1)
    const second = await send([e])
    assert.equal(second.body.duplicates, 1, 'the retry was not recognised as one')
    assert.equal((await runsFor(id)).length, 1)
  })

  it('ordering: a verdict with no run event at all still appears under a run', async () => {
    const id = envId('orphanverdict')
    const runId = `run_${randomUUID().slice(0, 8)}`
    projected((await send([
      event('verdict.recorded', id, 3, {
        run_id: runId,
        workflow: 'checkout',
        value: 'fail',
        summary: 'the order never reached the confirmation page',
        steps: 7,
        duration_ms: 4200,
        reproduction: JSON.stringify(['sign in as buyer', 'add an item', 'pay']),
      }),
    ])).body, 1)

    const rows = await runsFor(id)
    assert.equal(rows.length, 1, 'the verdict did not create the run it belongs to')

    const shown = data(
      await callProcedure(h, admin, 'runs.verdicts', 'query', { runId: rows[0].id })) as any[]
    assert.equal(shown.length, 1, 'the verdict is in the table and not on the page')
    assert.equal(shown[0].workflow, 'checkout')
    assert.equal(shown[0].value, 'fail')
    assert.equal(shown[0].steps, 7)
    assert.deepEqual(shown[0].reproduction, ['sign in as buyer', 'add an item', 'pay'])
  })

  it('a run and its verdicts reach the list the console actually renders', async () => {
    const id = envId('console')
    const runId = `run_${randomUUID().slice(0, 8)}`
    projected((await send([
      event('environment.ready', id, 1, { ttl_seconds: 3600 }),
      event('run.started', id, 2, { run_id: runId }),
      event('verdict.recorded', id, 3, { run_id: runId, workflow: 'signup', value: 'pass' }),
      event('verdict.recorded', id, 4, { run_id: runId, workflow: 'checkout', value: 'fail' }),
      event('run.finished', id, 5, { run_id: runId, state: 'complete' }),
    ])).body, 5)

    const shown = data(
      await callProcedure(h, admin, 'runs.recent', 'query', { envId: id, limit: 10 })).runs as any[]
    assert.equal(shown.length, 1, 'the run is in the table and not on the page')
    assert.equal(shown[0].env_id, id)
    assert.equal(shown[0].repository, org.repository)
    assert.equal(shown[0].branch, 'feature/a-branch')
    assert.equal(shown[0].state, 'complete')
    // The counts the list column shows. Two verdicts, one of them against the
    // application.
    assert.equal(Number(shown[0].verdicts), 2)
    assert.equal(Number(shown[0].failing), 1)

    // And the detail page, which titles itself from a separate query.
    const detail = data(await callProcedure(h, admin, 'runs.get', 'query', { runId: shown[0].id }))
    assert.equal(detail.env_id, id)
  })

  it('the same workflow reported twice in one run is one verdict, corrected', async () => {
    const id = envId('rerun')
    const runId = `run_${randomUUID().slice(0, 8)}`
    projected((await send([
      event('verdict.recorded', id, 1, { run_id: runId, workflow: 'search', value: 'blocked' }),
    ])).body, 1)
    projected((await send([
      event('verdict.recorded', id, 2, { run_id: runId, workflow: 'search', value: 'pass' }),
    ])).body, 1)

    const [run] = await runsFor(id)
    const shown = data(
      await callProcedure(h, admin, 'runs.verdicts', 'query', { runId: run.id })) as any[]
    assert.equal(shown.length, 1, 'one workflow produced two verdicts in one run')
    assert.equal(shown[0].value, 'pass')
  })

  it('a verdict word this control plane cannot read is unverified, never pass or fail', async () => {
    const id = envId('unknown')
    const runId = `run_${randomUUID().slice(0, 8)}`
    projected((await send([
      event('verdict.recorded', id, 1, {
        run_id: runId, workflow: 'newthing', value: 'inconclusive-in-a-later-engine',
      }),
    ])).body, 1)
    const [run] = await runsFor(id)
    const shown = data(
      await callProcedure(h, admin, 'runs.verdicts', 'query', { runId: run.id })) as any[]
    assert.equal(shown[0].value, 'unverified')
  })

  it('a run event with no environment and no identity is stored, counted and said out loud', async () => {
    const { body } = await send([
      {
        id: randomUUID(),
        type: 'run.started',
        envId: envId('nowhere'),
        sequence: 1,
        occurredAt: h.clock.now().toISOString(),
        payload: { run_id: 'run_orphan' },
      },
    ])
    assert.equal(body.accepted, 1)
    assert.equal(body.unprojected, 1)
    assert.match(String(body.outcomes[0].note), /repository and branch/)
  })

  it('a run event does not freeze the environment it created at queued', async () => {
    // The trap in creating an environment from a run event: a run carries a
    // high sequence number, and writing that number onto the environment would
    // make every lifecycle event that followed look stale.
    const id = envId('inert')
    const runId = `run_${randomUUID().slice(0, 8)}`
    projected((await send([
      event('run.started', id, 90, { run_id: runId }),
    ])).body, 1)
    projected((await send([
      event('environment.ready', id, 3, { ttl_seconds: 3600 }),
    ])).body, 1)

    const [env] = await h.admin<{ state: string }[]>`
      SELECT state::text AS state FROM environments
      WHERE org_id = ${org.orgId} AND env_id = ${id}`
    assert.equal(env!.state, 'running', 'the environment is stuck at the state a run event left it')
  })

  // -------------------------------------------------------------------------
  // network.decision
  // -------------------------------------------------------------------------

  it('egress decisions reach the network page, grouped by host and mode', async () => {
    const id = envId('egress')
    const decisions = [
      event('network.decision', id, 1, { host: 'api.stripe.com', mode: 'mock', allowed: true }),
      event('network.decision', id, 2, { host: 'api.stripe.com', mode: 'mock', allowed: true }),
      event('network.decision', id, 3, { host: 'evil.example', mode: 'block', allowed: false }),
    ]
    const { body } = await send(decisions)
    assert.equal(body.accepted, 3)
    assert.equal(body.rejected, 0)

    const shown = data(
      await callProcedure(h, admin, 'network.decisions', 'query', { envId: id })) as any[]
    const stripe = shown.find((d) => d.host === 'api.stripe.com')
    assert.ok(stripe, 'the decisions are in the events table and not on the page')
    assert.equal(stripe.mode, 'mock')
    assert.equal(Number(stripe.requests), 2)
    const blocked = shown.find((d) => d.host === 'evil.example')
    assert.ok(blocked, 'a refusal, which is the line somebody greps for, is missing')
    assert.equal(blocked.mode, 'block')
  })
})
