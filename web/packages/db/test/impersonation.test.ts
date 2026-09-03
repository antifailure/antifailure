// The constraint that makes the impersonation rules true rather than intended.
//
// Impersonation is the most dangerous thing the admin portal does, and the
// rules around it are the sort that are easy to write in a handler and easy to
// lose in the next refactor: capture a reason, and write the audit record
// before the session exists. A handler enforcing those is one caller. A CHECK
// enforcing them is every caller, including a psql prompt during an incident,
// which is exactly when somebody is most likely to reach around the product.
//
// The audit sequence number is the part worth explaining. The requirement is
// that an impersonated session can never exist unaudited, and the ordinary way
// to attempt that is to write the entry first and then the session, and trust
// that nobody ever reorders two statements. Requiring the session row to carry
// the sequence number of an entry that has already been written makes the
// unaudited session unrepresentable instead, which does not depend on anybody
// remembering anything.
//
// The last test here is the one that would be missing if this file were
// written carelessly. Three tests proving a constraint refuses things pass just
// as well when the constraint refuses EVERYTHING, and a constraint that
// rejected ordinary sessions would take the whole product down while looking
// like rigour.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { available, setup, seedTenant, dropTenant, pgError, type Harness, type Fixture } from './harness.ts'

const hasDatabase = await available()

describe('an impersonated session cannot be incomplete', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: Harness
  let tenant: Fixture
  /** The operator doing the impersonating. An `admin_users` row, not a `users`
   *  one: 0032 repointed the foreign key at the table operators actually live
   *  in, because they are a separate id space and the column could not hold one
   *  before. See the note on `operator` below. */
  let operator: string
  /** A real entry in the operator chain for the session rows to point at. 0032
   *  added the foreign key that makes "the record came first" structural on
   *  this table too, so a made-up number is no longer insertable. */
  let auditSeq: number

  before(async () => {
    h = await setup()
    tenant = await seedTenant(h.admin, 'impersonation')
    const [op] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`impersonation-${randomBytes(4).toString('hex')}@example.test`}, 'An operator', 'support')
      RETURNING id`
    operator = op!.id
    const [entry] = await h.admin<{ seq: string }[]>`
      INSERT INTO admin_audit_entries
        (admin_user_id, actor_label, action, target_type, origin, severity, entry_hash, prev_hash)
      VALUES (${operator}, 'an operator', 'impersonation.started', 'user', 'admin', 'critical',
              ${randomBytes(32).toString('hex')}, NULL)
      RETURNING seq`
    auditSeq = Number(entry!.seq)
  })

  after(async () => {
    await h.admin`DELETE FROM sessions WHERE user_id = ${tenant.userId}`
    await h.admin`DELETE FROM admin_audit_entries WHERE admin_user_id = ${operator}`
    await h.admin`DELETE FROM admin_users WHERE id = ${operator}`
    await dropTenant(h.admin, tenant.orgId)
    // Closed, or the pool's connections keep the process alive after the last
    // assertion and the file times out having passed every test in it.
    await h.close()
  })

  /** Inserts a session directly, as the owner, so that what is being tested is
   *  the constraint rather than any policy or any code path above it. */
  async function insert(columns: Record<string, unknown>): Promise<{ code?: string; message: string } | null> {
    const row = {
      token_hash: randomBytes(32),
      user_id: tenant.userId,
      expires_at: new Date(Date.now() + 3_600_000),
      ...columns,
    }
    return h
      .admin`INSERT INTO sessions ${h.admin(row)}`
      .then(() => null, (e: unknown) => pgError(e))
  }

  const CONSTRAINT = 'sessions_impersonation_is_complete'

  it('refuses an impersonation with no reason captured', async () => {
    const err = await insert({
      impersonated_by: operator,
      impersonator_label: 'an operator',
      impersonation_audit_seq: auditSeq,
    })
    assert.ok(err, 'an impersonation with no reason was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  it('refuses a reason that is only whitespace, which is not a reason', async () => {
    const err = await insert({
      impersonated_by: operator,
      impersonator_label: 'an operator',
      impersonation_reason: '   ',
      impersonation_audit_seq: auditSeq,
    })
    assert.ok(err, 'a blank reason was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  /**
   * The rule that "the audit record is written before the session exists".
   *
   * Expressed as a column the row cannot omit rather than as an ordering
   * between two statements, because an ordering is a convention and a NOT NULL
   * half of a CHECK is a fact.
   */
  it('refuses an impersonation that names no audit entry', async () => {
    const err = await insert({
      impersonated_by: operator,
      impersonator_label: 'an operator',
      impersonation_reason: 'looking into a failed run',
    })
    assert.ok(err, 'an unaudited impersonation was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  it('refuses an audit entry that was never written', async () => {
    // The other half of the rule, and it did not exist until 0032. The CHECK
    // says the column must be SET; the foreign key says the entry it names must
    // EXIST. Without the second, a handler could satisfy the constraint with any
    // integer and the session would carry a pointer into nothing, which reads as
    // an audited impersonation to everything that ever looks at it.
    const err = await insert({
      impersonated_by: operator,
      impersonator_label: 'an operator',
      impersonation_reason: 'looking into a failed run',
      impersonation_audit_seq: 2_000_000_000,
    })
    assert.ok(err, 'a session pointing at an audit entry that does not exist was accepted')
    assert.match(err.message, /sessions_impersonation_audit_fkey/)
  })

  it('refuses a marker with no operator named', async () => {
    // Keyed on the LABEL now, not on the id. 0032 moved the constraint's
    // predicate onto impersonation_audit_seq and let impersonated_by go null,
    // because the foreign key is ON DELETE SET NULL and the two could not both
    // hold: nulling one of four columns leaves exactly the shape the old
    // all-or-nothing CHECK refused, so deleting an operator failed on a table
    // nobody was looking at. What still cannot happen is a session that records
    // an impersonation without saying who did it, because impersonator_label is
    // text and no cascade can take it away.
    const err = await insert({
      impersonation_reason: 'looking into a failed run',
      impersonation_audit_seq: auditSeq,
    })
    assert.ok(err, 'a session claiming a reason but naming no operator was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  it('accepts a complete impersonation', async () => {
    const err = await insert({
      impersonated_by: operator,
      impersonator_label: 'an operator',
      impersonation_reason: 'looking into a failed run',
      impersonation_audit_seq: auditSeq,
    })
    assert.equal(err, null, `a complete impersonation was refused: ${err?.message}`)
  })

  it("survives its operator's account being deleted, with the record intact", async () => {
    // THE PROPERTY 0032 EXISTS FOR, and the one the old shape could not have.
    //
    // Under the previous constraint this DELETE failed: the foreign key nulls
    // impersonated_by, the all-or-nothing CHECK refuses a row with three of four
    // columns set, and the delete came back with a constraint violation on a
    // table nobody was thinking about. Now the id goes and the record stays,
    // which is the same trade `suspended_by text` makes three tables over: the
    // label names the person a year later, and the audit entry is immutable.
    const [doomed] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role)
      VALUES (${`departing-${randomBytes(4).toString('hex')}@example.test`}, 'A leaver', 'support')
      RETURNING id`
    const err = await insert({
      impersonated_by: doomed!.id,
      impersonator_label: 'departing@example.test',
      impersonation_reason: 'a support call that outlived the operator',
      impersonation_audit_seq: auditSeq,
    })
    assert.equal(err, null, `the session could not be created: ${err?.message}`)

    await assert.doesNotReject(
      () => h.admin`DELETE FROM admin_users WHERE id = ${doomed!.id}`,
      "deleting an operator with a past impersonation was refused",
    )

    const [row] = await h.admin<{
      impersonated_by: string | null
      impersonator_label: string
      impersonation_reason: string
    }[]>`
      SELECT impersonated_by, impersonator_label, impersonation_reason
      FROM sessions
      WHERE impersonation_reason = 'a support call that outlived the operator'`
    assert.ok(row, 'the session was deleted along with the operator')
    assert.equal(row.impersonated_by, null, 'the operator id survived a delete that should null it')
    assert.equal(
      row.impersonator_label,
      'departing@example.test',
      'the record lost the only durable name it had',
    )
  })

  /**
   * The negative control, and the reason the four tests above mean anything.
   *
   * Every assertion before this one passes if the constraint refuses every
   * INSERT. Almost every session this product creates is an ordinary one, so a
   * constraint that rejected them would sign out the entire customer base
   * while looking, from the tests alone, like unusually careful work.
   */
  it('still accepts an ordinary session, which is almost all of them', async () => {
    const err = await insert({})
    assert.equal(err, null, `an ordinary session was refused: ${err?.message}`)
  })
})
