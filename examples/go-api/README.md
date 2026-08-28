# A Go API on Postgres

The smallest example that uses the whole configuration surface. Two tables
with a foreign key between them, and three endpoints. The manifest names what
is masked, what the environment may reach, who the agents log in as, what they
do, and what must be true afterwards.

Three endpoints, because three is enough to have a bug worth catching:

    GET  /health      answers only once the database answers
    GET  /customers   the read that the join has to survive
    POST /orders      the write that the invariant is about

## Run it

From this directory, with Docker running:

```sh
af up
```

That builds the image, branches a Postgres database from a masked golden, runs
the migration against the branch, seals the network, and starts the service.
`af up --hud` draws the same run as a dashboard.

```sh
curl "$(af status -o json | jq -r .url)/customers"
af down
```

## What to look at, and why

**`masking.yaml` is the interesting file.** `customers.id` and
`orders.customer_id` are both remapped. Both carry `link: customer`, and that
is what makes them mask to the same new value.

Remove one of those `link` lines. Every order then belongs to nobody:
`/customers` still answers, the join returns nothing, and the failure looks
like an application bug. That is the mistake `link` exists to prevent, and it
is worth making once on purpose.

Two columns are marked `preserve` rather than left out. A column nobody has
classified defaults to `nullify`, so "reviewed and safe" and "not looked at
yet" are different statements rather than the same silence.

**The egress rule is a sandbox, not an allow.** The container is given a
placeholder in `STRIPE_SECRET_KEY`. The proxy substitutes the real test-mode
key on the way out.

So the payment path runs, and the key is never inside the environment. It
cannot be logged, dumped, or read out of a container somebody left running.

**The invariants are the assertions the API cannot make.** `no-orphaned-orders`
asks a question about the database rather than about a response. It is the one
that goes red if the masking breaks the join.

**The build declares its hosts.** A build runs under the same default deny as
the environment. `proxy.golang.org` is named because the build fetches from it,
and nothing else is reachable, at build time or after.

## What it deliberately does not have

No framework, no ORM, no configuration library. Everything in `main.go` is
there because the manifest beside it refers to it, so reading one explains the
other. A real application has more. It does not need more to show what
Antifailure does with it.
