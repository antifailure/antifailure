import { createHash, randomUUID } from 'node:crypto'
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { setTimeout as sleep } from 'node:timers/promises'
import { emptyWebsiteDocument, stableStringify, validateWebsiteDocument, WEBSITE_LIMITS } from '@antifailure/website'

const root = fileURLToPath(new URL('../', import.meta.url))
export const SNAPSHOT_PATH = resolve(root, 'lib/cms-snapshot.generated.json')
export const MARKER_PATH = resolve(root, 'public/cms-version.json')
export const MAX_SNAPSHOT_BYTES = WEBSITE_LIMITS.bytes + 4096

export function documentHash(document) {
  return createHash('sha256').update(stableStringify(document), 'utf8').digest('hex')
}

export function defaultSnapshot() {
  const document = emptyWebsiteDocument()
  return { revision: 0, contentHash: documentHash(document), document }
}

export function parseMarker(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)
    || !Number.isSafeInteger(value.revision) || value.revision < 0
    || typeof value.contentHash !== 'string' || !/^[a-f0-9]{64}$/.test(value.contentHash)) {
    throw new Error('The CMS revision marker is invalid.')
  }
  return { revision: value.revision, contentHash: value.contentHash }
}

export function parseSnapshot(value) {
  const marker = parseMarker(value)
  const result = validateWebsiteDocument(value.document)
  if (!result.ok) throw new Error(`The published CMS document is invalid at ${result.errors[0]?.path ?? 'document'}.`)
  if (documentHash(result.document) !== marker.contentHash) {
    throw new Error('The published CMS content does not match its content hash.')
  }
  return { ...marker, document: result.document }
}

function publicUrl(value) {
  const url = new URL(value)
  if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.hash) {
    throw new Error('The CMS public URL must be an HTTP URL without credentials or a fragment.')
  }
  return url
}

/** Read the decompressed stream with a byte ceiling, even when Content-Length
 * is absent or lies. A deadline covers both headers and the entire body. */
async function fetchJson(url, { fetchImpl = fetch, timeoutMs = 15_000, maxBytes = MAX_SNAPSHOT_BYTES } = {}) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(new Error('The CMS request timed out.')), timeoutMs)
  try {
    const response = await fetchImpl(publicUrl(url), {
      signal: controller.signal, redirect: 'error', cache: 'no-store',
      headers: { Accept: 'application/json', 'Cache-Control': 'no-cache' },
    })
    if (response.status !== 200) {
      await response.body?.cancel()
      return { status: response.status }
    }
    const length = response.headers.get('content-length')
    if (length !== null && (!/^\d+$/.test(length) || Number(length) > maxBytes)) {
      await response.body?.cancel()
      throw new Error('The CMS response exceeds the allowed size.')
    }
    if (!/^application\/json(?:\s*;|$)/i.test(response.headers.get('content-type') ?? '')) {
      await response.body?.cancel()
      throw new Error('The CMS endpoint did not return JSON.')
    }
    if (!response.body) throw new Error('The CMS endpoint returned an empty body.')
    const reader = response.body.getReader()
    const chunks = []
    let bytes = 0
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        bytes += value.byteLength
        if (bytes > maxBytes) {
          await reader.cancel()
          throw new Error('The CMS response exceeds the allowed size.')
        }
        chunks.push(value)
      }
    } finally { reader.releaseLock() }
    return { status: 200, value: JSON.parse(Buffer.concat(chunks).toString('utf8')) }
  } finally { clearTimeout(timer) }
}

export async function fetchPublishedSnapshot(url, options = {}) {
  const response = await fetchJson(url, options)
  if (response.status === 200) return parseSnapshot(response.value)
  if (response.status === 404 && options.allowBootstrap404 === true) {
    // Bootstrap is deliberately a manual, one-time escape hatch. An existing
    // marker means this site already shipped the CMS and must never fall back
    // to defaults because the API disappeared, even at revision zero.
    if (!options.liveMarkerUrl) throw new Error('CMS bootstrap requires the live site marker URL.')
    const marker = await fetchJson(options.liveMarkerUrl, { ...options, maxBytes: 1024 })
    if (marker.status !== 404) throw new Error('CMS bootstrap is refused because the site already has a CMS marker or its status is unknown.')
    return defaultSnapshot()
  }
  throw new Error(`The published CMS endpoint returned HTTP ${response.status}; no default content was substituted.`)
}

async function readSnapshot(path) {
  const body = await readFile(path)
  if (body.byteLength > MAX_SNAPSHOT_BYTES) throw new Error('The CMS snapshot file exceeds the allowed size.')
  return parseSnapshot(JSON.parse(body.toString('utf8')))
}

async function atomicJson(path, value) {
  await mkdir(dirname(path), { recursive: true })
  const temporary = `${path}.${randomUUID()}.tmp`
  await writeFile(temporary, `${stableStringify(value)}\n`, { mode: 0o600 })
  await rename(temporary, path)
}

/** Every normal local/CI build starts with code defaults. Only an explicit
 * published URL or local fixture can carry CMS content into a static build. */
export async function prepareSnapshot({ publishedUrl, fixturePath, snapshotPath = SNAPSHOT_PATH, markerPath = MARKER_PATH, ...options } = {}) {
  if (publishedUrl && fixturePath) throw new Error('Choose a published CMS URL or a snapshot fixture, not both.')
  const snapshot = publishedUrl ? await fetchPublishedSnapshot(publishedUrl, options)
    : fixturePath ? await readSnapshot(fixturePath) : defaultSnapshot()
  await atomicJson(snapshotPath, snapshot)
  await atomicJson(markerPath, parseMarker(snapshot))
  return snapshot
}

export function isSnapshotCurrent(built, latest) {
  const left = parseMarker(built)
  const right = parseMarker(latest)
  if (right.revision < left.revision) throw new Error('The published CMS revision moved backwards; refusing to upload.')
  if (right.revision === left.revision && right.contentHash !== left.contentHash) {
    throw new Error('The same CMS revision has two different content hashes; refusing to upload.')
  }
  return right.revision === left.revision
}

export async function checkCurrentSnapshot({ publishedUrl, snapshotPath = SNAPSHOT_PATH, ...options }) {
  if (!publishedUrl) throw new Error('Checking a deployment requires the published CMS URL.')
  const built = await readSnapshot(snapshotPath)
  const latest = await fetchPublishedSnapshot(publishedUrl, options)
  return { current: isSnapshotCurrent(built, latest), built, latest }
}

/** A successful upload is not proof the CDN serves the revision. */
export async function verifyLiveMarker({ liveMarkerUrl, markerPath = MARKER_PATH, attempts = 12, delayMs = 5000, wait = sleep, ...options }) {
  const expected = parseMarker(JSON.parse(await readFile(markerPath, 'utf8')))
  let reason = 'The live CMS marker has not been read.'
  for (let attempt = 0; attempt < attempts; attempt++) {
    try {
      const url = publicUrl(liveMarkerUrl)
      url.searchParams.set('cms_verify', `${expected.revision}-${randomUUID()}`)
      const response = await fetchJson(url, { ...options, maxBytes: 1024 })
      if (response.status !== 200) throw new Error(`The live CMS marker returned HTTP ${response.status}.`)
      const actual = parseMarker(response.value)
      if (actual.revision === expected.revision && actual.contentHash === expected.contentHash) return expected
      reason = `The live site serves CMS revision ${actual.revision}; this build requires revision ${expected.revision} with its exact content hash.`
    } catch (error) { reason = error instanceof Error ? error.message : 'The live CMS marker could not be read.' }
    if (attempt + 1 < attempts) await wait(delayMs)
  }
  throw new Error(reason)
}

export async function run(arguments_, env = process.env) {
  const options = {
    publishedUrl: env.CMS_PUBLISHED_URL,
    allowBootstrap404: env.CMS_ALLOW_BOOTSTRAP_404 === 'true',
    liveMarkerUrl: env.CMS_LIVE_MARKER_URL,
  }
  if (arguments_.length === 0) {
    const snapshot = await prepareSnapshot({ ...options, fixturePath: env.CMS_SNAPSHOT_FILE })
    console.log(`CMS build: revision ${snapshot.revision}, content ${snapshot.contentHash.slice(0, 12)}${options.publishedUrl ? '' : ' (local source or fixture)'}.`)
  } else if (arguments_.length === 1 && arguments_[0] === '--check-current') {
    const result = await checkCurrentSnapshot(options)
    if (env.GITHUB_OUTPUT) {
      const { appendFile } = await import('node:fs/promises')
      await appendFile(env.GITHUB_OUTPUT, `current=${result.current}\nrevision=${result.built.revision}\n`)
    }
    console.log(result.current ? `CMS revision ${result.built.revision} is still current.`
      : `::notice::CMS revision ${result.built.revision} was superseded by ${result.latest.revision}. This run will not upload stale content.`)
  } else if (arguments_.length === 1 && arguments_[0] === '--verify-live') {
    const marker = await verifyLiveMarker(options)
    console.log(`The live site serves CMS revision ${marker.revision} and its exact content hash.`)
  } else throw new Error('Usage: node scripts/cms-snapshot.mjs [--check-current|--verify-live]')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  run(process.argv.slice(2)).catch((error) => { console.error(error.message); process.exitCode = 1 })
}
