import { describe, it, before, after, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { startApi, type ApiHarness } from './harness.ts'
import { adminSignIn, hashPassword, adminCsrfTokenFor } from '../src/admin/session.ts'
import { runMaintenance } from '../src/maintenance.ts'
import { adminUrl } from './harness.ts'

const SITE = 'https://careers.test'
const valid = () => ({ submissionId: randomUUID(), name: 'Ada', email: 'ada@example.test', role: 'founding_engineer', projectUrl: '', why: 'Built a compiler.', compensationAcknowledged: true, website: '' })

/** What the route answers an applicant with, read as the form itself reads it. */
const recorded = async (response: Response) =>
  await response.json() as { id: string; submissionId: string; recorded: boolean }

/** One page of the operator queue, in the envelope tRPC puts it in. */
const listed = async (response: Response) =>
  await response.json() as {
    result?: { data?: { rows?: { name: string; why: string }[]; nextCursor?: { id: string; createdAt: string } | null } }
  }

describe('a careers application reaches a real private queue', () => {
  let h: ApiHarness
  let headers: Record<string, string>
  let caller = 0
  const post = (value: unknown, origin = SITE) => h.fetch('/v1/applications', {
    method: 'POST', headers: { origin, 'content-type': 'application/json', 'x-forwarded-for': `192.0.2.${++caller}` }, body: JSON.stringify(value),
  })
  const mutation = (action: string, id: string) => h.fetch(`/trpc/admin.administration.applications.${action}`, {
    method: 'POST', headers, body: JSON.stringify({ id }),
  })
  before(async () => {
    h = await startApi({ siteOrigins: [SITE] })
    h.clock.now = () => new Date()
    const password = 'careers-local-test-only'
    const { hash, salt } = await hashPassword(password)
    await h.admin`INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES ('careers-owner@example.test', 'Careers owner', 'owner', ${hash}, ${salt}, now())
      ON CONFLICT (email) DO UPDATE SET password_hash = ${hash}, password_salt = ${salt}`
    const signed = await adminSignIn(h.pool, { email: 'careers-owner@example.test', password }, new Date())
    headers = { cookie: `af_admin_session=${signed.token}`, 'x-antifailure-admin-csrf': adminCsrfTokenFor(signed.token), 'content-type': 'application/json' }
  })
  beforeEach(async () => { await h.admin`DELETE FROM recruitment_applications` })
  after(async () => { await h?.close() })

  it('confirms the durable submission with a real reference', async () => {
    const response = await post(valid())
    assert.equal(response.status, 201, await response.text())
  })
  it('stores exactly the applicant answers', async () => {
    await post(valid())
    const rows = await h.admin`SELECT name, email, role, project_url, why, compensation_acknowledged FROM recruitment_applications`
    assert.deepEqual(Array.from(rows), [{ name: 'Ada', email: 'ada@example.test', role: 'founding_engineer', project_url: '', why: 'Built a compiler.', compensation_acknowledged: true }])
  })
  it('concurrent identical retries create one application', async () => {
    const input = valid()
    const responses = await Promise.all([post(input), post(input)])
    assert.deepEqual({ statuses: responses.map((r) => r.status), rows: (await h.admin`SELECT count(*)::int AS n FROM recruitment_applications`)[0]!.n }, { statuses: [201, 201], rows: 1 })
  })
  it('an altered payload never acknowledges an old answer', async () => {
    const input = valid()
    await post(input)
    await post({ ...input, why: 'Built a database.' })
    assert.deepEqual((await h.admin`SELECT why FROM recruitment_applications ORDER BY why`).map((r) => r.why), ['Built a compiler.', 'Built a database.'])
  })
  it('the serving database credential cannot read applications', async () => {
    await post(valid())
    await assert.rejects(h.pool.withoutTenant((db) => db.execute(sql`SELECT * FROM recruitment_applications`)), (error: unknown) => (error as { cause?: { code?: string } }).cause?.code === '42501')
  })
  it('a recorded application is readable through the actual operator route', async () => {
    await post(valid())
    const response = await h.fetch('/trpc/admin.administration.applications.list', { headers })
    const body = await listed(response)
    assert.equal(body.result?.data?.rows?.[0]?.why, 'Built a compiler.', JSON.stringify(body))
  })
  it('anonymous requests cannot read the queue', async () => {
    assert.equal((await h.fetch('/trpc/admin.administration.applications.list')).status, 401)
  })
  it('review changes the stored state', async () => {
    const saved = await recorded(await post(valid()))
    const response = await mutation('review', saved.id)
    const rows = await h.admin`SELECT reviewed_at IS NOT NULL AS reviewed FROM recruitment_applications`
    assert.equal(rows[0]?.reviewed, true, await response.text())
  })
  it('review racing review records one audit action', async () => {
    const saved = await recorded(await post(valid()))
    await Promise.all([mutation('review', saved.id), mutation('review', saved.id)])
    assert.equal((await h.admin`SELECT count(*)::int AS n FROM admin_audit_entries WHERE action = 'recruitment.reviewed' AND target_id = ${saved.id}`)[0]!.n, 1)
  })
  it('delete removes the actual personal data', async () => {
    const saved = await recorded(await post(valid()))
    if (!saved.recorded) throw new Error('Fixture application was not recorded')
    const response = await mutation('remove', saved.id)
    assert.equal((await h.admin`SELECT count(*)::int AS n FROM recruitment_applications`)[0]!.n, 0, await response.text())
  })
  it('the scheduled maintenance path expires an old application', async () => {
    const saved = await recorded(await post(valid()))
    if (!saved.recorded) throw new Error('Fixture application was not recorded')
    await h.admin`UPDATE recruitment_applications SET created_at = now() - interval '181 days'`
    await runMaintenance({ adminUrl }, h.clock)
    assert.equal((await h.admin`SELECT count(*)::int AS n FROM recruitment_applications`)[0]!.n, 0)
  })
  it('maintenance retains an application inside the window', async () => {
    await post(valid())
    await runMaintenance({ adminUrl }, h.clock)
    assert.equal((await h.admin`SELECT count(*)::int AS n FROM recruitment_applications`)[0]!.n, 1)
  })
  it('refuses a different website origin', async () => { assert.equal((await post(valid(), 'https://evil.test')).status, 403) })
  it('refuses missing compensation acknowledgement', async () => { assert.equal((await post({ ...valid(), compensationAcknowledged: false })).status, 400) })
  it('refuses a hidden spam field', async () => { assert.equal((await post({ ...valid(), website: 'spam' })).status, 400) })
  it('refuses an executable work URL', async () => { assert.equal((await post({ ...valid(), projectUrl: 'javascript:alert(1)' })).status, 400) })
  it('refuses unknown payload fields', async () => { assert.equal((await post({ ...valid(), secret: 'not accepted' })).status, 400) })
  it('refuses a payload above the body limit', async () => { assert.equal((await post({ ...valid(), why: 'x'.repeat(40000) })).status, 413) })
  it('rate refusals remain readable from the allowed site', async () => {
    const headers = { origin: SITE, 'content-type': 'application/json', 'x-forwarded-for': '198.51.100.199' }
    let response: Response | undefined
    for (let i = 0; i < 6; i++) response = await h.fetch('/v1/applications', { method: 'POST', headers, body: JSON.stringify(valid()) })
    assert.deepEqual({ status: response!.status, origin: response!.headers.get('access-control-allow-origin') }, { status: 429, origin: SITE })
  })
  it('a refused database write cannot report recorded', async () => {
    await h.admin`REVOKE INSERT ON recruitment_applications FROM antifailure_app`
    try { assert.equal((await post(valid())).status, 503) }
    finally { await h.admin`GRANT INSERT ON recruitment_applications TO antifailure_app` }
  })
  it('a removed application cannot be marked reviewed', async () => {
    const saved = await recorded(await post(valid()))
    if (!saved.recorded) throw new Error('Fixture application was not recorded')
    await mutation('remove', saved.id)
    assert.equal((await mutation('review', saved.id)).status, 409)
  })
  it('an operator without recruitment permission cannot read applicant details', async () => {
    await h.admin`UPDATE admin_users SET role = 'support' WHERE email = 'careers-owner@example.test'`
    try { assert.equal((await h.fetch('/trpc/admin.administration.applications.list', { headers })).status, 403) }
    finally { await h.admin`UPDATE admin_users SET role = 'owner' WHERE email = 'careers-owner@example.test'` }
  })
  it('a review without the operator CSRF token is refused', async () => {
    const saved = await recorded(await post(valid()))
    const response = await h.fetch('/trpc/admin.administration.applications.review', { method: 'POST', headers: { cookie: headers.cookie!, 'content-type': 'application/json' }, body: JSON.stringify({ id: saved.id }) })
    assert.equal(response.status, 403)
  })
  it('pagination survives removal of the cursor application', async () => {
    for (let i = 0; i < 51; i++) await h.admin`INSERT INTO recruitment_applications
      (id, name, email, role, why, compensation_acknowledged, created_at)
      VALUES (${randomUUID()}, ${`Applicant ${i}`}, 'paging@example.test', 'founding_engineer', 'Paging fixture', true,
              ${new Date(Date.UTC(2026, 0, 1, 0, i)).toISOString()})`
    const first = await listed(await h.fetch('/trpc/admin.administration.applications.list', { headers }))
    const cursor = first.result?.data?.nextCursor
    if (!cursor) throw new Error('The first page did not produce a cursor')
    await h.admin`DELETE FROM recruitment_applications WHERE id = ${cursor.id}`
    const next = await listed(await h.fetch(`/trpc/admin.administration.applications.list?input=${encodeURIComponent(JSON.stringify({ cursor }))}`, { headers }))
    assert.deepEqual(next.result?.data?.rows?.map((r) => r.name), ['Applicant 50'])
  })
})
