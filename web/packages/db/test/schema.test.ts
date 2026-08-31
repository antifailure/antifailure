// The typed schema and the migrations describe the same tables, or this fails.
//
// They are two descriptions of one thing, and the migrations are the one the
// database obeys. A column renamed in a migration and not in schema.ts produces
// a query that fails at runtime on a path nobody exercised; a column added to
// schema.ts and not to a migration produces the same. Neither shows up in a
// type check, because both files type-check perfectly while describing
// different databases.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { getTableConfig, PgTable } from 'drizzle-orm/pg-core'
import { is } from 'drizzle-orm'
import * as schema from '../src/schema.ts'
import { available, setup, type Harness } from './harness.ts'

const hasDatabase = await available()

describe('schema drift', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  before(async () => {
    h = await setup()
  })
  after(async () => {
    await h.close()
  })

  // The module exports tables and enums side by side. is(x, PgTable) is
  // drizzle's own discriminator and is the only reliable one: a table and an
  // enum are both plain objects with symbol keys.
  const tables: PgTable[] = (Object.values(schema) as unknown[]).filter(
    (v): v is PgTable => is(v, PgTable),
  )

  it('describes at least the whole schema', () => {
    assert.ok(tables.length >= 16, `only ${tables.length} tables are typed`)
  })

  it('every typed table exists in the database with the same columns', async () => {
    for (const table of tables) {
      const config = getTableConfig(table)
      const actual = await h.admin<{ column_name: string; is_nullable: string }[]>`
        SELECT column_name, is_nullable FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = ${config.name}
        ORDER BY column_name`
      assert.ok(actual.length > 0, `table ${config.name} is typed but does not exist in the database`)

      const typed = config.columns.map((c) => c.name).sort()
      const inDatabase = actual.map((c) => c.column_name).sort()
      assert.deepEqual(
        typed,
        inDatabase,
        `${config.name} has different columns in schema.ts and in the migrations`,
      )

      // Nullability too. A column typed as required but nullable in the
      // database hands the application an undefined it never checks for.
      const nullableInDb = new Set(
        actual.filter((c) => c.is_nullable === 'YES').map((c) => c.column_name),
      )
      for (const column of config.columns) {
        const typedRequired = column.notNull
        const dbRequired = !nullableInDb.has(column.name)
        assert.equal(
          typedRequired,
          dbRequired,
          `${config.name}.${column.name} is ${typedRequired ? 'required' : 'optional'} in schema.ts ` +
            `and ${dbRequired ? 'required' : 'optional'} in the database`,
        )
      }
    }
  })

  it('every table in the database is typed', async () => {
    const rows = await h.admin<{ table_name: string }[]>`
      SELECT c.relname AS table_name FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      -- A partition is storage for its parent, not a relation the query
      -- surface names. Typing events types every month of it, and a model per
      -- partition would have to be written again every time the manager
      -- creates one.
      WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND NOT c.relispartition
      ORDER BY c.relname`
    const typed = new Set(tables.map((t) => getTableConfig(t).name))
    const untyped = rows
      .map((r) => r.table_name)
      // The migration runner's own bookkeeping is written in raw SQL and read
      // by nothing else, so it has no reason to appear in the query surface.
      .filter((t) => t !== 'schema_migrations' && !typed.has(t))
    assert.deepEqual(untyped, [], `these tables exist but nothing types them: ${untyped.join(', ')}`)
  })

  it('the enums in the database hold exactly the values the code knows about', async () => {
    const expected: Record<string, readonly string[]> = {
      member_role: schema.memberRole.enumValues,
      environment_state: schema.environmentState.enumValues,
      run_state: schema.runState.enumValues,
      verdict_value: schema.verdictValue.enumValues,
    }
    for (const [name, values] of Object.entries(expected)) {
      const rows = await h.admin<{ label: string }[]>`
        SELECT e.enumlabel AS label FROM pg_enum e
        JOIN pg_type t ON t.oid = e.enumtypid
        WHERE t.typname = ${name} ORDER BY e.enumsortorder`
      assert.deepEqual(
        rows.map((r) => r.label),
        [...values],
        `the ${name} enum differs between the database and the code`,
      )
    }
  })
})

// tenantScopedTables says in its own comment that the cross-tenant suite
// asserts it covers the database, and until this test existed nothing read it
// at all: the suite deliberately asks the database which tables carry org_id
// rather than trusting a hand-written list, which is the stronger check and
// left the list as a stale copy nobody maintained.
//
// So the list is checked against the typed schema instead, which needs no
// database and therefore runs on every machine. A table added with an org_id
// column and left out of the list is now a failure here rather than a silent
// omission from the only place a reviewer would look for the set.
describe('the tenant scoped table list', () => {
  // sessions carries org_id and is not tenant scoped: a session belongs to a
  // person, and the cross-tenant suite covers it by its own test for that
  // reason. Named here so that the exemption is a decision somebody made
  // rather than a table that fell out.
  const isolatedByUser = new Set(['sessions'])

  it('names every typed table that carries an org_id', () => {
    const typed: PgTable[] = (Object.values(schema) as unknown[]).filter(
      (v): v is PgTable => is(v, PgTable),
    )
    const orgScoped = typed
      .map((t) => getTableConfig(t))
      .filter((c) => c.columns.some((col) => col.name === 'org_id'))
      .map((c) => c.name)
      .filter((name) => !isolatedByUser.has(name))
      .sort()
    const listed = schema.tenantScopedTables.map((t) => getTableConfig(t).name).sort()

    assert.deepEqual(
      listed,
      orgScoped,
      'tenantScopedTables and the org_id columns in the schema disagree',
    )
  })
})
