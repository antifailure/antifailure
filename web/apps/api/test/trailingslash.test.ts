// Trimming a configured address, and the api having one way to do it.
//
// CodeQL #1, #3, #18 and #19 flagged `.replace` with an end anchored slash
// pattern as polynomial. The instrument for that cost is CodeQL's own analysis
// of this change, not a timing assertion here: a timing test on a shared runner
// is a test that fails for reasons that are not the code. These assert the
// behaviour the rewrite has to keep, and that no copy of the old form remains.
import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { trimTrailingSlashes } from '../src/trailingslash.ts'

describe('trimTrailingSlashes', () => {
  test('an empty value stays empty', () => assert.equal(trimTrailingSlashes(''), ''))
  test('one trailing slash goes', () => assert.equal(trimTrailingSlashes('https://app.test/'), 'https://app.test'))
  test('many trailing slashes go', () => assert.equal(trimTrailingSlashes('https://app.test////'), 'https://app.test'))
  test('a value with no trailing slash is unchanged', () => assert.equal(trimTrailingSlashes('https://app.test'), 'https://app.test'))
  test('a slash inside the path is kept', () => assert.equal(trimTrailingSlashes('https://app.test/console/runs//'), 'https://app.test/console/runs'))
  test('a value of nothing but slashes becomes empty', () => assert.equal(trimTrailingSlashes('///'), ''))
})

describe('the api source', () => {
  test('carries no end anchored slash replace, only trimTrailingSlashes', () => {
    const root = new URL('../src/', import.meta.url).pathname
    const found: string[] = []
    const walk = (dir: string): void => {
      for (const entry of readdirSync(dir)) {
        const full = join(dir, entry)
        if (statSync(full).isDirectory()) walk(full)
        else if (/\.(ts|tsx|js|mjs)$/.test(entry)) {
          readFileSync(full, 'utf8').split('\n').forEach((line, index) => {
            if (line.includes('.replace(/\\/+$/')) found.push(`${full.slice(root.length)}:${index + 1}`)
          })
        }
      }
    }
    walk(root)
    assert.deepEqual(found, [], `still trimming with the old pattern: ${found.join(', ')}`)
  })
})
