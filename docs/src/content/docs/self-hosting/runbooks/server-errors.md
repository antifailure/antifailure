---
title: Server errors
description: The application answered 5xx more than it should have in five minutes.
sidebar:
  order: 11
---

**Alert:** `server-errors`. **Severity 1.** Requests are failing and customers
can see it.

More than ten responses in the `5xx` category in a five minute window, counted
by the Container Apps ingress rather than by the application.

## Why a count and not a rate

A metric alert reads one series. It cannot divide server errors by total
requests, so a true error rate would need a log alert, which costs 1.50 USD a
month per rule and arrives five minutes later than the metric. The threshold is
therefore an absolute count, and it is a number to revisit once real traffic
exists: ten errors is a lot on a quiet service and nothing on a busy one.

## Read this before tuning the threshold

**An unknown path answers 404, and it used to answer 500.** The rate limit guard
runs before routing, so for a long time it could not tell a path the router has
never heard of from a route that exists with no declared limit, and it answered
both with a 500. Every scanner probing `/wp-login.php` on a public name landed
in this metric.

Measured on staging over 36 hours while that was still true: 457 responses in
the `2xx` category and **293 in `5xx`**. That is roughly eight an hour, well
under ten in five minutes, so the alert already had about fifteen times the
headroom it needed, and almost all of that 293 was scanning rather than
failure. Expect the `5xx` count to fall to close to nothing now that a probe
gets a 404, which makes this alert far sharper than the measurement above
suggests: treat a burst of it as real.

The one case that still answers 500 is a route that **exists** and has no entry
in `ENDPOINT_LIMITS`. That is deliberate, it is a bug in this server rather than
in the caller, and the log line beside it names the route to declare. It cannot
reach production without a test failing first, so seeing one means looking at
the most recent deploy.

## What to look at

The application counts its own requests by route, which is the breakdown Azure
does not have:

```sh
curl -s https://app.antifailure.dev/metrics | grep af_http_requests_total
```

**One route failing** is a bug in that handler. It can usually wait for morning
behind a traffic shift to the previous revision.

**Every route failing** is the database, the pool, or a deploy. Check
`/readyz` first, because a 503 there names the reason.

**Only `/webhooks/github` failing** is the GitHub App. A missing private key or
webhook secret makes that endpoint refuse every delivery, and GitHub retries,
which is why one broken credential produces a steady stream rather than a
spike.

Split the Azure metric by status code when the application's own counters
disagree with it, because a 5xx produced by the ingress never reaches the
application at all:

```sh
az monitor metrics list -g af-cp-prod-centralus \
  --resource afcpprod-app --resource-type Microsoft.App/containerApps \
  --metric Requests --filter "statusCode eq '*'" --interval PT5M -o table
```

## What not to do

**Do not restart the app first.** A restart destroys the state that explains the
failure and fixes nothing that is not a leak. Read `/readyz` and the metrics
before touching anything.

**Do not raise the threshold to silence it.** If ten errors in five minutes is
normal traffic for this service, that is the fact to record in
`infra/terraform/stacks/control-plane/production.tfvars`, with the number that
made it true.
