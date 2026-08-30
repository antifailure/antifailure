// The pure parts of this application, tested without a browser or a server.
//
// Two of them, and both earned their place by being wrong once.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { asJson } from '../lib/json.ts'

describe('reading a jsonb value', () => {
  it('passes structure through untouched', () => {
    const array = ['one', 'two']
    assert.equal(asJson(array), array, 'an array that arrived as an array was copied')
    const object = { a: 1 }
    assert.equal(asJson(object), object)
    assert.deepEqual(asJson([]), [])
  })

  it('unwraps a value that was encoded twice', () => {
    // The failure this exists for. A writer that stringified before handing the
    // value to a driver which stringifies again stores the jsonb *string*
    // `"[\"a\"]"`, and the page rendered a paragraph of escaped quotes where
    // five numbered steps belonged.
    assert.deepEqual(asJson('["Open /login","Press Send link"]'), [
      'Open /login',
      'Press Send link',
    ])
    assert.deepEqual(asJson('{"host":"api.stripe.com","mode":"mock"}'), {
      host: 'api.stripe.com',
      mode: 'mock',
    })
    assert.deepEqual(asJson('[]'), [])
  })

  it('leaves text alone, including text that looks like it might not be', () => {
    // One level and no more. A string that parses to another string is a
    // string, and unwrapping forever would rewrite the value.
    assert.equal(asJson('"hello"'), '"hello"')
    assert.equal(asJson('a note somebody typed'), 'a note somebody typed')
    // Prose that opens with a bracket parses as nothing and comes back whole,
    // because showing it is more use than hiding it.
    assert.equal(asJson('[see the runbook] this failed'), '[see the runbook] this failed')
    assert.equal(asJson('{not json'), '{not json')
  })

  it('passes anything that is not a string straight through', () => {
    assert.equal(asJson(null), null)
    assert.equal(asJson(undefined), undefined)
    assert.equal(asJson(7), 7)
    assert.equal(asJson(true), true)
  })
})
