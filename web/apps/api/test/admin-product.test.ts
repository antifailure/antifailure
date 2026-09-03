// The Product lane's routes, and the promises the pages over them make.
//
// WHY THIS FILE EXISTS BESIDE admin-routes.test.ts. That suite walks the whole
// operator tree and asserts no route is unguarded, with a floor under the count
// so the walk cannot go vacuous. What a floor cannot see is one lane's routes
// leaving the tree while another lane's arrive: the total holds and seven pages
// go blank. So this counts MINE, exactly, and it is exact rather than a floor
// because a route appearing here that this lane did not add is as much worth
// knowing about as one disappearing.
//
// It also pins the three things the pages above these routes depend on and
// cannot check for themselves:
//
//   the standing of a run is not its state, which is the difference between a
//   job that finished and a job that passed,
//   experiments have no table, so the Experiments and Feature Flags page is a
//   flags page and must not grow a route here,
//   and the data read is a separate permission from the product read, which is
//   the whole reason analytics can count runs without reading somebody's
//   column names.
//
// TWO HALVES, and the split is deliberate. The first needs no database: it is
// over the composed router and the catalog, so it runs in CI whether or not
// Postgres answered, and it is what catches a route that lost its permission or
// a lane that claimed somebody else's prefix. The second needs one, because a
// procedure with a correct permission on it and a broken query passes every
// assertion in the first half and answers nothing. Seven routes that are
// declared and never called are seven shippable gaps.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { appRouter } from '../src/routers/index.ts'
import { adminRouter } from '../src/admin/router.ts'
import { declaredAdminPermissions } from '../src/trpc.ts'
import {
  ADMIN_PERMISSIONS,
  ADMIN_ROLE_PERMISSIONS,
  RESERVED_PREFIXES,
  adminRolesWith,
  type AdminRole,
} from '../src/admin/permissions.ts'
import { available, startApi, seedOrg, dropOrg, type ApiHarness, type Org } from './harness.ts'
import type { AdminContext } from '../src/admin/trpc.ts'

const hasDatabase = await available()

const MINE = /^admin\.product\./

/** Every route the served tree exposes, whatever depth it sits at. */
function servedPaths(): string[] {
  const procedures = (appRouter as unknown as { _def: { procedures: Record<string, unknown> } })._def
    .procedures
  return Object.keys(procedures).sort()
}

/**
 * The routes this lane owns, named rather than counted.
 *
 * A bare count would pass if `twins.get` were renamed to `twins.detail` and
 * every link in the console broke, so the names are the assertion and the
 * length is a consequence of it.
 */
const EXPECTED: Record<string, string> = {
  'admin.product.twins.list': 'admin.product.read',
  'admin.product.twins.get': 'admin.product.read',
  'admin.product.twins.branches': 'admin.product.read',
  'admin.product.runs.list': 'admin.product.read',
  'admin.product.runs.get': 'admin.product.read',
  'admin.product.data.goldens': 'admin.product.data.read',
  'admin.product.data.masking': 'admin.product.data.read',
}

describe('the product routes are mounted, guarded, and under this lane\'s prefix', () => {
  it('exactly these seven exist, with exactly these permissions', () => {
    const operator = declaredAdminPermissions()
    const mine = servedPaths().filter((p) => MINE.test(p))

    assert.deepEqual(
      mine,
      Object.keys(EXPECTED).sort(),
      'the product routes in the served tree are not the ones this lane declares',
    )

    for (const [path, permission] of Object.entries(EXPECTED)) {
      assert.equal(
        operator.get(path),
        permission,
        `${path} does not declare ${permission}`,
      )
    }
  })

  it('every permission they declare is in the catalog and belongs to this lane', () => {
    const operator = declaredAdminPermissions()
    for (const path of servedPaths().filter((p) => MINE.test(p))) {
      const permission = operator.get(path)
      assert.ok(permission, `${path} declares no permission at all`)
      // In the catalog rather than merely a plausible string. A permission no
      // role holds refuses everybody, which reads as a broken page rather than
      // as a typo.
      assert.ok(
        (ADMIN_PERMISSIONS as readonly string[]).includes(permission),
        `${path} declares ${permission}, which is not in ADMIN_PERMISSIONS`,
      )
      assert.ok(
        permission.startsWith('admin.product.'),
        `${path} declares ${permission}, which belongs to another lane`,
      )
    }
  })

  it('the lane declares its prefix, so no second lane can claim it', () => {
    assert.equal(RESERVED_PREFIXES['admin.product'], 'product')
  })

  it('this lane adds no write', () => {
    // Every lever these pages need already exists elsewhere and is already
    // enforced where a test can observe it: a teardown is admin.infra.*, a
    // suspension is admin.tenants.suspend, a kill is admin.flags.kill. Asserted
    // rather than trusted, because a write added here later would be a second
    // code path writing a row somebody else's route already owns, and the two
    // would come to disagree about what it means.
    const operator = declaredAdminPermissions()
    const writes = [...operator.entries()].filter(
      ([path, permission]) => MINE.test(path) && !permission.endsWith('.read'),
    )
    assert.deepEqual(writes, [], 'a product route declares something other than a read')
  })
})

describe('the data read is a narrower permission than the product read', () => {
  it('analytics holds one and not the other', () => {
    // The split is not symmetry. A masking rule enumerates a customer's schema
    // and names which columns hold personal data; counting runs and twins does
    // not need that. Analytics is the role the split exists for, so if it ever
    // holds both, the split has quietly stopped being one.
    const analytics = ADMIN_ROLE_PERMISSIONS.analytics as readonly string[]
    assert.ok(analytics.includes('admin.product.read'))
    assert.ok(
      !analytics.includes('admin.product.data.read'),
      'analytics can read customer column names, which is what this split exists to prevent',
    )
  })

  it('both are held by somebody, so neither guards a page nobody can open', () => {
    assert.ok(adminRolesWith('admin.product.read').length > 0)
    assert.ok(adminRolesWith('admin.product.data.read').length > 0)
  })

  it('the auditor role reads both and writes neither', () => {
    const readOnly = ADMIN_ROLE_PERMISSIONS.read_only as readonly string[]
    assert.ok(readOnly.includes('admin.product.read'))
    assert.ok(readOnly.includes('admin.product.data.read'))
    assert.deepEqual(
      readOnly.filter((p) => /\.(write|revoke|suspend|plan|export|teardown|engage)$/.test(p)),
      [],
    )
  })
})

describe('experiments have no table, so this lane serves no experiment route', () => {
  it('nothing under admin.product mentions an experiment, a variant or an assignment', () => {
    // The trap this lane was warned about, made mechanical. There is no
    // experiment table, no variant, no assignment, no exposure log and no
    // results anywhere in the schema, and a rollout percent is not an
    // experiment. The Experiments and Feature Flags page is therefore a flags
    // page that says the rest is not wired. If somebody adds a route here to
    // back a dashboard, this is the line that stops them, because the route
    // would have to invent every number on it.
    const suspicious = servedPaths().filter(
      (p) => MINE.test(p) && /experiment|variant|assignment|exposure|cohort/i.test(p),
    )
    assert.deepEqual(
      suspicious,
      [],
      'a product route names an experiment concept that nothing in the schema backs',
    )
  })

  it('the flag routes the page reads are the money lane\'s, unchanged', () => {
    // The page is built on these four. If a lane renames one, the page breaks
    // silently at runtime, because the console addresses them by string.
    const operator = declaredAdminPermissions()
    assert.equal(operator.get('admin.flags.list'), 'admin.flags.read')
    assert.equal(operator.get('admin.flags.set'), 'admin.flags.write')
    assert.equal(operator.get('admin.flags.kill'), 'admin.flags.write')
    assert.equal(operator.get('admin.flags.target'), 'admin.flags.write')
    assert.equal(operator.get('admin.entitlements.forOrganization'), 'admin.entitlements.read')
    assert.equal(operator.get('admin.entitlements.revoke'), 'admin.entitlements.write')
  })
})

// ---------------------------------------------------------------------------
// The routes against a real database
//
// Everything above is over the composed router and the catalog: it proves a
// route is declared and guarded, and proves nothing about whether it answers.
// A procedure with a permission on it and a broken query is a shippable gap
// that passes every assertion in this file so far.
//
// So this half seeds a twin, the three families of run against it, a golden
// version, a masking rule and a pull request, then calls each of the seven and
// reads the answer back. Two of the assertions are the reason the block exists
// rather than a formality:
//
//   a run that finished with no verdict at all comes back as `unknown` and not
//   as `passed`, which is the exit-code-zero-over-nothing defect,
//   and a branch whose pull request is merged while its twin is still running
//   comes back as `orphaned`, which is the one finding the branches page is
//   for and the one that no column in the database states directly.
// ---------------------------------------------------------------------------

describe('the product routes answer from real rows', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  let operatorId: string
  let overdueEnvId: string
  let failingRunId: string
  let silentRunId: string
  let loadRunId: string
  let checkId: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'product')

    const [operator] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`product-${randomUUID().slice(0, 8)}@example.test`}, 'Product operator', 'owner')
      RETURNING id`
    operatorId = operator!.id

    // The twin seedOrg made, aged past its own expiry, given a golden version
    // and attached to a pull request, so the overdue filter, the golden join
    // and the branch grouping all have something true to find.
    //
    // THE EXPIRY IS RELATIVE TO THE INJECTED CLOCK, not to the database's
    // `now()`. The route compares against ctx.clock, which the harness fakes,
    // and a row aged with SQL is aged against a different clock entirely: the
    // first draft of this seeded two days in the database's past and the route
    // read it as two years in the fake clock's future. The same trap the
    // operator session harness records, in the other direction.
    const twoDaysAgo = new Date(h.clock.now().getTime() - 2 * 86_400_000).toISOString()
    const [env] = await h.admin<{ id: string }[]>`
      UPDATE environments
      SET expires_at = ${twoDaysAgo}, golden_version = 'v7', branch = 'feature/late',
          pull_request = 41
      WHERE org_id = ${org.orgId}
      RETURNING id`
    overdueEnvId = env!.id

    await h.admin`
      INSERT INTO golden_versions (org_id, repository_id, version, verified, size_bytes)
      VALUES (${org.orgId}, ${org.repoId}, 'v7', false, 4096)`
    await h.admin`
      INSERT INTO masking_rules (org_id, repository_id, table_name, column_name, transform, confirmed, reason)
      VALUES (${org.orgId}, ${org.repoId}, 'customers', 'email', 'hash', false, 'Looks like an address')`

    // Two agent runs. One failed a verdict; one reached `complete` having
    // reported nothing at all, which is the case the standing exists for.
    const [failing] = await h.admin<{ id: string }[]>`
      INSERT INTO runs (org_id, environment_id, kind, state, started_at, finished_at)
      VALUES (${org.orgId}, ${overdueEnvId}, 'agent', 'complete', now() - interval '10 minutes', now())
      RETURNING id`
    failingRunId = failing!.id
    await h.admin`
      INSERT INTO verdicts (org_id, run_id, workflow, value, summary, steps, duration_ms)
      VALUES (${org.orgId}, ${failingRunId}, 'checkout', 'fail', 'The basket never loaded', 12, 4200)`
    await h.admin`
      INSERT INTO artifacts (org_id, run_id, kind, storage_key, size_bytes, retained)
      VALUES (${org.orgId}, ${failingRunId}, 'trace', 'runs/x/trace.zip', 900, false)`

    const [silent] = await h.admin<{ id: string }[]>`
      INSERT INTO runs (org_id, environment_id, kind, state, started_at, finished_at)
      VALUES (${org.orgId}, ${overdueEnvId}, 'agent', 'complete', now() - interval '5 minutes', now())
      RETURNING id`
    silentRunId = silent!.id

    // A load run that succeeded and carries a failing verdict, which is the
    // other half of the same distinction: the job ran to completion and what it
    // found was a failure.
    const [workload] = await h.admin<{ id: string }[]>`
      INSERT INTO workloads (org_id, repository_id, slug, name, kind)
      VALUES (${org.orgId}, ${org.repoId}, 'checkout-load', 'Checkout load', 'observed_load')
      RETURNING id`
    const [version] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_versions (org_id, workload_id, version, body, body_digest)
      VALUES (${org.orgId}, ${workload!.id}, 1, '{}'::jsonb, ${'a'.repeat(64)})
      RETURNING id`
    const [loadRun] = await h.admin<{ id: string }[]>`
      INSERT INTO workload_runs
        (org_id, workload_id, workload_version_id, environment_id, state, request_key,
         repository, git_ref, deadline_at, started_at, finished_at, verdict, failure_code,
         reproduce_command)
      VALUES (${org.orgId}, ${workload!.id}, ${version!.id}, ${overdueEnvId}, 'succeeded',
              ${randomUUID()}, ${org.repository}, 'refs/heads/main', now() + interval '1 hour',
              now() - interval '3 minutes', now(), 'fail', 'threshold.p95',
              'af load run checkout-load')
      RETURNING id`
    loadRunId = loadRun!.id
    await h.admin`
      INSERT INTO workload_run_results (org_id, workload_run_id, kind, requests, failures, p95_ms)
      VALUES (${org.orgId}, ${loadRunId}, 'observed_load', 1200, 9, 411)`

    // A merged pull request whose twin is still running: the orphan.
    const [pr] = await h.admin<{ id: string }[]>`
      INSERT INTO pull_requests
        (org_id, repository_id, number, head_sha, head_ref, base_ref, head_repository,
         state, closed_at)
      VALUES (${org.orgId}, ${org.repoId}, 41, ${'b'.repeat(40)}, 'feature/late', 'main',
              ${org.repository}, 'merged', now() - interval '1 day')
      RETURNING id`
    const [generation] = await h.admin<{ id: string }[]>`
      INSERT INTO pr_generations
        (org_id, pull_request_id, head_sha, state, detail, deadline_at, started_at, finished_at)
      VALUES (${org.orgId}, ${pr!.id}, ${'b'.repeat(40)}, 'failed', 'The check never reported',
              now() + interval '1 hour', now() - interval '2 minutes', now())
      RETURNING id`
    checkId = generation!.id
  })

  after(async () => {
    await h.admin`DELETE FROM admin_audit_entries WHERE subject_org_id = ${org.orgId}`
    await h.admin`DELETE FROM admin_audit_entries WHERE admin_user_id = ${operatorId}`
    await h.admin`DELETE FROM admin_users WHERE id = ${operatorId}`
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  /** A caller as an operator of a given role, with the context the mount builds.
   *  Impersonating is false; the gate's refusal for a true one is
   *  admin-gate.test.ts's, and a second copy here would be a weaker one. */
  function callerAs(role: AdminRole, label = 'product@antifailure.test') {
    const ctx = {
      pool: h.pool,
      adminPool: h.adminPool,
      admin: {
        adminUserId: operatorId,
        label,
        email: label,
        role,
        sessionId: randomUUID(),
        impersonating: false,
        impersonatedUserId: null,
      },
      adminDb: <T,>(fn: (db: never) => Promise<T>) =>
        h.adminPool.withOperator({ adminUserId: operatorId, label }, fn as never),
      clock: h.clock,
      github: h.github,
      stripe: null,
      appBaseUrl: 'http://app.test/',
      mailer: null,
      productName: 'Antifailure',
      hostedRequiredPlan: null,
      actor: null,
      origin: 'admin',
      ip: '203.0.113.10',
    } as unknown as AdminContext
    return adminRouter.createCaller(ctx as never)
  }

  it('lists the twin, and the overdue filter finds the one that is over', async () => {
    const owner = callerAs('owner')
    const live = await owner.product.twins.list({ orgId: org.orgId, scope: 'live', limit: 50 })
    const mine = live.rows.find((t) => t.id === overdueEnvId)
    assert.ok(mine, 'the seeded twin is not in the live list')
    assert.equal(mine.overdue, true)
    assert.equal(mine.goldenVersion, 'v7')
    // False rather than null: a version row exists and it was never verified.
    // Null would mean the twin names a version this installation has no row
    // for, which is a different answer and the page says so differently.
    assert.equal(mine.goldenVerified, false)
    assert.equal(mine.runs, 2)

    const overdue = await owner.product.twins.list({ scope: 'overdue', orgId: org.orgId, limit: 50 })
    assert.ok(overdue.rows.some((t) => t.id === overdueEnvId))
  })

  it('the organization filter returns that organization and nothing else', async () => {
    // Asserted over EVERY row rather than by finding the seeded one. A filter
    // that is silently ignored still contains the row somebody looked for, so
    // a test that only looks for its own fixture passes over a broken filter.
    // The console link that carries this is the branches page: two tenants can
    // both have a branch called main, and before the organization travelled
    // with the name that link showed somebody else's environments beside the
    // ones that were clicked on.
    const owner = callerAs('owner')
    const scoped = await owner.product.twins.list({ orgId: org.orgId, scope: 'all', limit: 200 })
    assert.ok(scoped.rows.length > 0, 'the filter returned nothing at all')
    assert.deepEqual(
      [...new Set(scoped.rows.map((t) => t.orgId))],
      [org.orgId],
      'the twins list returned an organization that was not asked for',
    )

    const runs = await owner.product.runs.list({ kind: 'agent', orgId: org.orgId, limit: 200 })
    assert.ok(runs.rows.length > 0)
    assert.deepEqual([...new Set(runs.rows.map((r) => r.orgId))], [org.orgId])

    // And the unfiltered call is not accidentally scoped, or the assertion
    // above would hold for a route that ignores its input entirely.
    const everything = await owner.product.twins.list({ scope: 'all', limit: 200 })
    assert.ok(
      new Set(everything.rows.map((t) => t.orgId)).size >= 1,
      'the unfiltered list returned nothing, so the check above proves nothing',
    )
  })

  it('a page cursor it did not issue is refused rather than answered with page one', async () => {
    // Falling back to the first page is the failure that matters: the caller
    // asked for page four, got page one, and `More` would offer more forever.
    await assert.rejects(
      () => callerAs('owner').product.twins.list({ cursor: 'not-a-cursor', limit: 5 }),
      /not one this list issued/,
    )
  })

  it('the twin detail carries its runs, its golden version and its teardowns', async () => {
    const twin = await callerAs('owner').product.twins.get({ id: overdueEnvId })
    assert.equal(twin.orgSlug, org.slug)
    assert.equal(twin.golden?.verified, false)
    assert.equal(twin.runs.length, 2)
    assert.equal(twin.workloadRuns.length, 1)
    assert.deepEqual(twin.teardowns, [])
  })

  it('a run that finished with no verdict is unknown, and never passed', async () => {
    // The whole reason `standing` exists beside `state`. Both runs reached
    // `complete`; one found a failure and one reported nothing at all, and
    // calling the second a pass is the defect this repository has shipped once.
    const rows = (
      await callerAs('owner').product.runs.list({ kind: 'agent', orgId: org.orgId, limit: 50 })
    ).rows
    const failed = rows.find((r) => r.id === failingRunId)
    const silent = rows.find((r) => r.id === silentRunId)
    assert.equal(failed?.state, 'complete')
    assert.equal(failed?.standing, 'failed')
    assert.equal(failed?.failure, 'The basket never loaded')
    assert.equal(silent?.state, 'complete')
    assert.equal(silent?.standing, 'unknown')
    assert.equal(silent?.verdict, null)
  })

  it('a load run that succeeded with a failing verdict is a failure', async () => {
    const rows = (
      await callerAs('owner').product.runs.list({ kind: 'load', orgId: org.orgId, limit: 50 })
    ).rows
    const run = rows.find((r) => r.id === loadRunId)
    assert.equal(run?.state, 'succeeded')
    assert.equal(run?.standing, 'failed')
    assert.equal(run?.failure, 'threshold.p95')
  })

  it('the failures filter is applied by the query and not after the page', async () => {
    // Filtering after the page is cut returns an arbitrary number of rows per
    // page and eventually an empty page with a cursor behind it, which reads as
    // the end of a list that has more in it.
    const failedOnly = await callerAs('owner').product.runs.list({
      kind: 'agent', orgId: org.orgId, failedOnly: true, limit: 50,
    })
    assert.ok(failedOnly.rows.some((r) => r.id === failingRunId))
    assert.ok(
      !failedOnly.rows.some((r) => r.id === silentRunId),
      'a run that reported nothing was counted as a failure',
    )
  })

  it('the run detail carries the verdicts and the artifacts, and no storage key', async () => {
    const run = await callerAs('owner').product.runs.get({ kind: 'agent', id: failingRunId })
    assert.equal(run.kind, 'agent')
    if (run.kind !== 'agent') return
    assert.equal(run.verdicts.length, 1)
    assert.equal(run.verdicts[0]!.summary, 'The basket never loaded')
    assert.equal(run.artifacts.length, 1)
    // The bytes were removed and the row stayed, so the page can say so rather
    // than showing a gap that reads as a bug.
    assert.equal(run.artifacts[0]!.retained, false)
    assert.ok(
      !JSON.stringify(run.artifacts).includes('storage_key') &&
        !JSON.stringify(run.artifacts).includes('runs/x/trace.zip'),
      'the artifact response carries a pointer into the object store',
    )
  })

  it('a load run detail carries the command the engine reported', async () => {
    const run = await callerAs('owner').product.runs.get({ kind: 'load', id: loadRunId })
    assert.equal(run.kind, 'load')
    if (run.kind !== 'load') return
    assert.equal(run.reproduceCommand, 'af load run checkout-load')
    assert.equal((run.result as { requests?: number } | null)?.requests, 1200)
  })

  it('a pull request check detail names the head it ran on', async () => {
    const run = await callerAs('owner').product.runs.get({ kind: 'check', id: checkId })
    assert.equal(run.kind, 'check')
    if (run.kind !== 'check') return
    assert.equal(run.pullRequest, 41)
    assert.equal(run.standing, 'failed')
    assert.equal(run.headSha, 'b'.repeat(40))
    // Null rather than absent: the installation does not hold `checks: write`,
    // which is a state to serve rather than crash in.
    assert.equal(run.checkRunId, null)
  })

  it('a branch still holding a twin after its pull request merged is orphaned', async () => {
    // The finding the branches page exists for, and the one no column states
    // directly: the twin is fine, the pull request is fine, and the two of them
    // together are somebody paying for a fortnight-old change.
    const rows = (
      await callerAs('owner').product.twins.branches({ orgId: org.orgId, scope: 'orphaned', limit: 50 })
    ).rows
    const branch = rows.find((b) => b.branch === 'feature/late')
    assert.ok(branch, 'the merged pull request holding a live twin was not found')
    assert.equal(branch.orphaned, true)
    assert.equal(branch.pullRequest, 41)
    assert.equal(branch.pullRequestState, 'merged')
    assert.equal(branch.live, 1)
    assert.equal(branch.overdue, 1)
  })

  it('the safe state routes find the unverified version and the unconfirmed rule', async () => {
    const owner = callerAs('owner')
    const goldens = await owner.product.data.goldens({ orgId: org.orgId, scope: 'unverified', limit: 50 })
    const golden = goldens.rows.find((g) => g.version === 'v7')
    assert.ok(golden)
    assert.equal(golden.verified, false)
    // Live twins on an unverified copy is what turns the row from history into
    // a finding, so it is counted rather than inferred.
    assert.equal(golden.twins, 1)

    const rules = await owner.product.data.masking({ orgId: org.orgId, scope: 'unconfirmed', limit: 50 })
    const rule = rules.rows.find((r) => r.column === 'email')
    assert.ok(rule)
    assert.equal(rule.confirmed, false)
    assert.equal(rule.table, 'customers')
  })

  it('analytics reads the product and is refused the customer column names', async () => {
    // The split, exercised through the gate rather than asserted off the table.
    const analytics = callerAs('analytics')
    const twins = await analytics.product.twins.list({ orgId: org.orgId, limit: 5 })
    assert.ok(Array.isArray(twins.rows))
    await assert.rejects(
      () => analytics.product.data.masking({ orgId: org.orgId, limit: 5 }),
      /needs the admin.product.data.read permission/,
    )
  })

  it('reading is recorded without the route asking', async () => {
    // adminProcedure audits every query after it returns, so a lane that added
    // no adminAudit call of its own still leaves a trail. Counted rather than
    // trusted.
    //
    // MATCHED ON THE SUFFIX, because a tRPC path is relative to the router the
    // caller was built from: through `adminRouter` it is `product.twins.list`
    // and over HTTP through `appRouter` it is `admin.product.twins.list`. The
    // suffix is the part that is the same in both, and pinning the full string
    // here would pin the harness rather than the route.
    const count = async () => {
      const rows = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM admin_audit_entries
        WHERE admin_user_id = ${operatorId} AND action LIKE ${'read.%product.twins.list'}`
      return Number(rows[0]!.n)
    }
    const before = await count()
    await callerAs('owner').product.twins.list({ orgId: org.orgId, limit: 5 })
    assert.equal(await count(), before + 1)
  })
})
