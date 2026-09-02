# The site API

The small public backend for `antifailure.dev`. It is a Static Web Apps managed
function app and a discovery layer, not the product API. The product API is the
control plane's, on its own host; `docs/src/content/docs/reference/api.md` says
which is which.

| Function | Route | What it is |
| --- | --- | --- |
| `waitlist` | `POST /api/waitlist` | Stores one address. Idempotent on the address, and there is no read path. |
| `index` | `GET /api/{*path}` | The index at `/api`, and a `404` that says so for anything else. |

Every refusal is JSON with a stable `code`, a human `message`, and a
`resolution`, built from `shared/errors.js`. That table is published by
`GET /api`, so a caller can resolve a code it received from the host that sent
it, and `test/errors.test.js` reads this directory's source and fails on a code
with no entry.

These codes are not in the product's `AF-<AREA>-<NNN>` namespace and should not
be. This is the marketing site's form backend; the product's catalog is at
`/errors.v1.json`, and none of these situations is one the engine can recover
from by looking a product error up.

`antifailure.dev/openapi.json` and `antifailure.dev/errors.v1.json` are static
files this app does not serve. They are generated at build time and published
with the site: see `web/apps/api/scripts/openapi.ts` for why the OpenAPI
document is a committed artifact rather than a proxy of the live control plane.

## Where a signup goes

`af-wl-cus`, a Cosmos DB account with the Table API enabled, in the `af-web`
resource group. The table is called `waitlist`. The function reaches it through
`WAITLIST_TABLE_CONNECTION`, which is an application setting on the `af-site`
static web app rather than anything in this repository.

Nothing in this repository reads the list back, and nothing should: an
anonymous endpoint that can enumerate its own signups is how a waitlist becomes
a leaked mailing list. Somebody who already has access to the subscription
reads it with the Azure CLI, which confirms the table exists:

    az cosmosdb table list -a af-wl-cus -g af-web

The rows themselves are data plane rather than control plane, so reading them
needs the account key and a client that speaks the Table API. There is no
tooling here for that on purpose. Whoever needs the list has the subscription,
and a script in this repository that reads addresses is a script somebody runs
by accident.

One row in that table is not a person. `waitlist-probe@antifailure.dev` is
written by `.github/workflows/waitlist.yml` every morning and left there,
because that check is the only thing that can tell anybody a signup is reaching
storage. Do not count it, and do not email it.

## Running it

The Azure Functions Core Tools, and a `local.settings.json` that git ignores:

    {
      "IsEncrypted": false,
      "Values": {
        "FUNCTIONS_WORKER_RUNTIME": "node",
        "AzureWebJobsStorage": "",
        "WAITLIST_TABLE_CONNECTION": "<the table connection string>"
      }
    }

Then `npm ci && func start`. The host prints both routes as it registers them,
which is worth reading: the catch-all is `{*path}` and the waitlist is a
literal, and a router that ever resolved those the other way round would turn
every signup into a `404`. The bindings narrow the catch-all to `GET` and
`HEAD` for that reason, so it cannot take a `POST` even if precedence changed.

`npm test` covers the parts that are easy to get wrong and impossible to see:
what counts as an address, when the rate limiter trips, and what each storage
failure answers. It needs no network and no clock.

## Deploying it

`.github/workflows/deploy.yml`, which passes `api_location: api` with
`skip_api_build`, and `tools/site/assemble.sh`, which writes the
`platform.apiRuntime` the platform then needs to start the function on. The two
have to agree. When they did not, which was for the first two days this
existed, `api_location` was absent entirely: every deploy published a site with
no API, the platform removed the function app that a hand deploy had put there,
and every path under `/api` answered `500` while the deploy stayed green.
