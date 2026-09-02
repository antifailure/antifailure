// The boundary between the published API and the transport, held to both.
//
// The 1.0.0 notes say the control plane's HTTP API is not a published
// integration surface, and that is true of most of it and false of some of it.
// `www/public/openapi.json` is where the difference is supposed to live, and
// until this file existed nothing compared that document to the routes the
// server actually SERVES. openapi-artifact.test.ts compares the file to what
// openapi.ts produces, which is the file against its own generator; both are
// blind in the same direction, because a route the generator never mentions is
// missing from both sides and the comparison stays green.
//
// So this asks Hono what it is serving, the way limits.test.ts does, and holds
// the answer against src/boundary.ts in both directions:
//
//   - a served route nothing classifies fails, so a new route cannot arrive
//     already invisible;
//   - a route classified `contract` that is not in the document fails, which is
//     the four /v1/oidc/bindings routes;
//   - a route classified `excluded` that IS in the document fails, so the
//     document cannot quietly grow an operation nobody decided to publish.
//
// The third one is what makes the exclusion in openapi.ts safe to keep. #93
// argues at length that operator routes must stay out of the document because
// they take an operator session from a different table and no reader of the
// document could call one. That reasoning is right and it survives; what
// changes is that the exclusion now has to SAY it is an exclusion, so silence
// stops meaning both "deliberately out" and "forgotten".
//
// It needs no database. createServer registers every route synchronously and
// touches nothing before the first request, so the pool and the GitHub client
// are stubs and this runs on any checkout. That is deliberate: limits.test.ts
// asks the same question of the same table and skips when Postgres is absent,
// and a skip that machine load can cause is a pass with extra steps.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Pool } from '@antifailure/db'
import { createServer } from '../src/server.ts'
import { GROUNDS, ROUTE_BOUNDARY, boundaryFor, documentPath } from '../src/boundary.ts'
import { listProcedures, openApiDocument } from '../src/openapi.ts'
import type { GitHubClient } from '../src/auth/github.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const publishedPath = path.join(here, '..', '..', '..', '..', 'www', 'public', 'openapi.json')

/**
 * Contract routes the published document does not carry yet.
 *
 * A REGISTER OF A KNOWN GAP, NOT AN EXEMPTION, and the difference is the
 * assertion further down that fails when an entry stops being missing. The
 * change that documents one of these cannot land without deleting its line
 * here in the same commit. KNOWN_UNDOCUMENTED in route-docs.test.ts is the
 * same device and it emptied itself once already.
 *
 * EMPTY, and it emptied itself. It held the four `/v1/oidc/bindings` routes,
 * which are classified `contract` because there is no `af` command in front of
 * them and guides/github.md hands a customer a curl line, so the HTTP call is
 * the surface. The change that documented them could not land without these
 * four lines going in the same merge, because the assertion below fails when an
 * entry STOPS being missing. That is what happened.
 *
 * Leave it empty. A new contract route belongs in the document, not here.
 */
const CONTRACT_NOT_YET_IN_DOCUMENT: string[] = []

/**
 * tRPC procedures deliberately kept out of the document.
 *
 * `admin.` is the operator surface, and the case for withholding it is argued
 * at length on `isPublishedProcedure` in openapi.ts: those procedures take an
 * operator session from a different table, so describing them correctly would
 * describe an API no reader of this document can use, and publishing them would
 * map the operator surface for somebody enumerating it.
 *
 * The prefix is here rather than only in that predicate because narrowing what
 * a document enumerates does not show up in the document. Thirty five
 * procedures left the published set when #93 landed, and without this line the
 * only visible effect would have been a tidier file.
 */
const EXCLUDED_PROCEDURES: string[] = ['admin.']

/** Every route the process can ever serve, from the router itself.
 *
 *  Every optional feature is switched on. Email sign-in registers two routes
 *  only when a mailer is configured, and a classification that covered the
 *  default configuration alone would leave the two routes that carry a
 *  sign-in link unclassified on exactly the deployments that have them. */
function servedRoutes(): string[] {
  const { app } = createServer({
    pool: {} as unknown as Pool,
    github: {} as unknown as GitHubClient,
    emailSignIn: {
      mailer: { send: async () => {} },
      from: 'antifailure@example.com',
      appBaseUrl: 'https://console.example.com/',
    } as never,
  })
  const routes = (app.routes as { method: string; path: string }[]).map(
    (r) => `${r.method} ${r.path}`,
  )
  return [...new Set(routes)].sort()
}

const METHODS = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options'])

/** "METHOD /path" for every operation in a document, in the router's syntax. */
function operationsIn(document: Record<string, unknown>): Set<string> {
  const out = new Set<string>()
  const paths = (document.paths ?? {}) as Record<string, Record<string, unknown>>
  for (const [route, item] of Object.entries(paths)) {
    for (const method of Object.keys(item)) {
      if (METHODS.has(method)) out.add(`${method.toUpperCase()} ${route}`)
    }
  }
  return out
}

const generated = operationsIn(openApiDocument())

describe('the route boundary', () => {
  it('classifies every route the server serves', () => {
    const unclassified = servedRoutes().filter((route) => {
      const space = route.indexOf(' ')
      return !boundaryFor(route.slice(0, space), route.slice(space + 1))
    })
    assert.deepEqual(
      unclassified,
      [],
      `these routes are served and src/boundary.ts says nothing about them:\n  ${unclassified.join('\n  ')}\n` +
        `A route that nothing classifies is one nobody decided to publish or to withhold, and it ` +
        `stays that way for as long as the silence lasts.`,
    )
  })

  it('has no classification for a route nothing serves', () => {
    // The half that stops the register becoming a graveyard. Without it a
    // deleted route keeps its entry, the entry keeps its reason, and the file
    // stops describing the server.
    const served = new Set(servedRoutes())
    const stale = Object.keys(ROUTE_BOUNDARY).filter((route) => !served.has(route))
    assert.deepEqual(
      stale,
      [],
      `src/boundary.ts classifies these and the router serves none of them:\n  ${stale.join('\n  ')}`,
    )
  })

  it('gives every excluded route a ground and a reason worth reading', () => {
    const allowed = new Set<string>(GROUNDS)
    for (const [route, boundary] of Object.entries(ROUTE_BOUNDARY)) {
      // The reason is what somebody reads before moving a route across the
      // line. A one word reason is the same as none, which is the state this
      // file exists to end.
      assert.ok(
        boundary.reason.length > 40,
        `${route} carries no reason worth reading: ${JSON.stringify(boundary.reason)}`,
      )
      if (boundary.audience === 'contract') {
        assert.equal(boundary.grounds, undefined, `${route} is contract and cites grounds for exclusion`)
        continue
      }
      assert.ok(
        boundary.grounds && allowed.has(boundary.grounds),
        `${route} is excluded on grounds of ${JSON.stringify(boundary.grounds)}, which is not one of the ${GROUNDS.length}`,
      )
    }
  })

  it('publishes every contract route, and nothing else', () => {
    const known = new Set(CONTRACT_NOT_YET_IN_DOCUMENT)
    const missing: string[] = []
    const surprising: string[] = []
    for (const [route, boundary] of Object.entries(ROUTE_BOUNDARY)) {
      const space = route.indexOf(' ')
      const published = `${route.slice(0, space)} ${documentPath(route.slice(space + 1))}`
      const inDocument = generated.has(published)
      if (boundary.audience === 'contract' && !inDocument && !known.has(route)) missing.push(route)
      if (boundary.audience === 'excluded' && inDocument) surprising.push(route)
    }
    assert.deepEqual(
      missing.sort(),
      [],
      `these are classified as the published contract and the document does not carry them:\n  ${missing.join('\n  ')}\n` +
        `A caller generating a client from the document cannot call them, so the classification and ` +
        `the document disagree about what the API is.`,
    )
    assert.deepEqual(
      surprising.sort(),
      [],
      `these are classified as deliberately excluded and the document carries them anyway:\n  ${surprising.join('\n  ')}\n` +
        `Either the exclusion is wrong or the document grew an operation nobody decided to publish.`,
    )
  })

  it('has no stale entry in the register of known gaps', () => {
    // The assertion that makes the register above a gap rather than an
    // exemption. When the document grows one of these, this fails and the line
    // has to go, in the same change.
    const closed = CONTRACT_NOT_YET_IN_DOCUMENT.filter((route) => {
      const space = route.indexOf(' ')
      return generated.has(`${route.slice(0, space)} ${documentPath(route.slice(space + 1))}`)
    })
    assert.deepEqual(
      closed,
      [],
      `the document now carries these, so delete them from CONTRACT_NOT_YET_IN_DOCUMENT:\n  ${closed.join('\n  ')}`,
    )
  })

  it('publishes every tRPC procedure that is not deliberately withheld', () => {
    const excluded = EXCLUDED_PROCEDURES
    const missing = listProcedures()
      .map(({ path: procedure, type }) => ({
        procedure,
        published: `${type === 'query' ? 'GET' : 'POST'} /trpc/${procedure}`,
      }))
      .filter(({ procedure, published }) => {
        if (excluded.some((prefix) => procedure.startsWith(prefix))) return false
        return !generated.has(published)
      })
      .map(({ procedure }) => procedure)
    assert.deepEqual(
      missing.sort(),
      [],
      `these procedures are served and the document does not carry them:\n  ${missing.join('\n  ')}\n` +
        `Narrowing what the document enumerates does not show up in the document, so it has to show up here.`,
    )
  })

  it('has no stale entry in the withheld-procedure register', () => {
    const stale = EXCLUDED_PROCEDURES.filter(
      (prefix) => !listProcedures().some(({ path: p }) => p.startsWith(prefix)),
    )
    assert.deepEqual(stale, [], `no procedure begins with these any more:\n  ${stale.join('\n  ')}`)
  })

  it('holds the file the site publishes to the same rule, not just the generator', () => {
    // openapi-artifact.test.ts already fails when the file and the generator
    // disagree, so this looks redundant and is not: it is the one assertion
    // here that reads the BYTES the apex serves. If the two checks ever
    // disagree, the one that read the file is the one to believe.
    return readFile(publishedPath, 'utf8').then((text) => {
      const published = operationsIn(JSON.parse(text) as Record<string, unknown>)
      const known = new Set(CONTRACT_NOT_YET_IN_DOCUMENT)
      const missing = Object.entries(ROUTE_BOUNDARY)
        .filter(([route, boundary]) => {
          if (boundary.audience !== 'contract' || known.has(route)) return false
          const space = route.indexOf(' ')
          return !published.has(`${route.slice(0, space)} ${documentPath(route.slice(space + 1))}`)
        })
        .map(([route]) => route)
      assert.deepEqual(
        missing.sort(),
        [],
        `www/public/openapi.json is the file the apex serves and it does not carry:\n  ${missing.join('\n  ')}`,
      )
    })
  })

  it('finds routes, operations and procedures at all, so a broken scan cannot pass quietly', () => {
    // Every assertion above is satisfied by finding nothing. Three scans, three
    // sizes, and one landmark each, because a scan that returns an empty set
    // looks exactly like a boundary that is perfectly kept.
    const routes = servedRoutes()
    assert.ok(routes.length >= 40, `the router reported only ${routes.length} routes`)
    assert.ok(routes.includes('POST /v1/events'), 'the route scan did not find the ingestion path')
    assert.ok(
      routes.includes('GET /auth/email/callback'),
      'the route scan did not find the email sign-in routes, so the optional features are not switched on',
    )
    assert.ok(generated.size >= 60, `the document reported only ${generated.size} operations`)
    assert.ok(generated.has('GET /health'), 'the document scan did not find the liveness route')
    assert.ok(listProcedures().length >= 50, 'the procedure walk found almost nothing')
  })

  it('is described in prose by the number of grounds there actually are', async () => {
    // The defect constcheck exists for, in a language it cannot read. It counts
    // closed sets declared as Go constants and every instance it found ran the
    // same direction: a set grew, the code took the new member, and the
    // sentence naming a number never moved. This set is TypeScript, three
    // documents state its size, and nothing else would notice an eighth.
    const number = ['zero', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine', 'ten']
    const word = number[GROUNDS.length]
    assert.ok(word, `there are now ${GROUNDS.length} grounds and this check only spells up to ten`)
    const pages = [
      path.join(here, '..', 'src', 'boundary.ts'),
      path.join(here, '..', '..', '..', '..', 'CHANGELOG.md'),
      path.join(here, '..', '..', '..', '..', 'docs', 'src', 'content', 'docs', 'reference', 'stability.md'),
    ]
    for (const page of pages) {
      const text = await readFile(page, 'utf8')
      const claims = [...text.matchAll(/\b(zero|one|two|three|four|five|six|seven|eight|nine|ten)\s+(?:recorded\s+)?grounds\b/g)]
      assert.ok(claims.length > 0, `${path.basename(page)} states no number of grounds, so this check reads nothing there`)
      for (const claim of claims) {
        assert.equal(
          claim[1],
          word,
          `${path.basename(page)} says "${claim[0]}" and there are ${GROUNDS.length}`,
        )
      }
    }
  })

  it('resolves a classification by method as well as by path', () => {
    // The rule doing the work, exercised directly. Keying on the path alone
    // would classify DELETE /v1/tokens/:token by whatever GET said, and the two
    // are not the same question.
    assert.equal(boundaryFor('GET', '/health')?.audience, 'contract')
    assert.equal(boundaryFor('GET', '/metrics')?.audience, 'excluded')
    assert.equal(boundaryFor('POST', '/health'), undefined, 'a method nobody serves is not classified')
    assert.equal(boundaryFor('GET', '/v1/nothing'), undefined, 'a path nobody serves is not classified')
  })

  it('spells a route parameter the way the document does', () => {
    assert.equal(documentPath('/v1/environments/:envId'), '/v1/environments/{envId}')
    assert.equal(documentPath('/v1/oidc/bindings/:owner/:name'), '/v1/oidc/bindings/{owner}/{name}')
    assert.equal(documentPath('/health'), '/health')
  })

  it('refuses a route the classification would have hidden', () => {
    // The negative control on the comparison itself, with the real document and
    // a fabricated classification. Without this the assertions above are only
    // evidence that today's tree is consistent, not that an inconsistent one
    // would be caught.
    const pretend = {
      'GET /health': { audience: 'excluded' as const, grounds: 'cli-transport' as const, reason: 'a'.repeat(50) },
      'GET /v1/invented': { audience: 'contract' as const, reason: 'a'.repeat(50) },
    }
    const surprising: string[] = []
    const missing: string[] = []
    for (const [route, boundary] of Object.entries(pretend)) {
      const space = route.indexOf(' ')
      const published = `${route.slice(0, space)} ${documentPath(route.slice(space + 1))}`
      if (boundary.audience === 'excluded' && generated.has(published)) surprising.push(route)
      if (boundary.audience === 'contract' && !generated.has(published)) missing.push(route)
    }
    assert.deepEqual(surprising, ['GET /health'], 'a published route claiming to be excluded was not caught')
    assert.deepEqual(missing, ['GET /v1/invented'], 'a contract route absent from the document was not caught')
  })
})
