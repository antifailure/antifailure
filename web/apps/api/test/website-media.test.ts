import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHash, randomBytes, randomUUID } from 'node:crypto'
import { deflateSync } from 'node:zlib'
import { readFileSync } from 'node:fs'
import { Hono } from 'hono'
import { sql, type Pool } from '@antifailure/db'
import { FakeClock } from '../src/clock.ts'
import { inspectWebsiteMedia, mountWebsiteMedia, previewWebsiteAssetUrls, websiteByteRange, WEBSITE_MEDIA_LIMITS } from '../src/admin/website-media.ts'
import { adminCsrfTokenFor } from '../src/admin/session.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

function png(color = 42, paddingBytes = 0, compressed?: Buffer): Buffer {
  const chunk = (type: string, data: Buffer) => {
    const name = Buffer.from(type)
    let crc = 0xffffffff
    for (const byte of Buffer.concat([name, data])) {
      crc ^= byte
      for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0)
    }
    const length = Buffer.alloc(4), check = Buffer.alloc(4)
    length.writeUInt32BE(data.length); check.writeUInt32BE((crc ^ 0xffffffff) >>> 0)
    return Buffer.concat([length, name, data, check])
  }
  const header = Buffer.alloc(13)
  header.writeUInt32BE(1, 0); header.writeUInt32BE(1, 4); header[8] = 8; header[9] = 6
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', header), chunk('tEXt', Buffer.concat([Buffer.from(`fixture\0${randomUUID()}`), Buffer.alloc(paddingBytes, 65)])), chunk('IDAT', compressed ?? deflateSync(Buffer.from([0, color, color, color, 255]))), chunk('IEND', Buffer.alloc(0))])
}

const jpeg = Buffer.from('/9j/4AAQSkZJRgABAgAAAQABAAD//gAPTGF2YzYzLjEuMTAxAP/bAEMACAQEBAQEBQUFBQUFBgYGBgYGBgYGBgYGBgcHBwgICAcHBwYGBwcICAgICQkJCAgICAkJCgoKDAwLCw4ODhERFP/EAEsAAQEAAAAAAAAAAAAAAAAAAAAGAQEAAAAAAAAAAAAAAAAAAAAGEAEAAAAAAAAAAAAAAAAAAAAAEQEAAAAAAAAAAAAAAAAAAAAA/8AAEQgAEAAQAwEiAAIRAAMRAP/aAAwDAQACEQMRAD8AiwBQJf/Z', 'base64')
const gif = Buffer.from('R0lGODlhAQABAIAAAP///wAAACwAAAAAAQABAAACAkQBADs=', 'base64')
const webp = Buffer.from('UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA', 'base64')

describe('website media inspects file contents, dimensions and ranges', () => {
  it('recognizes real raster files and the checked-in WOFF2 font', () => {
    assert.deepEqual(inspectWebsiteMedia(png()), { kind: 'image', mimeType: 'image/png', width: 1, height: 1 })
    assert.deepEqual(inspectWebsiteMedia(jpeg), { kind: 'image', mimeType: 'image/jpeg', width: 16, height: 16 })
    assert.deepEqual(inspectWebsiteMedia(gif), { kind: 'image', mimeType: 'image/gif', width: 1, height: 1 })
    assert.deepEqual(inspectWebsiteMedia(webp), { kind: 'image', mimeType: 'image/webp', width: 1, height: 1 })
    assert.deepEqual(inspectWebsiteMedia(readFileSync(new URL('../../../../docs/public/fonts/Geist-Regular.woff2', import.meta.url))), { kind: 'font', mimeType: 'font/woff2', width: null, height: null })
  })

  it('refuses SVG, HTML, wrong MIME, incomplete files and excessive dimensions', () => {
    for (const bytes of [Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>'), Buffer.from('<!doctype html><script>alert(1)</script>'), Buffer.from('wOF2'), png().subarray(0, 40), jpeg.subarray(0, 35), gif.subarray(0, 15), webp.subarray(0, 15)]) assert.throws(() => inspectWebsiteMedia(bytes))
    assert.throws(() => inspectWebsiteMedia(png(), 'image/jpeg'), /does not match/)
    const giant = png(); giant.writeUInt32BE(12001, 16)
    assert.throws(() => inspectWebsiteMedia(giant), /12,000/)
    const corrupt = png(); corrupt[corrupt.indexOf('IDAT') + 4] = 0
    assert.throws(() => inspectWebsiteMedia(corrupt))
    const badCrc = png(); badCrc[29] = badCrc[29]! ^ 1
    assert.throws(() => inspectWebsiteMedia(badCrc))
    assert.throws(() => inspectWebsiteMedia(png(42, 0, Buffer.alloc(10))))
    assert.throws(() => inspectWebsiteMedia(png(42, 0, deflateSync(Buffer.from([9, 42, 42, 42, 255])))))
    const oversized = Buffer.concat([jpeg.subarray(0, jpeg.length - 2), Buffer.alloc(WEBSITE_MEDIA_LIMITS.image), jpeg.subarray(-2)])
    assert.throws(() => inspectWebsiteMedia(oversized), /too large/)
  })

  it('serves byte ranges, clamps the end and rejects invalid or multiple ranges', () => {
    assert.equal(websiteByteRange(undefined, 100), null)
    assert.deepEqual(websiteByteRange('bytes=0-9', 100), { start: 0, end: 9 })
    assert.deepEqual(websiteByteRange('bytes=95-', 100), { start: 95, end: 99 })
    assert.deepEqual(websiteByteRange('bytes=-5', 100), { start: 95, end: 99 })
    assert.deepEqual(websiteByteRange('bytes=95-1000', 100), { start: 95, end: 99 })
    for (const value of ['bytes=100-', 'bytes=9-2', 'bytes=-0', 'bytes=-', 'bytes=0-1,8-9', 'items=0-1', 'bytes=0-9007199254740999']) assert.throws(() => websiteByteRange(value, 100), RangeError)
  })

  it('refuses anonymous uploads before touching the body or database', async () => {
    const app = new Hono()
    const pool = { withoutTenant: () => { throw new Error('Database should not be read') } } as unknown as Pool
    mountWebsiteMedia(app, { pool, adminPool: null, clock: new FakeClock(), appBaseUrl: 'https://app.example.test', siteOrigins: ['https://example.test'] })
    const response = await app.request('/v1/admin/website/media', { method: 'POST', headers: { 'content-length': String(100 * 1024 * 1024) }, body: 'not a file' })
    assert.equal(response.status, 401)
  })

  it('rejects malformed media IDs before a database read, for an inert route probe', async () => {
    const app = new Hono()
    const pool = { withoutTenant: () => { throw new Error('Database should not be read') } } as unknown as Pool
    mountWebsiteMedia(app, { pool, adminPool: null, clock: new FakeClock(), appBaseUrl: 'https://app.example.test', siteOrigins: ['https://example.test'] })
    for (const method of ['GET', 'HEAD']) {
      const response = await app.request('/v1/website/media/not-a-uuid', { method })
      assert.equal(response.status, 400)
      assert.equal(await response.text(), '')
    }
  })
})

// A single encoded 16 by 16 frame in each browser video container.
const mp4 = Buffer.from('AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAAMUbW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAA+gAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAj90cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAA+gAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAABAAAAAQAAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAAPoAAAAAAABAAAAAAG3bWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAABAAAAAQABVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAABYm1pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAASJzdGJsAAAAvnN0c2QAAAAAAAAAAQAAAK5hdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAABAAEABIAAAASAAAAAAAAAABFExhdmM2My4xLjEwMSBsaWJ4MjY0AAAAAAAAAAAAAAAAGP//AAAANGF2Y0MBZAAK/+EAF2dkAAqs2V7ARAAAAwAEAAADAAg8SJZYAQAGaOvjyyLA/fj4AAAAABBwYXNwAAAAAQAAAAEAAAAUYnRydAAAAAAAABZQAAAAAAAAABhzdHRzAAAAAAAAAAEAAAABAABAAAAAABxzdHNjAAAAAAAAAAEAAAABAAAAAQAAAAEAAAAUc3RzegAAAAAAAALKAAAAAQAAABRzdGNvAAAAAAAAAAEAAANEAAAAYXVkdGEAAABZbWV0YQAAAAAAAAAhaGRscgAAAAAAAAAAbWRpcmFwcGwAAAAAAAAAAAAAAAAsaWxzdAAAACSpdG9vAAAAHGRhdGEAAAABAAAAAExhdmY2My4xLjEwMQAAAAhmcmVlAAAC0m1kYXQAAAKtBgX//6ncRem95tlIt5Ys2CDZI+7veDI2NCAtIGNvcmUgMTY1IHIzMjIyIGIzNTYwNWEgLSBILjI2NC9NUEVHLTQgQVZDIGNvZGVjIC0gQ29weWxlZnQgMjAwMy0yMDI1IC0gaHR0cDovL3d3dy52aWRlb2xhbi5vcmcveDI2NC5odG1sIC0gb3B0aW9uczogY2FiYWM9MSByZWY9MyBkZWJsb2NrPTE6MDowIGFuYWx5c2U9MHgzOjB4MTEzIG1lPWhleCBzdWJtZT03IHBzeT0xIHBzeV9yZD0xLjAwOjAuMDAgbWl4ZWRfcmVmPTEgbWVfcmFuZ2U9MTYgY2hyb21hX21lPTEgdHJlbGxpcz0xIDh4OGRjdD0xIGNxbT0wIGRlYWR6b25lPTIxLDExIGZhc3RfcHNraXA9MSBjaHJvbWFfcXBfb2Zmc2V0PS0yIHRocmVhZHM9MSBsb29rYWhlYWRfdGhyZWFkcz0xIHNsaWNlZF90aHJlYWRzPTAgbnI9MCBkZWNpbWF0ZT0xIGludGVybGFjZWQ9MCBibHVyYXlfY29tcGF0PTAgY29uc3RyYWluZWRfaW50cmE9MCBiZnJhbWVzPTMgYl9weXJhbWlkPTIgYl9hZGFwdD0xIGJfYmlhcz0wIGRpcmVjdD0xIHdlaWdodGI9MSBvcGVuX2dvcD0wIHdlaWdodHA9MiBrZXlpbnQ9MjUwIGtleWludF9taW49MSBzY2VuZWN1dD00MCBpbnRyYV9yZWZyZXNoPTAgcmNfbG9va2FoZWFkPTQwIHJjPWNyZiBtYnRyZWU9MSBjcmY9MjMuMCBxY29tcD0wLjYwIHFwbWluPTAgcXBtYXg9NjkgcXBzdGVwPTQgaXBfcmF0aW89MS40MCBhcT0xOjEuMDAAgAAAABVliIQAFf/+7M9+BTZo5i/CuhVjYc8=', 'base64')
const webm = Buffer.from('GkXfo59ChoEBQveBAULygQRC84EIQoKEd2VibUKHgQJChYECGFOAZwEAAAAAAAIAEU2bdLpNu4tTq4QVSalmU6yBoU27i1OrhBZUrmtTrIHWTbuMU6uEElTDZ1OsggEyTbuMU6uEHFO7a1OsggHq7AEAAAAAAABZAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAVSalmsCrXsYMPQkBNgIxMYXZmNjMuMS4xMDFXQYxMYXZmNjMuMS4xMDFEiYhAj0AAAAAAABZUrmvXrgEAAAAAAABO14EBc8WI7a10cHPaPBqcgQAitZyDdW5kiIEAhoVWX1ZQOYOBASPjg4Q7msoA4JCwgRC6gRCagQJVsIRVuYEBVe6BAOwBAAAAAAAAAgAAElTDZ/5zc59jwIBnyJlFo4dFTkNPREVSRIeMTGF2ZjYzLjEuMTAxc3PZY8CLY8WI7a10cHPaPBpnyKRFo4dFTkNPREVSRIeXTGF2YzYzLjEuMTAxIGxpYnZweC12cDlnyKFFo4hEVVJBVElPTkSHkzAwOjAwOjAxLjAwMDAwMDAwMAAfQ7Z1sOeBAKOrgQAAgIJJg0IAAPAA9gA4JBwYSgAAMGAAABC///cdr////u2f////zPoAABxTu2uRu4+zgQC3iveBAfGCAbXwgQM=', 'base64')

describe('website videos require a real video track and a complete container', () => {
  it('reads dimensions from MP4 and WebM video tracks', () => {
    assert.deepEqual(inspectWebsiteMedia(mp4), { kind: 'video', mimeType: 'video/mp4', width: 16, height: 16 })
    assert.deepEqual(inspectWebsiteMedia(webm), { kind: 'video', mimeType: 'video/webm', width: 16, height: 16 })
  })
  it('rejects truncated containers, an audio-only MP4 and Matroska labeled as WebM', () => {
    assert.throws(() => inspectWebsiteMedia(mp4.subarray(0, mp4.length - 20)))
    assert.throws(() => inspectWebsiteMedia(webm.subarray(0, webm.length - 20)))
    const audio = Buffer.from(mp4); audio.write('soun', audio.indexOf('vide'))
    assert.throws(() => inspectWebsiteMedia(audio))
    const wrongDoc = Buffer.from(webm); wrongDoc.write('mkv!', wrongDoc.indexOf('webm'))
    assert.throws(() => inspectWebsiteMedia(wrongDoc))
  })
})

const hasDatabase = await available()
describe('website media uploads and serves through real operator and application roles', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let ownerId: string
  let email: string
  let token: string
  let headers: Record<string, string>
  let app: Hono
  const content = png(73)
  let assetId: string

  before(async () => {
    h = await startApi({ appBaseUrl: 'https://app.example.test', siteOrigins: ['https://example.test', 'https://www.example.test'] })
    h.clock.advance(Date.now() - h.clock.now().getTime())
    email = `media-${randomUUID()}@example.test`
    const rows = await h.admin<{ id: string }[]>`INSERT INTO admin_users (email, name, role) VALUES (${email}, 'Website media owner', 'owner') RETURNING id`
    ownerId = rows[0]!.id
    token = randomBytes(32).toString('base64url')
    await h.admin`INSERT INTO admin_sessions (token_hash, admin_user_id, expires_at) VALUES (${createHash('sha256').update(token).digest()}, ${ownerId}, ${new Date(h.clock.now().getTime() + 3_600_000).toISOString()})`
    headers = { cookie: `__Host-af_admin_session=${token}`, 'x-antifailure-admin-csrf': adminCsrfTokenFor(token), origin: 'https://app.example.test', 'content-type': 'image/png', 'x-file-name': 'tiny%20diagram.png' }
    app = new Hono()
    mountWebsiteMedia(app, { pool: h.pool, adminPool: h.adminPool, clock: h.clock, appBaseUrl: 'https://app.example.test', siteOrigins: ['https://example.test', 'https://www.example.test'] })
  })
  after(async () => {
    if (!h) return
    await h.admin`DELETE FROM website_assets WHERE created_by = ${ownerId}`
    await h.admin`DELETE FROM admin_audit_entries WHERE admin_user_id = ${ownerId}`
    await h.admin`DELETE FROM admin_sessions WHERE admin_user_id = ${ownerId}`
    await h.admin`DELETE FROM admin_users WHERE id = ${ownerId}`
    await h.close()
  })

  const upload = (extra: Record<string, string> = {}, body: Buffer = content) => app.request('/v1/admin/website/media', { method: 'POST', headers: { ...headers, ...extra }, body: new Uint8Array(body) })

  it('requires CSRF, same origin and current owner role, then persists exactly once with an audit record', async () => {
    assert.equal((await upload({ 'x-antifailure-admin-csrf': '' })).status, 403)
    assert.equal((await upload({ origin: 'https://evil.example.test' })).status, 403)
    await h.admin`UPDATE admin_users SET role = 'support' WHERE id = ${ownerId}`
    assert.equal((await upload()).status, 403)
    await h.admin`UPDATE admin_users SET role = 'owner' WHERE id = ${ownerId}`
    assert.equal((await upload({ 'content-type': 'image/svg+xml' }, Buffer.from('<svg/>'))).status, 415)
    const uploaded = await upload()
    assert.equal(uploaded.status, 201, await uploaded.clone().text())
    const asset = await uploaded.json() as { id: string; isPublic: boolean; name: string; width: number }
    assetId = asset.id
    assert.equal(asset.isPublic, false)
    assert.equal(asset.name, 'tiny diagram.png')
    assert.equal(asset.width, 1)
    const duplicate = await upload()
    assert.equal((await duplicate.json() as { id: string }).id, assetId)
    const stored = await h.admin<{ bytes: Buffer }[]>`SELECT bytes FROM website_assets WHERE id = ${assetId}`
    assert.deepEqual(stored[0]!.bytes, content)
    const audit = await h.admin<{ action: string }[]>`SELECT action FROM admin_audit_entries WHERE admin_user_id = ${ownerId} AND target_id = ${assetId}`
    assert.equal(audit.filter((row) => row.action === 'website.asset.upload').length, 2)
  })

  it('hides drafts from the serving role and validates expiring, asset-bound preview capabilities', async () => {
    assert.ok(assetId)
    assert.equal((await app.request(`/v1/website/media/${assetId}`)).status, 404)
    const rows = await h.pool.withoutTenant((db) => db.execute(sql`SELECT id FROM website_assets WHERE id = ${assetId}::uuid`))
    assert.equal(rows.length, 0)
    const urls = await h.adminPool.withOperator({ adminUserId: ownerId, label: email }, (db) => previewWebsiteAssetUrls(db, [assetId], h.clock.now()))
    const url = urls[assetId]!
    const preview = await app.request(url)
    assert.equal(preview.status, 200)
    assert.equal(preview.headers.get('cache-control'), 'no-store')
    assert.deepEqual(Buffer.from(await preview.arrayBuffer()), content)
    await h.admin`UPDATE website_assets SET archived = true WHERE id = ${assetId}`
    assert.equal((await app.request(url)).status, 200)
    const archivedUrls = await h.adminPool.withOperator({ adminUserId: ownerId, label: email }, (db) => previewWebsiteAssetUrls(db, [assetId], h.clock.now()))
    assert.equal((await app.request(archivedUrls[assetId]!)).status, 200)
    await assert.rejects(() => h.pool.withoutTenant((db) => db.execute(sql`SELECT value FROM website_secrets`)))
    assert.equal((await app.request(url.replace(assetId, randomUUID()))).status, 404)
    assert.equal((await app.request(`${url}x`)).status, 404)
    h.clock.advance(15 * 60_000)
    assert.equal((await app.request(url)).status, 404)
  })

  it('serves published bytes, HEAD, ETag and ranges, including assets archived from the library', async () => {
    await h.admin`UPDATE website_assets SET is_public = true WHERE id = ${assetId}`
    const full = await app.request(`/v1/website/media/${assetId}`, { headers: { origin: 'https://www.example.test' } })
    assert.equal(full.status, 200)
    assert.equal(full.headers.get('content-type'), 'image/png')
    assert.equal(full.headers.get('cache-control'), 'public, max-age=31536000, immutable')
    assert.equal(full.headers.get('x-content-type-options'), 'nosniff')
    assert.equal(full.headers.get('access-control-allow-origin'), 'https://www.example.test')
    assert.deepEqual(Buffer.from(await full.arrayBuffer()), content)
    const head = await app.request(`/v1/website/media/${assetId}`, { method: 'HEAD' })
    assert.equal(head.status, 200)
    assert.equal(head.headers.get('content-length'), String(content.length))
    assert.equal((await head.arrayBuffer()).byteLength, 0)
    assert.equal((await app.request(`/v1/website/media/${assetId}`, { headers: { 'if-none-match': full.headers.get('etag')! } })).status, 304)
    const partial = await app.request(`/v1/website/media/${assetId}`, { headers: { range: 'bytes=2-9' } })
    assert.equal(partial.status, 206)
    assert.equal(partial.headers.get('content-range'), `bytes 2-9/${content.length}`)
    assert.deepEqual(Buffer.from(await partial.arrayBuffer()), content.subarray(2, 10))
    const unsatisfied = await app.request(`/v1/website/media/${assetId}`, { headers: { range: `bytes=${content.length}-` } })
    assert.equal(unsatisfied.status, 416)
    assert.equal(unsatisfied.headers.get('content-range'), `bytes */${content.length}`)
    await h.admin`UPDATE website_assets SET archived = true WHERE id = ${assetId}`
    assert.equal((await app.request(`/v1/website/media/${assetId}`)).status, 200)
  })

  it('uploads more than the generic body limit through the complete server and serves the same bytes', async () => {
    const large = png(83, 1024 * 1024 + 50)
    const response = await h.fetch('/v1/admin/website/media', { method: 'POST', headers, body: new Uint8Array(large) })
    assert.equal(response.status, 201, await response.clone().text())
    const asset = await response.json() as { id: string }
    const path = `/v1/website/media/${asset.id}`
    assert.equal((await h.fetch(path)).status, 404)
    const urls = await h.adminPool.withOperator({ adminUserId: ownerId, label: email }, (db) => previewWebsiteAssetUrls(db, [asset.id], h.clock.now()))
    const preview = await h.fetch(urls[asset.id]!, { headers: { range: 'bytes=0-15', origin: 'https://example.test' } })
    assert.equal(preview.status, 206)
    assert.equal(preview.headers.get('cache-control'), 'no-store')
    assert.deepEqual(Buffer.from(await preview.arrayBuffer()), large.subarray(0, 16))
    await h.admin`UPDATE website_assets SET is_public = true WHERE id = ${asset.id}`
    const full = await h.fetch(path, { headers: { origin: 'https://www.example.test' } })
    assert.equal(full.status, 200)
    assert.equal(full.headers.get('access-control-allow-origin'), 'https://www.example.test')
    assert.deepEqual(Buffer.from(await full.arrayBuffer()), large)
    const head = await h.fetch(path, { method: 'HEAD' })
    assert.equal(head.status, 200)
    assert.equal(head.headers.get('content-length'), String(large.length))
    assert.equal((await head.arrayBuffer()).byteLength, 0)
    const tail = await h.fetch(path, { headers: { range: 'bytes=-8' } })
    assert.equal(tail.status, 206)
    assert.deepEqual(Buffer.from(await tail.arrayBuffer()), large.subarray(-8))
    const preflight = await h.fetch(path, { method: 'OPTIONS', headers: { origin: 'https://www.example.test', 'access-control-request-method': 'GET', 'access-control-request-headers': 'range' } })
    assert.equal(preflight.status, 204)
    assert.equal(preflight.headers.get('access-control-allow-origin'), 'https://www.example.test')
    assert.equal((await h.fetch(path, { headers: { origin: 'https://evil.example.test' } })).headers.get('access-control-allow-origin'), null)
  })

  it('serializes concurrent uploads so only one can take the final library space', async () => {
    const first = png(93), second = png(94)
    const used = await h.admin<{ size: string }[]>`SELECT coalesce(sum(size_bytes), 0)::text AS size FROM website_assets`
    let remaining = 256 * 1024 * 1024 - Number(used[0]!.size) - Math.max(first.length, second.length)
    const fixtures: string[] = []
    try {
      // Compressible database fixtures avoid sending a 256 MB body through the test process.
      while (remaining > 0) {
        const length = Math.min(64 * 1024 * 1024, remaining)
        const id = randomUUID()
        fixtures.push(id)
        await h.admin`INSERT INTO website_assets (id, sha256, name, kind, mime_type, size_bytes, bytes, created_by)
          VALUES (${id}, ${createHash('sha256').update(id).digest('hex')}, 'Quota fixture', 'video', 'video/mp4', ${length}, convert_to(repeat('x', ${length}), 'UTF8'), ${ownerId})`
        remaining -= length
      }
      await h.admin`CREATE FUNCTION website_media_quota_test_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.05); RETURN NEW; END $$`
      await h.admin`CREATE TRIGGER website_media_quota_test_delay BEFORE INSERT ON website_assets FOR EACH ROW EXECUTE FUNCTION website_media_quota_test_delay()`
      const responses = await Promise.all([upload({}, first), upload({}, second)])
      assert.deepEqual(responses.map((response) => response.status).sort(), [201, 413])
      const total = await h.admin<{ size: string }[]>`SELECT sum(size_bytes)::text AS size FROM website_assets`
      assert.ok(Number(total[0]!.size) <= 256 * 1024 * 1024)
    } finally {
      await h.admin`DROP TRIGGER IF EXISTS website_media_quota_test_delay ON website_assets`
      await h.admin`DROP FUNCTION IF EXISTS website_media_quota_test_delay()`
      for (const id of fixtures) await h.admin`DELETE FROM website_assets WHERE id = ${id}`
    }
  })

  it('bounds buffered uploads and frees capacity after the active requests finish', async () => {
    function heldUpload() {
      let release!: () => void
      let started!: () => void
      const reading = new Promise<void>((resolve) => { started = resolve })
      const released = new Promise<void>((resolve) => { release = resolve })
      const bytes = png(111)
      const stream = new ReadableStream<Uint8Array>({ async pull(controller) {
        started()
        await released
        controller.enqueue(new Uint8Array(bytes))
        controller.close()
      } }, { highWaterMark: 0 })
      const response = app.request('/v1/admin/website/media', { method: 'POST', headers, body: stream, duplex: 'half' } as RequestInit & { duplex: 'half' })
      return { response, reading, release }
    }
    const first = heldUpload(), second = heldUpload()
    try {
      await Promise.all([first.reading, second.reading])
      const refused = await upload()
      assert.equal(refused.status, 429)
      assert.equal(refused.headers.get('retry-after'), '2')
    } finally {
      first.release(); second.release()
      const completed = await Promise.all([first.response, second.response])
      assert.deepEqual(completed.map((response) => response.status), [201, 201])
    }
    assert.equal((await upload()).status, 201)
  })
})
