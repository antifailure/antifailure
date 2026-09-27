import { createHash, createHmac, randomBytes, randomUUID, timingSafeEqual } from 'node:crypto'
import { inflateSync } from 'node:zlib'
import { sql, type AdminPool, type Db, type Pool } from '@antifailure/db'
import type { Context, Env, Hono, MiddlewareHandler } from 'hono'
import type { Clock } from '../clock.ts'
import { matchSiteOrigin } from '../siteorigin.ts'
import { ADMIN_CSRF_HEADER, adminCsrfMatches, looksSameOrigin, readAdminSessionCookie, resolveAdminSession } from './session.ts'
import { actorOf, adminAudit, type AdminContext } from './trpc.ts'

const MiB = 1024 * 1024
export const WEBSITE_MEDIA_LIMITS = { image: 12 * MiB, video: 64 * MiB, font: 4 * MiB } as const
const LIBRARY_LIMIT = 256 * MiB
const PREVIEW_TTL_SECONDS = 15 * 60
const KEY_NAME = 'website_media_preview'
const SERVICE_ACTOR = { adminUserId: '00000000-0000-0000-0000-000000000000', label: 'website-media-capability' }
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const MIME_KIND = {
  'image/png': 'image', 'image/jpeg': 'image', 'image/webp': 'image', 'image/gif': 'image',
  'video/mp4': 'video', 'video/webm': 'video', 'font/woff2': 'font',
} as const
type MediaKind = 'image' | 'video' | 'font'
export interface WebsiteMediaInfo { kind: MediaKind; mimeType: string; width: number | null; height: number | null }
export interface WebsiteMediaDeps {
  pool: Pool
  adminPool: AdminPool | null
  clock: Clock
  appBaseUrl: string
  siteOrigins: readonly string[]
  secureCookies?: boolean
}

class MediaRefusal extends Error {
  status: 400 | 413 | 415
  constructor(message: string, status: 400 | 413 | 415 = 415) { super(message); this.status = status }
}
const invalid = () => new MediaRefusal('This file is incomplete or unsupported. Choose a PNG, JPEG, WebP, GIF, MP4, WebM, or WOFF2 file.')
const CRC_TABLE = Uint32Array.from({ length: 256 }, (_, byte) => {
  let crc = byte
  for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0)
  return crc >>> 0
})
function crc32(data: Buffer): number {
  let crc = 0xffffffff
  for (const byte of data) crc = (crc >>> 8) ^ CRC_TABLE[(crc ^ byte) & 255]!
  return (crc ^ 0xffffffff) >>> 0
}

function dimensions(width: number, height: number): { width: number; height: number } {
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1 || width > 12000 || height > 12000 || width * height > 40_000_000) {
    throw new MediaRefusal('Use media no larger than 12,000 pixels on either side or 40 megapixels.', 400)
  }
  return { width, height }
}

function png(data: Buffer): WebsiteMediaInfo {
  if (data.length < 57 || data.readUInt32BE(8) !== 13 || data.toString('ascii', 12, 16) !== 'IHDR') throw invalid()
  const size = dimensions(data.readUInt32BE(16), data.readUInt32BE(20))
  const depth = data[24]!, color = data[25]!, interlace = data[28]!
  const channels = new Map([[0, 1], [2, 3], [3, 1], [4, 2], [6, 4]]).get(color)
  const depths = color === 0 ? [1, 2, 4, 8, 16] : color === 3 ? [1, 2, 4, 8] : [8, 16]
  if (!channels || !depths.includes(depth) || data[26] !== 0 || data[27] !== 0 || interlace > 1) throw invalid()
  let offset = 8
  let pixels = false
  let ended = false
  let palette = false
  const compressed: Buffer[] = []
  while (offset + 12 <= data.length) {
    const length = data.readUInt32BE(offset)
    const end = offset + length + 12
    if (end > data.length) throw invalid()
    const type = data.toString('ascii', offset + 4, offset + 8)
    if (crc32(data.subarray(offset + 4, end - 4)) !== data.readUInt32BE(end - 4)) throw invalid()
    if (type === 'IHDR' && offset !== 8) throw invalid()
    if (type === 'PLTE') {
      if (length < 3 || length > 768 || length % 3 !== 0 || pixels) throw invalid()
      palette = true
    }
    if (type === 'IDAT' && length > 0) { pixels = true; compressed.push(data.subarray(offset + 8, end - 4)) }
    if (type === 'IEND') { if (length !== 0 || end !== data.length) throw invalid(); ended = true; break }
    offset = end
  }
  if (!pixels || !ended || (color === 3 && !palette)) throw invalid()
  const passes = interlace ? [[0, 0, 8, 8], [4, 0, 8, 8], [0, 4, 4, 8], [2, 0, 4, 4], [0, 2, 2, 4], [1, 0, 2, 2], [0, 1, 1, 2]] : [[0, 0, 1, 1]]
  const scans = passes.flatMap(([x, y, dx, dy]) => {
    const width = Math.max(0, Math.ceil((size.width - x!) / dx!))
    const rows = Math.max(0, Math.ceil((size.height - y!) / dy!))
    return width && rows ? [{ stride: 1 + Math.ceil(width * channels * depth / 8), rows }] : []
  })
  const expanded = scans.reduce((sum, pass) => sum + pass.stride * pass.rows, 0)
  if (expanded > 64 * MiB) throw new MediaRefusal('This PNG needs too much memory to decode. Resize it before uploading.', 400)
  let decoded: Buffer
  try { decoded = inflateSync(Buffer.concat(compressed), { maxOutputLength: expanded }) } catch { throw invalid() }
  if (decoded.length !== expanded) throw invalid()
  let scanline = 0
  for (const pass of scans) for (let row = 0; row < pass.rows; row++) {
    if (decoded[scanline]! > 4) throw invalid()
    scanline += pass.stride
  }
  return { kind: 'image', mimeType: 'image/png', ...size }
}

function jpeg(data: Buffer): WebsiteMediaInfo {
  let offset = 2
  let size: { width: number; height: number } | undefined
  while (offset + 4 <= data.length) {
    if (data[offset] !== 0xff) throw invalid()
    while (data[offset] === 0xff) offset++
    const marker = data[offset++]!
    if (marker === 0xd9 || marker === 0xda) break
    if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd7)) continue
    const length = data.readUInt16BE(offset)
    if (length < 2 || offset + length > data.length) throw invalid()
    if ([0xc0, 0xc1, 0xc2].includes(marker)) {
      if (length < 8) throw invalid()
      size = dimensions(data.readUInt16BE(offset + 5), data.readUInt16BE(offset + 3))
    }
    if (marker === 0xdc) throw invalid()
    offset += length
  }
  if (!size || data.length < offset + 4 || data.readUInt16BE(data.length - 2) !== 0xffd9) throw invalid()
  return { kind: 'image', mimeType: 'image/jpeg', ...size }
}

function gif(data: Buffer): WebsiteMediaInfo {
  if (data.length < 20 || data[data.length - 1] !== 0x3b) throw invalid()
  const size = dimensions(data.readUInt16LE(6), data.readUInt16LE(8))
  let offset = 13 + (data[10]! & 0x80 ? 3 * (1 << ((data[10]! & 7) + 1)) : 0)
  let pixels = false
  const subBlocks = () => {
    while (offset < data.length) {
      const length = data[offset++]!
      if (length === 0) return
      offset += length
      if (offset > data.length) throw invalid()
    }
    throw invalid()
  }
  while (offset < data.length) {
    const marker = data[offset++]!
    if (marker === 0x3b) { if (offset !== data.length || !pixels) throw invalid(); break }
    if (marker === 0x21) { offset++; subBlocks(); continue }
    if (marker !== 0x2c || offset + 9 >= data.length) throw invalid()
    const left = data.readUInt16LE(offset), top = data.readUInt16LE(offset + 2)
    const width = data.readUInt16LE(offset + 4), height = data.readUInt16LE(offset + 6)
    if (width < 1 || height < 1 || left + width > size.width || top + height > size.height) throw invalid()
    const packed = data[offset + 8]!
    offset += 9 + (packed & 0x80 ? 3 * (1 << ((packed & 7) + 1)) : 0)
    if (offset >= data.length || data[offset]! < 2 || data[offset]! > 8) throw invalid()
    offset++
    subBlocks()
    pixels = true
  }
  if (!pixels) throw invalid()
  return { kind: 'image', mimeType: 'image/gif', ...size }
}

function webp(data: Buffer): WebsiteMediaInfo {
  if (data.length < 26 || data.readUInt32LE(4) + 8 !== data.length) throw invalid()
  let offset = 12
  let size: { width: number; height: number } | undefined
  let pixels = false
  while (offset + 8 <= data.length) {
    const type = data.toString('ascii', offset, offset + 4)
    const length = data.readUInt32LE(offset + 4)
    const start = offset + 8
    const end = start + length
    if (end > data.length) throw invalid()
    if (type === 'VP8X' && length === 10) size = dimensions(1 + data.readUIntLE(start + 4, 3), 1 + data.readUIntLE(start + 7, 3))
    if (type === 'VP8 ' && length >= 10 && data.readUIntLE(start + 3, 3) === 0x2a019d) {
      size ??= dimensions(data.readUInt16LE(start + 6) & 0x3fff, data.readUInt16LE(start + 8) & 0x3fff)
      pixels = true
    }
    if (type === 'VP8L' && length >= 5 && data[start] === 0x2f) {
      const bits = data.readUInt32LE(start + 1)
      size ??= dimensions((bits & 0x3fff) + 1, ((bits >>> 14) & 0x3fff) + 1)
      pixels = true
    }
    if (type === 'ANMF' && length > 24 && size) pixels = true
    offset = end + (length % 2)
  }
  if (!size || !pixels || offset !== data.length) throw invalid()
  return { kind: 'image', mimeType: 'image/webp', ...size }
}

interface Box { type: string; start: number; end: number }
function boxes(data: Buffer, start: number, end: number): Box[] {
  const result: Box[] = []
  for (let offset = start; offset < end;) {
    if (offset + 8 > end) throw invalid()
    let length = data.readUInt32BE(offset)
    let header = 8
    if (length === 1) {
      if (offset + 16 > end) throw invalid()
      const large = data.readBigUInt64BE(offset + 8)
      if (large > BigInt(data.length)) throw invalid()
      length = Number(large); header = 16
    } else if (length === 0) length = end - offset
    if (length < header || offset + length > end) throw invalid()
    result.push({ type: data.toString('ascii', offset + 4, offset + 8), start: offset + header, end: offset + length })
    if (result.length > 10000) throw invalid()
    offset += length
  }
  return result
}

function mp4(data: Buffer): WebsiteMediaInfo {
  const top = boxes(data, 0, data.length)
  const ftyp = top.find((box) => box.type === 'ftyp')
  const moov = top.find((box) => box.type === 'moov')
  if (!ftyp || ftyp.end - ftyp.start < 8 || !moov || !top.some((box) => box.type === 'mdat' && box.end > box.start)) throw invalid()
  for (const track of boxes(data, moov.start, moov.end).filter((box) => box.type === 'trak')) {
    const children = boxes(data, track.start, track.end)
    const tkhd = children.find((box) => box.type === 'tkhd')
    const mdia = children.find((box) => box.type === 'mdia')
    const handler = mdia && boxes(data, mdia.start, mdia.end).find((box) => box.type === 'hdlr')
    if (!tkhd || !handler || handler.end - handler.start < 12 || data.toString('ascii', handler.start + 8, handler.start + 12) !== 'vide') continue
    const version = data[tkhd.start]
    const expected = version === 0 ? 84 : version === 1 ? 96 : 0
    if (!expected || tkhd.end - tkhd.start < expected) throw invalid()
    const size = dimensions(Math.round(data.readUInt32BE(tkhd.start + expected - 8) / 65536), Math.round(data.readUInt32BE(tkhd.start + expected - 4) / 65536))
    return { kind: 'video', mimeType: 'video/mp4', ...size }
  }
  throw invalid()
}

interface Ebml { id: number; start: number; end: number }
function ebml(data: Buffer, start: number, end: number): Ebml[] {
  const result: Ebml[] = []
  const integer = (offset: number, isSize: boolean) => {
    if (offset >= end || data[offset] === 0) throw invalid()
    let length = 1
    for (let mask = 0x80; !(data[offset]! & mask); mask >>= 1) length++
    if (length > (isSize ? 8 : 4) || offset + length > end) throw invalid()
    let value = BigInt(isSize ? data[offset]! & (0xff >> length) : data[offset]!)
    for (let i = 1; i < length; i++) value = (value << 8n) | BigInt(data[offset + i]!)
    const unknown = isSize && value === (1n << BigInt(length * 7)) - 1n
    if (!unknown && value > BigInt(Number.MAX_SAFE_INTEGER)) throw invalid()
    return { value: Number(value), length, unknown }
  }
  for (let offset = start; offset < end;) {
    const id = integer(offset, false)
    const size = integer(offset + id.length, true)
    const content = offset + id.length + size.length
    const finish = size.unknown ? end : content + size.value
    if (finish > end || finish < content) throw invalid()
    result.push({ id: id.value, start: content, end: finish })
    if (result.length > 10000) throw invalid()
    offset = finish
  }
  return result
}
function webm(data: Buffer): WebsiteMediaInfo {
  const root = ebml(data, 0, data.length)
  const header = root.find((element) => element.id === 0x1a45dfa3)
  const segment = root.find((element) => element.id === 0x18538067)
  if (!header || !segment) throw invalid()
  const docType = ebml(data, header.start, header.end).find((element) => element.id === 0x4282)
  if (!docType || data.toString('ascii', docType.start, docType.end) !== 'webm') throw invalid()
  const elements = ebml(data, segment.start, segment.end)
  const tracks = elements.find((element) => element.id === 0x1654ae6b)
  if (!tracks || !elements.some((element) => element.id === 0x1f43b675 && element.end > element.start)) throw invalid()
  for (const entry of ebml(data, tracks.start, tracks.end).filter((element) => element.id === 0xae)) {
    const video = ebml(data, entry.start, entry.end).find((element) => element.id === 0xe0)
    if (!video) continue
    const fields = ebml(data, video.start, video.end)
    const width = fields.find((element) => element.id === 0xb0)
    const height = fields.find((element) => element.id === 0xba)
    if (!width || !height || width.end - width.start > 4 || height.end - height.start > 4 || width.start === width.end || height.start === height.end) throw invalid()
    return { kind: 'video', mimeType: 'video/webm', ...dimensions(data.readUIntBE(width.start, width.end - width.start), data.readUIntBE(height.start, height.end - height.start)) }
  }
  throw invalid()
}

/** Validate file structure and dimensions before storing bytes. Filename and browser MIME are never trusted as detection. */
function inspectMediaStructure(data: Buffer, declaredMime?: string): WebsiteMediaInfo {
  if (!data.length) throw invalid()
  let info: WebsiteMediaInfo
  if (data.length >= 8 && data.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) info = png(data)
  else if (data.length >= 4 && data.readUInt16BE(0) === 0xffd8) info = jpeg(data)
  else if (/^GIF8[79]a$/.test(data.toString('ascii', 0, 6))) info = gif(data)
  else if (data.toString('ascii', 0, 4) === 'RIFF' && data.toString('ascii', 8, 12) === 'WEBP') info = webp(data)
  else if (data.length >= 12 && data.toString('ascii', 4, 8) === 'ftyp') info = mp4(data)
  else if (data.length >= 4 && data.readUInt32BE(0) === 0x1a45dfa3) info = webm(data)
  else if (data.toString('ascii', 0, 4) === 'wOF2') {
    if (data.length < 49 || data.readUInt32BE(8) !== data.length || data.readUInt16BE(12) < 1 || data.readUInt16BE(12) > 256 || data.readUInt16BE(14) !== 0 || data.readUInt32BE(16) > 32 * MiB || data.readUInt32BE(16) < 12 || data.readUInt32BE(20) < 1 || data.readUInt32BE(20) > data.length - 48) throw invalid()
    const flavor = data.readUInt32BE(4)
    if (flavor !== 0x00010000 && flavor !== 0x4f54544f) throw invalid()
    info = { kind: 'font', mimeType: 'font/woff2', width: null, height: null }
  } else throw invalid()
  if (declaredMime && declaredMime !== 'application/octet-stream' && declaredMime !== info.mimeType) throw new MediaRefusal('The file content does not match its media type.')
  if (data.length > WEBSITE_MEDIA_LIMITS[info.kind]) throw new MediaRefusal(`This ${info.kind} is too large. The limit is ${WEBSITE_MEDIA_LIMITS[info.kind] / MiB} MB.`, 413)
  return info
}

export function inspectWebsiteMedia(data: Buffer, declaredMime?: string): WebsiteMediaInfo {
  try { return inspectMediaStructure(data, declaredMime) }
  catch (error) {
    if (error instanceof MediaRefusal) throw error
    throw invalid()
  }
}

function signature(key: Buffer, id: string, expires: number): string {
  return createHmac('sha256', key).update(`website-asset\n${id}\n${expires}`).digest('base64url')
}
function validCapability(key: Buffer, id: string, expiresValue: string | undefined, signed: string | undefined, now: Date): boolean {
  if (!expiresValue || !/^\d{10}$/.test(expiresValue) || !signed || !/^[A-Za-z0-9_-]{43}$/.test(signed)) return false
  const expires = Number(expiresValue)
  const seconds = Math.floor(now.getTime() / 1000)
  if (expires <= seconds || expires > seconds + PREVIEW_TTL_SECONDS) return false
  const expected = Buffer.from(signature(key, id, expires))
  const actual = Buffer.from(signed)
  return expected.length === actual.length && timingSafeEqual(expected, actual)
}

export async function previewWebsiteAssetUrls(db: Db, ids: readonly string[], now: Date): Promise<Record<string, string>> {
  const unique = [...new Set(ids)]
  if (unique.length === 0) return {}
  if (unique.length > 256 || unique.some((id) => !UUID.test(id))) throw new Error('Choose valid website assets.')
  const assets = await db.execute<{ id: string; is_public: boolean }>(sql`
    SELECT id, is_public FROM website_assets
    WHERE id IN (${sql.join(unique.map((id) => sql`${id}::uuid`), sql`, `)})`)
  if (assets.length !== unique.length) throw new Error('One or more website assets are unavailable.')
  await db.execute(sql`INSERT INTO website_secrets (key, value) VALUES (${KEY_NAME}, ${randomBytes(32)}) ON CONFLICT (key) DO NOTHING`)
  const secrets = await db.execute<{ value: Buffer }>(sql`SELECT value FROM website_secrets WHERE key = ${KEY_NAME}`)
  const key = secrets[0]?.value
  if (!key || key.length !== 32) throw new Error('Website preview signing is unavailable.')
  const expires = Math.floor(now.getTime() / 1000) + PREVIEW_TTL_SECONDS
  return Object.fromEntries(assets.map((asset) => [asset.id, asset.is_public
    ? `/v1/website/media/${asset.id}`
    : `/v1/website/media/${asset.id}?expires=${expires}&signature=${signature(key, asset.id, expires)}`]))
}

export function websiteByteRange(value: string | undefined, size: number): { start: number; end: number } | null {
  if (!value) return null
  const match = /^bytes=(\d*)-(\d*)$/.exec(value)
  if (!match || (!match[1] && !match[2])) throw new RangeError('Invalid range')
  let start: number, end: number
  if (!match[1]) {
    const suffix = Number(match[2])
    if (!Number.isSafeInteger(suffix) || suffix < 1) throw new RangeError('Invalid range')
    start = Math.max(0, size - suffix); end = size - 1
  } else {
    start = Number(match[1]); end = match[2] ? Number(match[2]) : size - 1
    if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start >= size || start > end) throw new RangeError('Invalid range')
    end = Math.min(end, size - 1)
  }
  return { start, end }
}

async function boundedBody(request: Request, max: number): Promise<Buffer> {
  const length = request.headers.get('content-length')
  if (length && (!/^\d+$/.test(length) || Number(length) > max)) throw new MediaRefusal('This upload is too large.', 413)
  if (!request.body) throw invalid()
  const reader = request.body.getReader()
  const chunks: Buffer[] = []
  let total = 0
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      total += value.byteLength
      if (total > max) { await reader.cancel(); throw new MediaRefusal('This upload is too large.', 413) }
      chunks.push(Buffer.from(value))
    }
  } finally { reader.releaseLock() }
  return Buffer.concat(chunks, total)
}

type AssetRow = {
  id: string; name: string; kind: MediaKind; mime_type: string; size_bytes: number; width: number | null; height: number | null
  sha256: string; is_public: boolean; archived: boolean; created_at: Date | string
}
function assetResponse(row: AssetRow) {
  return { id: row.id, name: row.name, kind: row.kind, mimeType: row.mime_type, sizeBytes: Number(row.size_bytes), width: row.width, height: row.height,
    isPublic: row.is_public, archived: row.archived, createdAt: new Date(row.created_at).toISOString() }
}

async function authorizeUpload<E extends Env>(c: Context<E>, deps: WebsiteMediaDeps) {
  c.header('cache-control', 'no-store')
  const token = readAdminSessionCookie(c.req.header('cookie'))
  if (!token) return c.json({ error: 'Sign in to the operator portal.' }, 401)
  const operator = await resolveAdminSession(deps.pool, token, deps.clock.now())
  if (!operator) return c.json({ error: 'Sign in to the operator portal.' }, 401)
  if (operator.impersonating || operator.role !== 'owner') return c.json({ error: 'Only an owner can upload website media.' }, 403)
  if (!looksSameOrigin({ origin: c.req.header('origin') ?? null, secFetchSite: c.req.header('sec-fetch-site') ?? null }, deps.appBaseUrl)) return c.json({ error: 'This operator request came from another site.' }, 403)
  if (!adminCsrfMatches(token, c.req.header(ADMIN_CSRF_HEADER))) return c.json({ error: `This operator request needs the ${ADMIN_CSRF_HEADER} header.` }, 403)
  if (!deps.adminPool) return c.json({ error: 'Website media storage is unavailable.' }, 503)
  return operator
}

/** Mounted before any generic body middleware. The route repeats this check so standalone mounts stay protected. */
export function websiteMediaUploadGuard<E extends Env>(deps: WebsiteMediaDeps): MiddlewareHandler<E> {
  return async (c, next) => {
    if (c.req.method !== 'POST') return next()
    const result = await authorizeUpload(c, deps)
    if (result instanceof Response) return result
    await next()
  }
}

export function mountWebsiteMedia<E extends Env>(app: Hono<E>, deps: WebsiteMediaDeps): void {
  const origins = [...deps.siteOrigins]
  let uploadsInFlight = 0
  try { origins.push(new URL(deps.appBaseUrl).origin) } catch { /* Self-hosted instances may omit a console origin. */ }
  app.post('/v1/admin/website/media', async (c) => {
    const operator = await authorizeUpload(c, deps)
    if (operator instanceof Response) return operator
    const mime = (c.req.header('content-type') ?? 'application/octet-stream').split(';')[0]!.trim().toLowerCase()
    const kind = MIME_KIND[mime as keyof typeof MIME_KIND]
    if (!kind && mime !== 'application/octet-stream') return c.json({ error: 'Choose a PNG, JPEG, WebP, GIF, MP4, WebM, or WOFF2 file.' }, 415)
    let name: string
    try { name = decodeURIComponent(c.req.header('x-file-name') ?? 'upload').normalize('NFC').replace(/[\u0000-\u001f\u007f/\\]/g, '').trim().slice(0, 180) || 'upload' }
    catch { return c.json({ error: 'The filename is invalid.' }, 400) }
    if (uploadsInFlight >= 2) {
      c.header('retry-after', '2')
      return c.json({ error: 'Two uploads are already in progress. Try again in a moment.' }, 429)
    }
    uploadsInFlight++
    try {
      const bytes = await boundedBody(c.req.raw, kind ? WEBSITE_MEDIA_LIMITS[kind] : WEBSITE_MEDIA_LIMITS.video)
      const info = inspectWebsiteMedia(bytes, mime)
      const sha = createHash('sha256').update(bytes).digest('hex')
      const asset = await deps.adminPool!.withOperator({ adminUserId: operator.adminUserId, label: operator.email }, async (db) => {
        await db.execute(sql`SELECT pg_advisory_xact_lock(87321051)`)
        const existing = await db.execute<AssetRow>(sql`SELECT id, name, kind, mime_type, size_bytes, width, height, sha256, is_public, archived, created_at FROM website_assets WHERE sha256 = ${sha} FOR UPDATE`)
        let row = existing[0]
        if (row) {
          if (row.archived) { await db.execute(sql`UPDATE website_assets SET archived = false WHERE id = ${row.id}::uuid`); row.archived = false }
        } else {
          const usage = await db.execute<{ used: string }>(sql`SELECT coalesce(sum(size_bytes), 0)::text AS used FROM website_assets`)
          if (Number(usage[0]?.used ?? 0) + bytes.length > LIBRARY_LIMIT) throw new MediaRefusal('The media library has reached its 256 MB limit.', 413)
          const inserted = await db.execute<AssetRow>(sql`
            INSERT INTO website_assets (id, sha256, name, kind, mime_type, size_bytes, width, height, bytes, is_public, archived, created_at, created_by)
            VALUES (${randomUUID()}::uuid, ${sha}, ${name}, ${info.kind}, ${info.mimeType}, ${bytes.length}, ${info.width}, ${info.height}, ${bytes}, false, false, ${deps.clock.now().toISOString()}::timestamptz, ${operator.adminUserId}::uuid)
            RETURNING id, name, kind, mime_type, size_bytes, width, height, sha256, is_public, archived, created_at`)
          row = inserted[0]!
        }
        const auditContext = { admin: actorOf(operator), clock: deps.clock, ip: undefined } as AdminContext
        await adminAudit(db, auditContext, { action: 'website.asset.upload', targetType: 'website_asset', targetId: row.id, severity: 'notice', detail: { kind: info.kind, sizeBytes: bytes.length, reused: existing.length > 0 }, tenantCopy: false })
        return assetResponse(row)
      })
      return c.json(asset, 201)
    } catch (error) {
      if (error instanceof MediaRefusal) return c.json({ error: error.message }, error.status)
      return c.json({ error: 'The upload could not be saved. Try again.' }, 503)
    } finally { uploadsInFlight-- }
  })

  app.options('/v1/website/media/:id', (c) => {
    c.header('vary', 'origin')
    const origin = matchSiteOrigin(c.req.header('origin'), origins)
    if (!origin) return c.body(null, 403)
    c.header('access-control-allow-origin', origin)
    c.header('access-control-allow-methods', 'GET, HEAD, OPTIONS')
    c.header('access-control-allow-headers', 'Range, If-Range, If-None-Match')
    c.header('access-control-max-age', '600')
    return c.body(null, 204)
  })

  const serve = async (c: Context<E>) => {
    c.header('x-content-type-options', 'nosniff')
    c.header('cache-control', 'no-store')
    c.header('referrer-policy', 'no-referrer')
    c.header('vary', 'origin')
    const origin = matchSiteOrigin(c.req.header('origin'), origins)
    if (origin) c.header('access-control-allow-origin', origin)
    const id = c.req.param('id')
    if (!id || !UUID.test(id)) return c.body(null, 400)
    const expires = c.req.query('expires'), signed = c.req.query('signature')
    const capability = expires !== undefined || signed !== undefined
    const publicQuery = sql`SELECT id, mime_type, size_bytes, sha256, is_public FROM website_assets WHERE id = ${id}::uuid AND is_public = true`
    try {
      let privileged = false
      if (capability) {
        if (!deps.adminPool) return c.body(null, 404)
        const valid = await deps.adminPool.withOperator(SERVICE_ACTOR, async (db) => {
          const keys = await db.execute<{ value: Buffer }>(sql`SELECT value FROM website_secrets WHERE key = ${KEY_NAME}`)
          return keys[0]?.value.length === 32 && validCapability(keys[0].value, id, expires, signed, deps.clock.now())
        })
        if (!valid) return c.body(null, 404)
        privileged = true
      }
      const read = <T,>(fn: (db: Db) => Promise<T>): Promise<T> => privileged
        ? deps.adminPool!.withOperator(SERVICE_ACTOR, fn)
        : deps.pool.withoutTenant(fn)
      const rows = await read((db) => db.execute<Pick<AssetRow, 'id' | 'mime_type' | 'size_bytes' | 'sha256' | 'is_public'>>(privileged
        ? sql`SELECT id, mime_type, size_bytes, sha256, is_public FROM website_assets WHERE id = ${id}::uuid`
        : publicQuery))
      const asset = rows[0]
      if (!asset || !(asset.mime_type in MIME_KIND)) return c.body(null, 404)
      const size = Number(asset.size_bytes)
      const etag = `"${asset.sha256}"`
      c.header('content-type', asset.mime_type)
      c.header('content-security-policy', "default-src 'none'; sandbox")
      c.header('content-disposition', `inline; filename="${asset.id}.${asset.mime_type.split('/')[1]}"`)
      c.header('accept-ranges', 'bytes')
      c.header('etag', etag)
      if (!privileged) {
        c.header('cache-control', 'public, max-age=31536000, immutable')
      }
      if (!c.req.header('range') && c.req.header('if-none-match')?.split(/\s*,\s*/).some((value) => value === etag || value === '*')) return c.body(null, 304)
      let range: { start: number; end: number } | null = null
      try { range = websiteByteRange(c.req.header('if-range') && c.req.header('if-range') !== etag ? undefined : c.req.header('range'), size) }
      catch { c.header('content-range', `bytes */${size}`); return c.body(null, 416) }
      const start = range?.start ?? 0, end = range?.end ?? size - 1
      c.header('content-length', String(end - start + 1))
      if (range) c.header('content-range', `bytes ${start}-${end}/${size}`)
      if (c.req.method === 'HEAD') return c.body(null, range ? 206 : 200)
      const found = await read((db) => db.execute<{ bytes: Buffer }>(sql`
        SELECT substring(bytes FROM ${start + 1} FOR ${end - start + 1}) AS bytes
        FROM website_assets WHERE id = ${id}::uuid AND ${privileged ? sql`true` : sql`is_public = true`}`))
      if (!found[0]) { c.header('content-length', '0'); return c.body(null, 404) }
      return new Response(new Uint8Array(found[0].bytes), { status: range ? 206 : 200, headers: c.res.headers })
    } catch {
      c.header('content-length', undefined)
      c.header('content-range', undefined)
      c.header('cache-control', 'no-store')
      return c.body(null, 503)
    }
  }
  app.on(['GET', 'HEAD'], '/v1/website/media/:id', serve)
}
