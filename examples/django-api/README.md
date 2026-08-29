# Django orders

A Django application against the same schema as [`../go-api`](../go-api) and
[`../next-app`](../next-app), and the third distinct shape of the three.

- `go-api` is a compiled binary that runs a SQL file to build its schema
- `next-app` is a framework with a build step and server rendered pages
- this one is a framework whose own migration system owns the schema

That last difference is the point of having it. The manifest's migrate command
is `python manage.py migrate --noinput`. There is no `psql` in this image and
no SQL file to point one at: the engine runs whatever the framework already
uses, against the branch rather than the golden, so a pull request that adds a
field gets the column and nobody else's environment does.

Three endpoints, because three is enough to have a bug worth catching:

    GET  /health      answers only once the database answers
    GET  /customers   the aggregate that crosses the foreign key
    POST /orders      the write the invariant is about

## Run it

From this directory, with Docker running:

```sh
af up
```

That builds the image, branches a Postgres database from a masked golden, runs
Django's migrations against the branch, seals the network, and starts the
service.

```sh
URL="$(af status -o json | jq -r .url)"
curl "$URL/customers"
af down
```

## What to look at, and why

**Your migrations, not ours.** Nothing here is written twice. The schema lives
in `orders/models.py`, Django generates `orders/migrations/`, and the manifest
runs the command a Django developer already types. Antifailure's contribution
is what that command runs against: a branch of a masked copy, thrown away with
the environment.

**The seed is a data migration, not a fixture.** `0002_seed.py` runs by the
same command as the schema, so there is no second step to forget and no
`loaddata` in the manifest. It is reversible, because a migration nobody dares
run twice is a migration nobody runs.

**The health path is the readiness contract.** `/health` runs `SELECT 1` and
returns 503 until that works. The manifest names it and the engine waits for
it, so `ready` means the application can serve rather than that a port is
open. A health check that only proves a process is listening reports ready and
then serves a stack trace.

**The join is what the masking has to survive.** `customers.id` and
`orders.customer_id` both carry `link: customer` in `masking.yaml`, so both are
remapped to the same new value. Without the link, `GET /customers` would return
every customer with `spent_cents` of zero: not an error, not an empty
response, just quietly wrong numbers. That is the failure masking rules exist
to prevent, and it is why the interesting query in each of these examples is
the one that crosses a foreign key.

**`ALLOWED_HOSTS` is `["*"]`, and that is the one line here not to copy.** The
engine reaches the service through the ingress forwarder, so the host header is
not predictable. It is safe here because the environment's entire network is
sealed by the proxy and nothing else can reach the service at all. In
production it is not, and `config/settings.py` says so where it is set.

**The egress default is `block` and there are no rules.** This application
calls nothing and the policy says so, rather than leaving a door open in case
it one day does. For a rule that is used, read `../go-api`, which takes a
payment through `api.stripe.com` answered by the pack that ships with the
engine.
