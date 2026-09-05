// The origins this control plane will answer a browser from.
//
// A LIST, AND THE REASON IT IS A LIST IS A LIVE OUTAGE RATHER THAN A
// GENERALISATION. This was one origin, `https://antifailure.dev`, and the
// marketing site is served on two hostnames: the apex and `www`. Both are
// custom domains on the same Azure Static Web App, both answer 200, and Static
// Web Apps cannot redirect one to the other because a route rule matches on
// PATH and has no hostname condition at all. So a visitor who typed `www`, or
// followed an old link, sent `origin: https://www.antifailure.dev` and every
// call the site makes was refused 403: the analytics beacon, the enterprise
// contact form, the careers application form. Reported from a phone, on the
// real site, while every check anybody ran was green, because they all asked
// the apex.
//
// Singular was not a security property. It was the assumption that a site is
// served on one hostname, and that assumption is false the moment a `www`
// exists. What IS a security property is EXACT MATCH, and that survives here
// unchanged: an incoming Origin must equal one of these strings in full. There
// is no suffix test anywhere, because `endsWith('antifailure.dev')` is how
// `evil-antifailure.dev` gets allowed, and it is the mistake that is invisible
// in review because the string looks right.
//
// ACCEPTING www DOES NOT MAKE www CANONICAL, and the two rules sit one layer
// apart rather than in tension. www/scripts/check-seo.mjs refuses any built file
// that publishes a spelling of the site other than the apex, and it is right:
// that is about what the site PUBLISHES, one target for every inbound signal.
// This is about which page the API will ANSWER, and a visitor who typed www
// exists whether or not a search engine indexes them. Canonicalising to the apex
// while refusing the hostname a real person arrived on is exactly the outage
// above. Both rules hold at once; holding only one of them is what broke it.
//
// Whatever is allowed here is still one exact origin per response. A CORS
// `Access-Control-Allow-Origin` header carries a single origin, never a list,
// so matchSiteOrigin returns the ONE entry that matched and that is what gets
// echoed. Every route that echoes it also sends `vary: origin`, which matters
// more with two allowed origins than it did with one: without it a shared cache
// can serve the apex's allow header to a request that arrived on www.
//
// Four values this refuses, each because it can only ever fail:
//
//   A PATH OR A QUERY. `Origin` is scheme, host and port and nothing else, so a
//   configured value carrying a path could never equal the header and the route
//   would silently allow nobody. That failure looks exactly like a browser
//   problem and is a typo.
//
//   A WILDCARD. There is no form of this variable that means "any origin". The
//   routes it configures write to the database from an unauthenticated caller;
//   `*` there is not a looser setting, it is a different feature.
//
//   ANYTHING BUT http OR https. A scheme the browser will not send as an Origin
//   is a value that can only ever fail to match.
//
//   AN EMPTY ENTRY. `https://antifailure.dev,` is a trailing comma somebody
//   left behind, not a request to allow the empty origin, and quietly dropping
//   it would let `,,,` read as a configured list.
//
// http is allowed alongside https for exactly one case, which is somebody
// running the marketing site on localhost against a control plane on localhost.
// Refusing it would mean the form can only be developed against production.

/** One entry, parsed rather than trusted. Exported for the tests that drive the
 *  refusals one at a time; the server reads siteOriginsFrom. */
export function siteOriginFrom(value: string | undefined | null): string | undefined {
  const raw = value?.trim()
  if (!raw) return undefined
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new Error('AF_SITE_ORIGIN must be absolute http or https origins, such as https://example.com.')
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new Error('AF_SITE_ORIGIN must be absolute http or https origins, such as https://example.com.')
  }
  if (url.pathname !== '/' || url.search || url.hash) {
    throw new Error(
      `AF_SITE_ORIGIN must be origins and nothing more: ${url.origin}, not ${raw}. ` +
        'A browser sends scheme, host and port in the Origin header, so a value carrying a path ' +
        'can never match it and the route would allow nobody while looking configured.',
    )
  }
  // url.origin rather than the string that was typed. It normalises the case of
  // the host and drops a default port, which is what the browser will send.
  return url.origin
}

/**
 * The whole variable: one origin, or several separated by commas.
 *
 * A single origin with no comma parses to a list of one, so every deployment
 * that set this before the site grew a second hostname keeps working with the
 * value it already has. Order is preserved and duplicates are dropped, because
 * the apex written twice is a configuration mistake rather than two origins and
 * echoing which one matched must not depend on which copy was reached first.
 */
export function siteOriginsFrom(value: string | undefined | null): string[] {
  const raw = value?.trim()
  if (!raw) return []
  const origins: string[] = []
  for (const part of raw.split(',')) {
    if (part.trim() === '') {
      throw new Error(
        `AF_SITE_ORIGIN has an empty entry: ${raw}. It is a comma separated list of whole ` +
          'origins, so a stray comma is a typo rather than a request to allow nothing.',
      )
    }
    const origin = siteOriginFrom(part)
    // siteOriginFrom only returns undefined for a blank value, which the check
    // above has already refused. The guard is here so a future edit to it
    // cannot push undefined into the list unnoticed.
    if (!origin) continue
    if (!origins.includes(origin)) origins.push(origin)
  }
  return origins
}

/**
 * The ONE comparison, so there is one place to get it wrong.
 *
 * Every route on this server that answers a cross origin browser calls this
 * rather than comparing for itself. Before the list, four of them compared
 * against `options.siteOrigin` independently: the analytics beacon, the lead
 * preflight and route, the careers form's origin guard, and the middleware that
 * keeps a careers rate refusal readable. Four copies of a rule is four places a
 * fifth route can be added without one, and a route that forgot the rule does
 * not fail: it silently answers nobody, which is exactly what www did.
 *
 * Returns the matched origin to echo, or null to refuse. Never returns the
 * list: `Access-Control-Allow-Origin` takes one origin and a browser rejects a
 * header carrying two.
 */
export function matchSiteOrigin(
  requestOrigin: string | undefined | null,
  allowed: readonly string[],
): string | null {
  if (!requestOrigin || allowed.length === 0) return null
  // Exact equality on the whole origin, never a suffix or a prefix test.
  return allowed.includes(requestOrigin) ? requestOrigin : null
}

/** What to say at start-up. Both absences look exactly like working software,
 *  so the process says which one it is out loud on every boot. */
export function siteOriginsSummary(origins: readonly string[]): string {
  if (origins.length === 0) {
    return 'AF_SITE_ORIGIN is not set: no other origin may post a beacon, an enterprise lead or a job application, so a form on the marketing site cannot submit'
  }
  return `the marketing site at ${origins.join(' and ')} may post analytics beacons, enterprise leads and job applications`
}
