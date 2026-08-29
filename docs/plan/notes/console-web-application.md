# The console is the web application

`STATUS.md` said so all along. Items 8.3, 8.4 and 8.8 are each "the view is the
web application", and 8.9 is `Design system | planned | The web application`.
What was deployed instead was 1,400 lines of hand-written HTML in
`web/apps/api/src/console/`, built without reading those lines. It worked, it
was fast, it escaped by default, and it did not look like the product: a bare
card on an empty page, in a different green from the marketing site, with none
of its type treatment, logo or texture.

It is now a Next.js application in `console/`, and this note records the three
decisions that shaped it and the four things that only a browser found.

## Where it is served, and why that is not a preference

The session is a `SameSite=Lax` cookie set by the control plane's origin.

Serving the console from `antifailure.dev` would make every data call
cross-origin, which needs `SameSite=None` on that cookie and a CORS policy that
allows credentials. That widens the cross-site request surface of **every**
endpoint on the API -- ingestion, provider keys, device approval -- in order to
move a dashboard to a second hostname. The trade is not worth making.

So the console is a static export that the control plane's own Hono process
serves from `/app/console-out`: one origin, one cookie policy, one process. See
`web/apps/api/src/console/static.ts`.

The consequence to design around: a static export has no server, so there are
no dynamic route segments. A detail view is a query string on a static page --
`/runs?run=...`, `/environments?env=...`, `/masking?repo=...` -- never
`/runs/[runId]`, which cannot be exported without knowing every id at build
time. Query strings turn out to be the better answer anyway: a specific run is
a link somebody can send.

## The one concession in the Content-Security-Policy

`script-src 'self' 'unsafe-inline'`, and it is stated in the source rather than
buried. A Next.js static export bootstraps from an inline `<script>` whose
contents change with every build, so there is no stable hash to allow-list and
no server to mint a nonce. The alternatives were keeping the server-rendered
console or shipping a policy that stops the application starting.

Everything else stays shut: no `unsafe-eval`, no third-party origin,
`connect-src 'self'`, `frame-ancestors 'none'`. The previous console had a
stricter policy and rendered as unstyled text for a week, which is a useful
reminder that a policy nobody checks in a browser is not a security property.

## Where the API had to grow

Three procedures, because three screens were useless without them:

| | |
| --- | --- |
| `repositories.list` | `masking.rules`, `masking.attestations` and `network.effective` all take a repository full name, and the only way to learn one was to read an environment row. A tenant with a repository connected and nothing built yet could not see its own masking rules. |
| `runs.recent` | `runs.list` takes an `envId`, which answers "what has this environment done" and not "what happened". Doing that by listing environments and fanning out is N+1 against a replica, so it is one query. |
| `runs.get` | A detail page that had to scan a list to title itself breaks the moment the run falls off that list. |

And four JSON endpoints under `/console/api/providers`, because `/v1/providers`
authenticates a Bearer token for `af provider` and a browser has a cookie.
Teaching one endpoint two authentication schemes is how an endpoint ends up
accepting the weaker one, so the browser gets its own, with the same role gate
and the same audit origin.

## What only a browser found

Four things, none of which a passing build would have shown.

**Every navigation blanked the page.** `Shell` was inside each page rather than
in a layout. A layout survives a client-side navigation and a page does not, so
clicking the sidebar unmounted the shell, refetched the session, and cleared the
whole window -- navigation rail included -- until it came back. It looked like
the application crashed on every click. Fixed by moving the chrome into
`app/(app)/layout.tsx`, which is also what makes one session fetch serve every
screen instead of three.

**The network page crashed the first time anybody pressed its button.** The
Explain form ran its query through a hook short-circuited to
`Promise.resolve(null)` until a question had been asked, which put the state
machine somewhere it should never have been able to go: status `ready` with no
data. On the first render after submitting, before the effect ran, the previous
`ready` was still current and the render read `.inspectsHost` off null. Next's
client-exception screen, white page, no recovery. The query now mounts with the
question, and `Loaded` refuses to hand a null to its children so the shape of
that mistake shows a skeleton instead of a white screen.

**The CSRF header was wrong.** The client sent `x-af-csrf`; the server has
always read `x-antifailure-csrf`. Every mutation in the console -- storing a
key, setting a cap, changing a role, approving a terminal -- answered 403. The
tests caught it, which is the only reason it is in this list rather than in
production.

**The buttons did not line up with their inputs.** `items-end` aligned them to
the bottom of the field's hint text rather than to the input beside it.

## What is proven

Against a real Postgres, with a seeded tenant, in Chrome:

- every page renders with data and with its empty state;
- storing a provider key, then setting a cap, end to end, with the audit entry
  and the masked last four coming back;
- `af login`'s whole device flow: `POST /auth/device/code`, approval in the new
  page, `POST /auth/device/token` returning a token carrying exactly the scopes
  that were shown on the approval screen;
- 390 pixels wide: no horizontal overflow, the rail becomes a drawer with
  scroll-lock and Escape, and wide tables scroll inside their own box with
  CSS-only shadows at the edges so a cut-off row reads as scrollable rather than
  as truncated.

## What is not

Nobody has used it against the hosted deployment, because sign-in there has
never worked: the OAuth client id and secret in Key Vault were both the string
`PLACEHOLDER-not-a-real-oauth-app`. The client id is now the real one. The
secret needs a person at the keyboard, and until it is set the only way in is a
session minted by hand.
