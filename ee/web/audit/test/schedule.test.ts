// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

import { it } from 'node:test'
import assert from 'node:assert/strict'
import { startForwarder } from '../src/index.ts'

const empty = { read: 0, delivered: 0, unlicensed: 0, disabled: 0, refused: 0, from: 0, to: 0, organizations: [] }

it('a stopped forwarder makes no subsequent pass', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  let calls = 0
  const forwarder = { pass: async () => { calls += 1; return empty } }
  const running = startForwarder(forwarder, 10)
  assert.equal(calls, 1)
  await Promise.resolve()
  running.stop()
  t.mock.timers.tick(100)
  assert.equal(calls, 1, 'shutdown left the audit polling loop running')
})

it('an unfinished pass prevents an overlapping pass', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  let calls = 0
  let finish!: (value: typeof empty) => void
  const forwarder = {
    pass: () => { calls += 1; return new Promise<typeof empty>((resolve) => { finish = resolve }) },
  }
  const running = startForwarder(forwarder, 10)
  t.mock.timers.tick(100)
  assert.equal(calls, 1, 'one slow collector caused overlapping deliveries')
  running.stop()
  finish(empty)
  await Promise.resolve()
})

it('a failed pass is reported and the next scheduled pass still runs', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] })
  let calls = 0
  const errors: unknown[] = []
  const forwarder = {
    pass: async () => { calls += 1; if (calls === 1) throw new Error('collector unavailable'); return empty },
  }
  const running = startForwarder(forwarder, 10, (err) => errors.push(err))
  await Promise.resolve()
  assert.equal(errors.length, 1)
  t.mock.timers.tick(10)
  assert.equal(calls, 2, 'one collector failure disabled the schedule')
  running.stop()
})
