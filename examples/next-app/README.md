# Next.js orders

A Next.js application against the same schema as [`../go-api`](../go-api), and
deliberately a different shape. That one is a compiled binary with three
endpoints. This one has a framework, a build step, and server rendered pages,
which brings the two problems those always bring:

- the build must not need a database, because it runs inside the image where
  there is not one yet
- the runtime must, and must say so before it is called ready

One page and one route, because that is enough to have both problems:

    GET  /              the customers and what each has spent, rendered per request
    GET  /api/health    answers only once the database answers

## Run it

From this directory, with Docker running:

```sh
af up
```

That builds the image, branches a Postgres database from a masked golden, runs
the migration against the branch, seals the network, and starts the service.

```sh
open "$(af status -o json | jq -r .url)"
af down
```

## What to look at, and why

**The build does not touch the database, and that is arranged rather than
lucky.** Two lines do it. `app/page.tsx` sets `export const dynamic =
"force-dynamic"`, so Next renders it per request instead of trying to prerender
it during `next build`. `lib/db.ts` creates the pool on first use rather than
at import time, so importing the module during the build does not open a
connection. Without either one the image fails to build, with a connection
error that reads like a configuration problem and is not one.

**The health path is the readiness contract.** `/api/health` runs `SELECT 1`
and returns 503 until that works. The manifest names it, and the engine waits
for it, so `ready` means the page will render rather than that a process is
listening. A service whose health check only proves a port is open reports
ready and then serves a stack trace.

**Standalone output needs its static files copied separately.** `next.config.ts`
sets `output: "standalone"`, which produces a server and a pruned
`node_modules`. The static assets are not in it. The Dockerfile copies
`.next/static` in a second `COPY`, and leaving that step out is the classic
mistake: the page renders, arrives with no CSS, and looks like a styling bug.

**`HOSTNAME` is set to `0.0.0.0` on purpose.** The standalone server binds to
whatever `HOSTNAME` says. Left unset it has bound to localhost in some
versions, which inside a container means the port is open and nothing outside
can reach it. The symptom is a service that starts cleanly and never becomes
ready.

**The join is what the masking has to survive.** `customers.id` and
`orders.customer_id` both carry `link: customer` in `masking.yaml`, so both are
remapped to the same new value. Without the link, every customer would render
with zero orders: not an error, not an empty page, just quietly wrong numbers.
That is the failure mode masking rules are for.

**The egress default is `block` and there are no rules.** This application
calls nothing, and the policy says so rather than leaving a door open in case
it one day does. For a rule that is actually used, read `../go-api`, which
takes a payment through `api.stripe.com` answered by the pack that ships with
the engine.
