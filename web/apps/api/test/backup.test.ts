import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import postgres from 'postgres'
import { sql } from 'drizzle-orm'
import { createPool } from '@antifailure/db'
import { migrate, migrationsDir } from '@antifailure/db'
import { seedOrg } from './harness.ts'
import {
  CONTAINER, dropDatabasesNamed, keyFor, start as startPostgres, url as drUrl,
} from './pgcontainer.ts'
import {
  APP_ROLE,
  backup,
  compareRestored,
  describe as describeDatabase,
  rehearse,
  restore,
} from '../src/backup.ts'
import { prepareScratch } from '../src/backup-scratch.ts'

// The disaster recovery drill.
//
// Every test declares a ten minute timeout. That is not slack for a slow
// assertion: each one drives pg_dump and pg_restore against a real cluster, and
// measured on a machine running eleven other agents a single restore of a small
// database took between twenty and a hundred and sixty seconds. Declaring it in
// the file rather than passing --test-timeout means continuous integration does
// not have to know, and a timeout here is a real hang rather than a busy
// laptop.
//
// A backup nobody has restored is a file. These tests are the restore, run for
// real against a real Postgres: the dump is taken, a second database is created
// from it, and the result is compared against what went in and then asked to
// behave.
//
// The comparison is not decoration. pg_restore exits zero over a GRANT that
// failed because the role does not exist, and over policies restored onto a
// table whose row level security it could not enable. Both produce a control
// plane that starts, answers every request, and isolates nothing. That is the
// failure this suite exists to catch, and the last test catches it the only way
// that really counts: by asking the restored database, through the
// unprivileged application role, to read another tenant's rows.

// This suite runs against a Postgres of its own rather than the shared
// development container. It creates and drops databases, which disturbs anybody
// else on the same cluster, and it takes long enough that somebody recreating
// the shared container mid-run is a real event rather than a theoretical one.
// It was, twice, and both times the failure arrived as ECONNRESET halfway
// through a restore, which reads as a bug in the restore.
let h: postgres.Sql | null = null
let workDir: string

before(async () => {
  if (!(await startPostgres())) return
  h = postgres(drUrl(), { max: 4, connect_timeout: 30, onnotice: () => {} })
  await migrate(h)
  await h.unsafe(`ALTER ROLE ${APP_ROLE} LOGIN PASSWORD 'app-test-password'`)
  workDir = await mkdtemp(path.join(tmpdir(), 'af-dr-'))
})

after(async () => {
  if (!h) return
  await dropDatabasesNamed('af_dr_')
  if (workDir) await rm(workDir, { recursive: true, force: true })
  await h.end({ timeout: 5 })
})

function skipWithoutDatabase(t: { skip: (reason: string) => void }): boolean {
  if (!h) {
    // The only honest reason to skip. Every other failure throws, because a
    // skip the code under test can cause is a pass with extra steps.
    t.skip('no Docker daemon, so there is nowhere to stand up a database to restore into')
    return true
  }
  return false
}

function targetName(): string {
  return `af_dr_${randomUUID().replace(/-/g, '').slice(0, 12)}`
}

function adminUrlOf(): string {
  return drUrl()
}

function appUrl(): string {
  const u = new global.URL(drUrl())
  u.username = APP_ROLE
  u.password = 'app-test-password'
  return u.toString()
}

test('a backup records what a restore has to reproduce', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  const org = await seedOrg(h!, 'dr-manifest')
  // The audit log is a hash chain, and its head is the one tamper-evident
  // thing in the system. seedOrg does not write one, so the backup would have
  // nothing to record and the assertion below would be vacuous.
  await h!`
    INSERT INTO audit_entries (org_id, actor_label, action, target_type, origin, entry_hash)
    VALUES (${org.orgId}, 'dr', 'environment.created', 'environment', 'web', 'seed-dr-manifest')`

  const result = await backup({ adminUrl: adminUrlOf(), outDir: workDir, label: 'manifest' })
  const manifest = result.manifest

  assert.ok(manifest.bytes > 0, 'the dump is empty')
  assert.match(manifest.sha256, /^[0-9a-f]{64}$/)
  assert.ok(manifest.schemaVersion > 0, 'no migrations were recorded')

  // The three things a restore silently loses.
  assert.ok(manifest.rlsEnabled.length > 5, 'no tables have row level security recorded')
  assert.ok(
    manifest.rlsForced.includes('organizations'),
    'FORCE is not recorded, and without it the table owner bypasses every policy',
  )
  assert.ok(
    (manifest.policies['environments'] ?? []).length > 0,
    'no policies were recorded for environments',
  )
  assert.ok(
    (manifest.grants['environments'] ?? []).includes('SELECT'),
    `no grants recorded for ${APP_ROLE}, so a restore could not tell they were missing`,
  )
  assert.ok(Object.keys(manifest.auditHeads).length > 0, 'no audit chain head recorded')

  // Roles are cluster-level and are not in the dump. Without this file a
  // restore into a fresh cluster has no application role at all.
  const roles = await readFile(result.rolesPath, 'utf8')
  assert.match(roles, new RegExp(`ROLE ${APP_ROLE}`), 'the application role is not in the roles file')
})

test('a restore reproduces the schema, the rows, the policies and the grants', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  await seedOrg(h!, 'dr-restore')

  const taken = await backup({ adminUrl: adminUrlOf(), outDir: workDir, label: 'restore' })
  const target = targetName()
  const result = await restore({
    adminUrl: adminUrlOf(),
    targetDatabase: target,
    dumpPath: taken.dumpPath,
    rolesPath: taken.rolesPath,
    manifestPath: taken.manifestPath,
  })

  assert.deepEqual(
    result.problems,
    [],
    'the restored database differs from the backup, so this backup is not one',
  )
  assert.ok(result.seconds > 0)
})

// The one that matters. Structure can match while behaviour does not: policies
// present but not enabled, enabled but not forced, forced but the application
// role owns the table. So the restored database is asked to do the thing the
// whole control plane rests on, through the role that actually connects.
test('the restored database still refuses a cross-tenant read', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  const mine = await seedOrg(h!, 'dr-mine')
  const theirs = await seedOrg(h!, 'dr-theirs')

  const taken = await backup({ adminUrl: adminUrlOf(), outDir: workDir, label: 'isolation' })
  const target = targetName()
  const result = await restore({
    adminUrl: adminUrlOf(),
    targetDatabase: target,
    dumpPath: taken.dumpPath,
    rolesPath: taken.rolesPath,
    manifestPath: taken.manifestPath,
    appPassword: 'app-test-password',
  })
  assert.deepEqual(result.problems, [])

  const restoredAppUrl = (() => {
    const u = new global.URL(appUrl())
    u.pathname = '/' + target
    return u.toString()
  })()
  const pool = createPool({ url: restoredAppUrl, max: 2, connectTimeoutSeconds: 10 })
  try {
    const own = await pool.withTenant({ orgId: mine.orgId }, async (db) =>
      // Deliberately unqualified. A WHERE clause would prove the query is
      // careful; the policy is what has to be careful.
      db.execute<{ id: string }>(sql`SELECT id FROM environments`),
    )
    assert.equal(own.length, 1, 'the restored database returned nothing for its own tenant')

    const other = await pool.withTenant({ orgId: theirs.orgId }, async (db) =>
      db.execute<{ id: string }>(sql`SELECT id FROM environments`),
    )
    assert.equal(other.length, 1)
    assert.notEqual(
      own[0]?.id,
      other[0]?.id,
      'two tenants saw the same row, so the restore lost row level security and every ' +
        'customer can read every other customer',
    )
  } finally {
    await pool.close()
  }
})

// The drill an operator runs on a schedule. It has to report a number, because
// "we have backups" is not a recovery time objective and a runbook that quotes
// a number nobody measured is a runbook that will be wrong on the day.
test('the drill measures a recovery time and finds nothing wrong', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  // Two, not one. The drill's last check asks one tenant to read another's
  // rows, and with a single organization in the database there is no other
  // tenant to be refused: the check would report that it could not run, which
  // this test would then fail on. Depending on the organizations earlier tests
  // in this file happen to have left behind would make that a property of the
  // order the runner picked.
  await seedOrg(h!, 'dr-drill-one')
  await seedOrg(h!, 'dr-drill-two')

  const lines: string[] = []
  const drill = await rehearse({
    adminUrl: adminUrlOf(),
    outDir: workDir,
    label: 'drill',
    targetDatabase: targetName(),
    appPassword: 'app-test-password',
    log: (line) => lines.push(line),
  })

  assert.deepEqual(drill.problems, [], lines.join('\n'))
  assert.ok(drill.recoveryTimeSeconds > 0, 'the drill measured nothing')
  assert.equal(drill.overBudget, null, 'no budget was given, so nothing can be over one')

  // The drill has to have ASKED. Everything else it does compares catalogue
  // text against catalogue text, and all of that passes over a database which
  // answers every query and isolates nothing.
  assert.equal(
    drill.isolation.attempted,
    true,
    `the drill never attempted the cross-tenant read: ${
      drill.isolation.attempted ? '' : drill.isolation.reason
    }`,
  )
  assert.ok(
    drill.isolation.attempted && drill.isolation.tables.includes('environments'),
    'the cross-tenant read did not reach environments',
  )
  assert.equal(
    drill.recoveryTimeSeconds,
    drill.restoreSeconds,
    'the recovery time must be the restore alone; recovery starts from a backup that ' +
      'already exists, and counting the time to take one flatters the number',
  )
  assert.ok(drill.bytes > 0)

  // Printed, so every run of this suite records a measurement rather than only
  // asserting that one exists. A runbook that quotes a recovery time nobody
  // measured is a runbook that will be wrong on the day, and the only way the
  // number stays honest is if it is re-measured every time the test runs.
  console.log(
    `[drill] restore ${drill.restoreSeconds.toFixed(1)}s, backup ` +
      `${drill.backupSeconds.toFixed(1)}s, ${drill.bytes} bytes`,
  )
  await writeFile(
    path.join(workDir, 'last-drill.json'),
    JSON.stringify(drill, null, 2) + '\n',
    'utf8',
  )
})

// Restoring over a live database is not a recovery, it is an outage with a
// different cause. The refusal is the feature.
test('a restore refuses to write over a database that already exists', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  const taken = await backup({ adminUrl: adminUrlOf(), outDir: workDir, label: 'over' })
  await assert.rejects(
    () =>
      restore({
        adminUrl: adminUrlOf(),
        targetDatabase: 'antifailure',
        dumpPath: taken.dumpPath,
      }),
    /already exists/,
  )
})

// Every check in the comparison has to be able to fail, or the drill is
// ceremony. This breaks the restored database in the three ways that matter and
// asserts the comparison notices each one.
test('the comparison notices row level security that did not come back', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  const org = await seedOrg(h!, 'dr-negative')
  // Rows in the tables this test is about to break, so that "came back short"
  // is a difference rather than zero against zero.
  const [run] = await h!<{ id: string }[]>`
    INSERT INTO runs (org_id, environment_id, kind, state)
    SELECT ${org.orgId}, id, 'test', 'complete' FROM environments WHERE org_id = ${org.orgId}
    RETURNING id`
  await h!`
    INSERT INTO verdicts (org_id, run_id, workflow, value, summary)
    VALUES (${org.orgId}, ${run!.id}, 'sign-up', 'pass', 'seeded for the negative control')`
  await h!`
    INSERT INTO artifacts (org_id, run_id, kind, storage_key)
    VALUES (${org.orgId}, ${run!.id}, 'screenshot', 'file:///seeded.png')`

  const taken = await backup({ adminUrl: adminUrlOf(), outDir: workDir, label: 'negative' })
  const target = targetName()
  const result = await restore({
    adminUrl: adminUrlOf(),
    targetDatabase: target,
    dumpPath: taken.dumpPath,
    rolesPath: taken.rolesPath,
    manifestPath: taken.manifestPath,
  })
  assert.deepEqual(result.problems, [], 'the restore was not clean to begin with')

  const broken = postgres(result.targetUrl, { max: 1, onnotice: () => {} })
  try {
    await broken.unsafe('ALTER TABLE environments NO FORCE ROW LEVEL SECURITY')
    await broken.unsafe('ALTER TABLE runs DISABLE ROW LEVEL SECURITY')
    await broken.unsafe('DELETE FROM verdicts')
    await broken.unsafe(`REVOKE SELECT ON artifacts FROM ${APP_ROLE}`)

    const manifest = JSON.parse(await readFile(taken.manifestPath, 'utf8'))
    const after = await describeDatabase(broken)
    const problems = compareRestored(manifest, after)

    assert.ok(
      problems.some((p) => p.includes('FORCED') && p.includes('environments')),
      `a table that lost FORCE was not reported: ${problems.join('; ')}`,
    )
    assert.ok(
      problems.some((p) => p.includes('not enabled') && p.includes('runs')),
      `a table that lost row level security was not reported: ${problems.join('; ')}`,
    )
    assert.ok(
      problems.some((p) => p.includes('verdicts') && p.includes('rows')),
      `a table that came back short was not reported: ${problems.join('; ')}`,
    )
    assert.ok(
      problems.some((p) => p.includes('artifacts') && p.includes('SELECT')),
      `a missing grant was not reported: ${problems.join('; ')}`,
    )
  } finally {
    await broken.end({ timeout: 5 })
  }
})


// Every check in this file reads the public schema, where all of the control
// plane's tables live today. That scope is an assumption, and the failure it
// invites is the one lane 3 named: an assertion scoped to a collection that
// excludes the casualty passes forever and discovers nothing.
//
// So the scope is reported rather than assumed. A table outside public is not
// treated as a restore failure, because it may well have restored perfectly.
// It is treated as something this verification cannot speak for, which is the
// honest answer and the one that makes somebody widen the check.
test('a table this check does not look at is named rather than ignored', async (t) => {
  // Guarded like its eleven neighbours. It always needed a database and never
  // said so, so on a machine with no Docker this one failed on a connection
  // while the rest of the file skipped, which reads as a defect in the check.
  if (skipWithoutDatabase(t)) return
  const admin = postgres(drUrl(), { max: 1, onnotice: () => {} })
  t.after(async () => {
    await admin.unsafe('DROP SCHEMA IF EXISTS ledger CASCADE').catch(() => {})
    await admin.end({ timeout: 5 })
  })

  const before = await describeDatabase(admin)
  assert.deepEqual(
    before.unverifiedTables,
    [],
    'the control plane grew a table outside public and this suite has not been widened to check it',
  )

  await admin.unsafe('CREATE SCHEMA ledger')
  await admin.unsafe('CREATE TABLE ledger.entries (id int primary key)')

  const after = await describeDatabase(admin)
  assert.deepEqual(after.unverifiedTables, ['ledger.entries'])

  // Present in the restored database and absent from the backup: reported as
  // something the check cannot account for.
  const problems = compareRestored({ ...before, ...blankManifestFields() }, after)
  assert.ok(
    problems.some((p) => p.includes('ledger.entries') && p.includes('cannot account for')),
    `a table outside public was not reported: ${problems.join('; ')}`,
  )

  // Present in the backup: reported as something never verified.
  const reverse = compareRestored({ ...after, ...blankManifestFields() }, after)
  assert.ok(
    reverse.some((p) => p.includes('ledger.entries') && p.includes('never looked at it')),
    `a table outside public in the backup was not reported: ${reverse.join('; ')}`,
  )
})

/** The manifest fields describe() does not produce, for tests that build one
 *  from a description rather than from a real backup. */
function blankManifestFields() {
  return {
    createdAt: '',
    database: '',
    serverVersion: '',
    clientVersion: '',
    sha256: '',
    bytes: 0,
  }
}

// Lane 4 asked every lane the same question tonight: for every destructive
// operation you own, what PROVES the thing you are about to destroy is yours?
// The drill drops a database. This is the answer, as a test rather than as an
// argument.
//
// The drill has always been safe here, because restore refuses a database that
// already exists and the drop sits after it. But that is a property of two
// functions being written in a particular order, and the first person to wrap
// the restore in a try/catch to make the drill "more robust" would turn it into
// something that deletes a live database, with nothing going red. So the drop
// now takes the name restore reports having CREATED, which does not exist on
// the path where nothing was created, and this asserts the observable end of
// it: the occupied database and its contents are still there afterwards.
test('a drill refuses a database it did not create, and leaves it standing', async (t) => {
  if (skipWithoutDatabase(t)) return
  const occupied = `af_occupied_${randomUUID().replace(/-/g, '').slice(0, 12)}`
  const admin = postgres(drUrl(), { max: 1, onnotice: () => {} })
  t.after(async () => {
    await admin.unsafe(`DROP DATABASE IF EXISTS "${occupied}" WITH (FORCE)`).catch(() => {})
    await admin.end({ timeout: 5 })
  })

  await admin.unsafe(`CREATE DATABASE "${occupied}"`)
  const inside = postgres(urlInto(occupied), { max: 1, onnotice: () => {} })
  try {
    await inside.unsafe('CREATE TABLE not_yours (id int primary key)')
    await inside.unsafe('INSERT INTO not_yours VALUES (1), (2), (3)')
  } finally {
    await inside.end({ timeout: 5 })
  }

  const workDir = await mkdtemp(path.join(tmpdir(), 'af-drill-occupied-'))
  t.after(() => rm(workDir, { recursive: true, force: true }))

  await assert.rejects(
    rehearse({
      adminUrl: adminUrlOf(),
      outDir: workDir,
      label: 'drill',
      targetDatabase: occupied,
      appPassword: 'app-test-password',
    }),
    /exist/i,
    'the drill accepted a database it did not create',
  )

  // The refusal is not the point. This is.
  const still = await admin<{ n: string }[]>`
    SELECT count(*)::text AS n FROM pg_database WHERE datname = ${occupied}`
  assert.equal(still[0]?.n, '1', 'the drill destroyed a database it did not create')

  const survivor = postgres(urlInto(occupied), { max: 1, onnotice: () => {} })
  try {
    const rows = await survivor<{ n: string }[]>`SELECT count(*)::text AS n FROM not_yours`
    assert.equal(rows[0]?.n, '3', 'the database survived but its contents did not')
  } finally {
    await survivor.end({ timeout: 5 })
  }
})

// The drill has to be able to go red, or it is a list of assertions that might
// all be vacuous. These are the two breaks that matter, and both are made in
// the SOURCE database rather than in the restored one, which is the whole
// point: the manifest is taken from the source, so a source whose isolation is
// already broken produces a manifest recording the breakage and a restore that
// matches it perfectly. Every structural check in this module passes. Only the
// behavioural one notices, and until it ran inside the drill nothing would
// have.
//
// Both restore the policy afterwards, through t.after so that a failure part
// way does not leave the rest of this file running against a database with no
// tenant isolation in it.

async function restoreEnvironmentPolicy(): Promise<void> {
  await h!.unsafe('DROP POLICY IF EXISTS tenant_isolation ON environments')
  await h!.unsafe(`
    CREATE POLICY tenant_isolation ON environments
      FOR ALL TO ${APP_ROLE}
      USING (org_id = current_org())
      WITH CHECK (org_id = current_org())`)
}

test('a drill goes red when the policy is there and lets every tenant through', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  await seedOrg(h!, 'dr-permissive-one')
  await seedOrg(h!, 'dr-permissive-two')
  t.after(restoreEnvironmentPolicy)

  // The nastiest shape of this failure: the policy is present, named the same
  // thing, on a table whose row level security is enabled and forced. Every
  // catalogue comparison in this file matches. It isolates nothing.
  await h!.unsafe('DROP POLICY tenant_isolation ON environments')
  await h!.unsafe(`
    CREATE POLICY tenant_isolation ON environments
      FOR ALL TO ${APP_ROLE} USING (true) WITH CHECK (true)`)

  const drill = await rehearse({
    adminUrl: adminUrlOf(),
    outDir: workDir,
    label: 'permissive',
    targetDatabase: targetName(),
    appPassword: 'app-test-password',
  })

  assert.ok(
    drill.problems.some((p) => p.includes('environments') && p.includes('isolates nothing')),
    `the drill did not notice a policy that lets every tenant through: ${drill.problems.join('; ')}`,
  )
  assert.ok(
    drill.problems.some((p) => p.includes('no organization set')),
    `an unscoped connection read every tenant and the drill did not say so: ${drill.problems.join('; ')}`,
  )
  // The half that proves this test is worth having. Nothing structural fired:
  // the policy name is in the manifest, it is in the restored database, the
  // row counts match, the grants match. A drill without the behavioural check
  // would have reported this backup sound.
  assert.deepEqual(
    drill.problems.filter((p) => !p.includes(APP_ROLE)),
    [],
    'a structural check fired, which would make this test pass for the wrong reason',
  )
})

test('a drill goes red when the policy did not come back at all', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  await seedOrg(h!, 'dr-dropped-one')
  await seedOrg(h!, 'dr-dropped-two')
  t.after(restoreEnvironmentPolicy)

  // Row level security stays enabled and forced, and there is now nothing
  // behind it, so the table refuses everything. The first version of the
  // behavioural check only looked for leakage and walked straight past this:
  // nothing came back, so nothing leaked, so the drill was green over a table
  // the application can no longer read.
  await h!.unsafe('DROP POLICY tenant_isolation ON environments')

  const drill = await rehearse({
    adminUrl: adminUrlOf(),
    outDir: workDir,
    label: 'dropped',
    targetDatabase: targetName(),
    appPassword: 'app-test-password',
  })

  assert.ok(
    drill.problems.some(
      (p) => p.includes('environments') && p.includes('cannot read'),
    ),
    `the drill did not notice a dropped policy: ${drill.problems.join('; ')}`,
  )
})

// A drill that could not attempt the cross-tenant read is a drill that proved
// nothing about isolation, and the one thing it must not do is look like a
// clean run.
test('a drill that cannot connect as the application says so rather than passing', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  await seedOrg(h!, 'dr-nopassword')

  const drill = await rehearse({
    adminUrl: adminUrlOf(),
    outDir: workDir,
    label: 'nopassword',
    targetDatabase: targetName(),
    // Deliberately absent.
    appPassword: undefined,
  })

  assert.equal(drill.isolation.attempted, false)
  assert.ok(
    drill.problems.some((p) => p.includes('never attempted')),
    `a drill that skipped the behavioural check reported: ${drill.problems.join('; ') || 'nothing'}`,
  )
})

// The recovery time is the number the runbook quotes, so a regression in it has
// to be able to fail a run. It is reported apart from the problems on purpose:
// a slow restore and a restore that isolates nothing are not the same finding,
// and a caller that prints them the same way teaches whoever reads the failure
// that "this backup is not one" sometimes means the machine was busy.
test('a drill over its recovery time budget reports it, and not as a broken backup', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  await seedOrg(h!, 'dr-budget-one')
  await seedOrg(h!, 'dr-budget-two')

  const drill = await rehearse({
    adminUrl: adminUrlOf(),
    outDir: workDir,
    label: 'budget',
    targetDatabase: targetName(),
    appPassword: 'app-test-password',
    // Below any real restore. A budget expressed as a fraction of the measured
    // time would be a test that cannot fail.
    maxRestoreSeconds: 0.000001,
  })

  assert.deepEqual(drill.problems, [], 'the backup was sound; only the clock was over')
  assert.ok(drill.overBudget, 'a restore slower than its budget was not reported')
  assert.equal(drill.overBudget?.budgetSeconds, 0.000001)
  assert.equal(drill.overBudget?.seconds, drill.restoreSeconds)

  const generous = await rehearse({
    adminUrl: adminUrlOf(),
    outDir: workDir,
    label: 'budget-ok',
    targetDatabase: targetName(),
    appPassword: 'app-test-password',
    maxRestoreSeconds: 86_400,
  })
  assert.equal(generous.overBudget, null, 'a restore inside its budget was reported as over it')
})

// The scratch database the scheduled drill runs against. Against an empty
// Postgres every comparison in this module passes over nothing and the
// cross-tenant read has no other tenant to be refused, which is a green run
// that examined nothing.
test('a scratch database comes out with two tenants that own rows', { timeout: 600_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return
  const name = `af_dr_scratch_${randomUUID().replace(/-/g, '').slice(0, 8)}`
  const admin = postgres(drUrl(), { max: 1, onnotice: () => {} })
  t.after(async () => {
    await admin.unsafe(`DROP DATABASE IF EXISTS "${name}" WITH (FORCE)`).catch(() => {})
    await admin.end({ timeout: 5 })
  })
  await admin.unsafe(`CREATE DATABASE "${name}"`)

  const { orgIds } = await prepareScratch(urlInto(name), { appPassword: 'app-test-password' })
  assert.equal(orgIds.length, 2)

  const seeded = postgres(urlInto(name), { max: 1, onnotice: () => {} })
  try {
    const rows = await seeded<{ org_id: string }[]>`
      SELECT DISTINCT org_id::text AS org_id FROM environments ORDER BY 1`
    assert.deepEqual(rows.map((r) => r.org_id).sort(), [...orgIds].sort())
    // Without an audit entry per organization the manifest records an empty
    // chain and the restore's audit comparison is a loop over nothing.
    const heads = await seeded<{ n: string }[]>`
      SELECT count(DISTINCT org_id)::text AS n FROM audit_entries`
    assert.equal(heads[0]?.n, '2')
  } finally {
    await seeded.end({ timeout: 5 })
  }

  // Running it again must refuse. The only way this tool can hurt anybody is
  // by being pointed at a database that already holds real organizations, and
  // the refusal asks the database what it contains rather than whether its
  // name looks like a scratch one.
  await assert.rejects(
    prepareScratch(urlInto(name), { appPassword: 'app-test-password' }),
    /already holds/,
  )
})

test('a scratch database with one organization is refused before it connects', async () => {
  // Deliberately a URL that names no cluster. The refusal has to come from the
  // argument rather than from the database, and pointing this at the suite's
  // own Postgres hid that: it would have passed just as well with the check
  // moved after the connection, and it would have needed Docker to say so.
  await assert.rejects(
    prepareScratch('postgres://nobody@127.0.0.1:1/never', { orgs: 1 }),
    /at least two organizations/,
    'one tenant cannot be isolated from anybody, and a drill against it refuses nothing',
  )
})

/** The admin URL pointed at one particular database. */
function urlInto(database: string): string {
  const u = new URL(drUrl())
  u.pathname = '/' + database
  return u.toString()
}

// ---------------------------------------------------------------------------
// The cluster this drill runs on, which is the thing that was wrong about it
// ---------------------------------------------------------------------------
//
// These are not about backup.ts. They are about whether anything above them
// means what it says, and they exist because for a while nothing above them
// did: the suite ran on one machine-wide container, so the database it dumped
// carried four branches' migrations at once and the drill certified a schema
// that was on no branch.
//
// Each one is written as a property of the cluster rather than of the code, so
// a future change that puts this back on a shared cluster fails here and names
// the reason, instead of going quietly green over somebody else's schema.

test('the cluster belongs to this checkout and not to the machine', { timeout: 60_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return

  // Two different checkouts must not be able to land on one container. Proved
  // over the pure function rather than by standing up a second container,
  // which is the only part of this that needs proving: the name is the whole
  // of the identity.
  assert.notEqual(
    keyFor('/some/other/checkout'),
    keyFor('/a/third/checkout'),
    'two checkouts share a container key, so they would share a cluster',
  )
  assert.match(CONTAINER, /^af-dr-[0-9a-f]{12}$/)
  assert.notEqual(
    CONTAINER,
    'af-dr-test',
    'this is the machine-wide container the suite used to share, and sharing it is the defect',
  )

  // And the URL in hand really is that container's, asked of Docker rather
  // than assumed from a number written down here. This is the assertion a
  // hardcoded port cannot make: with one, a stranger already listening on it
  // answers the probe and the whole suite runs against their cluster, and
  // every test above passes while proving nothing about this branch.
  //
  // Read out of `docker ps` rather than through --filter publish=, which
  // matches the CONTAINER port and answers nothing for a host port. Checked by
  // running it.
  const port = new URL(drUrl()).port
  const { stdout } = await promisify(execFile)('docker', [
    'ps', '--format', '{{.Names}}|{{.Ports}}',
  ])
  const owners = stdout
    .split('\n')
    .map((line) => line.trim().split('|'))
    .filter(([, ports]) => (ports ?? '').includes(`:${port}->`))
    .map(([name]) => name)
  assert.deepEqual(
    owners,
    [CONTAINER],
    `host port ${port} is published by ${owners.join(', ') || 'nothing docker can see'} rather ` +
      `than by this checkout's container alone`,
  )

  // The other half of the same property, from the container's end: this is
  // where the URL came from. A restart republishes on a different host port,
  // so a URL captured before one points at nothing, or on a busy machine at
  // whoever took the number. Measured: stopping and starting this container
  // moved it from 62172 to 49741.
  const mapping = await promisify(execFile)('docker', ['port', CONTAINER, '5432'])
  assert.ok(
    mapping.stdout.includes(`:${port}`),
    `${CONTAINER} publishes ${mapping.stdout.trim()}, not the port this suite is connected to`,
  )

  // And it says whose it is. The name is a hash, so without this an abandoned
  // checkout leaves a Postgres nobody can attribute and therefore nobody
  // removes. One per checkout is the cost of this design and the label is what
  // keeps that cost payable.
  const labelled = await promisify(execFile)('docker', [
    'inspect', '-f', '{{index .Config.Labels "af.checkout"}}', CONTAINER,
  ])
  assert.ok(
    labelled.stdout.trim().length > 0,
    `${CONTAINER} carries no af.checkout label, so an orphan of it could not be attributed`,
  )
})

test('the ledger holds this checkout\'s migrations and nobody else\'s', { timeout: 60_000 }, async (t) => {
  if (skipWithoutDatabase(t)) return

  // THE ASSERTION THIS WHOLE CHANGE IS FOR.
  //
  // schema_migrations records each migration BY NAME. On the shared container
  // that ledger accumulated every checkout's, so this database represented no
  // branch, and a drill that certifies a schema belonging to no branch has
  // certified nothing. It also made a renamed migration fatal: the runner sees
  // the new name as unapplied, runs it, and dies on "already exists".
  //
  // So the ledger is compared against the migrations directory on disk. Equal
  // means the database under the drill is this branch's database.
  const onDisk = (await readdir(migrationsDir)).filter((f) => f.endsWith('.sql')).sort()
  const applied = (await h!<{ name: string }[]>`
    SELECT name FROM schema_migrations ORDER BY name`).map((r) => r.name)

  assert.ok(onDisk.length > 0, 'no migrations on disk, so this comparison proves nothing')
  const strangers = applied.filter((name) => !onDisk.includes(name))
  assert.deepEqual(
    strangers,
    [],
    'the ledger holds migrations that are not in this checkout, so this cluster is shared and ' +
      'the schema being dumped belongs to no branch',
  )
  assert.deepEqual(
    applied,
    onDisk,
    'the ledger and the migrations directory disagree, so the drill is dumping a schema that is ' +
      'not the one this branch describes',
  )
})
