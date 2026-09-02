// The operator audit chain, and the four ways an audit log stops being one.
//
//   1. Nothing calls the writer. The function exists, the table exists, the
//      review passes, and the log is empty. This suite cannot catch that on its
//      own, so the API-side suite greps for call sites; what this proves is
//      that the writer works when called, which is the other half.
//   2. The entry survives a rolled-back action, so the log records things that
//      did not happen.
//   3. The action commits without its entry, so the log misses things that did.
//   4. History can be rewritten, so the log records whatever the last writer
//      wanted it to.
//
// The third is the one the impersonation requirement turns on: "the audit
// record is written BEFORE the session exists" is only enforceable if the two
// share a transaction, so both tests below are about ordering inside one.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { appendAdminAudit, verifyAdminAuditChain } from '../src/admin-audit.ts'
import { available, setup, seedTenant, type Harness, type Fixture } from './harness.ts'

const hasDb = await available()

describe('the operator audit chain', { skip: hasDb ? false : 'no database' }, () => {
  let h: Harness
  let org: Fixture
  let operatorId: string
  let operatorToken: Buffer

  before(async () => {
    h = await setup()
    org = await seedTenant(h.admin, 'audited')
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`auditor-${randomUUID().slice(0, 8)}@example.test`}, 'Auditor', 'super_admin')
      RETURNING id`
    operatorId = row!.id
    const token = randomBytes(32)
    operatorToken = createHash('sha256').update(token).digest()
    await h.admin`
      INSERT INTO admin_sessions (token_hash, admin_user_id, expires_at)
      VALUES (${operatorToken}, ${operatorId}, ${new Date(Date.now() + 3_600_000).toISOString()})`
  })

  after(async () => {
    await h?.close()
  })

  /** Appends through the operator scope, which is the path production takes.
   *  Driving it through the owner connection instead would prove the chain
   *  works while proving nothing about whether the policies let it run. */
  async function append(input: Parameters<typeof appendAdminAudit>[1]) {
    return h.pool.withPlatformAdmin(operatorToken, (db) => appendAdminAudit(db, input))
  }

  test('an installation-wide action names no tenant', async () => {
    // The reason this table exists separately. audit_entries.org_id is NOT NULL
    // and its chain is per organization, so an event that concerns the whole
    // installation has nothing to key to and would have had to be faked as one
    // row per affected tenant, implying a blast radius it did not have.
    const { seq } = await append({
      adminUserId: operatorId,
      actorLabel: 'Auditor',
      action: 'maintenance.enabled',
      targetType: 'installation',
      origin: 'admin',
      severity: 'critical',
      detail: { reason: 'database failover' },
    })
    const [row] = await h.admin<{ subject_org_id: string | null; severity: string }[]>`
      SELECT subject_org_id, severity FROM admin_audit_entries WHERE seq = ${seq}`
    assert.equal(row!.subject_org_id, null, 'an installation-wide entry was forced to name a tenant')
    assert.equal(row!.severity, 'critical')
  })

  test('an action against one tenant records which', async () => {
    const { seq } = await append({
      adminUserId: operatorId,
      actorLabel: 'Auditor',
      action: 'organization.suspended',
      targetType: 'organization',
      targetId: org.orgId,
      subjectOrgId: org.orgId,
      subjectOrgLabel: org.slug,
      origin: 'admin',
      severity: 'high',
      detail: { reason: 'abuse report' },
    })
    const [row] = await h.admin<{ subject_org_id: string; subject_org_label: string }[]>`
      SELECT subject_org_id, subject_org_label FROM admin_audit_entries WHERE seq = ${seq}`
    assert.equal(row!.subject_org_id, org.orgId)
    assert.equal(row!.subject_org_label, org.slug)
  })

  test('a failed sign-in is recorded even though nobody was identified', async () => {
    // The line most worth having, and the one a policy requiring a live
    // operator would silently drop: a failed sign-in happens, by definition, on
    // a connection that never got a session.
    const { seq } = await append({
      adminUserId: null,
      actorLabel: 'someone@example.test',
      action: 'admin.signin_failed',
      targetType: 'admin_user',
      origin: 'admin',
      severity: 'notice',
      detail: { reason: 'wrong password' },
    })
    const [row] = await h.admin<{ admin_user_id: string | null; actor_label: string }[]>`
      SELECT admin_user_id, actor_label FROM admin_audit_entries WHERE seq = ${seq}`
    assert.equal(row!.admin_user_id, null)
    assert.equal(row!.actor_label, 'someone@example.test')
  })

  test('the chain verifies, and detects an alteration', async () => {
    const before = await h.pool.withPlatformAdmin(operatorToken, verifyAdminAuditChain)
    assert.equal(before.ok, true, `chain broken before tampering: ${JSON.stringify(before.problems)}`)
    assert.ok(before.entries >= 3)

    // Tamper through the OWNER connection, because the application role cannot:
    // UPDATE is the privilege withheld to make the table append only. That the
    // owner can is the whole reason a hash chain exists on top of the grant.
    const [victim] = await h.admin<{ seq: string }[]>`
      SELECT seq FROM admin_audit_entries ORDER BY seq ASC LIMIT 1`
    await h.admin`
      UPDATE admin_audit_entries SET action = 'something_else' WHERE seq = ${victim!.seq}`

    const after = await h.pool.withPlatformAdmin(operatorToken, verifyAdminAuditChain)
    assert.equal(after.ok, false, 'a rewritten entry verified clean')
    assert.ok(
      after.problems.some((p) => p.kind === 'altered' && p.seq === Number(victim!.seq)),
      `the alteration was not reported: ${JSON.stringify(after.problems)}`,
    )

    // Put it back so the ordering tests below start from a sound chain.
    await h.admin`
      UPDATE admin_audit_entries SET action = 'maintenance.enabled' WHERE seq = ${victim!.seq}`
  })

  describe('the entry and the action commit together, or neither does', () => {
    test('a rolled-back action takes its audit entry with it', async () => {
      // Failure 2. An entry written in its own transaction survives a rolled
      // back action, and a log that records things that did not happen is as
      // useless as one that misses things that did.
      const marker = `rollback-${randomUUID().slice(0, 8)}`
      const [countBefore] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries`

      await assert.rejects(() =>
        h.pool.withPlatformAdmin(operatorToken, async (db) => {
          await appendAdminAudit(db, {
            adminUserId: operatorId,
            actorLabel: 'Auditor',
            action: 'organization.suspended',
            targetType: 'organization',
            targetId: marker,
            origin: 'admin',
            severity: 'high',
          })
          // The action fails after the entry is written.
          throw new Error('the action failed')
        }),
      )

      const [countAfter] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries`
      assert.equal(
        countAfter!.n,
        countBefore!.n,
        'an audit entry survived the rollback of the action it describes',
      )
    })

    test('the entry is written BEFORE the action, so the action cannot commit without it', async () => {
      // Failure 3, and the property impersonation turns on. Writing the entry
      // first inside one transaction is stronger than writing it first in its
      // own: the action cannot commit without the record, AND the record cannot
      // outlive a failed action.
      //
      // Proven by ordering: the entry's seq is read back and used by the action
      // itself, so an implementation that wrote the entry afterwards could not
      // produce this row at all.
      const [org2] = await h.admin<{ id: string; slug: string }[]>`
        INSERT INTO organizations (slug, name)
        VALUES (${`ordered-${randomUUID().slice(0, 8)}`}, 'Ordered') RETURNING id, slug`

      const seq = await h.pool.withPlatformAdmin(operatorToken, async (db) => {
        const entry = await appendAdminAudit(db, {
          adminUserId: operatorId,
          actorLabel: 'Auditor',
          action: 'organization.suspended',
          targetType: 'organization',
          targetId: org2!.id,
          subjectOrgId: org2!.id,
          subjectOrgLabel: org2!.slug,
          origin: 'admin',
          severity: 'high',
          detail: { reason: 'ordering proof' },
        })
        await db.execute(sql`
          UPDATE organizations
          SET suspended_at = now(), suspended_reason = ${'audit entry ' + entry.seq}
          WHERE id = ${org2!.id}::uuid`)
        return entry.seq
      })

      const [row] = await h.admin<{ suspended_reason: string }[]>`
        SELECT suspended_reason FROM organizations WHERE id = ${org2!.id}::uuid`
      assert.equal(
        row!.suspended_reason,
        `audit entry ${seq}`,
        'the action did not carry the entry that preceded it',
      )

      const [entry] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE seq = ${seq}`
      assert.equal(entry!.n, '1', 'the action committed without its audit entry')
    })
  })

  test('severity is constrained, so an append cannot invent one', async () => {
    await assert.rejects(
      () => h.admin`
        INSERT INTO admin_audit_entries (actor_label, action, target_type, origin, severity, entry_hash)
        VALUES ('x', 'y', 'z', 'admin', 'catastrophic', 'deadbeef')`,
      /admin_audit_severity_known|violates check constraint/,
    )
  })
})
