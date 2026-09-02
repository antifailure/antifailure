// The audit log: append-only at the database, and tamper-evident above it.
//
// Both halves are tested against the real database, because both are claims
// about what Postgres will refuse rather than about what the code does.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { sql } from 'drizzle-orm'
import { appendAudit, verifyAuditChain, auditEntryHash, canonicalJson } from '../src/audit.ts'
import { available, setup, seedTenant, dropTenant, pgError, type Harness, type Fixture } from './harness.ts'

const hasDatabase = await available()

describe('audit log', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let org: Fixture
  let other: Fixture

  before(async () => {
    h = await setup()
    org = await seedTenant(h.admin, 'audited')
    other = await seedTenant(h.admin, 'neighbour')
    // The seed writes one entry with a placeholder hash so that the tenancy
    // suite has a row to look for. It is not part of any chain, so this suite
    // clears both tenants and builds the chains it verifies.
    await h.admin`DELETE FROM audit_entries WHERE org_id IN (${org.orgId}, ${other.orgId})`
  })

  after(async () => {
    await dropTenant(h.admin, org.orgId)
    await dropTenant(h.admin, other.orgId)
    await h.close()
  })

  it('the application role is granted insert and select and nothing else', async () => {
    const rows = await h.admin<{ privilege_type: string }[]>`
      SELECT privilege_type FROM information_schema.table_privileges
      WHERE table_name = 'audit_entries' AND grantee = 'antifailure_app'
      ORDER BY privilege_type`
    assert.deepEqual(
      rows.map((r) => r.privilege_type).sort(),
      ['INSERT', 'SELECT'],
      'the application has a grant on the audit log beyond insert and select',
    )
  })

  it('the application role cannot update or delete an audit entry', async () => {
    await h.pool.withTenant({ orgId: org.orgId }, (db) =>
      appendAudit(db, {
        orgId: org.orgId,
        actorLabel: 'ada',
        action: 'environment.created',
        targetType: 'environment',
        origin: 'web',
      }),
    )

    for (const statement of [
      sql`UPDATE audit_entries SET action = 'nothing.happened' WHERE org_id = ${org.orgId}`,
      sql`DELETE FROM audit_entries WHERE org_id = ${org.orgId}`,
    ]) {
      const err = await h.pool
        .withTenant({ orgId: org.orgId }, async (db) => {
          await db.execute(statement)
        })
        .then(
          () => null,
          (e: unknown) => pgError(e),
        )
      assert.ok(err, 'the application rewrote its own audit log')
      assert.equal(err.code, '42501', `expected insufficient privilege, got ${err.code}: ${err.message}`)
    }

    const [row] = await h.admin<{ action: string }[]>`
      SELECT action FROM audit_entries WHERE org_id = ${org.orgId} ORDER BY seq LIMIT 1`
    assert.equal(row?.action, 'environment.created', 'the entry changed anyway')
  })

  it('a chain of entries verifies', async () => {
    await h.admin`DELETE FROM audit_entries WHERE org_id = ${org.orgId}`
    for (let i = 0; i < 25; i += 1) {
      await h.pool.withTenant({ orgId: org.orgId }, (db) =>
        appendAudit(db, {
          orgId: org.orgId,
          actorLabel: `user-${i % 3}`,
          action: i % 2 === 0 ? 'environment.created' : 'policy.updated',
          targetType: 'environment',
          targetId: `env-${i}`,
          origin: 'api',
          detail: { index: i, note: 'created from a pull request' },
        }),
      )
    }

    const report = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
      verifyAuditChain(db, org.orgId),
    )
    assert.equal(report.entries, 25)
    assert.deepEqual(report.problems, [])
    assert.equal(report.ok, true)
    assert.ok(report.head, 'a verified chain reports its head hash')
  })

  it('altering any field of any entry is detected', async () => {
    // Every entry, one at a time, so that the test proves detection rather
    // than proving that one convenient entry happens to be checked.
    const seqs = await h.admin<{ seq: string }[]>`
      SELECT seq FROM audit_entries WHERE org_id = ${org.orgId} ORDER BY seq`
    assert.ok(seqs.length >= 25)

    for (const { seq } of seqs) {
      const [before] = await h.admin<{ action: string }[]>`
        SELECT action FROM audit_entries WHERE seq = ${seq}`
      await h.admin`UPDATE audit_entries SET action = 'nothing.happened' WHERE seq = ${seq}`

      const report = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
        verifyAuditChain(db, org.orgId),
      )
      assert.equal(report.ok, false, `changing entry ${seq} was not detected`)
      assert.ok(
        report.problems.some((p) => p.seq === Number(seq) && p.kind === 'altered'),
        `entry ${seq} was changed but is not reported as altered`,
      )

      await h.admin`UPDATE audit_entries SET action = ${before!.action} WHERE seq = ${seq}`
    }

    const restored = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
      verifyAuditChain(db, org.orgId),
    )
    assert.equal(restored.ok, true, 'the chain did not verify again after the fields were restored')
  })

  it('removing an entry from the middle leaves a break that is reported', async () => {
    const rows = await h.admin<{ seq: string }[]>`
      SELECT seq FROM audit_entries WHERE org_id = ${org.orgId} ORDER BY seq`
    const victim = rows[Math.floor(rows.length / 2)]!
    const [saved] = await h.admin<Record<string, unknown>[]>`
      SELECT * FROM audit_entries WHERE seq = ${victim.seq}`

    await h.admin`DELETE FROM audit_entries WHERE seq = ${victim.seq}`

    const report = await h.pool.withTenant({ orgId: org.orgId }, (db) =>
      verifyAuditChain(db, org.orgId),
    )
    assert.equal(report.ok, false, 'a deleted entry left no trace')
    assert.ok(
      report.problems.some((p) => p.kind === 'broken_link'),
      'a deleted entry was not reported as a break in the chain',
    )

    await h.admin`
      INSERT INTO audit_entries
        (seq, org_id, actor_user_id, actor_label, action, target_type, target_id,
         origin, detail, occurred_at, prev_hash, entry_hash)
      VALUES (
        ${saved!.seq as string}, ${saved!.org_id as string}, ${saved!.actor_user_id as string | null},
        ${saved!.actor_label as string}, ${saved!.action as string}, ${saved!.target_type as string},
        ${saved!.target_id as string | null}, ${saved!.origin as string},
        ${h.admin.json(saved!.detail as never)}, ${saved!.occurred_at as Date},
        ${saved!.prev_hash as string | null}, ${saved!.entry_hash as string})`
  })

  it('one tenant cannot read another tenant’s audit entries', async () => {
    await h.pool.withTenant({ orgId: other.orgId }, (db) =>
      appendAudit(db, {
        orgId: other.orgId,
        actorLabel: 'neighbour',
        action: 'environment.created',
        targetType: 'environment',
        origin: 'web',
      }),
    )
    const report = await h.pool.withTenant({ orgId: other.orgId }, (db) =>
      verifyAuditChain(db, org.orgId),
    )
    // Not an error, an empty chain: the rows are invisible, so there is
    // nothing to verify and nothing leaks about how many entries exist.
    assert.equal(report.entries, 0)
  })

  it('each organization chains independently, so a busy tenant does not order a quiet one', async () => {
    const a = await h.pool.withTenant({ orgId: org.orgId }, (db) => verifyAuditChain(db, org.orgId))
    const b = await h.pool.withTenant({ orgId: other.orgId }, (db) =>
      verifyAuditChain(db, other.orgId),
    )
    assert.equal(a.ok, true)
    assert.equal(b.ok, true)
    assert.notEqual(a.head, b.head)
  })

  it('concurrent appends produce one chain rather than two forks', async () => {
    // The reason appendAudit takes a lock. Without it, two transactions read
    // the same head and both write an entry claiming it as their predecessor,
    // and the chain verifies as broken forever after.
    await h.admin`DELETE FROM audit_entries WHERE org_id = ${other.orgId}`
    await Promise.all(
      Array.from({ length: 12 }, (_, i) =>
        h.pool.withTenant({ orgId: other.orgId }, (db) =>
          appendAudit(db, {
            orgId: other.orgId,
            actorLabel: `writer-${i}`,
            action: 'environment.created',
            targetType: 'environment',
            targetId: `env-${i}`,
            origin: 'api',
          }),
        ),
      ),
    )
    const report = await h.pool.withTenant({ orgId: other.orgId }, (db) =>
      verifyAuditChain(db, other.orgId),
    )
    assert.equal(report.entries, 12)
    assert.deepEqual(report.problems, [], 'concurrent appends forked the chain')
  })
})

describe(
  'what deleting a person would do to the log',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    /**
     * The evidence under a published claim, kept where it will be re-run.
     *
     * The retention page says the account row is kept BY CHOICE rather than
     * because the database refuses, and gives the reason. The claim it replaced
     * said the database refuses the delete, and that came from a misread of
     * `pg_constraint.confdeltype`: `n` is SET NULL and `a` is NO ACTION. A
     * legal sentence resting on a one-letter misreading is worth a test.
     *
     * Both halves are asserted, because either one alone supports the wrong
     * conclusion. The delete SUCCEEDS, so "the database refuses" is false. And
     * the chain then fails its own verification, so declining to delete is the
     * stronger choice rather than the lazier one.
     */
    let h: Harness
    let tenant: Fixture

    before(async () => {
      h = await setup()
      tenant = await seedTenant(h.admin, 'deletion-evidence')
    })

    after(async () => {
      await dropTenant(h.admin, tenant.orgId)
      await h.close()
    })

    it('succeeds, and leaves the audit log reporting itself as altered', async () => {
      // seedTenant writes one entry with a literal string where the hash goes,
      // because its job is to give the cross-tenant suite a row to attack
      // rather than a valid chain. That entry alone makes verification fail, so
      // the "before" assertion below would fail for a reason that has nothing
      // to do with deleting anybody. Caught by that assertion rather than by
      // reading the fixture, which is what it is there for.
      await h.admin`DELETE FROM audit_entries WHERE org_id = ${tenant.orgId}`

      await h.pool.withTenant({ orgId: tenant.orgId, userId: tenant.userId }, async (db) => {
        for (const action of ['environment.created', 'masking.rule_proposed', 'token.created']) {
          await appendAudit(db, {
            orgId: tenant.orgId,
            actorUserId: tenant.userId,
            actorLabel: 'somebody who asked to be removed',
            action,
            targetType: 'environment',
            targetId: 'e1',
            origin: 'web',
            detail: {},
            occurredAt: new Date(),
          })
        }
      })

      const before = await h.pool.withTenant({ orgId: tenant.orgId }, (db) =>
        verifyAuditChain(db, tenant.orgId),
      )
      assert.equal(before.ok, true, 'the chain was already broken, so this proves nothing')

      // Through the owner connection, which is what a person with database
      // access carrying out a removal request by hand actually holds.
      await h.admin`DELETE FROM users WHERE id = ${tenant.userId}`
      const left = await h.admin<{ n: number }[]>`
        SELECT count(*)::int AS n FROM users WHERE id = ${tenant.userId}`
      assert.equal(
        left[0]!.n,
        0,
        'the delete was refused, so the page may say the database refuses it after all',
      )

      const nulled = await h.admin<{ n: number }[]>`
        SELECT count(*)::int AS n FROM audit_entries
        WHERE org_id = ${tenant.orgId} AND actor_user_id IS NULL`
      assert.ok(nulled[0]!.n >= 3, 'the delete did not null the actor on the entries')

      const after = await h.pool.withTenant({ orgId: tenant.orgId }, (db) =>
        verifyAuditChain(db, tenant.orgId),
      )
      assert.equal(
        after.ok,
        false,
        'deleting the person left the chain verifying, so the reason the page gives for ' +
          'keeping the row is not the real one',
      )
      assert.ok(
        after.problems.some((p) => p.kind === 'altered'),
        'the chain broke for some reason other than the hashed actor column',
      )
    })
  },
)

describe('the canonical form an entry is hashed in', () => {
  it('does not depend on the order keys were written in', () => {
    assert.equal(
      canonicalJson({ b: 1, a: { d: 2, c: 3 } }),
      canonicalJson({ a: { c: 3, d: 2 }, b: 1 }),
    )
  })

  it('cannot be collided by moving a character between adjacent fields', () => {
    // Without length prefixes these two entries concatenate to the same bytes,
    // and an attacker who chooses one field chooses the hash.
    const base = {
      seq: 1,
      orgId: '00000000-0000-0000-0000-000000000000',
      actorLabel: 'ab',
      action: '.c',
      targetType: 't',
      targetId: null,
      origin: 'web',
      detail: {},
      occurredAt: new Date('2026-01-01T00:00:00Z'),
      prevHash: null,
    }
    const shifted = { ...base, actorLabel: 'a', action: 'b.c' }
    assert.notEqual(auditEntryHash(base), auditEntryHash(shifted))
  })

  it('changes when any single field changes', () => {
    const base = {
      seq: 7,
      orgId: '11111111-1111-1111-1111-111111111111',
      actorUserId: '22222222-2222-2222-2222-222222222222',
      actorLabel: 'ada',
      action: 'policy.updated',
      targetType: 'repository',
      targetId: 'acme/app',
      origin: 'web',
      detail: { host: 'api.stripe.com' },
      occurredAt: new Date('2026-01-01T00:00:00Z'),
      prevHash: 'abc',
    }
    const original = auditEntryHash(base)
    const variants = [
      { ...base, seq: 8 },
      { ...base, orgId: '33333333-3333-3333-3333-333333333333' },
      { ...base, actorUserId: null },
      { ...base, actorLabel: 'grace' },
      { ...base, action: 'policy.deleted' },
      { ...base, targetType: 'environment' },
      { ...base, targetId: 'acme/other' },
      { ...base, origin: 'api' },
      { ...base, detail: { host: 'api.resend.com' } },
      { ...base, occurredAt: new Date('2026-01-01T00:00:01Z') },
      { ...base, prevHash: 'abd' },
    ]
    for (const v of variants) {
      assert.notEqual(auditEntryHash(v), original)
    }
  })
})
