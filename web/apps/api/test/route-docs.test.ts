// The documented HTTP surface, against the routes the server registers.
//
// This is config-docs.test.ts one level out. A page naming an environment
// variable nothing reads is an operator setting something inert; a page naming
// a PATH the server does not serve is worse, because the reader cannot tell
// they were misled until the request is made, and by then they have configured
// something else to match it.
//
// The defect that earned it: three documents, including the third page of the
// getting started path and the README, told a self-hoster to set
//
//   AF_GITHUB_REDIRECT_URI=https://cp.example.com/auth/callback
//
// There is no /auth/callback. The route is /auth/github/callback, which is what
// production.tfvars and staging.tfvars both configure, so the product's own
// deployment disagreed with its own instructions. That value is handed
// straight to GitHub as redirect_uri and nothing validates its path at start
// up, so the failure lands at the END of the first sign in, after the operator
// has registered an OAuth App with the same wrong URL. They then compare the
// variable against the App, see them match, and go looking somewhere else.
//
// What this cannot see, said here rather than in a report: it checks one
// direction only. A route the server serves and no page mentions passes.
// reference/api.md is missing eight of them and closing that needs the page
// edited first.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const srcDir = path.join(here, '..', 'src')
const repoRoot = path.join(here, '..', '..', '..', '..')
const docsDir = path.join(repoRoot, 'docs', 'src', 'content', 'docs')
const consoleAppDir = path.join(repoRoot, 'console', 'app')
const apiRefPath = path.join(docsDir, 'reference', 'api.md')

/** Every path the server registers, read from the source rather than a list. */
async function registeredRoutes(): Promise<Set<string>> {
  const found = new Set<string>()
  const walk = async (dir: string): Promise<string[]> => {
    const entries = await readdir(dir, { withFileTypes: true })
    const out: string[] = []
    for (const e of entries) {
      const full = path.join(dir, e.name)
      if (e.isDirectory()) out.push(...(await walk(full)))
      else if (e.name.endsWith('.ts')) out.push(full)
    }
    return out
  }
  for (const file of await walk(srcDir)) {
    const text = await readFile(file, 'utf8')
    for (const m of text.matchAll(/\bapp\.(?:get|post|put|patch|delete|all)\(\s*'([^']+)'/g)) {
      found.add(m[1]!)
    }
    // `app.use` with a path mounts a whole family, and the tRPC router is
    // mounted that way rather than declared route by route. Without this the
    // scanner never saw /trpc at all, so the backward check could not have
    // noticed if that family stopped being documented: it was passing on a
    // route it could not see. A bare '*' is global middleware and not a family.
    for (const m of text.matchAll(/\bapp\.use\(\s*\n?\s*'([^']+)'/g)) {
      if (m[1] !== '*') found.add(m[1]!)
    }
  }
  return found
}

/** The console's pages, which answer on the same host as the API.
 *
 *  Without these the gate reports /device, the page af login sends somebody to
 *  approve a code on, as a route nobody serves. It is served, by Next.js
 *  rather than by Hono, and a reader does not care which. A route group
 *  directory like (app) is a Next.js grouping and contributes no segment. */
async function consoleRoutes(): Promise<Set<string>> {
  const found = new Set<string>()
  const walk = async (dir: string, route: string): Promise<void> => {
    let entries
    try {
      entries = await readdir(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      if (e.isDirectory()) {
        const segment = e.name.startsWith('(') && e.name.endsWith(')') ? '' : `/${e.name}`
        await walk(path.join(dir, e.name), route + segment)
      } else if (e.name === 'page.tsx' || e.name === 'page.ts') {
        found.add(route === '' ? '/' : route)
      }
    }
  }
  await walk(consoleAppDir, '')
  return found
}

// A path is only ours when it hangs off one of OUR hosts, and this is the
// whole reason the gate is trustworthy rather than noisy.
//
// The first version matched a bare path under /auth/, /v1/, /trpc/, /byok/ or
// /console/api/, which sounds specific and is not: it produced ten findings
// and nine were false. /v1/ is what Stripe, Anthropic and OpenAI all use, so
// /v1/charges, /v1/messages and /v1/chat/completions were reported as missing
// routes on this server. It also pulled /auth/github.ts out of the middle of
// a source file path in a self-hosting page. One real finding under nine
// false ones is a gate somebody deletes, and then the real one dies with them.
//
// Requiring a host of ours drops every one of those and keeps all four real
// occurrences, because each was written as a complete URL an operator pastes.
const OUR_HOSTS = [
  'cp.example.com',
  'app.antifailure.dev',
  'app.dev.antifailure.dev',
  'your-control-plane',
  'localhost:8080',
  '127.0.0.1:8080',
]

/** Paths written in the documentation, including inside an example URL. */
async function documentedPaths(): Promise<Map<string, string[]>> {
  const out = new Map<string, string[]>()
  const walk = async (dir: string): Promise<string[]> => {
    const entries = await readdir(dir, { withFileTypes: true })
    const files: string[] = []
    for (const e of entries) {
      const full = path.join(dir, e.name)
      if (e.isDirectory()) files.push(...(await walk(full)))
      else if (e.name.endsWith('.md')) files.push(full)
    }
    return files
  }
  const files = [...(await walk(docsDir)), path.join(repoRoot, 'README.md')]
  for (const file of files) {
    const text = await readFile(file, 'utf8')
    for (const m of text.matchAll(/https?:\/\/([A-Za-z0-9.:-]+)(\/[A-Za-z0-9/_.:*<>-]*)/g)) {
      const host = m[1]!
      if (!OUR_HOSTS.includes(host)) continue
      const p = m[2]!.replace(/[.,)]+$/, '')
      if (p === '' || p === '/') continue
      const rel = path.relative(repoRoot, file)
      out.set(p, [...(out.get(p) ?? []), rel])
    }
  }
  return out
}

/** A documented path is served when a route matches it segment for segment,
 *  or when it is a segment prefix of one. The prefix case is real rather than
 *  a loophole: the model proxy is documented as the BASE URL a client library
 *  appends /v1/messages to, so /byok/anthropic is correct and complete. It is
 *  narrow enough to keep the defect this was written for: /auth/callback is a
 *  segment prefix of nothing. */
function served(documented: string, routes: Set<string>): boolean {
  const d = documented.split('/').filter(Boolean)
  for (const route of routes) {
    const r = route.split('/').filter(Boolean)
    if (d.length > r.length) continue
    let ok = true
    for (let i = 0; i < d.length; i++) {
      if (r[i]!.startsWith(':')) continue
      if (r[i] !== d[i]) {
        ok = false
        break
      }
    }
    if (ok) return true
  }
  return false
}

/** The patterns reference/api.md names, method stripped.
 *
 *  The page enumerates FAMILIES rather than routes: `/trpc/*`, `/v1/*`,
 *  `/auth/*`. That is the right way to write it, so this reads it that way
 *  rather than demanding a row per route, and a finding is a family nobody
 *  mentioned rather than thirty three lines of noise. */
async function documentedPatterns(): Promise<string[]> {
  const text = await readFile(apiRefPath, 'utf8')
  const out = new Set<string>()
  for (const m of text.matchAll(/`(?:GET|POST|PUT|PATCH|DELETE)?\s*(\/[A-Za-z0-9/_*.:-]*)`/g)) {
    out.add(m[1]!)
  }
  return [...out]
}

function covered(route: string, patterns: string[]): boolean {
  return patterns.some((p) => {
    if (p === route) return true
    if (!p.endsWith('/*')) return false
    return route.startsWith(p.slice(0, -1))
  })
}

// Route families reference/api.md does not mention.
//
// EMPTY, and it emptied itself. It held '/webhooks/', '/byok/' and
// '/console/api/' while the page was another agent's to fix, as a register of a
// known gap rather than an exemption: the second assertion below fails when an
// entry STOPS being missing, so the change that documented those eight routes
// could not land without removing them here in the same commit. That is what
// happened.
//
// Leave it empty. A new family belongs on the page, not in this list, and the
// only reason to add a row is a gap somebody else is actively closing.
const KNOWN_UNDOCUMENTED: string[] = []

describe('the HTTP paths the documentation names', () => {
  it('are all paths the server serves', async () => {
    const routes = new Set([...(await registeredRoutes()), ...(await consoleRoutes())])
    const documented = await documentedPaths()
    const missing: string[] = []
    for (const [p, files] of documented) {
      // A wildcard or a placeholder is describing a family, not an address.
      if (p.includes('*') || p.includes('<')) continue
      if (!served(p, routes)) missing.push(`${p}  named in ${[...new Set(files)].join(', ')}`)
    }
    missing.sort()
    assert.deepEqual(
      missing,
      [],
      `these paths are documented and the server registers no route for them:\n  ${missing.join('\n  ')}\n` +
        `A reader who configures one of these finds out at the request, not at start up.`,
    )
  })

  it('finds routes and paths at all, so a broken scan cannot pass quietly', async () => {
    // The negative control. Both scans encode an assumption about shape, and a
    // pattern that cannot match looks exactly like a pattern that found
    // nothing, so the sizes are asserted rather than trusted.
    const routes = await registeredRoutes()
    assert.ok(routes.size >= 25, `the source scan found only ${routes.size} routes`)
    assert.ok(routes.has('/auth/github/callback'), 'the scan did not find the OAuth callback')
    const pages = await consoleRoutes()
    assert.ok(pages.size >= 5, `the console scan found only ${pages.size} pages`)
    assert.ok(pages.has('/device'), 'the console scan did not find the device approval page')
    const documented = await documentedPaths()
    assert.ok(documented.size >= 4, `the documentation scan found only ${documented.size} paths`)
  })

  it('names every route family the server serves', async () => {
    const patterns = await documentedPatterns()
    const routes = await registeredRoutes()
    const missing = [...routes]
      .filter((r) => !covered(r, patterns))
      .filter((r) => !KNOWN_UNDOCUMENTED.some((k) => r.startsWith(k)))
      .sort()
    assert.deepEqual(
      missing,
      [],
      `the server serves these and reference/api.md names no pattern covering them:\n  ${missing.join('\n  ')}\n` +
        `A reader takes that page as the API surface, so a route missing from it does not exist as far as anybody outside this repository is concerned.`,
    )
  })

  it('has no stale entry in the known-undocumented register', async () => {
    // The half that stops the register above becoming a permanent exemption.
    // When the page grows a pattern covering one of these, this fails and the
    // entry has to go, in the same change.
    const patterns = await documentedPatterns()
    const routes = await registeredRoutes()
    const stale = KNOWN_UNDOCUMENTED.filter((k) => {
      const matching = [...routes].filter((r) => r.startsWith(k))
      return matching.length > 0 && matching.every((r) => covered(r, patterns))
    })
    assert.deepEqual(
      stale,
      [],
      `reference/api.md now documents these, so remove them from KNOWN_UNDOCUMENTED:\n  ${stale.join('\n  ')}`,
    )
  })

  it('reads patterns out of the page at all, so a broken scan cannot pass quietly', async () => {
    const patterns = await documentedPatterns()
    assert.ok(patterns.length >= 6, `only ${patterns.length} patterns were read from reference/api.md`)
    assert.ok(patterns.includes('/trpc/*'), 'the scan did not find the /trpc family')
    assert.ok(patterns.includes('/health'), 'the scan did not find /health')
  })

  it('rejects a path that is not a segment prefix of any route', async () => {
    // The rule doing the work, exercised directly, because the prefix
    // allowance is the part most likely to be widened later into something
    // that accepts anything.
    const routes = new Set(['/auth/github/callback', '/byok/anthropic/v1/messages', '/v1/environments/:envId'])
    assert.ok(served('/auth/github/callback', routes), 'an exact route')
    assert.ok(served('/byok/anthropic', routes), 'a segment prefix, which is how the proxy is documented')
    assert.ok(served('/v1/environments/env_abc', routes), 'a concrete value for a route parameter')
    assert.ok(!served('/auth/callback', routes), 'the defect this was written for')
    assert.ok(!served('/authx/github/callback', routes), 'a partial segment is not a prefix')
    assert.ok(!served('/v1/environment', routes), 'a partial segment deeper in')
  })
})
