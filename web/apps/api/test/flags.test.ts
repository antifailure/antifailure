// What a switch actually switches.
//
// Two things are being proved. That the ORDER in flags.ts is the order the
// evaluator runs in, exhaustively and without a database. And that a kill
// switch stops a real route, which is the half a flag system usually ships
// without: a table, an admin screen, and no call site.
//
// The case worth reading twice is the last one in the first block. An unknown
// flag is OFF for a rollout and NOT KILLED for a kill switch, and those are
// opposite defaults on purpose. Getting it wrong the other way would refuse
// every checkout on every self-hosted installation, none of which has ever
// created a flag row.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { KNOWN_FLAGS, bucketOf, decide, evaluateFlag, killSwitch } from '../src/flags.ts'
import {
  available, callProcedure, dropOrg, errorCode, seedOrg, signInAs, startApi,
  stripeAgainstMockPack, type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const srcDir = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src')

function flag(over: Record<string, unknown> = {}) {
  return {
    key: 'f', state: 'targeted', rollout_percent: 0, internal_only: false, killed_at: null,
    ...over,
  } as never
}
function target(over: Record<string, unknown> = {}) {
  return { flag_key: 'f', kind: 'organization', value: 'org-1', allow: true, ...over } as never
}

describe('the flag catalogue', () => {
  it('every known flag names a call site that exists', async () => {
    for (const [key, spec] of Object.entries(KNOWN_FLAGS)) {
      if (spec.checkedAt === null) {
        assert.ok(spec.notCheckedBecause, `${key} is not checked anywhere and does not say why`)
        continue
      }
      const [file, symbol] = spec.checkedAt.split(':')
      const source = await readFile(path.join(srcDir, file!), 'utf8')
      assert.ok(
        source.includes(symbol!),
        `${key} claims to be checked at ${spec.checkedAt}, and ${file} never calls ${symbol}`,
      )
    }
  })
})

describe('evaluation runs in the order the header states', () => {
  const org = { orgId: 'org-1', userId: 'user-1', plan: 'team', repository: 'acme/app' }

  it('1. a killed flag is off even when its state says on', () => {
    const v = decide(flag({ state: 'on', killed_at: new Date() }), [target()], org)
    assert.equal(v.on, false)
    assert.equal(v.because, 'killed')
  })

  it('2. state off is off even with an allow target', () => {
    assert.equal(decide(flag({ state: 'off' }), [target()], org).because, 'off')
  })

  it('3. a deny target beats state on, so one tenant can be pulled out of a rollout', () => {
    const v = decide(flag({ state: 'on' }), [target({ allow: false })], org)
    assert.equal(v.on, false)
    assert.equal(v.because, 'denied')
    // ... and beats an allow target for the same subject, whichever order they
    // came back in.
    assert.equal(
      decide(flag(), [target(), target({ kind: 'user', value: 'user-1', allow: false })], org).on,
      false,
    )
  })

  it('4 and 5. state on, then an allow target', () => {
    assert.equal(decide(flag({ state: 'on' }), [], org).because, 'on')
    assert.equal(decide(flag(), [target()], org).because, 'allowed')
    assert.equal(decide(flag(), [target({ value: 'somebody-else' })], org).on, false)
  })

  it('6. internal_only is checked after the allow list, so a design partner grant works', () => {
    assert.equal(decide(flag({ internal_only: true }), [], org).because, 'internal-only')
    assert.equal(decide(flag({ internal_only: true }), [], { ...org, internal: true }).on, true)
    // The row an operator added for one external customer has to mean
    // something, or it is a control that silently does nothing.
    assert.equal(decide(flag({ internal_only: true }), [target()], org).on, true)
  })

  it('7. the rollout is stable, and two flags at ten percent are not the same tenth', () => {
    const a = decide(flag({ key: 'a', rollout_percent: 100 }), [], org)
    assert.equal(a.on, true)
    assert.equal(decide(flag({ rollout_percent: 0 }), [], org).on, false)

    // Stable: the same subject gets the same answer every time.
    const once = bucketOf('a', org)
    assert.equal(bucketOf('a', org), once)
    // And different flags disagree, or one unlucky customer is in the first
    // wave of every rollout forever.
    const buckets = new Set(['a', 'b', 'c', 'd', 'e'].map((k) => bucketOf(k, org)))
    assert.ok(buckets.size > 1, 'every flag put this organization in the same bucket')
  })

  it('a subject with nothing stable to hash is not in the rollout', () => {
    // Rolling a die per request would flip the feature on and off between two
    // calls from the same caller, which is worse than off.
    assert.equal(decide(flag({ rollout_percent: 50 }), [], {}).on, false)
  })

  it('a repository target matches an owner wildcard and not a longer name', () => {
    const t = [target({ kind: 'repository', value: 'acme/*' })]
    assert.equal(decide(flag(), t, { repository: 'acme/app' }).on, true)
    assert.equal(decide(flag(), t, { repository: 'acme/other' }).on, true)
    // The slash is required, or `acme/*` would turn a feature on for
    // `acmecorp/app`, a different customer with a similar name.
    assert.equal(decide(flag(), t, { repository: 'acmecorp/app' }).on, false)
  })

  it('a target of a kind this build does not know never matches', () => {
    assert.equal(decide(flag(), [target({ kind: 'phase-of-the-moon' })], org).on, false)
  })
})

describe('a kill switch and a rollout default in opposite directions', async () => {
  if (!(await available())) {
    it('skipped: no database', () => {})
    return
  }

  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    h = await startApi({ stripe: (await stripeAgainstMockPack()).billing })
    org = await seedOrg(h.admin, 'flags')
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await h.admin`DELETE FROM feature_flag_targets WHERE flag_key LIKE 'billing.%'`
    await h.admin`DELETE FROM feature_flags WHERE key LIKE 'billing.%'`
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('an unknown flag is off for a rollout and NOT killed for a kill switch', async () => {
    await h.pool.withTenant({ orgId: org.orgId }, async (db) => {
      assert.equal((await evaluateFlag(db, 'never.created', { orgId: org.orgId })).on, false)
      assert.equal(
        await killSwitch(db, 'never.created', { orgId: org.orgId }), null,
        'a flag nobody ever created switched something off',
      )
    })
  })

  async function checkout() {
    return callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team', seats: 2,
      successUrl: 'https://app.test/ok', cancelUrl: 'https://app.test/no',
    })
  }

  it('checkout works with no flag row at all, which is every self-hosted installation', async () => {
    const answer = await checkout()
    assert.equal(errorCode(answer.body), null, JSON.stringify(answer.body))
  })

  it('killing the flag stops checkout, and the message says it is deliberate', async () => {
    await h.admin`
      INSERT INTO feature_flags (key, description, state, updated_by_label)
      VALUES ('billing.checkout', 'New subscriptions', 'off', 'ops@antifailure.test')`
    const refused = await checkout()
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    const said = JSON.stringify(refused.body)
    assert.match(said, /switched off right now/)
    // A sudden refusal reads as a broken product unless it says otherwise, and
    // a customer who believes the product is broken opens a ticket.
    assert.match(said, /deliberate and temporary/)
    assert.match(said, /billing\.checkout/)
  })

  it('turning it back on lets checkout through again', async () => {
    await h.admin`UPDATE feature_flags SET state = 'on' WHERE key = 'billing.checkout'`
    assert.equal(errorCode((await checkout()).body), null)
  })

  it('one organization can be denied while the flag stays on for everybody else', async () => {
    await h.admin`
      INSERT INTO feature_flag_targets (flag_key, kind, value, allow, org_id, reason, created_by_label)
      VALUES ('billing.checkout', 'organization', ${org.orgId}, false, ${org.orgId},
              'Chargebacks under review, AF-990', 'ops@antifailure.test')`
    const refused = await checkout()
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(refused.body), /switched off right now/)

    // The flag itself is still on. Turning it off for everybody to stop one
    // tenant is the failure the deny target exists to avoid, so this asserts
    // the flag was not touched.
    const [row] = await h.admin<{ state: string; killed_at: Date | null }[]>`
      SELECT state, killed_at FROM feature_flags WHERE key = 'billing.checkout'`
    assert.equal(row!.state, 'on')
    assert.equal(row!.killed_at, null)
  })

  it('one tenant cannot read another tenant\'s targets', async () => {
    const other = await seedOrg(h.admin, 'flags-other')
    try {
      const seen = await h.pool.withTenant({ orgId: other.orgId }, async (db) => {
        const { sql } = await import('drizzle-orm')
        return db.execute<{ n: string }>(sql`SELECT count(*) AS n FROM feature_flag_targets`)
      })
      // The deny target above names this suite's organization. Another tenant
      // reading it would be reading a customer list.
      assert.equal(Number(seen[0]!.n), 0)
    } finally {
      await dropOrg(h.admin, other.orgId)
    }
  })
})
