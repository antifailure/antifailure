// What the audit filter matches, which is the whole of what the box is for.
//
// The filter was `action = $1` while the control offered for it is a text box
// placeheld "filter by action", and an action is a dotted name. So typing a word
// that appears in the log answered "No entries with that action", about entries
// the log holds. These tests are about the two halves of the fix: a substring
// finds the entry, and a wildcard character somebody types is a character rather
// than a pattern, or a filter would quietly widen to everything.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import {
  available,
  startApi,
  seedOrg,
  signInAs,
  callProcedure,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'

const hasDb = await available()

describe(
  'the audit filter, against the log it reads',
  { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    let org: Org
    let session: SignedIn

    const ACTIONS = [
      'billing.plan.changed',
      'billing.invoice.paid',
      'member.invited',
      'policy.100%.reviewed',
      'env_started',
    ]

    before(async () => {
      h = await startApi()
      org = await seedOrg(h.admin, 'auditfilter')
      session = await signInAs(h, org, 'owner')
      for (const action of ACTIONS) {
        await h.admin`
          INSERT INTO audit_entries (org_id, actor_label, action, target_type, target_id, origin, entry_hash)
          VALUES (${org.orgId}, 'somebody', ${action}, 'organization', ${org.orgId}, 'console', ${randomUUID()})`
      }
    })

    after(async () => {
      await h.stop()
    })

    async function filtered(action?: string): Promise<string[]> {
      const input = action === undefined ? { limit: 50 } : { limit: 50, action }
      const r = await callProcedure(h, session, 'audit.list', 'query', input)
      assert.equal(r.status, 200, `audit.list answered ${r.status}`)
      const body = r.body as { result?: { data?: unknown } }
      const rows = (body.result?.data ?? body) as { action: string }[]
      assert.ok(Array.isArray(rows), 'audit.list returned something other than rows')
      return rows.map((row) => row.action).sort()
    }

    test('no filter reads the whole log', async () => {
      assert.deepEqual(await filtered(), [...ACTIONS].sort())
    })

    test('a word inside a dotted name finds every entry that carries it', async () => {
      // The defect, in one assertion: this used to be empty.
      assert.deepEqual(await filtered('billing'), [
        'billing.invoice.paid',
        'billing.plan.changed',
      ])
    })

    test('a middle fragment matches, not just a prefix', async () => {
      assert.deepEqual(await filtered('invoice'), ['billing.invoice.paid'])
    })

    test('the whole name still matches, so nothing a reader used to do broke', async () => {
      assert.deepEqual(await filtered('member.invited'), ['member.invited'])
    })

    test('case does not decide it, because a reader types lowercase', async () => {
      assert.deepEqual(await filtered('BILLING.Plan'), ['billing.plan.changed'])
    })

    test('a typed percent is a character, not a wildcard', async () => {
      // If % reached LIKE unescaped, this would return the whole log, and a
      // filter would be a way to ask for everything while looking specific.
      assert.deepEqual(await filtered('100%'), ['policy.100%.reviewed'])
      assert.deepEqual(await filtered('%'), ['policy.100%.reviewed'])
    })

    test('a typed underscore matches an underscore and not any character', async () => {
      assert.deepEqual(await filtered('env_started'), ['env_started'])
      assert.deepEqual(await filtered('_'), ['env_started'])
    })

    test('a word that is in no action matches nothing', async () => {
      assert.deepEqual(await filtered('deployment'), [])
    })
  },
)
