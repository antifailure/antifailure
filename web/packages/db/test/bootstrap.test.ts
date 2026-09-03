// The bootstrap job, and the credential it was not creating.
//
// WHAT THIS SUITE IS FOR. deploy/docker/bootstrap.mjs is the only thing that
// makes a fresh control plane database usable, and until now it made half of it
// usable. It created the application's login role and granted it membership of
// `antifailure_app`, which nothing else does. It did nothing at all about
// `antifailure_admin`.
//
// That role is created by migration 0023 as NOLOGIN with no password, exactly
// the way 0001 creates `antifailure_app`, and 0031 says why in as many words:
// NOLOGIN, so a password has to be set deliberately by whoever operates the
// installation. Nothing in this repository was that "something". So
// AF_ADMIN_DATABASE_URL named a role no client could authenticate as, and
// because createAdminPool is awaited at start-up, delivering that variable
// would not have produced a broken portal. It would have produced no control
// plane at all.
//
// WHY THIS RUNS THE REAL FILE IN THE REAL LAYOUT. bootstrap.mjs imports
// `@antifailure/db` by package name, which resolves in the image because the
// Dockerfile puts it at /app/bootstrap.mjs beside /app/node_modules. Copying it
// under web/node_modules reproduces that exactly. Importing its functions
// instead would test something the deployment never runs, and the failure being
// guarded against here is a job that exits 0 having achieved nothing.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { copyFile, rm } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import postgres from 'postgres'
import { adminUrl, connectTimeoutSeconds } from './harness.ts'

const run = promisify(execFile)
const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, '..', '..', '..', '..')
const webRoot = path.resolve(here, '..', '..', '..')
const source = path.join(repoRoot, 'deploy', 'docker', 'bootstrap.mjs')
const underTest = path.join(webRoot, 'node_modules', '.antifailure-bootstrap-under-test.mjs')

// A DATABASE OF ITS OWN, for the reason migrate.test.ts gives at length: a test
// that applies migrations owns a database, because the thing under test is the
// state of the schema. This one also creates and alters ROLES, which are
// cluster-wide rather than per-database, so every role it makes carries a
// suffix unique to the run.
const suffix = randomUUID().replace(/-/g, '').slice(0, 10)
const scratchName = `af_bootstrap_test_${suffix}`
const appRole = `af_app_${suffix}`
const plainRole = `af_plain_${suffix}`
const password = 'bootstrap-test-password'

const hasDatabase = await (async () => {
  const probe = postgres(adminUrl, { max: 1, connect_timeout: connectTimeoutSeconds, onnotice: () => {} })
  try {
    await probe`SELECT 1`
    return true
  } catch {
    return false
  } finally {
    await probe.end({ timeout: 2 })
  }
})()

/** A URL on the scratch database for some role. */
function urlFor(role: string, secret = password): string {
  const u = new URL(adminUrl)
  u.pathname = `/${scratchName}`
  u.username = role
  u.password = secret
  return u.toString()
}

function migrationUrl(): string {
  const u = new URL(adminUrl)
  u.pathname = `/${scratchName}`
  return u.toString()
}

/** Runs the real entrypoint and returns its exit code and combined output. */
async function bootstrap(env: Record<string, string>): Promise<{ code: number; out: string }> {
  try {
    const { stdout, stderr } = await run(process.execPath, [underTest], {
      env: { PATH: process.env.PATH ?? '', ...env },
      cwd: webRoot,
    })
    return { code: 0, out: stdout + stderr }
  } catch (err) {
    const e = err as { code?: number; stdout?: string; stderr?: string }
    return { code: e.code ?? 1, out: (e.stdout ?? '') + (e.stderr ?? '') }
  }
}

describe('the bootstrap job', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let owner: postgres.Sql

  before(async () => {
    await copyFile(source, underTest)
    const root = postgres(adminUrl, { max: 1, connect_timeout: connectTimeoutSeconds, onnotice: () => {} })
    try {
      await root.unsafe(`CREATE DATABASE ${scratchName}`)
    } finally {
      await root.end({ timeout: 5 })
    }
    owner = postgres(migrationUrl(), { max: 1, connect_timeout: connectTimeoutSeconds, onnotice: () => {} })

    // One bootstrap for the whole suite, with the operator portal on. Every
    // assertion below reads the state it left, which is the state a real deploy
    // leaves.
    const result = await bootstrap({
      AF_MIGRATION_DATABASE_URL: migrationUrl(),
      AF_DATABASE_URL: urlFor(appRole),
      AF_ADMIN_DATABASE_URL: urlFor('antifailure_admin'),
    })
    assert.equal(result.code, 0, `the bootstrap failed:\n${result.out}`)
  })

  after(async () => {
    await owner?.end({ timeout: 5 })
    const root = postgres(adminUrl, { max: 1, connect_timeout: connectTimeoutSeconds, onnotice: () => {} })
    try {
      await root.unsafe(`DROP DATABASE IF EXISTS ${scratchName} WITH (FORCE)`)
      // NOLOGIN rather than DROP: antifailure_admin is created by the
      // migrations and is cluster-wide, so another suite's database may be
      // depending on it. Only the roles this file invented are dropped.
      await root.unsafe(`ALTER ROLE antifailure_admin NOLOGIN`)
      for (const role of [appRole, plainRole]) {
        await root.unsafe(`DROP ROLE IF EXISTS ${role}`)
      }
    } finally {
      await root.end({ timeout: 5 })
    }
    await rm(underTest, { force: true })
  })

  // THE ASSERTION THE WHOLE CHANGE EXISTS FOR. Not "the job exited 0" and not
  // "the role has an attribute": a connection made with the URL the deployment
  // hands the application, answering the exact question createAdminPool asks
  // before it will serve a single operator request.
  test('the operator credential connects and passes the check the application makes at start-up', async () => {
    const operator = postgres(urlFor('antifailure_admin'), {
      max: 1,
      connect_timeout: connectTimeoutSeconds,
      onnotice: () => {},
    })
    try {
      const rows = await operator<{ role: string; bypass: boolean }[]>`
        SELECT current_user AS role,
               (SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user) AS bypass`
      assert.equal(rows[0]?.role, 'antifailure_admin')
      assert.equal(rows[0]?.bypass, true, 'the credential connects and would read zero rows through every policy')
    } finally {
      await operator.end({ timeout: 5 })
    }
  })

  // BYPASSRLS IS AN ATTRIBUTE AND NOT A GRANT, so holding it proves the
  // policies do not apply and proves nothing about whether the role may SELECT
  // at all. Both halves are asserted here, against a real row, because a
  // credential that satisfies ensureBypass and is then refused every read is
  // the same empty portal wearing a different cause.
  test('the operator credential reads a row the application role cannot', async () => {
    await owner`
      INSERT INTO organizations (slug, name) VALUES (${'wire-' + suffix}, 'Wired')
      ON CONFLICT (slug) DO NOTHING`

    const operator = postgres(urlFor('antifailure_admin'), { max: 1, onnotice: () => {} })
    const app = postgres(urlFor(appRole), { max: 1, onnotice: () => {} })
    try {
      const seen = await operator`SELECT slug FROM organizations WHERE slug = ${'wire-' + suffix}`
      assert.equal(seen.length, 1, 'the operator credential could not read across tenants')

      // The wall, from the other side. With no tenant scope set the application
      // role reads nothing, which is what makes the operator credential a
      // separate credential rather than a convenience.
      const hidden = await app`SELECT slug FROM organizations WHERE slug = ${'wire-' + suffix}`
      assert.equal(hidden.length, 0, 'the application role read across tenants, so the wall is not there')
    } finally {
      await operator.end({ timeout: 5 })
      await app.end({ timeout: 5 })
    }
  })

  test('the application role still gets its membership, which is the step nothing else does', async () => {
    const [row] = await owner<{ ok: boolean }[]>`
      SELECT pg_has_role(${appRole}, 'antifailure_app', 'MEMBER') AS ok`
    assert.equal(row?.ok, true)
  })

  // ONE BREAK PER ASSERTION BELOW. Each is a different way the operator URL can
  // be wrong, and each has to be refused BY NAME: an exit code with no message
  // sends somebody to read the source at 3am, and a good message with a zero
  // exit is a job that silently did nothing.
  describe('refuses an operator URL it cannot honour', () => {
    test('pointed at the application role', async () => {
      const result = await bootstrap({
        AF_MIGRATION_DATABASE_URL: migrationUrl(),
        AF_DATABASE_URL: urlFor(appRole),
        AF_ADMIN_DATABASE_URL: urlFor(appRole),
      })
      assert.notEqual(result.code, 0, 'the operator URL was allowed to be the application credential')
      assert.match(result.out, /application's own role/)
    })

    test('pointed at a role that does not exist', async () => {
      const result = await bootstrap({
        AF_MIGRATION_DATABASE_URL: migrationUrl(),
        AF_DATABASE_URL: urlFor(appRole),
        AF_ADMIN_DATABASE_URL: urlFor(`af_absent_${suffix}`),
      })
      assert.notEqual(result.code, 0)
      assert.match(result.out, /does not exist/)
      assert.match(result.out, /antifailure_admin/)
    })

    test('pointed at a role that exists without BYPASSRLS', async () => {
      await owner.unsafe(`CREATE ROLE ${plainRole} LOGIN NOBYPASSRLS PASSWORD '${password}'`)
      const result = await bootstrap({
        AF_MIGRATION_DATABASE_URL: migrationUrl(),
        AF_DATABASE_URL: urlFor(appRole),
        AF_ADMIN_DATABASE_URL: urlFor(plainRole),
      })
      assert.notEqual(result.code, 0, 'a role with no BYPASSRLS was accepted, and it reads zero rows through every policy')
      assert.match(result.out, /BYPASSRLS/)
    })

    test('carrying no password', async () => {
      const result = await bootstrap({
        AF_MIGRATION_DATABASE_URL: migrationUrl(),
        AF_DATABASE_URL: urlFor(appRole),
        AF_ADMIN_DATABASE_URL: urlFor('antifailure_admin', ''),
      })
      assert.notEqual(result.code, 0)
      assert.match(result.out, /AF_ADMIN_DATABASE_URL carries no password/)
    })
  })

  // The default, and it is the one every self-hosted installation runs. Unset
  // must stay a supported state rather than becoming a required variable by
  // accident: a single team running this for themselves has no operator portal
  // and the application says so at start-up.
  test('an installation with no operator portal bootstraps normally', async () => {
    const result = await bootstrap({
      AF_MIGRATION_DATABASE_URL: migrationUrl(),
      AF_DATABASE_URL: urlFor(appRole),
    })
    assert.equal(result.code, 0, result.out)
    assert.match(result.out, /AF_ADMIN_DATABASE_URL is not set/)
    assert.match(result.out, /bootstrap complete/)
  })
})
