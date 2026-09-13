// The call sites keep what the rewrite has to keep. In their own file because
// they load the modules that trim, and those load the database layer, which
// the helper and the source scan beside it do not need.
import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { unclaimedDetail } from '../src/github/lifecycle.ts'
import { renderWorkflow } from '../src/github/setup.ts'

describe('the places that trim a configured address', () => {
  test('the unclaimed run detail names the address without its trailing slashes', () => {
    assert.match(unclaimedDetail('https://app.test///'), /`https:\/\/app\.test`/)
  })
  test('the generated workflow defaults to the address without its trailing slashes', () => {
    const file = renderWorkflow('https://app.test///')
    // The address is READ OUT of the workflow and compared exactly, rather than
    // asked for with `includes`. A containment check is satisfied by any URL
    // that merely carries these characters, https://app.test.evil.com among
    // them, which is what CodeQL names as incomplete URL substring
    // sanitization, and it was also weaker than this test means to be: it could
    // not tell `https://app.test` from `https://app.test///` without a second
    // negative assertion about a string that is a prefix of the first one.
    const fallback = file.match(/\$\{\{ vars\.[A-Z0-9_]+ \|\| '([^']*)' \}\}/)
    assert.ok(fallback, `the workflow carries no quoted default:\n${file}`)
    assert.equal(fallback[1], 'https://app.test')
  })
})
