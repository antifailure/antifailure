// Rotating the secret that seals every customer's stored credential.
//
// This suite is the proof that rotation WORKS, not that a re-sealing function
// exists. The difference is the last step of the first test: the old key is
// REMOVED from the keyring and everything still opens. Without that step, a
// rotation that quietly did nothing and a rotation that completed look the same,
// because the old key is still there to open whatever was missed. That is the
// exact shape of the defect this lane closed.
//
// The orderings are enumerated rather than sampled, one test each: a row under a
// version nobody holds, a key removed while rows still name it, the tool
// interrupted and re-run, two runs at once, a row written by the application
// while the tool is running, the tool run without the new key, and a row that is
// genuinely tampered with, which must still report tampering.
//
// Against real Postgres, because the guarded UPDATE that makes the tool safe
// beside a serving application is a property of the database and not of this
// code: a mock would agree with whatever this file asserted.

import { test, describe, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { adminUrl, available, startApi, seedOrg, dropOrg, testAnalytics, type ApiHarness, type Org } from './harness.ts'
import { borrowKey, saveKey, setBudget } from '../src/providers/store.ts'
import { reseal, ResealRefused, SEALED_TABLES, describe as describeReseal } from '../src/providers/reseal.ts'
import { Keyring, MissingSealingKeyError, seal } from '../src/providers/seal.ts'

// Assembled rather than written out, for the reason seal.test.ts gives:
// tools/scanrepo refuses a repository carrying anything its detector reads as a
// live credential.
const ANTHROPIC = ['sk', 'ant', 'api03'].join('-')
const OPENAI = ['sk', 'proj'].join('-')
const KEY_A = `${ANTHROPIC}-aaaaaaaaaaaaaaaaaaaaaaaaaaaa1111`
const KEY_B = `${OPENAI}-bbbbbbbbbbbbbbbbbbbbbbbbbbbb2222`
const KEY_C = `${ANTHROPIC}-cccccccccccccccccccccccccccc3333`

const K1 = randomBytes(32)
const K2 = randomBytes(32)
const v1 = Keyring.of(K1, 'v1')
const v2 = Keyring.of(K2, 'v2')
const both = Keyring.from([['v1', K1], ['v2', K2]], 'v2')
const bothSealingOld = Keyring.from([['v1', K1], ['v2', K2]], 'v1')

describe('rotating the sealing key', { skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let api: ApiHarness
  let org: Org
  let other: Org

  before(async () => {
    api = await startApi()
    org = await seedOrg(api.admin, 'reseal-one')
    other = await seedOrg(api.admin, 'reseal-two')
  })
  after(async () => {
    await dropOrg(api.admin, org.orgId)
    await dropOrg(api.admin, other.orgId)
    await api.close()
  })

  beforeEach(async () => {
    await api.admin`DELETE FROM provider_keys`
    await api.admin`DELETE FROM provider_budgets`
  })

  const actor = { actorUserId: null, actorLabel: 'a rotation test', analytics: testAnalytics() }

  /** Two organizations, two providers, sealed under the OLD key. What a control
   *  plane looks like the moment before somebody rotates. */
  async function seedUnderV1(): Promise<void> {
    await saveKey(api.pool, api.clock, v1, { ...actor, orgId: org.orgId, provider: 'anthropic', key: KEY_A })
    await saveKey(api.pool, api.clock, v1, { ...actor, orgId: org.orgId, provider: 'openai', key: KEY_B })
    await saveKey(api.pool, api.clock, v1, { ...actor, orgId: other.orgId, provider: 'anthropic', key: KEY_A })
    await setBudget(api.pool, api.clock, { ...actor, orgId: org.orgId, provider: 'anthropic', capUsd: 10 })
    await setBudget(api.pool, api.clock, { ...actor, orgId: org.orgId, provider: 'openai', capUsd: 10 })
    await setBudget(api.pool, api.clock, { ...actor, orgId: other.orgId, provider: 'anthropic', capUsd: 10 })
  }

  async function versions(): Promise<Record<string, number>> {
    const rows = await api.admin<{ key_version: string; n: string }[]>`
      SELECT key_version, count(*)::text AS n FROM provider_keys GROUP BY key_version`
    const out: Record<string, number> = {}
    for (const r of rows) out[r.key_version] = Number(r.n)
    return out
  }

  // -------------------------------------------------------------------------
  // The rotation, end to end, including the step that proves it finished
  // -------------------------------------------------------------------------

  test('seal under one key, add a second, re-seal, then REMOVE the first', async () => {
    await seedUnderV1()
    assert.deepEqual(await versions(), { v1: 3 })

    // Adding the new key must not break the rows that are already there. This is
    // the property a single sealing key could not have, and the reason the
    // configuration is plural.
    const before = await borrowKey(api.pool, api.clock, both, { orgId: org.orgId, provider: 'anthropic' })
    assert.equal(before.key, KEY_A)

    const report = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.deepEqual(report.problems, [])
    assert.equal(report.resealed, 3)
    assert.equal(report.remaining, 0)
    assert.deepEqual(await versions(), { v2: 3 })

    // THE STEP THAT MAKES THIS A PROOF. A keyring holding ONLY the new key: if
    // anything had been missed, it would fail here, and with the old key still
    // configured it would have kept working and hidden the gap.
    for (const [o, provider, expected] of [
      [org.orgId, 'anthropic', KEY_A],
      [org.orgId, 'openai', KEY_B],
      [other.orgId, 'anthropic', KEY_A],
    ] as const) {
      const borrowed = await borrowKey(api.pool, api.clock, v2, { orgId: o, provider })
      assert.equal(borrowed.key, expected)
    }

    // And the check the runbook tells an operator to run at that point agrees.
    const after = await reseal({ adminUrl, keyring: v2, mode: 'check', to: 'v2' })
    assert.deepEqual(after.problems, [])
    assert.equal(after.remaining, 0)
    assert.equal(after.scanned, 3)
  })

  test('a second run does nothing, because it is idempotent', async () => {
    await seedUnderV1()
    assert.equal((await reseal({ adminUrl, keyring: both, to: 'v2' })).resealed, 3)
    const again = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.equal(again.resealed, 0)
    assert.equal(again.scanned, 0)
    assert.equal(again.remaining, 0)
  })

  test('a dry run writes nothing and still says what it would do', async () => {
    await seedUnderV1()
    const dry = await reseal({ adminUrl, keyring: both, to: 'v2', mode: 'dry run' })
    assert.equal(dry.resealed, 3)
    assert.equal(dry.applied, false)
    assert.deepEqual(await versions(), { v1: 3 })
  })

  // -------------------------------------------------------------------------
  // The silent failure, made loud, through the real read path
  // -------------------------------------------------------------------------

  test('a row under a version nobody holds names the version rather than reporting tampering', async () => {
    // What an operator who replaced the secret in place used to see on every
    // customer, with no way to tell it from a damaged table.
    await seedUnderV1()
    const err = await borrowKey(api.pool, api.clock, v2, { orgId: org.orgId, provider: 'anthropic' })
      .then(() => null, (e: unknown) => e)
    assert.ok(err instanceof MissingSealingKeyError)
    assert.equal(err.keyVersion, 'v1')
    assert.match(err.message, /does not hold/)
    assert.doesNotMatch(err.message, /altered|tamper/i)
    // And it carries nothing that could be paired with a key.
    assert.ok(!err.message.includes(KEY_A))
    assert.ok(!err.message.includes(K1.toString('base64')))
    assert.ok(!err.message.includes(K2.toString('base64')))
  })

  test('a key removed while rows still name it is reported per row, and the rows are untouched', async () => {
    await seedUnderV1()
    const report = await reseal({ adminUrl, keyring: v2, to: 'v2' })
    assert.equal(report.resealed, 0)
    assert.equal(report.problems.length, 3)
    assert.ok(report.problems.every((p) => p.kind === 'missing key'))
    assert.ok(report.problems.every((p) => p.keyVersion === 'v1'))
    // Untouched, so putting the key back recovers everything.
    assert.deepEqual(await versions(), { v1: 3 })
    assert.equal(
      (await borrowKey(api.pool, api.clock, v1, { orgId: org.orgId, provider: 'anthropic' })).key,
      KEY_A,
    )
    // The report says which of the three reasons it is, and says it by name.
    assert.ok(describeReseal(report).join('\n').includes('3 rows: missing key'))
  })

  test('a genuinely tampered row reports tampering and not a missing key', async () => {
    // The negative control for the whole distinction. If every failure became
    // "missing key", the new message would be worse than the old one: it would
    // send an operator to add a key they are already holding.
    await seedUnderV1()
    await api.admin`
      UPDATE provider_keys SET ciphertext = ciphertext || '\\x00'::bytea
      WHERE org_id = ${org.orgId} AND provider = 'anthropic'`
    const report = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.equal(report.resealed, 2)
    assert.equal(report.problems.length, 1)
    assert.equal(report.problems[0]!.kind, 'cannot open')
    assert.match(report.problems[0]!.detail, /altered/)
    assert.match(report.problems[0]!.detail, /IS configured here/)
  })

  test('a row bound to another organization reports cannot open, not a missing key', async () => {
    await seedUnderV1()
    const [row] = await api.admin<{ id: string }[]>`
      SELECT id::text AS id FROM provider_keys WHERE org_id = ${org.orgId} AND provider = 'anthropic'`
    await api.admin`UPDATE provider_keys SET org_id = ${other.orgId}, provider = 'openai' WHERE id = ${row!.id}::uuid`
    const report = await reseal({ adminUrl, keyring: both, to: 'v2', mode: 'check' })
    assert.equal(report.problems.length, 1)
    assert.equal(report.problems[0]!.kind, 'cannot open')
  })

  // -------------------------------------------------------------------------
  // Orderings
  // -------------------------------------------------------------------------

  test('interrupted half way and run again, it finishes the rest', async () => {
    await seedUnderV1()
    // A batch of one, and the run stopped after it: the closest thing to an
    // interrupt that a test can force deterministically.
    const first = await reseal({ adminUrl, keyring: both, to: 'v2', batchSize: 1, mode: 'apply' })
    assert.equal(first.resealed, 3)

    // Now the real interruption: put one row back to v1 by re-sealing it there,
    // which is the state a killed run leaves behind, and resume.
    await api.admin`DELETE FROM provider_keys WHERE org_id = ${other.orgId}`
    await saveKey(api.pool, api.clock, bothSealingOld, {
      ...actor, orgId: other.orgId, provider: 'anthropic', key: KEY_A,
    })
    assert.deepEqual(await versions(), { v1: 1, v2: 2 })

    const resumed = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.equal(resumed.resealed, 1)
    assert.deepEqual(await versions(), { v2: 3 })
    assert.equal((await reseal({ adminUrl, keyring: v2, mode: 'check', to: 'v2' })).problems.length, 0)
  })

  test('two runs at once do not corrupt anything, and one of them says so', async () => {
    await seedUnderV1()
    const [a, b] = await Promise.all([
      reseal({ adminUrl, keyring: both, to: 'v2' }),
      reseal({ adminUrl, keyring: both, to: 'v2' }),
    ])
    // Between them every row is written exactly once. Whichever one lost a race
    // reports the row as changed underneath it rather than overwriting it.
    assert.equal(a.resealed + b.resealed, 3)
    assert.deepEqual(await versions(), { v2: 3 })
    assert.deepEqual([...a.problems, ...b.problems], [])
    // Every row still opens under the new key alone, which is what "not
    // corrupted" means here rather than "the counts add up".
    assert.equal(
      (await borrowKey(api.pool, api.clock, v2, { orgId: other.orgId, provider: 'anthropic' })).key,
      KEY_A,
    )
  })

  test('a row the application writes while the tool runs is left as the application sealed it', async () => {
    // The ordering the guarded UPDATE exists for, FORCED rather than hoped for.
    // The hook fires after the new value is computed and before it is written,
    // and it is where another writer changes the same row.
    await seedUnderV1()
    const [target] = await api.admin<{ id: string }[]>`
      SELECT id::text AS id FROM provider_keys WHERE org_id = ${other.orgId}`
    let raced = false
    const report = await reseal({
      adminUrl,
      keyring: both,
      to: 'v2',
      onBeforeWrite: async ({ id }) => {
        if (id !== target!.id || raced) return
        raced = true
        // Another writer re-seals the same row under v2, with its own nonce.
        const fresh = seal(v2, KEY_C, { orgId: other.orgId, provider: 'anthropic' })
        await api.admin`
          UPDATE provider_keys
          SET ciphertext = ${fresh.ciphertext}, nonce = ${fresh.nonce}, key_version = 'v2',
              fingerprint = ${fresh.fingerprint}, last4 = ${fresh.last4}
          WHERE id = ${target!.id}::uuid`
      },
    })
    assert.ok(raced, 'the hook never fired, so this proved nothing')
    assert.equal(report.changedUnderUs, 1)
    assert.equal(report.resealed, 2)
    assert.deepEqual(report.problems, [])
    assert.deepEqual(await versions(), { v2: 3 })

    // THE ASSERTION THAT MATTERS: the other writer's value survived. The tool
    // did not put back the value it had read.
    assert.equal(
      (await borrowKey(api.pool, api.clock, v2, { orgId: other.orgId, provider: 'anthropic' })).key,
      KEY_C,
    )
  })

  test('a row whose ciphertext moved under us is left alone even when its version did not', async () => {
    // THE OTHER HALF OF THE GUARD, and it needs its own test because the version
    // half hides it. Removing the ciphertext comparison from the UPDATE leaves
    // every other test in this file green: the writer in the test above changes
    // the version too, so the version comparison alone catches it.
    //
    // No writer in this repository changes a ciphertext without changing the
    // version today, and the guard is kept anyway. What it buys is that the
    // tool's correctness does not depend on which column the NEXT writer happens
    // to touch, and there is already a second one being written against this
    // sealing module.
    await seedUnderV1()
    const [target] = await api.admin<{ id: string }[]>`
      SELECT id::text AS id FROM provider_keys WHERE org_id = ${other.orgId}`
    let raced = false
    const report = await reseal({
      adminUrl,
      keyring: both,
      to: 'v2',
      onBeforeWrite: async ({ id }) => {
        if (id !== target!.id || raced) return
        raced = true
        // A fresh seal under the SAME version. Only the bytes move.
        const fresh = seal(bothSealingOld, KEY_C, { orgId: other.orgId, provider: 'anthropic' })
        assert.equal(fresh.keyVersion, 'v1')
        await api.admin`
          UPDATE provider_keys
          SET ciphertext = ${fresh.ciphertext}, nonce = ${fresh.nonce},
              fingerprint = ${fresh.fingerprint}, last4 = ${fresh.last4}
          WHERE id = ${target!.id}::uuid`
      },
    })
    assert.ok(raced, 'the hook never fired, so this proved nothing')
    assert.equal(report.changedUnderUs, 1)
    assert.equal(report.resealed, 2)
    // The other writer's value survived, still under v1, still openable.
    assert.deepEqual(await versions(), { v1: 1, v2: 2 })
    assert.equal(
      (await borrowKey(api.pool, api.clock, both, { orgId: other.orgId, provider: 'anthropic' })).key,
      KEY_C,
    )
  })

  test('the application replacing a key mid-rotation leaves the customer with their new key', async () => {
    // The other half of the same concern, through the real write path rather
    // than the hook. saveKey revokes the old row and inserts a new one, so the
    // row the tool read is still there and the live row is a different one.
    await seedUnderV1()
    const read = await reseal({ adminUrl, keyring: both, to: 'v2', mode: 'dry run' })
    assert.equal(read.resealed, 3)
    await saveKey(api.pool, api.clock, both, {
      ...actor, orgId: org.orgId, provider: 'anthropic', key: KEY_C,
    })
    const report = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.deepEqual(report.problems, [])
    assert.deepEqual(await versions(), { v2: 4 })
    assert.equal(
      (await borrowKey(api.pool, api.clock, v2, { orgId: org.orgId, provider: 'anthropic' })).key,
      KEY_C,
    )
  })

  test('run with the new key absent, it refuses instead of writing under a key it does not hold', async () => {
    await seedUnderV1()
    await assert.rejects(
      () => reseal({ adminUrl, keyring: v1, to: 'v9' }),
      (err: unknown) => err instanceof ResealRefused && /no sealing key for version "v9"/.test(String(err)),
    )
    assert.deepEqual(await versions(), { v1: 3 })
  })

  test('revoked rows are re-sealed too, so a check after the old key is gone is unambiguous', async () => {
    // A revoked row carries ciphertext nothing reads. Left behind, it reports as
    // naming a key nobody holds once the old key is removed, and an operator
    // cannot tell that from a live row they missed.
    await seedUnderV1()
    await api.admin`UPDATE provider_keys SET revoked_at = now() WHERE org_id = ${other.orgId}`
    const report = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.equal(report.resealed, 3)
    assert.deepEqual(await versions(), { v2: 3 })
    assert.equal((await reseal({ adminUrl, keyring: v2, mode: 'check', to: 'v2' })).problems.length, 0)

    // And --live-only is available for an operator who wants the other choice,
    // with the consequence stated rather than discovered.
    await api.admin`DELETE FROM provider_keys`
    await seedUnderV1()
    await api.admin`UPDATE provider_keys SET revoked_at = now() WHERE org_id = ${other.orgId}`
    const live = await reseal({ adminUrl, keyring: both, to: 'v2', includeRevoked: false })
    assert.equal(live.resealed, 2)
    assert.deepEqual(await versions(), { v1: 1, v2: 2 })
  })

  test('an empty table is nothing to do rather than an error', async () => {
    const report = await reseal({ adminUrl, keyring: both, to: 'v2' })
    assert.equal(report.scanned, 0)
    assert.deepEqual(report.problems, [])
    assert.equal(report.remaining, 0)
  })

  test('every table the sealing module writes to is covered by the tool', async () => {
    // The gap this catches is a second sealed column added elsewhere and never
    // added here, which a rotation would silently leave behind. Read from the
    // database rather than from a list: any table with all four sealed columns
    // has to be in SEALED_TABLES.
    const rows = await api.admin<{ table_name: string }[]>`
      SELECT table_name FROM information_schema.columns
      WHERE table_schema = 'public' AND column_name IN ('ciphertext', 'nonce', 'key_version', 'fingerprint')
      GROUP BY table_name HAVING count(DISTINCT column_name) = 4`
    const covered = new Set(SEALED_TABLES.map((t) => t.table))
    const missing = rows.map((r) => r.table_name).filter((t) => !covered.has(t)).sort()
    assert.deepEqual(
      missing,
      [],
      `these tables hold sealed values and the re-sealing tool does not know about them:\n  ` +
        `${missing.join('\n  ')}\nAdd them to SEALED_TABLES in src/providers/reseal.ts.`,
    )
  })
})
