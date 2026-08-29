// PATCH, and the fact that no two identity providers send the same one.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// RFC 7644 section 3.5.2 describes one operation shape. What arrives is at
// least five, and an implementation that handles the one in the specification
// works against neither of the two providers most customers actually use. Each
// of these is a real message from a real provider:
//
//   Okta deactivating a user, with no path at all and the attribute inside the
//   value:                    {"op":"replace","value":{"active":false}}
//
//   Entra ID doing the same, with the operation capitalised and the boolean
//   sent as a string:         {"op":"Replace","path":"active","value":"False"}
//
//   Entra ID in another mode, wrapping the value in the multi-valued shape:
//                             {"op":"Replace","path":"active","value":[{"value":"False"}]}
//
//   Either of them adding a group member:
//                             {"op":"add","path":"members","value":[{"value":"<id>"}]}
//
//   Entra ID removing one, with a filter inside the path:
//                             {"op":"remove","path":"members[value eq \"<id>\"]"}
//
// The temptation is a chain of special cases at the point of use. That is how
// this becomes unmaintainable and how the sixth variant silently does nothing:
// an operation nobody recognised is skipped, the response is 200, and the
// provider believes the change was applied. Nothing shows up until somebody
// notices a departed employee still has access.
//
// So everything is normalised HERE into one shape, and an operation this does
// not understand is an ERROR rather than a skip. A 400 makes the provider retry
// and shows up in its own logs; a silent skip is invisible on both sides.

import { FilterRefused } from './filter.ts'

export class PatchRefused extends Error {
  readonly scimType: string

  constructor(message: string, scimType = 'invalidSyntax') {
    super(message)
    this.name = 'PatchRefused'
    this.scimType = scimType
  }
}

export interface ValueSelector {
  attribute: string
  operator: 'eq'
  value: string
}

export interface Change {
  op: 'add' | 'replace' | 'remove'
  /** The attribute, lowercased, with any urn prefix stripped. Null when the
   *  operation named none and the value carried the attributes instead. */
  attribute: string | null
  /** The sub-attribute, for a path like name.givenName. */
  sub: string | null
  /** The filter inside a path like members[value eq "x"]. */
  selector: ValueSelector | null
  value: unknown
}

const PATCH_SCHEMA = 'urn:ietf:params:scim:api:messages:2.0:PatchOp'

/** Turns a PATCH body into a flat list of changes. */
export function normalisePatch(body: unknown): Change[] {
  if (body === null || typeof body !== 'object') {
    throw new PatchRefused('The body is not a JSON object.')
  }
  const message = body as { schemas?: unknown; Operations?: unknown; operations?: unknown }

  // The schemas array is checked but a missing one is tolerated, because
  // several providers omit it and refusing would break provisioning over a
  // field that carries no information: the only thing this endpoint accepts is
  // a PatchOp. A schemas array naming something ELSE is refused, because that
  // is a client that believes it is talking to a different API.
  if (Array.isArray(message.schemas) && message.schemas.length > 0) {
    if (!message.schemas.includes(PATCH_SCHEMA)) {
      throw new PatchRefused(
        `A PATCH must declare the ${PATCH_SCHEMA} schema; this one declares ` +
          `${message.schemas.join(', ')}.`,
      )
    }
  }

  // Some clients send "operations". Accepted, because the alternative is a
  // provisioning integration that fails on a capital letter.
  const operations = Array.isArray(message.Operations)
    ? message.Operations
    : Array.isArray(message.operations)
      ? message.operations
      : null
  if (!operations) throw new PatchRefused('The body carries no Operations array.')
  if (operations.length === 0) throw new PatchRefused('The body carries no operations.')
  if (operations.length > 100) {
    throw new PatchRefused('A PATCH may carry at most 100 operations.', 'tooMany')
  }

  return operations.flatMap((operation, index) => one(operation, index))
}

function one(operation: unknown, index: number): Change[] {
  if (operation === null || typeof operation !== 'object') {
    throw new PatchRefused(`Operation ${index} is not an object.`)
  }
  const raw = operation as { op?: unknown; path?: unknown; value?: unknown }

  if (typeof raw.op !== 'string') throw new PatchRefused(`Operation ${index} states no op.`)
  const op = raw.op.toLowerCase()
  if (op !== 'add' && op !== 'replace' && op !== 'remove') {
    throw new PatchRefused(`Operation ${index} has op "${raw.op}", which is not one of add, replace, remove.`)
  }

  if (raw.path !== undefined && typeof raw.path !== 'string') {
    throw new PatchRefused(`Operation ${index} has a path that is not a string.`)
  }
  const path = raw.path as string | undefined

  // No path: the value is an object of attributes, which is Okta's shape.
  // Every key becomes its own change, so the rest of the system sees one shape.
  if (!path) {
    if (op === 'remove') {
      throw new PatchRefused(`Operation ${index} removes nothing: a remove needs a path.`)
    }
    if (raw.value === null || typeof raw.value !== 'object' || Array.isArray(raw.value)) {
      throw new PatchRefused(
        `Operation ${index} has no path, so its value must be an object of attributes.`,
      )
    }
    return Object.entries(raw.value as Record<string, unknown>).map(([attribute, value]) => ({
      op: op as 'add' | 'replace',
      ...splitPath(attribute),
      selector: null,
      value: unwrap(value),
    }))
  }

  return [{ op, ...splitPath(path), value: unwrap(raw.value) }]
}

/**
 * Splits a path into attribute, sub-attribute and selector.
 *
 * Handles the urn prefix providers put in front of core attributes, the
 * `name.givenName` sub-attribute form, and the `members[value eq "x"]` filter
 * form that Entra ID uses to remove one member of a group.
 */
export function splitPath(path: string): {
  attribute: string
  sub: string | null
  selector: ValueSelector | null
} {
  let rest = path.trim()
  if (rest === '') throw new PatchRefused('A path may not be empty.')
  if (rest.length > 512) throw new PatchRefused('That path is unreasonably long.')

  // urn:ietf:params:scim:schemas:core:2.0:User:userName -> userName, and the
  // extension form urn:...:enterprise:2.0:User:department -> department.
  const lastColon = rest.lastIndexOf(':')
  if (rest.toLowerCase().startsWith('urn:') && lastColon > 0) {
    rest = rest.slice(lastColon + 1)
  }

  let selector: ValueSelector | null = null
  const bracket = rest.indexOf('[')
  if (bracket >= 0) {
    if (!rest.endsWith(']')) {
      throw new PatchRefused(`The path ${path} has an unclosed bracket.`)
    }
    const inner = rest.slice(bracket + 1, -1)
    selector = parseSelector(inner, path)
    rest = rest.slice(0, bracket)
  }

  const dot = rest.indexOf('.')
  const attribute = (dot >= 0 ? rest.slice(0, dot) : rest).toLowerCase()
  const sub = dot >= 0 ? rest.slice(dot + 1).toLowerCase() : null

  if (!/^[a-z][a-z0-9_$-]*$/.test(attribute)) {
    throw new PatchRefused(`The path ${path} does not name an attribute.`)
  }
  if (sub !== null && !/^[a-z][a-z0-9_$-]*$/.test(sub)) {
    throw new PatchRefused(`The path ${path} does not name a sub-attribute.`)
  }
  return { attribute, sub, selector }
}

/**
 * The filter inside a path.
 *
 * Only `attr eq "value"` is accepted, which is the only form providers send
 * here and the only one this schema could answer. Anything else is refused by
 * name so the message says what was not understood.
 */
function parseSelector(inner: string, path: string): ValueSelector {
  const match = /^\s*([A-Za-z][A-Za-z0-9_$-]*)\s+(eq)\s+(.+?)\s*$/i.exec(inner)
  if (!match) {
    throw new FilterRefused(
      `The path ${path} contains a filter this does not understand. Only the form ` +
        `attribute eq "value" is supported here.`,
    )
  }
  const literal = match[3]!
  let value: unknown
  if (literal.startsWith('"')) {
    try {
      value = JSON.parse(literal)
    } catch {
      throw new FilterRefused(`The path ${path} has a malformed string.`)
    }
  } else {
    value = literal
  }
  if (typeof value !== 'string') {
    throw new FilterRefused(`The path ${path} compares to something that is not a string.`)
  }
  return { attribute: match[1]!.toLowerCase(), operator: 'eq', value }
}

/**
 * Takes a value out of the wrappers providers put around it.
 *
 * `[{"value": x}]` is the multi-valued shape, and Entra ID uses it for
 * single-valued attributes too. A one-element array carrying only a `value`
 * key is unwrapped; anything longer or shaped differently is left alone,
 * because that IS the multi-valued case and flattening it would silently drop
 * members.
 */
function unwrap(value: unknown): unknown {
  if (
    Array.isArray(value) &&
    value.length === 1 &&
    value[0] !== null &&
    typeof value[0] === 'object' &&
    !Array.isArray(value[0]) &&
    Object.keys(value[0] as object).length === 1 &&
    'value' in (value[0] as object)
  ) {
    return (value[0] as { value: unknown }).value
  }
  return value
}

/**
 * A boolean as providers actually send one.
 *
 * Entra ID sends the string "False" for active, capital F, and JSON.parse gives
 * a truthy string. An implementation that writes `Boolean(value)` deactivates
 * nobody, ever, and the symptom is that departed employees keep their access
 * while every request returns 200.
 */
export function asBoolean(value: unknown, what: string): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'string') {
    const lowered = value.trim().toLowerCase()
    if (lowered === 'true') return true
    if (lowered === 'false') return false
  }
  throw new PatchRefused(`${what} must be true or false, and this was ${JSON.stringify(value)}.`)
}

/** A string value, or a refusal that names the attribute. */
export function asString(value: unknown, what: string): string {
  if (typeof value === 'string') return value
  throw new PatchRefused(`${what} must be a string, and this was ${JSON.stringify(value)}.`)
}
