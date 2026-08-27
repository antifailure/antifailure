// The SCIM filter grammar, parsed strictly and never concatenated into SQL.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A filter arrives as a string in a query parameter, from a provisioning client
// holding a bearer token, and it has to become a WHERE clause. That sentence
// describes an injection vulnerability unless something stops it, and the thing
// that stops it here is that this module produces an ABSTRACT SYNTAX TREE and
// nothing else. It never emits SQL, never emits a fragment of SQL, and never
// sees a database. The translation to a query happens elsewhere, over a closed
// set of known attributes, with every literal bound as a parameter. There is no
// path by which a byte of the filter string reaches the database as anything
// but a bound value.
//
// The second reason for a real parser rather than a regular expression: a
// filter this does not understand has to be REFUSED, with the SCIM error the
// specification defines, and not quietly ignored. A provisioning client that
// asks "which user has userName ada@example.test" and is answered with every
// user, because the filter was dropped, will create a duplicate of everybody.
// Silently ignoring a filter is worse than refusing it.
//
// The grammar implemented is RFC 7644 section 3.4.2.2, less valuePath
// (`emails[type eq "work"]`) in the query filter position, which no provider
// sends there and which would need a sub-language over multi-valued attributes
// this schema does not have. It is refused by name so the message says what is
// missing rather than "parse error at 7".

export class FilterRefused extends Error {
  /** The SCIM scimType for the error response body. */
  readonly scimType: string

  constructor(message: string, scimType = 'invalidFilter') {
    super(message)
    this.name = 'FilterRefused'
    this.scimType = scimType
  }
}

export type CompareOperator = 'eq' | 'ne' | 'co' | 'sw' | 'ew' | 'gt' | 'lt' | 'ge' | 'le'

export type Filter =
  | { kind: 'compare'; attribute: string; operator: CompareOperator; value: string | number | boolean | null }
  | { kind: 'present'; attribute: string }
  | { kind: 'and'; left: Filter; right: Filter }
  | { kind: 'or'; left: Filter; right: Filter }
  | { kind: 'not'; inner: Filter }

const COMPARE: ReadonlySet<string> = new Set(['eq', 'ne', 'co', 'sw', 'ew', 'gt', 'lt', 'ge', 'le'])

type Token =
  | { type: 'word'; value: string }
  | { type: 'string'; value: string }
  | { type: 'number'; value: number }
  | { type: '('; }
  | { type: ')'; }

/**
 * Bounds, because this is reached by a bearer token and a parser with no bounds
 * is a way to spend the server's memory and stack from outside.
 */
const MAX_LENGTH = 2048
const MAX_DEPTH = 16

export function parseFilter(input: string): Filter {
  if (typeof input !== 'string') throw new FilterRefused('The filter is not a string.')
  if (input.length > MAX_LENGTH) {
    throw new FilterRefused(`The filter is longer than ${MAX_LENGTH} characters.`, 'tooMany')
  }
  const tokens = tokenize(input)
  if (tokens.length === 0) throw new FilterRefused('The filter is empty.')

  const parser = { tokens, at: 0, depth: 0 }
  const filter = parseOr(parser)
  if (parser.at !== tokens.length) {
    throw new FilterRefused(`The filter has trailing input after position ${parser.at}.`)
  }
  return filter
}

function tokenize(input: string): Token[] {
  const tokens: Token[] = []
  let i = 0

  while (i < input.length) {
    const c = input[i]!

    if (c === ' ' || c === '\t' || c === '\n' || c === '\r') {
      i += 1
      continue
    }
    if (c === '(' || c === ')') {
      tokens.push({ type: c } as Token)
      i += 1
      continue
    }
    if (c === '[' || c === ']') {
      throw new FilterRefused(
        'This filter uses a value path such as emails[type eq "work"], which is not supported. ' +
          'Filter on a single attribute instead.',
      )
    }

    if (c === '"') {
      // A JSON string, decoded with JSON.parse rather than by hand. Escape
      // handling written by hand is where a parser lets a quote through.
      let j = i + 1
      let escaped = false
      while (j < input.length) {
        const d = input[j]!
        if (escaped) {
          escaped = false
        } else if (d === '\\') {
          escaped = true
        } else if (d === '"') {
          break
        }
        j += 1
      }
      if (j >= input.length) throw new FilterRefused('The filter has an unterminated string.')
      let value: unknown
      try {
        value = JSON.parse(input.slice(i, j + 1))
      } catch {
        throw new FilterRefused('The filter has a string this cannot read.')
      }
      if (typeof value !== 'string') throw new FilterRefused('The filter has a malformed string.')
      tokens.push({ type: 'string', value })
      i = j + 1
      continue
    }

    if (/[-0-9]/.test(c)) {
      const match = /^-?\d+(\.\d+)?/.exec(input.slice(i))
      if (!match) throw new FilterRefused(`The filter has a malformed number at position ${i}.`)
      tokens.push({ type: 'number', value: Number(match[0]) })
      i += match[0].length
      continue
    }

    // An attribute path or a keyword. Colons appear in the urn: prefixes
    // providers put in front of attribute names, and dots in sub-attributes.
    const match = /^[A-Za-z][A-Za-z0-9_.:$-]*/.exec(input.slice(i))
    if (!match) {
      throw new FilterRefused(`The filter has an unexpected character at position ${i}.`)
    }
    tokens.push({ type: 'word', value: match[0] })
    i += match[0].length
  }

  return tokens
}

interface Parser {
  tokens: Token[]
  at: number
  depth: number
}

function peek(p: Parser): Token | null {
  return p.tokens[p.at] ?? null
}

function keyword(p: Parser): string | null {
  const token = peek(p)
  return token && token.type === 'word' ? token.value.toLowerCase() : null
}

function parseOr(p: Parser): Filter {
  let left = parseAnd(p)
  while (keyword(p) === 'or') {
    p.at += 1
    left = { kind: 'or', left, right: parseAnd(p) }
  }
  return left
}

function parseAnd(p: Parser): Filter {
  let left = parseUnary(p)
  while (keyword(p) === 'and') {
    p.at += 1
    left = { kind: 'and', left, right: parseUnary(p) }
  }
  return left
}

function parseUnary(p: Parser): Filter {
  if (keyword(p) === 'not') {
    p.at += 1
    const next = peek(p)
    if (!next || next.type !== '(') {
      throw new FilterRefused('"not" must be followed by a parenthesised filter.')
    }
    return { kind: 'not', inner: parseGroup(p) }
  }
  const token = peek(p)
  if (token?.type === '(') return parseGroup(p)
  return parseCompare(p)
}

function parseGroup(p: Parser): Filter {
  p.depth += 1
  if (p.depth > MAX_DEPTH) {
    throw new FilterRefused(`The filter nests deeper than ${MAX_DEPTH} levels.`, 'tooMany')
  }
  p.at += 1 // (
  const inner = parseOr(p)
  const close = peek(p)
  if (!close || close.type !== ')') throw new FilterRefused('The filter has an unclosed bracket.')
  p.at += 1
  p.depth -= 1
  return inner
}

function parseCompare(p: Parser): Filter {
  const attributeToken = peek(p)
  if (!attributeToken || attributeToken.type !== 'word') {
    throw new FilterRefused('The filter expects an attribute name.')
  }
  // The long urn: prefix providers sometimes send in front of a core attribute
  // is stripped, so `urn:ietf:params:scim:schemas:core:2.0:User:userName` and
  // `userName` mean the same thing rather than one of them silently matching
  // nothing.
  const attribute = attributeToken.value.replace(/^urn:[^:]*(:[^:]+)*:(?=[A-Za-z]+$)/, '')
  p.at += 1

  const operatorToken = peek(p)
  if (!operatorToken || operatorToken.type !== 'word') {
    throw new FilterRefused(`The filter expects an operator after ${attribute}.`)
  }
  const operator = operatorToken.value.toLowerCase()
  p.at += 1

  if (operator === 'pr') return { kind: 'present', attribute }

  if (!COMPARE.has(operator)) {
    throw new FilterRefused(
      `The filter uses the operator "${operatorToken.value}", which is not one this understands.`,
    )
  }

  const valueToken = peek(p)
  if (!valueToken) throw new FilterRefused(`The filter expects a value after ${operator}.`)
  p.at += 1

  let value: string | number | boolean | null
  if (valueToken.type === 'string') value = valueToken.value
  else if (valueToken.type === 'number') value = valueToken.value
  else if (valueToken.type === 'word') {
    const lowered = valueToken.value.toLowerCase()
    if (lowered === 'true') value = true
    else if (lowered === 'false') value = false
    else if (lowered === 'null') value = null
    else {
      throw new FilterRefused(
        `The filter compares ${attribute} to the bare word "${valueToken.value}". A string value ` +
          `must be quoted.`,
      )
    }
  } else {
    throw new FilterRefused(`The filter expects a value after ${operator}.`)
  }

  return { kind: 'compare', attribute, operator: operator as CompareOperator, value }
}

/** Every attribute a filter mentions, so a caller can refuse unknown ones. */
export function attributesIn(filter: Filter): string[] {
  switch (filter.kind) {
    case 'compare':
    case 'present':
      return [filter.attribute]
    case 'not':
      return attributesIn(filter.inner)
    case 'and':
    case 'or':
      return [...attributesIn(filter.left), ...attributesIn(filter.right)]
  }
}
