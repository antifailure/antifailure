# The console should be the web application, and is not

`STATUS.md` has said so all along. Items 8.3, 8.4 and 8.8 are all "the view is
the web application", and 8.9 is `Design system | planned | The web application`.

What is deployed instead is server-rendered HTML with a hand-written stylesheet,
in `web/apps/api/src/console/`. It was built without reading those lines. It
works, it is fast, it escapes by default, and it does not look like the product:
a bare card on an empty page, in a different green from the marketing site, with
none of its type treatment, logo or texture.

## What was wrong tonight, and what was not

Two separate things read as one complaint.

**The pages were unstyled text in a real browser.** Our own middleware set the
API's `content-security-policy` on every response after the route ran, so
`default-src 'none'` with no `style-src` blocked `console.css`. Fixed, deployed,
and confirmed against the live origin. That was a bug.

**The design is a stopgap.** Fixing the header does not make it the product.
That is not a bug; it is the wrong thing having been built.

## Where a Next.js console has to be served, and why

Not a preference. The session is a cookie set by `app.dev.antifailure.dev`.

Serving the console from `antifailure.dev` would make every data call
cross-origin, which needs `SameSite=None` on the session cookie and a CORS
policy that allows credentials. That widens the cross-site request surface for
every endpoint on the control plane at once, to move a dashboard onto a
different hostname. The CSRF header stops being worth much at the same time.

So the console is served by the control plane, on its own origin.

## The shape that fits

A **static export** of a Next.js app, served by the existing Hono process.

- `output: "export"` produces HTML and JS with no server, so the container keeps
  one process and one place where a session is read.
- Nothing here needs server rendering: every page is behind a session and reads
  tenant-scoped data, so the HTML is identical for everybody and only the data
  differs.
- Data comes from the tRPC surface that already exists and is already tested:
  environments, runs, verdicts, artifacts, network decisions, masking
  attestations, audit, members, org status, tokens. Twenty-eight procedures.
  Same origin, so `credentials: "same-origin"` and the CSRF header both work
  unchanged.
- The design system is `www/app/globals.css`: the same tokens, the same Geist
  and Inter, the same Tailwind 4. One product, one look.

The work is: the app itself, a build stage in
`deploy/docker/control-plane.Dockerfile`, and a static handler in the Hono app
to serve `out/`. The server-rendered pages come out when the replacement covers
them, not before.

## Why this is a note and not a branch

I scaffolded it and deleted the scaffold. Half a Next.js app with no pages wired
to anything is the dead code this repository has a rule about, and I did not
have the room left to finish it properly in one go. The analysis above is the
part worth keeping; the config files take ten minutes to write again.
