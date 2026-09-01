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
import { migrate, LOCK_TIMEOUT_MS, MIGRATION_STATEMENT_TIMEOUT_MS } from '../src/migrate.ts'
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

// A migration that cannot get its lock.
//
// The second real failure this file guards, and it is the more dangerous of the
// two because it does not look like a failure at all.
//
// `0018` takes ACCESS EXCLUSIVE on `network_rules` and SHARE ROW EXCLUSIVE on
// `users`, and the revision still serving traffic writes to both. A lock
// request that cannot be granted QUEUES, and every later request queues behind
// the request rather than behind the table. So one ordinary transaction that
// happens to be open on `users` at the wrong moment turns a two second
// migration into a sign-in outage lasting as long as that transaction does, and
// nothing bounds it: `lock_timeout`, `statement_timeout` and
// `idle_in_transaction_session_timeout` are all 0 on the flexible server.
//
// These tests use real time rather than the injected clock on purpose. What is
// being proved is that Postgres cancels a wait after a real interval, which is
// a property of the server and not of anything this process could fake.
describe('a migration that cannot get its lock', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  const lockScratch = `af_lock_test_${randomUUID().replace(/-/g, '').slice(0, 12)}`
  let sql: postgres.Sql
  let server: postgres.Sql
  let dir: string
  let scratchUrl: string

  before(async () => {
    server = postgres(adminUrl, { max: 1, onnotice: () => {} })
    await server.unsafe(`CREATE DATABASE ${lockScratch}`)
    const u = new URL(adminUrl)
    u.pathname = `/${lockScratch}`
    scratchUrl = u.toString()
    sql = postgres(scratchUrl, { max: 3, onnotice: () => {} })
    dir = await mkdtemp(path.join(tmpdir(), 'af-lock-'))
    await writeFile(
      path.join(dir, '0001_contended.sql'),
      `BEGIN;
       CREATE TABLE IF NOT EXISTS contended (id int);
       COMMIT;`,
    )
    await migrate(sql, { dir, log: () => {} })
    // The second file is the one every test here tries to apply. It alters the
    // table the holder below has locked, which is the shape of 0018.
    await writeFile(
      path.join(dir, '0002_alters_contended.sql'),
      `BEGIN;
       ALTER TABLE contended ADD COLUMN approved_at timestamptz;
       CREATE INDEX contended_approved_idx ON contended (approved_at);
       COMMIT;`,
    )
  })

  after(async () => {
    await sql.end({ timeout: 5 })
    await server.unsafe(`DROP DATABASE IF EXISTS ${lockScratch} WITH (FORCE)`)
    await server.end({ timeout: 5 })
    await rm(dir, { recursive: true, force: true })
  })

  // EVERY TEST HERE CARRIES AN EXPLICIT TIMEOUT, and it is not boilerplate.
  //
  // The regression these guard against is a migration that waits forever. With
  // the lock_timeout removed on purpose, these tests do not fail, they HANG:
  // the migration never returns, the holder is never released because the
  // release is in a finally the test never reaches, and `node --test` waits
  // with it. Proved by deleting the set_config line and watching the file run
  // past sixty seconds with three tests reported and the rest silent.
  //
  // A hung job and a red test are not the same signal. The first reads as
  // infrastructure and gets re-run; the second names the defect. Thirty seconds
  // is far above the sub-second these take when the timeout works.
  /** Holds ACCESS EXCLUSIVE on `contended` until the returned function is called. */
  async function holdTheTable(): Promise<() => Promise<void>> {
    const holder = postgres(scratchUrl, { max: 1, onnotice: () => {} })
    let release!: () => void
    const released = new Promise<void>((r) => {
      release = r
    })
    const held = holder.begin(async (tx) => {
      await tx`LOCK TABLE contended IN ACCESS EXCLUSIVE MODE`
      await released
    })
    // Wait for the lock to actually be held rather than sleeping and hoping.
    // A test that races its own fixture fails for the wrong reason.
    for (let i = 0; i < 200; i += 1) {
      const rows = await sql<{ n: number }[]>`
        SELECT count(*)::int AS n FROM pg_locks
        WHERE relation = 'contended'::regclass AND mode = 'AccessExclusiveLock' AND granted`
      if ((rows[0]?.n ?? 0) > 0) break
      await new Promise((r) => setTimeout(r, 25))
    }
    return async () => {
      release()
      await held.catch(() => {})
      await holder.end({ timeout: 5 })
    }
  }

  test('gives up on the lock instead of queueing behind it forever', { timeout: 30_000 }, async () => {
    const release = await holdTheTable()
    try {
      const started = Date.now()
      const err = await migrate(sql, { dir, log: () => {}, lockTimeoutMs: 400 }).then(
        () => null,
        (e: unknown) => e as Error & { cause?: { code?: string } },
      )
      const waited = Date.now() - started

      assert.ok(err, 'migrate resolved while the table was locked against it')
      // The number that matters. Without lock_timeout this does not return at
      // all while the holder lives, and neither does anybody's sign-in.
      assert.ok(waited < 5000, `waited ${waited}ms, which is not "gave up"`)
      assert.equal(err.cause?.code, '55P03', `not a lock timeout:\n${err.message}`)

      // Named, because a lock timeout that reads as a generic deploy failure
      // sends the next person hunting in the wrong place.
      assert.match(err.message, /0002_alters_contended\.sql/, err.message)
      assert.match(err.message, /waiting for a lock/i, err.message)
      assert.match(err.message, /NOTHING WAS APPLIED/, err.message)
    } finally {
      await release()
    }
  })

  test('leaves no partial state, so the retry is safe', { timeout: 30_000 }, async () => {
    const release = await holdTheTable()
    try {
      await migrate(sql, { dir, log: () => {}, lockTimeoutMs: 400 }).catch(() => {})

      // The file is one transaction so this should hold, and it is asserted
      // rather than assumed: "retry is safe" is the claim the error makes to
      // whoever reads it at three in the morning.
      const cols = await sql<{ n: number }[]>`
        SELECT count(*)::int AS n FROM information_schema.columns
        WHERE table_name = 'contended' AND column_name = 'approved_at'`
      assert.equal(cols[0]!.n, 0, 'the ALTER partly applied')
      const idx = await sql<{ n: number }[]>`
        SELECT count(*)::int AS n FROM pg_indexes WHERE indexname = 'contended_approved_idx'`
      assert.equal(idx[0]!.n, 0, 'the index survived a rolled back migration')
      const recorded = await sql<{ n: number }[]>`
        SELECT count(*)::int AS n FROM schema_migrations WHERE name = '0002_alters_contended.sql'`
      assert.equal(recorded[0]!.n, 0, 'a migration that did not apply was recorded as applied')

      // And the advisory lock went back, or the next deploy waits on a lock
      // nobody holds any more.
      //
      // Scoped to THIS database, because pg_locks is cluster-wide. An
      // unscoped count made this test report another database's lock as this
      // migration's leak: a test process killed mid-run left backends holding
      // advisory locks, and this assertion turned that into a false accusation
      // against a change that had nothing to do with it.
      const advisory = await sql<{ n: number }[]>`
        SELECT count(*)::int AS n
        FROM pg_locks l JOIN pg_stat_activity a USING (pid)
        WHERE l.locktype = 'advisory' AND a.datname = current_database()`
      assert.equal(advisory[0]!.n, 0, 'the migration advisory lock was not released')
    } finally {
      await release()
    }
  })

  test('applies once the holder lets go', { timeout: 30_000 }, async () => {
    const release = await holdTheTable()
    await migrate(sql, { dir, log: () => {}, lockTimeoutMs: 400 }).catch(() => {})
    await release()

    const result = await migrate(sql, { dir, log: () => {} })
    assert.deepEqual(result.applied, ['0002_alters_contended.sql'])
    const cols = await sql<{ n: number }[]>`
      SELECT count(*)::int AS n FROM information_schema.columns
      WHERE table_name = 'contended' AND column_name = 'approved_at'`
    assert.equal(cols[0]!.n, 1)
  })

  test('cancels a migration statement that would hold the lock itself', { timeout: 30_000 }, async () => {
    // The other end of the same outage, and lock_timeout is blind to it: the
    // lock was granted, so nothing is waiting on a lock. What is waiting is
    // every request behind a statement that will not finish.
    const runaway = await mkdtemp(path.join(tmpdir(), 'af-runaway-'))
    try {
      await writeFile(
        path.join(runaway, '0001_runaway.sql'),
        `BEGIN;
         CREATE TABLE IF NOT EXISTS runaway (id int);
         LOCK TABLE runaway IN ACCESS EXCLUSIVE MODE;
         SELECT pg_sleep(30);
         COMMIT;`,
      )
      const started = Date.now()
      const err = await migrate(sql, {
        dir: runaway,
        log: () => {},
        statementTimeoutMs: 400,
      }).then(
        () => null,
        (e: unknown) => e as Error & { code?: string },
      )
      const took = Date.now() - started

      assert.ok(err, 'a statement that sleeps for thirty seconds was allowed to')
      assert.ok(took < 10_000, `took ${took}ms, so it was not cancelled`)
      // 57014 and NOT wrapped as a lock timeout. The two failures need
      // different answers and an error that confuses them costs an hour.
      assert.equal(err.code, '57014', `not a statement timeout:\n${err.message}`)
    } finally {
      await rm(runaway, { recursive: true, force: true })
    }
  })

  test('gives the connection back without the timeouts on it', async () => {
    // A reserved connection goes back to the pool, and a session setting
    // survives that. Before the reset this test read 3s and 5min here, so the
    // next borrower silently inherited a five minute statement budget.
    // src/client.ts carries the same warning about tenant settings, where the
    // consequence is a cross-tenant read rather than a surprising timeout.
    const clean = await mkdtemp(path.join(tmpdir(), 'af-reset-'))
    try {
      await writeFile(path.join(clean, '0001_trivial.sql'), 'BEGIN;\nSELECT 1;\nCOMMIT;\n')
      await migrate(sql, { dir: clean, log: () => {} })

      const [lock] = await sql<{ lock_timeout: string }[]>`SHOW lock_timeout`
      const [statement] = await sql<{ statement_timeout: string }[]>`SHOW statement_timeout`
      assert.equal(lock!.lock_timeout, '0', 'lock_timeout leaked onto a pooled connection')
      assert.equal(statement!.statement_timeout, '0', 'statement_timeout leaked onto a pooled connection')
    } finally {
      await sql`DELETE FROM schema_migrations WHERE name = '0001_trivial.sql'`
      await rm(clean, { recursive: true, force: true })
    }
  })

  test('the shipped values fit between honest work and the job that runs it', () => {
    // 0018 is 2.7 seconds against 200,000 rows and about 27 at ten times that,
    // and those are whole-file figures while statement_timeout is per
    // statement, measured rather than assumed.
    // Ten-fold over the ten-times-the-rows case is the most that can be asked
    // here: the job's ceiling below is 900 seconds, so demanding thirty-fold
    // over 27 seconds would want 810 and leave the two assertions with no value
    // that satisfies both. Found by writing that and watching it go red.
    assert.ok(
      MIGRATION_STATEMENT_TIMEOUT_MS >= 10 * 27 * 1000,
      `${MIGRATION_STATEMENT_TIMEOUT_MS}ms leaves no margin over the 27s ten-times-the-rows case`,
    )
    // The bootstrap job's replicaTimeout is 900 seconds. Above that this never
    // fires and the job is killed instead, leaving the lock held on a
    // connection Postgres has not yet noticed is gone.
    assert.ok(
      MIGRATION_STATEMENT_TIMEOUT_MS < 900_000,
      `${MIGRATION_STATEMENT_TIMEOUT_MS}ms is at or past the job's own timeout, so it can never fire`,
    )
    // And the lock wait has to be far below either, because it is a wait and
    // not work.
    assert.ok(
      LOCK_TIMEOUT_MS < MIGRATION_STATEMENT_TIMEOUT_MS,
      'the lock wait budget is not smaller than the work budget',
    )
  })
})
