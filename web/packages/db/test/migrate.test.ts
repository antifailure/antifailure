// What a failed migration tells you.
//
// This suite exists because of one real deploy. A managed Postgres refused
// `CREATE EXTENSION pgcrypto` -- Azure will not create an extension that is not
// named in its azure.extensions parameter, and that parameter defaults to empty
// -- so the first statement of 0001_init.sql failed. What the operator was
// shown was:
//
//     PostgresError: current transaction is aborted, commands ignored until
//     end of transaction block
//         at run (packages/db/src/migrate.ts:74:14)
//
// Line 74 was the advisory unlock in the finally block. The real error had
// already been thrown, and the unlock -- issued on a connection now sitting in
// an aborted transaction -- threw 25P02 on the way out and replaced it, because
// an exception from a finally replaces the one being propagated.
//
// The failure was therefore reported as being in the lock release, named
// nothing about extensions, and pointed at a line that had done nothing wrong.
// These tests assert the property that was missing: the error you get is the
// error that happened.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import postgres from 'postgres'
import { migrate } from '../src/migrate.ts'
import { adminUrl } from './harness.ts'

// A DATABASE OF ITS OWN, and this is the whole reason the setup is longer than
// the tests.
//
// The first version of this suite ran against the shared test database and
// dropped schema_migrations in its teardown. That is the ledger every other
// suite depends on: with it gone, the next startApi() believes nothing has been
// applied and tries to run 0001 against a database that already has every
// table. Six suites in two packages hung, none of them anywhere near this file,
// and the symptom was "the OAuth exchange times out".
//
// A test that applies migrations is a test that owns a database. It cannot
// share one, because the thing under test is the state of the schema.
const scratchName = `af_migrate_test_${randomUUID().replace(/-/g, '').slice(0, 12)}`

const hasDatabase = await (async () => {
  const probe = postgres(adminUrl, { max: 1, connect_timeout: 3, onnotice: () => {} })
  try {
    await probe`SELECT 1`
    return true
  } catch {
    return false
  } finally {
    await probe.end({ timeout: 2 })
  }
})()

describe('a migration that fails', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let sql: postgres.Sql
  let server: postgres.Sql
  let dir: string

  before(async () => {
    // CREATE DATABASE cannot run inside a transaction, so it goes through its
    // own connection to the default database rather than through the pool the
    // tests use.
    server = postgres(adminUrl, { max: 1, onnotice: () => {} })
    await server.unsafe(`CREATE DATABASE ${scratchName}`)

    const scratchUrl = new URL(adminUrl)
    scratchUrl.pathname = `/${scratchName}`
    sql = postgres(scratchUrl.toString(), { max: 2, onnotice: () => {} })
    dir = await mkdtemp(path.join(tmpdir(), 'af-migrate-'))
  })

  after(async () => {
    await sql.end({ timeout: 5 })
    // WITH (FORCE) so a connection this suite failed to close does not leave a
    // database behind on somebody's machine forever.
    await server.unsafe(`DROP DATABASE IF EXISTS ${scratchName} WITH (FORCE)`)
    await server.end({ timeout: 5 })
    await rm(dir, { recursive: true, force: true })
  })

  test('reports the statement that actually failed, not the aborted transaction', async () => {
    // A file shaped like a real migration: BEGIN, a statement that Postgres
    // refuses, then more work. The refusal is a missing relation rather than a
    // missing extension because every Postgres refuses this one, and an
    // extension allow-list is specific to a managed provider.
    await writeFile(
      path.join(dir, '0001_refused.sql'),
      `BEGIN;
       SELECT * FROM a_relation_that_does_not_exist;
       CREATE TABLE never_created (id int);
       COMMIT;`,
    )

    const err = await migrate(sql, { dir, log: () => {} }).then(
      () => null,
      (e: unknown) => e as Error,
    )

    assert.ok(err, 'migrate resolved when the migration could not apply')
    const message = String(err.message)

    // The whole point. Before the fix this read "current transaction is
    // aborted, commands ignored until end of transaction block".
    assert.doesNotMatch(
      message,
      /current transaction is aborted/,
      `the aborted-transaction error masked the real one again:\n${message}`,
    )
    assert.match(
      message,
      /a_relation_that_does_not_exist/,
      `the error does not name the statement that failed:\n${message}`,
    )
  })

  test('leaves the connection usable, so the lock is released rather than leaked', async () => {
    // If the rollback did not happen, the advisory unlock would have been
    // refused and the lock would still be held by a connection handed back to
    // the pool. The next deploy would then block on it forever, which is a
    // worse failure than the one that started it: it has no error message at
    // all.
    const rows = await sql<{ held: boolean }[]>`
      SELECT EXISTS (
        SELECT 1 FROM pg_locks
        WHERE locktype = 'advisory'
          AND objid = (SELECT hashtext('antifailure.migrations')::oid)
      ) AS held`
    assert.equal(rows[0]!.held, false, 'the migration advisory lock is still held after a failed migration')
  })

  test('a migration that succeeds still applies and records itself', async () => {
    // The negative control. A test suite that only proves failures are
    // reported well cannot tell a working migrator from one that refuses
    // everything, which is the exact trap 0004's comment describes.
    const clean = await mkdtemp(path.join(tmpdir(), 'af-migrate-ok-'))
    try {
      await writeFile(
        path.join(clean, '0001_fine.sql'),
        `BEGIN;
         CREATE TABLE IF NOT EXISTS a_table_that_is_created (id int);
         COMMIT;`,
      )
      const result = await migrate(sql, { dir: clean, log: () => {} })
      assert.deepEqual(result.applied, ['0001_fine.sql'])

      const present = await sql<{ present: boolean }[]>`
        SELECT to_regclass('a_table_that_is_created') IS NOT NULL AS present`
      assert.equal(present[0]!.present, true)

      // And it is idempotent: a second run finds it already applied.
      const again = await migrate(sql, { dir: clean, log: () => {} })
      assert.deepEqual(again.applied, [])
      assert.deepEqual(again.alreadyApplied, ['0001_fine.sql'])
    } finally {
      await sql`DROP TABLE IF EXISTS a_table_that_is_created`
      await rm(clean, { recursive: true, force: true })
    }
  })
})
