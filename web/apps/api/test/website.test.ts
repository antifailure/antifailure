import { after, before, beforeEach, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { sql } from '@antifailure/db'
import { emptyWebsiteDocument, pageContentKey } from '@antifailure/website'
import { appRouter } from '../src/routers/index.ts'
import { actorOf } from '../src/admin/trpc.ts'
import { resolveAdminSession } from '../src/admin/session.ts'
import { websiteDigest } from '../src/admin/website-store.ts'
import { ADMIN_ROLES, adminRoleHas, type AdminRole } from '../src/admin/permissions.ts'
import { available, dropOrg, seedOrg, signInAs, startApi, type ApiHarness } from './harness.ts'

const hasDb = await available()
const denied = (error: unknown): boolean => error instanceof Error && (error.cause as { code?: string } | undefined)?.code === '42501'
const changed = (text: string) => ({ ...emptyWebsiteDocument(), fields: { 'hero.heading': text } })

test('website editing and publication are owner-only capabilities', () => {
  for (const role of ADMIN_ROLES) for (const permission of ['admin.website.read', 'admin.website.write', 'admin.website.publish'] as const) {
    assert.equal(adminRoleHas(role, permission), role === 'owner')
  }
})

describe('the website draft and publication boundary', { skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let owner: Awaited<ReturnType<typeof operator>>

  async function operator(role: AdminRole) {
    const email = `website-${randomUUID()}@example.test`
    const [user] = await h.admin<{ id: string }[]>`INSERT INTO admin_users(email, name, role) VALUES (${email}, 'Website operator', ${role}) RETURNING id`
    const token = randomBytes(32).toString('base64url')
    await h.admin`INSERT INTO admin_sessions(token_hash, admin_user_id, expires_at)
      VALUES (${createHash('sha256').update(token).digest()}, ${user!.id}, ${new Date(Date.now() + 3_600_000).toISOString()})`
    const session = await resolveAdminSession(h.pool, token, new Date())
    assert.ok(session)
    const cookie = `af_admin_session=${token}`
    const response = await h.fetch('/v1/admin/session', { headers: { cookie } })
    const csrf = (await response.json() as { csrfToken: string }).csrfToken
    return { email, cookie, csrf, id: user!.id, caller: appRouter.createCaller({ pool: h.pool, adminPool: h.adminPool,
      clock: h.clock, github: h.github, admin: actorOf(session), actor: null, origin: 'web', appBaseUrl: 'http://localhost',
      stripe: null, mailer: null, productName: 'Antifailure', hostedRequiredPlan: null } as never).admin.administration.website }
  }

  before(async () => { h = await startApi(); owner = await operator('owner') })
  beforeEach(async () => {
    // This suite runs only in its dedicated CMS database. Reset global content
    // through the test superuser; neither serving role holds these powers.
    await h.admin`TRUNCATE website_refresh_jobs, website_mutations, website_history, website_assets, website_ai_usage CASCADE`
    const empty = emptyWebsiteDocument()
    await h.admin`UPDATE website_draft SET revision = 0, document = ${h.admin.json(JSON.parse(JSON.stringify(empty)))}, updated_by = NULL`
    await h.admin`UPDATE website_published SET revision = 0, document = ${h.admin.json(JSON.parse(JSON.stringify(empty)))}, content_hash = ${websiteDigest(empty)}`
  })
  after(async () => { await h.close() })

  test('A then B saves in order; only publication changes the public document', async () => {
    const a = await owner.caller.saveDraft({ document: changed('A'), expectedRevision: 0, requestId: randomUUID() })
    assert.equal(a.draftRevision, 1)
    assert.equal(a.publishedRevision, 0)
    const b = await owner.caller.saveDraft({ document: changed('B'), expectedRevision: 1, requestId: randomUUID() })
    assert.equal(b.draftRevision, 2)
    const beforePublish = await h.pool.withoutTenant((db) => db.execute<{ document: unknown }>(sql`SELECT document FROM website_published`))
    assert.deepEqual(beforePublish[0]!.document, emptyWebsiteDocument())
    const published = await owner.caller.publish({ expectedRevision: 2, requestId: randomUUID() })
    assert.equal(published.draftRevision, 2)
    assert.equal(published.publishedRevision, 1)
    assert.equal(published.refresh?.status, 'queued')
    const publicRow = await h.pool.withoutTenant((db) => db.execute<{ document: unknown }>(sql`SELECT document FROM website_published`))
    assert.deepEqual(publicRow[0]!.document, changed('B'))
  })

  test('B then stale A preserves B and rejects the late older edit', async () => {
    await owner.caller.saveDraft({ document: changed('B'), expectedRevision: 0, requestId: randomUUID() })
    await assert.rejects(() => owner.caller.saveDraft({ document: changed('A'), expectedRevision: 0, requestId: randomUUID() }), { code: 'CONFLICT' })
    assert.equal((await owner.caller.get()).document.fields['hero.heading'], 'B')
  })

  test('A and B concurrent accept exactly one without losing the winner', async () => {
    const results = await Promise.allSettled(['A', 'B'].map((text) => owner.caller.saveDraft({ document: changed(text), expectedRevision: 0, requestId: randomUUID() })))
    assert.equal(results.filter((r) => r.status === 'fulfilled').length, 1)
    assert.equal(results.filter((r) => r.status === 'rejected').length, 1)
    const state = await owner.caller.get()
    assert.equal(state.draftRevision, 1)
    const winner = results.find((r) => r.status === 'fulfilled')
    assert.equal(state.document.fields['hero.heading'], winner!.status === 'fulfilled' ? winner!.value.document.fields['hero.heading'] : null)
  })

  test('A with no publish stays private; publish with no edits publishes source defaults', async () => {
    const initial = await owner.caller.publish({ expectedRevision: 0, requestId: randomUUID() })
    assert.deepEqual((await owner.caller.revision({ revision: initial.publishedRevision })).document, emptyWebsiteDocument())
    await owner.caller.saveDraft({ document: changed('Private'), expectedRevision: 0, requestId: randomUUID() })
    const publicRows = await h.pool.withoutTenant((db) => db.execute<{ document: unknown }>(sql`SELECT document FROM website_published`))
    assert.deepEqual(publicRows[0]!.document, emptyWebsiteDocument())
  })

  test('a new article stays a recoverable draft until its body is complete, then publishes at its path', async () => {
    const path = '/blog/a-tested-release'
    const key = (name: string) => pageContentKey(path, name)
    const draft = emptyWebsiteDocument()
    draft.pages = [{ path, kind: 'post' }]
    draft.fields[key('title')] = 'A tested release'
    draft.fields[key('description')] = 'The check that caught a deployment problem.'
    draft.fields[key('summary')] = 'How a rehearsal found the problem.'
    draft.fields[key('published')] = '2026-09-27'
    draft.fields[key('tags')] = 'Engineering, Releases'
    draft.fields[key('body')] = { type: 'doc', content: [{ type: 'paragraph' }] }
    const saved = await owner.caller.saveDraft({ document: draft, expectedRevision: 0, requestId: randomUUID() })
    assert.equal(saved.draftRevision, 1)
    await assert.rejects(() => owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() }), { code: 'BAD_REQUEST' })
    assert.equal((await owner.caller.get()).publishedRevision, 0)
    draft.fields[key('body')] = { type: 'doc', content: [{ type: 'heading', attrs: { level: 2 }, content: [{ type: 'text', text: 'The rehearsal' }] }, { type: 'paragraph', content: [{ type: 'text', text: 'The release was tested on an isolated twin.' }] }] }
    await owner.caller.saveDraft({ document: draft, expectedRevision: 1, requestId: randomUUID() })
    const published = await owner.caller.publish({ expectedRevision: 2, requestId: randomUUID() })
    assert.equal(published.publishedRevision, 1)
    assert.equal(published.refresh?.status, 'queued')
    const current = await h.pool.withoutTenant((db) => db.execute<{ document: { pages?: Array<{ path: string }> } }>(sql`SELECT document FROM website_published`))
    assert.equal(current[0]!.document.pages?.[0]?.path, path)
  })

  test('a lost save response retries the same receipt after later edits', async () => {
    const input = { document: changed('A'), expectedRevision: 0, requestId: randomUUID() }
    const first = await owner.caller.saveDraft(input)
    await owner.caller.saveDraft({ document: changed('B'), expectedRevision: 1, requestId: randomUUID() })
    assert.deepEqual(await owner.caller.saveDraft(input), first)
    assert.equal((await owner.caller.get()).draftRevision, 2)
    await assert.rejects(() => owner.caller.saveDraft({ ...input, document: changed('forged retry') }), { code: 'CONFLICT' })
  })

  test('publication retries append exactly one history row and refresh job', async () => {
    await owner.caller.saveDraft({ document: changed('A'), expectedRevision: 0, requestId: randomUUID() })
    const input = { expectedRevision: 1, requestId: randomUUID() }
    const [a, b] = await Promise.all([owner.caller.publish(input), owner.caller.publish(input)])
    assert.deepEqual(a, b)
    assert.equal((await owner.caller.history({ limit: 10 })).items.length, 1)
    const [jobs] = await h.admin`SELECT count(*)::int AS count FROM website_refresh_jobs`
    assert.equal(jobs!.count, 1)
  })

  test('a concurrent save and publish cannot publish the unreviewed later edit', async () => {
    await owner.caller.saveDraft({ document: changed('reviewed'), expectedRevision: 0, requestId: randomUUID() })
    const [publication, save] = await Promise.allSettled([
      owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() }),
      owner.caller.saveDraft({ document: changed('later edit'), expectedRevision: 1, requestId: randomUUID() }),
    ])
    assert.equal(save.status, 'fulfilled')
    const state = await owner.caller.get()
    assert.equal(state.document.fields['hero.heading'], 'later edit')
    if (publication.status === 'fulfilled') {
      assert.equal((await owner.caller.revision({ revision: 1 })).document.fields['hero.heading'], 'reviewed')
    } else {
      assert.equal(publication.reason.code, 'CONFLICT')
      assert.equal(state.publishedRevision, 0)
    }
  })

  test('restore appends a version, restores the editable draft and preserves original history', async () => {
    await owner.caller.saveDraft({ document: changed('A'), expectedRevision: 0, requestId: randomUUID() })
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    const original = await owner.caller.revision({ revision: 1 })
    await owner.caller.saveDraft({ document: changed('B'), expectedRevision: 1, requestId: randomUUID() })
    await owner.caller.publish({ expectedRevision: 2, requestId: randomUUID() })
    await assert.rejects(() => owner.caller.restore({ revision: 1, expectedRevision: 1, requestId: randomUUID() }), { code: 'CONFLICT' })
    const input = { revision: 1, expectedRevision: 2, requestId: randomUUID() }
    const restored = await owner.caller.restore(input)
    assert.equal(restored.draftRevision, 3)
    assert.equal(restored.publishedRevision, 3)
    assert.equal(restored.document.fields['hero.heading'], 'A')
    assert.deepEqual(await owner.caller.restore(input), restored)
    assert.deepEqual(await owner.caller.revision({ revision: 1 }), original)
    assert.equal((await owner.caller.revision({ revision: 3 })).restoredFrom, 1)
  })

  test('sourceVersion and publication cursor paginate immutable history', async () => {
    await owner.caller.saveDraft({ document: { ...changed('A'), sourceVersion: 'test-source' }, expectedRevision: 0, requestId: randomUUID() })
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    const first = await owner.caller.history({ limit: 1 })
    assert.equal(first.items[0]!.revision, 2)
    assert.equal(first.items[0]!.sourceVersion, 'test-source')
    assert.equal(first.nextCursor, 2)
    const second = await owner.caller.history({ limit: 1, cursor: first.nextCursor! })
    assert.equal(second.items[0]!.revision, 1)
    assert.equal(second.nextCursor, null)
  })

  test('the serving role cannot read operator-only website data or write published content', async () => {
    for (const table of ['website_draft', 'website_history', 'website_secrets', 'website_mutations', 'website_refresh_jobs', 'website_ai_usage']) {
      await assert.rejects(() => h.pool.withoutTenant((db) => db.execute(sql.raw(`SELECT * FROM ${table}`))), denied)
    }
    await assert.rejects(() => h.pool.withoutTenant((db) => db.execute(sql`UPDATE website_published SET revision = 99`)), denied)
    await assert.rejects(() => h.adminPool.withOperator({ adminUserId: owner.id, label: owner.email }, (db) => db.execute(sql`DELETE FROM website_history`)), denied)
    await assert.rejects(() => h.adminPool.withOperator({ adminUserId: owner.id, label: owner.email }, (db) => db.execute(sql`UPDATE website_history SET content_hash = 'changed'`)), denied)
  })

  test('forced row security still hides drafts after an accidental SELECT grant', async () => {
    await owner.caller.saveDraft({ document: changed('private even with a grant'), expectedRevision: 0, requestId: randomUUID() })
    await h.admin.unsafe('GRANT SELECT ON website_draft TO antifailure_app')
    try {
      const rows = await h.pool.withoutTenant((db) => db.execute(sql`SELECT document FROM website_draft`))
      assert.equal(rows.length, 0)
    } finally {
      await h.admin.unsafe('REVOKE SELECT ON website_draft FROM antifailure_app')
    }
  })

  test('every non-owner operator is refused; refusal changes no draft', async () => {
    for (const role of ADMIN_ROLES.filter((r) => r !== 'owner')) {
      const other = await operator(role)
      await assert.rejects(() => other.caller.get(), { code: 'FORBIDDEN' })
      await assert.rejects(() => other.caller.saveDraft({ document: changed(role), expectedRevision: 0, requestId: randomUUID() }), { code: 'FORBIDDEN' })
      await assert.rejects(() => other.caller.publish({ expectedRevision: 0, requestId: randomUUID() }), { code: 'FORBIDDEN' })
    }
    assert.equal((await owner.caller.get()).draftRevision, 0)
  })

  test('an anonymous visitor and a customer owner cannot enter the global editor', async () => {
    const anonymous = await h.fetch('/trpc/admin.administration.website.get')
    assert.equal(anonymous.status, 401)
    const organization = await seedOrg(h.admin, 'website-customer')
    try {
      const customer = await signInAs(h, organization, 'owner')
      const response = await h.fetch('/trpc/admin.administration.website.get', { headers: { cookie: customer.cookie } })
      assert.equal(response.status, 401)
      const change = await h.fetch('/trpc/admin.administration.website.saveDraft', { method: 'POST',
        headers: { 'content-type': 'application/json', cookie: customer.cookie, 'x-antifailure-csrf': customer.csrfToken, origin: 'http://app.test' },
        body: JSON.stringify({ document: changed('tenant edit'), expectedRevision: 0, requestId: randomUUID() }) })
      assert.equal(change.status, 401)
      assert.equal((await owner.caller.get()).draftRevision, 0)
    } finally { await dropOrg(h.admin, organization.orgId) }
  })

  test('real HTTP rejects absent CSRF and a same-site foreign origin before changing the draft', async () => {
    for (const headers of [{ cookie: owner.cookie }, { cookie: owner.cookie, 'x-antifailure-admin-csrf': owner.csrf, origin: 'https://www.antifailure.dev' }]) {
      const response = await h.fetch('/trpc/admin.administration.website.saveDraft', { method: 'POST', headers: { 'content-type': 'application/json', ...headers },
        body: JSON.stringify({ document: changed('refused'), expectedRevision: 0, requestId: randomUUID() }) })
      assert.equal(response.status, 403)
    }
    assert.equal((await owner.caller.get()).draftRevision, 0)
    const accepted = await h.fetch('/trpc/admin.administration.website.saveDraft', { method: 'POST',
      headers: { 'content-type': 'application/json', cookie: owner.cookie, 'x-antifailure-admin-csrf': owner.csrf, origin: 'http://app.test' },
      body: JSON.stringify({ document: changed('accepted'), expectedRevision: 0, requestId: randomUUID() }) })
    assert.equal(accepted.status, 200)
    assert.equal((await owner.caller.get()).document.fields['hero.heading'], 'accepted')
  })

  test('invalid markup, unsupported styles and missing assets are refused atomically', async () => {
    const id = randomUUID()
    for (const document of [
      { ...changed('x'), styles: { hero: { desktop: { position: 'sticky' } } } },
      { ...changed('x'), fields: { 'hero.link': { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'x', marks: [{ type: 'link', attrs: { href: 'javascript:alert(1)' } }] }] }] } } },
      { ...changed('x'), fields: { 'hero.image': { type: 'media', source: 'asset', assetId: id } } },
    ]) await assert.rejects(() => owner.caller.saveDraft({ document, expectedRevision: 0, requestId: randomUUID() }), { code: 'BAD_REQUEST' })
    assert.equal((await owner.caller.get()).draftRevision, 0)
  })

  test('a malformed stored field cannot blank valid content or silently publish a repaired draft', async () => {
    const broken = { ...changed('preserved'), fields: { 'hero.heading': 'preserved', 'hero.bad': { unexpected: 'value' } } }
    await h.admin`UPDATE website_draft SET document = ${h.admin.json(JSON.parse(JSON.stringify(broken)))}`
    const recovered = await owner.caller.get()
    assert.equal(recovered.document.fields['hero.heading'], 'preserved')
    assert.equal(recovered.document.fields['hero.bad'], undefined)
    assert.ok(recovered.warnings.some((warning) => warning.path.includes('hero.bad')))
    await assert.rejects(() => owner.caller.publish({ expectedRevision: 0, requestId: randomUUID() }), { code: 'BAD_REQUEST' })
    assert.equal((await owner.caller.get()).publishedRevision, 0)
    const saved = await owner.caller.saveDraft({ document: recovered.document, expectedRevision: 0, requestId: randomUUID() })
    assert.deepEqual(saved.warnings, [])
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
  })

  test('save and publish write audit entries in the same transaction', async () => {
    const before = await h.admin`SELECT count(*)::int AS n FROM admin_audit_entries WHERE actor_label = ${owner.email} AND action = 'website.draft.saved'`
    await owner.caller.saveDraft({ document: changed('audited'), expectedRevision: 0, requestId: randomUUID() })
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    const afterSave = await h.admin`SELECT count(*)::int AS n FROM admin_audit_entries WHERE actor_label = ${owner.email} AND action = 'website.draft.saved'`
    assert.equal(afterSave[0]!.n, before[0]!.n + 1)
    const audit = await h.admin`SELECT detail FROM admin_audit_entries WHERE actor_label = ${owner.email} AND action = 'website.published' ORDER BY seq DESC LIMIT 1`
    assert.equal(audit[0]!.detail.contentHash, websiteDigest(changed('audited')))
  })

  test('refresh retry only queues the current failed version', async () => {
    await owner.caller.publish({ expectedRevision: 0, requestId: randomUUID() })
    await assert.rejects(() => owner.caller.retryRefresh({ revision: 1 }), { code: 'CONFLICT' })
    await h.admin`UPDATE website_refresh_jobs SET status = 'failed', attempts = 5, last_error = 'temporary' WHERE revision = 1`
    const retried = await owner.caller.retryRefresh({ revision: 1 })
    assert.equal(retried.refresh?.status, 'queued')
    assert.equal(retried.refresh?.lastError, null)
    assert.equal(retried.refresh?.attempts, 0)
    await owner.caller.publish({ expectedRevision: 0, requestId: randomUUID() })
    await assert.rejects(() => owner.caller.retryRefresh({ revision: 1 }), { code: 'CONFLICT' })
  })

  test('public HTTP serves only published content, a stable ETag and exact origin CORS', async () => {
    const initial = await h.fetch('/v1/website/published', { headers: { origin: 'https://site.test' } })
    assert.equal(initial.status, 200)
    const initialBody = await initial.json() as Record<string, unknown>
    assert.deepEqual(Object.keys(initialBody).sort(), ['contentHash', 'document', 'revision'])
    assert.deepEqual(initialBody.document, emptyWebsiteDocument())
    const etag = initial.headers.get('etag')!
    await owner.caller.saveDraft({ document: changed('private'), expectedRevision: 0, requestId: randomUUID() })
    const unchanged = await h.fetch('/v1/website/published', { headers: { 'if-none-match': etag } })
    assert.equal(unchanged.status, 304)
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    const published = await h.fetch('/v1/website/published', { headers: { 'if-none-match': etag, origin: 'https://site.test' } })
    assert.equal(published.status, 200)
    assert.notEqual(published.headers.get('etag'), etag)
    const body = await published.json() as { document: unknown }
    assert.deepEqual(body.document, changed('private'))
    const page = await h.fetch('/v1/website/published?path=%2Fproduct%2Ftwins')
    assert.equal(page.status, 200)
    assert.deepEqual((await page.json() as { document: unknown }).document, emptyWebsiteDocument())
    const invalid = await h.fetch('/v1/website/published?path=https%3A%2F%2Fevil.test')
    assert.equal(invalid.status, 400)
    const evil = await h.fetch('/v1/website/published', { headers: { origin: 'https://evil-site.test' } })
    assert.equal(evil.headers.get('access-control-allow-origin'), null)
    const preflight = await h.fetch('/v1/website/published', { method: 'OPTIONS', headers: { origin: 'https://evil-site.test' } })
    assert.equal(preflight.status, 403)
    const head = await h.fetch('/v1/website/published', { method: 'HEAD' })
    assert.equal(head.status, 200)
    assert.equal(await head.text(), '')
  })

  test('the homepage response does not include edits for an unrelated page', async () => {
    const document = emptyWebsiteDocument()
    document.fields = { 'hero.heading': 'Homepage', 'page.product-twins.text.h1': 'Twins only' }
    await owner.caller.saveDraft({ document, expectedRevision: 0, requestId: randomUUID() })
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    const home = await (await h.fetch('/v1/website/published?path=%2F')).json() as { document: { fields: Record<string, unknown> } }
    const twin = await (await h.fetch('/v1/website/published?path=%2Fproduct%2Ftwins')).json() as { document: { fields: Record<string, unknown> } }
    assert.deepEqual(home.document.fields, { 'hero.heading': 'Homepage' })
    assert.deepEqual(twin.document.fields, { 'page.product-twins.text.h1': 'Twins only' })
  })

  test('AI proposals are owner-only, budgeted, and cannot write or publish a draft', async () => {
    const previousKey = process.env.AF_CMS_ANTHROPIC_API_KEY
    const previousFetch = globalThis.fetch
    process.env.AF_CMS_ANTHROPIC_API_KEY = 'test-key'
    globalThis.fetch = async () => new Response(JSON.stringify({
      content: [{ type: 'text', text: JSON.stringify({ message: 'Suggested a clearer headline.', edits: [{ key: 'hero.title', value: 'Test before you ship.' }], styles: [], actions: [] }) }],
      usage: { input_tokens: 100, output_tokens: 20 }, stop_reason: 'end_turn',
    }), { status: 200 })
    const input = { page: '/', prompt: 'Make the headline clearer', fields: [{ key: 'hero.title', label: 'Headline', kind: 'text' as const, value: 'Know what happens.' }], targets: ['hero'] }
    try {
      const support = await operator('support')
      await assert.rejects(() => support.caller.propose(input), { code: 'FORBIDDEN' })
      const wide = { ...input, fields: [1, 2, 3].map((n) => ({ key: `hero.text${n}`, label: `Text ${n}`, kind: 'text' as const, value: '界'.repeat(3000) })) }
      await assert.rejects(() => owner.caller.propose(wide), { code: 'BAD_REQUEST' })
      const before = await owner.caller.get()
      for (let i = 0; i < 40; i++) {
        const proposal = await owner.caller.propose(input)
        assert.deepEqual(proposal.edits, [{ key: 'hero.title', value: 'Test before you ship.' }])
      }
      await assert.rejects(() => owner.caller.propose(input), { code: 'TOO_MANY_REQUESTS' })
      const afterState = await owner.caller.get()
      assert.deepEqual(afterState.document, before.document)
      assert.equal(afterState.draftRevision, before.draftRevision)
      assert.equal(afterState.publishedRevision, before.publishedRevision)
    } finally {
      globalThis.fetch = previousFetch
      if (previousKey === undefined) delete process.env.AF_CMS_ANTHROPIC_API_KEY
      else process.env.AF_CMS_ANTHROPIC_API_KEY = previousKey
    }
  })

  test('referenced images and fonts become public atomically; private deletes and archival preserve references', async () => {
    async function asset(kind: 'image' | 'font') {
      const id = randomUUID()
      const data = Buffer.from(id)
      await h.admin`INSERT INTO website_assets(id,sha256,name,kind,mime_type,size_bytes,width,height,bytes,created_by)
        VALUES (${id},${createHash('sha256').update(data).digest('hex')},${`${kind}.file`},${kind},${kind === 'image' ? 'image/png' : 'font/woff2'},${data.length},${kind === 'image' ? 1 : null},${kind === 'image' ? 1 : null},${data},${owner.email})`
      return id
    }
    const image = await asset('image')
    const font = await asset('font')
    const unused = await asset('image')
    const document = { ...changed('with assets'), fields: { 'hero.image': { type: 'media' as const, source: 'asset' as const, assetId: image } }, styles: { hero: { desktop: { fontFamily: `asset:${font}` } } } }
    await owner.caller.saveDraft({ document, expectedRevision: 0, requestId: randomUUID() })
    await assert.rejects(() => owner.caller.deleteAsset({ id: image }), { code: 'CONFLICT' })
    await assert.rejects(() => owner.caller.deleteAsset({ id: image.toUpperCase() }), { code: 'CONFLICT' })
    await assert.rejects(() => owner.caller.deleteAsset({ id: font }), { code: 'CONFLICT' })
    await assert.rejects(() => owner.caller.saveDraft({ document: { ...document, styles: { hero: { desktop: { fontFamily: `asset:${image}` } } } }, expectedRevision: 1, requestId: randomUUID() }), { code: 'BAD_REQUEST' })
    await assert.rejects(() => owner.caller.saveDraft({ document: { ...document, fields: { 'hero.image': { type: 'media', source: 'asset', assetId: font } } }, expectedRevision: 1, requestId: randomUUID() }), { code: 'BAD_REQUEST' })
    await assert.rejects(() => owner.caller.saveDraft({ document: { ...document, fields: { 'hero.image': { type: 'media', source: 'asset', kind: 'video', assetId: image } } }, expectedRevision: 1, requestId: randomUUID() }), { code: 'BAD_REQUEST' })
    await owner.caller.deleteAsset({ id: unused })
    await owner.caller.archiveAsset({ id: image, archived: true })
    const preview = await owner.caller.assetPreview({ ids: [image] })
    assert.ok(preview.urls[image], 'archival must not break a draft preview')
    assert.equal((await owner.caller.assets()).items.length, 1)
    await owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() })
    const visible = await h.pool.withoutTenant((db) => db.execute<{ id: string }>(sql`SELECT id FROM website_assets`))
    assert.deepEqual(visible.map((r) => r.id).sort(), [image, font].sort())
    await assert.rejects(() => h.pool.withoutTenant((db) => db.execute(sql`SELECT name, created_by FROM website_assets`)), denied)
    await owner.caller.saveDraft({ document: emptyWebsiteDocument(), expectedRevision: 1, requestId: randomUUID() })
    await assert.rejects(() => owner.caller.deleteAsset({ id: image }), { code: 'CONFLICT' })
    await assert.rejects(() => owner.caller.deleteAsset({ id: font }), { code: 'CONFLICT' })
    await assert.rejects(() => h.adminPool.withOperator({ adminUserId: owner.id, label: owner.email }, (db) => db.execute(sql`UPDATE website_assets SET bytes = ${Buffer.from('mutated')} WHERE id = ${image}::uuid`)))
  })

  test('audit failure rolls back content, publication, history and refresh work together', async () => {
    await owner.caller.saveDraft({ document: changed('prepared'), expectedRevision: 0, requestId: randomUUID() })
    await h.admin.unsafe(`CREATE FUNCTION website_test_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action = 'website.published' THEN RAISE EXCEPTION 'test audit unavailable'; END IF; RETURN NEW; END $$`)
    await h.admin.unsafe('CREATE TRIGGER website_test_refuse_audit BEFORE INSERT ON admin_audit_entries FOR EACH ROW EXECUTE FUNCTION website_test_refuse_audit()')
    try {
      await assert.rejects(() => owner.caller.publish({ expectedRevision: 1, requestId: randomUUID() }), { code: 'INTERNAL_SERVER_ERROR' })
      assert.equal((await owner.caller.get()).publishedRevision, 0)
      assert.equal((await owner.caller.history({ limit: 10 })).items.length, 0)
      const [jobs] = await h.admin`SELECT count(*)::int AS n FROM website_refresh_jobs`
      assert.equal(jobs!.n, 0)
    } finally {
      await h.admin.unsafe('DROP TRIGGER website_test_refuse_audit ON admin_audit_entries')
      await h.admin.unsafe('DROP FUNCTION website_test_refuse_audit()')
    }
  })
})
