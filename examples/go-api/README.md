# A Go API on Postgres

The smallest example that uses the whole configuration surface. Two tables
with a foreign key between them, and three endpoints. The manifest names what
is masked, what the environment may reach, who the agents log in as, what they
do, and what must be true afterwards.

Three endpoints, because three is enough to have a bug worth catching:

    GET  /health      answers only once the database answers
    GET  /customers   the read that the join has to survive
    POST /orders      the write that the invariant is about, and the one
                      outbound call, which goes to Stripe through the proxy

## Run it

From this directory, with Docker running:

```sh
af up
```

That builds the image, branches a Postgres database from a masked golden, runs
the migration against the branch, seals the network, and starts the service.
`af up --hud` draws the same run as a dashboard.

```sh
URL="$(af status -o json | jq -r .url)"
curl "$URL/customers"
```

Placing an order takes a payment, and that payment is the outbound call the
egress rule is about:

```sh
curl -X POST "$URL/orders" -H 'Content-Type: application/json' \
  -d '{"customer_id":1,"total_cents":4999}'
```

```json
{"id":2,"customer_id":1,"total_cents":4999,"placed_at":"2026-08-28T13:17:11.613982Z","payment_intent":"pi_mock00000000000001"}
```

No Stripe account, no key, no network. Read what happened:

```sh
af net log
```

```
TIME      MODE  REQUEST                                         RULE            OUTCOME
08:17:11  mock  CONNECT https://api.stripe.com                  api.stripe.com  200
08:17:11  mock  POST https://api.stripe.com/v1/payment_intents  api.stripe.com  200, 350 B
```

```sh
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

**The egress rule answers from a mock.** `api.stripe.com` is the one host this
example names, and everything else is refused. It is set to `mock` so the
example runs with nothing configured: the Stripe pack ships with the engine and
answers from recorded responses.

`POST /orders` actually makes that call, over ordinary HTTPS, with an ordinary
`http.Client` on the default transport. Nothing in `main.go` imports
Antifailure or knows it is running inside a disposable environment; the service
reaches the sidecar because the environment sets the proxy variables and
because the network gives a service that ignores them nowhere else to send the
packet. A rule written as a mock is therefore not a description of what would
happen. It is what happened, and `af net log` has the line.

Two lines turn it into the stronger thing once you have a test key:

```yaml
mode: sandbox
credential: STRIPE_SECRET_KEY
```

Then the container is given a placeholder and the proxy substitutes the real
key on the way out. The payment path runs, and the key is never inside the
environment. It cannot be logged, dumped, or read out of a container somebody
left running.

**The invariants are the assertions the API cannot make.** `no-orphaned-orders`
asks a question about the database rather than about a response. It is the one
that goes red if the masking breaks the join.

**The build declares its hosts.** `proxy.golang.org` is named because the build
fetches from it. That list is declared rather than enforced in this release:
the engine validates it and shows it in `af explain`, and the local builder
does not yet seal a build. Write it as the record of what your build needs.

## What it deliberately does not have

No framework, no ORM, no configuration library. Everything in `main.go` is
there because the manifest beside it refers to it, so reading one explains the
other. A real application has more. It does not need more to show what
Antifailure does with it.
