// The engine's mock pack runtime, in TypeScript, so the billing integration is
// tested against the pack the product ships rather than against a stub.
//
// WHY THIS EXISTS AT ALL. `engine/internal/mockpack` answers Stripe offline,
// with state, and it is what a customer's application talks to inside a
// preview environment. Testing the control plane's own Stripe client against a
// hand-written fake would prove the fake agrees with the client, which is
// worth nothing: a fake is written by the same person, on the same day, with
// the same wrong idea about the response shape. Testing it against the pack
// proves the client survives what the product actually serves, and it is how
// four defects in the pack were found.
//
// WHY IT IS A SECOND IMPLEMENTATION. The alternative was running the Go
// sidecar from a Node test, which puts a Go toolchain and a compile in the way
// of `just test-web`. Two implementations drift, so they are not left to agree
// by inspection: the Go one emits a transcript to schemas/mockpack-vectors.json
// and mockpack.test.ts proves this one reproduces every answer in it. That is
// the same arrangement `web/packages/policy` uses for the egress engine, for
// the same reason.
//
// Kept in test/ rather than src/ because nothing the server ships uses it.

import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))

/** Where the packs the engine ships live. Read from the engine's own tree, so
 *  a pack edited there is the pack tested here and the two cannot be different
 *  files saying different things. */
export const packsDir = path.join(
  here, '..', '..', '..', '..', 'engine', 'internal', 'mockpack', 'packs',
)

export interface Route {
  method?: string
  path: string
  status?: number
  body?: unknown
  headers?: Record<string, string>
  store?: string
  load?: string
  merge?: string
  not_found?: unknown
  list?: string
}

export interface Pack {
  name: string
  hosts: string[]
  routes: Route[]
  description?: string
}

export interface MockResponse {
  status: number
  headers: Record<string, string>
  body: string
  pack: string
  route: string
}

/** A fixed instant rather than the real one, so two runs of one flow can be
 *  compared. Must equal fixedNow in mockpack.go. */
const FIXED_NOW = '1767225600'

/** Stands in for a field the request said nothing about, between substitution
 *  and the pass that removes it. */
const UNFILLED = '"__af_unfilled__"'

export async function loadPack(name: string): Promise<Pack> {
  return JSON.parse(await readFile(path.join(packsDir, `${name}.json`), 'utf8')) as Pack
}

interface Match {
  route: Route
  captures: Record<string, string>
  specificity: number
}

/** Answers requests from a set of packs and remembers what was created. */
export class MockPack {
  private readonly packs: Pack[]
  private readonly store = new Map<string, Map<string, unknown>>()
  private readonly order = new Map<string, string[]>()
  private counter = 0

  constructor(packs: Pack[]) {
    this.packs = packs
  }

  handles(host: string): boolean {
    return this.packs.some((p) => p.hosts.some((h) => hostMatches(host, h)))
  }

  /** The response for a request, or null when no route matched. A miss is
   *  reported rather than guessed at: an empty 200 is how an application
   *  carries on with nothing and fails somewhere unrelated. */
  answer(host: string, method: string, requestPath: string, body: string): MockResponse | null {
    let best: Match | null = null
    let bestPack: Pack | null = null
    for (const pack of this.packs) {
      if (!pack.hosts.some((h) => hostMatches(host, h))) continue
      for (const route of pack.routes) {
        const m = matchRoute(route, method, requestPath)
        if (!m) continue
        if (!best || m.specificity > best.specificity) {
          best = m
          bestPack = pack
        }
      }
    }
    if (!best || !bestPack) return null
    return this.respond(bestPack, best, body)
  }

  private respond(pack: Pack, m: Match, requestBody: string): MockResponse {
    const route = m.route
    const resp: MockResponse = {
      status: route.status ?? 200,
      headers: route.headers ?? {},
      body: '',
      pack: pack.name,
      route: `${route.method ?? ''} ${route.path}`,
    }

    if (route.load) {
      const stored = this.store.get(route.load)?.get(m.captures.id ?? '')
      if (stored !== undefined) {
        resp.body = JSON.stringify(stored)
        return resp
      }
      return missing(resp, route)
    }

    if (route.merge) {
      const id = m.captures.id ?? ''
      const stored = this.store.get(route.merge)?.get(id)
      if (stored === undefined) return missing(resp, route)
      const merged = mergeObjects(stored, JSON.parse(this.fill(route.body, m, requestBody, true)))
      this.remember(route.merge, merged)
      resp.body = JSON.stringify(merged)
      return resp
    }

    if (route.list) {
      resp.body = this.listOf(route.list, route.body, m)
      return resp
    }

    const filled = this.fill(route.body, m, requestBody, false)
    if (route.store) this.remember(route.store, JSON.parse(filled))
    resp.body = filled
    return resp
  }

  private remember(collection: string, value: unknown): void {
    const id = (value as { id?: unknown })?.id
    if (typeof id !== 'string' || id === '') return
    let byId = this.store.get(collection)
    if (!byId) {
      byId = new Map()
      this.store.set(collection, byId)
    }
    if (!byId.has(id)) this.order.set(collection, [...(this.order.get(collection) ?? []), id])
    byId.set(id, value)
  }

  private listOf(collection: string, shape: unknown, m: Match): string {
    const ids = this.order.get(collection) ?? []
    const items = ids.map((id) => this.store.get(collection)!.get(id))
    const data = JSON.stringify(items)
    if (shape === undefined) return data
    // The shape names where the items go with a placeholder, which is replaced
    // with the array rather than with a string.
    return this.fill(shape, m, '', false).replaceAll('"{items}"', data)
  }

  /**
   * Substitutes placeholders in a body, and returns JSON text.
   *
   * Three sources, in order: what the path captured, what the request body
   * carried, and generated values. The order matters because a pack that names
   * {id} in both a path and a body means the path's.
   *
   * dropUnsupplied is set for a merge only. An update names what CHANGED, so a
   * field the request said nothing about is removed rather than blanked, or
   * every update is a partial wipe.
   */
  private fill(body: unknown, m: Match, requestBody: string, dropUnsupplied: boolean): string {
    if (body === undefined) return ''
    const fields = fieldsOf(requestBody)
    let out = JSON.stringify(body)
    for (const [name, value] of Object.entries(m.captures)) {
      out = out.replaceAll(`{${name}}`, value)
    }
    for (const [name, value] of Object.entries(fields)) {
      out = out.replaceAll(`{request.${name}}`, value)
    }
    let blank = ''
    if (dropUnsupplied) {
      out = markWholeUnfilled(out)
      blank = UNFILLED
    }
    out = blankUnfilled(out, '{request.')
    out = scalars(out, m.captures, fields, blank)
    out = this.generate(out)
    if (dropUnsupplied) out = removeUnfilled(out)
    return out
  }

  /** Fills the placeholders a pack uses for identifiers and timestamps.
   *
   *  One identifier per prefix per response: a body that names {id:cs_} twice
   *  means one checkout session, not two. */
  private generate(s: string): string {
    const minted = new Map<string, string>()
    for (;;) {
      const start = s.indexOf('{id:')
      if (start < 0) break
      const end = s.indexOf('}', start)
      if (end < 0) break
      const prefix = s.slice(start + 4, end)
      let id = minted.get(prefix)
      if (id === undefined) {
        this.counter += 1
        id = `${prefix}mock${String(this.counter).padStart(14, '0')}`
        minted.set(prefix, id)
      }
      s = s.slice(0, start) + id + s.slice(end + 1)
    }
    return s.replaceAll('{now}', FIXED_NOW)
  }
}

function missing(resp: MockResponse, route: Route): MockResponse {
  resp.status = 404
  resp.body =
    route.not_found === undefined
      ? '{"error":{"type":"invalid_request_error","message":"No such object"}}'
      : JSON.stringify(route.not_found)
  return resp
}

/** Applies an update over a stored object, one level deep. A provider's update
 *  replaces a nested object wholesale rather than merging into it. */
function mergeObjects(stored: unknown, update: unknown): unknown {
  if (stored === null || typeof stored !== 'object' || Array.isArray(stored)) return update
  if (update === null || typeof update !== 'object' || Array.isArray(update)) return stored
  return { ...(stored as Record<string, unknown>), ...(update as Record<string, unknown>) }
}

function hostMatches(host: string, pattern: string): boolean {
  const h = host.toLowerCase().replace(/\.$/, '')
  const p = pattern.toLowerCase()
  if (p === '*') return true
  if (p.startsWith('*.')) {
    const suffix = p.slice(1)
    return h.endsWith(suffix) && h.length > suffix.length
  }
  return h === p
}

function matchRoute(route: Route, method: string, requestPath: string): Match | null {
  if (route.method && route.method.toLowerCase() !== method.toLowerCase()) return null
  const captures: Record<string, string> = {}
  let score = route.method ? 1 : 0

  const want = route.path.replace(/^\/+|\/+$/g, '').split('/')
  const have = requestPath.replace(/^\/+|\/+$/g, '').split('/')

  for (let i = 0; i < want.length; i += 1) {
    const seg = want[i]!
    // Matches the rest, and scores nothing, so a specific route always wins
    // over a catch all.
    if (seg === '**') return { route, captures, specificity: score }
    if (i >= have.length) return null
    if (seg.startsWith('{') && seg.endsWith('}')) {
      captures[seg.slice(1, -1)] = have[i]!
      score += 2
    } else if (seg === have[i]) {
      // A literal beats a placeholder, so /v1/customers/deleted wins over
      // /v1/customers/{id} without the author thinking about order.
      score += 10
    } else {
      return null
    }
  }
  if (have.length !== want.length) return null
  return { route, captures, specificity: score }
}

/** Flattens a request body one level, and also reads form encoding, because
 *  half of these providers take forms. */
export function fieldsOf(body: string): Record<string, string> {
  const out: Record<string, string> = {}
  if (body === '') return out
  try {
    const parsed: unknown = JSON.parse(body)
    if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
      flatten('', parsed as Record<string, unknown>, out)
      return out
    }
  } catch {
    // Not JSON, so it is a form. Falls through deliberately.
  }
  for (const pair of body.split('&')) {
    const at = pair.indexOf('=')
    if (at < 0) continue
    out[unescapeForm(pair.slice(0, at))] = unescapeForm(pair.slice(at + 1))
  }
  return out
}

function flatten(prefix: string, obj: Record<string, unknown>, out: Record<string, string>): void {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix === '' ? k : `${prefix}.${k}`
    if (typeof v === 'string') out[key] = v
    else if (typeof v === 'number') out[key] = numberText(v)
    else if (typeof v === 'boolean') out[key] = String(v)
    else if (v !== null && typeof v === 'object' && !Array.isArray(v)) {
      flatten(key, v as Record<string, unknown>, out)
    }
  }
}

/** Go writes a float with strconv.FormatFloat(v, 'f', -1, 64), which never
 *  uses exponent notation. JavaScript does above 1e21, so the two would
 *  disagree on a number no provider sends but a corpus could. */
function numberText(v: number): string {
  const plain = String(v)
  if (!plain.includes('e') && !plain.includes('E')) return plain
  return v.toFixed(20).replace(/\.?0+$/, '')
}

function unescapeForm(s: string): string {
  let out = ''
  const plus = s.replaceAll('+', ' ')
  for (let i = 0; i < plus.length; i += 1) {
    if (plus[i] === '%' && i + 2 < plus.length) {
      const n = Number.parseInt(plus.slice(i + 1, i + 3), 16)
      if (!Number.isNaN(n) && /^[0-9a-fA-F]{2}$/.test(plus.slice(i + 1, i + 3))) {
        out += String.fromCharCode(n)
        i += 2
        continue
      }
    }
    out += plus[i]
  }
  return out
}

/** Empties placeholders nothing supplied a value for. A literal
 *  {request.email} in a response is the placeholder leakage that proves nobody
 *  looked at the output. */
function blankUnfilled(s: string, prefix: string): string {
  for (;;) {
    const start = s.indexOf(prefix)
    if (start < 0) return s
    const end = s.indexOf('}', start)
    if (end < 0) return s
    s = s.slice(0, start) + s.slice(end + 1)
  }
}

/** Replaces `"{request.x}"`, the whole string literal, with the mark that
 *  removeUnfilled deletes the field for. A placeholder inside a longer string
 *  has no field to remove and blanks as it always did. */
function markWholeUnfilled(s: string): string {
  let from = 0
  for (;;) {
    const start = s.indexOf('"{request.', from)
    if (start < 0) return s
    const end = s.indexOf('}"', start)
    if (end < 0) return s
    if (s.slice(start + 1, end).includes('}')) {
      from = start + 1
      continue
    }
    s = s.slice(0, start) + UNFILLED + s.slice(end + 2)
    from = start + UNFILLED.length
  }
}

/**
 * Deletes every field the request said nothing about.
 *
 * A field whose value is the mark goes, and so does the whole branch CONTAINING
 * one. That second rule is what makes an update of a nested structure behave:
 * Stripe changes a plan with items[0][price], so the pack's update route names
 * the whole items list, and an update that says only cancel_at_period_end would
 * otherwise replace the subscription's items with an item whose price has no id.
 */
function removeUnfilled(s: string): string {
  if (!s.includes(UNFILLED)) return s
  let parsed: unknown
  try {
    parsed = JSON.parse(s)
  } catch {
    return s
  }
  return JSON.stringify(prune(parsed)[0])
}

/** The value with unsupplied fields removed, and whether it contained one at
 *  any depth. The root is never dropped however marked it is. */
function prune(v: unknown): [unknown, boolean] {
  if (Array.isArray(v)) {
    let marked = false
    const out = v.map((inner) => {
      const [cleaned, innerMarked] = prune(inner)
      if (innerMarked) marked = true
      return cleaned
    })
    return [out, marked]
  }
  if (v !== null && typeof v === 'object') {
    const out: Record<string, unknown> = {}
    let marked = false
    for (const [k, inner] of Object.entries(v as Record<string, unknown>)) {
      if (typeof inner === 'string' && `"${inner}"` === UNFILLED) {
        marked = true
        continue
      }
      const [cleaned, innerMarked] = prune(inner)
      if (innerMarked) {
        marked = true
        continue
      }
      out[k] = cleaned
    }
    return [out, marked]
  }
  return [v, false]
}

/**
 * Replaces "{json:name}", the whole JSON string literal including its quotes,
 * with a bare number or boolean.
 *
 * A pack cannot write an unquoted placeholder, because a pack file has to be
 * valid JSON before anything substitutes into it. Without this every numeric
 * field comes back as a string and a typed client rejects a response that a
 * curl and a grep call fine.
 */
function scalars(
  s: string,
  captures: Record<string, string>,
  fields: Record<string, string>,
  unsupplied: string,
): string {
  const blank = unsupplied === '' ? 'null' : unsupplied
  const open = '"{json:'
  for (;;) {
    const start = s.indexOf(open)
    if (start < 0) return s
    const end = s.indexOf('}"', start)
    if (end < 0) return s
    const name = s.slice(start + open.length, end)
    s = s.slice(0, start) + scalarFor(name, captures, fields, blank) + s.slice(end + 2)
  }
}

function scalarFor(
  name: string,
  captures: Record<string, string>,
  fields: Record<string, string>,
  unsupplied: string,
): string {
  const bar = name.indexOf('|')
  const fallback = bar < 0 ? null : name.slice(bar + 1)
  const key = bar < 0 ? name : name.slice(0, bar)

  let raw: string
  if (key === 'now') raw = FIXED_NOW
  else if (key.startsWith('request.')) raw = fields[key.slice('request.'.length)] ?? ''
  else raw = captures[key] ?? ''
  if (raw === '' && fallback !== null) raw = fallback

  if (raw === 'true' || raw === 'false') return raw
  // Checked against the JSON number grammar rather than parsed loosely: Inf
  // and NaN written into a response produce a body that is not JSON at all,
  // which is a worse failure than the wrong number.
  if (raw === '' || !/^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/.test(raw)) return unsupplied
  return raw
}
