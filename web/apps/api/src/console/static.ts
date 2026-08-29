// Serving the console's build.
//
// The console is a Next.js static export -- HTML, CSS, JS and fonts, no server
// -- and this hands those files out from the control plane's own process. That
// is not a packaging convenience. The session is a SameSite=Lax cookie on this
// origin, so a console served from anywhere else would need SameSite=None and
// credentialed CORS, which widens the cross-site surface of every endpoint on
// this API in order to move a dashboard to a second hostname.
//
// What this does NOT do is guess. If the build is missing, every request that
// would have been a page says so in one sentence and the process logs it at
// start-up, because an empty directory silently answering 404 looks exactly
// like a routing bug and is not one.

import { createHash } from 'node:crypto'
import { readFile, stat } from 'node:fs/promises'
import { join, normalize, resolve, sep } from 'node:path'

export interface ConsoleBuild {
  /** Absolute path of the exported directory, whether or not it exists. */
  readonly dir: string
  readonly present: boolean
  /** One line for the start-up log, said whichever way it went. */
  readonly summary: string
}

/**
 * Where the export is.
 *
 * AF_CONSOLE_DIR wins, then the layout the image builds: /app/console-out
 * beside /app/apps/api. Resolved once at start-up rather than per request, so
 * a misconfiguration is one log line instead of a per-request stat.
 */
export async function findConsoleBuild(dir?: string): Promise<ConsoleBuild> {
  const target = resolve(dir ?? process.env.AF_CONSOLE_DIR ?? defaultDir())
  try {
    const entry = await stat(join(target, 'index.html'))
    if (!entry.isFile()) throw new Error('not a file')
    return { dir: target, present: true, summary: `console build served from ${target}` }
  } catch {
    return {
      dir: target,
      present: false,
      summary:
        `NO CONSOLE BUILD at ${target}: the API is served and every page will say so. ` +
        'Build console/ and set AF_CONSOLE_DIR, or use the published image.',
    }
  }
}

function defaultDir(): string {
  // src/console/static.ts -> apps/api/src/console -> up four is the app root.
  return resolve(new URL('../../../../console-out', import.meta.url).pathname)
}

const TYPES: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.json': 'application/json; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
}

function extensionOf(path: string): string {
  const dot = path.lastIndexOf('.')
  const slash = path.lastIndexOf('/')
  return dot > slash ? path.slice(dot).toLowerCase() : ''
}

export interface Asset {
  body: Buffer
  contentType: string
  cacheControl: string
  etag: string
  status: 200 | 404
}

/**
 * Resolve one request path to a file inside the build.
 *
 * Three shapes, in this order:
 *   /               -> index.html
 *   /runs           -> runs.html      (the export writes a file per route)
 *   /_next/x.js     -> the file itself
 *
 * A path that escapes the root is refused before it is read. `..` in a URL is
 * usually a browser normalising badly rather than an attack, but the check
 * costs one comparison and the alternative is serving /etc/passwd.
 */
export async function readAsset(build: ConsoleBuild, urlPath: string): Promise<Asset | null> {
  if (!build.present) return null

  const clean = decodeURIComponent(urlPath.split('?')[0] ?? '/')
  const candidates =
    clean === '/' || clean === ''
      ? ['index.html']
      : extensionOf(clean)
        ? [clean.replace(/^\/+/, '')]
        : [`${clean.replace(/^\/+/, '').replace(/\/+$/, '')}.html`]

  for (const candidate of candidates) {
    const full = resolve(build.dir, normalize(candidate))
    if (full !== build.dir && !full.startsWith(build.dir + sep)) return notFoundPage(build)
    try {
      const body = await readFile(full)
      const ext = extensionOf(full)
      return {
        body,
        contentType: TYPES[ext] ?? 'application/octet-stream',
        // Everything under /_next/static carries a content hash in its name,
        // so it can be cached forever. A page cannot: it is the same URL for
        // every build, and it is rendered for whoever is signed in.
        cacheControl: full.includes(`${sep}_next${sep}static${sep}`)
          ? 'public, max-age=31536000, immutable'
          : ext === '.html'
            ? 'no-store'
            : 'public, max-age=300',
        etag: `"${createHash('sha256').update(body).digest('base64url').slice(0, 24)}"`,
        status: 200,
      }
    } catch {
      // Fall through to the next candidate, then to the 404 page.
    }
  }
  return notFoundPage(build)
}

async function notFoundPage(build: ConsoleBuild): Promise<Asset | null> {
  try {
    const body = await readFile(resolve(build.dir, '404.html'))
    return {
      body,
      contentType: 'text/html; charset=utf-8',
      cacheControl: 'no-store',
      etag: `"${createHash('sha256').update(body).digest('base64url').slice(0, 24)}"`,
      status: 404,
    }
  } catch {
    return null
  }
}
