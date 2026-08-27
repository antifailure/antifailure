import { test, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { sql } from 'drizzle-orm'
import { createPool } from '@antifailure/db'
import { migrate } from '@antifailure/db'
import { seedOrg } from './harness.ts'
import { URL as DR_URL, dropDatabasesNamed, start as startPostgres } from './pgcontainer.ts'
import {
  APP_ROLE,
  backup,
  compareRestored,
  describe as describeDatabase,
  rehearse,
  restore,
} from '../src/backup.ts'

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
  h = postgres(DR_URL, { max: 4, connect_timeout: 30, onnotice: () => {} })
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
  return DR_URL
}

function appUrl(): string {
  const u = new global.URL(DR_URL)
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
  await seedOrg(h!, 'dr-drill')

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
  assert.equal(
    drill.recoveryTimeSeconds,
    drill.restoreSeconds,
    'the recovery time must be the restore alone; recovery starts from a backup that ' +
      'already exists, and counting the time to take one flatters the number',
  )
  assert.ok(drill.bytes > 0)

  // Written where the operations page can quote it, so the documented recovery
  // time is a measurement rather than an aspiration.
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

