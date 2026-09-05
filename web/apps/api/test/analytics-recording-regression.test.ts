// The dashboard has to distinguish a control plane that never recorded from one
// that recorded and stopped, and the distinction has to survive the wire.
//
// analytics-recording-stopped.test.ts proves the rule. This file proves the
// rule is REACHED: that provenanceFor actually calls it and that the answer
// arrives at a caller. A predicate with no caller is a dead, shippable gap that
// looks exactly like a working feature, and the page can only draw a state the
// server sends it.
//
// The three cases run against ONE database on purpose, in an order that changes
// only one thing at a time. Recording stays off across the first two and the
// only difference between them is that the rollup has run, which is the whole
// claim: the fact that separates "never recorded" from "stopped recording"
// lives in the database rather than in the environment.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  available,
  callProcedure,
  dropOrg,
  seedOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
} from './harness.ts'
import { rollUp } from '../src/analytics/rollup.ts'

const hasDatabase = await available()

interface SeenProvenance {
  recording: boolean
  recordingStopped: boolean
  lastRolledUpAt: string | null
}

describe(
  'the dashboard says when recording stopped rather than only that it is off',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let off: ApiHarness
    let operator: Org

    const provenance = async (h: ApiHarness): Promise<SeenProvenance> => {
      const session = await signInAs(h, operator, 'owner')
      const res = await callProcedure(h, session, 'analytics.overview', 'query', { days: 28 })
      assert.equal(res.status, 200, JSON.stringify(res.body))
      const body = res.body as { result: { data: { provenance: SeenProvenance } } }
      return body.result.data.provenance
    }

    before(async () => {
      const seeding = await startApi()
      operator = await seedOrg(seeding.admin, 'operator')
      await seeding.close()
      off = await startApi({ analyticsOperatorOrgSlug: operator.slug, analyticsSecret: null })

      // Establish the precondition rather than inherit it.
      //
      // analytics_rollup_state is ONE row for the whole installation and it is
      // never torn down between suites, so a database that has run any rollup
      // before, including this file's own second test on a previous run,
      // arrives with last_run_at already set. The first test below would then
      // fail for a reason that has nothing to do with the code it is about.
      // This suite is the thing that decides whether the rollup has run, so it
      // says so out loud here instead of hoping for a clean database.
      await off.admin.unsafe(
        `UPDATE analytics_rollup_state SET last_run_at = NULL, settled_after = NULL`,
      )
    })

    after(async () => {
      await dropOrg(off.admin, operator.orgId)
      await off.close()
    })

    it('does not call it stopped on an installation that never recorded', async () => {
      // Staging, and every self-hosted control plane that never wanted
      // analytics. Recording is off and that is not a fault.
      const p = await provenance(off)
      assert.equal(p.recording, false)
      assert.equal(p.lastRolledUpAt, null)
      assert.equal(p.recordingStopped, false)
    })

    it('calls it stopped once something has rolled up and recording is still off', async () => {
      // Nothing about the server changed between this and the test above. The
      // rollup ran, so the database now remembers that this installation was
      // recording, which is precisely what a rollback cannot take away.
      await rollUp(off.admin, off.clock, { lookbackDays: 1 })
      const p = await provenance(off)
      assert.equal(p.recording, false)
      assert.notEqual(p.lastRolledUpAt, null)
      assert.equal(p.recordingStopped, true)
    })

    it('does not call it stopped while it is recording, rollup or no rollup', async () => {
      // Same database, rollup already run, recording switched back on. The
      // healthy state has to stay quiet or the warning is worthless.
      const on = await startApi({ analyticsOperatorOrgSlug: operator.slug })
      try {
        const session = await signInAs(on, operator, 'owner')
        const res = await callProcedure(on, session, 'analytics.overview', 'query', { days: 28 })
        assert.equal(res.status, 200, JSON.stringify(res.body))
        const p = (res.body as { result: { data: { provenance: SeenProvenance } } }).result.data
          .provenance
        assert.equal(p.recording, true)
        assert.notEqual(p.lastRolledUpAt, null)
        assert.equal(p.recordingStopped, false)
      } finally {
        await on.close()
      }
    })
  },
)
