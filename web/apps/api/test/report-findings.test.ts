import assert from 'node:assert/strict'
import { test } from 'node:test'
import { countPolicyAndLoad } from '../src/github/findings.ts'
import { stateFromReport, type ReportCounts } from '../src/github/states.ts'
import { decodeReport } from '../src/github/lifecycle.ts'

for (const [name, run, expected] of [
  ['policy failure', { Findings: [{ Level: 'fail' }] }, 'failed'],
  ['warning', { Findings: [{ Level: 'warn' }] }, 'passed'],
  ['ignored', { Findings: [{ Level: 'ignore' }] }, 'passed'],
  ['unknown finding', { Findings: [{ Level: 'new-level' }] }, 'unverified'],
  ['malformed finding', { Findings: [null] }, 'unverified'],
  ['malformed list', { Findings: {} }, 'unverified'],
  ['one bad row preserves failure', { Findings: [null, { Level: 'fail' }] }, 'failed'],
  ['incomplete load', { Load: { Sent: 4, Unavailable: 'generator stopped' } }, 'blocked'],
  ['empty load', { Load: { Sent: 0 } }, 'unverified'],
  ['malformed load', { Load: [] }, 'unverified'],
  ['missing count', { Load: {} }, 'unverified'],
  ['NaN count', { Load: { Sent: NaN } }, 'unverified'],
  ['healthy load', { Load: { Sent: 4 } }, 'passed'],
  ['old report without load', {}, 'passed'],
  ['failed finding outranks incomplete load', { Findings: [{ Level: 'fail' }], Load: { Sent: 0, Unavailable: 'stopped' } }, 'failed'],
] as const) {
  test(name, () => {
    const counts: ReportCounts = { passed: 1, failed: 0, flaky: 0, blocked: 0, unverified: 0 }
    countPolicyAndLoad(run, counts)
    assert.equal(stateFromReport(counts), expected)
  })
}

test('the actual callback decoder includes load policy failure', () => {
  const decoded = decodeReport({ Workflows: [{ Verdict: 'pass' }], Findings: [{ Level: 'fail', Rule: 'load_regression' }] })
  assert.equal(stateFromReport(decoded.counts), 'failed')
})

test('the actual callback decoder includes incomplete load', () => {
  const decoded = decodeReport({ Workflows: [{ Verdict: 'pass' }], Load: { Sent: 1, Unavailable: 'interrupted' } })
  assert.equal(stateFromReport(decoded.counts), 'blocked')
})
