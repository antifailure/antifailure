// Writes the OpenAPI document to a file, after checking it.
//
// WHY A FILE AND NOT A PROXY, which is the decision this script exists to
// carry out.
//
// `antifailure.dev/openapi.json` has to resolve, because that is the address an
// agent guesses and the address llms.txt advertises. The first attempt served
// it by proxying `app.antifailure.dev/openapi.json` at request time. Two things
// were wrong with that and both were measured rather than argued.
//
// The site deploys on every push to main. The production control plane moves
// only on a release promotion, and on the day this was written it was serving
// f66d6af2 while main was 80d76a41. So the proxy would have published a
// document with no operation IDs, no server URL and no input schemas, for as
// long as the promotion took, while the site's own documentation described the
// document that had them. The apex would have contradicted itself.
//
// And a proxy can only validate at request time, with whatever validator fits
// in a function. The one that shipped checked the root shape: openapi, info,
// paths. A document whose nested path item was malformed passed that check and
// was cached for five minutes.
//
// A file generated here is validated in CI before it can be committed, is
// pinned to the revision that built it by virtue of being IN that revision, and
// cannot 502 because production is unreachable. The cost is that it describes
// the revision the SITE was built from rather than the revision production is
// serving, which is a real gap and is the reason deploy.yml probes production's
// live document afterwards and says so out loud rather than leaving it implied.
//
// WHAT THIS VALIDATION CANNOT SEE. It is not a complete OpenAPI 3.1 metaschema
// validator and does not pretend to be one: it does not check every JSON Schema
// keyword against the 2020-12 dialect, does not check example values against
// their schemas, and does not know the full parameter serialization rules.
// Redocly is the reference implementation and is the thing to run when the
// document's shape changes structurally. What this does check is the class of
// defect that can actually reach the apex from here: a dangling reference, a
// missing description, a duplicate or unusable operation ID, an unreachable
// component, and a response with no body schema.

import { writeFileSync, readFileSync, existsSync } from 'node:fs'
import { openApiDocument } from '../src/openapi.ts'

const METHODS = new Set(['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'])

export function problemsWith(document: Record<string, unknown>): string[] {
  const problems: string[] = []
  const say = (message: string) => problems.push(message)

  if (typeof document.openapi !== 'string' || !/^3\.1\.\d+$/.test(document.openapi)) {
    say(`openapi is ${JSON.stringify(document.openapi)}, which is not a 3.1 version`)
  }
  const info = document.info as { title?: unknown; version?: unknown } | undefined
  if (!info || typeof info.title !== 'string' || typeof info.version !== 'string') {
    say('info needs a string title and a string version')
  }
  const servers = document.servers as { url?: unknown }[] | undefined
  if (!Array.isArray(servers) || servers.length === 0) {
    say('servers is empty, so a generated client has no host to call')
  } else {
    for (const server of servers) {
      if (typeof server.url !== 'string' || !/^https:\/\//.test(server.url)) {
        say(`server url ${JSON.stringify(server.url)} is not an https address`)
      }
    }
  }

  const components = (document.components ?? {}) as {
    schemas?: Record<string, unknown>
    securitySchemes?: Record<string, unknown>
  }
  const schemas = components.schemas ?? {}
  const securitySchemes = components.securitySchemes ?? {}

  // Every $ref in the whole tree, not just the ones at the top. A malformed
  // nested path is exactly what the root-shape check could not see.
  const referenced = new Set<string>()
  const walk = (node: unknown, where: string) => {
    if (Array.isArray(node)) {
      node.forEach((item, i) => walk(item, `${where}[${i}]`))
      return
    }
    if (node === null || typeof node !== 'object') {
      if (node === undefined) say(`${where} is undefined, which JSON cannot carry`)
      return
    }
    for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
      if (key === '$ref' && typeof value === 'string') {
        const prefix = '#/components/schemas/'
        if (!value.startsWith(prefix)) {
          say(`${where}.$ref points outside components/schemas: ${value}`)
        } else {
          const name = value.slice(prefix.length)
          referenced.add(name)
          if (!(name in schemas)) say(`${where}.$ref names a schema that does not exist: ${name}`)
        }
        continue
      }
      walk(value, `${where}.${key}`)
    }
  }
  walk(document, '$')

  for (const name of Object.keys(schemas)) {
    if (!referenced.has(name)) {
      say(`components.schemas.${name} is referenced by nothing, so it documents nothing`)
    }
  }

  const paths = document.paths as Record<string, unknown> | undefined
  if (!paths || typeof paths !== 'object' || Object.keys(paths).length === 0) {
    say('paths is empty')
    return problems
  }

  const operationIds = new Set<string>()
  for (const [route, item] of Object.entries(paths)) {
    if (!route.startsWith('/')) say(`path ${route} does not start with a slash`)
    if (item === null || typeof item !== 'object' || Array.isArray(item)) {
      say(`path ${route} is not a path item object`)
      continue
    }
    const methods = Object.entries(item as Record<string, unknown>).filter(([key]) =>
      METHODS.has(key),
    )
    if (methods.length === 0) say(`path ${route} declares no operation`)

    for (const [method, raw] of methods) {
      const at = `${method.toUpperCase()} ${route}`
      if (raw === null || typeof raw !== 'object') {
        say(`${at} is not an operation object`)
        continue
      }
      const operation = raw as {
        operationId?: unknown
        description?: unknown
        responses?: Record<string, unknown>
        security?: { [scheme: string]: unknown }[]
        parameters?: unknown
        requestBody?: { content?: Record<string, unknown> }
      }

      if (typeof operation.operationId !== 'string' || !/^[a-zA-Z][a-zA-Z0-9_]*$/.test(operation.operationId)) {
        say(`${at} has no operationId a generated function can be named after`)
      } else if (operationIds.has(operation.operationId)) {
        say(`${at} repeats the operationId ${operation.operationId}`)
      } else {
        operationIds.add(operation.operationId)
      }

      if (typeof operation.description !== 'string' || operation.description.length < 20) {
        say(`${at} has no description worth reading`)
      }

      const responses = operation.responses
      if (!responses || Object.keys(responses).length === 0) {
        say(`${at} declares no response`)
      } else {
        for (const [status, value] of Object.entries(responses)) {
          if (!/^[1-5]\d\d$/.test(status) && status !== 'default') {
            say(`${at} declares a response keyed ${status}`)
          }
          const response = value as { description?: unknown; content?: Record<string, unknown> }
          if (typeof response.description !== 'string' || response.description.length === 0) {
            say(`${at} response ${status} has no description`)
          }
          for (const [media, body] of Object.entries(response.content ?? {})) {
            if (!/^[\w.+-]+\/[\w.+-]+$/.test(media)) say(`${at} response ${status} media type ${media}`)
            if (!(body as { schema?: unknown }).schema) {
              say(`${at} response ${status} ${media} carries no schema`)
            }
          }
        }
      }

      for (const requirement of operation.security ?? []) {
        for (const scheme of Object.keys(requirement)) {
          if (!(scheme in securitySchemes)) {
            say(`${at} requires the security scheme ${scheme}, which is not declared`)
          }
        }
      }

      if (Array.isArray(operation.parameters)) {
        for (const parameter of operation.parameters as { name?: unknown; in?: unknown }[]) {
          if (typeof parameter.name !== 'string') say(`${at} has a parameter with no name`)
          if (!['query', 'header', 'path', 'cookie'].includes(String(parameter.in))) {
            say(`${at} parameter ${String(parameter.name)} is in ${String(parameter.in)}`)
          }
        }
      }
    }
  }

  return problems
}

/** The bytes the site serves. Stable ordering, and a trailing newline so the
 *  file behaves in a diff and in an editor. */
export function render(): string {
  return `${JSON.stringify(openApiDocument(), null, 2)}\n`
}

function main(argv: string[]): number {
  const check = argv.includes('--check')
  const target = argv.find((arg) => !arg.startsWith('--'))
  if (!target) {
    console.error('usage: openapi.ts <path> [--check]')
    return 2
  }

  const document = openApiDocument()
  const problems = problemsWith(document)
  if (problems.length > 0) {
    console.error(`openapi: the document is not publishable, ${problems.length} problems:`)
    for (const problem of problems) console.error(`  ${problem}`)
    return 1
  }

  const wanted = render()
  if (check) {
    if (!existsSync(target)) {
      console.error(`openapi: ${target} does not exist. Run 'just generate'.`)
      return 1
    }
    if (readFileSync(target, 'utf8') !== wanted) {
      console.error(
        `openapi: ${target} is not what the router produces. The site would publish a ` +
          "document describing an API this revision does not serve. Run 'just generate'.",
      )
      return 1
    }
    console.log(`openapi: ${target} matches the router, ${Object.keys(document.paths as object).length} paths`)
    return 0
  }

  writeFileSync(target, wanted)
  console.log(`openapi: wrote ${target}, ${Object.keys(document.paths as object).length} paths`)
  return 0
}

if (process.argv[1] && import.meta.url === `file://${process.argv[1]}`) {
  process.exit(main(process.argv.slice(2)))
}
