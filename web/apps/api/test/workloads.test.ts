// Workload definitions, the versions nobody can edit, and the promotion that
// turns an exploration into something that runs every time.
//
// The orderings a run goes through are the other file, workloadruns.test.ts.
// This one is about the things that are true before any run exists, and the
// constraints that make them true in the database rather than in a route: an
// immutable version, a kind that cannot change, and a result that cannot wear
// another kind's columns.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { digestOf, dispatchInputs, parseBody, BodyRefused } from '../src/workloads/bodies.ts'
import { compileExploration, ExplorationRefused } from '../src/workloads/promote.ts'
import {
  available, startApi, seedOrg, signInAs, callProcedure, errorCode, dropOrg,
  type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

/** callProcedure with the body left as `any`.
 *
 *  The response is a tRPC envelope whose data shape differs per procedure, and
 *  a test that narrowed it would be asserting against a type it wrote rather
 *  than against what came back. Every other suite here does the same. */
type Answer = { status: number; body: any }

/** The Postgres SQLSTATE underneath whatever the query builder wrapped it in. */
function sqlState(error: unknown): string | null {
  let cur: unknown = error
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; cause?: unknown }
    if (typeof e.code === 'string' && /^[0-9A-Z]{5}$/.test(e.code)) return e.code
    cur = e.cause
  }
  return null
}

// ---------------------------------------------------------------------------
// The body, which needs no database
// ---------------------------------------------------------------------------

describe('what a workload version is allowed to say', () => {
  it('accepts the knobs each command actually has', () => {
    assert.deepEqual(parseBody('observed_load', { durationSeconds: 60, scale: 2 }).body, {
      durationSeconds: 60, scale: 2,
    })
    assert.deepEqual(parseBody('http_scenario', { select: ['checkout'], seed: 7 }).body, {
      select: ['checkout'], seed: 7,
    })
    assert.deepEqual(parseBody('browser_workflow', { select: [] }).body, { select: [] })
    assert.deepEqual(parseBody('exploration', { select: ['upgrade'], seed: 'a' }).body, {
      select: ['upgrade'], seed: 'a',
    })
  })

  it('refuses a key the command has no flag for, rather than dropping it', () => {
    // Strict on the write boundary. A misspelled key that is silently ignored
    // is a workload that runs and does not do what its author wrote, and the
    // person reading the result has no way to tell.
    assert.throws(
      () => parseBody('observed_load', { durationSecond: 60 }),
      (e: unknown) => e instanceof BodyRefused && /durationSecond/.test((e as Error).message),
    )
  })

  it('refuses a body written for another kind', () => {
    // The four kinds are four kinds. A scenario body handed to a load workload
    // is not a body with an extra field, it is a different thing.
    assert.throws(() => parseBody('observed_load', { select: ['checkout'] }), BodyRefused)
    assert.throws(() => parseBody('http_scenario', { select: [] }), BodyRefused)
  })

  it('digests two orderings of the same body the same, and a changed one differently', () => {
    // A digest that depended on key order would answer "this save changed
    // nothing" wrongly after a round trip through jsonb, which reorders.
    assert.equal(
      digestOf({ scale: 2, durationSeconds: 60 }),
      digestOf({ durationSeconds: 60, scale: 2 }),
    )
    assert.notEqual(digestOf({ scale: 2 }), digestOf({ scale: 3 }))
  })

  it('turns each kind into the flags its command has, and says which need the newer workflow', () => {
    // The inputs are the whole contract with the customer's workflow. Asserted
    // exactly, because an input the workflow does not declare is a 422 from
    // GitHub and an input it declares and ignores is a dead socket.
    const load = dispatchInputs('observed_load', { durationSeconds: 90, scale: 0.5 })
    assert.deepEqual(load.inputs, {
      command: 'load', workflows: '', duration: '90s', scale: '0.5',
    })
    assert.equal(load.needsUpdatedWorkflow, false)

    const agents = dispatchInputs('browser_workflow', { select: ['sign-up', 'checkout'] })
    assert.deepEqual(agents.inputs, {
      command: 'agents', workflows: 'sign-up,checkout', duration: '', scale: '',
    })
    assert.equal(agents.needsUpdatedWorkflow, false)

    const scenario = dispatchInputs('http_scenario', { select: ['checkout'], seed: 7, concurrency: 50 })
    assert.deepEqual(scenario.inputs, {
      command: 'scenario', workflows: 'checkout', duration: '', scale: '', seed: '7', concurrency: '50',
    })
    // Six inputs against a workflow that declares four is a 422, so this one
    // has to say it needs the newer file rather than failing obscurely.
    assert.equal(scenario.needsUpdatedWorkflow, true)

    const explore = dispatchInputs('exploration', { select: ['upgrade'], seed: 'abc' })
    assert.equal(explore.inputs.command, 'explore')
    assert.equal(explore.inputs.seed, 'abc')
    assert.equal(explore.needsUpdatedWorkflow, true)
  })
})

// ---------------------------------------------------------------------------
// Promotion, which also needs no database
// ---------------------------------------------------------------------------

describe('compiling an exploration into a workflow', () => {
  const exploration = {
    name: 'Upgrade a plan',
    goal: 'reach the billing page and see the new plan',
    seed: 'seed-9',
    reached: true,
    journey: [
      { kind: 'goto', url: 'http://127.0.0.1:8731/pricing?ref=nav' },
      { kind: 'click', control: 'Upgrade plan' },
      { kind: 'fill', field: 'Card number' },
    ],
    findings: [
      { kind: 'no_effect', url: 'http://127.0.0.1:8731/pricing', control: 'Compare plans' },
      { kind: 'goal_unreached' },
    ],
    missing: ['/settings', '/team'],
  }

  it('carries the name, the start path and the journey, in accessible names', () => {
    const c = compileExploration(exploration, 'shopper')
    assert.equal(c.slug, 'upgrade-a-plan')
    assert.match(c.manifestBlock, /name: "upgrade-a-plan"/)
    // The host is stripped: a manifest with 127.0.0.1:8731 in it reads as a
    // mistake within a day, and the path is the part that means anything.
    assert.match(c.manifestBlock, /start_path: "\/pricing\?ref=nav"/)
    assert.doesNotMatch(c.manifestBlock, /127\.0\.0\.1/)
    assert.match(c.description, /open \/pricing/)
    assert.match(c.description, /press "Upgrade plan"/)
    assert.match(c.manifestBlock, /persona: "shopper"/)
    // Room above what the exploration spent, because a declared workflow takes
    // a shorter route and a budget with no slack turns one redirect into a
    // blocked run.
    assert.match(c.manifestBlock, /steps: 13/)
  })

  it('asserts the goal and says why that is not the same as a passing page', () => {
    const c = compileExploration(exploration, null)
    assert.match(c.manifestBlock, /expect:\n\s+- "reach the billing page and see the new plan\."/)
    assert.ok(
      c.dropped.some((d) => /knows what it was looking for/.test(d)),
      'the compilation does not say that the expectation is the goal rather than a passing page',
    )
  })

  it('lists the friction it will not assert, once each, and not the unreached goal twice', () => {
    const c = compileExploration({ ...exploration, reached: false }, null)
    const friction = c.dropped.filter((d) => /friction finding/.test(d))
    assert.equal(friction.length, 1, 'goal_unreached was listed as a friction finding as well')
    assert.ok(friction[0]!.includes('a control that did nothing'))
    assert.ok(friction[0]!.includes('"Compare plans"'))
    assert.equal(
      c.dropped.filter((d) => /never reached the goal/.test(d)).length,
      1,
      'the unreached goal is said twice, which reads as two things having gone wrong',
    )
  })

  it('says the parts it did not walk, and that nothing runs until the block is pasted in', () => {
    const c = compileExploration(exploration, null)
    assert.ok(c.dropped.some((d) => /2 parts of the application/.test(d)))
    assert.ok(
      c.dropped.some((d) => /antifailure\.yaml/.test(d)),
      'the compilation does not say that a control plane cannot put a file in a repository',
    )
  })

  it('skips a move it cannot read rather than losing the walk', () => {
    // One malformed element must not discard the collection. A journey that
    // lost every step compiles a workflow that says "did not get anywhere",
    // which is a false statement about a real exploration.
    const c = compileExploration(
      { ...exploration, journey: [exploration.journey[0], { kind: 42 }, exploration.journey[1]] },
      null,
    )
    assert.match(c.description, /open \/pricing/)
    assert.match(c.description, /press "Upgrade plan"/)
  })

  it('refuses a document with no name or no goal', () => {
    assert.throws(() => compileExploration({ goal: 'somewhere' }, null), ExplorationRefused)
    assert.throws(() => compileExploration({ name: 'thing' }, null), ExplorationRefused)
    assert.throws(() => compileExploration('not a document', null), ExplorationRefused)
  })
})

// ---------------------------------------------------------------------------
// Against a real database
// ---------------------------------------------------------------------------

describe('workload definitions', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn
  let viewer: SignedIn
  /** A finished run, so the constraint tests below always have a row to attack.
   *  Built in `before` rather than found by a query, because a test that
   *  returns early when it finds nothing is a test that reports success over a
   *  subject it never examined. */
  let runId: string
  let environmentId: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'studio')
    owner = await signInAs(h, org, 'owner')
    viewer = await signInAs(h, org, 'viewer')

    const [environment] = await h.admin<{ id: string }[]>`
      SELECT id FROM environments WHERE org_id = ${org.orgId} AND env_id = ${org.envId}`
    environmentId = environment!.id
    const [workload] = await h.admin<{ id: string }[]>`
      INSERT INTO workloads (org_id, repository_id, slug, name, kind)
      VALUES (${org.orgId}, ${org.repoId}, 'fixture', 'Fixture', 'browser_workflow')
      RETURNING id`
    const [version] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_versions (org_id, workload_id, version, body, body_digest)
      VALUES (${org.orgId}, ${workload!.id}, 1, '{"select":[]}'::jsonb, ${'a'.repeat(64)})
      RETURNING id`
    const [run] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_runs (
        org_id, workload_id, workload_version_id, environment_id, state,
        request_key, repository, git_ref, deadline_at, finished_at)
      VALUES (${org.orgId}, ${workload!.id}, ${version!.id}, ${environmentId}, 'succeeded',
              'fixture', ${org.repository}, 'main', now() + interval '1 hour', now())
      RETURNING id`
    runId = run!.id
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function create(over: Record<string, unknown> = {}): Promise<Answer> {
    return callProcedure(h, owner, 'workloads.create', 'mutation', {
      repository: org.repository,
      slug: `w-${randomUUID().slice(0, 8)}`,
      name: 'Checkout under load',
      kind: 'observed_load',
      body: { durationSeconds: 60, scale: 1 },
      ...over,
    })
  }

  it('creates a definition with its first version, because one with none cannot run', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    const { status, body } = await create({ slug })
    assert.equal(status, 200, JSON.stringify(body))
    assert.equal(body.result.data.version, 1)

    const read: Answer = await callProcedure(h, viewer, 'workloads.get', 'query', { slug })
    assert.equal(read.status, 200)
    assert.equal(read.body.result.data.workload.kind, 'observed_load')
    assert.equal(read.body.result.data.versions.length, 1)
    // A to-many is an array, said explicitly because getting this backwards on
    // the reading side is what blanks a page over one surprising row.
    assert.ok(Array.isArray(read.body.result.data.runs))
  })

  it('refuses a second live workload of the same name in one repository', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    await create({ slug })
    const second = await create({ slug })
    assert.equal(errorCode(second.body), 'BAD_REQUEST')
    assert.match(JSON.stringify(second.body), /already has a workload called/)
  })

  it('lets an archived name be used again, because a slug is not a tombstone', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    await create({ slug })
    const archived = await callProcedure(h, owner, 'workloads.archive', 'mutation', { slug })
    assert.equal(archived.status, 200, JSON.stringify(archived.body))
    const again = await create({ slug })
    assert.equal(again.status, 200, JSON.stringify(again.body))
  })

  it('adds a version, and adds nothing when the body is what it already says', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    await create({ slug })
    const same: Answer = await callProcedure(h, owner, 'workloads.addVersion', 'mutation', {
      slug, body: { durationSeconds: 60, scale: 1 },
    })
    assert.equal(same.body.result.data.created, false)
    assert.equal(same.body.result.data.version, 1)

    const changed: Answer = await callProcedure(
      h, owner, 'workloads.addVersion', 'mutation',
      { slug, body: { durationSeconds: 120, scale: 1 }, notes: 'twice as long' },
    )
    assert.equal(changed.body.result.data.created, true)
    assert.equal(changed.body.result.data.version, 2)
  })

  it('refuses a version in the wrong kind for the workload it belongs to', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    await create({ slug })
    const wrong = await callProcedure(h, owner, 'workloads.addVersion', 'mutation', {
      slug, body: { select: ['checkout'] },
    })
    assert.equal(errorCode(wrong.body), 'BAD_REQUEST')
  })

  // -------------------------------------------------------------------------
  // What the database refuses, whatever a route does
  // -------------------------------------------------------------------------

  it('grants the application no way to edit a version at all', async () => {
    // A privilege, not a policy. RLS admits a row and cannot say "this table is
    // append only", so immutability is a GRANT and this is what proves it: the
    // statement is refused for insufficient privilege rather than matching no
    // rows, which is what a policy would have produced.
    const rows = await h.admin<{ id: string }[]>`
      SELECT v.id FROM workload_versions v
      JOIN workloads w ON w.id = v.workload_id
      WHERE w.org_id = ${org.orgId} AND w.slug = 'fixture'`
    assert.ok(rows[0], 'no version to attack')

    for (const attempt of ['update', 'delete'] as const) {
      const failure = await h.pool
        .withTenant({ orgId: org.orgId }, async (db) => {
          const { sql } = await import('drizzle-orm')
          await db.execute(
            attempt === 'update'
              ? sql`UPDATE workload_versions SET body = '{}'::jsonb WHERE id = ${rows[0]!.id}`
              : sql`DELETE FROM workload_versions WHERE id = ${rows[0]!.id}`,
          )
        })
        .then(() => null, (e: unknown) => e)
      assert.ok(failure, `a version could be ${attempt}d`)
      // The SQLSTATE, not the message. drizzle wraps a failure as "Failed
      // query: <sql>" and hangs the driver's error off cause, so asserting on
      // the outer message would pass for any failure at all, including a typo
      // in this test's own SQL. 42501 is insufficient_privilege, which is what
      // a withheld grant produces and what a policy does NOT: a policy makes
      // the statement match no rows and raise nothing at all.
      assert.equal(sqlState(failure), '42501', `${attempt} failed for another reason: ${failure}`)
    }
  })

  it('refuses to change a workload from one kind to another', async () => {
    // The trigger, not the absence of a route. The absence of a route is not a
    // guarantee: the next route somebody writes is the one that forgets.
    const rows = await h.admin<{ id: string }[]>`
      SELECT id FROM workloads WHERE org_id = ${org.orgId} AND slug = 'fixture'`
    assert.ok(rows[0], 'no workload to attack')
    await assert.rejects(
      h.admin`UPDATE workloads SET kind = 'exploration' WHERE id = ${rows[0]!.id}`,
      /cannot become/,
    )
    // The trigger stops a change of kind and nothing else: an ordinary update
    // to the same row still works, so it is not passing by refusing every
    // write.
    await h.admin`UPDATE workloads SET name = 'Fixture renamed' WHERE id = ${rows[0]!.id}`
  })

  it('refuses a result of one kind wearing the columns of another', async () => {
    // The constraint that keeps the four kinds four kinds. Without it a browser
    // result could carry a request count and a console would draw a latency
    // chart over a number that is not a latency.
    await assert.rejects(
      h.admin`
        INSERT INTO workload_run_results (org_id, workload_run_id, kind, requests, workflows)
        VALUES (${org.orgId}, ${runId}, 'browser_workflow', 10, 1)`,
      /workload_run_results_shape/,
    )
    // And the other direction, so the constraint is not passing by refusing
    // everything: the right shape for this kind is accepted.
    await h.admin`
      INSERT INTO workload_run_results (org_id, workload_run_id, kind, workflows)
      VALUES (${org.orgId}, ${runId}, 'browser_workflow', 1)`
    await h.admin`DELETE FROM workload_run_results WHERE workload_run_id = ${runId}`
  })

  it('keeps a baseline and a regression together, because no baseline is not no change', async () => {
    await assert.rejects(
      h.admin`
        INSERT INTO workload_route_metrics (org_id, workload_run_id, route, sent, baseline_p95_ms)
        VALUES (${org.orgId}, ${runId}, 'GET /alone', 1, 120)`,
      /workload_route_metrics_baseline/,
    )
    await assert.rejects(
      h.admin`
        INSERT INTO workload_route_metrics (org_id, workload_run_id, route, sent, p95_increase)
        VALUES (${org.orgId}, ${runId}, 'GET /alone', 1, 1.4)`,
      /workload_route_metrics_baseline/,
    )
    // Both together is the shape that means something, and it is accepted.
    await h.admin`
      INSERT INTO workload_route_metrics (org_id, workload_run_id, route, sent, baseline_p95_ms, p95_increase)
      VALUES (${org.orgId}, ${runId}, 'GET /alone', 1, 120, 1.4)`
    await h.admin`DELETE FROM workload_route_metrics WHERE workload_run_id = ${runId}`
  })

  it('refuses evidence that claims to be uploaded with nothing to verify it', async () => {
    await assert.rejects(
      h.admin`
        INSERT INTO workload_evidence (org_id, workload_run_id, kind, availability, locator)
        VALUES (${org.orgId}, ${runId}, 'trace', 'uploaded', 's3://bucket/t.zip')`,
      /workload_evidence_uploaded_is_verifiable/,
    )
    // A runner path with no checksum is the ordinary, honest case and is
    // accepted, so the constraint is refusing the claim rather than the row.
    await h.admin`
      INSERT INTO workload_evidence (org_id, workload_run_id, kind, availability, locator)
      VALUES (${org.orgId}, ${runId}, 'trace', 'runner_local', '/home/runner/t.zip')`
    await h.admin`DELETE FROM workload_evidence WHERE workload_run_id = ${runId}`
  })

  // -------------------------------------------------------------------------
  // Promotion through the route
  // -------------------------------------------------------------------------

  it('promotes an exploration into a versioned workflow, and says what it dropped', async () => {
    const promoted: Answer = await callProcedure(h, owner, 'workloads.promote', 'mutation', {
      repository: org.repository,
      exploration: {
        name: `Discovered ${randomUUID().slice(0, 6)}`,
        goal: 'see the receipt',
        seed: 's1',
        reached: true,
        journey: [{ kind: 'goto', url: 'http://env.test/checkout' }],
        findings: [{ kind: 'dead_end', url: 'http://env.test/checkout' }],
        missing: [],
      },
    })
    assert.equal(promoted.status, 200, JSON.stringify(promoted.body))
    const data = promoted.body.result.data
    assert.equal(data.version, 1)
    assert.ok(data.dropped.length >= 2, 'a promotion that dropped nothing is a promotion that lied')
    assert.match(data.manifestBlock, /workflows:/)

    // The version is stored as `promoted`, so a reader can tell a compiled
    // workflow from one somebody wrote.
    const stored = await h.admin<{ source: string; notes: string }[]>`
      SELECT v.source::text AS source, v.notes FROM workload_versions v
      JOIN workloads w ON w.id = v.workload_id
      WHERE w.slug = ${data.slug} AND w.org_id = ${org.orgId}`
    assert.equal(stored[0]!.source, 'promoted')
    assert.ok(stored[0]!.notes.length > 0, 'what was dropped was not kept with the version')
  })

  it('refuses to promote into a workload of another kind', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    await create({ slug })
    const refused = await callProcedure(h, owner, 'workloads.promote', 'mutation', {
      repository: org.repository,
      slug,
      exploration: { name: 'x', goal: 'y', journey: [] },
    })
    assert.equal(errorCode(refused.body), 'BAD_REQUEST')
    assert.match(JSON.stringify(refused.body), /cannot change kind/)
  })

  it('refuses to promote from a run that is not an exploration', async () => {
    const refused = await callProcedure(h, owner, 'workloads.promote', 'mutation', {
      repository: org.repository,
      fromRunId: runId,
      exploration: { name: 'x', goal: 'y', journey: [] },
    })
    assert.equal(errorCode(refused.body), 'BAD_REQUEST')
  })

  it('refuses to archive a workload with a run still going', async () => {
    const slug = `w-${randomUUID().slice(0, 8)}`
    const made = await create({ slug })
    assert.equal(made.status, 200)
    const ids = await h.admin<{ id: string; version_id: string; env: string }[]>`
      SELECT w.id, v.id AS version_id, e.id AS env
      FROM workloads w
      JOIN workload_versions v ON v.workload_id = w.id
      JOIN environments e ON e.org_id = w.org_id
      WHERE w.slug = ${slug} AND w.org_id = ${org.orgId} LIMIT 1`
    await h.admin`
      INSERT INTO workload_runs (
        org_id, workload_id, workload_version_id, environment_id, state,
        request_key, repository, git_ref, deadline_at)
      VALUES (${org.orgId}, ${ids[0]!.id}, ${ids[0]!.version_id}, ${ids[0]!.env}, 'running',
              ${`live-${slug}`}, ${org.repository}, 'main', now() + interval '1 hour')`

    const refused = await callProcedure(h, owner, 'workloads.archive', 'mutation', { slug })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(refused.body), /has a run going/)
  })
})
