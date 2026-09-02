// Every table a customer's screen reads has to be written by something that is
// not a fixture.
//
// This test exists because four tables were read by the console or by the
// compliance pack and written by nobody: golden_versions, runs, verdicts and
// artifacts. The engine emitted the events, the sink mapped them, the control
// plane accepted the types and the projector had a case for none of them, so
// every one of those screens was blank for every real customer.
//
// The reason nobody noticed for so long is the whole point of this file. The
// staging seeder fills all of them. So does the test harness. Every screen
// looks right in development, every suite passes, and production is empty,
// because the only INSERTs into the table are the two files that exist to make
// it look populated to us.
//
// So the assertion is not "the table has rows". It is: somewhere outside
// test/, outside staging.ts and outside the backup drill, a statement writes
// this table. A table whose only writers are fixtures is a page that only ever
// looks populated to us.
//
// It reads source rather than a database on purpose. A database test can only
// find this by asserting an empty table, which is what a fresh install looks
// like too, and the failure would be indistinguishable from "nothing has run
// yet". The absence of a writer is a fact about the repository and it is
// checkable without anything running at all.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..', '..', '..', '..')
const schemaFile = path.join(repo, 'web', 'packages', 'db', 'src', 'schema.ts')

/**
 * The files that exist to make a table look populated.
 *
 * Matched on the path rather than on a list of names, because the next fixture
 * will not be called harness.ts. Anything under a test directory, anything
 * named like a test, the staging seeder and the backup drill's scratch seeder.
 */
function isFixture(file: string): boolean {
  const p = file.split(path.sep).join('/')
  return (
    p.includes('/test/') ||
    p.includes('/tests/') ||
    p.includes('/testdata/') ||
    /\.test\.(ts|tsx|js)$/.test(p) ||
    /_test\.go$/.test(p) ||
    // Suffix rather than a leading slash: these paths arrive relative to the
    // repository root, so an anchored '/web/...' matched nothing and the
    // staging seeder counted as a production writer for every table it fills,
    // which is the exact lie this file exists to catch.
    p.endsWith('web/packages/db/src/staging.ts') ||
    p.endsWith('web/apps/api/src/backup-scratch.ts') ||
    p.endsWith('deploy/docker/personas.mjs')
  )
}

/**
 * A migration is not a writer either, and it is a separate category from a
 * fixture because the reason is different. A backfill inside 0011 filled the
 * events partitions once, at deploy time, from data that was already there. It
 * cannot be the thing that keeps a table filled, and counting it would let a
 * one-off data move stand in for a missing feature.
 */
function isMigration(file: string): boolean {
  return file.split(path.sep).join('/').includes('/migrations/')
}

const SOURCE = /\.(ts|tsx|go|sql|mjs)$/
const SKIP_DIR = new Set([
  'node_modules', '.git', 'dist', 'build', '.next', 'vendor', 'coverage', '.turbo',
])

function sources(dir: string, out: string[] = []): string[] {
  let entries: fs.Dirent[]
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true })
  } catch {
    return out
  }
  for (const e of entries) {
    if (e.isDirectory()) {
      if (SKIP_DIR.has(e.name)) continue
      sources(path.join(dir, e.name), out)
    } else if (SOURCE.test(e.name)) {
      out.push(path.join(dir, e.name))
    }
  }
  return out
}

interface Site {
  file: string
  line: number
}

/** Every table declared in the schema, in declaration order. */
function declaredTables(): string[] {
  const text = fs.readFileSync(schemaFile, 'utf8')
  const names: string[] = []
  for (const m of text.matchAll(/pgTable\(\s*'([a-z0-9_]+)'/g)) names.push(m[1]!)
  return names
}

const roots = ['web', 'api', 'console', 'ee', 'engine', 'tools', 'deploy']
const files = roots.flatMap((r) => sources(path.join(repo, r)))
const contents = new Map<string, string[]>()
for (const f of files) {
  try {
    contents.set(f, fs.readFileSync(f, 'utf8').split('\n'))
  } catch {
    // A file that cannot be read is reported by the sanity check below rather
    // than silently reducing the corpus, which is how a scanner comes back
    // clean because it looked at nothing.
  }
}

/**
 * Finds statements against one table.
 *
 * Word-bounded, so `runs` does not match `pr_generations` and `events` does not
 * match `billing_events`. Case insensitive because SQL in this repository is
 * written in both.
 */
function sitesFor(table: string, verb: 'INSERT INTO' | 'FROM' | 'UPDATE'): Site[] {
  const pattern = new RegExp(`\\b${verb.replace(' ', '\\s+')}\\s+${table}\\b`, 'i')
  const found: Site[] = []
  for (const [file, lines] of contents) {
    lines.forEach((line, i) => {
      if (pattern.test(line)) found.push({ file: path.relative(repo, file), line: i + 1 })
    })
  }
  return found
}

/** Where a customer meets the table: the console's API, or the compliance pack. */
function isCustomerFacingReader(file: string): boolean {
  const p = file.split(path.sep).join('/')
  if (isFixture(p)) return false
  return (
    p.startsWith('web/apps/api/src/') ||
    p.startsWith('console/') ||
    p.startsWith('ee/engine/compliance/')
  )
}

/**
 * The tables that a customer-facing reader queries and nothing outside a
 * fixture writes, each with the reason and what is blank because of it.
 *
 * A frozen list rather than a filter, and both directions of drift fail:
 *
 * A table that appears here and is not in this list is a new blank page, and
 * the test says so before anybody ships it.
 *
 * A table that is in this list and HAS gained a writer also fails, because a
 * standing exemption over a defect that was fixed is an exemption that will
 * cover the next one silently. The fix is to delete the line.
 */
const UNWIRED: Record<string, string> = {
  artifacts: [
    'The runner writes a video, a trace and a screenshot to the environment\'s own artifacts',
    'directory and nothing uploads them. There is no storage backend, no upload path and no',
    'download route in the control plane, and artifact.stored is accepted by ingest and mapped',
    'from no engine event at all. The console\'s artifacts table is therefore empty for every',
    'customer. Projecting the local paths would be worse than the blank: it would list files',
    'nobody can fetch. This needs the uploader, not a projector.',
  ].join(' '),
}

describe('every table a screen reads is written by something that is not a fixture', () => {
  const tables = declaredTables()

  // -------------------------------------------------------------------------
  // The instrument, before its answer
  // -------------------------------------------------------------------------
  //
  // Every assertion below is of the form "nothing was found", and a scanner
  // that looked at nothing finds nothing. So the scanner proves it can see
  // before it is allowed to report.

  it('the scan reaches the source it is supposed to read', () => {
    assert.ok(files.length > 500, `only ${files.length} source files were read; the walk is wrong`)
    assert.ok(
      tables.length > 30,
      `only ${tables.length} tables were parsed out of schema.ts; the parse is wrong`,
    )
    assert.ok(tables.includes('golden_versions'), 'the parse missed golden_versions')
    assert.ok(tables.includes('environments'), 'the parse missed environments')
  })

  it('the scan finds a writer it is known to be able to find', () => {
    // environments is the one this whole class of defect was first found and
    // fixed in, and its writer is the ingestion projector. If this comes back
    // empty the matcher is broken and every clean answer below is worthless.
    const writers = sitesFor('environments', 'INSERT INTO')
      .filter((s) => !isFixture(s.file) && !isMigration(s.file))
    assert.ok(
      writers.length > 0,
      'no production INSERT into environments was found, so the matcher cannot see the one ' +
        'writer this test is certain exists; every other result in this file is meaningless',
    )
    assert.ok(
      writers.some((s) => s.file.endsWith('ingest.ts')),
      `the environments writer was found at ${JSON.stringify(writers)} rather than in ingest.ts`,
    )
  })

  it('the scan can tell a fixture from a writer', () => {
    const all = sitesFor('golden_versions', 'INSERT INTO')
    assert.ok(all.length > 0, 'no INSERT into golden_versions was found at all')
    assert.ok(
      all.some((s) => isFixture(s.file)),
      'the fixture INSERTs into golden_versions were not recognised as fixtures, so the ' +
        'exclusion is not doing anything and a fixture-only table would pass',
    )
  })

  it('the scan finds the readers it is supposed to find', () => {
    const readers = sitesFor('golden_versions', 'FROM').filter((s) => isCustomerFacingReader(s.file))
    assert.ok(
      readers.length > 0,
      'no customer-facing reader of golden_versions was found, so a table with no writer would ' +
        'never be reported',
    )
  })

  // -------------------------------------------------------------------------
  // The answer
  // -------------------------------------------------------------------------

  it('no table a customer meets is written only by fixtures', () => {
    const offenders: string[] = []
    const staleExemptions: string[] = []

    for (const table of tables) {
      const readers = sitesFor(table, 'FROM').filter((s) => isCustomerFacingReader(s.file))
      const writers = sitesFor(table, 'INSERT INTO')
      const inserted = writers.filter((s) => !isFixture(s.file) && !isMigration(s.file))
      const fixtures = writers.filter((s) => isFixture(s.file))

      // A SINGLETON SEEDED BY ITS MIGRATION AND MAINTAINED BY UPDATE.
      //
      // Looking only for INSERT INTO cannot see this shape, and it is a real
      // one: analytics_rollup_state holds exactly one row of bookkeeping,
      // created by `INSERT INTO analytics_rollup_state (id) VALUES (true)` in
      // the migration that declares it, and thereafter only ever UPDATEd, by
      // the rollup on the maintenance pass. That is a production writer on a
      // real path, and this scan reported the table as having none, which
      // would have sent somebody either to delete a working feature or to
      // write a disclosure for a gap that does not exist.
      //
      // Deliberately NOT "count every UPDATE as a writer". A table whose rows
      // have to be created per customer, and which nothing inserts into
      // outside a fixture, is exactly the defect this file exists to catch,
      // and blanket-counting UPDATEs would hide it. The migration INSERT is
      // what makes the row's existence guaranteed rather than hoped for, so
      // the pair is the evidence, not the UPDATE alone.
      const seededByMigration = writers.some((s) => isMigration(s.file))
      const maintained = seededByMigration
        ? sitesFor(table, 'UPDATE').filter((s) => !isFixture(s.file) && !isMigration(s.file))
        : []
      const production = [...inserted, ...maintained]
      const exempt = Object.hasOwn(UNWIRED, table)

      if (production.length > 0) {
        if (exempt) {
          staleExemptions.push(
            `${table} is exempted in UNWIRED and now has a production writer at ` +
              `${production[0]!.file}:${production[0]!.line}. Delete the exemption: one left ` +
              'standing over a defect that was fixed will cover the next one silently.',
          )
        }
        continue
      }
      if (readers.length === 0 || exempt) continue

      offenders.push(
        `${table} is read by ${readers.length} customer-facing site(s), the first at ` +
          `${readers[0]!.file}:${readers[0]!.line}, and the only statements that write it are ` +
          `${fixtures.length} fixture(s)` +
          (fixtures[0] ? ` such as ${fixtures[0].file}:${fixtures[0].line}` : '') +
          '. The staging seeder fills this table, so the screen that reads it looks correct in ' +
          'development and is blank for every real customer. Either give it a writer on the ' +
          'path a customer actually takes, or delete the table and the screen. If it is ' +
          'genuinely unbuilt, add it to UNWIRED in this file with the reason and with what is ' +
          'blank because of it.',
      )
    }

    assert.deepEqual(
      [...offenders, ...staleExemptions], [],
      `\n${[...offenders, ...staleExemptions].join('\n\n')}\n`,
    )
  })

  it('every exemption is about a table that still exists and is still unwritten', () => {
    for (const table of Object.keys(UNWIRED)) {
      assert.ok(
        tables.includes(table),
        `${table} is exempted here and is not a table in schema.ts at all, so the exemption ` +
          'guards nothing; remove it',
      )
      assert.ok(
        UNWIRED[table]!.length > 60,
        `the exemption for ${table} does not say enough to be worth having; it has to say why ` +
          'the capability is not built and what is blank because of it',
      )
    }
  })

  it('the five that were fixed stay fixed', () => {
    // Named individually rather than left to the sweep above, because the
    // sweep passes for a table nobody reads. These four are read by the
    // console and by the compliance pack, and a change that removed their
    // projector would put every one of those screens back to blank.
    for (const table of ['golden_versions', 'runs', 'verdicts', 'environments']) {
      const production = sitesFor(table, 'INSERT INTO')
        .filter((s) => !isFixture(s.file) && !isMigration(s.file))
      assert.ok(
        production.length > 0,
        `${table} has lost its production writer. The page that reads it is blank again.`,
      )
    }
  })
})
