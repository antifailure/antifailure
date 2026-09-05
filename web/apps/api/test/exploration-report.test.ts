import { test } from 'node:test'
import assert from 'node:assert/strict'
import { decodeReport } from '../src/github/lifecycle.ts'
import { stateFromReport } from '../src/github/states.ts'

const observed = { name: 'goal', outcome: { verdict: 'pass' }, visited: ['/runs'], evidence: { trace: 'trace.zip' } }
const complete = { Declared: ['goal'], Results: [observed] }
const cases: Record<string, unknown> = {
  complete,
  legacy: undefined,
  unavailable: { ...complete, Unavailable: 'browser refused' },
  absent: { Declared: ['goal'], Results: [] },
  'null-results': { Declared: ['goal'], Results: null },
  blocked: { ...complete, Results: [{ ...observed, outcome: { verdict: 'blocked' } }] },
  'unknown-verdict': { ...complete, Results: [{ ...observed, outcome: { verdict: 'future' } }] },
  'malformed-element': { Declared: ['goal', 'other'], Results: [observed, null] },
  'no-page': { ...complete, Results: [{ ...observed, visited: [] }] },
  'no-trace': { ...complete, Results: [{ ...observed, evidence: {} }] },
  duplicate: { Declared: ['goal', 'other'], Results: [observed, observed] },
  unknown: { ...complete, Results: [{ ...observed, name: 'other' }] },
  malformed: 'unreadable',
}
for (const [name, exploration] of Object.entries(cases)) {
  test(`hosted report exploration ${name}`, () => {
    const counts = decodeReport({ Workflows: [{ Verdict: 'pass' }], Exploration: exploration }).counts
    assert.equal(stateFromReport(counts), name === 'complete' || name === 'legacy' ? 'passed' : 'blocked')
  })
}

test('exploration observations do not hide a real workflow failure', () => {
  assert.equal(stateFromReport(decodeReport({ Workflows: [{ Verdict: 'fail' }], Exploration: { ...complete, Unavailable: 'browser refused' } }).counts), 'failed')
})
