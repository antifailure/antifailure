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
    assert.ok(file.includes('https://app.test'), file)
    assert.ok(!file.includes('https://app.test/'), `a trailing slash reached the workflow:\n${file}`)
  })
})
