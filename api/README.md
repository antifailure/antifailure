# The site API

The backend for one form. `antifailure.dev/api` is a Static Web Apps managed
function app with two functions in it, and it is a helper for the marketing
site rather than a product. The product's API is the control plane's, on its
own host; `docs/src/content/docs/reference/api.md` says which is which and is
the page a reader lands on.

| Function | Route | What it is |
| --- | --- | --- |
| `waitlist` | `POST /api/waitlist` | Stores one address. Idempotent on the address, and there is no read path. |
| `index` | `GET /api/{*path}` | The index at `/api`, and a `404` that says so for anything else. |

## Where a signup goes

`af-wl-cus`, a Cosmos DB account with the Table API enabled, in the `af-web`
resource group. The table is called `waitlist`. The function reaches it through
`WAITLIST_TABLE_CONNECTION`, which is an application setting on the `af-site`
static web app rather than anything in this repository.

Nothing in this repository reads the list back, and nothing should: an
anonymous endpoint that can enumerate its own signups is how a waitlist becomes
a leaked mailing list. Somebody who already has access to the subscription
reads it with the Azure CLI.

    az cosmosdb table list -a af-wl-cus -g af-web

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
