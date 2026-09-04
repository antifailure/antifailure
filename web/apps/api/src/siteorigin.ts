// The one other origin this control plane will answer a browser from.
//
// A whole origin, parsed rather than trusted, and compared later by exact
// string equality. Three things this refuses and why each one matters:
//
//   A PATH OR A QUERY. `Origin` is scheme, host and port and nothing else, so a
//   configured value carrying a path could never equal the header and the route
//   would silently allow nobody. That failure looks exactly like a browser
//   problem and is a typo.
//
//   A WILDCARD. There is no form of this variable that means "any origin". The
//   route it configures writes to the database from an unauthenticated caller;
//   `*` there is not a looser setting, it is a different feature.
//
//   ANYTHING BUT http OR https. A scheme the browser will not send as an Origin
//   is a value that can only ever fail to match.
//
// http is allowed alongside https for exactly one case, which is somebody
// running the marketing site on localhost against a control plane on localhost.
// Refusing it would mean the form can only be developed against production.
export function siteOriginFrom(value: string | undefined | null): string | undefined {
  const raw = value?.trim()
  if (!raw) return undefined
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new Error('AF_SITE_ORIGIN must be an absolute http or https origin, such as https://example.com.')
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new Error('AF_SITE_ORIGIN must be an absolute http or https origin, such as https://example.com.')
  }
  if (url.pathname !== '/' || url.search || url.hash) {
    throw new Error(
      `AF_SITE_ORIGIN must be an origin and nothing more: ${url.origin}, not ${raw}. ` +
        'A browser sends scheme, host and port in the Origin header, so a value carrying a path ' +
        'can never match it and the route would allow nobody while looking configured.',
    )
  }
  // url.origin rather than the string that was typed. It normalises the case of
  // the host and drops a default port, which is what the browser will send.
  return url.origin
}
