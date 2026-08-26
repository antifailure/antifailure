// The egress policy, in TypeScript, deciding exactly what the engine decides.
//
// There is a Go implementation of this in engine/internal/policy and it is the
// one that decides real traffic. This one exists because the control plane
// renders a policy page and answers "what would happen to this request", and
// doing that by calling the engine would mean the control plane could not
// answer without an environment running.
//
// Two implementations of one decision drift. The one that drifts is this one,
// because nothing here is on the path of a real request, and a policy page
// that confidently shows allow for something the sidecar blocks is worse than
// no page: somebody reads it and believes it.
//
// So the Go implementation emits its answers to schemas/policy-vectors.json
// and the test beside this file requires this code to reproduce every one of
// them, including the exact wording of the reason. A change to either side
// that alters a decision fails a test in both languages.
//
// The rules, restated because they are the reason the file is shaped this way:
// specificity decides rather than order, and the default is block.

export type Mode = 'block' | 'allow' | 'capture' | 'mock' | 'sandbox' | 'synth'

export const ALL_MODES: readonly Mode[] = [
  'block', 'allow', 'capture', 'mock', 'sandbox', 'synth',
]

export interface EgressRule {
  host: string
  mode: Mode
  paths?: string[]
  methods?: string[]
  rate_limit?: string
  credential?: string
  fixtures?: string
  webhook_path?: string
  note?: string
}

export interface Egress {
  default?: Mode
  allow_ipv6?: boolean
  rules?: EgressRule[]
}

export interface Request {
  host: string
  /** Zero or absent means the scheme's default. */
  port?: number
  method?: string
  path?: string
  tls?: boolean
}

export interface Decision {
  mode: Mode
  /** The host pattern of the rule that decided, or empty when the default did. */
  ruleHost: string
  rateLimit: string
  credential: string
  fixtures: string
  webhookPath: string
  matched: boolean
  /** Whether the request reaches the real destination. Only allow and sandbox
   *  do: capture and mock answer inside the environment, and synth invents. */
  allowed: boolean
  reason: string
}

export interface Match {
  rule: EgressRule
  /** Position in the manifest, which breaks ties between equal specificity. */
  index: number
  specificity: number
  why: string
}

type HostMatch = 'any' | 'exactly' | 'address' | 'suffixed'

interface Why {
  host: HostMatch
  suffix?: string
  path?: string
  method?: boolean
}

interface Compiled {
  rule: EgressRule
  index: number
  hostExact: string
  hostSuffix: string
  matchAll: boolean
  ip: string | null
  port: number
  paths: string[]
  methods: Set<string> | null
  specificity: number
}

export class PolicyError extends Error {}

/** Renders a request the way a decision log line does. */
export function requestString(req: Request): string {
  const scheme = req.tls ? 'https' : 'http'
  const port = req.port ?? 0
  let host = req.host
  const isDefaultPort = (req.tls && port === 443) || (!req.tls && port === 80)
  if (port !== 0 && !isDefaultPort) {
    host = host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`
  }
  return `${req.method || 'GET'} ${scheme}://${host}${req.path ?? ''}`
}

/**
 * Parses an address the way Go's net.ParseIP does, and returns a canonical
 * form so that two spellings of one address compare equal.
 *
 * Only what a manifest can hold: dotted-quad IPv4 and colon-separated IPv6.
 * A hostname that happens to be all digits is not an address, and treating it
 * as one would silently widen a rule.
 */
export function parseIP(value: string): string | null {
  if (value.includes(':')) return parseIPv6(value)
  return parseIPv4(value)
}

function parseIPv4(value: string): string | null {
  const parts = value.split('.')
  if (parts.length !== 4) return null
  const octets: number[] = []
  for (const part of parts) {
    // Leading zeros are rejected rather than reinterpreted. Some parsers read
    // 0177.0.0.1 as octal and some as decimal, and a rule that means different
    // things to different readers is a rule nobody can review.
    if (!/^(0|[1-9][0-9]{0,2})$/.test(part)) return null
    const n = Number(part)
    if (n > 255) return null
    octets.push(n)
  }
  return octets.join('.')
}

function parseIPv6(value: string): string | null {
  const halves = value.split('::')
  if (halves.length > 2) return null
  const expand = (s: string): string[] => (s === '' ? [] : s.split(':'))
  const head = expand(halves[0] ?? '')
  const tail = halves.length === 2 ? expand(halves[1] ?? '') : []
  const groups: string[] = []
  if (halves.length === 2) {
    const fill = 8 - head.length - tail.length
    if (fill < 1) return null
    groups.push(...head, ...Array(fill).fill('0'), ...tail)
  } else {
    groups.push(...head)
  }
  if (groups.length !== 8) return null
  const out: string[] = []
  for (const g of groups) {
    if (!/^[0-9a-fA-F]{1,4}$/.test(g)) return null
    out.push(parseInt(g, 16).toString(16))
  }
  return out.join(':')
}

/** Splits a trailing :port the way Go's net.SplitHostPort does. */
function splitHostPort(value: string): { host: string; port: number } | null {
  const colon = value.lastIndexOf(':')
  if (colon < 0) return null
  // A bare IPv6 address has several colons and no port. Bracketed form is the
  // only way to write one with a port.
  if (value.indexOf(':') !== colon && !value.startsWith('[')) return null
  const host = value.startsWith('[') && value[colon - 1] === ']'
    ? value.slice(1, colon - 1)
    : value.slice(0, colon)
  const portText = value.slice(colon + 1)
  if (!/^[0-9]+$/.test(portText)) return null
  return { host, port: Number(portText) }
}

function compileRule(rule: EgressRule, index: number): Compiled {
  const c: Compiled = {
    rule, index, hostExact: '', hostSuffix: '', matchAll: false,
    ip: null, port: 0, paths: [], methods: null, specificity: 0,
  }

  let host = rule.host.trim().toLowerCase()
  if (host === '') throw new PolicyError(`policy: rule ${index}: the host is empty`)

  const split = splitHostPort(host)
  if (split) {
    if (split.port <= 0 || split.port > 65535) {
      throw new PolicyError(`policy: rule ${index} for ${rule.host}: the port is not valid`)
    }
    host = split.host
    c.port = split.port
  }

  // Checked before the trailing dot is trimmed. Otherwise "*." becomes "*" and
  // a typo silently widens a rule to every host, which is the worst way for a
  // security control to fail.
  if (host === '*.') {
    throw new PolicyError(`policy: rule ${index} for ${rule.host}: a wildcard needs a domain after it`)
  }
  host = host.replace(/\.$/, '')

  if (host === '*') {
    c.matchAll = true
  } else if (host.startsWith('*.')) {
    const suffix = host.slice(1) // keeps the leading dot
    if (suffix.slice(1).includes('*')) {
      throw new PolicyError(
        `policy: rule ${index} for ${rule.host}: a wildcard is only allowed at the start, as *.example.com`,
      )
    }
    c.hostSuffix = suffix
  } else {
    if (host.includes('*')) {
      throw new PolicyError(
        `policy: rule ${index} for ${rule.host}: a wildcard is only allowed at the start, as *.example.com`,
      )
    }
    const ip = parseIP(host)
    if (ip !== null) c.ip = ip
    else c.hostExact = host
  }

  for (const p of rule.paths ?? []) {
    c.paths.push(p.startsWith('/') ? p : `/${p}`)
  }
  // Longest first, so the most specific path in a rule decides the match.
  c.paths.sort((a, b) => b.length - a.length)

  if ((rule.methods ?? []).length > 0) {
    c.methods = new Set((rule.methods ?? []).map((m) => m.trim().toUpperCase()))
  }

  c.specificity = specificityOf(c)
  return c
}

/**
 * Ranks a rule.
 *
 * The weights are chosen so that no combination of weaker signals outranks a
 * stronger one: an exact host always beats a wildcard however many paths the
 * wildcard names. That is what makes the ranking explainable in one sentence.
 */
function specificityOf(c: Compiled): number {
  const EXACT_HOST = 1 << 20
  const IP_HOST = 1 << 20 // an address is as specific as an exact name
  const WILDCARD_HOST = 1 << 12
  const PER_PATH_CHAR = 1 << 2
  const HAS_METHOD = 1 << 10
  const HAS_PORT = 1 << 11

  let score = 0
  if (c.hostExact !== '') {
    // No length term. Two exact hosts can never both match one request, so
    // ranking them against each other decides nothing and only makes the
    // printed order look arbitrary.
    score += EXACT_HOST
  } else if (c.ip !== null) {
    score += IP_HOST
  } else if (c.hostSuffix !== '') {
    score += WILDCARD_HOST + c.hostSuffix.length
  }
  if (c.paths.length > 0) score += PER_PATH_CHAR * c.paths[0]!.length
  if (c.methods !== null) score += HAS_METHOD
  if (c.port !== 0) score += HAS_PORT
  return score
}

interface Normalized {
  host: string
  port: number
  method: string
  path: string
}

function normalize(req: Request): Normalized {
  // A trailing dot is the fully qualified form of a name and means the same
  // thing. Not normalizing it is a way to walk straight past a rule.
  const host = req.host.trim().toLowerCase().replace(/\.$/, '')
  const port = req.port && req.port !== 0 ? req.port : req.tls ? 443 : 80
  return {
    host,
    port,
    method: (req.method || 'GET').toUpperCase(),
    path: req.path && req.path !== '' ? req.path : '/',
  }
}

/**
 * Reports whether a request path is under a prefix.
 *
 * The boundary check is what stops /admin from matching /administrator, which
 * is the classic way a path rule turns out to cover far more than its author
 * meant.
 */
function pathMatches(path: string, prefix: string): boolean {
  if (prefix === '/') return true
  const trimmed = prefix.replace(/\/$/, '')
  return path === trimmed || path.startsWith(`${trimmed}/`)
}

function whyString(w: Why): string {
  let out: string
  switch (w.host) {
    case 'any': out = 'it matches every host'; break
    case 'exactly': out = 'the host matches exactly'; break
    case 'address': out = 'the address matches'; break
    case 'suffixed': out = `the host ends in ${w.suffix}`; break
  }
  if (w.method) out += ' and the method is listed'
  if (w.path) out += ` and the path is under ${w.path}`
  return out
}

function matchRule(c: Compiled, n: Normalized): Why | null {
  if (c.port !== 0 && c.port !== n.port) return null

  const w: Why = { host: 'any' }
  if (c.matchAll) {
    w.host = 'any'
  } else if (c.hostExact !== '') {
    if (c.hostExact !== n.host) return null
    w.host = 'exactly'
  } else if (c.ip !== null) {
    // An address rule matches only an address. A name that resolves to the
    // address is a different request, and treating them alike is how a rule
    // ends up covering traffic nobody intended.
    const ip = parseIP(n.host)
    if (ip === null || ip !== c.ip) return null
    w.host = 'address'
  } else if (c.hostSuffix !== '') {
    if (!n.host.endsWith(c.hostSuffix)) return null
    // The wildcard must cover at least one label, so *.example.com does not
    // match example.com itself. An apex and its subdomains are frequently
    // operated differently.
    if (n.host.length <= c.hostSuffix.length) return null
    w.host = 'suffixed'
    w.suffix = c.hostSuffix
  } else {
    return null
  }

  if (c.methods !== null) {
    if (!c.methods.has(n.method)) return null
    w.method = true
  }

  if (c.paths.length > 0) {
    const matched = c.paths.find((p) => pathMatches(n.path, p))
    if (matched === undefined) return null
    w.path = matched
  }
  return w
}

/** Whether a mode can be served without reading the request. Only block and
 *  allow can: one refuses everything to the host, the other forwards it. */
function inspectMode(m: Mode): boolean {
  return m === 'capture' || m === 'mock' || m === 'sandbox' || m === 'synth'
}

export class PolicyEngine {
  private readonly rules: Compiled[]
  private readonly fallback: Mode
  private readonly allowIPv6: boolean

  /**
   * Compiles an egress section.
   *
   * A rule that cannot be compiled is refused rather than skipped. Skipping
   * would produce an engine that silently enforces less than the manifest
   * says, which for a security control looks exactly like working.
   */
  constructor(egress: Egress | null | undefined) {
    this.fallback = egress?.default || 'block'
    this.allowIPv6 = egress?.allow_ipv6 ?? false
    this.rules = (egress?.rules ?? []).map((r, i) => compileRule(r, i))
    // Sorted once so evaluation is a scan. Ties break on manifest order, so a
    // policy prints in the order it was written.
    this.rules.sort((a, b) =>
      a.specificity !== b.specificity ? b.specificity - a.specificity : a.index - b.index,
    )
  }

  evaluate(req: Request): Decision {
    const n = normalize(req)
    for (const c of this.rules) {
      const w = matchRule(c, n)
      if (w === null) continue
      return {
        mode: c.rule.mode,
        ruleHost: c.rule.host,
        rateLimit: c.rule.rate_limit ?? '',
        credential: c.rule.credential ?? '',
        fixtures: c.rule.fixtures ?? '',
        webhookPath: c.rule.webhook_path ?? '',
        matched: true,
        allowed: c.rule.mode === 'allow' || c.rule.mode === 'sandbox',
        reason: ruleReason(c.rule, w),
      }
    }
    return {
      mode: this.fallback,
      ruleHost: '',
      rateLimit: '', credential: '', fixtures: '', webhookPath: '',
      matched: false,
      allowed: this.fallback === 'allow' || this.fallback === 'sandbox',
      reason: defaultReason(this.fallback, n.host),
    }
  }

  /**
   * The decision together with every rule that matched, most specific first.
   *
   * A surprising decision is diagnosable when you can see the rules that lost,
   * and mysterious when you can only see the one that won.
   */
  explain(req: Request): { decision: Decision; chain: Match[] } {
    const n = normalize(req)
    const chain: Match[] = []
    for (const c of this.rules) {
      const w = matchRule(c, n)
      if (w === null) continue
      chain.push({ rule: c.rule, index: c.index, specificity: c.specificity, why: whyString(w) })
    }
    return { decision: this.evaluate(req), chain }
  }

  /** Whether the environment may open IPv6 connections. Off by default: an
   *  IPv6 path that bypasses the proxy is the most common way an egress
   *  control is silently defeated. */
  allowsIPv6(): boolean {
    return this.allowIPv6
  }

  default(): Mode {
    return this.fallback
  }

  /** The compiled rules in evaluation order, which is what a policy view
   *  renders so a reader sees them in the order that decides. */
  compiledRules(): EgressRule[] {
    return this.rules.map((c) => c.rule)
  }

  hosts(): string[] {
    return [...new Set(this.rules.map((c) => c.rule.host))].sort()
  }

  /**
   * Whether decisions for a host need to see inside TLS.
   *
   * A host reached over HTTPS arrives as a tunnel, and a tunnel shows only the
   * name. That is enough when the answer is the same for every request to that
   * host and not enough otherwise. Getting it wrong in the safe direction
   * costs a tunnel that could have been inspected; the other way silently
   * applies a host rule where a path rule was written.
   */
  inspectsHost(host: string, port = 0): boolean {
    const h = host.trim().toLowerCase().replace(/\.$/, '')
    const p = port === 0 ? 443 : port
    if (inspectMode(this.fallback)) return true
    for (const c of this.rules) {
      if (!matchesHostOnly(c, h, p)) continue
      if (c.paths.length > 0 || c.methods !== null || inspectMode(c.rule.mode)) return true
    }
    return false
  }
}

function matchesHostOnly(c: Compiled, host: string, port: number): boolean {
  if (c.port !== 0 && c.port !== port) return false
  if (c.matchAll) return true
  if (c.hostExact !== '') return c.hostExact === host
  if (c.ip !== null) {
    const ip = parseIP(host)
    return ip !== null && ip === c.ip
  }
  if (c.hostSuffix !== '') {
    return host.endsWith(c.hostSuffix) && host.length > c.hostSuffix.length
  }
  return false
}

function defaultReason(mode: Mode, host: string): string {
  if (mode === 'block') {
    return `No rule matches ${host}, and the default is block. Add an egress rule for it if the environment should reach it.`
  }
  return `No rule matches ${host}, so the default of ${mode} applies.`
}

function ruleReason(rule: EgressRule, w: Why): string {
  const base = `The rule for ${rule.host} decided ${rule.mode} because ${whyString(w)}.`
  if (rule.note) return `${base} ${rule.note}`
  if (rule.mode === 'synth') {
    return `${base} A workflow that touches a synthesized response reports unverified rather than passed.`
  }
  return base
}
