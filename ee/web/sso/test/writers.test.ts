// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

// The enterprise half of "every table a screen reads is written by something
// that is not a fixture".
//
// It lives here rather than in web/packages/db/test/writers.test.ts because
// the community test cannot ask this question honestly. In a community
// checkout ee/web is not on disk, so a scan rooted there finds no readers,
// skips these tables, and reports green having measured nothing. A vacuous
// pass is worse than a missing test: it looks like coverage.
//
// The tables below are real and are read by live enterprise code. What none of
// them has is anything that CREATES a row on a path a customer takes, so the
// code that reads them is reading a table only a test harness fills.

import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..', '..', '..', '..')

const SOURCE = /\.(ts|tsx|go|sql|mjs)$/
const SKIP_DIR = new Set(['node_modules', '.git', 'dist', '.next', 'out'])

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

const files = ['ee', 'web', 'console'].flatMap((r) => sources(path.join(repo, r)))

/** A path under any test directory, or a seeder. These fill tables in
 *  development and prove nothing about the path a customer takes. */
function isFixture(rel: string): boolean {
  return (
    rel.includes('/test/') ||
    rel.includes('/tests/') ||
    rel.endsWith('.test.ts') ||
    rel.includes('/bin/seed') ||
    rel.includes('harness')
  )
}

function isMigration(rel: string): boolean {
  return rel.includes('/migrations/')
}

/**
 * Finds statements against one table.
 *
 * Word bounded and case insensitive, which is not decoration and is not what
 * this used to be. `includes` was a substring test, so `INSERT INTO
 * sso_connections` matched `INSERT INTO sso_connections_archive` and a rename
 * would have reported a writer for a table that no longer existed. The readers
 * test at the bottom of this file already carries that lesson in a comment,
 * because a mutation caught it there; it was never carried back up here, where
 * every other assertion in the file is made.
 *
 * Case insensitive for the same reason its sibling in web/packages/db is: SQL
 * in this repository is written both ways, and a matcher that only sees one of
 * them reports no writer for a table that has one.
 */
function sitesFor(table: string, verb: string): string[] {
  const pattern = new RegExp(`\\b${verb.replace(/ /g, '\\s+')}\\s+${table}\\b`, 'i')
  const hits: string[] = []
  for (const f of files) {
    const rel = path.relative(repo, f).split(path.sep).join('/')
    let text: string
    try {
      text = fs.readFileSync(f, 'utf8')
    } catch {
      continue
    }
    if (pattern.test(text)) hits.push(rel)
  }
  return hits
}

describe('the enterprise tables a screen reads are written by nothing but fixtures', () => {
  test('the scan reached the enterprise source it is supposed to read', () => {
    // Without this every assertion below is vacuously true, which is the exact
    // failure this file exists to avoid.
    assert.ok(
      files.some((f) => f.includes(`${path.sep}ee${path.sep}web${path.sep}sso${path.sep}src`)),
      'the walk did not reach ee/web/sso/src, so nothing below measured anything',
    )
  })

  test('the matcher can find a statement it is known to be able to find', () => {
    // THE PART THE WALK CHECK ABOVE DOES NOT COVER, and the difference matters.
    // That one proves the file list reached ee/web/sso/src. This one proves
    // `sitesFor` can match anything at all once it gets there, and every
    // assertion below is of the form "no production writer was found".
    //
    // A matcher that stopped matching would report no writer for every table,
    // which is what these tests already expect, so the file would go green
    // while the very thing it watches for, an SSO connection gaining a real
    // writer, had happened. The fixtures are the landmark: they exist, they
    // are excluded from the answer by design, and their absence from this scan
    // means the scan is blind rather than the tables being unwritten.
    const seen = sitesFor('sso_connections', 'INSERT INTO')
    assert.ok(
      seen.length > 0,
      'sitesFor found no INSERT INTO sso_connections anywhere, not even in the fixtures that ' +
        'certainly contain one, so every expectation below passed by matching nothing',
    )
    assert.ok(
      seen.some((f) => isFixture(f)),
      `INSERT INTO sso_connections was found only at ${seen.join(', ')} and none of it was ` +
        'recognised as a fixture, so the exclusion below is not doing anything',
    )
  })

  test('nothing creates an SSO connection, though enforcement reads one on every login', () => {
    // ee/web/sso/src/enforce.ts UPDATEs the row to set enforced, and
    // ee/web/sso/src/store.ts reads it on every login, so enforcement is live
    // code over a table only the harness fills. The administration path that
    // would create one does not exist.
    for (const table of ['sso_connections', 'sso_connection_secrets', 'sso_domains']) {
      const writers = sitesFor(table, 'INSERT INTO')
      const production = writers.filter((w) => !isFixture(w) && !isMigration(w))
      assert.deepEqual(
        production,
        [],
        `${table} has gained a production writer at ${production[0]}. That is good news, and ` +
          'this expectation is now stale: delete it rather than leaving an exemption standing ' +
          'over a defect that was fixed, because it will cover the next one silently.',
      )
    }
  })

  test('nothing issues a SCIM token, so no SCIM request can ever authenticate', () => {
    // ee/web/scim/src/store.ts authenticates every SCIM request against this
    // table and updates last_used_at on a hit.
    const writers = sitesFor('scim_tokens', 'INSERT INTO')
    const production = writers.filter((w) => !isFixture(w) && !isMigration(w))
    assert.deepEqual(
      production,
      [],
      `scim_tokens has gained a production writer at ${production[0]}. Delete this expectation.`,
    )
  })

  test('the readers this file describes are really there', () => {
    // Named rather than counted, so that moving one of them fails here instead
    // of quietly leaving the comments above describing code that has gone.
    // Anchored on a word boundary rather than a substring. `includes` here was
    // wrong and a mutation caught it: 'UPDATE sso_connections' is a substring
    // of 'UPDATE sso_connections_renamed', so a rename of the table would have
    // left this assertion green while the reader it describes had gone.
    const enforce = fs.readFileSync(path.join(repo, 'ee/web/sso/src/enforce.ts'), 'utf8')
    assert.match(enforce, /UPDATE sso_connections\b/, 'enforce.ts no longer updates sso_connections')
    const scim = fs.readFileSync(path.join(repo, 'ee/web/scim/src/store.ts'), 'utf8')
    assert.match(scim, /FROM scim_tokens\b/, 'scim/store.ts no longer reads scim_tokens')
  })
})
