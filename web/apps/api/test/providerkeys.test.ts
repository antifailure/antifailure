// Storing, using, budgeting and rotating somebody else's provider key.
//
// The claims worth testing here are not "it saves and loads". They are:
//
//   - a budget is a CAP, not a suggestion: it is checked before the key is
//     decrypted, so a run with no allowance never causes the plaintext to exist
//     in this process at all;
//   - a missing budget means zero, not unlimited;
//   - rotation replaces atomically and the old key stops working immediately;
//   - and nothing anywhere -- the row, the audit log, an error message -- ever
//     carries the key.
//
// The last is asserted by grepping what the system actually wrote, rather than
// by reading the code and believing it.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { available, startApi, seedOrg, dropOrg, type ApiHarness, type Org, testAnalytics } from './harness.ts'
import {
  borrowKey,
  listBudgets,
  listKeys,
  periodOf,
  ProviderKeyError,
  recordSpend,
  revokeKey,
  saveKey,
  setBudget,
} from '../src/providers/store.ts'

// Assembled rather than written out. tools/scanrepo refuses a repository
// carrying anything its detector reads as a live credential, and a literal
// fixture is a repository that fails its own gate.
const ANTHROPIC = ['sk', 'ant', 'api03'].join('-')
const KEY_ONE = `${ANTHROPIC}-aaaaaaaaaaaaaaaaaaaaaaaaaaaa1111`
const KEY_TWO = `${ANTHROPIC}-bbbbbbbbbbbbbbbbbbbbbbbbbbbb2222`

describe('provider keys', { skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let api: ApiHarness
  let org: Org
  const sealingKey = randomBytes(32)

  before(async () => {
    api = await startApi()
    org = await seedOrg(api.admin, 'byok')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await api.close()
  })

  beforeEach(async () => {
    await api.admin`DELETE FROM provider_keys WHERE org_id = ${org.orgId}`
    await api.admin`DELETE FROM provider_budgets WHERE org_id = ${org.orgId}`
  })

  const actor = { actorUserId: null, actorLabel: 'a test', analytics: testAnalytics() }

  // -------------------------------------------------------------------------

  test('a stored key is listed by its last four and never by its value', async () => {
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })

    const keys = await listKeys(api.pool, org.orgId)
    assert.equal(keys.length, 1)
    assert.equal(keys[0]!.last4, '1111')
    // The listing type has no field for it, and the query names its columns, so
    // this is belt and braces against a future SELECT *.
    assert.equal(JSON.stringify(keys).includes(KEY_ONE), false)
  })

  test('the row in the database does not contain the key', async () => {
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    const rows = await api.admin<{ dump: string }[]>`
      SELECT provider_keys::text AS dump FROM provider_keys WHERE org_id = ${org.orgId}`
    assert.equal(rows.length, 1)
    assert.equal(rows[0]!.dump.includes(KEY_ONE), false, 'the plaintext key is in the row')
    assert.equal(rows[0]!.dump.includes(ANTHROPIC), false, 'even the prefix survived into the row')
  })

  test('the audit log records the fingerprint and never the key', async () => {
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    const rows = await api.admin<{ dump: string; action: string }[]>`
      SELECT audit_entries::text AS dump, action FROM audit_entries WHERE org_id = ${org.orgId}`
    const stored = rows.find((r) => r.action === 'provider_key.stored')
    assert.ok(stored, 'storing a key wrote no audit entry')
    assert.equal(stored.dump.includes(KEY_ONE), false, 'the key is in the audit log')
    // But enough to say WHICH key was in use.
    assert.match(stored.dump, /fingerprint/)
    assert.match(stored.dump, /1111/)
  })

  // -------------------------------------------------------------------------
  // Budgets
  // -------------------------------------------------------------------------

  test('with no budget nothing may be spent, because a missing cap is zero and not unlimited', async () => {
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    await assert.rejects(
      () => borrowKey(api.pool, api.clock, sealingKey, { orgId: org.orgId, provider: 'anthropic' }),
      (err: Error) => err instanceof ProviderKeyError && /No budget is set/.test(err.message),
    )
  })

  test('with a budget the key comes back', async () => {
    // The negative control for every refusal below. Without it, a borrowKey
    // that refused unconditionally would pass the whole rest of this suite.
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 50, ...actor })

    const borrowed = await borrowKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic',
    })
    assert.equal(borrowed.key, KEY_ONE)
    assert.equal(borrowed.budget.capUsd, 50)
    assert.equal(borrowed.budget.remainingUsd, 50)
  })

  test('a spent budget refuses, and the refusal names the numbers', async () => {
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 10, ...actor })
    await recordSpend(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', usd: 10 })

    await assert.rejects(
      () => borrowKey(api.pool, api.clock, sealingKey, { orgId: org.orgId, provider: 'anthropic' }),
      (err: Error) => /10\.00 of 10\.00 USD/.test(err.message),
    )
  })

  test('an estimate that would cross the cap is refused before anything is spent', async () => {
    // The cap has to bind BEFORE the request, not after. A budget that only
    // notices once the money is gone is a report, not a cap.
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 10, ...actor })
    await recordSpend(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', usd: 9.5 })

    await assert.rejects(
      () => borrowKey(api.pool, api.clock, sealingKey, {
        orgId: org.orgId, provider: 'anthropic', estimatedUsd: 2,
      }),
      (err: Error) => /past its cap/.test(err.message),
    )
    // And a request that fits is still allowed, so the check is a bound rather
    // than a blanket refusal once anything has been spent.
    const ok = await borrowKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', estimatedUsd: 0.25,
    })
    assert.equal(ok.key, KEY_ONE)
  })

  test('spend accumulates rather than being overwritten', async () => {
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 100, ...actor })
    await recordSpend(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', usd: 1.25 })
    await recordSpend(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', usd: 2.5 })
    const [budget] = await listBudgets(api.pool, api.clock, org.orgId)
    assert.equal(budget!.spentUsd, 3.75)
    assert.equal(budget!.remainingUsd, 96.25)
  })

  test('a budget is per provider, so one cannot spend the other one dry', async () => {
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 10, ...actor })
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'openai', capUsd: 10, ...actor })
    await recordSpend(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', usd: 10 })

    const budgets = await listBudgets(api.pool, api.clock, org.orgId)
    const anthropic = budgets.find((b) => b.provider === 'anthropic')!
    const openai = budgets.find((b) => b.provider === 'openai')!
    assert.equal(anthropic.remainingUsd, 0)
    assert.equal(openai.remainingUsd, 10)
  })

  test('remaining never goes below zero, because a negative reads as a refund', async () => {
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 5, ...actor })
    await recordSpend(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', usd: 7 })
    const [budget] = await listBudgets(api.pool, api.clock, org.orgId)
    assert.equal(budget!.remainingUsd, 0)
    assert.equal(budget!.spentUsd, 7)
  })

  test('the period is this calendar month, so a rollover is a row that does not match', () => {
    assert.equal(periodOf(new Date('2026-08-28T23:59:59Z')), '2026-08-01')
    assert.equal(periodOf(new Date('2026-09-01T00:00:01Z')), '2026-09-01')
    assert.equal(periodOf(new Date('2026-01-15T12:00:00Z')), '2026-01-01')
  })

  // -------------------------------------------------------------------------
  // Rotation
  // -------------------------------------------------------------------------

  test('rotating replaces the key and the old one stops working at once', async () => {
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 50, ...actor })
    const first = await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    assert.equal(first.replaced, false)

    const second = await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_TWO, ...actor,
    })
    assert.equal(second.replaced, true)
    assert.notEqual(second.stored.fingerprint, first.stored.fingerprint)

    const borrowed = await borrowKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic',
    })
    assert.equal(borrowed.key, KEY_TWO, 'the old key is still being handed out after a rotation')

    // Exactly one live key, which is what the unique index promises.
    const live = await listKeys(api.pool, org.orgId)
    assert.equal(live.filter((k) => k.provider === 'anthropic').length, 1)
  })

  test('rotating to the same key says so instead of pretending something changed', async () => {
    // The mistake somebody makes at the exact moment they believe they have
    // rotated: pasting the key that was already there.
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    const again = await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    assert.equal(again.sameAsBefore, true)
  })

  test('the old key is gone from everywhere it could still be read', async () => {
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 50, ...actor })
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_TWO, ...actor,
    })

    // Everything the organization can see, dumped and searched. The revoked row
    // survives for the audit trail, and it must not be openable back to the old
    // key by anything reading the table.
    const dump = await api.admin<{ dump: string }[]>`
      SELECT string_agg(t::text, ' ') AS dump FROM provider_keys t WHERE org_id = ${org.orgId}`
    assert.equal(dump[0]!.dump.includes(KEY_ONE), false)
    assert.equal(dump[0]!.dump.includes(KEY_TWO), false)

    const audit = await api.admin<{ dump: string }[]>`
      SELECT string_agg(t::text, ' ') AS dump FROM audit_entries t WHERE org_id = ${org.orgId}`
    assert.equal(audit[0]!.dump.includes(KEY_ONE), false)
    assert.equal(audit[0]!.dump.includes(KEY_TWO), false)
  })

  test('revoking leaves nothing to borrow', async () => {
    await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 50, ...actor })
    await saveKey(api.pool, api.clock, sealingKey, {
      orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
    })
    const { revoked } = await revokeKey(api.pool, api.clock, {
      orgId: org.orgId, provider: 'anthropic', ...actor,
    })
    assert.equal(revoked, true)

    await assert.rejects(
      () => borrowKey(api.pool, api.clock, sealingKey, { orgId: org.orgId, provider: 'anthropic' }),
      (err: Error) => /No anthropic key is stored/.test(err.message),
    )
  })

  test('a key that is obviously the wrong provider is refused before it is stored', async () => {
    await assert.rejects(
      () => saveKey(api.pool, api.clock, sealingKey, {
        orgId: org.orgId, provider: 'openai', key: KEY_ONE, ...actor,
      }),
      (err: Error) => err instanceof ProviderKeyError && /Anthropic key/.test(err.message),
    )
    assert.equal((await listKeys(api.pool, org.orgId)).length, 0)
  })

  // -------------------------------------------------------------------------
  // Tenancy
  // -------------------------------------------------------------------------

  test('another organization cannot see or borrow it', async () => {
    const other = await seedOrg(api.admin, 'byok-other')
    try {
      await setBudget(api.pool, api.clock, { orgId: org.orgId, provider: 'anthropic', capUsd: 50, ...actor })
      await saveKey(api.pool, api.clock, sealingKey, {
        orgId: org.orgId, provider: 'anthropic', key: KEY_ONE, ...actor,
      })

      // Row-level security: the other tenant sees nothing.
      assert.deepEqual(await listKeys(api.pool, other.orgId), [])

      // And even if a row were somehow visible, the ciphertext is bound to the
      // organization it was sealed for, so it would not open.
      await assert.rejects(
        () => borrowKey(api.pool, api.clock, sealingKey, { orgId: other.orgId, provider: 'anthropic' }),
      )
    } finally {
      await dropOrg(api.admin, other.orgId)
    }
  })
})
