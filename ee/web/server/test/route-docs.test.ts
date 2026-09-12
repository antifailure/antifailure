// The HTTP paths the documentation names, against the routes the ENTERPRISE
// edition serves.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// WHY THIS LIVES HERE. The community control plane has the same gate, in its own
// api suite, and it holds every documented path to the routes the community
// server registers. It cannot see the enterprise routes and is not allowed to
// name the code that registers them. So the enterprise pages were written with
// the host spelled `<your-control-plane>`, which that gate's host pattern cannot
// match, and every single sign on, provisioning and audit stream path in the
// documentation was checked by nothing at all. That was not a decision anybody
// wrote down; it was a blind spot that happened to keep a gate green.
//
// The defect that earned it: the audit stream page told a hosted customer to
//
//   curl -X PUT https://app.antifailure.dev/enterprise/audit-stream
//
// The community gate went red on it, correctly, because the hosted control plane
// deploys the community image and answered that request with 404, as it did
// /sso/start and /scim/v2/ServiceProviderConfig. The page now names the host the
// way the single sign on and provisioning pages do, and this suite is what
// checks the paths those three pages name, against a server built the way the
// enterprise entry point builds one.
//
// THE ROUTES ARE READ FROM A LIVE SERVER, NOT FROM THE SOURCE. The community gate
// scans for `app.get('...')`, which is right for a server written that way. The
// enterprise routes are extension objects, and the audit stream's path is a
// constant, so a scan would need to understand both and would still say nothing
// about whether register.ts mounts what the packages declare. Here the real
// registerEnterprise runs, the real createServer walks what it registered, and
// Hono's own route table is the answer. The pool is never queried: postgres
// connects on the first statement, and nothing here issues one.
//
// WHAT THIS CANNOT SEE, said here rather than in a report. Like its community
// twin it checks one direction: an enterprise route no page mentions passes. And
// it proves the enterprise edition serves a path, not that a particular hosted
// deployment runs the enterprise edition. That second fact is a deployment's,
// and a probe of the deployment is the only instrument for it.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { clearExtensions, createServer, setSignInPolicy, systemClock } from '@antifailure/api'
import { createPool, type Pool } from '@antifailure/db'
import { registerEnterprise } from '../src/register.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.join(here, '..', '..', '..', '..')
const docsDir = path.join(repoRoot, 'docs', 'src', 'content', 'docs')
const enterpriseDocsDir = path.join(docsDir, 'enterprise')
const consoleAppDir = path.join(repoRoot, 'console', 'app')

// The community gate's hosts, plus the placeholder the enterprise pages use.
// The placeholder is the whole reason this suite exists, so it is listed first.
const OUR_HOSTS = [
  '<your-control-plane>',
  'cp.example.com',
  'app.antifailure.dev',
  'app.dev.antifailure.dev',
  'your-control-plane',
  'localhost:8080',
  '127.0.0.1:8080',
]

/** The paths a server built with or without the enterprise registration
 *  answers, from Hono's own route table. Global middleware is not a route. */
function routesOf(app: { routes: ReadonlyArray<{ path: string }> }): Set<string> {
  return new Set(app.routes.map((r) => r.path).filter((p) => p !== '*' && p !== '/*'))
}

/** The console's pages, which answer on the same host. Read exactly as the
 *  community gate reads them, so a page both suites see is judged the same. */
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

interface Named {
  path: string
  file: string
}

/** Every path written after one of our hosts, in every page and the README. */
async function documentedPaths(): Promise<Named[]> {
  const walk = async (dir: string): Promise<string[]> => {
    const files: string[] = []
    for (const e of await readdir(dir, { withFileTypes: true })) {
      const full = path.join(dir, e.name)
      if (e.isDirectory()) files.push(...(await walk(full)))
      else if (e.name.endsWith('.md')) files.push(full)
    }
    return files
  }
  const out: Named[] = []
  for (const file of [...(await walk(docsDir)), path.join(repoRoot, 'README.md')]) {
    const text = await readFile(file, 'utf8')
    for (const m of text.matchAll(/https?:\/\/(<[a-z-]+>|[A-Za-z0-9.:-]+)(\/[A-Za-z0-9/_.:*<>-]*)/g)) {
      if (!OUR_HOSTS.includes(m[1]!)) continue
      const p = m[2]!.replace(/[.,)]+$/, '')
      if (p === '' || p === '/') continue
      out.push({ path: p, file })
    }
  }
  return out
}

/** A documented path is served when a route matches it segment for segment, or
 *  when it is a segment prefix of one, which is how a base URL like /scim/v2 is
 *  written. A `<placeholder>` segment matches a route PARAMETER and nothing else,
 *  so `/sso/<handle>/acs` is not served by `/sso/saml/acs`. A route segment `*`
 *  is a mounted family and matches whatever follows it. */
export function served(documented: string, routes: ReadonlySet<string>): boolean {
  const d = documented.split('/').filter(Boolean)
  for (const route of routes) {
    const r = route.split('/').filter(Boolean)
    let ok = true
    for (let i = 0; i < d.length; i++) {
      const seg = r[i]
      if (seg === '*') break
      if (seg === undefined) {
        ok = false
        break
      }
      // A route parameter takes anything, placeholder included. A literal takes
      // only itself, and no route segment is spelled with angle brackets, so a
      // placeholder against a literal is refused by this comparison alone.
      if (seg.startsWith(':')) continue
      if (seg !== d[i]) {
        ok = false
        break
      }
    }
    if (ok) return true
  }
  return false
}

function listing(named: Named[]): string {
  return named
    .map((n) => `${n.path}  named in ${path.relative(repoRoot, n.file)}`)
    .sort()
    .join('\n  ')
}

describe('the HTTP paths the documentation names, against the enterprise edition', () => {
  let pool: Pool
  let enterprise: Set<string>
  let community: Set<string>
  let mounted: string[]

  before(async () => {
    // Port 9 on loopback, the discard port, so a statement that did get issued
    // would fail at once rather than reach somebody's database.
    pool = createPool({ url: 'postgres://nobody:nothing@127.0.0.1:9/none', max: 1, connectTimeoutSeconds: 1 })
    const pages = await consoleRoutes()

    clearExtensions()
    setSignInPolicy(null)
    community = new Set([...routesOf(createServer({ pool, github: {} as never, secureCookies: false }).app), ...pages])

    const registered = registerEnterprise({
      pool,
      clock: systemClock,
      baseUrl: 'https://cp.example.com',
      appBaseUrl: 'https://cp.example.com',
      secureCookies: false,
      env: {},
      log: () => {},
      encryptionKey: randomBytes(32),
    })
    mounted = registered.mounted
    enterprise = new Set([...routesOf(createServer({ pool, github: {} as never, secureCookies: false }).app), ...pages])
  })

  after(async () => {
    clearExtensions()
    setSignInPolicy(null)
    await pool.close()
  })

  it('are all paths the enterprise edition serves', async () => {
    const missing = (await documentedPaths())
      // A wildcard is describing a family, not an address.
      .filter((n) => !n.path.includes('*'))
      .filter((n) => !served(n.path, enterprise))
    assert.deepEqual(
      missing,
      [],
      `these paths are documented and the enterprise edition registers no route for them:\n  ${listing(missing)}\n` +
        `A reader who configures one of these finds out at the request, not at start up.`,
    )
  })

  it('name an enterprise route only on an enterprise page', async () => {
    // The other half of the partition. A path only the enterprise edition serves,
    // named on a page a community reader follows, is the 404 this suite was
    // written about, on the page least likely to say which edition it needs.
    const misplaced = (await documentedPaths())
      .filter((n) => !n.file.startsWith(enterpriseDocsDir + path.sep))
      .filter((n) => !n.path.includes('*'))
      .filter((n) => served(n.path, enterprise) && !served(n.path, community))
    assert.deepEqual(
      misplaced,
      [],
      `these pages are outside the enterprise section and name a path only the enterprise edition serves:\n  ${listing(misplaced)}`,
    )
  })

  it('reads the routes and the pages at all, so a broken scan cannot pass quietly', async () => {
    // The negative control. An empty route table serves nothing and a scan that
    // reads no placeholder host finds nothing to refuse, and both look exactly
    // like a clean result.
    assert.deepEqual([...mounted].sort(), ['audit-stream', 'scim', 'sso'], 'registerEnterprise mounted something else')
    for (const route of ['/enterprise/audit-stream', '/sso/oidc/:handle/callback', '/scim/v2/Users']) {
      assert.ok(enterprise.has(route), `the enterprise server does not register ${route}`)
      assert.ok(!community.has(route), `the community server registers ${route}, so the two tables are not two editions`)
    }
    assert.ok(community.has('/auth/github/callback'), 'the community route table is missing the OAuth callback')
    assert.ok(enterprise.has('/auth/github/callback'), 'the enterprise route table dropped a community route')
    const named = await documentedPaths()
    const at = (p: string, page: string) =>
      named.some((n) => n.path === p && path.relative(docsDir, n.file) === path.join('enterprise', page))
    assert.ok(at('/enterprise/audit-stream', 'audit-stream.md'), 'the audit stream page was not read')
    assert.ok(at('/sso/oidc/<handle>/callback', 'sso.md'), 'a path after the placeholder host was not read')
    assert.ok(at('/scim/v2', 'scim.md'), 'the provisioning base URL was not read')
  })

  it('refuses a path no route matches, placeholder or not', () => {
    // The matcher exercised directly, because the placeholder allowance is the
    // part most likely to be widened later into something that accepts anything.
    const routes = new Set(['/sso/saml/:handle/acs', '/scim/v2/Users', '/trpc/*', '/enterprise/audit-stream'])
    assert.ok(served('/sso/saml/<handle>/acs', routes), 'a placeholder for a route parameter')
    assert.ok(served('/sso/saml/acme/acs', routes), 'a concrete value for a route parameter')
    assert.ok(served('/scim/v2', routes), 'a base URL that is a segment prefix')
    assert.ok(served('/trpc/org.list', routes), 'a path inside a mounted family')
    assert.ok(!served('/enterprise/audit-streams', routes), 'a partial segment')
    assert.ok(!served('/sso/<handle>/acs', routes), 'a placeholder standing where the route has a literal')
    assert.ok(!served('/enterprise/audit-stream/extra', routes), 'a path longer than the route')
    assert.ok(!served('/auth/callback', routes), 'the community gate\'s own defect')
  })
})
