// What a forwarded header actually contains, and what may go in an inet column.
//
// LIFTED OUT OF server.ts RATHER THAN COPIED, because there are now two files
// that write a caller's address to the database and only one of them had the
// parsing. `x-forwarded-for` is not an address: it is a comma separated LIST of
// them, oldest last, and each entry may carry a port, and an IPv6 one may be
// bracketed. Postgres refuses every one of those shapes on an `inet` column with
// a 22P02, which surfaces as a 500 on whichever route wrote it.
//
// The route that found this the second time was the impersonation start, which
// is the worst place to learn it: a support engineer behind two proxies would
// get "the control plane could not complete this request" on the one action
// they came to take, and nothing about the message would point at a header.

/** The caller's address, or undefined when the header does not carry one.
 *
 *  Undefined rather than the raw header, because a column that takes an address
 *  should hold an address or nothing. A value that is plainly not one is a
 *  proxy misconfiguration and storing it would only move the failure to
 *  whoever reads the column. */
export function clientAddress(forwardedFor: string | undefined): string | undefined {
  const first = (forwardedFor ?? '').split(',')[0]?.trim()
  if (!first) return undefined
  // A bracketed IPv6 literal with a port, which is what some proxies send.
  const unbracketed = /^\[(.+)\](?::\d+)?$/.exec(first)?.[1] ?? first
  // An IPv4 address with a port, likewise. IPv6 is left alone here: it is
  // full of colons, so stripping at the last one would truncate an address.
  const withoutPort = /^(\d{1,3}(?:\.\d{1,3}){3}):\d+$/.exec(unbracketed)?.[1] ?? unbracketed
  return looksLikeAddress(withoutPort) ? withoutPort : undefined
}

/** The first entry of the list, whatever it is, for keying a rate limit bucket.
 *
 *  Deliberately NOT validated: a bucket key is a string and an unparseable one
 *  is still a caller who should be limited. Never write this to an inet column;
 *  that is what clientAddress is for. */
export function clientIP(forwardedFor: string | undefined): string {
  return (forwardedFor ?? '').split(',')[0]?.trim() || 'unknown'
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
