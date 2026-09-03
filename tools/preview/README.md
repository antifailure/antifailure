# Looking at the console

The console is a Next static export with no server of its own, and every screen
in it is a fetch against the control plane. Opened on its own it renders its
error state. The operator portal underneath is worse: `admin_users` rows are
written only by `admin.operators.create`, which itself needs an operator
session, so a fresh database has nobody who can create the first operator. That
is correct for a shipped installation, whose password is provisioned at deploy,
and it means there was no way to look at any of these pages.

This is the local way through it. Two scripts.

## Run it

```
tools/preview/up.sh          # database, seed, console build, control plane
tools/preview/shots.sh       # every operator page at 320 and 1440
tools/preview/down.sh        # stop it and remove its database
```

`up.sh` prints the address and the sign-in when it finishes. It takes a few
minutes the first time, mostly `npm ci` and the console build. Afterwards
`AF_PREVIEW_SKIP_BUILD=1 tools/preview/up.sh` reuses the export and takes about
a minute.

## What you get

A control plane on a port derived from this checkout's path, serving the console
from its own process at `/` and the operator portal at `/admin`. The origin is
shared on purpose: the session is a SameSite cookie on the control plane, as
`console/next.config.ts` explains, so a console served from a second port would
not keep it.

The database holds real rows in real tables, never numbers typed into a page.
Sixty three organizations, so the customers list needs a second page and paging
can be seen working, with members, repositories and environments each. Users,
runs, verdicts, artifacts, events, masking rules, network rules and a tenant
audit chain, from the staging seeder the repository already had. On top of that
eleven operators including one suspended and one never provisioned, ten feature
flags across all three states with one killed, and sixty operator audit entries
appended through the real chain so the log verifies rather than merely existing.

## The credential is local only

The seeded operator is `operator@preview.local` with the password
`preview-only-not-a-secret`. It is written down in `seed.ts`, printed by `up.sh`
and committed to this repository on purpose, because a password every agent has
to be told separately is a password nobody uses. What makes writing it down safe
is that both scripts refuse to run against anything but localhost: `up.sh`
checks `AF_PREVIEW_HOST` and `seed.ts` checks the hostname of the database URL
and exits naming the host. This is not the operator bootstrap a real deployment
needs and must never be mistaken for one.

## The screenshots

`shots.sh` writes `.preview/shots/<route>@<width>.png` for every entry in
`console/lib/admin-nav.ts`, which is the file the portal's own navigation is
built from, so a section added by any lane is photographed the next time this
runs. On a branch where that file does not exist yet it is read from
`origin/w-admin-shell`.

It signs in over HTTP and gives the browser the cookie, so the pictures are of
pages rather than of a sign-in screen, and it exits non zero when any of these
is true:

- the page scrolls horizontally, naming the widest element that causes it
- the page never rendered its own name from the navigation, which is how a
  loading state, an error state and a blank screen are all caught at once
- a skeleton was still on screen when the picture was taken
- the browser was not signed in
- the route is not in this build, which is expected on a branch where the
  section has not landed and is allowed by `AF_PREVIEW_ALLOW_MISSING=1`

Overflow is measured with `document.scrollWidth` against `window.innerWidth`
inside the page, over the DevTools protocol, rather than from the pixel width of
a file. The browser is the headless Chromium already in the Playwright cache;
set `AF_PREVIEW_CHROME` to use another. A page taller than 6000 pixels is
photographed down to that and its true height is recorded in
`.preview/shots/shots.json`.

## Knobs

| Variable | Default |
| --- | --- |
| `AF_PREVIEW_PORT` | derived from the checkout path |
| `AF_PREVIEW_DB_PORT` | derived from the checkout path |
| `AF_PREVIEW_DB_CONTAINER` | `af-preview-<hash of the checkout path>` |
| `AF_PREVIEW_SCALE` | `3`, the multiplier on the tenant seeder |
| `AF_PREVIEW_SKIP_BUILD` | unset, `1` keeps the existing export |
| `AF_PREVIEW_WIDTHS` | `320,1440` |
| `AF_PREVIEW_URL` | read from `.preview/url` |

The database and both ports are derived from the checkout's path so that six
worktrees can run this at once. Sharing one would mean sharing a schema, and two
branches with an unmerged migration of the same number meeting in one database
fails in a way that reads as a bug in the schema rather than as two branches
meeting. `down.sh` removes only a container carrying the `af-preview=1` label
that `up.sh` puts on the ones it creates.
