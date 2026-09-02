// What a browser gets when a route refuses it.
//
// This API answers JSON, and for almost everything it serves that is right:
// the caller is the console's fetch, the `af` binary, a CI job or a webhook,
// and a JSON body is the thing each of them reads.
//
// A handful of routes are different, and the difference is not a detail. They
// are opened by a person, in the address bar, by following a link or by coming
// back from somebody else's redirect:
//
//   GET /auth/github               pressed on the marketing site
//   GET /auth/github/callback      GitHub sends the browser here
//   GET /auth/email/callback       a link in an email
//   GET /exports/deletion          a link handed to somebody whose org is gone
//
// When one of those refuses, a JSON body is not an error message. It is a line
// of punctuation and quoting rendered as plain text on a white page, with no
// heading, no explanation of what to do next and no way back. The refused
// sign-in was the worst of them: the primary call to action on the marketing
// site ended at
//
//   {"error":"This installation is not open for sign-ups. Ask an owner to ..."}
//
// after the visitor had already authorised an OAuth application on their real
// GitHub account. That is the moment somebody decides a product is not real.
//
// So these routes negotiate. A caller that asked for HTML gets a page; every
// other caller, including `curl`, the console's own fetch and anything sending
// a wildcard Accept, gets exactly the JSON body it got before. The sentence in
// the JSON and the sentence on the page come from the same place, so the two
// cannot drift.
//
// Why the page is written here, by hand, in a file with no dependencies:
//
// It has to render in a deployment that has no console build at all, which is
// a real and supported way to run this server. It has to render before a
// session exists. And it has to render when the thing that just failed is the
// only way in. A page that needed the application to be working would be
// missing on exactly the occasions it is for.
//
// The last time this repository hand wrote HTML in the API it shipped unstyled
// for a week, because the global header middleware overwrote the route's
// Content-Security-Policy with one that had no style-src, and the test checked
// a substring both policies contained. Two things follow from that and both are
// deliberate below: the policy is one exported constant, asserted whole rather
// than by substring, and the only inline style carries a nonce so the policy
// does not have to say 'unsafe-inline' to let its own stylesheet run.

import { randomBytes } from 'node:crypto'

/** A link on the page. There is at most one primary, and it is the way out. */
export interface ProblemAction {
  href: string
  label: string
  tone?: 'primary' | 'secondary'
}

export interface Problem {
  /** The status both representations answer with. */
  status: 400 | 401 | 403 | 404 | 409 | 429 | 503
  /** The heading. What happened, in words, not a code. */
  title: string
  /** The body, one string per paragraph. */
  body: string[]
  /** Where the person can go. Empty is allowed and means there is nowhere. */
  actions?: ProblemAction[]
  /**
   * What a machine gets, under the `error` key, exactly as it did before this
   * file existed. Changing it would change an API answer, which is a different
   * decision from giving a browser a page.
   */
  error: string
  /** Extra fields to merge into the JSON body, for the routes that carry one. */
  json?: Record<string, unknown>
}

/**
 * Whether this caller asked for a page.
 *
 * Only an explicit `text/html` counts. A browser navigation always sends it;
 * a wildcard Accept, which is what `fetch` with no Accept and `curl` both
 * send, is not a person looking at a screen. Reading a wildcard as "anything,
 * so give it HTML" would change what every existing script receives, which is
 * the one thing this must not do.
 */
export function wantsHtml(accept: string | undefined | null): boolean {
  if (!accept) return false
  return accept
    .split(',')
    .some((part) => part.trim().toLowerCase().split(';')[0] === 'text/html')
}

/**
 * The policy this page is served under.
 *
 * Everything is shut except the one stylesheet, which is allowed by nonce
 * rather than by 'unsafe-inline': the page carries exactly one <style> element
 * and no script at all, so there is nothing here that needs a blanket
 * permission. No image, no font, no connection, nowhere to submit, and it
 * cannot be framed.
 */
export function problemCsp(nonce: string): string {
  return [
    "default-src 'none'",
    `style-src 'nonce-${nonce}'`,
    "form-action 'none'",
    "base-uri 'none'",
    "frame-ancestors 'none'",
  ].join('; ')
}

const ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
}

/** Every value interpolated below goes through this, including the ones that
 *  are literals today. A page that escapes only the fields somebody remembered
 *  are dynamic is one refactor away from not escaping them. */
function esc(value: string): string {
  return value.replace(/[&<>"']/g, (ch) => ESCAPES[ch]!)
}

/**
 * The look.
 *
 * The same palette, type scale, radii and focus ring as the console, taken
 * from console/app/globals.css and console/components/ui.tsx, because a person
 * who has just come from antifailure.dev should not land somewhere that looks
 * like a different company. The values are written out rather than read from a
 * custom property so that the whole page is one self contained file.
 *
 * Deliberately not a red box. The most common reason to see this page is not
 * having been invited yet, which is not a fault and should not be dressed as
 * one. It is the same quiet paper, ink and rule as every other standalone
 * screen in the product, with the one green mark the brand uses.
 *
 * Light only, and it says so. Nothing in this product has a dark theme: the
 * console, the marketing site and this page all commit to one ground. The
 * declaration stops a browser in dark mode inventing its own version of it.
 */
const STYLE = `
:root { color-scheme: light; }
* { box-sizing: border-box; }
html, body { margin: 0; background: #f7f7f5; color: #101010; }
body {
  font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  -webkit-font-smoothing: antialiased;
  min-width: 320px;
}
main {
  display: grid;
  place-items: center;
  min-height: 100dvh;
  padding: 40px 20px;
}
.col { width: 100%; max-width: 440px; }
.mark { display: block; width: 36px; height: 36px; }
h1 {
  margin: 28px 0 0;
  font-size: 28px;
  font-weight: 600;
  line-height: 1.125;
  letter-spacing: -0.04em;
  color: #101010;
  text-wrap: balance;
}
p {
  margin: 12px 0 0;
  max-width: 52ch;
  font-size: 13.5px;
  line-height: 24px;
  color: #575752;
}
.actions { margin-top: 28px; display: grid; gap: 12px; }
a.action {
  display: inline-flex;
  height: 44px;
  align-items: center;
  justify-content: center;
  gap: 10px;
  width: 100%;
  padding: 0 16px;
  border-radius: 5px;
  border: 1px solid transparent;
  font-size: 14px;
  font-weight: 500;
  text-decoration: none;
  transition: background-color 120ms ease, border-color 120ms ease;
}
a.primary { background: #101010; color: #ffffff; }
a.primary:hover { background: #2b2b2b; }
a.secondary { background: #ffffff; color: #101010; border-color: rgba(16, 16, 16, 0.1); }
a.secondary:hover { border-color: rgba(16, 16, 16, 0.22); }
a:focus-visible { outline: 2px solid #101010; outline-offset: 2px; border-radius: 3px; }
@media (max-width: 400px) {
  h1 { font-size: 25px; }
}
`.trim()

/** The brand mark, the same path the console and the site draw. */
const MARK =
  '<svg class="mark" viewBox="0 0 18 18" fill="none" aria-hidden="true">' +
  '<path d="M1.8 6.4V1.8H6.4M11.6 1.8H16.2V6.4M16.2 11.6V16.2H11.6M6.4 16.2H1.8V11.6" ' +
  'stroke="#33bf00" stroke-width="2.1" stroke-linecap="square"/></svg>'

/** The page, as a complete document. */
export function problemHtml(problem: Problem, nonce: string): string {
  const paragraphs = problem.body.map((line) => `      <p>${esc(line)}</p>`).join('\n')
  const actions = (problem.actions ?? []).filter((a) => a.href)
  const buttons = actions.length
    ? '\n      <div class="actions">\n' +
      actions
        .map(
          (a) =>
            `        <a class="action ${a.tone === 'secondary' ? 'secondary' : 'primary'}" ` +
            `href="${esc(a.href)}">${esc(a.label)}</a>`,
        )
        .join('\n') +
      '\n      </div>'
    : ''
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="robots" content="noindex">
    <title>${esc(problem.title)}</title>
    <style nonce="${esc(nonce)}">${STYLE}</style>
  </head>
  <body>
    <main>
      <div class="col" role="alert">
        ${MARK}
        <h1>${esc(problem.title)}</h1>
${paragraphs}${buttons}
      </div>
    </main>
  </body>
</html>
`
}

/** The narrow slice of Hono's context this needs, so the module stays testable
 *  without one. */
export interface Answering {
  req: { header(name: string): string | undefined }
  header(name: string, value: string): void
  json(body: unknown, status: number): Response
  body(body: string, status: number): Response
}

/**
 * Answers one refusal in whichever representation the caller asked for.
 *
 * The JSON branch is byte for byte what these routes returned before, which is
 * what makes this safe to apply to a route that already has clients.
 */
export function problem(c: Answering, p: Problem): Response {
  if (!wantsHtml(c.req.header('accept'))) {
    return c.json({ error: p.error, ...(p.json ?? {}) }, p.status)
  }
  const nonce = randomBytes(16).toString('base64')
  c.header('content-type', 'text/html; charset=utf-8')
  c.header('cache-control', 'no-store')
  // Set here rather than left to the global middleware, which applies a policy
  // with no style-src at all. See the note at the top of this file.
  c.header('content-security-policy', problemCsp(nonce))
  return c.body(problemHtml(p, nonce), p.status)
}
