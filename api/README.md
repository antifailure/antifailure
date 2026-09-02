# The site API

The small public backend for `antifailure.dev`. It is a Static Web Apps managed
function app and a discovery layer, not the product API. The product API is the
control plane's, on its own host; `docs/src/content/docs/reference/api.md` says
which is which.

| Function | Route | What it is |
| --- | --- | --- |
| `index` | `GET /api/{*path}` | The index at `/api`, and a `404` that says so for anything else. |

One function, and it accepts nothing. There was a `waitlist` here that stored
one address per person; signing up is now a GitHub exchange against the control
plane at `app.antifailure.dev`, which has a session, a database and an audit
chain, so this host has nothing left to accept. `endpoints` in the index is an
empty array rather than a missing field, because the question somebody typing
`/api` is asking is what this host offers a machine, and "nothing, and the
product's API is over there" is an answer.

Every refusal is JSON with a stable `code`, a human `message`, and a
`resolution`, built from `shared/errors.js`. That table is published by
`GET /api`, so a caller can resolve a code it received from the host that sent
it, and `test/errors.test.js` reads this directory's source and fails on a code
with no entry.

These codes are not in the product's `AF-<AREA>-<NNN>` namespace and should not
be. This answers for the marketing host, not for the product; the product's
catalog is at `/errors.v1.json`, and none of these situations is one the engine
can recover from by looking a product error up.

The catalog is down to one entry. Three went with the waitlist, and the test
that names a catalog entry no code path can return is what took them: an entry
nothing emits is published at `GET /api` as a description of a situation this
app cannot be in.

`antifailure.dev/openapi.json` and `antifailure.dev/errors.v1.json` are static
files this app does not serve. They are generated at build time and published
with the site: see `web/apps/api/scripts/openapi.ts` for why the OpenAPI
document is a committed artifact rather than a proxy of the live control plane.

## Where a signup goes

The control plane, at `app.antifailure.dev`, and nothing on this host is
involved. `GET /auth/github` there starts the exchange, the callback writes the
user and the organization, and `docs/src/content/docs/reference/control-plane.md`
describes the variables that decide who may sign in and what they land in.

There used to be a Cosmos DB account here holding a waitlist table, reachable
only by somebody with the subscription, that nothing ever mailed. It has no
reader in this repository and no writer either now. Deleting the Azure resource
is a decision about data somebody left with us rather than a code change, so it
is not done here.

## Running it

The Azure Functions Core Tools, and a `local.settings.json` that git ignores:

    {
      "IsEncrypted": false,
      "Values": {
        "FUNCTIONS_WORKER_RUNTIME": "node",
        "AzureWebJobsStorage": ""
      }
    }

Then `npm ci && func start`. The route the host prints is the catch-all,
`{*path}`, narrowed to `GET` and `HEAD`. That narrowing is the thing to keep:
a catch-all that accepted `POST` would answer, with a cheerful 404 body, for
any endpoint somebody adds under `/api` and forgets to route, and the caller
would see a submission that silently did nothing.

`npm test` covers what the index answers, what a path nothing serves answers,
and that every code the source can emit is in the published catalog and every
entry in the catalog is one the source can emit. It needs no network and no
clock.

## Deploying it

`.github/workflows/deploy.yml`, which passes `api_location: api` with
`skip_api_build`, and `tools/site/assemble.sh`, which writes the
`platform.apiRuntime` the platform then needs to start the function on. The two
have to agree. When they did not, which was for the first two days this
existed, `api_location` was absent entirely: every deploy published a site with
no API, the platform removed the function app that a hand deploy had put there,
and every path under `/api` answered `500` while the deploy stayed green.
