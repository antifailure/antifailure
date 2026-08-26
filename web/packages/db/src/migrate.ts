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
 * Applies every migration not yet recorded, in filename order.
 *
 * The connection must be one that can create tables and roles: migrations are
 * the one thing the application role is deliberately not allowed to do, because
 * a role that can ALTER TABLE can disable the policies that isolate tenants.
 */
export async function migrate(
  sql: postgres.Sql,
  opts: { dir?: string; log?: (line: string) => void } = {},
): Promise<MigrateResult> {
  const dir = opts.dir ?? migrationsDir
  const log = opts.log ?? (() => {})

  // One connection for the whole run, held for its duration. A migration file
  // opens its own transaction, and a transaction spread across a pool is not a
  // transaction: the COMMIT can land on a different connection from the BEGIN.
  // Reserving also means two deploys racing each other queue on the advisory
  // lock below instead of interleaving statements.
  const conn = await sql.reserve()
  try {
    return await run(conn, dir, log)
  } finally {
    conn.release()
  }
}

async function run(
  sql: postgres.ReservedSql,
  dir: string,
  log: (line: string) => void,
): Promise<MigrateResult> {
  // Two replicas starting at once both want to apply the same file. The lock
  // makes the second wait and then find the work already recorded, rather than
  // both running CREATE TABLE and one of them failing the deploy.
  await sql`SELECT pg_advisory_lock(hashtext('antifailure.migrations'))`
  try {
    return await apply(sql, dir, log)
  } finally {
    await sql`SELECT pg_advisory_unlock(hashtext('antifailure.migrations'))`
  }
}

async function apply(
  sql: postgres.ReservedSql,
  dir: string,
  log: (line: string) => void,
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
    await sql.unsafe(body).simple()
    await sql`
      INSERT INTO schema_migrations (name, digest) VALUES (${name}, ${digest})`
    result.applied.push(name)
  }

  return result
}
