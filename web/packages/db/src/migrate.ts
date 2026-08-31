// Applying migrations.
//
// Each file runs inside its own transaction and is recorded in the same
// transaction that applied it, so a file either applied and is recorded or did
// neither. A migration that half-applied and was recorded as done is the worst
// state a schema can be in, because every later deploy believes the work
// happened.
//
// The files themselves already open with BEGIN and close with COMMIT, which
// makes each one runnable by hand against a database with psql when something
// has gone wrong at three in the morning. The runner wraps that rather than
// duplicating it: postgres.js sends the whole file as one simple query, and a
// simple query containing BEGIN and COMMIT is one transaction.

import { readdir, readFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type postgres from 'postgres'

const here = path.dirname(fileURLToPath(import.meta.url))
export const migrationsDir = path.join(here, '..', 'migrations')

export interface AppliedMigration {
  name: string
  digest: string
  appliedAt: Date
}

export interface MigrateResult {
  applied: string[]
  alreadyApplied: string[]
}

/**
 * How long a migration may WAIT for a lock before giving up. Not how long it
 * may run: `lock_timeout` bounds the wait for each lock acquisition and has
 * nothing to say about the work that follows, so a file that takes a minute of
 * honest work is unaffected by a three second value.
 *
 * THREE SECONDS, AND THE NUMBER IS ABOUT CONTENTION RATHER THAN SIZE.
 *
 * The failure this exists for is not slowness. `0018` takes ACCESS EXCLUSIVE on
 * `network_rules` and SHARE ROW EXCLUSIVE on `users`, and the revision that is
 * still serving writes to both. A lock request that cannot be granted
 * immediately QUEUES, and every later request queues behind the request rather
 * than behind the table, so one transaction holding a row lock on `users` at
 * the wrong moment stops every sign-in for as long as it lives. The server
 * offers nothing to bound that: `lock_timeout`, `statement_timeout` and
 * `idle_in_transaction_session_timeout` are all 0 on the flexible server, read
 * back with `az postgres flexible-server parameter show`.
 *
 * So the value has to separate two populations, not measure one:
 *
 *   granted immediately   no conflicting transaction, which is every deploy
 *                         that is not unlucky. Milliseconds.
 *   never granted         a transaction that is stuck, idle in transaction, or
 *                         simply longer than anybody expected. Unbounded, and
 *                         it is the whole hazard.
 *
 * Three seconds is far above the first and far below anything a person would
 * call an outage. Deliberately NOT tied to the migration's own duration: `0018`
 * takes 2.7 seconds against 200,000 rows and would take 27 at ten times that,
 * and neither number belongs in this constant, because neither is a wait.
 *
 * THE TRADE, STATED. A deploy that is genuinely unlucky fails and rolls back
 * rather than blocking sign-in. That is the intended direction: a failed deploy
 * is visible and reversible in one command, and a deploy that succeeds while
 * nobody can log in looks healthy from /readyz.
 */
export const LOCK_TIMEOUT_MS = 3000

/**
 * How long any ONE statement in a migration may run.
 *
 * `lock_timeout` covers being blocked by somebody else and says nothing about a
 * migration that blocks everybody else on its own. Those are the same outage
 * from the other end: a `CREATE INDEX` or an unindexed `UPDATE` on a large table
 * holds ACCESS EXCLUSIVE for as long as it runs, and every request behind it
 * waits. `lock_timeout` cannot see that, because the lock was granted.
 *
 * PER STATEMENT, NOT PER FILE, and that was measured rather than assumed. A
 * migration file is sent as one simple query, so it is reasonable to expect the
 * budget to cover the file. It does not: against Postgres 17, with
 * `statement_timeout` at 2000, a simple query of three `pg_sleep(1.2)` calls
 * completed in 3616ms. Each statement gets the whole budget. That makes this
 * safer than a shared one, because a long file of honest statements is not
 * capped in aggregate, and it is why the number can be generous.
 *
 * FIVE MINUTES, against two numbers:
 *
 *   above  `0018` is 2.7 seconds against 200,000 rows and about 27 at ten times
 *          that, and those are whole-file figures, so its slowest single
 *          statement is well under either. 300 seconds is a hundredfold margin
 *          on the case we have actually measured.
 *   below  the bootstrap job's `replicaTimeout` of 900 seconds. It has to be,
 *          or it never fires. And the difference matters more than it looks:
 *          when the job is killed, Postgres does not learn the client is gone
 *          until a TCP keepalive says so, and the ACCESS EXCLUSIVE lock is held
 *          for the whole of that. Ending the statement from inside the server
 *          is deterministic; waiting for a dead client to be noticed is not.
 *
 * A migration that genuinely needs longer than five minutes for one statement is
 * a backfill, and a backfill that takes the schema's locks for five minutes
 * should be a decision somebody makes on purpose. Pass `statementTimeoutMs` to
 * make it, rather than editing this.
 */
export const MIGRATION_STATEMENT_TIMEOUT_MS = 300_000

/**
 * Applies every migration not yet recorded, in filename order.
 *
 * The connection must be one that can create tables and roles: migrations are
 * the one thing the application role is deliberately not allowed to do, because
 * a role that can ALTER TABLE can disable the policies that isolate tenants.
 */
export async function migrate(
  sql: postgres.Sql,
  opts: {
    dir?: string
    log?: (line: string) => void
    lockTimeoutMs?: number
    statementTimeoutMs?: number
  } = {},
): Promise<MigrateResult> {
  const dir = opts.dir ?? migrationsDir
  const log = opts.log ?? (() => {})
  const lockTimeoutMs = opts.lockTimeoutMs ?? LOCK_TIMEOUT_MS
  const statementTimeoutMs = opts.statementTimeoutMs ?? MIGRATION_STATEMENT_TIMEOUT_MS

  // One connection for the whole run, held for its duration. A migration file
  // opens its own transaction, and a transaction spread across a pool is not a
  // transaction: the COMMIT can land on a different connection from the BEGIN.
  // Reserving also means two deploys racing each other queue on the advisory
  // lock below instead of interleaving statements.
  const conn = await sql.reserve()
  try {
    return await run(conn, dir, log, lockTimeoutMs, statementTimeoutMs)
  } finally {
    conn.release()
  }
}

async function run(
  sql: postgres.ReservedSql,
  dir: string,
  log: (line: string) => void,
  lockTimeoutMs: number,
  statementTimeoutMs: number,
): Promise<MigrateResult> {
  // Two replicas starting at once both want to apply the same file. The lock
  // makes the second wait and then find the work already recorded, rather than
  // both running CREATE TABLE and one of them failing the deploy.
  await sql`SELECT pg_advisory_lock(hashtext('antifailure.migrations'))`
  try {
    // AFTER the advisory lock, and the order is the whole point.
    //
    // lock_timeout applies to pg_advisory_lock too. That was checked rather
    // than assumed, against Postgres 17: with lock_timeout at 1000, a second
    // session's pg_advisory_lock failed with 55P03 after 1005ms. Setting it
    // before the line above would therefore turn two deploys racing each other
    // into a failed deploy, when waiting is the correct and finite answer to
    // that race and is exactly what the lock is for.
    //
    // Session scope on the reserved connection rather than SET LOCAL, so it
    // covers every file: a migration file commits its own transaction, and a
    // LOCAL setting would be reverted by that COMMIT and cover only the first.
    //
    // Not a role level `ALTER ROLE af_migrator SET lock_timeout`, which would
    // also reach a person at a psql prompt doing something deliberately slow,
    // and break-glass. Not the bootstrap job either: `migrate` has seven
    // callers and the bootstrap is one. main.ts runs it under AF_MIGRATE=1 and
    // backup-scratch runs it against a restored copy, so a guard in the job
    // would leave the deploy path main.ts owns unprotected. The runner is the
    // narrowest thing that actually covers all of them.
    //
    // Zero disables it, which is Postgres's own meaning for the setting.
    await sql`SELECT set_config('lock_timeout', ${String(lockTimeoutMs)}, false)`
    // Beside it and for the other half of the same outage: lock_timeout bounds
    // being blocked, this bounds blocking everybody else. Also after the
    // advisory lock, because waiting for another deploy is not a runaway
    // statement and must not be cancelled as one.
    await sql`SELECT set_config('statement_timeout', ${String(statementTimeoutMs)}, false)`
    return await apply(sql, dir, log, lockTimeoutMs)
  } finally {
    // THIS IS WHY A FAILED MIGRATION USED TO BE UNDIAGNOSABLE, and it is worth
    // the paragraph because the symptom names nothing useful.
    //
    // A migration file is one transaction. When a statement in it fails, the
    // connection is left in an aborted transaction, and EVERY subsequent
    // statement on that connection is refused with 25P02, "current transaction
    // is aborted". The unlock below is such a statement. An exception thrown
    // from a finally block REPLACES the exception being propagated, so the
    // caller was told "current transaction is aborted" and never told which
    // statement aborted it. The real error had already happened and was
    // discarded on the way out.
    //
    // It cost an hour on a real deploy: `CREATE EXTENSION pgcrypto` was refused
    // by a managed Postgres that had not allow-listed it, and the operator saw
    // only 25P02 with a stack trace pointing at this unlock.
    //
    // Rolling back first puts the connection back in a usable state so the
    // unlock succeeds and nothing is masked. The rollback is separately
    // guarded because on the success path there is no open transaction and
    // ROLLBACK there is a warning, not an error.
    try {
      await sql`ROLLBACK`
    } catch {
      // No transaction was open, which is the successful path.
    }
    // Put the two settings back, because this connection is about to be
    // returned to the pool and a session setting survives that.
    //
    // Checked rather than assumed, which is the only reason it is here: after
    // a migrate() on a pool, `SHOW lock_timeout` on the same pool answered
    // "3s" and `SHOW statement_timeout` answered "5min". Whoever borrowed that
    // connection next would silently inherit a five minute statement budget it
    // never asked for. src/client.ts carries the same warning about tenant
    // settings for the same reason, and that one is a cross-tenant read.
    //
    // After the ROLLBACK above, so the connection is out of any aborted
    // transaction and RESET is not refused with 25P02. Reset by name rather
    // than RESET ALL, which would also discard whatever the caller set on
    // purpose before handing the pool over.
    try {
      await sql`RESET lock_timeout`
      await sql`RESET statement_timeout`
    } catch (err) {
      // Reported, not thrown, for the same reason as the unlock below: if we
      // are unwinding, the migration failure is the error worth having.
      log(`could not reset the migration timeouts: ${String(err)}`)
    }
    try {
      await sql`SELECT pg_advisory_unlock(hashtext('antifailure.migrations'))`
    } catch (err) {
      // Reported and not thrown. If we are unwinding, the migration failure is
      // the useful error and this one would replace it again; if we are not,
      // the connection is about to be released and a session-scoped advisory
      // lock goes with it.
      log(`could not release the migration lock: ${String(err)}`)
    }
  }
}

/**
 * Turns 55P03 into a sentence that names the migration, the wait and the fix.
 *
 * A lock timeout that reads as a generic deploy failure sends the next person
 * to the wrong place. The database's own message is "canceling statement due to
 * lock timeout", which says nothing about which file, nothing about what it was
 * waiting for, and nothing about whether it is safe to run again. All three are
 * the questions actually being asked at that moment.
 *
 * Everything else is rethrown untouched. A migration that failed on a syntax
 * error is not improved by being wrapped in advice about locks.
 */
function explainLockTimeout(err: unknown, name: string, lockTimeoutMs: number): unknown {
  // 55P03 is lock_not_available, which Postgres raises for exactly this. Read
  // from the error's own code rather than matched against its text, because
  // the text is localised and the code is not.
  if (!(err instanceof Error) || (err as { code?: string }).code !== '55P03') return err

  return new Error(
    `migration ${name} gave up waiting for a lock after ${lockTimeoutMs}ms.
` +
      `
` +
      `Something else was holding a conflicting lock on a table this migration
` +
      `alters. That is usually the revision still serving traffic: it writes to
` +
      `the same tables, and an ALTER TABLE queues behind it while every later
` +
      `request queues behind the ALTER rather than behind the table, which is
` +
      `how a two second migration becomes a sign-in outage.
` +
      `
` +
      `NOTHING WAS APPLIED. The file is one transaction, so it rolled back whole
` +
      `and was not recorded. Running the deploy again is safe and is usually all
` +
      `it takes, because the transaction that was in the way has since ended.
` +
      `
` +
      `If it happens twice, find the holder before retrying a third time:
` +
      `  SELECT pid, state, wait_event_type, xact_start, left(query, 120)
` +
      `  FROM pg_stat_activity
` +
      `  WHERE state <> 'idle' OR state = 'idle in transaction'
` +
      `  ORDER BY xact_start;`,
    { cause: err },
  )
}

async function apply(
  sql: postgres.ReservedSql,
  dir: string,
  log: (line: string) => void,
  lockTimeoutMs: number,
): Promise<MigrateResult> {

  await sql`
    CREATE TABLE IF NOT EXISTS schema_migrations (
      name        text PRIMARY KEY,
      digest      text NOT NULL,
      applied_at  timestamptz NOT NULL DEFAULT now()
    )`

  const files = (await readdir(dir)).filter((f) => f.endsWith('.sql')).sort()
  const rows = await sql<{ name: string; digest: string }[]>`
    SELECT name, digest FROM schema_migrations`
  const applied = new Map(rows.map((r) => [r.name, r.digest]))

  const result: MigrateResult = { applied: [], alreadyApplied: [] }

  for (const name of files) {
    const body = await readFile(path.join(dir, name), 'utf8')
    const digest = createHash('sha256').update(body).digest('hex')
    const previous = applied.get(name)

    if (previous !== undefined) {
      if (previous !== digest) {
        // Editing a migration that already ran means every database that ran
        // the old version now differs from every database that ran the new
        // one, and nothing will ever tell them apart again. The fix is a new
        // file, so this refuses rather than warns.
        throw new Error(
          `migration ${name} has changed since it was applied.\n` +
            `A migration that already ran cannot be edited: databases that ran the\n` +
            `old text will never receive the new text. Add a new migration instead.`,
        )
      }
      result.alreadyApplied.push(name)
      continue
    }

    log(`applying ${name}`)
    // simple() sends the file as one query, which is what lets a single file
    // contain several statements inside one BEGIN.
    try {
      await sql.unsafe(body).simple()
    } catch (err) {
      throw lockTimeoutMs > 0 ? explainLockTimeout(err, name, lockTimeoutMs) : err
    }
    await sql`
      INSERT INTO schema_migrations (name, digest) VALUES (${name}, ${digest})`
    result.applied.push(name)
  }

  return result
}
