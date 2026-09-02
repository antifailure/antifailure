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

**An unknown path answers 500 rather than 404 today**, because the rate limit
guard runs before routing and reports "no declared rate limit" for a path the
router has never heard of. So every scanner probing `/wp-login.php` on a public
name lands in this metric.

Measured on staging over 36 hours rather than guessed: 457 responses in the
`2xx` category and **293 in `5xx`**. That is roughly eight an hour, well under
ten in five minutes, so the alert has about fifteen times the headroom it needs.
Production is on a name that will attract more scanning than staging's, so watch
the first week before deciding the threshold is right.

The real fix is for an undeclared path to answer 404. Until it does, a burst of
this alert with no customer complaining is worth checking against the route
breakdown below before anybody is woken.

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
