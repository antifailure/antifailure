// Telling "never recorded" apart from "stopped recording".
//
// Both render as a dashboard that is not moving, and only one of them is a
// fault. An installation that never switched analytics on is running the way
// its operator meant it to: staging does this, and so does any self-hosted
// control plane that does not want the numbers. An installation that recorded
// until last Tuesday and does not now has lost something, and the usual way to
// get there is a rollback to a container revision from before the analytics
// variables existed, which takes the environment back with it.
//
// The environment cannot answer this. A rolled back revision and a control
// plane that never wanted analytics carry the identical absence, so a rule that
// reads "the variable is missing" refuses both or neither. `last_run_at` is
// written by the rollup and lives in the database, which is the one party a
// rollback does not move, so it is the fact that separates them.
//
// Four cells, one test each rather than four assertions in one, because an
// assertion that throws stops the ones after it and a cell that never ran looks
// exactly like a cell that passed.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'

import { recordingStopped } from '../src/analytics/read.ts'

const A_ROLLUP_HAS_RUN = '2026-09-04T03:17:00.000Z'

describe('whether analytics recorded once and stopped', () => {
  it('is not stopped while it is recording and has rolled up', () => {
    // The ordinary healthy installation. Nothing to say.
    assert.equal(recordingStopped(true, A_ROLLUP_HAS_RUN), false)
  })

  it('is not stopped while it is recording and has never rolled up', () => {
    // Switched on, nothing computed yet. The page says the rollup has never
    // run, which is a different sentence and already exists.
    assert.equal(recordingStopped(true, null), false)
  })

  it('is NOT stopped when it never recorded at all', () => {
    // The cell that decides the whole design. Staging is here, and so is every
    // self-hosted control plane whose operator never set the secret. If this
    // one answered true, the check would cry wolf on every correctly
    // configured installation that does not want analytics, and a warning that
    // is wrong most of the time is one nobody reads by the time it is right.
    assert.equal(recordingStopped(false, null), false)
  })

  it('IS stopped when it recorded before and is not recording now', () => {
    // The regression. Recording is off, yet something has rolled up, so this
    // installation was recording at some point and is not now.
    assert.equal(recordingStopped(false, A_ROLLUP_HAS_RUN), true)
  })
})
