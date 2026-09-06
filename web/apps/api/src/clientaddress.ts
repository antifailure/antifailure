// What a forwarded header actually contains, which entry of it can be believed,
// and what may go in an inet column.
//
// LIFTED OUT OF server.ts RATHER THAN COPIED, because there are now two files
// that write a caller's address to the database and only one of them had the
// parsing. `x-forwarded-for` is not an address: it is a comma separated LIST of
// them, and each entry may carry a port, and an IPv6 one may be bracketed.
// Postgres refuses every one of those shapes on an `inet` column with a 22P02,
// which surfaces as a 500 on whichever route wrote it.
//
// WHICH ENTRY, and this is the part that was wrong for a year. Every proxy a
// request passes through APPENDS the peer address it saw to the end of the
// list, so the list reads oldest first: whatever the caller put in the header
// themselves, then what the outermost proxy saw, then what the next one saw.
// The only entries that were written by something this deployment trusts are
// the LAST ones, one per trusted proxy, and the client as the innermost
// trusted proxy saw it is the entry that many places from the end.
//
// The first version of this file took the FIRST entry, with a comment saying
// the later entries were the caller's. That is backwards. In production the
// only proxy is the Azure Container Apps ingress, and Microsoft's own ingress
// page says of X-Forwarded-For: "If specified in initial request, it is
// appended to. Only the rightmost IP is provided by Azure Container Apps. Any
// other values must be validated by the user to prevent IP spoofing." So a
// caller who sent `X-Forwarded-For: 10.0.0.1` arrived here as
// `10.0.0.1, <their real address>`, the auth limiter keyed on 10.0.0.1, a new
// value per request was a new bucket per request, and the sign-in audit trail
// recorded whatever they typed. Every limit on sign-in, the magic link, the
// device code and the OAuth callback could be walked around with one header.
//
// The number of trusted proxies is a property of the deployment, not of the
// code, so it is a setting: AF_TRUSTED_PROXY_HOPS, read once at start-up,
// default one, which is Container Apps ingress alone and the Helm chart's one
// ingress controller. An installation that puts a Front Door or a WAF in
// front of the ingress has two, and says so. It has to be the number of
// proxies that EVERY request passes through: a proxy some requests can skip
// is not a trusted hop, because a caller who reaches the inner one directly
// gets to write the entry the count attributes to the outer one.

/** What a deployment gets when it does not say: one proxy, the ingress. */
export const DEFAULT_TRUSTED_PROXY_HOPS = 1

/** More than this is not a proxy chain, it is a typo. */
const MAX_TRUSTED_PROXY_HOPS = 16

/** AF_TRUSTED_PROXY_HOPS as a number, or the default when it is unset.
 *
 *  Refuses anything that is not a small positive integer, at start-up, because
 *  a hop count of zero or "one " would otherwise become NaN, every selection
 *  would come back undefined, every caller would share the "unknown" bucket,
 *  and the first sign of it would be the whole product answering 429. */
export function trustedProxyHopsFrom(value: string | undefined | null): number {
  const raw = value?.trim()
  if (!raw) return DEFAULT_TRUSTED_PROXY_HOPS
  if (!/^\d{1,2}$/.test(raw)) {
    throw new Error(
      `AF_TRUSTED_PROXY_HOPS must be a whole number from 1 to ${MAX_TRUSTED_PROXY_HOPS}; ` +
        `received ${JSON.stringify(value)}.`,
    )
  }
  const hops = Number(raw)
  if (hops < 1 || hops > MAX_TRUSTED_PROXY_HOPS) {
    throw new Error(
      `AF_TRUSTED_PROXY_HOPS must be a whole number from 1 to ${MAX_TRUSTED_PROXY_HOPS}; ` +
        `received ${JSON.stringify(value)}.`,
    )
  }
  return hops
}

/** What the process says at start-up, so the trust model is never a guess. */
export function describeTrustedProxyHops(hops: number): string {
  return hops === 1
    ? 'client addresses are read from the last X-Forwarded-For entry: one trusted proxy (AF_TRUSTED_PROXY_HOPS is not set)'
    : `client addresses are read from the X-Forwarded-For entry ${hops} from the end: ${hops} trusted proxies (AF_TRUSTED_PROXY_HOPS=${hops})`
}

/** The entry the innermost trusted proxy appended, before any validation.
 *
 *  Counted from the RIGHT, `trustedHops` places from the end. A header with
 *  fewer entries than hops was written entirely by trusted proxies, so its
 *  first entry is the earliest trustworthy observation and is what comes
 *  back; nothing to the left of a trusted entry can be the caller's in that
 *  case, because the caller's entries would be the ones pushed leftmost. */
function trustedEntry(forwardedFor: string | undefined, trustedHops: number): string | undefined {
  const entries = (forwardedFor ?? '')
    .split(',')
    .map((entry) => entry.trim())
    .filter((entry) => entry !== '')
  if (entries.length === 0) return undefined
  const hops = Number.isInteger(trustedHops) && trustedHops >= 1 ? trustedHops : DEFAULT_TRUSTED_PROXY_HOPS
  return entries[Math.max(0, entries.length - hops)]
}

/** The caller's address, or undefined when the header does not carry one.
 *
 *  Undefined rather than the raw header, because a column that takes an address
 *  should hold an address or nothing. A value that is plainly not one is a
 *  proxy misconfiguration and storing it would only move the failure to
 *  whoever reads the column. */
export function clientAddress(
  forwardedFor: string | undefined,
  trustedHops: number = DEFAULT_TRUSTED_PROXY_HOPS,
): string | undefined {
  const entry = trustedEntry(forwardedFor, trustedHops)
  if (!entry) return undefined
  // A bracketed IPv6 literal with a port, which is what some proxies send.
  const unbracketed = /^\[(.+)\](?::\d+)?$/.exec(entry)?.[1] ?? entry
  // An IPv4 address with a port, likewise. IPv6 is left alone here: it is
  // full of colons, so stripping at the last one would truncate an address.
  const withoutPort = /^(\d{1,3}(?:\.\d{1,3}){3}):\d+$/.exec(unbracketed)?.[1] ?? unbracketed
  return looksLikeAddress(withoutPort) ? withoutPort : undefined
}

/** The same address, as a rate limit bucket key.
 *
 *  THE SAME SELECTION AS clientAddress, on purpose. There used to be two
 *  functions with two rules, and the rule this one had was "the first entry,
 *  unvalidated", which handed every caller a fresh bucket per request.
 *
 *  A request whose trusted entry is missing or unparseable, which is a direct
 *  connection with no proxy in front of it or a proxy sending something that
 *  is not an address, goes into one shared "unknown" bucket rather than being
 *  exempt. That is the safe direction: such a caller is limited harder, never
 *  softer, and a header full of garbage can never buy its way out. Never write
 *  this to an inet column; that is what clientAddress is for. */
export function clientIP(
  forwardedFor: string | undefined,
  trustedHops: number = DEFAULT_TRUSTED_PROXY_HOPS,
): string {
  return clientAddress(forwardedFor, trustedHops) ?? 'unknown'
}

function looksLikeAddress(value: string): boolean {
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(value)) {
    return value.split('.').every((octet) => Number(octet) <= 255)
  }
  // Deliberately shape rather than grammar: Postgres does the real parsing,
  // and this only has to keep a header value that is plainly not an address
  // out of the statement.
  return /^[0-9a-fA-F:]+$/.test(value) && value.includes(':')
}
