// The migration filenames themselves, before anything runs them.
//
// The runner applies files in filename order and records each by name. Nothing
// in it notices that two files claim the same number, and it cannot: by the
// time it is looking at them they are just a sorted list.
//
// This is not hypothetical. On 2026-08-28 two branches each added a migration
// numbered 0012, one for email sign-in and one for device authorization,
// written by different people within an hour of each other. Whichever merged
// second would have applied SECOND on databases that took both, and FIRST on a
// database that only ever saw it, so two installations would have run the same
// two migrations in different orders and nothing would ever have said so.
//
// The fix is cheap and belongs here rather than in a review checklist: a number
// used twice fails the build, on the branch, before the merge.
//
// Needs no database, which is the point. A gate that only runs where Postgres
// is available is a gate that does not run on the machine where the mistake is
// made.

import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import { readdir } from 'node:fs/promises'
import { migrationsDir } from '../src/migrate.ts'

const files = (await readdir(migrationsDir)).filter((f) => f.endsWith('.sql')).sort()

describe('migration filenames', () => {
  test('there is at least one, so an empty directory cannot pass this suite', () => {
    // Without this every assertion below is vacuously true, which is the
    // failure mode of every test that iterates a collection.
    assert.ok(files.length > 0, `no migrations found in ${migrationsDir}`)
  })

  test('every filename is <4 digits>_<name>.sql', () => {
    const bad = files.filter((f) => !/^\d{4}_[a-z0-9_]+\.sql$/.test(f))
    assert.deepEqual(
      bad,
      [],
      `these do not match NNNN_lower_snake_case.sql, so they sort unpredictably ` +
        `against the others:\n  ${bad.join('\n  ')}`,
    )
  })

  test('no number is used twice', () => {
    const byNumber = new Map<string, string[]>()
    for (const f of files) {
      const n = f.slice(0, 4)
      byNumber.set(n, [...(byNumber.get(n) ?? []), f])
    }
    const duplicated = [...byNumber.entries()].filter(([, fs]) => fs.length > 1)
    assert.deepEqual(
      duplicated.map(([n, fs]) => `${n}: ${fs.join(', ')}`),
      [],
      'two migrations share a number. They will apply in a different relative\n' +
        'order on a database that receives them together than on one that\n' +
        'receives them apart, and nothing downstream will ever report it.\n' +
        'Renumber the one that has not merged yet.',
    )
  })

  test('the numbers run consecutively from 0001', () => {
    // A gap is not harmful on its own, but it is almost always the visible half
    // of a migration that was written, numbered, and then deleted or never
    // committed -- which means a database somewhere may have applied it.
    const numbers = files.map((f) => Number(f.slice(0, 4)))
    const expected = numbers.map((_, i) => i + 1)
    assert.deepEqual(
      numbers,
      expected,
      `the migration numbers are ${numbers.join(', ')} but should be ` +
        `${expected.join(', ')}. A gap usually means a file was removed after ` +
        `it had already been applied somewhere.`,
    )
  })
})
