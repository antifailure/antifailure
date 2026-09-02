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

  before(async () => {
    h = await setup()
    tenant = await seedTenant(h.admin, 'impersonation')
  })

  after(async () => {
    await h.admin`DELETE FROM sessions WHERE user_id = ${tenant.userId}`
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
      impersonated_by: tenant.userId,
      impersonator_label: 'an operator',
      impersonation_audit_seq: 1,
    })
    assert.ok(err, 'an impersonation with no reason was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  it('refuses a reason that is only whitespace, which is not a reason', async () => {
    const err = await insert({
      impersonated_by: tenant.userId,
      impersonator_label: 'an operator',
      impersonation_reason: '   ',
      impersonation_audit_seq: 1,
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
      impersonated_by: tenant.userId,
      impersonator_label: 'an operator',
      impersonation_reason: 'looking into a failed run',
    })
    assert.ok(err, 'an unaudited impersonation was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  it('refuses a marker with no operator behind it', async () => {
    const err = await insert({
      impersonation_reason: 'looking into a failed run',
      impersonation_audit_seq: 1,
    })
    assert.ok(err, 'a session claiming a reason but no operator was accepted')
    assert.match(err.message, new RegExp(CONSTRAINT))
  })

  it('accepts a complete impersonation', async () => {
    const err = await insert({
      impersonated_by: tenant.userId,
      impersonator_label: 'an operator',
      impersonation_reason: 'looking into a failed run',
      impersonation_audit_seq: 42,
    })
    assert.equal(err, null, `a complete impersonation was refused: ${err?.message}`)
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
