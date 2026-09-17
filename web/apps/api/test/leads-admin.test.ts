// The enterprise leads queue reaches a real operator page, on a credential the
// serving process does not have.
//
// This is the operator half of what web/apps/api/test/leads.test.ts proves for
// the public form. That file proves a lead lands and that the serving role
// cannot read it back; this file proves the portal can. The chain it asserts is
// the one an operator experiences: the queue is unreadable to the serving
// credential and readable to the operator one, it lists oldest first, marking a
// lead handled works only because migration 0047 grants the operator role
// UPDATE, and a second mark is refused rather than silently overwriting who
// answered it and when.
//
// Every write path is guarded the way the recruitment lane's is: anonymous is
// refused, a role without the permission is refused, and a mutation without the
// operator CSRF token is refused. The permission is held by owner alone, so the
// wrong-role case uses an owner demoted to support.

import { describe, it, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { available, startApi, type ApiHarness } from './harness.ts'
import { adminSignIn, hashPassword, adminCsrfTokenFor } from '../src/admin/session.ts'

const hasDb = await available()

/** One lead row, inserted straight through the privileged connection rather
 *  than the public route, because this file is about the OPERATOR side and the
 *  public route has its own suite. The columns are the ones 0035 requires NOT
 *  NULL; seats is optional and defaults to null here. */
async function insertLead(
  h: ApiHarness,
  overrides: { company?: string; createdAt?: string; seats?: number | null } = {},
): Promise<string> {
  const id = randomUUID()
  await h.admin`
    INSERT INTO enterprise_leads (id, email, name, company, seats, message, source, created_at)
    VALUES (${id}::uuid, 'buyer@example.test', 'Ada Buyer', ${overrides.company ?? 'Acme'},
            ${overrides.seats ?? null}, 'We want to buy.', 'pricing',
            ${overrides.createdAt ?? new Date().toISOString()}::timestamptz)`
  return id
}

/** One page of the operator queue, in the envelope tRPC puts it in. */
const listed = async (response: Response) =>
  (await response.json()) as {
    result?: {
      data?: {
        rows?: { id: string; company: string; handledAt: string | null }[]
        nextCursor?: { id: string; createdAt: string } | null
      }
    }
  }

describe('the enterprise leads queue reaches a real operator page', { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let headers: Record<string, string>
  let signedToken: string

  before(async () => {
    h = await startApi()
    h.clock.now = () => new Date()
    const password = 'leads-local-test-only'
    const { hash, salt } = await hashPassword(password)
    await h.admin`INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES ('leads-owner@example.test', 'Leads owner', 'owner', ${hash}, ${salt}, now())
      ON CONFLICT (email) DO UPDATE SET password_hash = ${hash}, password_salt = ${salt}, role = 'owner'`
    const signed = await adminSignIn(h.pool, { email: 'leads-owner@example.test', password }, new Date())
    signedToken = signed.token
    headers = {
      cookie: `af_admin_session=${signed.token}`,
      'x-antifailure-admin-csrf': adminCsrfTokenFor(signed.token),
      'content-type': 'application/json',
    }
  })
  beforeEach(async () => {
    await h.admin`DELETE FROM enterprise_leads`
  })
  after(async () => {
    await h.admin`DELETE FROM admin_users WHERE email = 'leads-owner@example.test'`
    await h?.close()
  })

  const handle = (id: string, note?: string) =>
    h.fetch('/trpc/admin.administration.leads.handle', {
      method: 'POST',
      headers,
      body: JSON.stringify(note === undefined ? { id } : { id, note }),
    })

  it('the serving credential cannot read the leads and the operator one can', async () => {
    const id = await insertLead(h)
    // The boundary migration 0035 draws, asserted rather than assumed: the
    // serving role holds INSERT and no SELECT, so a query bug on the anonymous
    // route cannot publish a prospect's details. The database's own words are
    // on `cause`, so match those rather than drizzle's "Failed query" wrapper.
    await assert.rejects(
      () => h.pool.withoutTenant((db) => db.execute(sql`SELECT id FROM enterprise_leads LIMIT 1`)),
      (error: unknown) => (error as { cause?: { code?: string } }).cause?.code === '42501',
    )
    // And the operator route, which reads on antifailure_admin, returns it.
    const body = await listed(await h.fetch('/trpc/admin.administration.leads.list', { headers }))
    assert.equal(body.result?.data?.rows?.[0]?.id, id, JSON.stringify(body))
  })

  it('anonymous requests cannot read the queue', async () => {
    assert.equal((await h.fetch('/trpc/admin.administration.leads.list')).status, 401)
  })

  it('an operator without the leads permission cannot read the queue', async () => {
    await h.admin`UPDATE admin_users SET role = 'support' WHERE email = 'leads-owner@example.test'`
    try {
      assert.equal((await h.fetch('/trpc/admin.administration.leads.list', { headers })).status, 403)
    } finally {
      await h.admin`UPDATE admin_users SET role = 'owner' WHERE email = 'leads-owner@example.test'`
    }
  })

  it('lists the waiting leads oldest first', async () => {
    const older = await insertLead(h, { company: 'Older', createdAt: new Date(Date.UTC(2026, 0, 1)).toISOString() })
    const newer = await insertLead(h, { company: 'Newer', createdAt: new Date(Date.UTC(2026, 0, 2)).toISOString() })
    const body = await listed(await h.fetch('/trpc/admin.administration.leads.list', { headers }))
    assert.deepEqual(body.result?.data?.rows?.map((r) => r.id), [older, newer], JSON.stringify(body))
  })

  it('marking a lead handled works on the new UPDATE grant and records who did it', async () => {
    const id = await insertLead(h)
    const response = await handle(id, 'called them back')
    assert.equal(response.status, 200, await response.text())
    const rows = await h.admin`
      SELECT handled_at IS NOT NULL AS handled, handled_note,
             (handled_by = (SELECT id FROM admin_users WHERE email = 'leads-owner@example.test')) AS by_owner
      FROM enterprise_leads WHERE id = ${id}::uuid`
    assert.deepEqual(
      { handled: rows[0]?.handled, note: rows[0]?.handled_note, byOwner: rows[0]?.by_owner },
      { handled: true, note: 'called them back', byOwner: true },
    )
  })

  it('a handled lead moves from the waiting queue into the handled one', async () => {
    const id = await insertLead(h)
    await handle(id)
    const waiting = await listed(await h.fetch('/trpc/admin.administration.leads.list', { headers }))
    assert.deepEqual(waiting.result?.data?.rows?.map((r) => r.id), [])
    const done = await listed(
      await h.fetch(`/trpc/admin.administration.leads.list?input=${encodeURIComponent(JSON.stringify({ handled: true }))}`, { headers }),
    )
    assert.deepEqual(done.result?.data?.rows?.map((r) => r.id), [id])
  })

  it('a second handle is refused rather than overwriting who answered it', async () => {
    const id = await insertLead(h)
    assert.equal((await handle(id, 'first')).status, 200)
    assert.equal((await handle(id, 'second')).status, 409)
  })

  it('handle racing handle records one audit action', async () => {
    const id = await insertLead(h)
    await Promise.all([handle(id), handle(id)])
    assert.equal(
      (await h.admin`SELECT count(*)::int AS n FROM admin_audit_entries WHERE action = 'leads.handled' AND target_id = ${id}`)[0]!.n,
      1,
    )
  })

  it('a handle without the operator CSRF token is refused', async () => {
    const id = await insertLead(h)
    const response = await h.fetch('/trpc/admin.administration.leads.handle', {
      method: 'POST',
      headers: { cookie: `af_admin_session=${signedToken}`, 'content-type': 'application/json' },
      body: JSON.stringify({ id }),
    })
    assert.equal(response.status, 403)
  })

  it('pagination survives removal of the cursor lead', async () => {
    for (let i = 0; i < 51; i++) {
      await h.admin`INSERT INTO enterprise_leads (id, email, name, company, seats, message, source, created_at)
        VALUES (${randomUUID()}::uuid, 'buyer@example.test', 'Ada Buyer', ${`Company ${i}`}, NULL,
                'Paging fixture', 'pricing', ${new Date(Date.UTC(2026, 0, 1, 0, i)).toISOString()}::timestamptz)`
    }
    const first = await listed(await h.fetch('/trpc/admin.administration.leads.list', { headers }))
    const cursor = first.result?.data?.nextCursor
    if (!cursor) throw new Error('The first page did not produce a cursor')
    await h.admin`DELETE FROM enterprise_leads WHERE id = ${cursor.id}::uuid`
    const next = await listed(
      await h.fetch(`/trpc/admin.administration.leads.list?input=${encodeURIComponent(JSON.stringify({ cursor }))}`, { headers }),
    )
    assert.deepEqual(next.result?.data?.rows?.map((r) => r.company), ['Company 50'])
  })
})
