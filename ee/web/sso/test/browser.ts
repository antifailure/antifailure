// What the Keycloak end to end test needs from a browser: whether a redirect has
// reached a given origin, and the text of a value in an HTML form.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// In a module of their own, with a unit test, because keycloak.test.ts runs only
// against a real Keycloak (AF_KEYCLOAK_URL), which CI does not start. A rule
// written inside that file is a rule nothing in CI exercises.

/** Whether `url` is on exactly `origin`: scheme, host and port. */
export function isOrigin(url: string, origin: string): boolean {
  // An origin comparison, never a prefix. A prefix check reads
  // https://antifailure.test.evil as home, and it is invisible in review
  // because the string looks right.
  try {
    return new URL(url).origin === origin
  } catch {
    return false
  }
}

/** The handful of entities that appear in a form value in an HTML page. */
export function decodeHtml(value: string): string {
  // &amp; last. Decoding it first turns the text "&lt;b&gt;", which a page
  // writes as &amp;lt;b&amp;gt;, into "<b>".
  return value
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#x27;|&apos;/g, "'")
    .replace(/&#x2F;/g, '/')
    .replace(/&amp;/g, '&')
}
